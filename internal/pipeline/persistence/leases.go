package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

var ErrLeaseHeld = errors.New("resource lease is held")
var ErrLeaseNeedsReconciliation = errors.New("expired lease requires reconciliation")

type Lease struct {
	ResourceType     string
	ResourceID       string
	OwnerID          string
	AcquiredAt       time.Time
	ExpiresAt        time.Time
	ConfigSnapshotID string
	ResourceRevision uint64
}

func (r *Repositories) AcquireLease(ctx context.Context, lease Lease, reconciliationAuthorized bool) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		var owner, expiry string
		err := tx.QueryRowContext(ctx, "SELECT owner_id, expires_at FROM leases WHERE resource_type=? AND resource_id=?", lease.ResourceType, lease.ResourceID).Scan(&owner, &expiry)
		if err == nil {
			expiresAt, parseErr := time.Parse(time.RFC3339Nano, expiry)
			if parseErr != nil {
				return parseErr
			}
			if expiresAt.After(lease.AcquiredAt) {
				return fmt.Errorf("%w by %s until %s", ErrLeaseHeld, owner, expiry)
			}
			if !reconciliationAuthorized {
				return ErrLeaseNeedsReconciliation
			}
			_, err = tx.ExecContext(ctx, `UPDATE leases SET owner_id=?,acquired_at=?,expires_at=?,config_snapshot_id=?,resource_revision=?
				WHERE resource_type=? AND resource_id=?`, lease.OwnerID, formatTime(lease.AcquiredAt), formatTime(lease.ExpiresAt),
				lease.ConfigSnapshotID, lease.ResourceRevision, lease.ResourceType, lease.ResourceID)
			return err
		}
		if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO leases(resource_type,resource_id,owner_id,acquired_at,expires_at,config_snapshot_id,resource_revision)
			VALUES(?,?,?,?,?,?,?)`, lease.ResourceType, lease.ResourceID, lease.OwnerID, formatTime(lease.AcquiredAt), formatTime(lease.ExpiresAt),
			lease.ConfigSnapshotID, lease.ResourceRevision)
		return err
	})
}

func (r *Repositories) RenewLease(ctx context.Context, resourceType, resourceID, ownerID string, previousExpiry, nextExpiry time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE leases SET expires_at=? WHERE resource_type=? AND resource_id=? AND owner_id=? AND expires_at=?`,
		formatTime(nextExpiry), resourceType, resourceID, ownerID, formatTime(previousExpiry))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrLeaseHeld
	}
	return nil
}
