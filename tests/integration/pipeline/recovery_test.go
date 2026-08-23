package pipeline_test

import (
	"context"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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
