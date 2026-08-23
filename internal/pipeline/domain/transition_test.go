package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestTransitionMatrix(t *testing.T) {
	states := []domain.State{
		domain.StateReceived, domain.StateClaimed, domain.StateStabilizing, domain.StateWaitingResubmit,
		domain.StateWorking, domain.StateTechnicalValidation, domain.StateReleaseMatching, domain.StateReviewRequired,
		domain.StateEnriching, domain.StateApproved, domain.StateArbitrationPending, domain.StateImportReady,
		domain.StateImportSubmitted, domain.StateImportReconciling, domain.StateImported, domain.StateQuarantined,
		domain.StateRejected, domain.StateSuperseded, domain.StateCancelled,
	}
	legal := map[[2]domain.State]bool{
		{domain.StateReceived, domain.StateClaimed}:                    true,
		{domain.StateReceived, domain.StateCancelled}:                  true,
		{domain.StateClaimed, domain.StateStabilizing}:                 true,
		{domain.StateClaimed, domain.StateCancelled}:                   true,
		{domain.StateStabilizing, domain.StateWorking}:                 true,
		{domain.StateStabilizing, domain.StateWaitingResubmit}:         true,
		{domain.StateStabilizing, domain.StateQuarantined}:             true,
		{domain.StateStabilizing, domain.StateCancelled}:               true,
		{domain.StateWaitingResubmit, domain.StateReceived}:            true,
		{domain.StateWaitingResubmit, domain.StateCancelled}:           true,
		{domain.StateWorking, domain.StateTechnicalValidation}:         true,
		{domain.StateWorking, domain.StateQuarantined}:                 true,
		{domain.StateWorking, domain.StateCancelled}:                   true,
		{domain.StateTechnicalValidation, domain.StateReleaseMatching}: true,
		{domain.StateTechnicalValidation, domain.StateQuarantined}:     true,
		{domain.StateTechnicalValidation, domain.StateRejected}:        true,
		{domain.StateTechnicalValidation, domain.StateCancelled}:       true,
		{domain.StateReleaseMatching, domain.StateReviewRequired}:      true,
		{domain.StateReleaseMatching, domain.StateEnriching}:           true,
		{domain.StateReleaseMatching, domain.StateQuarantined}:         true,
		{domain.StateReleaseMatching, domain.StateRejected}:            true,
		{domain.StateReleaseMatching, domain.StateCancelled}:           true,
		{domain.StateReviewRequired, domain.StateWorking}:              true,
		{domain.StateReviewRequired, domain.StateRejected}:             true,
		{domain.StateReviewRequired, domain.StateCancelled}:            true,
		{domain.StateEnriching, domain.StateApproved}:                  true,
		{domain.StateEnriching, domain.StateQuarantined}:               true,
		{domain.StateEnriching, domain.StateCancelled}:                 true,
		{domain.StateApproved, domain.StateArbitrationPending}:         true,
		{domain.StateApproved, domain.StateImportReady}:                true,
		{domain.StateApproved, domain.StateSuperseded}:                 true,
		{domain.StateApproved, domain.StateCancelled}:                  true,
		{domain.StateArbitrationPending, domain.StateImportReady}:      true,
		{domain.StateArbitrationPending, domain.StateSuperseded}:       true,
		{domain.StateArbitrationPending, domain.StateCancelled}:        true,
		{domain.StateImportReady, domain.StateImportSubmitted}:         true,
		{domain.StateImportReady, domain.StateSuperseded}:              true,
		{domain.StateImportReady, domain.StateCancelled}:               true,
		{domain.StateImportSubmitted, domain.StateImportReconciling}:   true,
		{domain.StateImportReconciling, domain.StateImported}:          true,
		{domain.StateImportReconciling, domain.StateImportSubmitted}:   true,
		{domain.StateQuarantined, domain.StateReviewRequired}:          true,
		{domain.StateQuarantined, domain.StateRejected}:                true,
		{domain.StateQuarantined, domain.StateCancelled}:               true,
	}
	for _, from := range states {
		for _, to := range states {
			if got, want := domain.CanTransition(from, to), legal[[2]domain.State{from, to}]; got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestTransitionUsesRevisionAndEmitsEvent(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	candidate, err := domain.CreateCandidate(domain.NewCandidate{ID: "candidate-1", Source: domain.SourceManual, ReleaseDirectory: "/data/incoming/manual/submission-1", ConfigSnapshotID: "config-1", AcquisitionEvidenceID: "evidence-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	event, err := candidate.Transition(0, domain.StateClaimed, "admin-1", "sealed submission", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.StateRevision != 1 || event.PreviousRevision != 0 || event.Revision != 1 || event.PreviousState != domain.StateReceived || event.NewState != domain.StateClaimed {
		t.Fatalf("bad transition event: candidate=%+v event=%+v", candidate, event)
	}
	_, err = candidate.Transition(0, domain.StateStabilizing, "worker-1", "claim acquired", now.Add(2*time.Second))
	var stale *domain.StaleRevisionError
	if !errors.As(err, &stale) || stale.Current != 1 || stale.State != domain.StateClaimed {
		t.Fatalf("stale error = %#v", err)
	}
}

func TestTerminalStatesHaveNoOutgoingEdges(t *testing.T) {
	for _, terminal := range []domain.State{domain.StateImported, domain.StateRejected, domain.StateSuperseded, domain.StateCancelled} {
		if !terminal.Terminal() {
			t.Errorf("%s is not terminal", terminal)
		}
		for _, target := range []domain.State{domain.StateReceived, domain.StateWorking, domain.StateCancelled} {
			if domain.CanTransition(terminal, target) {
				t.Errorf("terminal %s transitions to %s", terminal, target)
			}
		}
	}
}

func TestParseStateRejectsUnknownPersistenceValue(t *testing.T) {
	if _, err := domain.ParseState("GUESSED"); err == nil {
		t.Fatal("unknown persisted state accepted")
	}
}
