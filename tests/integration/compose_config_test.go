package integration_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/config"
)

type composeDocument struct {
	Services map[string]composeService `json:"services"`
	Networks map[string]composeNetwork `json:"networks"`
}

type composeService struct {
	Image       string                           `json:"image"`
	Platform    string                           `json:"platform"`
	User        string                           `json:"user"`
	Entrypoint  []string                         `json:"entrypoint"`
	Command     []string                         `json:"command"`
	Environment map[string]string                `json:"environment"`
	Networks    map[string]composeServiceNetwork `json:"networks"`
	Volumes     []composeVolume                  `json:"volumes"`
	Ports       []composePort                    `json:"ports"`
	Healthcheck map[string]any                   `json:"healthcheck"`
}

func TestSlskdLoadsSoulseekCredentialsFromSecretFiles(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	secretDir := t.TempDir()
	usernamePath := filepath.Join(secretDir, "username")
	passwordPath := filepath.Join(secretDir, "password")
	if err := os.WriteFile(usernamePath, []byte("test-user\n"), 0o600); err != nil {
		t.Fatalf("write username secret: %v", err)
	}
	if err := os.WriteFile(passwordPath, []byte("test-password\n"), 0o600); err != nil {
		t.Fatalf("write password secret: %v", err)
	}

	scriptPath := filepath.Join(repoRoot, "deploy", "scripts", "slskd-secret-entrypoint.sh")
	command := exec.Command(
		"bash",
		scriptPath,
		"sh",
		"-c",
		`test "$SLSKD_SLSK_USERNAME" = test-user && test "$SLSKD_SLSK_PASSWORD" = test-password`,
	)
	command.Env = append(
		os.Environ(),
		"SLSKD_SLSK_USERNAME_FILE="+usernamePath,
		"SLSKD_SLSK_PASSWORD_FILE="+passwordPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("load slskd credentials from secret files: %v\n%s", err, output)
	}
}

func TestComposeRunsSlskdSecretEntrypoint(t *testing.T) {
	document := renderCompose(t)
	got := document.Services["slskd"].Entrypoint
	want := []string{
		"/usr/bin/tini",
		"--",
		"/bin/bash",
		"/denyra/slskd-secret-entrypoint.sh",
		"/entrypoint.sh",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("slskd entrypoint = %v, want %v", got, want)
	}
	if got := document.Services["slskd"].Command; !slices.Equal(got, []string{"./slskd"}) {
		t.Fatalf("slskd command = %v, want [./slskd]", got)
	}
}

type composeServiceNetwork struct {
	IPv4Address string `json:"ipv4_address"`
}

type composeNetwork struct {
	Internal bool `json:"internal"`
}

type composeVolume struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type composePort struct {
	Target    int    `json:"target"`
	Published string `json:"published"`
}

func renderCompose(t *testing.T) composeDocument {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	command := exec.Command("docker", "compose", "-f", filepath.Join(repoRoot, "deploy", "compose.yaml"), "config", "--format", "json")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render Compose configuration: %v\n%s", err, output)
	}
	var document composeDocument
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode Compose JSON: %v", err)
	}
	return document
}

func TestComposeTopologyAndListeners(t *testing.T) {
	document := renderCompose(t)
	wantServices := []string{"acquisition-gateway", "lidarr", "media-pipeline", "navidrome", "sftpgo", "slskd"}
	for _, name := range wantServices {
		service, ok := document.Services[name]
		if !ok {
			t.Errorf("missing service %q", name)
			continue
		}
		if service.Platform != "linux/amd64" {
			t.Errorf("service %s platform = %q", name, service.Platform)
		}
		if !strings.Contains(service.Image, "@sha256:") {
			t.Errorf("service %s image is not digest-pinned: %q", name, service.Image)
		}
		if len(service.Healthcheck) == 0 {
			t.Errorf("service %s has no healthcheck", name)
		}
	}

	control, ok := document.Networks["denyra-control"]
	if !ok || !control.Internal {
		t.Fatalf("denyra-control must exist and be internal")
	}
	var members []string
	for name, service := range document.Services {
		if _, joined := service.Networks["denyra-control"]; joined {
			members = append(members, name)
		}
	}
	slices.Sort(members)
	if !slices.Equal(members, []string{"acquisition-gateway", "media-pipeline"}) {
		t.Errorf("denyra-control members = %v", members)
	}
	if got := document.Services["acquisition-gateway"].Networks["denyra-control"].IPv4Address; got != "172.30.0.2" {
		t.Errorf("gateway control address = %q", got)
	}
	if got := document.Services["media-pipeline"].Networks["denyra-control"].IPv4Address; got != "172.30.0.3" {
		t.Errorf("pipeline control address = %q", got)
	}
	if got := document.Services["acquisition-gateway"].Environment["DENYRA_HTTP_INTERNAL_ADDRESS"]; got != "172.30.0.2:8081" {
		t.Errorf("gateway internal listener = %q", got)
	}
	if got := document.Services["acquisition-gateway"].Environment["DENYRA_HTTP_ACQUISITION_EVENT_ADDRESS"]; got != "0.0.0.0:8082" {
		t.Errorf("gateway acquisition event listener = %q", got)
	}
	if got := document.Services["media-pipeline"].Environment["DENYRA_HTTP_INTERNAL_ADDRESS"]; got != "172.30.0.3:8081" {
		t.Errorf("pipeline internal listener = %q", got)
	}
	if got := document.Services["media-pipeline"].Environment["DENYRA_HTTP_ADMIN_ADDRESS"]; got != "0.0.0.0:8090" {
		t.Errorf("pipeline admin listener = %q", got)
	}
	if got := document.Services["media-pipeline"].Ports; len(got) != 1 || got[0].Target != 8090 {
		t.Errorf("pipeline published ports = %+v", got)
	}
	for _, name := range []string{"acquisition-gateway", "lidarr", "slskd"} {
		if ports := document.Services[name].Ports; len(ports) != 0 {
			t.Errorf("service %s unexpectedly publishes ports: %+v", name, ports)
		}
	}
}

