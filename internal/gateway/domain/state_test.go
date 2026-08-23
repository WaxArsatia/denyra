package domain_test

import (
	"errors"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"testing"
	"time"
)

const releaseGroup = "12345678-1234-1234-1234-123456789abc"

func TestAcquisitionStateUsesStableDedupAndOptimisticTransitions(t *testing.T) {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	job, err := domain.NewJob("job-1", 42, releaseGroup, "config-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if job.DedupKey() != "42:"+releaseGroup {
		t.Fatalf("dedup=%s", job.DedupKey())
	}
	event, err := job.Transition(0, domain.StatePrimarySearchRequested, "worker", "wanted discovered", now.Add(time.Second))
	if err != nil || event.Revision != 1 {
		t.Fatalf("transition=%+v %v", event, err)
	}
	_, err = job.Transition(0, domain.StateCancelled, "admin", "stale", now.Add(2*time.Second))
	var stale *domain.StaleRevisionError
	if !errors.As(err, &stale) {
		t.Fatalf("stale=%v", err)
	}
	if _, err := job.Transition(1, domain.StateNoCandidate, "worker", "illegal", now.Add(3*time.Second)); err == nil {
		t.Fatal("illegal transition accepted")
	}
}
func TestAcquisitionStatesAreClosedAndTerminal(t *testing.T) {
	if _, err := domain.ParseState("UNKNOWN"); err == nil {
		t.Fatal("unknown state accepted")
	}
	if !domain.StateHandedOff.Terminal() || !domain.StateCancelled.Terminal() {
		t.Fatal("terminal state classification wrong")
	}
}
