package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
)

func TestUnmanagedSummariesUseStableCursorAndIndexedSearch(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	ctx := context.Background()
	for index := 0; index < 120; index++ {
		candidateID := fmt.Sprintf("cursor-%03d", index)
		pathBase := fmt.Sprintf("Album %03d", index)
		if index == 18 {
			pathBase = "Folder Needle"
		}
		candidate, err := domain.CreateCandidate(domain.NewCandidate{ID: candidateID, Source: domain.SourceManual, ReleaseDirectory: filepath.Join(t.TempDir(), "Artist", pathBase), ConfigSnapshotID: "config-1", AcquisitionEvidenceID: "manual:" + candidateID, Now: now})
		if err != nil || repository.CreateCandidate(ctx, candidate) != nil {
			t.Fatalf("candidate %s: %v", candidateID, err)
		}
		artist := "Shared Artist"
		if index == 17 {
			artist = "Needle Artist"
		}
		release := integrationMigrationRelease(candidateID, artist, fmt.Sprintf("Album %03d", index), candidate.ReleaseDirectory, "fingerprint-"+candidateID, now)
		if err := repository.PutUnmanagedRelease(ctx, release, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE unmanaged_releases SET status='PREPARED' WHERE candidate_id='cursor-119'`); err != nil {
		t.Fatal(err)
	}

	first, next, err := repository.UnmanagedSummaries(ctx, application.UnmanagedFilter{}, 50, "")
	if err != nil || len(first) != 50 || next == "" {
		t.Fatalf("first page len=%d next=%q err=%v", len(first), next, err)
	}
	second, next2, err := repository.UnmanagedSummaries(ctx, application.UnmanagedFilter{}, 50, next)
	if err != nil || len(second) != 50 || next2 == "" {
		t.Fatalf("second page len=%d next=%q err=%v", len(second), next2, err)
	}
	seen := make(map[string]bool, 100)
	for _, item := range append(first, second...) {
		if seen[item.CandidateID] {
			t.Fatalf("duplicate candidate across cursors: %s", item.CandidateID)
		}
		seen[item.CandidateID] = true
	}

	for name, filter := range map[string]application.UnmanagedFilter{
		"artist": {Query: "needle"},
		"album":  {Query: "album 017"},
		"path":   {Query: "folder needle"},
		"status": {Status: "PREPARED"},
	} {
		items, _, err := repository.UnmanagedSummaries(ctx, filter, 50, "")
		if err != nil || len(items) != 1 || items[0].CandidateID != map[string]string{"artist": "cursor-017", "album": "cursor-017", "path": "cursor-018", "status": "cursor-119"}[name] {
			t.Fatalf("%s search items=%+v err=%v", name, items, err)
		}
	}
	if _, _, err := repository.UnmanagedSummaries(ctx, application.UnmanagedFilter{Query: "x"}, 50, ""); err == nil {
		t.Fatal("one-character unmanaged search was accepted")
	}

	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT candidate_id FROM unmanaged_releases WHERE NOT EXISTS(SELECT 1 FROM migration_items mi WHERE mi.unmanaged_candidate_id=unmanaged_releases.candidate_id AND mi.state='MIGRATED') AND status=? ORDER BY updated_at DESC,candidate_id DESC LIMIT ?`, "IMPORTED", 50)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "unmanaged_status_updated") {
		t.Fatalf("status page query plan does not use unmanaged_status_updated: %s", plan.String())
	}
}

func TestMigrationFailureBeforeManualImportRestoresExactUnmanagedManifest(t *testing.T) {
	scenario := newMigrationScenario(t, migrationImportModePrepareFailure)
	before, err := media.SHA256(scenario.track)
	if err != nil {
		t.Fatal(err)
	}
	if err := scenario.service.Process(context.Background(), scenario.itemID); err == nil {
		t.Fatal("prepare failure ignored")
	}
	after, err := media.SHA256(scenario.track)
	if err != nil || before != after {
		t.Fatalf("rollback hash before=%s after=%s err=%v", before, after, err)
	}
	item, _ := scenario.repository.MigrationItem(context.Background(), scenario.itemID)
	if item.State != domain.MigrationFailedRetryable || scenario.importer.submits != 0 || scenario.mutation.restores != 1 {
		t.Fatalf("rollback item=%+v submits=%d restores=%d", item, scenario.importer.submits, scenario.mutation.restores)
	}
}

