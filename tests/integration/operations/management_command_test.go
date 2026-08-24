package operations_test

import (
	"bytes"
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
case "$*" in "compose version"*) exit 0;; esac
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
	out, err := f.command("setup").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "command unavailable in this checkout") {
		t.Fatalf("err=%v output=%q", err, out)
	}
	if _, err := os.Stat(f.home); !os.IsNotExist(err) {
		t.Fatalf("dispatcher unexpectedly created deployment root: %v", err)
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
