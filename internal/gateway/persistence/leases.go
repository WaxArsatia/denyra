package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrLeaseHeld = errors.New("lease held")
var ErrLeaseNeedsReconciliation = errors.New("expired lease requires reconciliation")

type Lease struct {
	ResourceType, ResourceID, OwnerID, ConfigSnapshotID string
	AcquiredAt, ExpiresAt                               time.Time
	ResourceRevision                                    uint64
}

func (r *Repositories) AcquireLease(ctx context.Context, lease Lease, reconciled bool) error {
	return withTx(ctx, r.DB, func(tx *sql.Tx) error {
		var expires string
		err := tx.QueryRowContext(ctx, `SELECT expires_at FROM leases WHERE resource_type=? AND resource_id=?`, lease.ResourceType, lease.ResourceID).Scan(&expires)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `INSERT INTO leases(resource_type,resource_id,owner_id,acquired_at,expires_at,config_snapshot_id,resource_revision) VALUES(?,?,?,?,?,?,?)`, lease.ResourceType, lease.ResourceID, lease.OwnerID, formatTime(lease.AcquiredAt), formatTime(lease.ExpiresAt), lease.ConfigSnapshotID, lease.ResourceRevision)
			return err
		}
		if err != nil {
			return err
		}
		expiry, _ := time.Parse(time.RFC3339Nano, expires)
		if expiry.After(lease.AcquiredAt) {
			return ErrLeaseHeld
		}
		if !reconciled {
			return ErrLeaseNeedsReconciliation
		}
		_, err = tx.ExecContext(ctx, `UPDATE leases SET owner_id=?,acquired_at=?,expires_at=?,config_snapshot_id=?,resource_revision=? WHERE resource_type=? AND resource_id=?`, lease.OwnerID, formatTime(lease.AcquiredAt), formatTime(lease.ExpiresAt), lease.ConfigSnapshotID, lease.ResourceRevision, lease.ResourceType, lease.ResourceID)
		return err
	})
}

func (r *Repositories) RenewLease(ctx context.Context, resourceType, resourceID, ownerID string, expectedRevision uint64, expiresAt time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE leases SET expires_at=? WHERE resource_type=? AND resource_id=? AND owner_id=? AND resource_revision=?`, formatTime(expiresAt), resourceType, resourceID, ownerID, expectedRevision)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return ErrLeaseHeld
	}
	return nil
}
func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
