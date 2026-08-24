package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type completionQueueStub struct {
	records []lidarr.QueueRecord
}

func (stub completionQueueStub) QueueRecords(context.Context, int) ([]lidarr.QueueRecord, error) {
	return stub.records, nil
}

type completionStoreStub struct {
	pending  []persistence.PendingCandidate
	events   []persistence.SlskdCompletionEvent
	inserted []persistence.Candidate
}

func (stub *completionStoreStub) IncompletePendingCandidatesBySource(context.Context, string) ([]persistence.PendingCandidate, error) {
	return stub.pending, nil
}

func (stub *completionStoreStub) CandidatesWithoutEffect(context.Context, string, string) ([]persistence.Candidate, error) {
	return nil, nil
}

func (stub *completionStoreStub) InsertCandidate(_ context.Context, candidate persistence.Candidate) error {
	stub.inserted = append(stub.inserted, candidate)
	return nil
}

func (stub *completionStoreStub) SlskdCompletionEventsSince(context.Context, time.Time) ([]persistence.SlskdCompletionEvent, error) {
	return stub.events, nil
}

type completionHandoffStub struct {
	candidates []persistence.Candidate
}

func (stub *completionHandoffStub) AcceptCompleted(_ context.Context, candidate persistence.Candidate, _ contracts.AcquisitionProvenance) error {
	stub.candidates = append(stub.candidates, candidate)
	return nil
}

func TestPrimaryCompletionRecoversFromDurableEventWhenLidarrQueueRecordIsGone(t *testing.T) {
	now := time.Date(2026, 8, 24, 8, 25, 0, 0, time.UTC)
	root := t.TempDir()
	downloadID := "1_NjcTv2rhjbhL_U0D_5"
	completedRoot := filepath.Join(root, "lidarr", downloadID)
	completedFile := filepath.Join(completedRoot, "Feast - Peradaban.flac")
	stableAt := now.Add(-20 * time.Second)

	store := &completionStoreStub{
		pending: []persistence.PendingCandidate{{
			ID:            "candidate-feast",
			JobID:         "job-feast",
			Source:        "slskd",
			SourceLocator: downloadID,
			DownloadID:    downloadID,
			CreatedAt:     now.Add(-2 * time.Minute),
		}},
		events: []persistence.SlskdCompletionEvent{{
			ID:            "event-feast",
			TransferID:    "transfer-feast",
			BatchID:       "batch-feast",
			LocalFilename: completedFile,
			TransferState: "Completed, Succeeded",
			Timestamp:     stableAt,
			ReceivedAt:    stableAt,
			Payload:       []byte(`{"type":"DownloadFileComplete"}`),
			PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	handoff := &completionHandoffStub{}
	service := PrimaryCompletionService{
		Queue:             completionQueueStub{},
		Store:             store,
		Handoff:           handoff,
		DownloadsRoot:     root,
		PageSize:          100,
		EngineVersion:     "0.26.0",
		StabilityInterval: 10 * time.Second,
		Now:               func() time.Time { return now },
	}

	completed, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if completed != 1 {
		t.Fatalf("completed=%d, want 1", completed)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("inserted candidates=%d, want 1", len(store.inserted))
	}
	if got := store.inserted[0].SourceLocator; got != completedRoot {
		t.Fatalf("source locator=%q, want %q", got, completedRoot)
	}
	if len(handoff.candidates) != 1 {
		t.Fatalf("handoff candidates=%d, want 1", len(handoff.candidates))
	}
}
