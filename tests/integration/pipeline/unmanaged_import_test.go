package pipeline_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestUnmanagedImportPersistsEveryRestartBoundaryAndCompletesOnce(t *testing.T) {
	for _, tool := range []string{"ffprobe", "flac", "metaflac"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("deployment tool %s unavailable locally", tool)
		}
	}
	for _, faultPoint := range []string{"after_intent", "after_mutation", "after_layout", "after_final_rename", "after_publish", "after_visibility"} {
		t.Run(faultPoint, func(t *testing.T) {
			db, repository, now := pipelineRepositories(t)
			defer db.Close()
			candidate := createPersistedCandidate(t, repository, now)
			root := t.TempDir()
			work, library, quarantine := filepath.Join(root, "work"), filepath.Join(root, "unmanaged"), filepath.Join(root, "quarantine")
			track, decision, technical := seedRealUnmanagedCandidate(t, work, candidate.ID)
			if err := repository.SaveWorkflow(context.Background(), candidate.ID, "", domain.CanonicalRelease{}, domain.ReleaseMatch{}, technical, nil, "", now); err != nil {
				t.Fatal(err)
			}
			nav := &integrationUnmanagedNav{visible: true}
			runner := media.Runner{MaxOutput: 1 << 20}
			mutation := application.MutationService{WorkRoot: work, QuarantineRoot: quarantine, Tags: media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: 10 * time.Second, Runner: runner}, Integrity: media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: runner}, Checksum: media.SHA256}
			fired := false
			service := application.UnmanagedImportService{Store: repository, Metadata: application.UnmanagedMetadataService{}, Mutation: mutation, Navidrome: nav, WorkRoot: work, LibraryRoot: library, ScanPoll: time.Millisecond, Now: func() time.Time { now = now.Add(time.Millisecond); return now }, Fault: func(point string) error {
				if point == faultPoint && !fired {
					fired = true
					return errors.New("injected " + point)
				}
				return nil
			}}
			if _, err := service.Import(context.Background(), candidate.ID, decision); err == nil || !fired {
				t.Fatalf("fault %s not observed: err=%v fired=%v", faultPoint, err, fired)
			}
			restarted := service
			restarted.Fault = nil
			release, err := restarted.Import(context.Background(), candidate.ID, decision)
			if err != nil || release.Status != "IMPORTED" {
				t.Fatalf("restart release=%+v err=%v", release, err)
			}
			wantTrack := filepath.Join(library, "Kaleb J", "OFF GUARD (2024)", "01 - Untukmu.flac")
			if _, err := os.Stat(wantTrack); err != nil {
				t.Fatalf("final track absent: %v (original=%s)", err, track)
			}
			intent, err := repository.UnmanagedImportIntent(context.Background(), candidate.ID)
			if err != nil || intent.Status != "COMPLETED" || intent.Fingerprint == "" || len(intent.Manifest) != 1 || intent.Manifest[0].SHA256 == "" {
				t.Fatalf("durable intent=%+v err=%v", intent, err)
			}
			if nav.scans == 0 {
				t.Fatal("Navidrome scan not triggered")
			}
		})
	}
}

