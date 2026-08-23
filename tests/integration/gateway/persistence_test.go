package gateway_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

const releaseGroupMBID = "12345678-1234-1234-1234-123456789abc"

func gatewayRepositories(t *testing.T) (*sql.DB, *persistence.Repositories, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	db, err := denysqlite.Open(context.Background(), filepath.Join(t.TempDir(), "gateway.db"), denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := migrations.For("gateway")
	if err := denysqlite.Migrate(context.Background(), db, steps, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('config-1','{}','config-hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return db, persistence.New(db, func() time.Time { return now }), now
}
func createJob(t *testing.T, r *persistence.Repositories, now time.Time) domain.Job {
	t.Helper()
	job, err := domain.NewJob("job-1", 42, releaseGroupMBID, "config-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	return job
}
func TestGatewayPersistenceDedupTransitionIntentAndReopen(t *testing.T) {
	db, r, now := gatewayRepositories(t)
	job := createJob(t, r, now)
	duplicate, _ := domain.NewJob("job-2", 42, releaseGroupMBID, "config-1", now)
	if err := r.CreateJob(context.Background(), duplicate); err == nil {
		t.Fatal("active dedup accepted")
	}
	retry := now.Add(time.Minute)
	event, err := r.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: 0, To: domain.StatePrimarySearchRequested, Actor: "worker", Reason: "wanted", NextRetryAt: &retry, OccurredAt: now.Add(time.Second)})
	if err != nil || event.Revision != 1 {
		t.Fatalf("transition=%+v %v", event, err)
	}
	effect := persistence.Effect{JobID: job.ID, Type: "ALBUM_SEARCH", IdempotencyKey: "search-1", RequestHash: "hash", Request: []byte(`{"album":42}`), CreatedAt: now}
	if err := r.PutEffect(context.Background(), effect); err != nil {
		t.Fatal(err)
	}
	if err := r.PutEffect(context.Background(), effect); err != nil {
		t.Fatal(err)
	}
	effects, err := r.UnresolvedEffects(context.Background(), "ALBUM_SEARCH")
	if err != nil || len(effects) != 1 {
		t.Fatalf("effects=%+v %v", effects, err)
	}
	if err := r.AcknowledgeEffect(context.Background(), "search-1", []byte(`{"id":7}`), "response", now); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var name, databasePath string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &databasePath); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = denysqlite.Open(context.Background(), databasePath, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reopened := persistence.New(db, nil)
	stored, err := reopened.Job(context.Background(), job.ID)
	if err != nil || stored.NextRetryAt == nil || !stored.NextRetryAt.Equal(retry) || stored.Revision != 1 {
		t.Fatalf("reopened=%+v %v", stored, err)
	}
}
func TestGatewayPersistenceCandidateImmutableAndWinnerRace(t *testing.T) {
	db, r, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, r, now)
	completed := now.Add(time.Minute)
	for _, id := range []string{"candidate-a", "candidate-b"} {
		if err := r.InsertCandidate(context.Background(), persistence.Candidate{ID: id, JobID: job.ID, Source: "slskd", SourceLocator: id, CompletedAt: &completed, OutputSHA256: strings.Repeat("a", 64), OutputManifest: []byte(`[]`), Provenance: []byte(`{}`), ProvenanceSHA256: "hash-" + id, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE candidates SET source='spotiflac' WHERE candidate_id='candidate-a'`); err == nil {
		t.Fatal("immutable candidate updated")
	}
	if _, err := db.Exec(`UPDATE acquisition_jobs SET state='ARBITRATING' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO arbitrations(job_id,first_approved_at,deadline,evidence_json,state_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, job.ID, now.Format(time.RFC3339Nano), now.Add(30*time.Minute).Format(time.RFC3339Nano), []byte(`{}`), 0, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, id := range []string{"candidate-a", "candidate-b"} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			effect := persistence.Effect{JobID: job.ID, Type: "PIPELINE_WINNER", IdempotencyKey: "winner-" + id, RequestHash: "hash-" + id, Request: []byte(`{}`), CreatedAt: now}
			if _, err := r.LockWinnerWithEffects(context.Background(), persistence.WinnerLock{JobID: job.ID, CandidateID: id, ExpectedRevision: 0, Reason: "quality", Evidence: []byte(`{}`), Effects: []persistence.Effect{effect}, LockedAt: now}); err == nil {
				mutex.Lock()
				successes++
				mutex.Unlock()
			} else if !errors.Is(err, contracts.ErrIdempotencyConflict) && err.Error() != "winner lock lost" {
				t.Errorf("winner error=%v", err)
			}
		}(id)
	}
	wait.Wait()
	if successes != 1 {
		t.Fatalf("winner successes=%d", successes)
	}
}
func TestGatewayPersistenceLeaseRequiresReconciliation(t *testing.T) {
	db, r, now := gatewayRepositories(t)
	defer db.Close()
	createJob(t, r, now)
	lease := persistence.Lease{ResourceType: "job", ResourceID: "job-1", OwnerID: "a", AcquiredAt: now, ExpiresAt: now.Add(time.Minute), ConfigSnapshotID: "config-1"}
	if err := r.AcquireLease(context.Background(), lease, false); err != nil {
		t.Fatal(err)
	}
	other := lease
	other.OwnerID = "b"
	if err := r.AcquireLease(context.Background(), other, false); !errors.Is(err, persistence.ErrLeaseHeld) {
		t.Fatalf("held=%v", err)
	}
	other.AcquiredAt = now.Add(2 * time.Minute)
	other.ExpiresAt = now.Add(3 * time.Minute)
	if err := r.AcquireLease(context.Background(), other, false); !errors.Is(err, persistence.ErrLeaseNeedsReconciliation) {
		t.Fatalf("expired=%v", err)
	}
	if err := r.AcquireLease(context.Background(), other, true); err != nil {
		t.Fatal(err)
	}
}
