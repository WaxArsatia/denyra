package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var ErrUnstableRelease = errors.New("release tree is not stable")
var ErrWaitingResubmit = errors.New("sealed manual submission changed; resubmit required")

type CompletionEvidence struct {
	ID                string
	Source            domain.Source
	SourceRoot        string
	CompletedPath     string
	CompletedAt       time.Time
	SealedFingerprint string
}

type PauseFunc func(context.Context, time.Duration) error

type ClaimService struct {
	WorkRoot          string
	LockRoot          string
	StabilityInterval time.Duration
	Pause             PauseFunc
	Scan              func(string) (denyrafs.Tree, error)
	Move              func(string, string) error
}

type ClaimResult struct {
	WorkPath    string
	Fingerprint string
	Entries     []denyrafs.Entry
}

func (s ClaimService) Claim(ctx context.Context, candidateID string, evidence CompletionEvidence) (ClaimResult, error) {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return ClaimResult{}, err
	}
	if evidence.ID == "" || !evidence.Source.Valid() || evidence.CompletedAt.IsZero() {
		return ClaimResult{}, fmt.Errorf("durable completion evidence is incomplete")
	}
	if err := validateContainedPath(evidence.SourceRoot, evidence.CompletedPath); err != nil {
		return ClaimResult{}, err
	}
	if s.StabilityInterval <= 0 || s.Pause == nil {
		return ClaimResult{}, fmt.Errorf("stability policy is not configured")
	}
	if s.Scan == nil {
		s.Scan = denyrafs.Scan
	}
	if s.Move == nil {
		s.Move = denyrafs.MoveAtomic
	}
	if err := os.MkdirAll(s.LockRoot, 0o700); err != nil {
		return ClaimResult{}, err
	}
	lock, err := denyrafs.AcquireLock(filepath.Join(s.LockRoot, candidateID+".lock"))
	if err != nil {
		return ClaimResult{}, err
	}
	defer lock.Close()

	first, err := s.Scan(evidence.CompletedPath)
	if err != nil {
		return ClaimResult{}, err
	}
	if err := s.Pause(ctx, s.StabilityInterval); err != nil {
		return ClaimResult{}, err
	}
	second, err := s.Scan(evidence.CompletedPath)
	if err != nil {
		return ClaimResult{}, err
	}
	if first.Fingerprint != second.Fingerprint {
		if evidence.Source == domain.SourceManual && evidence.SealedFingerprint != "" {
			return ClaimResult{}, ErrWaitingResubmit
		}
		return ClaimResult{}, ErrUnstableRelease
	}
	if evidence.Source == domain.SourceManual && evidence.SealedFingerprint != second.Fingerprint {
		return ClaimResult{}, ErrWaitingResubmit
	}
	target := filepath.Join(s.WorkRoot, candidateID)
	if err := os.MkdirAll(s.WorkRoot, 0o750); err != nil {
		return ClaimResult{}, err
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		if err == nil {
			return ClaimResult{}, fmt.Errorf("claim target already exists")
		}
		return ClaimResult{}, err
	}
	final, err := s.Scan(evidence.CompletedPath)
	if err != nil {
		return ClaimResult{}, err
	}
	if final.Fingerprint != second.Fingerprint {
		return ClaimResult{}, ErrUnstableRelease
	}
	if err := s.Move(evidence.CompletedPath, target); err != nil {
		return ClaimResult{}, err
	}
	moved, err := s.Scan(target)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("verify moved release: %w", err)
	}
	if moved.Fingerprint != final.Fingerprint {
		return ClaimResult{}, fmt.Errorf("%w: release identity changed during atomic move", ErrUnstableRelease)
	}
	return ClaimResult{WorkPath: target, Fingerprint: moved.Fingerprint, Entries: moved.Entries}, nil
}

func validateContainedPath(root, target string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return fmt.Errorf("source root and completion path must be absolute canonical paths")
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		return fmt.Errorf("completion path is outside its authorized source root")
	}
	return nil
}
