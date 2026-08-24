package gateway_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type fixedPrimaryCompletionQueue struct {
	records []lidarr.QueueRecord
}

func (queue fixedPrimaryCompletionQueue) QueueRecords(context.Context, int) ([]lidarr.QueueRecord, error) {
	return append([]lidarr.QueueRecord(nil), queue.records...), nil
}

type recordingPrimaryCompletionHandoff struct {
	candidates []persistence.Candidate
	evidence   []contracts.AcquisitionProvenance
	store      *persistence.Repositories
	now        time.Time
}

func (handoff *recordingPrimaryCompletionHandoff) AcceptCompleted(_ context.Context, candidate persistence.Candidate, evidence contracts.AcquisitionProvenance) error {
	handoff.candidates = append(handoff.candidates, candidate)
	handoff.evidence = append(handoff.evidence, evidence)
	if handoff.store != nil {
		return handoff.store.PutEffect(context.Background(), persistence.Effect{JobID: candidate.JobID, Type: "PIPELINE_ACCEPT", IdempotencyKey: "candidate-complete-" + candidate.ID, RequestHash: "test", Request: []byte(`{}`), CreatedAt: handoff.now})
	}
	return nil
}

func TestPrimaryCompletionRequiresCompletedHealthyLidarrBatch(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	pending := primaryPendingCandidate(t, store, job.ID, now)
	root := t.TempDir()
	completedPath := filepath.Join(root, "lidarr", pending.DownloadID)
	handoff := &recordingPrimaryCompletionHandoff{store: store, now: now}
	service := application.PrimaryCompletionService{
		Queue: fixedPrimaryCompletionQueue{records: []lidarr.QueueRecord{{
			DownloadID: pending.DownloadID, Status: "downloading", TrackedDownloadStatus: "ok", OutputPath: completedPath,
		}}},
		Store: store, Handoff: handoff, DownloadsRoot: root, PageSize: 100, EngineVersion: "0.26.0", Now: func() time.Time { return now.Add(time.Minute) },
	}

	if completed, err := service.Reconcile(context.Background()); err != nil || completed != 0 {
		t.Fatalf("incomplete reconciliation completed=%d err=%v", completed, err)
	}
	if len(handoff.candidates) != 0 {
		t.Fatal("incomplete Lidarr batch was handed to pipeline")
	}

	service.Queue = fixedPrimaryCompletionQueue{records: []lidarr.QueueRecord{{
		DownloadID: pending.DownloadID, Status: "completed", TrackedDownloadStatus: "warning", OutputPath: completedPath, ErrorMessage: "one transfer failed",
	}}}
	if completed, err := service.Reconcile(context.Background()); err != nil || completed != 0 {
		t.Fatalf("warning reconciliation completed=%d err=%v", completed, err)
	}
	if len(handoff.candidates) != 0 {
		t.Fatal("warning Lidarr batch was handed to pipeline")
	}
}

func TestPrimaryCompletionPersistsThenHandsOffExactlyOnce(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	pending := primaryPendingCandidate(t, store, job.ID, now)
	root := t.TempDir()
	completedPath := filepath.Join(root, "lidarr", pending.DownloadID)
	handoff := &recordingPrimaryCompletionHandoff{store: store, now: now}
	service := application.PrimaryCompletionService{
		Queue: fixedPrimaryCompletionQueue{records: []lidarr.QueueRecord{{
			ID: 91, DownloadID: pending.DownloadID, Title: "Artist - Album", Status: "completed", TrackedDownloadStatus: "ok", OutputPath: completedPath,
		}}},
		Store: store, Handoff: handoff, DownloadsRoot: root, PageSize: 100, EngineVersion: "0.26.0", Now: func() time.Time { return now.Add(time.Minute) },
	}

	if completed, err := service.Reconcile(context.Background()); err != nil || completed != 1 {
		t.Fatalf("first reconciliation completed=%d err=%v", completed, err)
	}
	if completed, err := service.Reconcile(context.Background()); err != nil || completed != 0 {
		t.Fatalf("replay reconciliation completed=%d err=%v", completed, err)
	}
	if len(handoff.candidates) != 1 || len(handoff.evidence) != 1 {
		t.Fatalf("handoff candidates=%d evidence=%d", len(handoff.candidates), len(handoff.evidence))
	}
	got := handoff.candidates[0]
	if got.ID != pending.ID || got.SourceLocator != completedPath || got.DownloadID != pending.DownloadID || got.CompletedAt == nil {
		t.Fatalf("completed candidate=%+v", got)
	}
	if got.OutputSHA256 != "" || len(got.OutputManifest) != 0 {
		t.Fatal("gateway fabricated file checksum evidence for slskd output")
	}
	provenance := handoff.evidence[0]
	if provenance.Provider != "slskd" || provenance.EngineVersion != "0.26.0" || provenance.DownloadID != pending.DownloadID || provenance.ObservedStatus != "completed" || provenance.OutputSHA256 != "" {
		t.Fatalf("provenance=%+v", provenance)
	}
	stored, err := store.Candidate(context.Background(), pending.ID)
	if err != nil {
		t.Fatalf("completed candidate not durable: %v", err)
	}
	if stored.SourceLocator != completedPath {
		t.Fatalf("stored source path=%q", stored.SourceLocator)
	}
}

