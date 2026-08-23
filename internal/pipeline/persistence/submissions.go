package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) Submission(ctx context.Context, id string) (application.SubmissionRecord, error) {
	var record application.SubmissionRecord
	err := r.DB.QueryRowContext(ctx, `SELECT id,source_path,status,state_revision,COALESCE(sealed_fingerprint,'') FROM submissions WHERE id=?`, id).Scan(&record.ID, &record.SourcePath, &record.Status, &record.Revision, &record.SealedFingerprint)
	return record, err
}

func (r *Repositories) SealSubmission(ctx context.Context, id string, expected uint64, fingerprint, actor string, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE submissions SET sealed_fingerprint=?,status='SEALED',state_revision=state_revision+1,submitted_by=?,submitted_at=?,updated_at=? WHERE id=? AND state_revision=?`, fingerprint, actor, formatTime(at), formatTime(at), id, expected)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			var current uint64
			var status string
			load := tx.QueryRowContext(ctx, `SELECT state_revision,status FROM submissions WHERE id=?`, id).Scan(&current, &status)
			if errors.Is(load, sql.ErrNoRows) {
				return load
			}
			if load != nil {
				return load
			}
			return fmt.Errorf("stale submission revision: expected=%d current=%d status=%s", expected, current, status)
		}
		auditID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor,action,reason,details_json,occurred_at) VALUES(?,?,?,?,json_object('submission_id',?,'fingerprint',?),?)`, auditID, actor, "SUBMISSION_SEALED", "explicit operator submission", id, fingerprint, formatTime(at))
		return err
	})
}
