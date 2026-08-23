// Package upgrade selects a safe Denyra rollback branch.
package upgrade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

type RollbackMode string

const (
	RollbackBinaryOnly       RollbackMode = "BINARY_ONLY"
	RollbackRestoreDatabases RollbackMode = "RESTORE_DATABASE_TREE"
)

type DatabasePair struct {
	Current string
	Prior   string
}

func SmokeMigrations(ctx context.Context, sourceRoot string, at time.Time) (map[string]int, error) {
	if !filepath.IsAbs(sourceRoot) {
		return nil, errors.New("source root must be absolute")
	}
	result := make(map[string]int)
	for _, service := range []string{"gateway", "pipeline"} {
		path := filepath.Join(sourceRoot, "state", service, "denyra.db")
		db, err := denysqlite.Open(ctx, path, denysqlite.Options{BusyTimeout: 5 * time.Second, MaxOpenConns: 2})
		if err != nil {
			return nil, err
		}
		steps, err := migrations.For(service)
		if err == nil {
			err = denysqlite.Migrate(ctx, db, steps, at.UTC())
		}
		if err == nil {
			var integrity string
			err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
			if err == nil && integrity != "ok" {
				err = fmt.Errorf("%s database integrity check failed", service)
			}
		}
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		result[service] = steps[len(steps)-1].Sequence
	}
	return result, nil
}

func SelectRollback(ctx context.Context, pairs ...DatabasePair) (RollbackMode, error) {
	if len(pairs) == 0 {
		return "", errors.New("at least one database pair is required")
	}
	for _, pair := range pairs {
		current, err := migrationLedger(ctx, pair.Current)
		if err != nil {
			return "", fmt.Errorf("read current database: %w", err)
		}
		prior, err := migrationLedger(ctx, pair.Prior)
		if err != nil {
			return "", fmt.Errorf("read prior database: %w", err)
		}
		if len(current) != len(prior) {
			return RollbackRestoreDatabases, nil
		}
		for index := range current {
			if current[index] != prior[index] {
				return RollbackRestoreDatabases, nil
			}
		}
	}
	return RollbackBinaryOnly, nil
}

type migrationIdentity struct {
	Sequence int
	Name     string
	Checksum string
}

func migrationLedger(ctx context.Context, path string) ([]migrationIdentity, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("database path must be absolute")
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path)+"?mode=ro&_query_only=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, errors.New("database integrity check failed")
	}
	rows, err := db.QueryContext(ctx, `SELECT sequence,name,checksum FROM schema_migrations ORDER BY sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []migrationIdentity
	for rows.Next() {
		var identity migrationIdentity
		if err := rows.Scan(&identity.Sequence, &identity.Name, &identity.Checksum); err != nil {
			return nil, err
		}
		result = append(result, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("database has no migration ledger")
	}
	return result, nil
}
