// Package restore creates and verifies deterministic Denyra restore evidence.
package restore

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/waxarsatia/denyra/migrations"
)

type FileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   uint32 `json:"mode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
}

type Manifest struct {
	SchemaVersion  int          `json:"schema_version"`
	BackupID       string       `json:"backup_id"`
	CreatedAt      time.Time    `json:"created_at"`
	GitCommit      string       `json:"git_commit"`
	SourceFiles    []FileRecord `json:"source_files"`
	WorkspaceFiles []FileRecord `json:"workspace_files"`
}

type CreateOptions struct {
	BackupID      string
	GitCommit     string
	SourceRoot    string
	WorkspaceRoot string
	CreatedAt     time.Time
}

type VerifyOptions struct {
	RestoreRoot string
	SnapshotID  string
	ExpectedUID *uint32
	ExpectedGID *uint32
}

type Report struct {
	SnapshotID           string         `json:"snapshot_id"`
	BackupID             string         `json:"backup_id"`
	CreatedAt            time.Time      `json:"created_at"`
	GitCommit            string         `json:"git_commit"`
	DatabaseVersions     map[string]int `json:"database_versions"`
	FileCount            int            `json:"file_count"`
	Bytes                int64          `json:"bytes"`
	ChecksumFailures     int            `json:"checksum_failures"`
	RequiredOwnerChanges int            `json:"required_owner_changes"`
	SameDevice           bool           `json:"same_device"`
	VerifiedAt           time.Time      `json:"verified_at"`
}

var sourceDirectories = []string{"library", "library-unmanaged", "state", "incoming", "processing", "quarantine"}

func Create(options CreateOptions) (Manifest, error) {
	if options.BackupID == "" || options.SourceRoot == "" || options.WorkspaceRoot == "" || options.CreatedAt.IsZero() {
		return Manifest{}, errors.New("backup ID, source, workspace, and creation time are required")
	}
	if !validGitCommit(options.GitCommit) {
		return Manifest{}, errors.New("Git commit must be 40 lowercase hexadecimal characters")
	}
	sourceFiles, err := scanFiles(options.SourceRoot, sourceDirectories, excludeLiveDatabase)
	if err != nil {
		return Manifest{}, fmt.Errorf("scan source: %w", err)
	}
	workspaceFiles, err := scanFiles(options.WorkspaceRoot, []string{"."}, func(path string) bool {
		name := filepath.Base(path)
		return name == "manifest.json" || name == "restore-report.json" || name == "cutover-report.md"
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("scan workspace: %w", err)
	}
	return Manifest{SchemaVersion: 1, BackupID: options.BackupID, CreatedAt: options.CreatedAt.UTC(), GitCommit: options.GitCommit, SourceFiles: sourceFiles, WorkspaceFiles: workspaceFiles}, nil
}

func WriteManifest(path string, manifest Manifest) error {
	if filepath.Base(path) != "manifest.json" {
		return errors.New("manifest filename must be manifest.json")
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o440)
}

func ReadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<20))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.SchemaVersion != 1 || manifest.BackupID == "" || manifest.CreatedAt.IsZero() || !validGitCommit(manifest.GitCommit) {
		return Manifest{}, errors.New("invalid restore manifest identity")
	}
	return manifest, nil
}

func Verify(ctx context.Context, options VerifyOptions) (Report, error) {
	root, err := canonicalDirectory(options.RestoreRoot)
	if err != nil {
		return Report{}, err
	}
	manifestPath, err := findManifest(filepath.Join(root, "workspace"))
	if err != nil {
		return Report{}, err
	}
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return Report{}, fmt.Errorf("read restore manifest: %w", err)
	}
	workspace := filepath.Dir(manifestPath)
	source := filepath.Join(root, "source")
	if _, err := canonicalDirectory(source); err != nil {
		return Report{}, fmt.Errorf("source tree: %w", err)
	}

	report := Report{SnapshotID: options.SnapshotID, BackupID: manifest.BackupID, CreatedAt: manifest.CreatedAt, GitCommit: manifest.GitCommit, DatabaseVersions: make(map[string]int), SameDevice: true, VerifiedAt: time.Now().UTC()}
	for _, group := range []struct {
		root    string
		records []FileRecord
	}{{source, manifest.SourceFiles}, {workspace, manifest.WorkspaceFiles}} {
		for _, record := range group.records {
			report.FileCount++
			report.Bytes += record.Bytes
			if err := verifyRecord(group.root, record, options.ExpectedUID, options.ExpectedGID, &report); err != nil {
				report.ChecksumFailures++
			}
		}
	}
	for _, service := range []string{"gateway", "pipeline"} {
		databasePath := filepath.Join(source, "state", service, "denyra.db")
		backupPath := filepath.Join(workspace, service+".db")
		installedHash, _, installedErr := hashFile(databasePath)
		backupHash, _, backupErr := hashFile(backupPath)
		if installedErr != nil || backupErr != nil || installedHash != backupHash {
			report.ChecksumFailures++
			continue
		}
		version, verifyErr := verifyDatabase(ctx, service, databasePath)
		if verifyErr != nil {
			report.ChecksumFailures++
			continue
		}
		report.DatabaseVersions[service] = version
	}
	report.SameDevice, err = verifySameDevice(source)
	if err != nil {
		return report, err
	}
	if report.ChecksumFailures != 0 || !report.SameDevice {
		return report, fmt.Errorf("restore verification failed: checksum_failures=%d same_device=%t", report.ChecksumFailures, report.SameDevice)
	}
	return report, nil
}

func WriteReport(path string, report Report) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o440)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func WriteCutoverReport(path string, report Report) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o440)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	_, err = fmt.Fprintf(writer, "# Denyra restore cutover report\n\nSnapshot: `%s`\n\nBackup: `%s`\n\nGit commit: `%s`\n\nCreated: `%s`\n\nVerified: `%s`\n\nFiles: %d\n\nBytes: %d\n\nChecksum failures: %d\n\nRequired owner changes: %d\n\nSame-device layout: %t\n\nDatabase migrations: gateway=%d, pipeline=%d\n", report.SnapshotID, report.BackupID, report.GitCommit, report.CreatedAt.Format(time.RFC3339), report.VerifiedAt.Format(time.RFC3339), report.FileCount, report.Bytes, report.ChecksumFailures, report.RequiredOwnerChanges, report.SameDevice, report.DatabaseVersions["gateway"], report.DatabaseVersions["pipeline"])
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(writer, "\n## Manual cutover\n\nKeep the live tree untouched. Stop the stateful services, change their bind mounts to this verified tree, and start them. Roll back by restoring the previous bind mounts.\n")
	if err != nil {
		return err
	}
	return writer.Flush()
}

func validGitCommit(commit string) bool {
	if len(commit) != 40 {
		return false
	}
	for _, character := range commit {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func scanFiles(root string, directories []string, excluded func(string) bool) ([]FileRecord, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return nil, err
	}
	var records []FileRecord
	for _, directory := range directories {
		path := filepath.Join(canonicalRoot, directory)
		if _, err := canonicalDirectory(path); err != nil {
			return nil, err
		}
		err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if current == path || entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
				return fmt.Errorf("unsupported restore entry: %s", current)
			}
			relative, err := filepath.Rel(canonicalRoot, current)
			if err != nil {
				return err
			}
			if excluded(filepath.ToSlash(relative)) {
				return nil
			}
			hash, size, err := hashFile(current)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return errors.New("filesystem ownership metadata unavailable")
			}
			records = append(records, FileRecord{Path: filepath.ToSlash(relative), SHA256: hash, Bytes: size, Mode: uint32(info.Mode().Perm()), UID: stat.Uid, GID: stat.Gid})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(records, func(left, right int) bool { return records[left].Path < records[right].Path })
	return records, nil
}

func excludeLiveDatabase(path string) bool {
	return strings.HasPrefix(path, "state/gateway/denyra.db") || strings.HasPrefix(path, "state/pipeline/denyra.db")
}

func verifyRecord(root string, record FileRecord, expectedUID, expectedGID *uint32, report *Report) error {
	if record.Path == "" || filepath.IsAbs(record.Path) || !safeRelativePath(record.Path) {
		return errors.New("invalid manifest path")
	}
	path := filepath.Join(root, filepath.FromSlash(record.Path))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("restored file is missing or unsafe")
	}
	hash, size, err := hashFile(path)
	if err != nil || hash != record.SHA256 || size != record.Bytes {
		return errors.New("restored file checksum differs")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("filesystem ownership metadata unavailable")
	}
	if (expectedUID != nil && stat.Uid != *expectedUID) || (expectedGID != nil && stat.Gid != *expectedGID) {
		report.RequiredOwnerChanges++
	}
	if info.Mode().Perm()&0o002 != 0 {
		return errors.New("world-writable restored file")
	}
	return nil
}

func safeRelativePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func verifyDatabase(ctx context.Context, service, path string) (int, error) {
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path)+"?mode=ro&_query_only=1")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return 0, fmt.Errorf("%s database integrity check failed", service)
	}
	expected, err := migrations.For(service)
	if err != nil {
		return 0, err
	}
	for _, migration := range expected {
		var name, checksum string
		if err := db.QueryRowContext(ctx, `SELECT name,checksum FROM schema_migrations WHERE sequence=?`, migration.Sequence).Scan(&name, &checksum); err != nil {
			return 0, err
		}
		digest := sha256.Sum256([]byte(migration.SQL))
		if name != migration.Name || checksum != hex.EncodeToString(digest[:]) {
			return 0, fmt.Errorf("%s migration %d identity differs", service, migration.Sequence)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != len(expected) {
		return 0, fmt.Errorf("%s migration count differs", service)
	}
	return expected[len(expected)-1].Sequence, nil
}

func verifySameDevice(source string) (bool, error) {
	var device uint64
	for _, directory := range sourceDirectories {
		info, err := os.Stat(filepath.Join(source, directory))
		if err != nil {
			return false, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return false, errors.New("filesystem device metadata unavailable")
		}
		if device == 0 {
			device = uint64(stat.Dev)
		} else if device != uint64(stat.Dev) {
			return false, nil
		}
	}
	return true, nil
}

func findManifest(workspaceRoot string) (string, error) {
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			path := filepath.Join(workspaceRoot, entry.Name(), "manifest.json")
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				matches = append(matches, path)
			}
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("expected exactly one restore manifest, found %d", len(matches))
	}
	return matches[0], nil
}

func canonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(path)
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	if canonical != clean {
		return "", errors.New("symlinked path is not allowed")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return canonical, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