func TestControlledWorkflowRoutesApprovedManualReleaseToUnmanagedLibrary(t *testing.T) {
	for _, tool := range []string{"ffprobe", "flac", "metaflac"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("deployment tool %s unavailable locally", tool)
		}
	}
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	root := t.TempDir()
	incoming, work, library, quarantine := filepath.Join(root, "incoming"), filepath.Join(root, "work"), filepath.Join(root, "unmanaged"), filepath.Join(root, "quarantine")
	candidateID := "manual-unmanaged"
	source := filepath.Join(incoming, candidateID)
	track, decision, _ := seedRealUnmanagedCandidate(t, incoming, candidateID)
	if track != filepath.Join(source, "01.flac") {
		t.Fatalf("seed path=%s", track)
	}
	if err := repository.DiscoverSubmission(context.Background(), candidateID, source, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,password_changed_at,created_at,updated_at) VALUES('admin-1','admin','hash',?,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	tree, err := denyrafs.Scan(source)
	if err != nil {
		t.Fatal(err)
	}
	decision.PreviewFingerprint = tree.Fingerprint
	if err := (application.SubmissionService{Store: repository, IncomingRoot: incoming, Now: func() time.Time { return now.Add(time.Millisecond) }}).Submit(context.Background(), candidateID, 0, "admin-1", decision); err != nil {
		t.Fatal(err)
	}
	runner := media.Runner{MaxOutput: 1 << 20}
	ffprobe := media.FFProbe{Binary: "ffprobe", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	flac := media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	metaflac := media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	mutation := application.MutationService{WorkRoot: work, QuarantineRoot: quarantine, Tags: metaflac, Integrity: flac, Checksum: media.SHA256}
	nav := &integrationUnmanagedNav{visible: true}
	unmanaged := application.UnmanagedImportService{Store: repository, Metadata: application.UnmanagedMetadataService{}, Mutation: mutation, Navidrome: nav, WorkRoot: work, LibraryRoot: library, ScanPoll: time.Millisecond, Now: func() time.Time { now = now.Add(time.Millisecond); return now }}
	workflow := application.ControlledWorkflow{
		Store: repository, Claim: application.ClaimService{WorkRoot: work, LockRoot: filepath.Join(root, "locks"), StabilityInterval: time.Millisecond, Pause: func(context.Context, time.Duration) error { return nil }},
		Validator: application.TechnicalValidator{Inspector: ffprobe, Integrity: flac, Heuristic: media.NoHeuristic{}, Checksum: media.SHA256},
		Matching:  application.MatchingService{WorkRoot: work, QuarantineRoot: quarantine}, Mutation: mutation, Unmanaged: unmanaged,
		SourceRoots: map[domain.Source]string{domain.SourceManual: incoming}, MaxInlineTransitions: 12, Now: func() time.Time { now = now.Add(time.Millisecond); return now },
	}
	if err := workflow.Process(context.Background(), application.WorkItem{CandidateID: candidateID, ConfigSnapshotID: "config-1"}); err != nil {
		t.Fatal(err)
	}
	candidate, err := repository.Candidate(context.Background(), candidateID)
	if err != nil || candidate.State != domain.StateUnmanagedImported || !candidate.State.Terminal() {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	if _, err := os.Stat(filepath.Join(library, "Kaleb J", "OFF GUARD (2024)", "01 - Untukmu.flac")); err != nil {
		t.Fatalf("workflow final release: %v", err)
	}
}

type integrationUnmanagedNav struct {
	visible bool
	scans   int
}

func (n *integrationUnmanagedNav) EnsureLibraries(context.Context) (int, int, bool, error) {
	return 1, 2, false, nil
}
func (n *integrationUnmanagedNav) StartScan(context.Context, ...int) error       { n.scans++; return nil }
func (n *integrationUnmanagedNav) WaitScan(context.Context, time.Duration) error { return nil }
func (n *integrationUnmanagedNav) ReleaseVisible(context.Context, int, navidrome.ReleaseIdentity) (bool, error) {
	return n.visible, nil
}

func seedRealUnmanagedCandidate(t *testing.T, work, candidateID string) (string, domain.SubmissionDecision, domain.TechnicalReleaseResult) {
	t.Helper()
	directory := filepath.Join(work, candidateID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(directory, "01.flac")
	copyFile(t, filepath.Join(generateFLACFixtures(t), "mono-16-44100.flac"), track)
	runCommand(t, "metaflac", "--set-tag=TITLE=Old", "--set-tag=ARTIST=Kaleb J", "--set-tag=ALBUM=OFF GUARD", "--set-tag=ALBUMARTIST=Kaleb J", "--set-tag=TRACKNUMBER=1", "--set-tag=DISCNUMBER=1", "--set-tag=ISRC=IDABC2600001", "--set-tag=CUSTOM=keep", track)
	runner := media.Runner{MaxOutput: 1 << 20}
	probe := media.FFProbe{Binary: "ffprobe", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	info, comments, command, err := probe.Inspect(context.Background(), track)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := media.SHA256(track)
	if err != nil {
		t.Fatal(err)
	}
	metadata := domain.MetadataPlan{AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2024", DiscTotal: 1, TrackTotal: 1, Preserved: map[string]map[string][]string{"01.flac": comments}, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Untukmu", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: info.DurationMS}}}
	decision := domain.SubmissionDecision{Destination: domain.DestinationUnmanaged, Metadata: metadata}
	technical := domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{{RelativePath: "01.flac", SHA256Before: hash, Info: info, OriginalComments: comments, Commands: []domain.CommandEvidence{command}}}}
	return track, decision, technical
}
