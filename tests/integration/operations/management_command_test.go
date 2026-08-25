package operations_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type managementFixture struct {
	t       *testing.T
	repo    string
	home    string
	bin     string
	logPath string
}

func newManagementFixture(t *testing.T) *managementFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	tmp := t.TempDir()
	f := &managementFixture{
		t:       t,
		repo:    root,
		home:    filepath.Join(tmp, "home"),
		bin:     filepath.Join(tmp, "bin"),
		logPath: filepath.Join(tmp, "commands.log"),
	}
	if err := os.MkdirAll(f.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	f.writeExecutable("docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DENYRA_TEST_LOG"
case "$*" in
  "compose version"*) exit 0 ;;
  *" build --pull"*) [ "${DENYRA_TEST_DOCKER_FAIL:-}" != build ] || exit 19 ;;
  *" up -d --wait --wait-timeout "*" lidarr slskd sftpgo navidrome")
    [ "${DENYRA_TEST_DOCKER_FAIL:-}" != dependencies ] || exit 20
    mkdir -p "$DENYRA_DATA_ROOT/state/lidarr"
    if [ ! -s "$DENYRA_DATA_ROOT/state/lidarr/config.xml" ]; then
      printf '<Config><ApiKey>%s</ApiKey><Unrelated>keep</Unrelated></Config>\n' "${DENYRA_TEST_LIDARR_API_KEY:-fixture-lidarr-api-key}" > "$DENYRA_DATA_ROOT/state/lidarr/config.xml"
    fi
    ;;
  *" up -d --remove-orphans --wait")
    if [ "${DENYRA_TEST_DOCKER_FAIL:-}" = start-once ] && [ ! -e "$DENYRA_TEST_START_MARKER" ]; then
      : > "$DENYRA_TEST_START_MARKER"
      exit 22
    fi
    ;;
  *" --profile setup run --rm reconciler")
    [ "${DENYRA_TEST_DOCKER_FAIL:-}" != reconcile ] || exit 21
    if [ "${DENYRA_TEST_DOCKER_FAIL:-}" = reconcile-once ] && [ ! -e "$DENYRA_TEST_RECONCILE_MARKER" ]; then
      : > "$DENYRA_TEST_RECONCILE_MARKER"
      exit 21
    fi
    ;;
