package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

type Approval struct {
	CandidateID, JobID, ConfigSnapshotID, MusicBrainzReleaseID string
	Source                                                     domain.CandidateSource
	ApprovedAt, CompletionAt                                   time.Time
	Quality                                                    contracts.QualityVector
	Warnings                                                   []contracts.Warning
	PipelineStateRevision                                      uint64
}

type Arbitration struct {
	JobID, WinnerCandidateID, Reason string
	FirstApprovedAt, Deadline        time.Time
	WinnerLockedAt                   *time.Time
	StateRevision                    uint64
}

type ApprovalRecordResult struct {
	Arbitration Arbitration
	Approvals   []Approval
	Replay      bool
	Status      int
	Body        []byte
}

func (r *Repositories) RecordApproval(ctx context.Context, approval Approval, window time.Duration, key, requestHash string, requestBody []byte, receivedAt time.Time) (ApprovalRecordResult, error) {
	var result ApprovalRecordResult
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		replay, status, body, err := beginGatewayIdempotency(ctx, tx, key, "candidate-approval", requestHash, requestBody, receivedAt)
		if err != nil {
			return err
		}
		if replay {
			result.Replay, result.Status, result.Body = true, status, body
			return nil
		}
		var jobID, source, completion, selectedRelease string
		if err := tx.QueryRowContext(ctx, `SELECT c.job_id,c.source,c.completed_at,COALESCE(j.selected_release_mbid,'') FROM candidates c JOIN acquisition_jobs j ON j.id=c.job_id WHERE c.candidate_id=?`, approval.CandidateID).Scan(&jobID, &source, &completion, &selectedRelease); err != nil {
			return err
		}
		if jobID != approval.JobID || source != string(approval.Source) || selectedRelease != approval.MusicBrainzReleaseID {
			return fmt.Errorf("approval identity does not match immutable acquisition candidate")
		}
		storedCompletion, err := time.Parse(time.RFC3339Nano, completion)
		if err != nil || !storedCompletion.Equal(approval.CompletionAt) {
			return fmt.Errorf("approval completion timestamp does not match acquisition evidence")
		}
		qualityJSON, err := json.Marshal(approval.Quality)
		if err != nil {
			return err
		}
		warningsJSON, err := json.Marshal(approval.Warnings)
		if err != nil {
			return err
		}
		insert, err := tx.ExecContext(ctx, `INSERT INTO candidate_approvals(candidate_id,approved_at,quality_json,quality_sha256,completion_at,source,created_at,config_snapshot_id,musicbrainz_release_id,warnings_json,pipeline_state_revision,request_json,request_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, approval.CandidateID, formatTime(approval.ApprovedAt), qualityJSON, sha256Hex(qualityJSON), formatTime(approval.CompletionAt), approval.Source, formatTime(receivedAt), approval.ConfigSnapshotID, approval.MusicBrainzReleaseID, warningsJSON, approval.PipelineStateRevision, requestBody, requestHash)
		if err != nil {
			return err
		}
		inserted, _ := insert.RowsAffected()
		if inserted == 0 {
			var storedHash string
			if err := tx.QueryRowContext(ctx, `SELECT request_sha256 FROM candidate_approvals WHERE candidate_id=?`, approval.CandidateID).Scan(&storedHash); err != nil {
				return err
			}
			if storedHash != requestHash {
				return contracts.ErrIdempotencyConflict
			}
		}
		deadline := approval.ApprovedAt.Add(window)
		if _, err := tx.ExecContext(ctx, `INSERT INTO arbitrations(job_id,first_approved_at,deadline,evidence_json,state_revision,created_at,updated_at) VALUES(?,?,?,?,0,?,?) ON CONFLICT(job_id) DO NOTHING`, approval.JobID, formatTime(approval.ApprovedAt), formatTime(deadline), requestBody, formatTime(receivedAt), formatTime(receivedAt)); err != nil {
			return err
		}
		var currentFirst string
		var currentRevision uint64
		var currentWinner sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT first_approved_at,state_revision,winner_candidate_id FROM arbitrations WHERE job_id=?`, approval.JobID).Scan(&currentFirst, &currentRevision, &currentWinner); err != nil {
			return err
		}
		parsedFirst, err := time.Parse(time.RFC3339Nano, currentFirst)
		if err != nil {
			return err
		}
		if !currentWinner.Valid && approval.ApprovedAt.Before(parsedFirst) {
			updated, err := tx.ExecContext(ctx, `UPDATE arbitrations SET first_approved_at=?,deadline=?,evidence_json=?,state_revision=state_revision+1,updated_at=? WHERE job_id=? AND state_revision=? AND winner_candidate_id IS NULL`, formatTime(approval.ApprovedAt), formatTime(deadline), requestBody, formatTime(receivedAt), approval.JobID, currentRevision)
			if err != nil {
				return err
			}
			count, _ := updated.RowsAffected()
			if count != 1 {
				return errors.New("arbitration first-approval update lost")
			}
		}
		job, err := jobQuery(ctx, tx, approval.JobID)
		if err != nil {
			return err
		}
		if job.State == domain.StatePrimaryActive || job.State == domain.StateDualCandidate {
			event, err := job.Transition(job.Revision, domain.StateArbitrating, "pipeline-quality-callback", "candidate reached APPROVED", receivedAt)
			if err != nil {
				return err
			}
			if err := updateJobTransitionTx(ctx, tx, job, event); err != nil {
				return err
			}
		}
		result.Arbitration, err = arbitrationInTx(ctx, tx, approval.JobID)
		if err != nil {
			return err
		}
		result.Approvals, err = approvalsInTx(ctx, tx, approval.JobID)
		return err
	})
	return result, err
}

