package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

func (r *Repositories) CurrentConfigSnapshotID(ctx context.Context) (string, error) {
	var id string
	err := r.DB.QueryRowContext(ctx, `SELECT id FROM config_snapshots ORDER BY created_at DESC,id DESC LIMIT 1`).Scan(&id)
	return id, err
}

func (r *Repositories) ActiveJobs(ctx context.Context) ([]domain.Job, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id FROM acquisition_jobs WHERE state NOT IN ('HANDED_OFF','CANCELLED') ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []domain.Job
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		job, err := r.Job(ctx, id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *Repositories) ReleaseLease(ctx context.Context, resourceType, resourceID, ownerID string) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM leases WHERE resource_type=? AND resource_id=? AND owner_id=?`, resourceType, resourceID, ownerID)
	return err
}

func (r *Repositories) ReconcileExpiredLeases(ctx context.Context, now time.Time) (int, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT resource_type,resource_id,owner_id,expires_at FROM leases`)
	if err != nil {
		return 0, err
	}
	type expiredLease struct{ resourceType, resourceID, ownerID string }
	var expired []expiredLease
	for rows.Next() {
		var lease expiredLease
		var expiresAt string
		if err := rows.Scan(&lease.resourceType, &lease.resourceID, &lease.ownerID, &expiresAt); err != nil {
			rows.Close()
			return 0, err
		}
		expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if !expiry.After(now) {
			expired = append(expired, lease)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, lease := range expired {
		if _, err := r.DB.ExecContext(ctx, `DELETE FROM leases WHERE resource_type=? AND resource_id=? AND owner_id=?`, lease.resourceType, lease.resourceID, lease.ownerID); err != nil {
			return 0, err
		}
		if err := r.AppendRecoveryEvent(ctx, lease.resourceID, "EXPIRED_LEASE_RECONCILED", map[string]string{"resource_type": lease.resourceType, "owner_id": lease.ownerID}, now); err != nil {
			return 0, err
		}
	}
	return len(expired), nil
}

func (r *Repositories) Maintenance(ctx context.Context) (bool, string, error) {
	var enabled int
	var reason string
	err := r.DB.QueryRowContext(ctx, `SELECT enabled,reason FROM runtime_flags WHERE key='maintenance'`).Scan(&enabled, &reason)
	return enabled == 1, reason, err
}

func (r *Repositories) SetMaintenance(ctx context.Context, enabled bool, reason string, at time.Time) error {
	value := 0
	if enabled {
		value = 1
	}
	_, err := r.DB.ExecContext(ctx, `UPDATE runtime_flags SET enabled=?,reason=?,updated_at=? WHERE key='maintenance'`, value, reason, formatTime(at))
	return err
}

func (r *Repositories) AppendRecoveryEvent(ctx context.Context, jobID, kind string, details any, at time.Time) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	id, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	var nullableJob any
	if jobID != "" {
		nullableJob = jobID
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO recovery_events(id,job_id,kind,details_json,occurred_at) VALUES(?,?,?,?,?)`, id, nullableJob, kind, payload, formatTime(at))
	return err
}

func (r *Repositories) HasArbitration(ctx context.Context, jobID string) (bool, error) {
	var value int
	err := r.DB.QueryRowContext(ctx, `SELECT 1 FROM arbitrations WHERE job_id=?`, jobID).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
