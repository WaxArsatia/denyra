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
	} {
		if !strings.Contains(compose, required) {
			t.Errorf("Compose backup profile missing %q", required)
		}
	}

	script := readText(t, filepath.Join(root, "scripts/backup/backup.sh"))
	order := []string{"verify-repository.sh", "/internal/maintenance", "stop lidarr navidrome sftpgo slskd", "/internal/maintenance/backup", "restic backup", "restic check", "restic forget"}
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
}

func readText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
