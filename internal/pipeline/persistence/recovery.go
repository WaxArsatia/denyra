package persistence

import (
	"context"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	"time"
)

func (r *Repositories) RecoveryCandidates(ctx context.Context) ([]application.RecoveryCandidate, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT candidate_id,state,state_revision,release_directory FROM candidates WHERE state NOT IN ('IMPORTED','UNMANAGED_IMPORTED','REJECTED','SUPERSEDED','CANCELLED')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []application.RecoveryCandidate
	for rows.Next() {
		var item application.RecoveryCandidate
		var state string
		if err := rows.Scan(&item.ID, &state, &item.Revision, &item.Path); err != nil {
			return nil, err
		}
		item.State, err = domain.ParseState(state)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (r *Repositories) ExpiredLeases(ctx context.Context, now time.Time) ([]application.ExpiredLease, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT resource_type,resource_id,expires_at FROM leases WHERE expires_at<=? ORDER BY expires_at,resource_type,resource_id`, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []application.ExpiredLease
	for rows.Next() {
		var item application.ExpiredLease
		var expires string
		if err := rows.Scan(&item.ResourceType, &item.ResourceID, &expires); err != nil {
			return nil, err
		}
		item.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (r *Repositories) AppendRecoveryFinding(ctx context.Context, finding application.RecoveryFinding) error {
	id, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO recovery_findings(id,kind,resource_id,path,classification,details_json,observed_at) VALUES(?,?,?,NULLIF(?,''),?,?,?)`, id, finding.Kind, finding.ResourceID, finding.Path, finding.Classification, finding.Details, formatTime(finding.ObservedAt))
	return err
}

func (r *Repositories) UnresolvedEffects(ctx context.Context) ([]application.UnresolvedEffect, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT 'IDEMPOTENCY_PENDING',key FROM idempotency_records WHERE response_status IS NULL UNION ALL SELECT 'IMPORT_PENDING',id FROM import_intents WHERE status NOT IN (?,?) UNION ALL SELECT 'UNMANAGED_IMPORT_PENDING',id FROM unmanaged_import_intents WHERE status NOT IN ('COMPLETED','REVIEW_REQUIRED') UNION ALL SELECT 'MUTATION_INCOMPLETE',id FROM mutations WHERE completed_at IS NULL ORDER BY 1,2`, application.ImportImported, application.ImportFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []application.UnresolvedEffect
	for rows.Next() {
		var item application.UnresolvedEffect
		if err := rows.Scan(&item.Kind, &item.ResourceID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
