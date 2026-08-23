package health_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/health"
)

func TestExternalOutageIsDegradedWithoutBlockingReadiness(t *testing.T) {
	service := health.New()
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyOK, Local: true})
	service.Set(contracts.DependencyHealth{Name: "musicbrainz", State: contracts.DependencyDegraded, Details: "unreachable", Local: false})

	snapshot := service.Snapshot()
	if !snapshot.Live || !snapshot.Ready {
		t.Fatalf("external outage changed process readiness: %+v", snapshot)
	}
}

func TestInternalFailureBlocksReadiness(t *testing.T) {
	service := health.New()
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyOK, Local: true})
	service.Set(contracts.DependencyHealth{Name: "lidarr-internal", State: contracts.DependencyFailed, Details: "unreachable", Local: true})

	if snapshot := service.Snapshot(); snapshot.Ready {
		t.Fatalf("internal failure retained readiness: %+v", snapshot)
	}
}

func TestHealthHandlersUseStatusCodesAndJSON(t *testing.T) {
	service := health.New()
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyFailed, Local: true})
	handler := health.Handler(service)

	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("live status = %d", live.Code)
	}

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d", ready.Code)
	}
	var body contracts.Health
	if err := json.Unmarshal(ready.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ready response: %v", err)
	}
	if body.Ready || !body.Live {
		t.Fatalf("unexpected ready response: %+v", body)
	}

	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/health/unknown", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown health route status = %d", notFound.Code)
	}
}

func TestShutdownChangesLivenessAndReadiness(t *testing.T) {
	service := health.New()
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyOK, Local: true})
	service.Stop()

	snapshot := service.Snapshot()
	if snapshot.Live || snapshot.Ready {
		t.Fatalf("stopped process still reports healthy: %+v", snapshot)
	}

	recorder := httptest.NewRecorder()
	health.Handler(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped live status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}
