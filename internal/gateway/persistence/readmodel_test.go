package persistence_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestAcquisitionJobPageUsesStableCursorAndStateFilter(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	db, err := denysqlite.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"), denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps, err := migrations.For("gateway")
	if err != nil {
		t.Fatal(err)
	}
	if err := denysqlite.Migrate(ctx, db, steps, now); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('config-1','{}','hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	repository := persistence.New(db, func() time.Time { return now })
	for index := 0; index < 55; index++ {
		job, err := domain.NewJob(fmt.Sprintf("job-%03d", index), int64(index+1), "12345678-1234-1234-1234-123456789abc", "config-1", now)
		if err != nil || repository.CreateJob(ctx, job) != nil {
			t.Fatalf("job %d: %v", index, err)
		}
	}
	if _, err := db.Exec(`UPDATE acquisition_jobs SET state='CANCELLED' WHERE id='job-054'`); err != nil {
		t.Fatal(err)
	}

	first, err := repository.JobSummaries(ctx, 50, "", "")
	if err != nil || len(first.Items) != 50 || first.Next == "" {
		t.Fatalf("first=%d next=%q err=%v", len(first.Items), first.Next, err)
	}
	second, err := repository.JobSummaries(ctx, 50, first.Next, "")
	if err != nil || len(second.Items) != 5 || second.Next != "" {
		t.Fatalf("second=%d next=%q err=%v", len(second.Items), second.Next, err)
	}
	seen := make(map[string]bool, 55)
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.JobID] {
			t.Fatalf("duplicate job across pages: %s", item.JobID)
		}
		seen[item.JobID] = true
	}
	filtered, err := repository.JobSummaries(ctx, 1000, "", "CANCELLED")
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].JobID != "job-054" {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	if _, err := repository.JobSummaries(ctx, 50, "invalid", ""); err == nil {
		t.Fatal("malformed acquisition cursor accepted")
	}
	if _, err := repository.JobSummaries(ctx, 50, "", "UNKNOWN"); err == nil {
		t.Fatal("unknown acquisition state accepted")
	}
}
