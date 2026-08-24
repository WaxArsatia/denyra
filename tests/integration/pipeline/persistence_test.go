package pipeline_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestPersistenceQualityIntentIsImmutableAndIdempotent(t *testing.T) {
	db, repositories, now := pipelineRepositories(t)
	defer db.Close()
	if err := repositories.PutQualityIntent(context.Background(), "quality-1", "candidate-1", "hash-a", []byte(`{"candidate":"one"}`), now); err != nil {
		t.Fatal(err)
	}
	if err := repositories.PutQualityIntent(context.Background(), "quality-1", "candidate-1", "hash-a", []byte(`{"candidate":"one"}`), now); err != nil {
		t.Fatalf("same quality intent rejected: %v", err)
	}
	if err := repositories.PutQualityIntent(context.Background(), "quality-1", "candidate-1", "hash-b", []byte(`{"candidate":"two"}`), now); !errors.Is(err, contracts.ErrIdempotencyConflict) {
		t.Fatalf("conflicting quality intent error = %v", err)
	}
	if err := repositories.CompleteQualityIntent(context.Background(), "quality-1", 202, "response-hash", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repositories.CompleteQualityIntent(context.Background(), "quality-1", 202, "response-hash", now.Add(2*time.Second)); err != nil {
		t.Fatalf("same completion rejected: %v", err)
	}
}

func TestPersistenceUsesOptimisticTransitionAndAtomicAudit(t *testing.T) {
	db, repositories, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repositories, now)

	event, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{
		CandidateID: candidate.ID, ExpectedRevision: 0, To: domain.StateClaimed,
		Actor: "worker-1", Reason: "completion evidence accepted", DetailsJSON: []byte(`{"evidence":"complete"}`), OccurredAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Revision != 1 {
		t.Fatalf("revision = %d", event.Revision)
	}
	_, err = repositories.UpdateState(context.Background(), persistence.TransitionCommand{
		CandidateID: candidate.ID, ExpectedRevision: 0, To: domain.StateCancelled,
		Actor: "admin-1", Reason: "stale cancellation", OccurredAt: now.Add(2 * time.Second),
	})
	var stale *domain.StaleRevisionError
	if !errors.As(err, &stale) || stale.Current != 1 {
		t.Fatalf("stale transition error = %#v", err)
	}
	var transitions, audits int
	if err := db.QueryRow("SELECT COUNT(*) FROM state_transitions WHERE candidate_id=?", candidate.ID).Scan(&transitions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE candidate_id=?", candidate.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if transitions != 1 || audits != 1 {
		t.Fatalf("transition/audit transaction split: transitions=%d audits=%d", transitions, audits)
	}
}

func TestPersistenceRejectsDuplicateHandoffAndImmutableEvidenceMutation(t *testing.T) {
	db, repositories, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repositories, now)
	duplicate, err := domain.CreateCandidate(domain.NewCandidate{
		ID: "candidate-duplicate", Source: candidate.Source, ReleaseDirectory: "/data/downloads/slskd/duplicate",
		ConfigSnapshotID: candidate.ConfigSnapshotID, AcquisitionEvidenceID: candidate.AcquisitionEvidenceID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.CreateCandidate(context.Background(), duplicate); err == nil {
		t.Fatal("duplicate source evidence accepted")
	}
	snapshotID, err := repositories.InsertMetadataSnapshot(context.Background(), candidate.ID, "", "ORIGINAL", []byte(`{"TITLE":["Before"]}`), "abc", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE metadata_snapshots SET sha256='changed' WHERE id=?", snapshotID); err == nil {
		t.Fatal("immutable metadata snapshot updated")
	}
	auditID, err := repositories.AppendAudit(context.Background(), candidate.ID, "worker-1", "EVIDENCE", "stored", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE id=?", auditID); err == nil {
		t.Fatal("append-only audit deleted")
	}
}

func TestPersistenceLeaseRaceAndExpiredReconciliation(t *testing.T) {
	db, repositories, now := pipelineRepositories(t)
	defer db.Close()
	createPersistedCandidate(t, repositories, now)
	lease := persistence.Lease{ResourceType: "candidate", ResourceID: "candidate-1", OwnerID: "worker-1", AcquiredAt: now, ExpiresAt: now.Add(time.Minute), ConfigSnapshotID: "config-1"}
	if err := repositories.AcquireLease(context.Background(), lease, false); err != nil {
		t.Fatal(err)
	}
	contender := lease
	contender.OwnerID = "worker-2"
	if err := repositories.AcquireLease(context.Background(), contender, false); !errors.Is(err, persistence.ErrLeaseHeld) {
		t.Fatalf("active lease contender error = %v", err)
	}
	contender.AcquiredAt = now.Add(2 * time.Minute)
	contender.ExpiresAt = now.Add(3 * time.Minute)
	if err := repositories.AcquireLease(context.Background(), contender, false); !errors.Is(err, persistence.ErrLeaseNeedsReconciliation) {
		t.Fatalf("expired lease without reconciliation error = %v", err)
	}
	if err := repositories.AcquireLease(context.Background(), contender, true); err != nil {
		t.Fatalf("reconciled lease: %v", err)
	}
}

func TestPersistenceConcurrentWritersAllowExactlyOneRevision(t *testing.T) {
	db, repositories, now := pipelineRepositories(t)
	defer db.Close()
	createPersistedCandidate(t, repositories, now)
	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, target := range []domain.State{domain.StateClaimed, domain.StateCancelled} {
		wait.Add(1)
		go func(target domain.State) {
			defer wait.Done()
			_, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{
				CandidateID: "candidate-1", ExpectedRevision: 0, To: target, Actor: "writer", Reason: "race", OccurredAt: now.Add(time.Second),
			})
			if err == nil {
				mutex.Lock()
				successes++
				mutex.Unlock()
				return
			}
			var stale *domain.StaleRevisionError
			if !errors.As(err, &stale) {
				t.Errorf("unexpected writer error: %v", err)
			}
		}(target)
	}
	wait.Wait()
	if successes != 1 {
		t.Fatalf("successful writers = %d, want 1", successes)
	}
}

func TestPersistenceUploadSessionSurvivesRestartAndFinalizesIdempotently(t *testing.T) {
	db, repositories, now := pipelineRepositories(t)
	defer db.Close()
	session := domain.UploadSession{
		ID: "upload-1", SubmissionID: "submission-1", Actor: "admin-1", Status: domain.UploadSessionOpen,
		Files:     []domain.UploadFileSpec{{ID: "entry-1", RelativePath: "OFF GUARD/01.flac", SizeBytes: 12, MediaType: "audio/flac", Status: domain.UploadEntryPending}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repositories.CreateUploadSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := repositories.CompleteUploadEntry(context.Background(), session.ID, "entry-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted := persistence.New(db, func() time.Time { return now.Add(2 * time.Second) })
	loaded, err := restarted.UploadSession(context.Background(), session.ID)
	if err != nil || loaded.Files[0].Status != domain.UploadEntryComplete {
		t.Fatalf("restarted session=%+v err=%v", loaded, err)
	}
	finalPath := "/data/incoming/manual/submission-1"
	provenance := []byte(`{"ingress":"browser","manifest":["OFF GUARD/01.flac"]}`)
	if err := restarted.FinalizeUploadSession(context.Background(), session.ID, finalPath, provenance, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.FinalizeUploadSession(context.Background(), session.ID, finalPath, provenance, now.Add(3*time.Second)); err != nil {
		t.Fatalf("idempotent finalize: %v", err)
	}
	var submissions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM submissions WHERE id='submission-1' AND ingress='browser' AND provenance_json=?`, provenance).Scan(&submissions); err != nil {
		t.Fatal(err)
	}
	if submissions != 1 {
		t.Fatalf("browser submissions=%d, want 1", submissions)
	}
}

func pipelineRepositories(t *testing.T) (*sql.DB, *persistence.Repositories, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := denysqlite.Open(ctx, filepath.Join(t.TempDir(), "pipeline.db"), denysqlite.Options{BusyTimeout: 5 * time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	serviceMigrations, err := migrations.For("pipeline")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	if err := denysqlite.Migrate(ctx, db, serviceMigrations, now); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('config-1','{}','config-hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, persistence.New(db, func() time.Time { return now }), now
}

func createPersistedCandidate(t *testing.T, repositories *persistence.Repositories, now time.Time) domain.Candidate {
	t.Helper()
	candidate, err := domain.CreateCandidate(domain.NewCandidate{
		ID: "candidate-1", Source: domain.SourceSlskd, ReleaseDirectory: "/data/downloads/slskd/release-1",
		ConfigSnapshotID: "config-1", AcquisitionEvidenceID: "download-1", GatewayJobID: "job-1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.CreateCandidate(context.Background(), candidate); err != nil {
		t.Fatal(fmt.Errorf("persist candidate: %w", err))
	}
	return candidate
}
