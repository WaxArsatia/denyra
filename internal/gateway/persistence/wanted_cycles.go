package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

type WantedKey struct {
	AlbumID          int64
	ReleaseGroupMBID string
}

type wantedCycleJob struct {
	ID               string
	AlbumID          int64
	ReleaseGroupMBID string
	State            domain.State
	Revision         uint64
	HasCycle         bool
	CycleOpen        bool
}

func (r *Repositories) ReconcileWantedCycles(ctx context.Context, wanted []WantedKey, at time.Time) (int, error) {
	at = at.UTC()
	wantedSet := make(map[WantedKey]struct{}, len(wanted))
	for _, key := range wanted {
		wantedSet[key] = struct{}{}
	}
	changed := 0
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		jobs, err := wantedCycleJobs(ctx, tx)
		if err != nil {
			return err
		}
		groups := make(map[WantedKey][]wantedCycleJob)
		for _, job := range jobs {
			key := WantedKey{AlbumID: job.AlbumID, ReleaseGroupMBID: job.ReleaseGroupMBID}
			groups[key] = append(groups[key], job)
		}
		for key, items := range groups {
			_, isWanted := wantedSet[key]
			if !isWanted {
				for _, item := range items {
					if !item.CycleOpen {
						if !item.HasCycle && !item.State.Terminal() && domain.CanTransition(item.State, domain.StateCancelled) {
							if err := cancelWantedCycleJob(ctx, tx, item, "legacy acquisition target left Lidarr Wanted/Missing", at); err != nil {
								return err
							}
							changed++
						}
						continue
					}
					if !item.State.Terminal() && !domain.CanTransition(item.State, domain.StateCancelled) {
						continue
					}
					if _, err := tx.ExecContext(ctx, `UPDATE acquisition_wanted_cycles SET closed_at=? WHERE job_id=? AND closed_at IS NULL`, formatTime(at), item.ID); err != nil {
						return err
					}
					if !item.State.Terminal() {
						if err := cancelWantedCycleJob(ctx, tx, item, "target left Lidarr Wanted/Missing", at); err != nil {
							return err
						}
						changed++
					}
				}
				continue
			}

			keeper := -1
			for index := range items {
				if items[index].CycleOpen {
					keeper = index
					break
				}
			}
			if keeper < 0 {
				for index := range items {
					if !items[index].HasCycle && items[index].State == domain.StateHandedOff {
						keeper = index
						break
					}
				}
			}
			if keeper < 0 {
				for index := range items {
					if !items[index].HasCycle && items[index].State != domain.StateCancelled {
						keeper = index
						break
					}
				}
			}
			if keeper < 0 {
				continue
			}
			kept := items[keeper]
			if !kept.CycleOpen {
				if _, err := tx.ExecContext(ctx, `INSERT INTO acquisition_wanted_cycles(job_id,lidarr_album_id,release_group_mbid,opened_at) VALUES(?,?,?,?)`, kept.ID, kept.AlbumID, kept.ReleaseGroupMBID, formatTime(at)); err != nil {
					return err
				}
			}
			for index, item := range items {
				if index == keeper || item.HasCycle || item.State.Terminal() || !domain.CanTransition(item.State, domain.StateCancelled) {
					continue
				}
				if err := cancelWantedCycleJob(ctx, tx, item, "duplicate Wanted cycle coalesced behind durable handoff", at); err != nil {
					return err
				}
				changed++
			}
		}
		return nil
	})
	return changed, err
}

func wantedCycleJobs(ctx context.Context, tx *sql.Tx) ([]wantedCycleJob, error) {
	rows, err := tx.QueryContext(ctx, `SELECT j.id,j.lidarr_album_id,j.release_group_mbid,j.state,j.state_revision,c.job_id IS NOT NULL,CASE WHEN c.job_id IS NOT NULL AND c.closed_at IS NULL THEN 1 ELSE 0 END FROM acquisition_jobs j LEFT JOIN acquisition_wanted_cycles c ON c.job_id=j.id ORDER BY j.lidarr_album_id,j.release_group_mbid,CASE WHEN j.state='HANDED_OFF' THEN 0 ELSE 1 END,j.created_at,j.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]wantedCycleJob, 0)
	for rows.Next() {
		var item wantedCycleJob
		var state string
		if err := rows.Scan(&item.ID, &item.AlbumID, &item.ReleaseGroupMBID, &state, &item.Revision, &item.HasCycle, &item.CycleOpen); err != nil {
			return nil, err
		}
		parsed, err := domain.ParseState(state)
		if err != nil {
			return nil, err
		}
		item.State = parsed
		jobs = append(jobs, item)
	}
	return jobs, rows.Err()
}

func cancelWantedCycleJob(ctx context.Context, tx *sql.Tx, item wantedCycleJob, reason string, at time.Time) error {
	if !domain.CanTransition(item.State, domain.StateCancelled) {
		return fmt.Errorf("illegal acquisition transition %s -> %s", item.State, domain.StateCancelled)
	}
	result, err := tx.ExecContext(ctx, `UPDATE acquisition_jobs SET state='CANCELLED',state_revision=state_revision+1,next_retry_at=NULL,updated_at=? WHERE id=? AND state_revision=?`, formatTime(at), item.ID, item.Revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return &domain.StaleRevisionError{Expected: item.Revision, Current: item.Revision, State: item.State}
	}
	id, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO state_transitions(id,job_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, item.ID, "gateway-wanted-reconciliation", reason, item.State, domain.StateCancelled, item.Revision, item.Revision+1, formatTime(at))
	return err
}
