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

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func jobQuery(ctx context.Context, q rowQuery, id string) (domain.Job, error) {
	var job domain.Job
	var state, created, updated string
	var selected, next sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id,lidarr_album_id,release_group_mbid,selected_release_mbid,config_snapshot_id,state,state_revision,next_retry_at,created_at,updated_at FROM acquisition_jobs WHERE id=?`, id).Scan(&job.ID, &job.LidarrAlbumID, &job.ReleaseGroupMBID, &selected, &job.ConfigSnapshotID, &state, &job.Revision, &next, &created, &updated)
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
	return job, err
}

type TransitionCommand struct {
	JobID         string
	Expected      uint64
	To            domain.State
	Actor, Reason string
	NextRetryAt   *time.Time
	OccurredAt    time.Time
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
		result, err := tx.ExecContext(ctx, `UPDATE acquisition_jobs SET state=?,state_revision=?,next_retry_at=?,updated_at=? WHERE id=? AND state_revision=?`, job.State, job.Revision, retry, formatTime(job.UpdatedAt), job.ID, command.Expected)
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
func (r *Repositories) SetSearchContext(ctx context.Context, jobID string, expected uint64, queueWatermark, historyWatermark, commandID string, started, commandDeadline, graceDeadline time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE acquisition_jobs SET queue_watermark=?,history_watermark=?,command_id=?,correlation_started_at=?,command_deadline=?,grace_deadline=?,updated_at=? WHERE id=? AND state_revision=?`, queueWatermark, historyWatermark, commandID, formatTime(started), formatTime(commandDeadline), formatTime(graceDeadline), formatTime(started), jobID, expected)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("stale search context revision")
	}
	return nil
}
