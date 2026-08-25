package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestRetryUnmanagedRequiresCurrentReviewRevisionAndDoesNotMoveFiles(t *testing.T) {
	now := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	store := &unmanagedReviewStore{candidate: domain.Candidate{ID: "candidate-1", State: domain.StateUnmanagedReview, StateRevision: 7, UpdatedAt: now}}
	moved := false
	service := application.ReviewDecisionService{Store: store, Move: func(string, string) error { moved = true; return errors.New("must not move") }, Now: func() time.Time { return now.Add(time.Second) }}

	if err := service.RetryUnmanaged(context.Background(), "candidate-1", 7, "admin-1", "metadata corrected"); err != nil {
		t.Fatal(err)
	}
	if moved || store.to != domain.StateUnmanagedReady || store.expected != 7 || store.reason != "metadata corrected" {
		t.Fatalf("moved=%t transition=%s expected=%d reason=%q", moved, store.to, store.expected, store.reason)
	}
	if err := service.RetryUnmanaged(context.Background(), "candidate-1", 7, "admin-1", ""); err == nil {
		t.Fatal("empty unmanaged retry reason accepted")
	}
	store.candidate.State = domain.StateReviewRequired
	if err := service.RetryUnmanaged(context.Background(), "candidate-1", 7, "admin-1", "wrong state"); err == nil {
		t.Fatal("managed review candidate accepted by unmanaged retry")
	}
}

type unmanagedReviewStore struct {
	candidate domain.Candidate
	to        domain.State
	expected  uint64
	reason    string
}

func (s *unmanagedReviewStore) Candidate(context.Context, string) (domain.Candidate, error) {
	return s.candidate, nil
}

func (s *unmanagedReviewStore) TransitionCandidate(_ context.Context, _ string, expected uint64, to domain.State, _ string, reason, _ string, at time.Time) (domain.TransitionEvent, error) {
	s.expected, s.to, s.reason = expected, to, reason
	return s.candidate.Transition(expected, to, "admin-1", reason, at)
}
