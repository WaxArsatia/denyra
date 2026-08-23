package acceptance_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestAcquisitionEveryDurableJobStateAndDeadlineSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	db, err := denysqlite.Open(ctx, path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := migrations.For("gateway")
	if err := denysqlite.Migrate(ctx, db, steps, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('config-acceptance','{}','acceptance-hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	states := []domain.State{domain.StateDiscovered, domain.StatePrimarySearchRequested, domain.StatePrimarySearchRunning, domain.StatePrimaryReconciling, domain.StatePrimaryActive, domain.StatePrimaryRetryableError, domain.StateFallbackRunning, domain.StateFallbackRetryableError, domain.StateNoCandidate, domain.StateDualCandidate, domain.StateArbitrating, domain.StateWinnerLocked, domain.StateHandedOff, domain.StateCancelled}
	deadline := now.Add(6 * time.Hour)
	for index, state := range states {
		jobID := fmt.Sprintf("job-%02d", index)
		releaseGroup := fmt.Sprintf("%08x-1234-1234-1234-%012x", index+1, index+1)
		if _, err := db.Exec(`INSERT INTO acquisition_jobs(id,lidarr_album_id,release_group_mbid,selected_release_mbid,config_snapshot_id,state,state_revision,primary_attempt,fallback_attempt,next_retry_at,queue_watermark,history_watermark,command_id,correlation_started_at,command_deadline,grace_deadline,overall_deadline,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, jobID, index+1, releaseGroup, "abcdefab-1234-5678-9abc-abcdefabcdef", "config-acceptance", state, index, 4, 3, deadline.Format(time.RFC3339Nano), "11", "21", "77", now.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", state, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = denysqlite.Open(ctx, path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := persistence.New(db, func() time.Time { return now })
	for index, state := range states {
		job, err := store.Job(ctx, fmt.Sprintf("job-%02d", index))
		if err != nil || job.State != state || job.Revision != uint64(index) || job.NextRetryAt == nil || !job.NextRetryAt.Equal(deadline) || job.OverallDeadline == nil || !job.OverallDeadline.Equal(deadline) {
			t.Fatalf("restart %s job=%+v err=%v", state, job, err)
		}
	}
}

func TestAcquisitionOutcomeClassificationNeverConvertsOperationalErrorToNoCandidate(t *testing.T) {
	state, err := domain.ClassifyFallback([]domain.ProviderResult{{Provider: "tidal", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "no matches"}, {Provider: "qobuz", Outcome: domain.OutcomeRetryableError, Evidence: "network error"}})
	if err != nil {
		t.Fatal(err)
	}
	if state != domain.StateFallbackRetryableError {
		t.Fatalf("mixed fallback state=%s", state)
	}
	state, err = domain.ClassifyFallback([]domain.ProviderResult{{Provider: "tidal", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "no matches"}, {Provider: "qobuz", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "no matches"}})
	if err != nil || state != domain.StateNoCandidate {
		t.Fatalf("legitimate no-result state=%s err=%v", state, err)
	}
}

func TestAcquisitionGatewayContainsNoDirectSlskdSearchClient(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "gateway")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(data))
		if strings.Contains(lower, "http://slskd") || strings.Contains(lower, "https://slskd") || strings.Contains(lower, `"/api/v0/searches`) {
			t.Errorf("direct slskd search boundary found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
