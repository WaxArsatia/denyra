package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func TestOpenEnforcesWALForeignKeysAndBusyTimeout(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	assertPragma(t, db, "journal_mode", "wal")
	assertPragma(t, db, "foreign_keys", "1")
	assertPragma(t, db, "busy_timeout", "2500")
}

func TestMigrateTracksChecksumAndRejectsChangedAppliedMigration(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	migrations := []sqlite.Migration{{Sequence: 1, Name: "create_items", SQL: "CREATE TABLE items (id INTEGER PRIMARY KEY);"}}
	if err := sqlite.Migrate(ctx, db, migrations, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := sqlite.Migrate(ctx, db, migrations, time.Now()); err != nil {
		t.Fatalf("idempotent Migrate: %v", err)
	}
	changed := []sqlite.Migration{{Sequence: 1, Name: "create_items", SQL: "CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT);"}}
	if err := sqlite.Migrate(ctx, db, changed, time.Now()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("changed migration error = %v, want checksum failure", err)
	}
}

func TestWithinTxRollsBackErrorAndCommitsSuccess(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if _, err := db.Exec("CREATE TABLE events (value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create events: %v", err)
	}
	wantErr := assertError("stop")
	err := sqlite.WithinTx(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO events(value) VALUES ('rolled-back')"); err != nil {
			return err
		}
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("WithinTx error = %v, want original", err)
	}
	assertCount(t, db, 0)
	if err := sqlite.WithinTx(context.Background(), db, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO events(value) VALUES ('committed')")
		return err
	}); err != nil {
		t.Fatalf("WithinTx commit: %v", err)
	}
	assertCount(t, db, 1)
}

func TestConcurrentWritersCompleteWithinBusyTimeout(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if _, err := db.Exec("CREATE TABLE counters (value INTEGER NOT NULL)"); err != nil {
		t.Fatalf("create counters: %v", err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			_, err := db.Exec("INSERT INTO counters(value) VALUES (?)", value)
			errors <- err
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent insert: %v", err)
		}
	}
}

func TestOnlineBackupProducesReadableIndependentDatabase(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if _, err := db.Exec("CREATE TABLE evidence (value TEXT); INSERT INTO evidence(value) VALUES ('preserved')"); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	target := filepath.Join(t.TempDir(), "backup.db")
	if err := sqlite.Backup(context.Background(), db, target); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	backup, err := sql.Open("sqlite3", target)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backup.Close()
	var value string
	if err := backup.QueryRow("SELECT value FROM evidence").Scan(&value); err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if value != "preserved" {
		t.Fatalf("backup value = %q", value)
	}
}

func TestOnlineBackupRejectsSourcePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.db")
	db, err := sqlite.Open(context.Background(), path, sqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := sqlite.Backup(context.Background(), db, path); err == nil {
		t.Fatal("Backup accepted source as destination")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("source database disappeared: %v", err)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denyra.db")
	db, err := sqlite.Open(context.Background(), path, sqlite.Options{BusyTimeout: 2500 * time.Millisecond, MaxOpenConns: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func assertPragma(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	if strings.ToLower(got) != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}

func assertCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&got); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
