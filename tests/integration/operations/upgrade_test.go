package operations_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/deplock"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/internal/platform/upgrade"
	"github.com/waxarsatia/denyra/migrations"
)

func TestUpgradeRollbackBranchUsesExactMigrationLedger(t *testing.T) {
	prior := filepath.Join(t.TempDir(), "prior.db")
	current := filepath.Join(t.TempDir(), "current.db")
	createUpgradeDatabase(t, prior)
	content, err := os.ReadFile(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, content, 0o600); err != nil {
		t.Fatal(err)
	}
	mode, err := upgrade.SelectRollback(context.Background(), upgrade.DatabasePair{Current: current, Prior: prior})
	if err != nil || mode != upgrade.RollbackBinaryOnly {
		t.Fatalf("identical ledger mode=%q err=%v", mode, err)
	}
	db, err := denysqlite.Open(context.Background(), current, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO schema_migrations(sequence,name,checksum,applied_at) VALUES(999,'future','` + strings.Repeat("a", 64) + `','2026-08-24T00:00:00Z')`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	mode, err = upgrade.SelectRollback(context.Background(), upgrade.DatabasePair{Current: current, Prior: prior})
	if err != nil || mode != upgrade.RollbackRestoreDatabases {
		t.Fatalf("changed ledger mode=%q err=%v", mode, err)
	}
}

func TestUpgradeRejectsFloatingOrMismatchedDependencyIdentity(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := `{"schema":1,"images":[{"id":"lidarr","reference":"lscr.io/linuxserver/lidarr:nightly@sha256:` + digest + `","platform":"linux/amd64","version":"nightly","digest":"sha256:` + digest + `"}],"artifacts":[],"registries":[],"components":[]}`
	for name, document := range map[string]string{
		"floating": strings.Replace(valid, "nightly@sha256:"+digest, "latest", 1),
		"platform": strings.Replace(valid, "linux/amd64", "linux/arm64", 1),
		"digest":   strings.Replace(valid, `"digest":"sha256:`+digest, `"digest":"sha256:`+strings.Repeat("b", 64), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := deplock.Decode([]byte(document)); err == nil {
				t.Fatal("invalid dependency identity accepted")
			}
		})
	}
}

func TestUpgradeScriptsRequireReviewBackupRestoreAndSmokeEvidence(t *testing.T) {
	root := repositoryRoot(t)
	verify := readText(t, filepath.Join(root, "scripts/upgrade/verify-update.sh"))
	deploy := readText(t, filepath.Join(root, "scripts/upgrade/deploy.sh"))
	rollback := readText(t, filepath.Join(root, "scripts/upgrade/rollback.sh"))
	for _, required := range []string{"DENYRA_UPGRADE_BASE_LOCK", "DENYRA_UPGRADE_APPROVAL_FILE", "verify-pins/verify.sh", "requirements.lock", "templ@v0.3.1020", "docker buildx bake", "images.lock.json"} {
		if !strings.Contains(verify, required) {
			t.Errorf("verify-update missing %q", required)
		}
	}
	for _, required := range []string{"DENYRA_VERIFIED_BACKUP_DIR", "DENYRA_UPGRADE_RESTORE_TARGET", "restore-report.json", "go test -race ./...", "docker compose", "--wait"} {
		if !strings.Contains(deploy, required) {
			t.Errorf("deploy missing %q", required)
		}
	}
	for _, required := range []string{"BINARY_ONLY", "RESTORE_DATABASE_TREE", "denyra.db", "prior-compose.yaml"} {
		if !strings.Contains(rollback, required) {
			t.Errorf("rollback missing %q", required)
		}
	}
}

func createUpgradeDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := denysqlite.Open(context.Background(), path, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := migrations.For("gateway")
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
