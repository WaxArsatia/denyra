package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"time"
)

func (r *Repositories) CreateJob(ctx context.Context, job domain.Job) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO acquisition_jobs(id,lidarr_album_id,release_group_mbid,selected_release_mbid,config_snapshot_id,state,state_revision,next_retry_at,created_at,updated_at) VALUES(?,?,?,NULLIF(?,''),?,?,?,NULL,?,?)`, job.ID, job.LidarrAlbumID, job.ReleaseGroupMBID, job.SelectedReleaseMBID, job.ConfigSnapshotID, job.State, job.Revision, formatTime(job.CreatedAt), formatTime(job.UpdatedAt))
	return err
}
func (r *Repositories) Job(ctx context.Context, id string) (domain.Job, error) {
	return jobQuery(ctx, r.DB, id)
}
func (r *Repositories) FindActiveJob(ctx context.Context, albumID int64, releaseGroup string) (domain.Job, error) {
	var id string
	if err := r.DB.QueryRowContext(ctx, `SELECT id FROM acquisition_jobs WHERE lidarr_album_id=? AND release_group_mbid=? AND state NOT IN ('HANDED_OFF','CANCELLED')`, albumID, releaseGroup).Scan(&id); err != nil {
		return domain.Job{}, err
	}
	return r.Job(ctx, id)
}
func (r *Repositories) ReviseSelectedRelease(ctx context.Context, jobID string, expected uint64, selected string, at time.Time) error {
	at = at.UTC()
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		job, err := jobQuery(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if job.Revision != expected {
			return &domain.StaleRevisionError{Expected: expected, Current: job.Revision, State: job.State}
		}
		targetState := domain.StateDiscovered
		reason := "selected MusicBrainz release changed"
		var result sql.Result
		if job.SelectedReleaseMBID == "" && selected != "" {
			targetState = job.State
			reason = "selected MusicBrainz release backfilled from monitored Lidarr release"
			result, err = tx.ExecContext(ctx, `UPDATE acquisition_jobs SET selected_release_mbid=?,selected_release_revision=selected_release_revision+1,state_revision=state_revision+1,updated_at=? WHERE id=? AND state_revision=?`, selected, formatTime(at), jobID, expected)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE acquisition_jobs SET selected_release_mbid=NULLIF(?,''),selected_release_revision=selected_release_revision+1,state='DISCOVERED',state_revision=state_revision+1,primary_attempt=0,fallback_attempt=0,next_retry_at=NULL,queue_watermark=NULL,history_watermark=NULL,command_id=NULL,correlation_started_at=NULL,command_deadline=NULL,grace_deadline=NULL,overall_deadline=NULL,updated_at=? WHERE id=? AND state_revision=?`, selected, formatTime(at), jobID, expected)
		}
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return fmt.Errorf("stale selected release revision")
		}
		id, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO state_transitions(id,job_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, jobID, "gateway-reconciliation", reason, job.State, targetState, expected, expected+1, formatTime(at))
		return err
	})
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func jobQuery(ctx context.Context, q rowQuery, id string) (domain.Job, error) {
	var job domain.Job
	var state, created, updated string
	var selected, next, overall sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id,lidarr_album_id,release_group_mbid,selected_release_mbid,selected_release_revision,config_snapshot_id,state,state_revision,primary_attempt,fallback_attempt,next_retry_at,overall_deadline,created_at,updated_at FROM acquisition_jobs WHERE id=?`, id).Scan(&job.ID, &job.LidarrAlbumID, &job.ReleaseGroupMBID, &selected, &job.SelectedReleaseRevision, &job.ConfigSnapshotID, &state, &job.Revision, &job.PrimaryAttempt, &job.FallbackAttempt, &next, &overall, &created, &updated)
	if err != nil {
		return job, err
	}
	job.SelectedReleaseMBID = selected.String
	job.State, err = domain.ParseState(state)
	if err != nil {
		return job, err
	}
	job.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err == nil {
		job.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	if err == nil && next.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, next.String)
		err = parseErr
		job.NextRetryAt = &value
	}
	if err == nil && overall.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, overall.String)
		err = parseErr
		job.OverallDeadline = &value
	}
	return job, err
}

type TransitionCommand struct {
	JobID                    string
	Expected                 uint64
	To                       domain.State
	Actor, Reason            string
	NextRetryAt              *time.Time
	IncrementPrimaryAttempt  bool
	IncrementFallbackAttempt bool
	OccurredAt               time.Time
}

func (r *Repositories) UpdateState(ctx context.Context, command TransitionCommand) (domain.Transition, error) {
	var event domain.Transition
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		job, err := jobQuery(ctx, tx, command.JobID)
		if err != nil {
			return err
		}
		event, err = job.Transition(command.Expected, command.To, command.Actor, command.Reason, command.OccurredAt)
		if err != nil {
			return err
		}
		var retry any
		if command.NextRetryAt != nil {
			retry = formatTime(*command.NextRetryAt)
		}
		primaryIncrement := 0
		if command.IncrementPrimaryAttempt {
			primaryIncrement = 1
		}
		fallbackIncrement := 0
		if command.IncrementFallbackAttempt {
			fallbackIncrement = 1
		}
		result, err := tx.ExecContext(ctx, `UPDATE acquisition_jobs SET state=?,state_revision=?,primary_attempt=primary_attempt+?,fallback_attempt=fallback_attempt+?,next_retry_at=?,updated_at=? WHERE id=? AND state_revision=?`, job.State, job.Revision, primaryIncrement, fallbackIncrement, retry, formatTime(job.UpdatedAt), job.ID, command.Expected)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			current, load := jobQuery(ctx, tx, job.ID)
			if load != nil {
				return load
			}
			return &domain.StaleRevisionError{Expected: command.Expected, Current: current.Revision, State: current.State}
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

func (r *Repositories) RestartAcquisitionCycle(ctx context.Context, jobID string, expected uint64, nextRetryAt, at time.Time) (domain.Transition, error) {
	var event domain.Transition
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		job, err := jobQuery(ctx, tx, jobID)
		if err != nil {
			return err
		}
		event, err = job.Transition(expected, domain.StatePrimaryRetryableError, "gateway-fallback", "fallback acquisition cycle deadline expired", at)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE acquisition_jobs SET state='PRIMARY_RETRYABLE_ERROR',state_revision=?,primary_attempt=primary_attempt+1,fallback_attempt=0,next_retry_at=?,queue_watermark=NULL,history_watermark=NULL,command_id=NULL,correlation_started_at=NULL,command_deadline=NULL,grace_deadline=NULL,overall_deadline=NULL,updated_at=? WHERE id=? AND state_revision=?`, event.Revision, formatTime(nextRetryAt), formatTime(at), jobID, expected)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			current, loadErr := jobQuery(ctx, tx, jobID)
			if loadErr != nil {
				return loadErr
			}
			return &domain.StaleRevisionError{Expected: expected, Current: current.Revision, State: current.State}
		}
		id, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO state_transitions(id,job_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, jobID, event.Actor, event.Reason, event.Previous, event.Next, event.PreviousRevision, event.Revision, formatTime(event.OccurredAt))
		return err
	})
	return event, err
}
func (r *Repositories) SetSearchContext(ctx context.Context, jobID string, expected uint64, queueWatermark, historyWatermark, commandID string, started, commandDeadline time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE acquisition_jobs SET queue_watermark=?,history_watermark=?,command_id=?,correlation_started_at=?,command_deadline=?,grace_deadline=NULL,updated_at=? WHERE id=? AND state_revision=?`, queueWatermark, historyWatermark, commandID, formatTime(started), formatTime(commandDeadline), formatTime(started), jobID, expected)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("stale search context revision")
	}
	return nil
}

func (r *Repositories) SetInitialSearchContext(ctx context.Context, jobID string, expected uint64, queueWatermark, historyWatermark string, started, commandDeadline, recoveryDeadline time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE acquisition_jobs SET queue_watermark=?,history_watermark=?,command_id='UNKNOWN',correlation_started_at=?,command_deadline=?,grace_deadline=?,updated_at=? WHERE id=? AND state_revision=?`, queueWatermark, historyWatermark, formatTime(started), formatTime(commandDeadline), formatTime(recoveryDeadline), formatTime(started), jobID, expected)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("stale initial search context revision")
	}
	return nil
}

