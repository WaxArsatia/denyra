package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var ErrPreviewChanged = errors.New("submission tree changed after preview")

type SubmissionRecord struct {
	ID, SourcePath, Status, SealedFingerprint string
	Ingress                                   string
	Revision                                  uint64
}
type SubmissionStore interface {
	Submission(context.Context, string) (SubmissionRecord, error)
	SealSubmission(context.Context, string, uint64, string, string, domain.SubmissionDecision, time.Time) error
}

type SubmissionService struct {
	Store        SubmissionStore
	IncomingRoot string
	Scan         func(string) (denyrafs.Tree, error)
	Now          func() time.Time
}

func (s SubmissionService) Submit(ctx context.Context, id string, expected uint64, actor string, decisions ...domain.SubmissionDecision) error {
	if s.Store == nil || s.IncomingRoot == "" {
		return fmt.Errorf("submission service is not configured")
	}
	if len(decisions) != 1 {
		return fmt.Errorf("sealed submission decision is required")
	}
	decision := decisions[0]
	if err := domain.ValidateSubmissionDecision(decision); err != nil {
		return err
	}
	record, err := s.Store.Submission(ctx, id)
	if err != nil {
		return err
	}
	want := filepath.Join(s.IncomingRoot, id)
	if filepath.Clean(record.SourcePath) != want {
		return fmt.Errorf("submission path is not canonical for its identity")
	}
	scan := s.Scan
	if scan == nil {
		scan = denyrafs.Scan
	}
	tree, err := scan(want)
	if err != nil {
		return err
	}
	if len(tree.Entries) == 0 {
		return fmt.Errorf("submission is empty")
	}
	if tree.Fingerprint != decision.PreviewFingerprint {
		return ErrPreviewChanged
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return s.Store.SealSubmission(ctx, id, expected, tree.Fingerprint, actor, decision, now)
}
