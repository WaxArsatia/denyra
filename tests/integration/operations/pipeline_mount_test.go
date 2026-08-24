package operations_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineUsesSimpleDataMountAndKeepsLibraryReadOnly(t *testing.T) {
	root := repositoryRoot(t)
	compose := readText(t, filepath.Join(root, "deploy/compose.yaml"))
	start := strings.Index(compose, "  media-pipeline:\n")
	end := strings.Index(compose[start:], "\n  lidarr:\n")
	if start < 0 || end < 0 {
		t.Fatal("media-pipeline Compose service not found")
	}
	service := compose[start : start+end]
	if !strings.Contains(service, "source: ${DENYRA_DATA_ROOT:-/data}\n        target: /data") {
		t.Fatal("pipeline raw downloads and processing paths are not on one parent mount")
	}
	if !strings.Contains(service, "source: ${DENYRA_DATA_ROOT:-/data}/library\n        target: /data/library\n        read_only: true") {
		t.Fatal("pipeline library overlay is not read-only")
	}
	for _, separate := range []string{"target: /data/downloads/slskd", "target: /data/processing/work", "target: /data/quarantine"} {
		if strings.Contains(service, separate) {
			t.Fatalf("pipeline uses separate mount %q, breaking atomic rename", separate)
		}
	}
	if strings.Contains(service, "tmpfs:") {
		t.Fatal("pipeline retained state-masking tmpfs entries")
	}
}
