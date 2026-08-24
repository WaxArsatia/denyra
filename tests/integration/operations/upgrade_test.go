package operations_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/internal/platform/upgrade"
	"github.com/waxarsatia/denyra/migrations"
)

func TestUpgradeRollbackBranchUsesExactMigrationLedger(t *testing.T) {
	prior := filepath.Join(t.TempDir(), "prior.db")
	current := filepath.Join(t.TempDir(), "current.db")
	createUpgradeDatabase(t, prior)
	content, err := os.ReadFile(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, content, 0o600); err != nil {
		t.Fatal(err)
	}
	mode, err := upgrade.SelectRollback(context.Background(), upgrade.DatabasePair{Current: current, Prior: prior})
	if err != nil || mode != upgrade.RollbackBinaryOnly {
		t.Fatalf("identical ledger mode=%q err=%v", mode, err)
	}
	db, err := denysqlite.Open(context.Background(), current, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO schema_migrations(sequence,name,checksum,applied_at) VALUES(999,'future','` + strings.Repeat("a", 64) + `','2026-08-24T00:00:00Z')`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	mode, err = upgrade.SelectRollback(context.Background(), upgrade.DatabasePair{Current: current, Prior: prior})
	if err != nil || mode != upgrade.RollbackRestoreDatabases {
		t.Fatalf("changed ledger mode=%q err=%v", mode, err)
	}
}

func createUpgradeDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := denysqlite.Open(context.Background(), path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := migrations.For("gateway")
	if err == nil {
		err = denysqlite.Migrate(context.Background(), db, steps, time.Now().UTC())
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}
