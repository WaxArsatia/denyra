package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type CandidateStateStore interface {
	Candidate(context.Context, string) (domain.Candidate, error)
	TransitionCandidate(context.Context, string, uint64, domain.State, string, string, string, time.Time) (domain.TransitionEvent, error)
}

type ReviewDecisionService struct {
	Store          CandidateStateStore
	WorkRoot       string
	QuarantineRoot string
	Move           func(string, string) error
	Now            func() time.Time
}

func (s ReviewDecisionService) Approve(ctx context.Context, candidateID string, expected uint64, actor, targetReleaseMBID, reason string) error {
	if _, err := domain.CanonicalMBID(targetReleaseMBID); err != nil {
		return err
	}
	return s.returnToWork(ctx, candidateID, expected, actor, reason, targetReleaseMBID)
}

func (s ReviewDecisionService) Retry(ctx context.Context, candidateID string, expected uint64, actor, reason string) error {
	return s.returnToWork(ctx, candidateID, expected, actor, reason, "")
}

func (s ReviewDecisionService) Reject(ctx context.Context, candidateID string, expected uint64, actor, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("decision reason is required")
	}
	_, err := s.Store.TransitionCandidate(ctx, candidateID, expected, domain.StateRejected, actor, reason, "", s.now())
	return err
}

func (s ReviewDecisionService) Cancel(ctx context.Context, candidateID string, expected uint64, actor, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("decision reason is required")
	}
	_, err := s.Store.TransitionCandidate(ctx, candidateID, expected, domain.StateCancelled, actor, reason, "", s.now())
	return err
}

func (s ReviewDecisionService) returnToWork(ctx context.Context, candidateID string, expected uint64, actor, reason, target string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("decision reason is required")
	}
	if s.Store == nil || s.WorkRoot == "" || s.QuarantineRoot == "" {
		return fmt.Errorf("review service is not configured")
	}
	now := s.now()
	candidate, err := s.Store.Candidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if _, err := candidate.Transition(expected, domain.StateWorking, actor, reason, now); err != nil {
		return err
	}
	move := s.Move
	if move == nil {
		move = denyrafs.MoveAtomic
	}
	if err := os.MkdirAll(s.WorkRoot, 0o750); err != nil {
		return err
	}
	source := filepath.Join(s.QuarantineRoot, candidateID)
	destination := filepath.Join(s.WorkRoot, candidateID)
	if err := move(source, destination); err != nil {
		return fmt.Errorf("return approved candidate to work: %w", err)
	}
	if _, err := s.Store.TransitionCandidate(ctx, candidateID, expected, domain.StateWorking, actor, reason, target, now); err != nil {
		if rollbackErr := move(destination, source); rollbackErr != nil {
			return fmt.Errorf("persist review decision: %v; rollback candidate move: %w", err, rollbackErr)
		}
		return err
	}
	return nil
}

func (s ReviewDecisionService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
