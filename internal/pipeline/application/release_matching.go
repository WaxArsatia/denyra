package application

import (
	"fmt"
	"os"
	"path/filepath"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type MatchingService struct {
	DurationPolicy domain.DurationPolicy
	WorkRoot       string
	QuarantineRoot string
	Move           func(string, string) error
}

type MatchingDecision struct {
	Match     domain.ReleaseMatch
	State     domain.State
	FilePath  string
	Reason    string
	Retryable bool
}

func (s MatchingService) Evaluate(candidateID, explicitReleaseMBID string, release domain.CanonicalRelease, tracks []domain.CandidateTrack) (MatchingDecision, error) {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return MatchingDecision{}, err
	}
	match, err := domain.MatchRelease(s.DurationPolicy, explicitReleaseMBID, release, tracks)
	if err != nil {
		return s.quarantine(candidateID, domain.StateReviewRequired, "ambiguous or inconsistent release match: "+err.Error(), domain.ReleaseMatch{})
	}
	switch match.Status {
	case domain.DurationAutoApprove:
		return MatchingDecision{Match: match, State: domain.StateEnriching, FilePath: filepath.Join(s.WorkRoot, candidateID)}, nil
	case domain.DurationManualReview:
		return s.quarantine(candidateID, domain.StateReviewRequired, "duration or reference requires manual review", match)
	case domain.DurationReject:
		return s.quarantine(candidateID, domain.StateRejected, "duration exceeds manual review threshold", match)
	default:
		return MatchingDecision{}, fmt.Errorf("unknown release duration status %q", match.Status)
	}
}

func (s MatchingService) ApproveReview(candidateID, targetReleaseMBID, reason string) (string, error) {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return "", err
	}
	if _, err := domain.CanonicalMBID(targetReleaseMBID); err != nil {
		return "", err
	}
	if reason == "" {
		return "", fmt.Errorf("approval reason is required")
	}
	move := s.Move
	if move == nil {
		move = denyrafs.MoveAtomic
	}
	source, target := filepath.Join(s.QuarantineRoot, candidateID), filepath.Join(s.WorkRoot, candidateID)
	if err := os.MkdirAll(s.WorkRoot, 0o750); err != nil {
		return "", err
	}
	if err := move(source, target); err != nil {
		return "", err
	}
	return target, nil
}

func (s MatchingService) quarantine(candidateID string, state domain.State, reason string, match domain.ReleaseMatch) (MatchingDecision, error) {
	move := s.Move
	if move == nil {
		move = denyrafs.MoveAtomic
	}
	source, target := filepath.Join(s.WorkRoot, candidateID), filepath.Join(s.QuarantineRoot, candidateID)
	if err := os.MkdirAll(s.QuarantineRoot, 0o750); err != nil {
		return MatchingDecision{}, err
	}
	if err := move(source, target); err != nil {
		return MatchingDecision{}, err
	}
	return MatchingDecision{Match: match, State: state, FilePath: target, Reason: reason}, nil
}
