package integration_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
)

func TestServiceImagesStartReadyWithExternalDependenciesDegraded(t *testing.T) {
	if os.Getenv("DENYRA_TEST_IMAGE_SMOKE") != "1" {
		t.Skip("set DENYRA_TEST_IMAGE_SMOKE=1 after building local images")
	}
	for _, fixture := range []struct {
		name      string
		image     string
		database  string
		externals []string
	}{
		{name: "gateway", image: "docker.io/denyra/acquisition-gateway:local", database: "gateway", externals: []string{"soulseek", "spotiflac-providers"}},
		{name: "pipeline", image: "docker.io/denyra/media-pipeline:local", database: "pipeline", externals: []string{"musicbrainz", "lrclib"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			runServiceImageSmoke(t, fixture.image, fixture.database, fixture.externals)
		})
	}
}

func runServiceImageSmoke(t *testing.T, image, database string, externalNames []string) {
	t.Helper()
	root := t.TempDir()
	for _, relative := range []string{
		"downloads/slskd", "downloads/spotiflac", "downloads/other", "incoming/manual",
		"processing/work", "processing/approved", "quarantine", "library",
		"state/gateway", "state/pipeline", "secrets",
	} {
		if err := os.MkdirAll(filepath.Join(root, relative), 0o750); err != nil {
			t.Fatalf("create %s: %v", relative, err)
		}
	}
	for name, value := range map[string]string{
		"internal_bearer": "internal-bearer-fixture", "audit_key": "audit-key-with-adequate-entropy", "lidarr_api_key": "lidarr-key-fixture",
		"bootstrap_admin": "fixture-password",
	} {
		if err := os.WriteFile(filepath.Join(root, "secrets", name), []byte(value), 0o600); err != nil {
			t.Fatalf("write secret %s: %v", name, err)
		}
	}
	config := fmt.Sprintf(`[http]
admin_address = "0.0.0.0:8090"
internal_address = "0.0.0.0:8081"

[database]
gateway_path = "/data/state/gateway/denyra.db"
pipeline_path = "/data/state/pipeline/denyra.db"

[secrets.internal_bearer]
source = "file"
name = "/data/secrets/internal_bearer"

[secrets.audit_key]
source = "file"
name = "/data/secrets/audit_key"

[secrets.lidarr_api_key]
source = "file"
name = "/data/secrets/lidarr_api_key"

[secrets.bootstrap_admin]
source = "file"
name = "/data/secrets/bootstrap_admin"
`)
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	port := freePort(t)
	name := fmt.Sprintf("denyra-smoke-%s-%d", database, time.Now().UnixNano())
	command := exec.Command("docker", "run", "--detach", "--name", name, "--read-only", "--tmpfs", "/tmp:mode=1777",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--publish", fmt.Sprintf("127.0.0.1:%d:8081", port),
		"--mount", fmt.Sprintf("type=bind,src=%s,dst=/data", root), image, "--config", "/data/config.toml")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start %s: %v\n%s", image, err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	var snapshot contracts.Health
	for time.Now().Before(deadline) {
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health/ready", port))
		if err == nil {
			err = json.NewDecoder(response.Body).Decode(&snapshot)
			_ = response.Body.Close()
			if err == nil && response.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !snapshot.Live || !snapshot.Ready {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("%s did not become ready: %+v\n%s", image, snapshot, logs)
	}
	states := make(map[string]contracts.DependencyState)
	for _, dependency := range snapshot.Dependencies {
		states[dependency.Name] = dependency.State
	}
	for _, external := range externalNames {
		if states[external] != contracts.DependencyDegraded {
			t.Fatalf("external %s state = %q, want degraded; all=%v", external, states[external], states)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