func (r *Repositories) CompleteApprovalCallback(ctx context.Context, key string, status int, body []byte, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE idempotency_records SET response_status=?,response_body=?,completed_at=? WHERE key=? AND response_status IS NULL`, status, body, formatTime(at), key)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var storedStatus sql.NullInt64
	var storedBody []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT response_status,response_body FROM idempotency_records WHERE key=?`, key).Scan(&storedStatus, &storedBody); err != nil {
		return err
	}
	if !storedStatus.Valid || int(storedStatus.Int64) != status || string(storedBody) != string(body) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

type WinnerLock struct {
	JobID, CandidateID, Reason string
	ExpectedRevision           uint64
	Evidence                   []byte
	Effects                    []Effect
	LockedAt                   time.Time
}

func (r *Repositories) LockWinnerWithEffects(ctx context.Context, lock WinnerLock) (bool, error) {
	locked := false
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		arbitration, err := arbitrationInTx(ctx, tx, lock.JobID)
		if err != nil {
			return err
		}
		if arbitration.WinnerCandidateID != "" {
			if arbitration.WinnerCandidateID != lock.CandidateID {
				return contracts.ErrIdempotencyConflict
			}
			return nil
		}
		if arbitration.StateRevision != lock.ExpectedRevision {
			return fmt.Errorf("stale arbitration revision: expected=%d current=%d", lock.ExpectedRevision, arbitration.StateRevision)
		}
		result, err := tx.ExecContext(ctx, `UPDATE arbitrations SET winner_candidate_id=?,winner_locked_at=?,reason=?,evidence_json=?,state_revision=state_revision+1,updated_at=? WHERE job_id=? AND state_revision=? AND winner_candidate_id IS NULL`, lock.CandidateID, formatTime(lock.LockedAt), lock.Reason, lock.Evidence, formatTime(lock.LockedAt), lock.JobID, lock.ExpectedRevision)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return errors.New("winner lock lost")
		}
		job, err := jobQuery(ctx, tx, lock.JobID)
		if err != nil {
			return err
		}
		event, err := job.Transition(job.Revision, domain.StateWinnerLocked, "gateway-arbitration", lock.Reason, lock.LockedAt)
		if err != nil {
			return err
		}
		if err := updateJobTransitionTx(ctx, tx, job, event); err != nil {
			return err
		}
		for _, effect := range lock.Effects {
			if err := insertEffectTx(ctx, tx, effect); err != nil {
				return err
			}
		}
		locked = true
		return nil
	})
	return locked, err
}

func (r *Repositories) Arbitration(ctx context.Context, jobID string) (Arbitration, error) {
	return arbitrationInTx(ctx, r.DB, jobID)
}

func (r *Repositories) Approvals(ctx context.Context, jobID string) ([]Approval, error) {
	return approvalsInTx(ctx, r.DB, jobID)
}

type arbitrationQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func arbitrationInTx(ctx context.Context, query arbitrationQuery, jobID string) (Arbitration, error) {
	var arbitration Arbitration
	var first, deadline string
	var winner, locked, reason sql.NullString
	err := query.QueryRowContext(ctx, `SELECT job_id,first_approved_at,deadline,winner_candidate_id,winner_locked_at,reason,state_revision FROM arbitrations WHERE job_id=?`, jobID).Scan(&arbitration.JobID, &first, &deadline, &winner, &locked, &reason, &arbitration.StateRevision)
	if err != nil {
		return arbitration, err
	}
	arbitration.FirstApprovedAt, err = time.Parse(time.RFC3339Nano, first)
	if err == nil {
		arbitration.Deadline, err = time.Parse(time.RFC3339Nano, deadline)
	}
	arbitration.WinnerCandidateID = winner.String
	arbitration.Reason = reason.String
	if err == nil && locked.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, locked.String)
		err = parseErr
		arbitration.WinnerLockedAt = &value
	}
	return arbitration, err
}