func TestSFTPGoUsesNativeHealthcheck(t *testing.T) {
	document := renderCompose(t)
	values, ok := document.Services["sftpgo"].Healthcheck["test"].([]any)
	if !ok {
		t.Fatalf("SFTPGo healthcheck test has type %T", document.Services["sftpgo"].Healthcheck["test"])
	}
	got := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("SFTPGo healthcheck value has type %T", value)
		}
		got = append(got, text)
	}
	want := []string{"CMD", "sftpgo", "ping"}
	if !slices.Equal(got, want) {
		t.Fatalf("SFTPGo healthcheck = %v, want %v", got, want)
	}
}

func TestComposeUsesDerivedImageLock(t *testing.T) {
	document := renderCompose(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	lockBytes, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "images.lock.json"))
	if err != nil {
		t.Fatalf("read deployment image lock: %v", err)
	}
	var lock struct {
		DependenciesLockSHA256 string `json:"dependencies_lock_sha256"`
		Images                 []struct {
			ID        string `json:"id"`
			Reference string `json:"reference"`
			Platform  string `json:"platform"`
		} `json:"images"`
	}
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		t.Fatalf("decode deployment image lock: %v", err)
	}
	dependencyBytes, err := os.ReadFile(filepath.Join(repoRoot, "dependencies.lock.json"))
	if err != nil {
		t.Fatalf("read dependency lock: %v", err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(dependencyBytes)); lock.DependenciesLockSHA256 != want {
		t.Fatalf("deployment image lock references dependency lock %q, want %q", lock.DependenciesLockSHA256, want)
	}
	serviceByID := map[string]string{
		"acquisition-gateway": "acquisition-gateway",
		"lidarr-derived":      "lidarr",
		"media-pipeline":      "media-pipeline",
		"navidrome-derived":   "navidrome",
	}
	for _, image := range lock.Images {
		serviceName, ok := serviceByID[image.ID]
		if !ok {
			t.Errorf("unknown derived image ID %q", image.ID)
			continue
		}
		service := document.Services[serviceName]
		if service.Image != image.Reference || service.Platform != image.Platform {
			t.Errorf("service %s does not match deployment image lock", serviceName)
		}
		if os.Getenv("TEST_DOCKER_IMAGES") == "1" {
			if output, err := exec.Command("docker", "image", "inspect", image.Reference).CombinedOutput(); err != nil {
				t.Errorf("locked image %s is not locally resolvable: %v\n%s", image.Reference, err, output)
			}
		}
	}
	var dependencyLock struct {
		Images []struct {
			ID        string `json:"id"`
			Reference string `json:"reference"`
			Platform  string `json:"platform"`
		} `json:"images"`
	}
	if err := json.Unmarshal(dependencyBytes, &dependencyLock); err != nil {
		t.Fatalf("decode dependency lock: %v", err)
	}
	serviceByDependencyID := map[string]string{"slskd": "slskd", "sftpgo": "sftpgo"}
	for _, image := range dependencyLock.Images {
		serviceName, used := serviceByDependencyID[image.ID]
		if !used {
			continue
		}
		service := document.Services[serviceName]
		if service.Image != image.Reference || service.Platform != image.Platform {
			t.Errorf("service %s does not match dependency lock", serviceName)
		}
	}
}

