package application

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
)

type SubmissionRecord struct {
	ID, SourcePath, Status, SealedFingerprint string
	Revision                                  uint64
}
type SubmissionStore interface {
	Submission(context.Context, string) (SubmissionRecord, error)
	SealSubmission(context.Context, string, uint64, string, string, time.Time) error
}

type SubmissionService struct {
	Store        SubmissionStore
	IncomingRoot string
	Scan         func(string) (denyrafs.Tree, error)
	Now          func() time.Time
}

func (s SubmissionService) Submit(ctx context.Context, id string, expected uint64, actor string) error {
	if s.Store == nil || s.IncomingRoot == "" {
		return fmt.Errorf("submission service is not configured")
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
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return s.Store.SealSubmission(ctx, id, expected, tree.Fingerprint, actor, now)
}