func approvalsInTx(ctx context.Context, query arbitrationQuery, jobID string) ([]Approval, error) {
	rows, err := query.QueryContext(ctx, `SELECT a.candidate_id,c.job_id,a.config_snapshot_id,a.musicbrainz_release_id,a.source,a.approved_at,a.completion_at,a.quality_json,a.warnings_json,a.pipeline_state_revision FROM candidate_approvals a JOIN candidates c ON c.candidate_id=a.candidate_id WHERE c.job_id=? ORDER BY a.approved_at,a.candidate_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var approvals []Approval
	for rows.Next() {
		var approval Approval
		var approvedAt, completionAt string
		var qualityJSON, warningsJSON []byte
		if err := rows.Scan(&approval.CandidateID, &approval.JobID, &approval.ConfigSnapshotID, &approval.MusicBrainzReleaseID, &approval.Source, &approvedAt, &completionAt, &qualityJSON, &warningsJSON, &approval.PipelineStateRevision); err != nil {
			return nil, err
		}
		approval.ApprovedAt, err = time.Parse(time.RFC3339Nano, approvedAt)
		if err == nil {
			approval.CompletionAt, err = time.Parse(time.RFC3339Nano, completionAt)
		}
		if err == nil {
			err = json.Unmarshal(qualityJSON, &approval.Quality)
		}
		if err == nil {
			err = json.Unmarshal(warningsJSON, &approval.Warnings)
		}
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, rows.Err()
}

func beginGatewayIdempotency(ctx context.Context, tx *sql.Tx, key, scope, hash string, body []byte, at time.Time) (bool, int, []byte, error) {
	if key == "" {
		return false, 0, nil, fmt.Errorf("idempotency key is required")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO idempotency_records(key,scope,request_hash,request_body,created_at) VALUES(?,?,?,?,?) ON CONFLICT(key) DO NOTHING`, key, scope, hash, body, formatTime(at))
	if err != nil {
		return false, 0, nil, err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return false, 0, nil, nil
	}
	var storedScope, storedHash string
	var storedBody, response []byte
	var status sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT scope,request_hash,request_body,response_status,response_body FROM idempotency_records WHERE key=?`, key).Scan(&storedScope, &storedHash, &storedBody, &status, &response); err != nil {
		return false, 0, nil, err
	}
	if storedScope != scope || storedHash != hash || string(storedBody) != string(body) {
		return false, 0, nil, contracts.ErrIdempotencyConflict
	}
	if status.Valid {
		return true, int(status.Int64), response, nil
	}
	return false, 0, nil, nil
}

func updateJobTransitionTx(ctx context.Context, tx *sql.Tx, job domain.Job, event domain.Transition) error {
	result, err := tx.ExecContext(ctx, `UPDATE acquisition_jobs SET state=?,state_revision=?,next_retry_at=NULL,updated_at=? WHERE id=? AND state_revision=?`, job.State, job.Revision, formatTime(job.UpdatedAt), job.ID, event.PreviousRevision)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return &domain.StaleRevisionError{Expected: event.PreviousRevision, Current: job.Revision, State: job.State}
	}
	id, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO state_transitions(id,job_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, job.ID, event.Actor, event.Reason, event.Previous, event.Next, event.PreviousRevision, event.Revision, formatTime(event.OccurredAt))
	return err
}

func insertEffectTx(ctx context.Context, tx *sql.Tx, effect Effect) error {
	id := effect.ID
	if id == "" {
		var err error
		id, err = ids.NewToken(16)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO external_effects(id,job_id,effect_type,idempotency_key,request_hash,request_json,status,created_at) VALUES(?,?,?,?,?,?,'INTENDED',?) ON CONFLICT(idempotency_key) DO NOTHING`, id, effect.JobID, effect.Type, effect.IdempotencyKey, effect.RequestHash, effect.Request, formatTime(effect.CreatedAt))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var jobID, effectType, hash string
	var request []byte
	if err := tx.QueryRowContext(ctx, `SELECT job_id,effect_type,request_hash,request_json FROM external_effects WHERE idempotency_key=?`, effect.IdempotencyKey).Scan(&jobID, &effectType, &hash, &request); err != nil {
		return err
	}
	if jobID != effect.JobID || effectType != effect.Type || hash != effect.RequestHash || string(request) != string(effect.Request) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}
