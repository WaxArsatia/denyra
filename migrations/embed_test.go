package migrations_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestEmbeddedMigrationsAreOrderedAndComplete(t *testing.T) {
	for _, service := range []string{"gateway", "pipeline"} {
		loaded, err := migrations.For(service)
		if err != nil {
			t.Fatalf("For(%s): %v", service, err)
		}
		if len(loaded) == 0 || loaded[0].Sequence != 1 || loaded[0].Name != "foundation" {
			t.Fatalf("unexpected %s migrations: %+v", service, loaded)
		}
		if service == "pipeline" && loaded[len(loaded)-1].Sequence != 11 {
			t.Fatalf("pipeline migration tail = %+v, want sequence 11", loaded[len(loaded)-1])
		}
	}
	if _, err := migrations.For("unknown"); err == nil {
		t.Fatal("unknown service migrations accepted")
	}
}

func TestMigrationFailureIsTransactional(t *testing.T) {
	ctx := context.Background()
	db, err := denysqlite.Open(ctx, filepath.Join(t.TempDir(), "transactional.db"), denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	broken := []denysqlite.Migration{{Sequence: 1, Name: "candidate", SQL: `CREATE TABLE candidate_value(id INTEGER PRIMARY KEY); SELECT RAISE(ABORT, 'fixture failure');`}}
	if err := denysqlite.Migrate(ctx, db, broken, time.Now().UTC()); err == nil {
		t.Fatal("broken migration succeeded")
	}
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='candidate_value'`).Scan(&name); err != sql.ErrNoRows {
		t.Fatalf("failed migration left table name=%q err=%v", name, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE sequence=1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration ledger count=%d err=%v", count, err)
	}

	corrected := []denysqlite.Migration{{Sequence: 1, Name: "candidate", SQL: `CREATE TABLE candidate_value(id INTEGER PRIMARY KEY);`}}
	if err := denysqlite.Migrate(ctx, db, corrected, time.Now().UTC()); err != nil {
		t.Fatalf("corrected migration: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE sequence=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("corrected migration ledger count=%d err=%v", count, err)
	}
}
