package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func (r *Repositories) ReadyWork(ctx context.Context, limit int) ([]application.WorkItem, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT candidate_id,state_revision,config_snapshot_id,state FROM candidates WHERE state IN ('RECEIVED','CLAIMED','STABILIZING','WORKING','TECHNICAL_VALIDATION','RELEASE_MATCHING','ENRICHING','APPROVED','IMPORT_READY','IMPORT_SUBMITTED','IMPORT_RECONCILING','UNMANAGED_READY','UNMANAGED_IMPORTING') AND NOT EXISTS(SELECT 1 FROM leases WHERE resource_type='candidate' AND resource_id=candidate_id) ORDER BY updated_at,candidate_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []application.WorkItem
	for rows.Next() {
		var item application.WorkItem
		var state string
		if err := rows.Scan(&item.CandidateID, &item.Revision, &item.ConfigSnapshotID, &state); err != nil {
			return nil, err
		}
		item.AdmissionRequired = state != "IMPORT_RECONCILING" && state != "UNMANAGED_IMPORTING"
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *Repositories) AcquireWorkLease(ctx context.Context, item application.WorkItem, owner string, acquired, expires time.Time) error {
	return r.AcquireLease(ctx, Lease{ResourceType: "candidate", ResourceID: item.CandidateID, OwnerID: owner, AcquiredAt: acquired, ExpiresAt: expires, ConfigSnapshotID: item.ConfigSnapshotID, ResourceRevision: item.Revision}, false)
}
func (r *Repositories) ReleaseWorkLease(ctx context.Context, candidateID, owner string) error {
	result, err := r.DB.ExecContext(ctx, `DELETE FROM leases WHERE resource_type='candidate' AND resource_id=? AND owner_id=?`, candidateID, owner)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (r *Repositories) RenewWorkLease(ctx context.Context, candidateID, owner string, previous, next time.Time) error {
	return r.RenewLease(ctx, "candidate", candidateID, owner, previous, next)
}
func (r *Repositories) DeleteExpiredLease(ctx context.Context, resourceType, resourceID string, expiresAt time.Time) error {
	result, err := r.DB.ExecContext(ctx, `DELETE FROM leases WHERE resource_type=? AND resource_id=? AND expires_at=?`, resourceType, resourceID, formatTime(expiresAt))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return errors.New("lease changed during recovery")
	}
	return nil
}
