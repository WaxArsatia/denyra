package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type RecoveryCandidate struct {
	ID    string
	State domain.State
	Path  string
}
type ExpiredLease struct {
	ResourceType, ResourceID string
	ExpiresAt                time.Time
}
type RecoveryFinding struct {
	Kind, ResourceID, Path, Classification string
	Details                                []byte
	ObservedAt                             time.Time
}
type UnresolvedEffect struct{ Kind, ResourceID string }
type RecoveryStore interface {
	RecoveryCandidates(context.Context) ([]RecoveryCandidate, error)
	ExpiredLeases(context.Context, time.Time) ([]ExpiredLease, error)
	DeleteExpiredLease(context.Context, string, string, time.Time) error
	AppendRecoveryFinding(context.Context, RecoveryFinding) error
	UnresolvedEffects(context.Context) ([]UnresolvedEffect, error)
}
type RecoveryService struct {
	Store                                  RecoveryStore
	WorkRoot, ApprovedRoot, QuarantineRoot string
	Now                                    func() time.Time
}
type RecoveryReport struct {
	ExpiredLeases      int
	OrphanDirectories  int
	MissingDirectories int
	UnresolvedEffects  int
}

func (s RecoveryService) Reconcile(ctx context.Context) (RecoveryReport, error) {
	if s.Store == nil {
		return RecoveryReport{}, fmt.Errorf("recovery store is required")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	report := RecoveryReport{}
	leases, err := s.Store.ExpiredLeases(ctx, now)
	if err != nil {
		return report, err
	}
	for _, lease := range leases {
		if err := s.Store.AppendRecoveryFinding(ctx, RecoveryFinding{Kind: "EXPIRED_LEASE", ResourceID: lease.ResourceID, Classification: "RECONCILED", Details: []byte(`{}`), ObservedAt: now}); err != nil {
			return report, err
		}
		if err := s.Store.DeleteExpiredLease(ctx, lease.ResourceType, lease.ResourceID, lease.ExpiresAt); err != nil {
			return report, err
		}
		report.ExpiredLeases++
	}
	candidates, err := s.Store.RecoveryCandidates(ctx)
	if err != nil {
		return report, err
	}
	known := map[string]RecoveryCandidate{}
	for _, candidate := range candidates {
		known[candidate.ID] = candidate
		if candidate.Path != "" {
			if info, err := os.Lstat(candidate.Path); err != nil || !info.IsDir() {
				_ = s.Store.AppendRecoveryFinding(ctx, RecoveryFinding{Kind: "MISSING_DIRECTORY", ResourceID: candidate.ID, Path: candidate.Path, Classification: "NEEDS_OPERATOR", Details: []byte(`{}`), ObservedAt: now})
				report.MissingDirectories++
			}
		}
	}
	for _, root := range []string{s.WorkRoot, s.ApprovedRoot, s.QuarantineRoot} {
		entries, readErr := os.ReadDir(root)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return report, readErr
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
				continue
			}
			if _, ok := known[entry.Name()]; !ok {
				path := filepath.Join(root, entry.Name())
				if err := s.Store.AppendRecoveryFinding(ctx, RecoveryFinding{Kind: "ORPHAN_DIRECTORY", ResourceID: entry.Name(), Path: path, Classification: "QUARANTINE_HOLD", Details: []byte(`{}`), ObservedAt: now}); err != nil {
					return report, err
				}
				report.OrphanDirectories++
			}
		}
	}
	effects, err := s.Store.UnresolvedEffects(ctx)
	if err != nil {
		return report, err
	}
	for _, effect := range effects {
		if err := s.Store.AppendRecoveryFinding(ctx, RecoveryFinding{Kind: effect.Kind, ResourceID: effect.ResourceID, Classification: "RECONCILE_BEFORE_RETRY", Details: []byte(`{}`), ObservedAt: now}); err != nil {
			return report, err
		}
		report.UnresolvedEffects++
	}
	return report, nil
}
