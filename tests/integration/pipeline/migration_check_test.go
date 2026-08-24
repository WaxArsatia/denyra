package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
)

func TestMigrationCheckBatchPersistsMixedOutcomesWithoutTouchingUnmanagedFiles(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	root := t.TempDir()
	ids := []string{"release-1", "release-2", "release-3", "release-4"}
	albums := []string{"none", "ambiguous", "exact", "error"}
	artists := []string{"Kaleb J", "Other", "Kaleb J", "Other"}
	before := map[string][2]string{}
	for index, id := range ids {
		candidate, err := domain.CreateCandidate(domain.NewCandidate{ID: id, Source: domain.SourceManual, ReleaseDirectory: filepath.Join(root, id), ConfigSnapshotID: "config-1", AcquisitionEvidenceID: "manual:" + id, Now: now})
		if err != nil || repository.CreateCandidate(context.Background(), candidate) != nil {
			t.Fatalf("create %s: candidate=%+v err=%v", id, candidate, err)
		}
		path := filepath.Join(root, id)
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
		track := filepath.Join(path, "01.flac")
		if err := os.WriteFile(track, []byte("unchanged-"+id), 0o640); err != nil {
			t.Fatal(err)
		}
		tree, err := denyrafs.Scan(path)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := media.SHA256(track)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = [2]string{tree.Fingerprint, hash}
		release := integrationMigrationRelease(id, artists[index], albums[index], path, tree.Fingerprint, now)
		if err := repository.PutUnmanagedRelease(context.Background(), release, now); err != nil {
			t.Fatal(err)
		}
	}

	service := application.MigrationCheckService{Store: repository, Identity: integrationMigrationIdentity{}, Now: func() time.Time { return now }}
	selected, err := service.ResolveSelection(context.Background(), application.Selection{SelectAll: true, Filter: application.UnmanagedFilter{Query: "Kaleb", Status: "IMPORTED"}})
	if err != nil || !slices.Equal(selected, []string{"release-1", "release-3"}) {
		t.Fatalf("filter selection=%v err=%v", selected, err)
	}
	batch, items, err := service.CreateBatch(context.Background(), application.Selection{ReleaseIDs: ids}, "admin-1")
	if err != nil || len(items) != 4 {
		t.Fatalf("batch=%+v items=%d err=%v", batch, len(items), err)
	}
	for _, item := range items {
		_, _ = service.CheckItem(context.Background(), item.ID)
	}

	restarted := persistence.New(db, func() time.Time { return now.Add(time.Hour) })
	stored, err := restarted.MigrationItems(context.Background(), batch.ID)
	if err != nil || len(stored) != 4 {
		t.Fatalf("restarted items=%+v err=%v", stored, err)
	}
	states := make([]domain.MigrationState, 0, len(stored))
	for _, item := range stored {
		states = append(states, item.State)
	}
	sort.Slice(states, func(i, j int) bool { return states[i] < states[j] })
	wantStates := []domain.MigrationState{domain.MigrationAmbiguous, domain.MigrationExactMatch, domain.MigrationFailedRetryable, domain.MigrationNoMatch}
	sort.Slice(wantStates, func(i, j int) bool { return wantStates[i] < wantStates[j] })
	if !slices.Equal(states, wantStates) {
		t.Fatalf("states=%v want=%v", states, wantStates)
	}
	errorsStored, err := restarted.MigrationItemErrors(context.Background(), batch.ID)
	if err != nil || len(errorsStored) != 1 {
		t.Fatalf("migration errors=%+v err=%v", errorsStored, err)
	}
	for _, id := range ids {
		path := filepath.Join(root, id)
		tree, err := denyrafs.Scan(path)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := media.SHA256(filepath.Join(path, "01.flac"))
		if err != nil || before[id] != [2]string{tree.Fingerprint, hash} {
			t.Fatalf("read-only check changed %s: before=%v after=%v err=%v", id, before[id], [2]string{tree.Fingerprint, hash}, err)
		}
	}
}

type integrationMigrationIdentity struct{}

func (integrationMigrationIdentity) Decide(_ context.Context, plan domain.MetadataPlan, _ domain.TechnicalReleaseResult) (application.IdentityDecision, error) {
	switch plan.Album {
	case "none":
		return application.IdentityDecision{Status: application.IdentityNoMatch}, nil
	case "ambiguous":
		return application.IdentityDecision{Status: application.IdentityAmbiguous, Candidates: []application.IdentityCandidate{{}, {}}}, nil
	case "exact":
		return application.IdentityDecision{Status: application.IdentityExact, Exact: &application.IdentityCandidate{Release: domain.CanonicalRelease{ReleaseMBID: releaseMBID}}}, nil
	case "error":
		return application.IdentityDecision{Status: application.IdentityError}, errors.New("retryable MusicBrainz outage")
	default:
		return application.IdentityDecision{}, errors.New("unexpected migration fixture")
	}
}

func integrationMigrationRelease(id, artist, album, path, fingerprint string, now time.Time) domain.UnmanagedRelease {
	metadata := domain.MetadataPlan{AlbumArtist: artist, Album: album, DiscTotal: 1, TrackTotal: 1, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Track", Artist: artist, Track: 1, Disc: 1, DurationMS: 1}}}
	return domain.UnmanagedRelease{CandidateID: id, Plan: domain.UnmanagedPlan{CandidateID: id, Metadata: metadata}, Evidence: domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{{RelativePath: "01.flac", Info: domain.TechnicalInfo{DurationMS: 1}}}}, FinalPath: path, Fingerprint: fingerprint, Status: "IMPORTED", CreatedAt: now, UpdatedAt: now}
}
