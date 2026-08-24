package operations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupProfileAndRunbookScriptAreDeterministic(t *testing.T) {
	root := repositoryRoot(t)
	compose := readText(t, filepath.Join(root, "deploy/compose.yaml"))
	for _, required := range []string{
		`profiles: ["backup"]`,
		`network_mode: none`,
		`RESTIC_PASSWORD_FILE: /run/secrets/restic_password`,
		`read_only: true`,
		`source: ${DENYRA_HOME:-/srv/denyra}`,
		`target: /source`,
		`source: ${DENYRA_DATA_ROOT:-/srv/denyra/data}/backups`,
	} {
		if !strings.Contains(compose, required) {
			t.Errorf("Compose backup profile missing %q", required)
		}
	}

	script := readText(t, filepath.Join(root, "scripts/backup/backup.sh"))
	order := []string{"verify-repository.sh", "/internal/maintenance", "stop lidarr navidrome sftpgo slskd", "/internal/maintenance/backup", "cp -a -- \"$DENYRA_CONFIG_DIR/.\"", "--git-commit", "restic backup", "restic check", "restic forget"}
	position := -1
	for _, fragment := range order {
		next := strings.Index(script, fragment)
		if next <= position {
			t.Fatalf("backup operation %q absent or out of order", fragment)
		}
		position = next
	}
	for _, retention := range []string{"--keep-daily 7", "--keep-weekly 4", "--keep-monthly 12"} {
		if !strings.Contains(script, retention) {
			t.Errorf("backup retention missing %q", retention)
		}
	}
	for _, included := range []string{
		"/source/config", "/source/secrets", "/source/data/library", "/source/data/library-unmanaged", "/source/data/state",
		"/source/data/incoming", "/source/data/processing", "/source/data/quarantine", "/workspace/$DENYRA_BACKUP_ID",
	} {
		if !strings.Contains(script, included) {
			t.Errorf("backup file set missing %q", included)
		}
	}
	for _, excluded := range []string{
		"/source/credentials.txt", "/source/data/downloads", "/source/data/cache", "/source/updates", "/source/data/backups",
	} {
		if !strings.Contains(script, excluded) {
			t.Errorf("backup exclusion missing %q", excluded)
		}
	}
}

func TestBackupCommandRequiresExplicitExternalRepository(t *testing.T) {
	f := newManagementFixture(t)
	if err := os.MkdirAll(f.home, 0o750); err != nil {
		t.Fatal(err)
	}
	out, err := f.command("backup").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "set DENYRA_RESTIC_REPOSITORY_PATH") {
		t.Fatalf("err=%v output=%q", err, out)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
