package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
)

type SlskdCompletionEvent struct {
	ID, TransferID, BatchID, LocalFilename, RemoteFilename, TransferState string
	Version                                                               int
	Timestamp, ReceivedAt                                                 time.Time
	Payload                                                               []byte
	PayloadSHA256                                                         string
}

func (r *Repositories) RecordSlskdCompletionEvent(ctx context.Context, event SlskdCompletionEvent) error {
	result, err := r.DB.ExecContext(ctx, `INSERT INTO slskd_completion_events(id,event_version,transfer_id,batch_id,local_filename,remote_filename,transfer_state,event_timestamp,payload_json,payload_sha256,received_at) VALUES(?,?,?,NULLIF(?,''),?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, event.ID, event.Version, event.TransferID, event.BatchID, event.LocalFilename, event.RemoteFilename, event.TransferState, formatTime(event.Timestamp), event.Payload, event.PayloadSHA256, formatTime(event.ReceivedAt))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var storedHash string
	if err := r.DB.QueryRowContext(ctx, `SELECT payload_sha256 FROM slskd_completion_events WHERE id=?`, event.ID).Scan(&storedHash); err != nil {
		if err == sql.ErrNoRows {
			return contracts.ErrIdempotencyConflict
		}
		return err
	}
	if storedHash != event.PayloadSHA256 {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}
