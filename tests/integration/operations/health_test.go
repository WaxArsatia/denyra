package operations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/health"
)

func TestHealthLocalFailureBlocksAndExternalOutageOnlyDegrades(t *testing.T) {
	service := health.New()
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyOK, Local: true})
	service.Set(contracts.DependencyHealth{Name: "musicbrainz", State: contracts.DependencyDegraded, Local: false})
	if snapshot := service.Snapshot(); !snapshot.Ready {
		t.Fatalf("external outage blocked readiness: %+v", snapshot)
	}
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyFailed, Local: true})
	if snapshot := service.Snapshot(); snapshot.Ready {
		t.Fatalf("local failure did not block readiness: %+v", snapshot)
	}
}

func TestHealthCommandsUseLoopbackDefaults(t *testing.T) {
	root := repositoryRoot(t)
	for _, path := range []string{"cmd/acquisition-gateway/main.go", "cmd/media-pipeline/main.go"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `"127.0.0.1:8081"`) || strings.Contains(text, "172.30.0.") {
			t.Errorf("%s health default is not loopback", path)
		}
	}
}
