package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIInstallsMediaTestDependencies(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, required := range []string{"sudo apt-get update", "sudo apt-get install -y --no-install-recommends ffmpeg flac"} {
		if !strings.Contains(text, required) {
			t.Errorf("CI workflow does not provide media test dependency %q", required)
		}
	}
}
