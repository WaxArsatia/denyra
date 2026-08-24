package integration_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildProvenanceDerivesCanonicalLockedInputs(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	output := filepath.Join(t.TempDir(), "build-provenance.json")
	command := exec.Command(filepath.Join(repoRoot, "scripts", "verify-pins", "build-provenance.sh"), "--lock", filepath.Join(repoRoot, "dependencies.lock.json"), "--output", output, "--service", "media-pipeline")
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build-provenance: %v\n%s", err, combined)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	var provenance struct {
		Service    string           `json:"service"`
		LockSHA256 string           `json:"lock_sha256"`
		Images     []map[string]any `json:"images"`
		Artifacts  []map[string]any `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	if provenance.Service != "media-pipeline" || len(provenance.LockSHA256) != 64 {
		t.Fatalf("invalid provenance header: %+v", provenance)
	}
	if len(provenance.Images) != 6 || len(provenance.Artifacts) < 10 {
		t.Fatalf("locked inputs missing: images=%d artifacts=%d", len(provenance.Images), len(provenance.Artifacts))
	}
}

func TestBakeTargetsUseRepositoryBuildContext(t *testing.T) {
	t.Parallel()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	command := exec.Command("docker", "buildx", "bake", "--file", filepath.Join(repoRoot, "deploy", "docker", "docker-bake.hcl"), "--print")
	command.Dir = repoRoot
	output, err := command.Output()
	if err != nil {
		t.Fatalf("bake --print: %v", err)
	}
	var definition struct {
		Target map[string]struct {
			Context string            `json:"context"`
			Labels  map[string]string `json:"labels"`
		} `json:"target"`
	}
	if err := json.Unmarshal(output, &definition); err != nil {
		t.Fatalf("decode bake definition: %v", err)
	}
	for name, target := range definition.Target {
		if target.Context != "." {
			t.Errorf("target %s context = %q, want repository root", name, target.Context)
		}
	}
	lockBytes, err := os.ReadFile(filepath.Join(repoRoot, "dependencies.lock.json"))
	if err != nil {
		t.Fatalf("read dependency lock: %v", err)
	}
	lockSHA256 := fmt.Sprintf("%x", sha256.Sum256(lockBytes))
	for _, name := range []string{"gateway", "pipeline"} {
		if got := definition.Target[name].Labels["io.denyra.dependencies-lock.sha256"]; got != lockSHA256 {
			t.Errorf("target %s lock label = %q, want %q", name, got, lockSHA256)
		}
	}
}

func TestImageBuildEntryPointsDisableNondeterministicDefaultAttestations(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, relative := range []string{
		".github/workflows/ci.yml",
		"docs/runbooks/install.md",
		"scripts/upgrade/verify-update.sh",
	} {
		content, err := os.ReadFile(filepath.Join(repoRoot, relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		if strings.Contains(string(content), "docker buildx bake") && !strings.Contains(string(content), "BUILDX_NO_DEFAULT_ATTESTATIONS=1 docker buildx bake") {
			t.Errorf("%s can emit nondeterministic default provenance attestations", relative)
		}
	}
}

func TestRuntimeImageProvenanceMatchesLock(t *testing.T) {
	if os.Getenv("TEST_DOCKER_IMAGES") != "1" {
		t.Skip("set TEST_DOCKER_IMAGES=1 after building the runtime images")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	lockBytes, err := os.ReadFile(filepath.Join(repoRoot, "dependencies.lock.json"))
	if err != nil {
		t.Fatalf("read dependency lock: %v", err)
	}
	var lock map[string]any
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		t.Fatalf("decode dependency lock: %v", err)
	}
	lockSHA256 := fmt.Sprintf("%x", sha256.Sum256(lockBytes))

	for _, image := range []string{"denyra/acquisition-gateway:local", "denyra/media-pipeline:local"} {
		t.Run(strings.TrimPrefix(image, "denyra/"), func(t *testing.T) {
			labelsJSON, err := exec.Command("docker", "image", "inspect", "--format", "{{json .Config.Labels}}", image).Output()
			if err != nil {
				t.Fatalf("inspect image labels: %v", err)
			}
			var labels map[string]string
			if err := json.Unmarshal(labelsJSON, &labels); err != nil {
				t.Fatalf("decode image labels: %v", err)
			}
			if got := labels["io.denyra.dependencies-lock.sha256"]; got != lockSHA256 {
				t.Fatalf("lock label = %q, want %q", got, lockSHA256)
			}
			if got := labels["io.denyra.target-platform"]; got != "linux/amd64" {
				t.Fatalf("platform label = %q, want linux/amd64", got)
			}

			provenanceJSON, err := exec.Command("docker", "run", "--rm", "--entrypoint", "cat", image, "/app/build-provenance.json").Output()
			if err != nil {
				t.Fatalf("read embedded build provenance: %v", err)
			}
			var provenance map[string]any
			if err := json.Unmarshal(provenanceJSON, &provenance); err != nil {
				t.Fatalf("decode build provenance: %v", err)
			}
			if got := provenance["lock_sha256"]; got != lockSHA256 {
				t.Fatalf("embedded lock hash = %v, want %s", got, lockSHA256)
			}
			for _, key := range []string{"images", "artifacts", "registries", "components"} {
				if !reflect.DeepEqual(provenance[key], lock[key]) {
					t.Errorf("embedded %s differ from dependencies.lock.json", key)
				}
			}
		})
	}
}

func TestPipelineImageRunsPinnedBeets(t *testing.T) {
	if os.Getenv("TEST_DOCKER_IMAGES") != "1" {
		t.Skip("set TEST_DOCKER_IMAGES=1 after building the runtime images")
	}
	output, err := exec.Command("docker", "run", "--rm", "--entrypoint", "/opt/python/bin/beet", "denyra/media-pipeline:local", "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run Beets in pipeline image: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "2.13.1") {
		t.Fatalf("Beets version output missing locked version: %s", output)
	}
}
