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

func TestUpdateOrderBuildsBeforeStop(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	out, err := f.updateCommand("").CombinedOutput()
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	assertOrderedFragments(t, f.log(), []string{
		"git diff --quiet --ignore-submodules --",
		"git diff --cached --quiet --ignore-submodules --",
		"compose.yaml config",
		"inspect --format {{.Image}} cid-acquisition-gateway",
		"git fetch origin main",
		"git merge --ff-only origin/main",
		" pull --policy always slskd sftpgo restic",
		" build --pull",
		" stop",
		" up -d --remove-orphans --wait --wait-timeout 1",
		" ps",
	})
	env := readFile(t, filepath.Join(f.home, "config", "denyra.env"))
	if !strings.Contains(env, "DENYRA_GIT_COMMIT="+strings.Repeat("b", 40)+"\n") {
		t.Fatalf("active commit not updated:\n%s", env)
	}
}

func TestDirtyTreeStopsUpdateBeforeFetch(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	out, err := f.updateCommand("dirty").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "tracked source files have local changes") {
		t.Fatalf("err=%v output=%q", err, out)
	}
	if strings.Contains(f.log(), "git fetch") || strings.Contains(f.log(), " stop") {
		t.Fatalf("dirty update progressed:\n%s", f.log())
	}
}

func TestBuildFailureLeavesOldStackRunningAndRetryDeploysCandidate(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	out, err := f.updateCommand("build").CombinedOutput()
	if err == nil {
		t.Fatalf("build failure succeeded: %s", out)
	}
	if strings.Contains(f.log(), " stop") {
		t.Fatalf("build failure stopped stack:\n%s", f.log())
	}
	if env := readFile(t, filepath.Join(f.home, "config", "denyra.env")); !strings.Contains(env, "DENYRA_GIT_COMMIT="+strings.Repeat("a", 40)+"\n") {
		t.Fatalf("build failure changed active commit:\n%s", env)
	}
	if err := os.WriteFile(f.logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = f.updateCommand("").CombinedOutput()
	if err != nil || strings.Contains(string(out), "already current") {
		t.Fatalf("retry err=%v output=%q", err, out)
	}
	if !strings.Contains(f.log(), " build --pull") || !strings.Contains(f.log(), " stop") {
		t.Fatalf("retry did not deploy candidate:\n%s", f.log())
	}
}

func TestAutomaticRollbackRestoresStateAndPriorImages(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	out, err := f.updateCommand("smoke").CombinedOutput()
	if err == nil {
		t.Fatalf("unhealthy update succeeded: %s", out)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(f.home, "data", "state", "gateway", "sentinel"))); got != "old" {
		t.Fatalf("restored state=%q", got)
	}
	log := f.log()
	if !strings.Contains(log, "prior-compose.yaml") || !strings.Contains(log, "prior-images.yaml") {
		t.Fatalf("rollback did not use exact prior model:\n%s", log)
	}
}

func TestInterruptedUpdateUsesAutomaticRollback(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	out, err := f.updateCommand("interrupt").CombinedOutput()
	if err == nil {
		t.Fatalf("interrupted update succeeded: %s", out)
	}
	if !strings.Contains(f.log(), "prior-images.yaml") {
		t.Fatalf("interrupt did not start prior images:\n%s", f.log())
	}
}

func TestRollbackRequiresConfirmationAndRestoresSuccessfulSnapshot(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	if out, err := f.updateCommand("").CombinedOutput(); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if err := os.WriteFile(f.logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cancel := f.command("rollback")
	cancel.Stdin = strings.NewReader("n\n")
	if out, err := cancel.CombinedOutput(); err != nil || !strings.Contains(string(out), "rollback cancelled") {
		t.Fatalf("cancel err=%v output=%q", err, out)
	}
	if strings.Contains(f.log(), "prior-images.yaml") {
		t.Fatalf("cancel started prior images:\n%s", f.log())
	}
	confirm := f.command("rollback")
	confirm.Stdin = strings.NewReader("yes\n")
	confirm.Env = append(confirm.Env, "DENYRA_WAIT_SECONDS=1")
	if out, err := confirm.CombinedOutput(); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(f.home, "data", "state", "gateway", "sentinel"))); got != "old" {
		t.Fatalf("restored state=%q", got)
	}
}

