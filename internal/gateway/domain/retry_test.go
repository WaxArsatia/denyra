package domain_test

import (
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"testing"
	"time"
)

func TestRetryPoliciesPersistAbsoluteDeterministicDeadlines(t *testing.T) {
	policy := domain.RetryPolicy{Primary: []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}, Fallback: []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}, NoCandidate: 24 * time.Hour}
	now := time.Date(2026, 8, 24, 0, 0, 0, 123, time.UTC)
	deadline, err := policy.PrimaryDeadline(now, 99)
	if err != nil || !deadline.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("primary deadline=%s %v", deadline, err)
	}
	deadline, err = policy.FallbackDeadline(now, 0)
	if err != nil || !deadline.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("fallback=%s %v", deadline, err)
	}
	deadline, err = policy.NoCandidateDeadline(now)
	if err != nil || !deadline.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("no candidate=%s %v", deadline, err)
	}
}
func TestMixedNoResultAndErrorNeverBecomesNoCandidate(t *testing.T) {
	state, err := domain.ClassifyFallback([]domain.ProviderResult{{Provider: "tidal", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"}, {Provider: "qobuz", Outcome: domain.OutcomeRetryableError, Evidence: "network"}})
	if err != nil || state != domain.StateFallbackRetryableError {
		t.Fatalf("mixed=%s %v", state, err)
	}
	state, err = domain.ClassifyFallback([]domain.ProviderResult{{Provider: "tidal", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"}, {Provider: "qobuz", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"}})
	if err != nil || state != domain.StateNoCandidate {
		t.Fatalf("all no result=%s %v", state, err)
	}
}
