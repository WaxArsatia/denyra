package operations_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	denyrarestore "github.com/waxarsatia/denyra/internal/platform/restore"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestRestoreScriptsNeverCutOverOrOverwriteLiveTree(t *testing.T) {
	root := repositoryRoot(t)
	restoreScript := readText(t, filepath.Join(root, "scripts/restore/restore.sh"))
	for _, required := range []string{"DENYRA_RESTORE_SNAPSHOT", "DENYRA_RESTIC_REPOSITORY_PATH", "--target /restore", "--verify", "--overwrite never", "verify.sh"} {
		if !strings.Contains(restoreScript, required) {
			t.Errorf("restore script missing %q", required)
		}
	}
	allScripts := restoreScript + readText(t, filepath.Join(root, "scripts/restore/verify.sh")) + readText(t, filepath.Join(root, "scripts/restore/cutover-check.sh"))
	for _, forbidden := range []string{"rm -rf", "docker compose down -v", "mv /data", "rm /data"} {
		if strings.Contains(allScripts, forbidden) {
			t.Errorf("restore scripts contain destructive operation %q", forbidden)
		}
	}
	if !strings.Contains(allScripts, "cutover remains manual") && !strings.Contains(allScripts, "Perform cutover manually") {
		t.Fatal("restore scripts do not preserve manual cutover")
	}
}

func TestRestoreFixtureVerifiesChecksumsDatabasesAndLayout(t *testing.T) {
	root, source, workspace := restoreFixture(t)
	manifest, err := denyrarestore.Create(denyrarestore.CreateOptions{
		BackupID: "fixture-backup", GitCommit: strings.Repeat("a", 40), SourceRoot: source, WorkspaceRoot: workspace,
		CreatedAt: time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := denyrarestore.WriteManifest(filepath.Join(workspace, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	installRestoredDatabases(t, source, workspace)

	report, err := denyrarestore.Verify(context.Background(), denyrarestore.VerifyOptions{RestoreRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if report.GitCommit != strings.Repeat("a", 40) || report.ChecksumFailures != 0 || report.FileCount == 0 || report.DatabaseVersions["gateway"] == 0 || report.DatabaseVersions["pipeline"] == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRestoreFixtureRejectsChangedLibraryFile(t *testing.T) {
	root, source, workspace := restoreFixture(t)
	manifest, err := denyrarestore.Create(denyrarestore.CreateOptions{BackupID: "fixture-backup", GitCommit: strings.Repeat("a", 40), SourceRoot: source, WorkspaceRoot: workspace, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := denyrarestore.WriteManifest(filepath.Join(workspace, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	installRestoredDatabases(t, source, workspace)
	if err := os.WriteFile(filepath.Join(source, "library/Artist/Album/01.flac"), []byte("changed"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := denyrarestore.Verify(context.Background(), denyrarestore.VerifyOptions{RestoreRoot: root}); err == nil {
		t.Fatal("changed library file passed restore verification")
	}
}

func TestRestoreFixtureRejectsChangedConfigFile(t *testing.T) {
	root, source, workspace := restoreFixture(t)
	manifest, err := denyrarestore.Create(denyrarestore.CreateOptions{BackupID: "fixture-backup", GitCommit: strings.Repeat("a", 40), SourceRoot: source, WorkspaceRoot: workspace, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := denyrarestore.WriteManifest(filepath.Join(workspace, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	installRestoredDatabases(t, source, workspace)
	if err := os.WriteFile(filepath.Join(workspace, "config", "gateway.toml"), []byte("changed"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := denyrarestore.Verify(context.Background(), denyrarestore.VerifyOptions{RestoreRoot: root}); err == nil {
		t.Fatal("changed config file passed restore verification")
	}
}

func restoreFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	workspace := filepath.Join(root, "workspace", "fixture-backup")
	for _, directory := range []string{
		filepath.Join(source, "library/Artist/Album"), filepath.Join(source, "state/gateway"),
		filepath.Join(source, "state/pipeline"), filepath.Join(source, "incoming"),
		filepath.Join(source, "processing"), filepath.Join(source, "quarantine"), filepath.Join(workspace, "config"),
	} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "library/Artist/Album/01.flac"), []byte("fixture-flac"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway.toml", "pipeline.toml", "navidrome.toml", "navidrome-lyrics.toml", "slskd.yml"} {
		if err := os.WriteFile(filepath.Join(workspace, "config", name), []byte("fixture "+name+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	for _, service := range []string{"gateway", "pipeline"} {
		createRestoreDatabase(t, service, filepath.Join(workspace, service+".db"))
	}
	return root, source, workspace
}

func createRestoreDatabase(t *testing.T, service, path string) {
	t.Helper()
	db, err := denysqlite.Open(context.Background(), path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := migrations.For(service)
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

func installRestoredDatabases(t *testing.T, source, workspace string) {
	t.Helper()
	for _, service := range []string{"gateway", "pipeline"} {
		content, err := os.ReadFile(filepath.Join(workspace, service+".db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "state", service, "denyra.db"), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
