package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

type CorrelationEvidence struct {
	ID, JobID, ReleaseGroupMBID, ReleaseMBID, CommandID, DownloadID string
	SourceKind, SourceRecordID, Watermark, EvidenceSHA256           string
	AlbumID                                                         int64
	ObservedAt                                                      time.Time
	Evidence                                                        []byte
}

func (r *Repositories) InsertCorrelationEvidence(ctx context.Context, item CorrelationEvidence) error {
	return insertCorrelationEvidence(ctx, r.DB, item)
}

type evidenceExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertCorrelationEvidence(ctx context.Context, executor evidenceExecutor, item CorrelationEvidence) error {
	id := item.ID
	if id == "" {
		var err error
		id, err = ids.NewToken(16)
		if err != nil {
			return err
		}
	}
	result, err := executor.ExecContext(ctx, `INSERT INTO correlation_evidence(id,job_id,album_id,release_group_mbid,release_mbid,command_id,download_id,source_kind,source_record_id,watermark,observed_at,evidence_json,evidence_sha256) VALUES(?,?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?,?,?,?,?) ON CONFLICT(job_id,source_kind,source_record_id) DO NOTHING`, id, item.JobID, item.AlbumID, item.ReleaseGroupMBID, item.ReleaseMBID, item.CommandID, item.DownloadID, item.SourceKind, item.SourceRecordID, item.Watermark, formatTime(item.ObservedAt), item.Evidence, item.EvidenceSHA256)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var hash string
	if err := executor.QueryRowContext(ctx, `SELECT evidence_sha256 FROM correlation_evidence WHERE job_id=? AND source_kind=? AND source_record_id=?`, item.JobID, item.SourceKind, item.SourceRecordID).Scan(&hash); err != nil {
		return err
	}
	if hash != item.EvidenceSHA256 {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

type PrimarySearchContext struct {
	QueueWatermark, HistoryWatermark, CommandID string
	StartedAt, CommandDeadline, GraceDeadline   time.Time
}

func (r *Repositories) PrimarySearchContext(ctx context.Context, jobID string) (PrimarySearchContext, error) {
	var value PrimarySearchContext
	var queue, history, command, started, commandDeadline, graceDeadline sql.NullString
	if err := r.DB.QueryRowContext(ctx, `SELECT queue_watermark,history_watermark,command_id,correlation_started_at,command_deadline,grace_deadline FROM acquisition_jobs WHERE id=?`, jobID).Scan(&queue, &history, &command, &started, &commandDeadline, &graceDeadline); err != nil {
		return value, err
	}
	if !queue.Valid || !history.Valid || !command.Valid || !started.Valid || !commandDeadline.Valid || !graceDeadline.Valid {
		return value, fmt.Errorf("primary search context is incomplete")
	}
	value.QueueWatermark = queue.String
	value.HistoryWatermark = history.String
	value.CommandID = command.String
	var err error
	value.StartedAt, err = time.Parse(time.RFC3339Nano, started.String)
	if err == nil {
		value.CommandDeadline, err = time.Parse(time.RFC3339Nano, commandDeadline.String)
	}
	if err == nil {
		value.GraceDeadline, err = time.Parse(time.RFC3339Nano, graceDeadline.String)
	}
	return value, err
}

func (r *Repositories) ActivatePrimary(ctx context.Context, command TransitionCommand, evidence []CorrelationEvidence) (domain.Transition, error) {
	var event domain.Transition
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		job, err := jobQuery(ctx, tx, command.JobID)
		if err != nil {
			return err
		}
		event, err = job.Transition(command.Expected, domain.StatePrimaryActive, command.Actor, command.Reason, command.OccurredAt)
		if err != nil {
			return err
		}
		if len(evidence) == 0 {
			return fmt.Errorf("primary activation requires correlation evidence")
		}
		for _, item := range evidence {
			if err := insertCorrelationEvidence(ctx, tx, item); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE acquisition_jobs SET state=?,state_revision=?,primary_attempt=0,next_retry_at=NULL,updated_at=? WHERE id=? AND state_revision=?`, job.State, job.Revision, formatTime(job.UpdatedAt), job.ID, command.Expected)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return &domain.StaleRevisionError{Expected: command.Expected, Current: job.Revision, State: job.State}
		}
		id, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO state_transitions(id,job_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, job.ID, event.Actor, event.Reason, event.Previous, event.Next, event.PreviousRevision, event.Revision, formatTime(event.OccurredAt))
		return err
	})
	return event, err
}
