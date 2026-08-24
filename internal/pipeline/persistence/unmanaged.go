package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) PutUnmanagedRelease(ctx context.Context, release domain.UnmanagedRelease, at time.Time) error {
	plan, evidence, manifest, err := unmanagedJSON(release.Plan, release.Evidence, release.Manifest)
	if err != nil {
		return err
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO unmanaged_releases(candidate_id,approved_plan_json,evidence_json,state,state_revision,final_path,manifest_json,fingerprint,status,created_at,updated_at)
		VALUES(?,?,?,?,?,NULLIF(?,''),?,NULLIF(?,''),?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, release.CandidateID, plan, evidence, release.State, release.StateRevision, release.FinalPath, manifest, release.Fingerprint, release.Status, formatTime(at), formatTime(at))
	if err != nil {
		return fmt.Errorf("persist unmanaged release: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	var storedPlan, storedEvidence []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT approved_plan_json,evidence_json FROM unmanaged_releases WHERE candidate_id=?`, release.CandidateID).Scan(&storedPlan, &storedEvidence); err != nil {
		return err
	}
	if !bytes.Equal(storedPlan, plan) || !bytes.Equal(storedEvidence, evidence) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

func (r *Repositories) UnmanagedRelease(ctx context.Context, candidateID string) (domain.UnmanagedRelease, error) {
	var result domain.UnmanagedRelease
	var plan, evidence, manifest []byte
	var finalPath, fingerprint *string
	var createdAt, updatedAt string
	err := r.DB.QueryRowContext(ctx, `SELECT candidate_id,approved_plan_json,evidence_json,state,state_revision,final_path,manifest_json,fingerprint,status,created_at,updated_at FROM unmanaged_releases WHERE candidate_id=?`, candidateID).
		Scan(&result.CandidateID, &plan, &evidence, &result.State, &result.StateRevision, &finalPath, &manifest, &fingerprint, &result.Status, &createdAt, &updatedAt)
	if err != nil {
		return result, err
	}
	if err := decodeUnmanagedJSON(plan, evidence, manifest, &result.Plan, &result.Evidence, &result.Manifest); err != nil {
		return result, err
	}
	if finalPath != nil {
		result.FinalPath = *finalPath
	}
	if fingerprint != nil {
		result.Fingerprint = *fingerprint
	}
	if result.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return result, err
	}
	if result.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Repositories) PutUnmanagedImportIntent(ctx context.Context, intent domain.UnmanagedImportIntent, at time.Time) error {
	plan, evidence, manifest, err := unmanagedJSON(intent.Plan, intent.Evidence, intent.Manifest)
	if err != nil {
		return err
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO unmanaged_import_intents(id,candidate_id,idempotency_key,approved_plan_json,evidence_json,final_path,manifest_json,fingerprint,status,created_at,updated_at)
		VALUES(?,?,?,?,?,NULLIF(?,''),?,NULLIF(?,''),?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, intent.ID, intent.CandidateID, intent.IdempotencyKey, plan, evidence, intent.FinalPath, manifest, intent.Fingerprint, intent.Status, formatTime(at), formatTime(at))
	if err != nil {
		return fmt.Errorf("persist unmanaged import intent: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	var candidateID string
	var storedPlan, storedEvidence []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT candidate_id,approved_plan_json,evidence_json FROM unmanaged_import_intents WHERE idempotency_key=?`, intent.IdempotencyKey).Scan(&candidateID, &storedPlan, &storedEvidence); err != nil {
		return err
	}
	if candidateID != intent.CandidateID || !bytes.Equal(storedPlan, plan) || !bytes.Equal(storedEvidence, evidence) {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

func (r *Repositories) UnmanagedImportIntent(ctx context.Context, candidateID string) (domain.UnmanagedImportIntent, error) {
	var result domain.UnmanagedImportIntent
	var plan, evidence, manifest []byte
	var finalPath, fingerprint *string
	var createdAt, updatedAt string
	err := r.DB.QueryRowContext(ctx, `SELECT id,candidate_id,idempotency_key,approved_plan_json,evidence_json,final_path,manifest_json,fingerprint,status,created_at,updated_at FROM unmanaged_import_intents WHERE candidate_id=?`, candidateID).
		Scan(&result.ID, &result.CandidateID, &result.IdempotencyKey, &plan, &evidence, &finalPath, &manifest, &fingerprint, &result.Status, &createdAt, &updatedAt)
	if err != nil {
		return result, err
	}
	if err := decodeUnmanagedJSON(plan, evidence, manifest, &result.Plan, &result.Evidence, &result.Manifest); err != nil {
		return result, err
	}
	if finalPath != nil {
		result.FinalPath = *finalPath
	}
	if fingerprint != nil {
		result.Fingerprint = *fingerprint
	}
	if result.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return result, err
	}
	if result.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Repositories) UpdateUnmanagedImport(ctx context.Context, intent domain.UnmanagedImportIntent, release domain.UnmanagedRelease, at time.Time) error {
	_, _, manifest, err := unmanagedJSON(intent.Plan, intent.Evidence, intent.Manifest)
	if err != nil {
		return err
	}
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE unmanaged_import_intents SET final_path=NULLIF(?,''),manifest_json=?,fingerprint=NULLIF(?,''),status=?,updated_at=? WHERE id=? AND candidate_id=?`, intent.FinalPath, manifest, intent.Fingerprint, intent.Status, formatTime(at), intent.ID, intent.CandidateID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("unmanaged import intent %s not found", intent.ID)
		}
		result, err = tx.ExecContext(ctx, `UPDATE unmanaged_releases SET state=?,state_revision=?,final_path=NULLIF(?,''),manifest_json=?,fingerprint=NULLIF(?,''),status=?,updated_at=? WHERE candidate_id=?`, release.State, release.StateRevision, release.FinalPath, manifest, release.Fingerprint, release.Status, formatTime(at), release.CandidateID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("unmanaged release %s not found", release.CandidateID)
		}
		return nil
	})
}

func unmanagedJSON(plan domain.UnmanagedPlan, evidence domain.TechnicalReleaseResult, manifest []domain.PlannedFile) ([]byte, []byte, []byte, error) {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, nil, nil, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, nil, nil, err
	}
	manifestJSON, err := json.Marshal(manifest)
	return planJSON, evidenceJSON, manifestJSON, err
}

func decodeUnmanagedJSON(planJSON, evidenceJSON, manifestJSON []byte, plan *domain.UnmanagedPlan, evidence *domain.TechnicalReleaseResult, manifest *[]domain.PlannedFile) error {
	if err := json.Unmarshal(planJSON, plan); err != nil {
		return err
	}
	if err := json.Unmarshal(evidenceJSON, evidence); err != nil {
		return err
	}
	return json.Unmarshal(manifestJSON, manifest)
}
