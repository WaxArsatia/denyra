package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func Backup(ctx context.Context, source *sql.DB, targetPath string) error {
	if !filepath.IsAbs(targetPath) {
		return fmt.Errorf("backup target path must be absolute")
	}
	var sequence int
	var schema, sourcePath string
	if err := source.QueryRowContext(ctx, "PRAGMA database_list").Scan(&sequence, &schema, &sourcePath); err != nil {
		return fmt.Errorf("resolve source database path: %w", err)
	}
	cleanSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("canonicalize source path: %w", err)
	}
	cleanTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("canonicalize target path: %w", err)
	}
	if filepath.Clean(cleanSource) == filepath.Clean(cleanTarget) {
		return fmt.Errorf("backup target must differ from source")
	}
	if _, err := os.Lstat(cleanTarget); err == nil {
		return fmt.Errorf("backup target already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect backup target: %w", err)
	}
	destination, err := sql.Open("sqlite3", cleanTarget)
	if err != nil {
		return fmt.Errorf("open backup target: %w", err)
	}
	defer destination.Close()

	sourceConn, err := source.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire source connection: %w", err)
	}
	defer sourceConn.Close()
	destinationConn, err := destination.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire destination connection: %w", err)
	}
	defer destinationConn.Close()

	return destinationConn.Raw(func(destinationDriver any) error {
		dest, ok := destinationDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("destination is not go-sqlite3")
		}
		return sourceConn.Raw(func(sourceDriver any) error {
			src, ok := sourceDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("source is not go-sqlite3")
			}
			backup, err := dest.Backup("main", src, "main")
			if err != nil {
				return fmt.Errorf("start online backup: %w", err)
			}
			done, stepErr := backup.Step(-1)
			closeErr := backup.Close()
			if stepErr != nil {
				return fmt.Errorf("copy online backup: %w", stepErr)
			}
			if closeErr != nil {
				return fmt.Errorf("finish online backup: %w", closeErr)
			}
			if !done {
				return fmt.Errorf("online backup did not complete")
			}
			return nil
		})
	})
}
