package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func (s ImportService) Reconcile(ctx context.Context, intent domain.ImportIntent) (domain.ImportVerification, error) {
	if err := domain.ValidateCandidateID(intent.CandidateID); err != nil {
		return domain.ImportVerification{}, err
	}
	if s.Verifier == nil || s.Store == nil {
		return domain.ImportVerification{}, fmt.Errorf("import reconciliation is not configured")
	}
	verification, err := s.Verifier.Verify(ctx, intent.Plan, intent.Manifest)
	if err != nil {
		return verification, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if !verification.Complete {
		if err := s.Store.MarkImportStatus(ctx, intent.ID, "IMPORT_RECONCILING", MarshalImportEvidence(verification), now().UTC()); err != nil {
			return verification, err
		}
		return verification, nil
	}
	approvedPath := filepath.Join(s.ApprovedRoot, intent.CandidateID)
	remove := s.RemoveAll
	if remove == nil {
		remove = os.RemoveAll
	}
	if err := remove(approvedPath); err != nil {
		return verification, fmt.Errorf("delete verified staging source: %w", err)
	}
	if err := s.Store.MarkImportStatus(ctx, intent.ID, "IMPORTED", MarshalImportEvidence(verification), now().UTC()); err != nil {
		return verification, err
	}
	return verification, nil
}
