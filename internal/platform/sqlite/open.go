// Package sqlite provides Denyra's durable SQLite primitives.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Options struct {
	BusyTimeout  time.Duration
	MaxOpenConns int
}

func Open(ctx context.Context, path string, options Options) (*sql.DB, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("SQLite path must be absolute")
	}
	if options.BusyTimeout <= 0 || options.MaxOpenConns <= 0 {
		return nil, fmt.Errorf("SQLite options must be positive")
	}
	query := url.Values{}
	query.Set("_journal_mode", "WAL")
	query.Set("_foreign_keys", "on")
	query.Set("_busy_timeout", strconv.FormatInt(options.BusyTimeout.Milliseconds(), 10))
	query.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(options.MaxOpenConns)
	db.SetMaxIdleConns(options.MaxOpenConns)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	var quickCheck string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quickCheck); err != nil || quickCheck != "ok" {
		_ = db.Close()
		return nil, fmt.Errorf("SQLite quick_check failed: result=%q error=%w", quickCheck, err)
	}
	return db, nil
}