func TestComposeMountOwnership(t *testing.T) {
	document := renderCompose(t)
	assertMount := func(serviceName, target string, readOnly bool) {
		t.Helper()
		for _, mount := range document.Services[serviceName].Volumes {
			if mount.Target == target {
				if mount.ReadOnly != readOnly {
					t.Errorf("%s mount %s read_only = %v", serviceName, target, mount.ReadOnly)
				}
				return
			}
		}
		t.Errorf("%s missing mount %s", serviceName, target)
	}
	assertNoMount := func(serviceName, target string) {
		t.Helper()
		for _, mount := range document.Services[serviceName].Volumes {
			if mount.Target == target {
				t.Errorf("%s must not mount %s", serviceName, target)
			}
		}
	}

	assertMount("lidarr", "/data/processing/approved", false)
	assertMount("lidarr", "/data/library", false)
	assertMount("slskd", "/data/downloads/slskd", false)
	assertMount("acquisition-gateway", "/data/downloads/spotiflac", false)
	assertNoMount("acquisition-gateway", "/data/downloads/slskd")
	assertMount("media-pipeline", "/data", false)
	assertMount("media-pipeline", "/data/library", true)
	assertMount("sftpgo", "/data/incoming/manual", false)
	assertMount("navidrome", "/music", true)
	for _, service := range []string{"acquisition-gateway", "slskd", "sftpgo"} {
		assertNoMount(service, "/data/library")
		assertNoMount(service, "/music")
	}
	for _, serviceName := range []string{"acquisition-gateway", "media-pipeline", "slskd", "sftpgo", "navidrome"} {
		if got := document.Services[serviceName].User; got != "1000:1000" {
			t.Errorf("service %s user = %q, want 1000:1000", serviceName, got)
		}
	}
	if lidarr := document.Services["lidarr"].Environment; lidarr["PUID"] != "1000" || lidarr["PGID"] != "1000" {
		t.Errorf("Lidarr media ownership IDs are not locked")
	}
}

func TestComposePurposeNetworksCannotReachControlListeners(t *testing.T) {
	document := renderCompose(t)
	wantMembers := map[string][]string{
		"denyra-acquisition": {"acquisition-gateway", "lidarr", "slskd"},
		"denyra-import":      {"lidarr", "media-pipeline"},
		"denyra-playback":    {"navidrome"},
		"denyra-upload":      {"sftpgo"},
	}
	for networkName, want := range wantMembers {
		var got []string
		for serviceName, service := range document.Services {
			if _, joined := service.Networks[networkName]; joined {
				got = append(got, serviceName)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("network %s members = %v, want %v", networkName, got, want)
		}
	}
	for _, serviceName := range []string{"lidarr", "slskd", "sftpgo", "navidrome"} {
		if _, joined := document.Services[serviceName].Networks["denyra-control"]; joined {
			t.Errorf("%s can route directly to control listeners", serviceName)
		}
	}
	if _, joined := document.Services["media-pipeline"].Networks["denyra-acquisition"]; joined {
		t.Error("pipeline must not join the acquisition network")
	}
	if _, joined := document.Services["acquisition-gateway"].Networks["denyra-import"]; joined {
		t.Error("gateway must not join the import network")
	}
}

func TestComposeVersionGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	binDir := t.TempDir()
	fakeDocker := `#!/bin/sh
case "$1 $2" in
  "version --format") printf '%s\n' "${TEST_ENGINE_VERSION}" ;;
  "compose version") printf '%s\n' "${TEST_COMPOSE_VERSION}" ;;
  "buildx version") printf 'github.com/docker/buildx %s 0000000\n' "${TEST_BUILDX_VERSION}" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	run := func(engine, compose, buildx string) ([]byte, error) {
		command := exec.Command(filepath.Join(repoRoot, "scripts", "check-compose.sh"))
		command.Env = append(os.Environ(),
			"PATH="+binDir+":"+os.Getenv("PATH"),
			"TEST_ENGINE_VERSION="+engine,
			"TEST_COMPOSE_VERSION="+compose,
			"TEST_BUILDX_VERSION="+buildx,
		)
		return command.CombinedOutput()
	}
	if output, err := run("29.6.2", "v2.40.3", "v0.36.1"); err != nil {
		t.Fatalf("exact toolchain rejected: %v\n%s", err, output)
	}
	output, err := run("29.7.2", "v5.5.0", "v0.36.1")
	if err == nil {
		t.Fatal("mismatched Engine and Compose versions accepted")
	}
	if !strings.Contains(string(output), "approved Compose-v2 compatibility exception") {
		t.Fatalf("mismatch output omits compatibility exception: %s", output)
	}
}

func TestComposeServiceConfigsAreValid(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, serviceName := range []string{"gateway", "pipeline"} {
		path := filepath.Join(repoRoot, "deploy", "config", serviceName+".toml")
		cfg, err := config.Load(path, nil)
		if err != nil {
			t.Fatalf("load %s config: %v", serviceName, err)
		}
		if cfg.HTTP.InternalAddress == "0.0.0.0:8081" || cfg.HTTP.InternalAddress == ":8081" {
			t.Errorf("%s internal listener uses wildcard address", serviceName)
		}
	}
}
