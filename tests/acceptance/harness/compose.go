package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type Compose struct {
	Root    string
	Project string
	Env     []string
}

func StartCompose(t *testing.T) Compose {
	t.Helper()
	if os.Getenv("DENYRA_ACCEPTANCE_COMPOSE") != "1" {
		t.Skip("set DENYRA_ACCEPTANCE_COMPOSE=1 to run Compose acceptance tests")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, relative := range []string{
		"downloads/slskd", "downloads/spotiflac", "downloads/other", "incoming/manual",
		"processing/work", "processing/approved", "quarantine", "library", "backups",
		"state/gateway", "state/pipeline", "state/lidarr", "state/slskd", "state/sftpgo",
		"state/navidrome", "cache/navidrome", "secrets", "acceptance",
	} {
		if err := os.MkdirAll(filepath.Join(root, relative), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{
		"internal_bearer": "acceptance-internal-bearer", "audit_key": "acceptance-audit-key-with-entropy",
		"lidarr_api_key": "acceptance-lidarr-key", "bootstrap_admin": "acceptance-password",
		"navidrome_admin": "acceptance-navidrome-password", "sftpgo_admin": "acceptance-sftpgo-admin",
		"sftpgo_upload": "acceptance-sftpgo-upload", "slskd_api_key": "acceptance-slskd-key",
		"slskd_web_password": "acceptance-slskd-password",
		"soulseek_username":  "acceptance-disabled", "soulseek_password": "acceptance-disabled",
		"restic_password": "acceptance-restic-password",
	} {
		if err := os.WriteFile(filepath.Join(root, "secrets", name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	project := "denyra-acceptance-" + strconv.Itoa(os.Getpid())
	environment := append(os.Environ(),
		"DENYRA_HOME="+root,
		"DENYRA_DATA_ROOT="+root,
		"DENYRA_SECRETS_DIR="+filepath.Join(root, "secrets"),
		fmt.Sprintf("DENYRA_MEDIA_UID=%d", os.Getuid()), fmt.Sprintf("DENYRA_MEDIA_GID=%d", os.Getgid()),
	)
	stack := Compose{Root: repository, Project: project, Env: environment}
	t.Cleanup(func() { stack.runCleanup("down", "--volumes", "--remove-orphans") })
	stack.run(t, 3*time.Minute, "up", "-d", "--wait", "--wait-timeout", "180", "acceptance-fixture", "media-pipeline", "acquisition-gateway", "navidrome")
	return stack
}

func (compose Compose) AssertReady(t *testing.T) {
	t.Helper()
	compose.run(t, 30*time.Second, "exec", "-T", "media-pipeline", "/app/media-pipeline", "healthcheck", "--address", "127.0.0.1:8081")
	compose.run(t, 30*time.Second, "exec", "-T", "acquisition-gateway", "/app/acquisition-gateway", "healthcheck", "--address", "127.0.0.1:8081")
}

func (compose Compose) run(t *testing.T, timeout time.Duration, arguments ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append(compose.arguments(), arguments...)...)
	command.Dir = compose.Root
	command.Env = compose.Env
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Compose %v failed: %v\n%s", arguments, err, output)
	}
}

func (compose Compose) runCleanup(arguments ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append(compose.arguments(), arguments...)...)
	command.Dir = compose.Root
	command.Env = compose.Env
	_ = command.Run()
}

func (compose Compose) arguments() []string {
	return []string{"compose", "-p", compose.Project, "-f", "deploy/compose.yaml", "-f", "deploy/compose.acceptance.yaml"}
}

func (compose Compose) value(name string) string {
	prefix := name + "="
	for _, entry := range compose.Env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
