package acceptance_test

import (
	"context"
	"fmt"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineEveryDurableCandidateStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.db")
	ctx := context.Background()
	db, err := denysqlite.Open(ctx, path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := migrations.For("pipeline")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	if err := denysqlite.Migrate(ctx, db, steps, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('config-acceptance','{}','acceptance-hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	states := []domain.State{domain.StateReceived, domain.StateClaimed, domain.StateStabilizing, domain.StateWorking, domain.StateTechnicalValidation, domain.StateReleaseMatching, domain.StateReviewRequired, domain.StateEnriching, domain.StateApproved, domain.StateArbitrationPending, domain.StateImportReady, domain.StateImportSubmitted, domain.StateImportReconciling, domain.StateImported, domain.StateQuarantined, domain.StateRejected, domain.StateSuperseded, domain.StateCancelled, domain.StateWaitingResubmit}
	for index, state := range states {
		id := fmt.Sprintf("candidate-%02d", index)
		if _, err := db.Exec(`INSERT INTO candidates(candidate_id,source,release_directory,config_snapshot_id,acquisition_evidence_id,state,state_revision,created_at,updated_at) VALUES(?,'manual',?,'config-acceptance',?,?,?, ?,?)`, id, "/data/quarantine/"+id, "evidence-"+id, state, index, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", state, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = denysqlite.Open(ctx, path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := persistence.New(db, func() time.Time { return now })
	for index, want := range states {
		candidate, err := repository.Candidate(ctx, fmt.Sprintf("candidate-%02d", index))
		if err != nil || candidate.State != want || candidate.StateRevision != uint64(index) {
			t.Fatalf("restart state %d=%+v err=%v want=%s", index, candidate, err, want)
		}
	}
}