esac
`)
	return f
}

func (f *managementFixture) writeExecutable(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.bin, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *managementFixture) command(args ...string) *exec.Cmd {
	f.t.Helper()
	cmd := exec.Command(filepath.Join(f.repo, "denyra"), args...)
	cmd.Dir = f.repo
	cmd.Env = append(os.Environ(),
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DENYRA_HOME="+f.home,
		"DENYRA_TEST_LOG="+f.logPath,
	)
	return cmd
}

func (f *managementFixture) run(args ...string) string {
	f.t.Helper()
	out, err := f.command(args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("denyra %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *managementFixture) log() string {
	f.t.Helper()
	content, err := os.ReadFile(f.logPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return string(content)
}

func TestStatusUsesOneComposeContext(t *testing.T) {
	f := newManagementFixture(t)
	f.run("status")
	log := f.log()
	for _, want := range []string{
		"compose --project-name denyra",
		"--env-file " + filepath.Join(f.home, "config", "denyra.env"),
		"-f " + filepath.Join(f.repo, "deploy", "compose.yaml"),
		"ps",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("compose invocation missing %q: %s", want, log)
		}
	}
}

func TestUnknownCommandReturnsUsage(t *testing.T) {
	f := newManagementFixture(t)
	cmd := f.command("unknown")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil || cmd.ProcessState.ExitCode() != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("exit=%v stderr=%q err=%v", cmd.ProcessState, stderr.String(), err)
	}
}

func TestDeploymentRootValidation(t *testing.T) {
	tests := []struct {
		name     string
		home     string
		dataRoot string
		want     string
	}{
		{name: "relative home", home: "relative", want: "DENYRA_HOME must be an absolute path"},
		{name: "shell home", home: "/tmp/$DENYRA_HOME", want: "DENYRA_HOME is unsafe"},
		{name: "tilde home", home: "/tmp/~/denyra", want: "DENYRA_HOME is unsafe"},
		{name: "root home", home: "/", want: "DENYRA_HOME is unsafe"},
		{name: "relative data", dataRoot: "relative", want: "DENYRA_DATA_ROOT must be an absolute path"},
		{name: "root data", dataRoot: "/", want: "DENYRA_DATA_ROOT cannot be /"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagementFixture(t)
			cmd := f.command("status")
			if tt.home != "" {
				cmd.Env = append(cmd.Env, "DENYRA_HOME="+tt.home)
			}
			if tt.dataRoot != "" {
				cmd.Env = append(cmd.Env, "DENYRA_DATA_ROOT="+tt.dataRoot)
			}
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tt.want) {
				t.Fatalf("wanted %q, err=%v output=%q", tt.want, err, out)
			}
		})
	}
}

func TestDeploymentRootNeedNotExistBeforeSetup(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	out, err := f.setupCommand().CombinedOutput()
	if err != nil {
		t.Fatalf("err=%v output=%q", err, out)
	}
	if info, err := os.Stat(f.home); err != nil || !info.IsDir() {
		t.Fatalf("setup did not create deployment root: %v", err)
	}
}

func TestOperationLockRejectsConcurrentOperation(t *testing.T) {
	f := newManagementFixture(t)
	if err := os.MkdirAll(filepath.Join(f.home, ".operation-lock"), 0o750); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(f.repo, "scripts", "manage", "common.sh")
	cmd := exec.Command("sh", "-c", `repo_root=$(CDPATH= cd -- "$(dirname -- "$1")/../.." && pwd); . "$1"; denyra_context; denyra_lock`, "sh", common)
	cmd.Env = append(os.Environ(), "DENYRA_HOME="+f.home)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "another Denyra operation is running") {
		t.Fatalf("err=%v output=%q", err, out)
	}
}

var setupSecretNames = []string{
	"internal_bearer",
	"audit_key",
	"bootstrap_admin",
	"navidrome_admin",
	"sftpgo_admin",
	"sftpgo_upload",
	"slskd_api_key",
	"slskd_web_password",
	"restic_password",
	"soulseek_username",
	"soulseek_password",
}

func (f *managementFixture) setupCommand() *exec.Cmd {
	f.t.Helper()
	f.writeExecutable("git", `#!/bin/sh
case "$*" in
  "--version") echo "git version test" ;;
  "rev-parse --short=12 HEAD") echo "123456789abc" ;;
  "rev-parse HEAD") echo "123456789abcdef0123456789abcdef012345678" ;;
  *) exit 2 ;;
