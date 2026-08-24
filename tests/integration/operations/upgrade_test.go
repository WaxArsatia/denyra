package operations_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/internal/platform/upgrade"
	"github.com/waxarsatia/denyra/migrations"
)

func TestUpdateSnapshotRecordsPriorImagesAndOnlyState(t *testing.T) {
	f := newManagementFixture(t)
	prepareSnapshotFixture(t, f)
	path, output, err := runSnapshotFixture(f, "")
	if err != nil {
		t.Fatalf("snapshot: %v\n%s", err, output)
	}
	images, err := os.ReadFile(filepath.Join(path, "prior-images.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for index, service := range []string{"acquisition-gateway", "media-pipeline", "lidarr", "slskd", "sftpgo", "navidrome"} {
		want := "  " + service + ":\n    image: sha256:" + strings.Repeat(string(rune('a'+index)), 64)
		if !strings.Contains(string(images), want) {
			t.Errorf("missing image override %q:\n%s", want, images)
		}
	}
	for _, want := range []string{"config/denyra.env", "state/gateway/sentinel"} {
		if _, err := os.Stat(filepath.Join(path, want)); err != nil {
			t.Errorf("snapshot missing %s: %v", want, err)
		}
	}
	for _, forbidden := range []string{"library", "downloads", "processing", "quarantine", "cache"} {
		if _, err := os.Stat(filepath.Join(path, forbidden)); !os.IsNotExist(err) {
			t.Errorf("snapshot contains excluded path %s", forbidden)
		}
	}
}

func TestPriorImageSnapshotRejectsMissingOrMutableIdentity(t *testing.T) {
	for _, failure := range []string{"missing", "mutable"} {
		t.Run(failure, func(t *testing.T) {
			f := newManagementFixture(t)
			prepareSnapshotFixture(t, f)
			_, output, err := runSnapshotFixture(f, failure)
			if err == nil || !strings.Contains(output, "prior image") {
				t.Fatalf("failure=%s err=%v output=%q", failure, err, output)
			}
		})
	}
}

func TestSnapshotPathRejectsSymlinkedState(t *testing.T) {
	f := newManagementFixture(t)
	prepareSnapshotFixture(t, f)
	state := filepath.Join(f.home, "data", "state")
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), state); err != nil {
		t.Fatal(err)
	}
	_, output, err := runSnapshotFixture(f, "")
	if err == nil || !strings.Contains(output, "state root must not be a symlink") {
		t.Fatalf("err=%v output=%q", err, output)
	}
}

func prepareSnapshotFixture(t *testing.T, f *managementFixture) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(f.home, "config"), filepath.Join(f.home, "data", "state", "gateway"), filepath.Join(f.home, "updates"),
		filepath.Join(f.home, "data", "library"), filepath.Join(f.home, "data", "downloads"),
	} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.home, "config", "denyra.env"), []byte("DENYRA_DATA_ROOT="+filepath.Join(f.home, "data")+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "data", "state", "gateway", "sentinel"), []byte("state"), 0o640); err != nil {
		t.Fatal(err)
	}
f.writeExecutable("docker", `#!/bin/sh
case "$*" in
  *" config") echo "services: {}" ;;
  *" ps -q "*)
    for service do :; done
    [ "${DENYRA_TEST_SNAPSHOT_FAILURE:-}" != missing ] || [ "$service" != lidarr ] || exit 0
    echo "cid-$service"
    ;;
  "inspect --format {{.Image}} "*)
    for service do :; done
    service=${service#cid-}
    if [ "${DENYRA_TEST_SNAPSHOT_FAILURE:-}" = mutable ] && [ "$service" = lidarr ]; then
      echo "denyra/lidarr:latest"
      exit 0
    fi
    case "$service" in
      acquisition-gateway) char=a ;; media-pipeline) char=b ;; lidarr) char=c ;;
      slskd) char=d ;; sftpgo) char=e ;; navidrome) char=f ;;
    esac
    printf 'sha256:'
    i=0; while [ "$i" -lt 64 ]; do printf '%s' "$char"; i=$((i+1)); done
    printf '\n'
    ;;
  *) exit 2 ;;
esac
`)
}

func runSnapshotFixture(f *managementFixture, failure string) (string, string, error) {
	f.t.Helper()
	script := `. "$1/scripts/manage/common.sh"
. "$1/scripts/manage/snapshot.sh"
repo_root=$1
denyra_context
pending=$(denyra_snapshot_prepare aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)
snapshot=$(denyra_snapshot_name "$pending" bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb)
denyra_snapshot_capture "$snapshot"
printf '%s\n' "$snapshot"`
	cmd := exec.Command("sh", "-c", script, "sh", f.repo)
	cmd.Env = append(os.Environ(),
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DENYRA_HOME="+f.home,
		"DENYRA_DATA_ROOT="+filepath.Join(f.home, "data"),
		"DENYRA_TEST_SNAPSHOT_FAILURE="+failure,
	)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return "", text, err
	}
	lines := strings.Split(text, "\n")
	return lines[len(lines)-1], text, nil
}

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