func (r *Repositories) SetSearchCommandID(ctx context.Context, jobID string, expected uint64, commandID string, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE acquisition_jobs SET command_id=?,updated_at=? WHERE id=? AND state_revision=?`, commandID, formatTime(at), jobID, expected)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("stale search command context revision")
	}
	return nil
}

func (r *Repositories) SetGraceDeadline(ctx context.Context, jobID string, expected uint64, deadline, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE acquisition_jobs SET grace_deadline=?,updated_at=? WHERE id=? AND state_revision=?`, formatTime(deadline), formatTime(at), jobID, expected)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("stale grace deadline revision")
	}
	return nil
}

func (r *Repositories) SetOverallDeadline(ctx context.Context, jobID string, expected uint64, deadline, at time.Time) (time.Time, error) {
	result, err := r.DB.ExecContext(ctx, `UPDATE acquisition_jobs SET overall_deadline=?,updated_at=? WHERE id=? AND state_revision=? AND overall_deadline IS NULL`, formatTime(deadline), formatTime(at), jobID, expected)
	if err != nil {
		return time.Time{}, err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return deadline.UTC(), nil
	}
	var stored string
	if err := r.DB.QueryRowContext(ctx, `SELECT overall_deadline FROM acquisition_jobs WHERE id=? AND state_revision=?`, jobID, expected).Scan(&stored); err != nil {
		return time.Time{}, fmt.Errorf("stale overall deadline revision: %w", err)
	}
	return time.Parse(time.RFC3339Nano, stored)
}