esac
`)
	cmd := f.command("setup")
	cmd.Env = append(cmd.Env,
		"DENYRA_SOULSEEK_USERNAME=test-listener",
		"DENYRA_SOULSEEK_PASSWORD=test-password",
		"DENYRA_WAIT_SECONDS=1",
	)
	return cmd
}

func (f *managementFixture) runSetup() {
	f.t.Helper()
	out, err := f.setupCommand().CombinedOutput()
	if err != nil {
		f.t.Fatalf("setup: %v\n%s", err, out)
	}
}

func TestSetupCreatesExternalDeploymentState(t *testing.T) {
	f := newManagementFixture(t)
	f.runSetup()

	assertMode(t, filepath.Join(f.home, "secrets"), 0o700)
	assertMode(t, filepath.Join(f.home, "config"), 0o750)
	assertMode(t, filepath.Join(f.home, "data"), 0o750)
	assertMode(t, filepath.Join(f.home, "credentials.txt"), 0o600)
	for _, relative := range []string{
		"library", "library-unmanaged", "incoming/manual", "incoming/uploading",
		"processing/work", "processing/approved", "quarantine", "state/pipeline",
	} {
		info, err := os.Stat(filepath.Join(f.home, "data", filepath.FromSlash(relative)))
		if err != nil || !info.IsDir() {
			t.Errorf("setup root %s missing: %v", relative, err)
		}
	}
	for _, name := range setupSecretNames {
		path := filepath.Join(f.home, "secrets", name)
		content, err := os.ReadFile(path)
		if err != nil || len(bytes.TrimSpace(content)) == 0 {
			t.Fatalf("secret %s missing/empty: %v", name, err)
		}
		assertMode(t, path, 0o600)
	}

	env, err := os.ReadFile(filepath.Join(f.home, "config", "denyra.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DENYRA_HOME=" + f.home,
		"DENYRA_CONFIG_DIR=" + filepath.Join(f.home, "config"),
		"DENYRA_SECRETS_DIR=" + filepath.Join(f.home, "secrets"),
		"DENYRA_DATA_ROOT=" + filepath.Join(f.home, "data"),
		fmt.Sprintf("DENYRA_MEDIA_UID=%d", os.Getuid()),
		fmt.Sprintf("DENYRA_MEDIA_GID=%d", os.Getgid()),
		"DENYRA_IMAGE_TAG=123456789abc",
		"DENYRA_GIT_COMMIT=123456789abcdef0123456789abcdef012345678",
	} {
		if !strings.Contains(string(env), want+"\n") {
			t.Errorf("denyra.env missing %q:\n%s", want, env)
		}
	}
	locator, err := os.ReadFile(filepath.Join(f.repo, ".denyra-home"))
	if err != nil || strings.TrimSpace(string(locator)) != f.home {
		t.Fatalf("locator=%q err=%v", locator, err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
}

func TestSetupAndCredentialsReportApprovedHostPorts(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	f.runSetup()

	stored, err := os.ReadFile(filepath.Join(f.home, "credentials.txt"))
	if err != nil {
		t.Fatal(err)
	}
	commandOutput := f.run("credentials")
	for _, want := range []string{
		"http://localhost:4000",
		"http://localhost:4002",
		"http://localhost:4003",
		"http://localhost:4004",
		"localhost:4005",
		"localhost:50300",
	} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("stored credentials missing %q:\n%s", want, stored)
		}
		if !strings.Contains(commandOutput, want) {
			t.Errorf("credentials output missing %q:\n%s", want, commandOutput)
		}
	}
}

func TestSetupUsesContainersWithoutHostCompilerSteps(t *testing.T) {
	root := repositoryRoot(t)
	scripts := readText(t, filepath.Join(root, "scripts/manage/setup.sh")) + readText(t, filepath.Join(root, "scripts/manage/smoke.sh"))
	for _, forbidden := range []string{"go build", "go run", "python ", "pip ", "npm ", "templ ", "ffmpeg ", "flac "} {
		if strings.Contains(scripts, forbidden) {
			t.Errorf("setup requires host build/media command %q", forbidden)
		}
	}
}

func TestSetupIsIdempotent(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	f.runSetup()
	before := readSetupSecrets(t, f.home)
	f.runSetup()
	after := readSetupSecrets(t, f.home)
	for name, content := range before {
		if !bytes.Equal(content, after[name]) {
			t.Errorf("secret %s changed on rerun", name)
		}
	}
}

func TestSetupResumesMissingGeneratedConfig(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	f.runSetup()
	before := readSetupSecrets(t, f.home)
	missing := filepath.Join(f.home, "config", "navidrome.toml")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	f.runSetup()
	if info, err := os.Stat(missing); err != nil || info.Size() == 0 {
		t.Fatalf("missing config was not restored: %v", err)
	}
	after := readSetupSecrets(t, f.home)
	for name, content := range before {
		if !bytes.Equal(content, after[name]) {
			t.Errorf("secret %s changed while resuming", name)
		}
	}
}

func TestSetupMigratesLegacyLocalCredentialNames(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	if err := os.MkdirAll(filepath.Join(f.home, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{
		"denyra_admin_password":    "legacy-denyra-password",
		"navidrome_admin_password": "legacy-navidrome-password",
		"sftpgo_admin_password":    "legacy-sftpgo-admin-password",
		"sftpgo_upload_password":   "legacy-sftpgo-upload-password",
	}
	for name, value := range legacy {
		if err := os.WriteFile(filepath.Join(f.home, "secrets", name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.runSetup()
	for oldName, newName := range map[string]string{
		"denyra_admin_password": "bootstrap_admin", "navidrome_admin_password": "navidrome_admin",
		"sftpgo_admin_password": "sftpgo_admin", "sftpgo_upload_password": "sftpgo_upload",
	} {
		if got, want := readFile(t, filepath.Join(f.home, "secrets", newName)), legacy[oldName]; got != want {
			t.Errorf("%s=%q want legacy %q", newName, got, want)
		}
	}
}

func readSetupSecrets(t *testing.T, home string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte, len(setupSecretNames))
	for _, name := range setupSecretNames {
		content, err := os.ReadFile(filepath.Join(home, "secrets", name))
		if err != nil {
			t.Fatal(err)
		}
		result[name] = content
	}
	return result
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%#o want=%#o", path, got, want)
	}
}

func TestSetupBuildOrderAndLidarrAPIKeyExtraction(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	f.runSetup()
	log := f.log()
	assertOrderedFragments(t, log, []string{
		"build --pull",
		"up -d --wait --wait-timeout 1 lidarr slskd sftpgo navidrome",
		"--profile setup run --rm reconciler",
		"up -d --remove-orphans --wait",
		"ps",
	})
	key, err := os.ReadFile(filepath.Join(f.home, "secrets", "lidarr_api_key"))
	if err != nil || string(key) != "fixture-lidarr-api-key" {
		t.Fatalf("Lidarr API key=%q err=%v", key, err)
	}
}

func TestSetupRetriesTransientReconciliation(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	cmd := f.setupCommand()
	cmd.Env = append(cmd.Env,
		"DENYRA_TEST_DOCKER_FAIL=reconcile-once",
		"DENYRA_TEST_RECONCILE_MARKER="+filepath.Join(f.home, "reconcile-once"),
		"DENYRA_RECONCILE_RETRY_SECONDS=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup did not recover transient reconciliation: %v\n%s", err, out)
	}
	if got := strings.Count(f.log(), "--profile setup run --rm reconciler"); got != 2 {
		t.Fatalf("reconcile attempts=%d log=\n%s", got, f.log())
	}
}

func TestSetupRetriesTransientFinalStart(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	cmd := f.setupCommand()
	cmd.Env = append(cmd.Env,
		"DENYRA_TEST_DOCKER_FAIL=start-once",
		"DENYRA_TEST_START_MARKER="+filepath.Join(f.home, "start-once"),
		"DENYRA_START_RETRY_SECONDS=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup did not recover transient final start: %v\n%s", err, out)
	}
	if got := strings.Count(f.log(), "up -d --remove-orphans --wait"); got != 2 {
		t.Fatalf("final start attempts=%d log=\n%s", got, f.log())
	}
}

func TestSetupPersistsComposeContextForLaterCommands(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	override := filepath.Join(f.home, "local.compose.yaml")
	cmd := f.setupCommand()
	cmd.Env = append(cmd.Env, "DENYRA_PROJECT_NAME=denyra-local", "DENYRA_COMPOSE_OVERRIDE="+override)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if err := os.WriteFile(f.logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.run("status")
	log := f.log()
	if !strings.Contains(log, "--project-name denyra-local") || !strings.Contains(log, "-f "+override) {
		t.Fatalf("persisted compose context missing:\n%s", log)
	}
}

func TestSetupFailureStopsBeforeUnsafeNextStage(t *testing.T) {
	tests := []struct {
		name      string
		failure   string
		forbidden string
	}{
		{name: "build", failure: "build", forbidden: "up -d --wait"},
		{name: "dependencies", failure: "dependencies", forbidden: "--profile setup run --rm reconciler"},
		{name: "reconcile", failure: "reconcile", forbidden: "up -d --remove-orphans --wait"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagementFixture(t)
			t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
			cmd := f.setupCommand()
			cmd.Env = append(cmd.Env, "DENYRA_TEST_DOCKER_FAIL="+tt.failure)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("setup unexpectedly succeeded: %s", out)
			}
			if strings.Contains(f.log(), tt.forbidden) {
				t.Fatalf("setup continued after %s failure:\n%s", tt.failure, f.log())
			}
		})
	}
}

func TestLidarrAPIKeyMismatchIsRejected(t *testing.T) {
	f := newManagementFixture(t)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, ".denyra-home")) })
	f.runSetup()
	configPath := filepath.Join(f.home, "data", "state", "lidarr", "config.xml")
	if err := os.WriteFile(configPath, []byte("<Config><ApiKey>different-lidarr-api-key</ApiKey></Config>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := f.setupCommand().CombinedOutput()
	if err == nil || !strings.Contains(string(out), "existing Lidarr API key does not match persistent Lidarr state") {
		t.Fatalf("err=%v output=%q", err, out)
	}
}

func assertOrderedFragments(t *testing.T, text string, fragments []string) {
	t.Helper()
	position := 0
	for _, fragment := range fragments {
		next := strings.Index(text[position:], fragment)
		if next < 0 {
			t.Fatalf("missing ordered fragment %q after byte %d:\n%s", fragment, position, text)
		}
		position += next + len(fragment)
	}
}
