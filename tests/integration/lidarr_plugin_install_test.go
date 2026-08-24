package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLidarrPluginInstallerUsesOwnerAndPluginDirectory(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	temporary := t.TempDir()
	source := filepath.Join(temporary, "source")
	config := filepath.Join(temporary, "config")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatalf("create plugin source: %v", err)
	}
	if err := os.MkdirAll(config, 0o750); err != nil {
		t.Fatalf("create Lidarr config root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.dll"), []byte("fixture"), 0o440); err != nil {
		t.Fatalf("write plugin fixture: %v", err)
	}
	image := "debian:stable-slim"
	t.Cleanup(func() {
		cleanup := exec.Command(
			"docker", "run", "--rm",
			"--entrypoint", "/bin/chmod",
			"--mount", "type=bind,source="+config+",target=/config",
			image, "-R", "a+rwx", "/config",
		)
		if output, err := cleanup.CombinedOutput(); err != nil {
			t.Errorf("restore test directory permissions: %v\n%s", err, output)
		}
	})

	command := exec.Command(
		"docker", "run", "--rm",
		"--entrypoint", "/bin/sh",
		"--mount", "type=bind,source="+filepath.Join(repoRoot, "deploy", "docker", "lidarr-install-plugin.sh")+",target=/install.sh,readonly",
		"--mount", "type=bind,source="+source+",target=/defaults/denyra-plugins/Lidarr.Plugin.Slskd,readonly",
		"--mount", "type=bind,source="+config+",target=/config",
		image,
		"/install.sh",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run Lidarr plugin installer: %v\n%s", err, output)
	}
	want := filepath.Join(config, "plugins", "allquiet-hub", "Lidarr.Plugin.Slskd", "plugin.dll")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("plugin was not installed at owner/plugin path: %v", err)
	}
	legacy := filepath.Join(config, "plugins", "Lidarr.Plugin.Slskd")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy ownerless plugin path still exists: %v", err)
	}
}
