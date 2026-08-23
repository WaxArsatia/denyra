package persistence

import (
	"context"
	"time"

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
	_, err := r.DB.ExecContext(ctx, `INSERT INTO attempts(id,job_id,kind,number,started_at,details_json) VALUES(?,?,?,?,?,?)`, id, attempt.JobID, attempt.Kind, attempt.Number, formatTime(attempt.StartedAt), attempt.Details)
	return id, err
}

func (r *Repositories) CompleteAttempt(ctx context.Context, id, outcome, errorClass string, details []byte, at time.Time) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE attempts SET completed_at=?,outcome=?,error_class=NULLIF(?,''),details_json=? WHERE id=? AND completed_at IS NULL`, formatTime(at), outcome, errorClass, details, id)
	return err
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
	_, err := r.DB.ExecContext(ctx, `INSERT INTO provider_results(id,job_id,attempt_id,provider,outcome,evidence_json,evidence_sha256,started_at,established_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, item.JobID, item.AttemptID, item.Provider, item.Outcome, item.Evidence, item.EvidenceSHA256, formatTime(item.StartedAt), established, completed)
	return err
}