func TestMigrationLostAcknowledgementAndPartialImportNeverSubmitOrCopyTwice(t *testing.T) {
	scenario := newMigrationScenario(t, migrationImportModeLostAcknowledgement)
	if err := scenario.service.Process(context.Background(), scenario.itemID); err != nil {
		t.Fatal(err)
	}
	item, _ := scenario.repository.MigrationItem(context.Background(), scenario.itemID)
	if item.State != domain.MigrationReconciling || scenario.importer.submits != 1 {
		t.Fatalf("lost acknowledgement item=%+v submits=%d", item, scenario.importer.submits)
	}
	if _, err := os.Stat(filepath.Dir(scenario.track)); !os.IsNotExist(err) {
		t.Fatalf("second unmanaged copy retained: %v", err)
	}
	if err := scenario.service.Process(context.Background(), scenario.itemID); err != nil {
		t.Fatal(err)
	}
	item, _ = scenario.repository.MigrationItem(context.Background(), scenario.itemID)
	if item.State != domain.MigrationReconciling || scenario.importer.submits != 1 {
		t.Fatalf("partial retry item=%+v submits=%d", item, scenario.importer.submits)
	}
}

func TestMigrationCompleteRequiresManagedVisibleAndUnmanagedAbsent(t *testing.T) {
	scenario := newMigrationScenario(t, migrationImportModeComplete)
	if err := scenario.service.Process(context.Background(), scenario.itemID); err != nil {
		t.Fatal(err)
	}
	item, _ := scenario.repository.MigrationItem(context.Background(), scenario.itemID)
	if item.State != domain.MigrationMigrated || scenario.importer.submits != 1 || scenario.nav.scans != 1 || scenario.mutation.cleanups != 1 {
		t.Fatalf("complete item=%+v submits=%d nav=%+v mutation=%+v", item, scenario.importer.submits, scenario.nav, scenario.mutation)
	}
}

type migrationImportMode int

const (
	migrationImportModePrepareFailure migrationImportMode = iota
	migrationImportModeLostAcknowledgement
	migrationImportModeComplete
)

type integrationMigrationScenario struct {
	repository *persistence.Repositories
	service    application.MigrationService
	itemID     string
	track      string
	importer   *migrationImporter
	mutation   *integrationMigrationMutation
	nav        *integrationMigrationNav
}

