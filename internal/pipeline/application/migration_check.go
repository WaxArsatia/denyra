package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type UnmanagedFilter struct {
	Query  string `json:"query,omitempty"`
	Status string `json:"status,omitempty"`
}

type Selection struct {
	ReleaseIDs []string          `json:"release_ids,omitempty"`
	Revisions  map[string]uint64 `json:"state_revisions,omitempty"`
	SelectAll  bool              `json:"select_all,omitempty"`
	Filter     UnmanagedFilter   `json:"filter,omitempty"`
}

type MigrationIdentity interface {
	Decide(context.Context, domain.MetadataPlan, domain.TechnicalReleaseResult) (IdentityDecision, error)
}

type MigrationCheckStore interface {
	PutMigrationBatch(context.Context, domain.MigrationBatch, []domain.MigrationItem) error
	MigrationItem(context.Context, string) (domain.MigrationItem, error)
	UnmanagedRelease(context.Context, string) (domain.UnmanagedRelease, error)
	UpdateMigrationItem(context.Context, string, uint64, domain.MigrationItem, *domain.MigrationItemError) error
}

type MigrationCheckService struct {
	Store    MigrationCheckStore
	Identity MigrationIdentity
	Now      func() time.Time
}

func (s MigrationCheckService) ResolveSelection(ctx context.Context, selection Selection) ([]string, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("migration check store is required")
	}
	if selection.SelectAll && len(selection.ReleaseIDs) > 0 {
		return nil, fmt.Errorf("select all and explicit release IDs are mutually exclusive")
	}
	if selection.SelectAll {
		return nil, fmt.Errorf("select all is not supported; select at most 100 releases from the current page")
	}
	if len(selection.ReleaseIDs) > 100 {
		return nil, fmt.Errorf("migration selection exceeds 100 releases")
	}
	var releaseIDs []string
	releaseIDs = append([]string(nil), selection.ReleaseIDs...)
	seen := make(map[string]bool, len(releaseIDs))
	resolved := make([]string, 0, len(releaseIDs))
	for _, releaseID := range releaseIDs {
		if err := domain.ValidateCandidateID(releaseID); err != nil {
			return nil, err
		}
		if !seen[releaseID] {
			expected, found := selection.Revisions[releaseID]
			if !found {
				return nil, fmt.Errorf("migration selection revision is required for %s", releaseID)
			}
			release, err := s.Store.UnmanagedRelease(ctx, releaseID)
			if err != nil {
				return nil, err
			}
			if release.StateRevision != expected {
				return nil, fmt.Errorf("migration selection revision changed for %s", releaseID)
			}
			seen[releaseID] = true
			resolved = append(resolved, releaseID)
		}
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("migration selection is empty")
	}
	sort.Strings(resolved)
	return resolved, nil
}

func (s MigrationCheckService) CreateBatch(ctx context.Context, selection Selection, actor string) (domain.MigrationBatch, []domain.MigrationItem, error) {
	if strings.TrimSpace(actor) == "" {
		return domain.MigrationBatch{}, nil, fmt.Errorf("authenticated migration actor is required")
	}
	releaseIDs, err := s.ResolveSelection(ctx, selection)
	if err != nil {
		return domain.MigrationBatch{}, nil, err
	}
	batchID, err := ids.NewToken(16)
	if err != nil {
		return domain.MigrationBatch{}, nil, err
	}
	selectionJSON, err := json.Marshal(selection)
	if err != nil {
		return domain.MigrationBatch{}, nil, err
	}
	now := s.now()
	batch := domain.MigrationBatch{ID: batchID, IdempotencyKey: "migration-batch-" + batchID, Actor: actor, SelectionJSON: selectionJSON, Status: "RUNNING", CreatedAt: now, UpdatedAt: now}
	items := make([]domain.MigrationItem, 0, len(releaseIDs))
	for _, releaseID := range releaseIDs {
		itemID, tokenErr := ids.NewToken(16)
		if tokenErr != nil {
			return domain.MigrationBatch{}, nil, tokenErr
		}
		items = append(items, domain.MigrationItem{ID: itemID, BatchID: batchID, UnmanagedCandidateID: releaseID, State: domain.MigrationCheckPending, IdempotencyKey: "migration-check-" + batchID + "-" + releaseID, CreatedAt: now, UpdatedAt: now})
	}
	if err := s.Store.PutMigrationBatch(ctx, batch, items); err != nil {
		return domain.MigrationBatch{}, nil, err
	}
	return batch, items, nil
}

