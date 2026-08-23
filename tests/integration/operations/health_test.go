package operations_test

import (
	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/health"
	"testing"
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
