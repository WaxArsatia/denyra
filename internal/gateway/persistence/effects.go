package persistence

import (
	"context"
	"database/sql"
	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	"time"
)

type Effect struct {
	ID, JobID, Type, IdempotencyKey, RequestHash, Status string
	Request, Response                                    []byte
	CreatedAt                                            time.Time
	AcknowledgedAt                                       *time.Time
}

func (r *Repositories) PutEffect(ctx context.Context, effect Effect) error {
	id := effect.ID
	if id == "" {
		var err error
		id, err = ids.NewToken(16)
		if err != nil {
			return err
		}
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO external_effects(id,job_id,effect_type,idempotency_key,request_hash,request_json,status,created_at) VALUES(?,?,?,?,?,?,'INTENDED',?) ON CONFLICT(idempotency_key) DO NOTHING`, id, effect.JobID, effect.Type, effect.IdempotencyKey, effect.RequestHash, effect.Request, formatTime(effect.CreatedAt))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var jobID, kind, hash string
	var request []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT job_id,effect_type,request_hash,request_json FROM external_effects WHERE idempotency_key=?`, effect.IdempotencyKey).Scan(&jobID, &kind, &hash, &request); err != nil {
		return err
	}
	if jobID != effect.JobID || kind != effect.Type || hash != effect.RequestHash || string(request) != string(effect.Request) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}
func (r *Repositories) AcknowledgeEffect(ctx context.Context, key string, response []byte, responseHash string, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE external_effects SET status='ACKNOWLEDGED',response_json=?,response_hash=?,acknowledged_at=? WHERE idempotency_key=? AND acknowledged_at IS NULL`, response, responseHash, formatTime(at), key)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var hash sql.NullString
	if err := r.DB.QueryRowContext(ctx, `SELECT response_hash FROM external_effects WHERE idempotency_key=?`, key).Scan(&hash); err != nil {
		return err
	}
	if !hash.Valid || hash.String != responseHash {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}
func (r *Repositories) UnresolvedEffects(ctx context.Context, kind string) ([]Effect, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,job_id,effect_type,idempotency_key,request_hash,request_json,status,created_at FROM external_effects WHERE acknowledged_at IS NULL AND (?='' OR effect_type=?) ORDER BY created_at,id`, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Effect
	for rows.Next() {
		var item Effect
		var created string
		if err := rows.Scan(&item.ID, &item.JobID, &item.Type, &item.IdempotencyKey, &item.RequestHash, &item.Request, &item.Status, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, item)
	}
	return result, rows.Err()
}
