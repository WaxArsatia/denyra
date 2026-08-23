package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/waxarsatia/denyra/internal/contracts"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"time"
)

func (r *Repositories) StartArbitration(ctx context.Context, jobID string, first, deadline time.Time, revision uint64, evidence []byte) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO arbitrations(job_id,first_approved_at,deadline,evidence_json,state_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id) DO NOTHING`, jobID, formatTime(first), formatTime(deadline), evidence, revision, formatTime(first), formatTime(first))
	return err
}
func (r *Repositories) LockWinner(ctx context.Context, jobID, candidateID string, expected uint64, reason string, evidence []byte, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		var current sql.NullString
		var revision uint64
		if err := tx.QueryRowContext(ctx, `SELECT winner_candidate_id,state_revision FROM arbitrations WHERE job_id=?`, jobID).Scan(&current, &revision); err != nil {
			return err
		}
		if current.Valid {
			if current.String == candidateID {
				return nil
			}
			return contracts.ErrIdempotencyConflict
		}
		if revision != expected {
			return fmt.Errorf("stale arbitration revision: expected=%d current=%d", expected, revision)
		}
		result, err := tx.ExecContext(ctx, `UPDATE arbitrations SET winner_candidate_id=?,winner_locked_at=?,reason=?,evidence_json=?,state_revision=state_revision+1,updated_at=? WHERE job_id=? AND state_revision=? AND winner_candidate_id IS NULL`, candidateID, formatTime(at), reason, evidence, formatTime(at), jobID, expected)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return errors.New("winner lock lost")
		}
		return nil
	})
}
