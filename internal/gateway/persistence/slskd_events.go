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

func (r *Repositories) SlskdCompletionEventsSince(ctx context.Context, since time.Time) ([]SlskdCompletionEvent, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,event_version,transfer_id,COALESCE(batch_id,''),local_filename,remote_filename,transfer_state,event_timestamp,payload_json,payload_sha256,received_at FROM slskd_completion_events WHERE received_at>=? AND transfer_state='Completed, Succeeded' ORDER BY received_at,id`, formatTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []SlskdCompletionEvent
	for rows.Next() {
		var event SlskdCompletionEvent
		var timestamp, receivedAt string
		if err := rows.Scan(&event.ID, &event.Version, &event.TransferID, &event.BatchID, &event.LocalFilename, &event.RemoteFilename, &event.TransferState, &timestamp, &event.Payload, &event.PayloadSHA256, &receivedAt); err != nil {
			return nil, err
		}
		event.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, err
		}
		event.ReceivedAt, err = time.Parse(time.RFC3339Nano, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