func newMigrationScenario(t *testing.T, mode migrationImportMode) integrationMigrationScenario {
	t.Helper()
	db, repository, now := pipelineRepositories(t)
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	unmanagedRoot, approvedRoot := filepath.Join(root, "unmanaged"), filepath.Join(root, "approved")
	candidateID := "migration-candidate"
	finalPath := filepath.Join(unmanagedRoot, "Kaleb J", "OFF GUARD")
	if err := os.MkdirAll(finalPath, 0o750); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(finalPath, "01 - Track.flac")
	if err := os.WriteFile(track, []byte("original migration bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	candidate, err := domain.CreateCandidate(domain.NewCandidate{ID: candidateID, Source: domain.SourceManual, ReleaseDirectory: finalPath, ConfigSnapshotID: "config-1", AcquisitionEvidenceID: "manual:" + candidateID, Now: now})
	if err != nil || repository.CreateCandidate(context.Background(), candidate) != nil {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	metadata := domain.MetadataPlan{AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2026", DiscTotal: 1, TrackTotal: 1, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Track", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1}}}
	release := domain.UnmanagedRelease{CandidateID: candidateID, FinalPath: finalPath, Status: "IMPORTED", Plan: domain.UnmanagedPlan{CandidateID: candidateID, Metadata: metadata, Files: []domain.PlannedFile{{SourceRelative: "01.flac", TargetRelative: "01 - Track.flac", Kind: "FLAC"}}}, Evidence: domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{{RelativePath: "01.flac", Info: domain.TechnicalInfo{DurationMS: 1}}}}, CreatedAt: now, UpdatedAt: now}
	if err := repository.PutUnmanagedRelease(context.Background(), release, now); err != nil {
		t.Fatal(err)
	}
	identity := exactMigrationIdentity{}
	checks := application.MigrationCheckService{Store: repository, Identity: identity, Now: func() time.Time { return now }}
	_, items, err := checks.CreateBatch(context.Background(), application.Selection{ReleaseIDs: []string{candidateID}, Revisions: migrationRevisions(candidateID)}, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	checked, err := checks.CheckItem(context.Background(), items[0].ID)
	if err != nil || checked.State != domain.MigrationExactMatch {
		t.Fatalf("check=%+v err=%v", checked, err)
	}
	mutation := &integrationMigrationMutation{}
	importer := &migrationImporter{mode: mode}
	verifier := migrationVerifier{complete: mode == migrationImportModeComplete}
	nav := &integrationMigrationNav{}
	imports := application.ImportService{WorkRoot: filepath.Join(root, "unused-work"), ApprovedRoot: approvedRoot, Configuration: passingConfig{}, Importer: importer, Verifier: verifier, Store: repository, Now: func() time.Time { return now }}
	service := application.MigrationService{Store: repository, Identity: identity, Catalog: application.LidarrCatalogService{Catalog: migrationCatalog{}}, Mutation: mutation, Import: imports, Navidrome: nav, UnmanagedRoot: unmanagedRoot, ApprovedRoot: approvedRoot, ScanPoll: time.Millisecond, Now: func() time.Time { return now }}
	if err := service.ConfirmSelected(context.Background(), []application.ConfirmedSelection{{ItemID: checked.ID, ExpectedRevision: checked.StateRevision, ReleaseMBID: releaseMBID}}, "admin-1"); err != nil {
		t.Fatal(err)
	}
	return integrationMigrationScenario{repository: repository, service: service, itemID: checked.ID, track: track, importer: importer, mutation: mutation, nav: nav}
}

type exactMigrationIdentity struct{}

func (exactMigrationIdentity) Decide(context.Context, domain.MetadataPlan, domain.TechnicalReleaseResult) (application.IdentityDecision, error) {
	const migrationArtistMBID = "55555555-5555-5555-5555-555555555555"
	release := domain.CanonicalRelease{ReleaseMBID: releaseMBID, ReleaseGroupMBID: releaseGroupMBID, Title: "OFF GUARD", Date: "2026", ArtistCredits: []domain.ArtistCredit{{Name: "Kaleb J", ArtistMBID: migrationArtistMBID}}, Tracks: []domain.CanonicalTrack{{ReleaseTrackMBID: releaseTrackMBID, RecordingMBID: recordingMBID, Title: "Track", ArtistCredits: []domain.ArtistCredit{{Name: "Kaleb J", ArtistMBID: migrationArtistMBID}}, Disc: 1, Track: 1}}}
	return application.IdentityDecision{Status: application.IdentityExact, Exact: &application.IdentityCandidate{Release: release}}, nil
}

type migrationCatalog struct{}

func (migrationCatalog) EnsureRelease(context.Context, domain.CanonicalRelease) (application.CatalogResult, error) {
	return application.CatalogResult{ArtistID: 1, AlbumID: 2, AlbumReleaseID: 3}, nil
}

type integrationMigrationMutation struct{ restores, cleanups int }

func (m *integrationMigrationMutation) Apply(_ context.Context, _ string, _ map[string]domain.TagSet) (application.MigrationMutationResult, error) {
	return application.MigrationMutationResult{Files: []application.MigrationMutationFile{{RelativePath: "01 - Track.flac"}}}, nil
}
func (m *integrationMigrationMutation) Restore(context.Context, string, application.MigrationMutationResult) error {
	m.restores++
	return nil
}
func (m *integrationMigrationMutation) Cleanup(string) error { m.cleanups++; return nil }

type migrationImporter struct {
	mode    migrationImportMode
	submits int
}

func (i *migrationImporter) Prepare(_ context.Context, approvedPath, _ string, _ string, _ int) (domain.LidarrImportPlan, error) {
	if i.mode == migrationImportModePrepareFailure {
		return domain.LidarrImportPlan{}, errors.New("prepare failed")
	}
	if _, err := os.Stat(filepath.Join(approvedPath, "01 - Track.flac")); err != nil {
		return domain.LidarrImportPlan{}, err
	}
	return domain.LidarrImportPlan{RequestBody: []byte(`[{"path":"01.flac"}]`), AlbumID: 2, AlbumReleaseID: 3, TrackIDs: []int{4}}, nil
}
func (i *migrationImporter) Submit(context.Context, domain.LidarrImportPlan) error {
	i.submits++
	if i.mode == migrationImportModeLostAcknowledgement {
		return errors.New("connection reset after request")
	}
	return nil
}

type migrationVerifier struct{ complete bool }

func (v migrationVerifier) Verify(context.Context, domain.LidarrImportPlan, domain.ReleaseManifest) (domain.ImportVerification, error) {
	return domain.ImportVerification{Complete: v.complete}, nil
}

type integrationMigrationNav struct{ scans int }

func (n *integrationMigrationNav) EnsureLibraries(context.Context) (int, int, bool, error) {
	return 1, 2, false, nil
}
func (n *integrationMigrationNav) StartScan(context.Context, ...int) error       { n.scans++; return nil }
func (n *integrationMigrationNav) WaitScan(context.Context, time.Duration) error { return nil }
func (n *integrationMigrationNav) ReleaseVisible(_ context.Context, libraryID int, _ navidrome.ReleaseIdentity) (bool, error) {
	return libraryID == 1, nil
}
