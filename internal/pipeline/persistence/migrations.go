package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) SelectUnmanaged(ctx context.Context, filter application.UnmanagedFilter) ([]string, error) {
	status := strings.ToUpper(strings.TrimSpace(filter.Status))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	like := "%" + query + "%"
	rows, err := r.DB.QueryContext(ctx, `SELECT candidate_id FROM unmanaged_releases
WHERE (?='' OR status=?) AND (?='' OR lower(candidate_id) LIKE ? OR lower(COALESCE(final_path,'')) LIKE ?
OR lower(COALESCE(json_extract(approved_plan_json,'$.metadata.album_artist'),'')) LIKE ?
OR lower(COALESCE(json_extract(approved_plan_json,'$.metadata.album'),'')) LIKE ?)
ORDER BY candidate_id`, status, status, query, like, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var candidateID string
		if err := rows.Scan(&candidateID); err != nil {
			return nil, err
		}
		result = append(result, candidateID)
	}
	return result, rows.Err()
}

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
			if _, err := tx.ExecContext(ctx, `INSERT INTO migration_items(id,batch_id,unmanaged_candidate_id,state,state_revision,resume_state,approved_release_mbid,request_evidence_json,response_evidence_json,idempotency_key,created_at,updated_at)
VALUES(?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,?, ?,?)`, item.ID, item.BatchID, item.UnmanagedCandidateID, item.State, item.StateRevision, item.ResumeState,
				item.ApprovedReleaseMBID, nullableJSON(item.RequestEvidence), nullableJSON(item.ResponseEvidence), item.IdempotencyKey, formatTime(item.CreatedAt), formatTime(item.UpdatedAt)); err != nil {
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
	err := r.DB.QueryRowContext(ctx, `SELECT id,idempotency_key,actor,selection_json,status,created_at,updated_at FROM migration_batches WHERE id=?`, batchID).
		Scan(&result.ID, &result.IdempotencyKey, &result.Actor, &result.SelectionJSON, &result.Status, &created, &updated)
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

func (r *Repositories) UpdateMigrationItem(ctx context.Context, itemID string, expected uint64, next domain.MigrationItem, failure *domain.MigrationItemError) error {
	if next.ID != itemID || next.StateRevision != expected+1 {
		return fmt.Errorf("invalid migration item revision")
	}
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE migration_items SET state=?,state_revision=?,resume_state=NULLIF(?,''),approved_release_mbid=COALESCE(NULLIF(?,''),approved_release_mbid),request_evidence_json=COALESCE(request_evidence_json,?),response_evidence_json=COALESCE(response_evidence_json,?),updated_at=? WHERE id=? AND state_revision=?`,
			next.State, next.StateRevision, next.ResumeState, next.ApprovedReleaseMBID, nullableJSON(next.RequestEvidence), nullableJSON(next.ResponseEvidence), formatTime(next.UpdatedAt), itemID, expected)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("migration item revision changed")
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
		if next.State.CheckTerminal() {
			_, err = tx.ExecContext(ctx, `UPDATE migration_batches SET status='COMPLETED',updated_at=? WHERE id=(SELECT batch_id FROM migration_items WHERE id=?)
AND NOT EXISTS(SELECT 1 FROM migration_items WHERE batch_id=(SELECT batch_id FROM migration_items WHERE id=?) AND (state IN ('CHECK_PENDING','CHECKING') OR (state='FAILED_RETRYABLE' AND resume_state='CHECKING')))`, formatTime(next.UpdatedAt), itemID, itemID)
		}
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
WHERE b.status='RUNNING' AND (migration_items.state IN ('CHECK_PENDING','CHECKING') OR (migration_items.state='FAILED_RETRYABLE' AND migration_items.resume_state='CHECKING'))
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

const migrationItemSelect = `SELECT migration_items.id,migration_items.batch_id,migration_items.unmanaged_candidate_id,migration_items.state,migration_items.state_revision,COALESCE(migration_items.resume_state,''),COALESCE(migration_items.approved_release_mbid,''),migration_items.request_evidence_json,migration_items.response_evidence_json,migration_items.idempotency_key,migration_items.created_at,migration_items.updated_at FROM migration_items`

type migrationScanner interface{ Scan(...any) error }

func scanMigrationItem(scanner migrationScanner) (domain.MigrationItem, error) {
	var item domain.MigrationItem
	var request, response []byte
	var created, updated string
	err := scanner.Scan(&item.ID, &item.BatchID, &item.UnmanagedCandidateID, &item.State, &item.StateRevision, &item.ResumeState, &item.ApprovedReleaseMBID, &request, &response, &item.IdempotencyKey, &created, &updated)
	if err != nil {
		return item, err
	}
	item.RequestEvidence, item.ResponseEvidence = request, response
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
