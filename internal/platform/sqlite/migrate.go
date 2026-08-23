package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

type Migration struct {
	Sequence int
	Name     string
	SQL      string
}

func Migrate(ctx context.Context, db *sql.DB, migrations []Migration, appliedAt time.Time) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		sequence INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	sorted := append([]Migration(nil), migrations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sequence < sorted[j].Sequence })
	for index, migration := range sorted {
		if migration.Sequence <= 0 || migration.Name == "" || migration.SQL == "" {
			return fmt.Errorf("migration at index %d has incomplete identity", index)
		}
		if index > 0 && sorted[index-1].Sequence == migration.Sequence {
			return fmt.Errorf("duplicate migration sequence %d", migration.Sequence)
		}
		if err := applyMigration(ctx, db, migration, appliedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration, appliedAt time.Time) error {
	checksumBytes := sha256.Sum256([]byte(migration.SQL))
	checksum := hex.EncodeToString(checksumBytes[:])
	var existingName, existingChecksum string
	err := db.QueryRowContext(ctx, "SELECT name, checksum FROM schema_migrations WHERE sequence = ?", migration.Sequence).Scan(&existingName, &existingChecksum)
	if err == nil {
		if existingName != migration.Name || existingChecksum != checksum {
			return fmt.Errorf("migration %d checksum or name differs from applied migration", migration.Sequence)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read migration %d: %w", migration.Sequence, err)
	}
	return WithinTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.Sequence, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(sequence, name, checksum, applied_at) VALUES (?, ?, ?, ?)", migration.Sequence, migration.Name, checksum, appliedAt.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.Sequence, err)
		}
		return nil
	})
}
