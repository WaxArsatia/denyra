package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) PutMigrationBatch(ctx context.Context, batch domain.MigrationBatch, items []domain.MigrationItem) error {
	if batch.ID == "" || batch.IdempotencyKey == "" || batch.Actor == "" || len(items) == 0 {
		return fmt.Errorf("migration batch identity, actor, and items are required")
	}
	auditID, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"batch_id": batch.ID, "release_count": len(items), "selection": json.RawMessage(batch.SelectionJSON)})
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO migration_batches(id,idempotency_key,actor,selection_json,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			batch.ID, batch.IdempotencyKey, batch.Actor, batch.SelectionJSON, batch.Status, formatTime(batch.CreatedAt), formatTime(batch.UpdatedAt)); err != nil {
			return err
		}
		for _, item := range items {
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM unmanaged_releases WHERE candidate_id=?`, item.UnmanagedCandidateID).Scan(&status); err != nil {
				return err
			}
			if status != "IMPORTED" {
				return fmt.Errorf("unmanaged release %s is not imported", item.UnmanagedCandidateID)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO migration_items(id,batch_id,unmanaged_candidate_id,state,state_revision,resume_state,approved_release_mbid,request_evidence_json,response_evidence_json,migration_evidence_json,idempotency_key,created_at,updated_at)
VALUES(?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,?,?,?,?)`, item.ID, item.BatchID, item.UnmanagedCandidateID, item.State, item.StateRevision, item.ResumeState,
				item.ApprovedReleaseMBID, nullableJSON(item.RequestEvidence), nullableJSON(item.ResponseEvidence), nullableJSON(item.MigrationEvidence), item.IdempotencyKey, formatTime(item.CreatedAt), formatTime(item.UpdatedAt)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,candidate_id,actor,action,reason,details_json,occurred_at) VALUES(?,NULL,?,'MIGRATION_BATCH_CREATED','operator requested explicit catalog check',?,?)`, auditID, batch.Actor, details, formatTime(batch.CreatedAt))
		return err
	})
}

func (r *Repositories) MigrationBatch(ctx context.Context, batchID string) (domain.MigrationBatch, error) {
	var result domain.MigrationBatch
	var created, updated string
	err := r.DB.QueryRowContext(ctx, `SELECT id,idempotency_key,actor,selection_json,status,state_revision,created_at,updated_at FROM migration_batches WHERE id=?`, batchID).
		Scan(&result.ID, &result.IdempotencyKey, &result.Actor, &result.SelectionJSON, &result.Status, &result.StateRevision, &created, &updated)
	if err != nil {
		return result, err
	}
	result.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err == nil {
		result.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	return result, err
}

func (r *Repositories) MigrationItems(ctx context.Context, batchID string) ([]domain.MigrationItem, error) {
	rows, err := r.DB.QueryContext(ctx, migrationItemSelect+` WHERE batch_id=? ORDER BY unmanaged_candidate_id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MigrationItem
	for rows.Next() {
		item, err := scanMigrationItem(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repositories) MigrationItem(ctx context.Context, itemID string) (domain.MigrationItem, error) {
	row := r.DB.QueryRowContext(ctx, migrationItemSelect+` WHERE id=?`, itemID)
	return scanMigrationItem(row)
}

func (r *Repositories) ConfirmMigrationItem(ctx context.Context, itemID string, expected uint64, releaseMBID, actor string, at time.Time) (domain.MigrationItem, error) {
	var confirmed domain.MigrationItem
	auditID, err := ids.NewToken(16)
	if err != nil {
		return confirmed, err
	}
	err = denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		item, err := scanMigrationItem(tx.QueryRowContext(ctx, migrationItemSelect+` WHERE id=?`, itemID))
		if err != nil {
			return err
		}
		if item.StateRevision != expected || item.State != domain.MigrationExactMatch || item.ApprovedReleaseMBID != releaseMBID {
			return fmt.Errorf("migration confirmation conflicts with exact match or revision")
		}
		confirmed, err = domain.TransitionMigration(item, domain.MigrationConfirmed, at)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE migration_items SET state=?,state_revision=?,updated_at=? WHERE id=? AND state_revision=? AND state='EXACT_MATCH' AND approved_release_mbid=?`, confirmed.State, confirmed.StateRevision, formatTime(confirmed.UpdatedAt), itemID, expected, releaseMBID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("migration confirmation revision changed")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE migration_batches SET status='RUNNING',state_revision=state_revision+1,updated_at=? WHERE id=?`, formatTime(confirmed.UpdatedAt), item.BatchID); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]any{"migration_item_id": itemID, "release_mbid": releaseMBID})
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,candidate_id,actor,action,reason,target_release_mbid,state_revision,details_json,occurred_at) VALUES(?,?,?,'MIGRATION_CONFIRMED','operator confirmed exact migration',?,?,?,?)`, auditID, item.UnmanagedCandidateID, actor, releaseMBID, confirmed.StateRevision, details, formatTime(at))
		return err
	})
	return confirmed, err
}

func (r *Repositories) UpdateMigrationItem(ctx context.Context, itemID string, expected uint64, next domain.MigrationItem, failure *domain.MigrationItemError) error {
	if next.ID != itemID || next.StateRevision != expected+1 {
		return fmt.Errorf("invalid migration item revision")
	}
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE migration_items SET state=?,state_revision=?,resume_state=NULLIF(?,''),approved_release_mbid=COALESCE(NULLIF(?,''),approved_release_mbid),request_evidence_json=COALESCE(request_evidence_json,?),response_evidence_json=COALESCE(response_evidence_json,?),migration_evidence_json=COALESCE(?,migration_evidence_json),updated_at=? WHERE id=? AND state_revision=?`,
			next.State, next.StateRevision, next.ResumeState, next.ApprovedReleaseMBID, nullableJSON(next.RequestEvidence), nullableJSON(next.ResponseEvidence), nullableJSON(next.MigrationEvidence), formatTime(next.UpdatedAt), itemID, expected)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("migration item revision changed")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE migration_batches SET status=CASE WHEN EXISTS(
			SELECT 1 FROM migration_items WHERE batch_id=migration_batches.id AND state IN ('CHECK_PENDING','CHECKING','CONFIRMED','LIDARR_CATALOG_READY','IMPORT_SUBMITTED','RECONCILING')
		) THEN 'RUNNING' ELSE 'COMPLETED' END,state_revision=state_revision+1,updated_at=? WHERE id=?`, formatTime(next.UpdatedAt), next.BatchID); err != nil {
			return err
		}
		if failure != nil {
			if failure.ItemID != itemID || failure.ID == "" || failure.Message == "" {
				return fmt.Errorf("invalid migration item failure")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO migration_item_errors(id,item_id,state,error_text,evidence_json,occurred_at) VALUES(?,?,?,?,?,?)`,
				failure.ID, failure.ItemID, failure.State, failure.Message, nullableJSON(failure.Evidence), formatTime(failure.CreatedAt)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repositories) SaveMigrationEvidence(ctx context.Context, itemID string, expected uint64, evidence []byte, at time.Time) error {
	if len(evidence) == 0 {
		return fmt.Errorf("migration evidence is required")
	}
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE migration_items SET migration_evidence_json=?,updated_at=? WHERE id=? AND state_revision=?`, evidence, formatTime(at), itemID, expected)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("migration item revision changed")
		}
		_, err = tx.ExecContext(ctx, `UPDATE migration_batches SET state_revision=state_revision+1,updated_at=? WHERE id=(SELECT batch_id FROM migration_items WHERE id=?)`, formatTime(at), itemID)
		return err
	})
}

func (r *Repositories) MigrationItemErrors(ctx context.Context, batchID string) ([]domain.MigrationItemError, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT e.id,e.item_id,e.state,e.error_text,e.evidence_json,e.occurred_at FROM migration_item_errors e JOIN migration_items i ON i.id=e.item_id WHERE i.batch_id=? ORDER BY e.occurred_at,e.id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MigrationItemError
	for rows.Next() {
		var item domain.MigrationItemError
		var evidence []byte
		var occurred string
		if err := rows.Scan(&item.ID, &item.ItemID, &item.State, &item.Message, &evidence, &occurred); err != nil {
			return nil, err
		}
		item.Evidence = evidence
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repositories) ReadyMigrationChecks(ctx context.Context, limit int) ([]domain.MigrationItem, error) {
	rows, err := r.DB.QueryContext(ctx, migrationItemSelect+` JOIN migration_batches b ON b.id=migration_items.batch_id
WHERE (migration_items.state IN ('CHECK_PENDING','CHECKING','CONFIRMED','LIDARR_CATALOG_READY','IMPORT_SUBMITTED','RECONCILING') OR (migration_items.state='FAILED_RETRYABLE' AND migration_items.resume_state IN ('CHECKING','CONFIRMED','LIDARR_CATALOG_READY','RECONCILING')))
AND NOT EXISTS(SELECT 1 FROM leases WHERE resource_type='migration_check' AND resource_id=migration_items.id)
ORDER BY migration_items.updated_at,migration_items.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MigrationItem
	for rows.Next() {
		item, err := scanMigrationItem(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repositories) AcquireMigrationLease(ctx context.Context, item domain.MigrationItem, owner string, acquired, expires time.Time) error {
	var configSnapshotID string
	err := r.DB.QueryRowContext(ctx, `SELECT c.config_snapshot_id FROM migration_items i JOIN candidates c ON c.candidate_id=i.unmanaged_candidate_id WHERE i.id=?`, item.ID).Scan(&configSnapshotID)
	if err != nil {
		return err
	}
	return r.AcquireLease(ctx, Lease{ResourceType: "migration_check", ResourceID: item.ID, OwnerID: owner, AcquiredAt: acquired, ExpiresAt: expires, ConfigSnapshotID: configSnapshotID, ResourceRevision: item.StateRevision}, false)
}

func (r *Repositories) ReleaseMigrationLease(ctx context.Context, itemID, owner string) error {
	result, err := r.DB.ExecContext(ctx, `DELETE FROM leases WHERE resource_type='migration_check' AND resource_id=? AND owner_id=?`, itemID, owner)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

const migrationItemSelect = `SELECT migration_items.id,migration_items.batch_id,migration_items.unmanaged_candidate_id,migration_items.state,migration_items.state_revision,COALESCE(migration_items.resume_state,''),COALESCE(migration_items.approved_release_mbid,''),migration_items.request_evidence_json,migration_items.response_evidence_json,migration_items.migration_evidence_json,migration_items.idempotency_key,migration_items.created_at,migration_items.updated_at FROM migration_items`

type migrationScanner interface{ Scan(...any) error }

func scanMigrationItem(scanner migrationScanner) (domain.MigrationItem, error) {
	var item domain.MigrationItem
	var request, response, migration []byte
	var created, updated string
	err := scanner.Scan(&item.ID, &item.BatchID, &item.UnmanagedCandidateID, &item.State, &item.StateRevision, &item.ResumeState, &item.ApprovedReleaseMBID, &request, &response, &migration, &item.IdempotencyKey, &created, &updated)
	if err != nil {
		return item, err
	}
	item.RequestEvidence, item.ResponseEvidence, item.MigrationEvidence = request, response, migration
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err == nil {
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	return item, err
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
