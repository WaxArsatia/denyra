package pipeline_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
)

type processorFunc func(context.Context, application.WorkItem) error

func (f processorFunc) Process(ctx context.Context, item application.WorkItem) error {
	return f(ctx, item)
}

func TestPipelineRecoveryReconcilesLeaseOrphansAndManualDiscovery(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	if err := repository.AcquireLease(context.Background(), persistence.Lease{ResourceType: "candidate", ResourceID: candidate.ID, OwnerID: "dead", AcquiredAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), ConfigSnapshotID: "config-1"}, false); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	approved := filepath.Join(root, "approved")
	quarantine := filepath.Join(root, "quarantine")
	incoming := filepath.Join(root, "incoming")
	for _, path := range []string{work, approved, quarantine, incoming} {
		if err := os.Mkdir(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(work, "orphan-1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(incoming, "submission-1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incoming, "submission-1", "track.flac"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovery := application.RecoveryService{Store: repository, WorkRoot: work, ApprovedRoot: approved, QuarantineRoot: quarantine, Now: func() time.Time { return now }}
	report, err := recovery.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ExpiredLeases != 1 || report.OrphanDirectories != 1 || report.MissingDirectories != 1 {
		t.Fatalf("recovery report=%+v", report)
	}
	discovery := application.DiscoveryService{Store: repository, IncomingRoot: incoming, Now: func() time.Time { return now }}
	count, err := discovery.Scan(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("discovery=%d %v", count, err)
	}
	submission, err := repository.Submission(context.Background(), "submission-1")
	if err != nil || submission.Status != "DISCOVERED" {
		t.Fatalf("submission=%+v %v", submission, err)
	}
	service := application.SubmissionService{Store: repository, IncomingRoot: incoming, Now: func() time.Time { return now.Add(time.Second) }}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,password_changed_at,created_at,updated_at) VALUES('admin-1','admin','hash',?,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	tree, err := denyrafs.Scan(submission.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	decision := validUnmanagedDecision(tree.Fingerprint)
	if err := service.Submit(context.Background(), submission.ID, submission.Revision, "admin-1", decision); err != nil {
		t.Fatal(err)
	}
	manualCandidate, err := repository.Candidate(context.Background(), submission.ID)
	if err != nil || manualCandidate.State != domain.StateReceived || manualCandidate.Source != domain.SourceManual {
		t.Fatalf("manual candidate=%+v err=%v", manualCandidate, err)
	}
}

func TestRecoveryClassifiesImportIntentStatuses(t *testing.T) {
	tests := map[string]bool{
		application.ImportPending:     true,
		application.ImportReconciling: true,
		application.ImportSubmitted:   true,
		application.ImportImported:    false,
		application.ImportFailed:      false,
	}
	for status, wantUnresolved := range tests {
		t.Run(status, func(t *testing.T) {
			db, repository, now := pipelineRepositories(t)
			defer db.Close()
			candidate := createPersistedCandidate(t, repository, now)
			intent := domain.ImportIntent{ID: "intent-" + status, CandidateID: candidate.ID, IdempotencyKey: "key-" + status, TargetReleaseMBID: "00000000-0000-0000-0000-000000000001", RequestHash: "hash"}
			if err := repository.PutImportIntent(context.Background(), intent, now); err != nil {
				t.Fatal(err)
			}
			if err := repository.MarkImportStatus(context.Background(), intent.ID, status, nil, now); err != nil {
				t.Fatal(err)
			}
			effects, err := repository.UnresolvedEffects(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, effect := range effects {
				found = found || effect.Kind == "IMPORT_PENDING" && effect.ResourceID == intent.ID
			}
			if found != wantUnresolved {
				t.Fatalf("status %s unresolved = %t, want %t; effects=%+v", status, found, wantUnresolved, effects)
			}
		})
	}
}

func TestSubmissionRejectsTreeChangedAfterPreview(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	incoming := t.TempDir()
	source := filepath.Join(incoming, "submission-changed")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(source, "track.flac")
	if err := os.WriteFile(track, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := repository.DiscoverSubmission(context.Background(), "submission-changed", source, now); err != nil {
		t.Fatal(err)
	}
	tree, err := denyrafs.Scan(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(track, []byte("after"), 0o640); err != nil {
		t.Fatal(err)
	}
	service := application.SubmissionService{Store: repository, IncomingRoot: incoming, Now: func() time.Time { return now }}
	err = service.Submit(context.Background(), "submission-changed", 0, "admin-1", validUnmanagedDecision(tree.Fingerprint))
	if !errors.Is(err, application.ErrPreviewChanged) {
		t.Fatalf("changed preview error=%v", err)
	}
}

func TestRecoveryCompletesVisibleUnmanagedImportBeforeMissingWorkCheck(t *testing.T) {
	for _, tool := range []string{"ffprobe", "flac", "metaflac"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("deployment tool %s unavailable locally", tool)
		}
	}
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	for _, state := range []domain.State{domain.StateClaimed, domain.StateStabilizing, domain.StateWorking, domain.StateTechnicalValidation, domain.StateReleaseMatching, domain.StateUnmanagedReady, domain.StateUnmanagedImporting} {
		now = now.Add(time.Millisecond)
		event, err := repository.UpdateState(context.Background(), persistence.TransitionCommand{CandidateID: candidate.ID, ExpectedRevision: candidate.StateRevision, To: state, Actor: "test", Reason: "prepare recovery", OccurredAt: now})
		if err != nil {
			t.Fatal(err)
		}
		candidate.State, candidate.StateRevision = event.NewState, event.Revision
	}
	root := t.TempDir()
	work, library, approved, quarantine := filepath.Join(root, "work"), filepath.Join(root, "unmanaged"), filepath.Join(root, "approved"), filepath.Join(root, "quarantine")
	for _, path := range []string{approved, quarantine} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	_, decision, technical := seedRealUnmanagedCandidate(t, work, candidate.ID)
	if err := repository.SaveWorkflow(context.Background(), candidate.ID, "", domain.CanonicalRelease{}, domain.ReleaseMatch{}, technical, nil, "", now); err != nil {
		t.Fatal(err)
	}
	runner := media.Runner{MaxOutput: 1 << 20}
	mutation := application.MutationService{WorkRoot: work, QuarantineRoot: quarantine, Tags: media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: 10 * time.Second, Runner: runner}, Integrity: media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: runner}, Checksum: media.SHA256}
	nav := &integrationUnmanagedNav{visible: true}
	injected := false
	importer := application.UnmanagedImportService{Store: repository, Metadata: application.UnmanagedMetadataService{}, Mutation: mutation, Navidrome: nav, WorkRoot: work, LibraryRoot: library, ScanPoll: time.Millisecond, Now: func() time.Time { now = now.Add(time.Millisecond); return now }, Fault: func(point string) error {
		if point == "after_visibility" && !injected {
			injected = true
			return errors.New("crash before candidate state update")
		}
		return nil
	}}
	if _, err := importer.Import(context.Background(), candidate.ID, decision); err == nil {
		t.Fatal("visibility fault missing")
	}
	importer.Fault = nil
	recovery := application.RecoveryService{Store: repository, WorkRoot: work, ApprovedRoot: approved, QuarantineRoot: quarantine, Unmanaged: importer, Now: func() time.Time { now = now.Add(time.Millisecond); return now }}
	report, err := recovery.Reconcile(context.Background())
	if err != nil || report.UnmanagedImports != 1 || report.MissingDirectories != 0 {
		t.Fatalf("recovery report=%+v err=%v", report, err)
	}
	stored, err := repository.Candidate(context.Background(), candidate.ID)
	if err != nil || stored.State != domain.StateUnmanagedImported {
		t.Fatalf("candidate=%+v err=%v", stored, err)
	}
}

func validUnmanagedDecision(fingerprint string) domain.SubmissionDecision {
	return domain.SubmissionDecision{PreviewFingerprint: fingerprint, Destination: domain.DestinationUnmanaged, Metadata: domain.MetadataPlan{
		AlbumArtist: "Kaleb J", Album: "OFF GUARD", DiscTotal: 1, TrackTotal: 1,
		Tracks: []domain.TrackMetadata{{RelativePath: "track.flac", Title: "Track", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1000}},
	}}
}

func TestPipelineWorkerUsesAdmissionLeaseAndEventFirstTrigger(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	var calls atomic.Int32
	processed := make(chan struct{}, 1)
	gate := &application.AdmissionGate{DataRoot: t.TempDir(), MinimumFreeBytes: 0, MinimumFreePercent: 0}
	worker := &application.Worker{Store: repository, Processor: processorFunc(func(context.Context, application.WorkItem) error {
		calls.Add(1)
		select {
		case processed <- struct{}{}:
		default:
		}
		return nil
	}), Admission: gate, Concurrency: 1, LeaseDuration: time.Minute, OwnerID: "worker-test", Queue: make(chan string, 2), Now: func() time.Time { return now }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, time.Hour) }()
	worker.Notify(candidate.ID)
	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("event-first worker did not process candidate")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("processor calls=%d", calls.Load())
	}
	gate.SetMaintenance(true)
	if err := gate.AllowNew(); err != application.ErrMaintenance {
		t.Fatalf("maintenance gate=%v", err)
	}
}
