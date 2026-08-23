package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func (r *Repositories) PutImportIntent(ctx context.Context, intent domain.ImportIntent, at time.Time) error {
	manifest, err := json.Marshal(intent.Manifest)
	if err != nil {
		return err
	}
	plan, err := json.Marshal(intent.Plan)
	if err != nil {
		return err
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO import_intents(id,candidate_id,idempotency_key,target_release_mbid,request_hash,release_manifest_json,plan_json,download_id,status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,NULLIF(?,''),'PENDING',?,?) ON CONFLICT(idempotency_key) DO NOTHING`, intent.ID, intent.CandidateID, intent.IdempotencyKey,
		intent.TargetReleaseMBID, intent.RequestHash, manifest, plan, intent.DownloadID, formatTime(at), formatTime(at))
	if err != nil {
		return fmt.Errorf("persist import intent: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	var candidateID, target, requestHash string
	if err := r.DB.QueryRowContext(ctx, "SELECT candidate_id,target_release_mbid,request_hash FROM import_intents WHERE idempotency_key=?", intent.IdempotencyKey).Scan(&candidateID, &target, &requestHash); err != nil {
		return err
	}
	if candidateID != intent.CandidateID || target != intent.TargetReleaseMBID || requestHash != intent.RequestHash {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

func (r *Repositories) MarkImportStatus(ctx context.Context, intentID, status string, response []byte, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE import_intents SET status=?,response_json=?,updated_at=? WHERE id=?`, status, response, formatTime(at), intentID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("import intent %s not found", intentID)
	}
	return nil
}