func (s MigrationCheckService) Item(ctx context.Context, itemID string) (domain.MigrationItem, error) {
	if s.Store == nil {
		return domain.MigrationItem{}, fmt.Errorf("migration check store is required")
	}
	return s.Store.MigrationItem(ctx, itemID)
}

func (s MigrationCheckService) CheckItem(ctx context.Context, itemID string) (domain.MigrationItem, error) {
	if s.Store == nil || s.Identity == nil {
		return domain.MigrationItem{}, fmt.Errorf("migration check service is not configured")
	}
	item, err := s.Store.MigrationItem(ctx, itemID)
	if err != nil {
		return item, err
	}
	if item.State == domain.MigrationFailedRetryable {
		next, transitionErr := domain.TransitionMigration(item, item.ResumeState, s.now())
		if transitionErr != nil {
			return item, transitionErr
		}
		if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
			return item, err
		}
		item = next
	}
	if item.State != domain.MigrationCheckPending && item.State != domain.MigrationChecking {
		return item, nil
	}
	release, err := s.Store.UnmanagedRelease(ctx, item.UnmanagedCandidateID)
	if err != nil {
		return item, err
	}
	if release.Status != "IMPORTED" {
		return item, fmt.Errorf("unmanaged release %s is not imported", release.CandidateID)
	}
	requestEvidence, err := json.Marshal(struct {
		Metadata  domain.MetadataPlan           `json:"metadata"`
		Technical domain.TechnicalReleaseResult `json:"technical"`
	}{release.Plan.Metadata, release.Evidence})
	if err != nil {
		return item, err
	}
	if item.State == domain.MigrationCheckPending {
		next, transitionErr := domain.TransitionMigration(item, domain.MigrationChecking, s.now())
		if transitionErr != nil {
			return item, transitionErr
		}
		next.RequestEvidence = requestEvidence
		if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
			return item, err
		}
		item = next
	}
	decision, decideErr := s.Identity.Decide(ctx, release.Plan.Metadata, release.Evidence)
	responseEvidence, marshalErr := json.Marshal(decision)
	if marshalErr != nil {
		return item, marshalErr
	}
	if decideErr != nil || decision.Status == IdentityError {
		if decideErr == nil {
			decideErr = fmt.Errorf("identity check failed: %s", decision.Reason)
		}
		return s.failCheck(ctx, item, responseEvidence, decideErr)
	}
	var target domain.MigrationState
	switch decision.Status {
	case IdentityNoMatch:
		target = domain.MigrationNoMatch
	case IdentityAmbiguous:
		target = domain.MigrationAmbiguous
	case IdentityExact:
		target = domain.MigrationExactMatch
		if decision.Exact == nil {
			return s.failCheck(ctx, item, responseEvidence, fmt.Errorf("exact identity result omitted release"))
		}
		if _, err := domain.CanonicalMBID(decision.Exact.Release.ReleaseMBID); err != nil {
			return s.failCheck(ctx, item, responseEvidence, err)
		}
	default:
		return s.failCheck(ctx, item, responseEvidence, fmt.Errorf("unknown identity status %q", decision.Status))
	}
	next, err := domain.TransitionMigration(item, target, s.now())
	if err != nil {
		return item, err
	}
	next.ResponseEvidence = responseEvidence
	if decision.Exact != nil {
		next.ApprovedReleaseMBID = decision.Exact.Release.ReleaseMBID
	}
	if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
		return item, err
	}
	return next, nil
}

func (s MigrationCheckService) failCheck(ctx context.Context, item domain.MigrationItem, evidence []byte, cause error) (domain.MigrationItem, error) {
	next, err := domain.TransitionMigration(item, domain.MigrationFailedRetryable, s.now())
	if err != nil {
		return item, err
	}
	errorID, err := ids.NewToken(16)
	if err != nil {
		return item, err
	}
	failure := &domain.MigrationItemError{ID: errorID, ItemID: item.ID, State: item.State, Message: cause.Error(), Evidence: evidence, CreatedAt: s.now()}
	if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, failure); err != nil {
		return item, err
	}
	return next, cause
}

func (s MigrationCheckService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
