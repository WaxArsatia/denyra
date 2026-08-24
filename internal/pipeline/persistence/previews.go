package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func (r *Repositories) PutSubmissionPreview(ctx context.Context, preview domain.SubmissionPreview, at time.Time) error {
	stored := preview
	stored.Draft = nil
	payload, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO submission_previews(submission_id,tree_fingerprint,preview_json,decision_json,updated_at) VALUES(?,?,?,NULL,?)
		ON CONFLICT(submission_id) DO UPDATE SET tree_fingerprint=excluded.tree_fingerprint,preview_json=excluded.preview_json,
		decision_json=CASE WHEN submission_previews.tree_fingerprint=excluded.tree_fingerprint THEN submission_previews.decision_json ELSE NULL END,updated_at=excluded.updated_at`,
		preview.SubmissionID, preview.Fingerprint, payload, formatTime(at))
	return err
}

func (r *Repositories) CachedSubmissionPreview(ctx context.Context, submissionID, fingerprint string) (domain.SubmissionPreview, bool, error) {
	var previewPayload []byte
	var decisionPayload sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT preview_json,decision_json FROM submission_previews WHERE submission_id=? AND tree_fingerprint=?`, submissionID, fingerprint).Scan(&previewPayload, &decisionPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SubmissionPreview{}, false, nil
	}
	if err != nil {
		return domain.SubmissionPreview{}, false, err
	}
	var preview domain.SubmissionPreview
	if err := json.Unmarshal(previewPayload, &preview); err != nil {
		return domain.SubmissionPreview{}, false, err
	}
	if decisionPayload.Valid {
		var decision domain.SubmissionDecision
		if err := json.Unmarshal([]byte(decisionPayload.String), &decision); err != nil {
			return domain.SubmissionPreview{}, false, err
		}
		preview.Draft = &decision
	}
	return preview, true, nil
}

func (r *Repositories) SaveSubmissionDraft(ctx context.Context, submissionID string, decision domain.SubmissionDecision, at time.Time) error {
	payload, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	result, err := r.DB.ExecContext(ctx, `UPDATE submission_previews SET decision_json=?,updated_at=? WHERE submission_id=? AND tree_fingerprint=?`, payload, formatTime(at), submissionID, decision.PreviewFingerprint)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return application.ErrPreviewChanged
	}
	return nil
}
