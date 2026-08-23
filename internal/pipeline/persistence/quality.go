package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
)

func (r *Repositories) PutQualityIntent(ctx context.Context, key, candidateID, requestHash string, payload []byte, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `INSERT INTO idempotency_records(key,scope,request_hash,request_body,created_at)
		VALUES(?,?,?,?,?) ON CONFLICT(key) DO NOTHING`, key, "quality:"+candidateID, requestHash, payload, formatTime(at))
	if err != nil {
		return fmt.Errorf("persist quality intent: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	var scope, storedHash string
	var storedPayload []byte
	if err := r.DB.QueryRowContext(ctx, "SELECT scope,request_hash,request_body FROM idempotency_records WHERE key=?", key).Scan(&scope, &storedHash, &storedPayload); err != nil {
		return err
	}
	if scope != "quality:"+candidateID || storedHash != requestHash || string(storedPayload) != string(payload) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

func (r *Repositories) CompleteQualityIntent(ctx context.Context, key string, status int, responseHash string, at time.Time) error {
	var existingStatus sql.NullInt64
	var existingHash []byte
	if err := r.DB.QueryRowContext(ctx, "SELECT response_status,response_body FROM idempotency_records WHERE key=?", key).Scan(&existingStatus, &existingHash); err != nil {
		return err
	}
	if existingStatus.Valid {
		if int(existingStatus.Int64) != status || string(existingHash) != responseHash {
			return contracts.ErrIdempotencyConflict
		}
		return nil
	}
	result, err := r.DB.ExecContext(ctx, `UPDATE idempotency_records SET response_status=?,response_body=?,completed_at=?
		WHERE key=? AND response_status IS NULL`, status, []byte(responseHash), formatTime(at), key)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}