func TestRollbackRejectsMissingPriorImage(t *testing.T) {
	f := newManagementFixture(t)
	prepareUpdateFixture(t, f)
	if out, err := f.updateCommand("").CombinedOutput(); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	cmd := f.command("rollback")
	cmd.Stdin = strings.NewReader("y\n")
	cmd.Env = append(cmd.Env, "DENYRA_TEST_UPDATE_FAILURE=missing-image")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "prior image missing: lidarr") {
		t.Fatalf("err=%v output=%q", err, out)
	}
}

func prepareUpdateFixture(t *testing.T, f *managementFixture) {
	t.Helper()
	prepareSnapshotFixture(t, f)
	oldCommit := strings.Repeat("a", 40)
	env := "DENYRA_HOME=" + f.home + "\nDENYRA_CONFIG_DIR=" + filepath.Join(f.home, "config") +
		"\nDENYRA_SECRETS_DIR=" + filepath.Join(f.home, "secrets") + "\nDENYRA_DATA_ROOT=" + filepath.Join(f.home, "data") +
		"\nDENYRA_IMAGE_TAG=" + strings.Repeat("a", 12) + "\nDENYRA_GIT_COMMIT=" + oldCommit + "\n"
	if err := os.WriteFile(filepath.Join(f.home, "config", "denyra.env"), []byte(env), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "data", "state", "gateway", "sentinel"), []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	f.writeExecutable("git", `#!/bin/sh
printf 'git %s\n' "$*" >> "$DENYRA_TEST_LOG"
case "$*" in
  "diff --quiet --ignore-submodules --") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != dirty ] ;;
  "diff --cached --quiet --ignore-submodules --") exit 0 ;;
  "symbolic-ref --quiet --short HEAD") echo main ;;
  "fetch origin main") exit 0 ;;
  "merge --ff-only origin/main") exit 0 ;;
  "rev-parse HEAD") printf '%040d\n' 0 | tr 0 b ;;
  *) exit 2 ;;
esac
`)
	f.writeExecutable("docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DENYRA_TEST_LOG"
case "$*" in
  *" config") echo "services: {}" ;;
  *" ps -q "*)
    for service do :; done
    echo "cid-$service"
    ;;
  "inspect --format {{.Image}} "*)
    for service do :; done
    service=${service#cid-}
    case "$service" in
      acquisition-gateway) char=a ;; media-pipeline) char=b ;; lidarr) char=c ;;
      slskd) char=d ;; sftpgo) char=e ;; navidrome) char=f ;;
    esac
    printf 'sha256:'; printf '%064d\n' 0 | tr 0 "$char"
    ;;
  "image inspect sha256:"*)
    if [ "${DENYRA_TEST_UPDATE_FAILURE:-}" = missing-image ] && printf '%s' "$*" | grep -q 'cccccccc'; then
      exit 1
    fi
    exit 0
    ;;
  *" pull --policy always slskd sftpgo restic") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != pull ] ;;
  *" build --pull") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != build ] ;;
  *" stop") exit 0 ;;
  *" up -d --remove-orphans --wait --wait-timeout "*)
    if printf '%s' "$*" | grep -q 'prior-images.yaml'; then
      exit 0
    fi
    if [ "${DENYRA_TEST_UPDATE_FAILURE:-}" = interrupt ]; then
      kill -TERM "$PPID"
      sleep 1
    fi
    mkdir -p "$DENYRA_DATA_ROOT/state/gateway"
    printf 'candidate\n' > "$DENYRA_DATA_ROOT/state/gateway/sentinel"
    ;;
  *" exec -T acquisition-gateway "*)
    [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != smoke ] || printf '%s' "$*" | grep -q 'prior-images.yaml'
    ;;
  *" exec -T "*) exit 0 ;;
  *" ps") exit 0 ;;
  *) exit 0 ;;
esac
`)
}

func (f *managementFixture) updateCommand(failure string) *exec.Cmd {
	f.t.Helper()
	cmd := f.command("update")
	cmd.Env = append(cmd.Env,
		"DENYRA_DATA_ROOT="+filepath.Join(f.home, "data"),
		"DENYRA_WAIT_SECONDS=1",
		"DENYRA_TEST_UPDATE_FAILURE="+failure,
	)
	return cmd
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
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