func TestPrimaryCompletionRejectsQueuePathOutsideSlskdRoot(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	pending := primaryPendingCandidate(t, store, job.ID, now)
	handoff := &recordingPrimaryCompletionHandoff{store: store, now: now}
	service := application.PrimaryCompletionService{
		Queue: fixedPrimaryCompletionQueue{records: []lidarr.QueueRecord{{
			DownloadID: pending.DownloadID, Status: "completed", TrackedDownloadStatus: "ok", OutputPath: filepath.Join(t.TempDir(), pending.DownloadID),
		}}},
		Store: store, Handoff: handoff, DownloadsRoot: t.TempDir(), PageSize: 100, EngineVersion: "0.26.0", Now: func() time.Time { return now.Add(time.Minute) },
	}
	if _, err := service.Reconcile(context.Background()); err == nil {
		t.Fatal("queue path outside slskd root was accepted")
	}
	if len(handoff.candidates) != 0 {
		t.Fatal("invalid path reached pipeline handoff")
	}
}

func TestPrimaryCompletionRecoversDurableCandidateMissingHandoffIntent(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	pending := primaryPendingCandidate(t, store, job.ID, now)
	completedAt := now.Add(time.Minute)
	candidate := persistence.Candidate{ID: pending.ID, JobID: job.ID, Source: "slskd", SourceLocator: filepath.Join(t.TempDir(), "lidarr", pending.DownloadID), DownloadID: pending.DownloadID, CompletedAt: &completedAt, Provenance: []byte(`{"completion":"durable"}`), ProvenanceSHA256: strings.Repeat("a", 64), CreatedAt: completedAt}
	if err := store.InsertCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	handoff := &recordingPrimaryCompletionHandoff{store: store, now: now}
	service := application.PrimaryCompletionService{Queue: fixedPrimaryCompletionQueue{}, Store: store, Handoff: handoff, DownloadsRoot: filepath.Dir(filepath.Dir(candidate.SourceLocator)), PageSize: 100, EngineVersion: "0.26.0", Now: func() time.Time { return now.Add(2 * time.Minute) }}
	if recovered, err := service.Reconcile(context.Background()); err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	if len(handoff.candidates) != 1 || handoff.candidates[0].ID != candidate.ID {
		t.Fatalf("handoff=%+v", handoff.candidates)
	}
}

func primaryPendingCandidate(t *testing.T, store *persistence.Repositories, jobID string, now time.Time) persistence.PendingCandidate {
	t.Helper()
	id, err := persistence.NewCandidateID()
	if err != nil {
		t.Fatal(err)
	}
	evidence := []byte(`{"download_id":"primary-download"}`)
	sum := sha256.Sum256(evidence)
	pending := persistence.PendingCandidate{ID: id, JobID: jobID, Source: "slskd", SourceLocator: "primary-download", DownloadID: "primary-download", Provenance: evidence, ProvenanceSHA256: hex.EncodeToString(sum[:]), CreatedAt: now}
	if err := store.InsertPendingCandidate(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	return pending
}
