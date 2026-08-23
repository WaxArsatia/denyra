package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type Attempt struct {
	ID, JobID, Kind, Outcome, ErrorClass string
	Number                               int
	StartedAt                            time.Time
	CompletedAt                          *time.Time
	Details                              []byte
}

func (r *Repositories) InsertAttempt(ctx context.Context, attempt Attempt) (string, error) {
	id := attempt.ID
	if id == "" {
		var err error
		id, err = ids.NewToken(16)
		if err != nil {
			return "", err
		}
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO attempts(id,job_id,kind,number,started_at,details_json) VALUES(?,?,?,?,?,?) ON CONFLICT(job_id,kind,number) DO NOTHING`, id, attempt.JobID, attempt.Kind, attempt.Number, formatTime(attempt.StartedAt), attempt.Details)
	if err != nil {
		return "", err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return id, nil
	}
	var storedID, started string
	var details []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT id,started_at,details_json FROM attempts WHERE job_id=? AND kind=? AND number=?`, attempt.JobID, attempt.Kind, attempt.Number).Scan(&storedID, &started, &details); err != nil {
		return "", err
	}
	if started != formatTime(attempt.StartedAt) || string(details) != string(attempt.Details) {
		return "", contracts.ErrIdempotencyConflict
	}
	return storedID, nil
}

func (r *Repositories) CompleteAttempt(ctx context.Context, id, outcome, errorClass string, details []byte, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE attempts SET completed_at=?,outcome=?,error_class=NULLIF(?,''),details_json=? WHERE id=? AND completed_at IS NULL`, formatTime(at), outcome, errorClass, details, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var storedOutcome string
	var storedError sql.NullString
	var storedDetails []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT outcome,error_class,details_json FROM attempts WHERE id=? AND completed_at IS NOT NULL`, id).Scan(&storedOutcome, &storedError, &storedDetails); err != nil {
		return err
	}
	if storedOutcome != outcome || storedError.String != errorClass || string(storedDetails) != string(details) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

type ProviderEvidence struct {
	ID, JobID, AttemptID, Provider, Outcome, EvidenceSHA256 string
	Evidence                                                []byte
	StartedAt                                               time.Time
	EstablishedAt, CompletedAt                              *time.Time
}

func (r *Repositories) InsertProviderResult(ctx context.Context, item ProviderEvidence) error {
	id := item.ID
	if id == "" {
		var err error
		id, err = ids.NewToken(16)
		if err != nil {
			return err
		}
	}
	var established, completed any
	if item.EstablishedAt != nil {
		established = formatTime(*item.EstablishedAt)
	}
	if item.CompletedAt != nil {
		completed = formatTime(*item.CompletedAt)
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO provider_results(id,job_id,attempt_id,provider,outcome,evidence_json,evidence_sha256,started_at,established_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(attempt_id,provider) DO NOTHING`, id, item.JobID, item.AttemptID, item.Provider, item.Outcome, item.Evidence, item.EvidenceSHA256, formatTime(item.StartedAt), established, completed)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var hash, outcome string
	if err := r.DB.QueryRowContext(ctx, `SELECT evidence_sha256,outcome FROM provider_results WHERE attempt_id=? AND provider=?`, item.AttemptID, item.Provider).Scan(&hash, &outcome); err != nil {
		return err
	}
	if hash != item.EvidenceSHA256 || outcome != item.Outcome {
		return fmt.Errorf("provider evidence conflict: %w", contracts.ErrIdempotencyConflict)
	}
	return nil
}
