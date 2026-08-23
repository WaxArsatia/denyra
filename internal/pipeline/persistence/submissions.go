package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) DiscoverSubmission(ctx context.Context, id, path string, at time.Time) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO submissions(id,source_path,status,state_revision,created_at,updated_at) VALUES(?,?,'DISCOVERED',0,?,?) ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at WHERE submissions.status='DISCOVERED' AND submissions.source_path=excluded.source_path`, id, path, formatTime(at), formatTime(at))
	return err
}

func (r *Repositories) Submission(ctx context.Context, id string) (application.SubmissionRecord, error) {
	var record application.SubmissionRecord
	err := r.DB.QueryRowContext(ctx, `SELECT id,source_path,status,state_revision,COALESCE(sealed_fingerprint,'') FROM submissions WHERE id=?`, id).Scan(&record.ID, &record.SourcePath, &record.Status, &record.Revision, &record.SealedFingerprint)
	return record, err
}

func (r *Repositories) SealSubmission(ctx context.Context, id string, expected uint64, fingerprint, actor string, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		var sourcePath string
		if err := tx.QueryRowContext(ctx, `SELECT source_path FROM submissions WHERE id=?`, id).Scan(&sourcePath); err != nil {
			return err
		}
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
		var snapshotID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM config_snapshots ORDER BY created_at DESC,id DESC LIMIT 1`).Scan(&snapshotID); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO candidates(candidate_id,source,release_directory,config_snapshot_id,acquisition_evidence_id,state,state_revision,created_at,updated_at) VALUES(?,'manual',?,?,?,'RECEIVED',0,?,?) ON CONFLICT(candidate_id) DO NOTHING`, id, sourcePath, snapshotID, "manual:"+id, formatTime(at), formatTime(at))
		if err != nil {
			return err
		}
		created, _ := result.RowsAffected()
		if created == 0 {
			var state string
			var revision uint64
			if err := tx.QueryRowContext(ctx, `SELECT state,state_revision FROM candidates WHERE candidate_id=?`, id).Scan(&state, &revision); err != nil {
				return err
			}
			if state != string(domain.StateWaitingResubmit) {
				return fmt.Errorf("candidate cannot be resubmitted from %s", state)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE candidates SET release_directory=?,state='RECEIVED',state_revision=state_revision+1,updated_at=? WHERE candidate_id=? AND state_revision=?`, sourcePath, formatTime(at), id, revision); err != nil {
				return err
			}
			transitionID, err := ids.NewToken(16)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions(id,candidate_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,'operator resubmitted sealed tree','WAITING_RESUBMIT','RECEIVED',?,?,?)`, transitionID, id, actor, revision, revision+1, formatTime(at)); err != nil {
				return err
			}
		}
		auditID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor,action,reason,details_json,occurred_at) VALUES(?,?,?,?,json_object('submission_id',?,'fingerprint',?),?)`, auditID, actor, "SUBMISSION_SEALED", "explicit operator submission", id, fingerprint, formatTime(at))
		return err
	})
}
