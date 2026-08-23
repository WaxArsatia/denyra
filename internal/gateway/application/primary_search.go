package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type PrimaryLidarr interface {
	QueueWatermark(context.Context) (string, error)
	HistoryWatermark(context.Context) (string, error)
	StartAlbumSearch(context.Context, int64) (lidarr.Command, []byte, error)
	Command(context.Context, int64) (lidarr.Command, error)
}
type PrimarySearch struct {
	Lidarr                                    PrimaryLidarr
	Store                                     *persistence.Repositories
	Policy                                    domain.RetryPolicy
	CommandTimeout, PollInterval, GraceWindow time.Duration
	Pause                                     func(context.Context, time.Duration) error
	Now                                       func() time.Time
}

func (s PrimarySearch) Run(ctx context.Context, jobID string) error {
	if s.Lidarr == nil || s.Store == nil || s.Pause == nil || s.CommandTimeout <= 0 || s.PollInterval <= 0 || s.GraceWindow <= 0 {
		return fmt.Errorf("primary search service is not configured")
	}
	job, err := s.Store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	now := s.now()
	event, err := s.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: domain.StatePrimarySearchRequested, Actor: "gateway", Reason: "monitored wanted album", OccurredAt: now})
	if err != nil {
		return err
	}
	queue, err := s.Lidarr.QueueWatermark(ctx)
	if err != nil {
		return s.primaryError(ctx, job.ID, event.Revision, job.PrimaryAttempt, err)
	}
	history, err := s.Lidarr.HistoryWatermark(ctx)
	if err != nil {
		return s.primaryError(ctx, job.ID, event.Revision, job.PrimaryAttempt, err)
	}
	request, _ := json.Marshal(map[string]any{"name": "AlbumSearch", "albumIds": []int64{job.LidarrAlbumID}})
	sum := sha256.Sum256(request)
	key := "album-search-" + job.ID + "-" + fmt.Sprint(event.Revision)
	if err := s.Store.PutEffect(ctx, persistence.Effect{JobID: job.ID, Type: "ALBUM_SEARCH", IdempotencyKey: key, RequestHash: hex.EncodeToString(sum[:]), Request: request, CreatedAt: now}); err != nil {
		return err
	}
	command, actual, err := s.Lidarr.StartAlbumSearch(ctx, job.LidarrAlbumID)
	if err != nil {
		return s.primaryError(ctx, job.ID, event.Revision, job.PrimaryAttempt, err)
	}
	response, _ := json.Marshal(command)
	responseSum := sha256.Sum256(response)
	if err := s.Store.AcknowledgeEffect(ctx, key, response, hex.EncodeToString(responseSum[:]), s.now()); err != nil {
		return err
	}
	if string(actual) != string(request) {
		return s.primaryError(ctx, job.ID, event.Revision, job.PrimaryAttempt, fmt.Errorf("AlbumSearch request mismatch"))
	}
	running, err := s.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: event.Revision, To: domain.StatePrimarySearchRunning, Actor: "gateway", Reason: "AlbumSearch command accepted", OccurredAt: s.now()})
	if err != nil {
		return err
	}
	started := s.now()
	deadline := started.Add(s.CommandTimeout)
	if err := s.Store.SetSearchContext(ctx, job.ID, running.Revision, queue, history, fmt.Sprint(command.ID), started, deadline); err != nil {
		return err
	}
	for {
		if !s.now().Before(deadline) {
			return s.primaryError(ctx, job.ID, running.Revision, job.PrimaryAttempt, fmt.Errorf("AlbumSearch command timeout"))
		}
		current, err := s.Lidarr.Command(ctx, command.ID)
		if err != nil {
			return s.primaryError(ctx, job.ID, running.Revision, job.PrimaryAttempt, err)
		}
		switch strings.ToLower(current.Status) {
		case "completed", "successful":
			completedAt := s.now()
			if err := s.Store.SetGraceDeadline(ctx, job.ID, running.Revision, completedAt.Add(s.GraceWindow), completedAt); err != nil {
				return err
			}
			_, err = s.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: running.Revision, To: domain.StatePrimaryReconciling, Actor: "gateway", Reason: "AlbumSearch completed successfully", OccurredAt: completedAt})
			return err
		case "failed", "aborted", "cancelled":
			return s.primaryError(ctx, job.ID, running.Revision, job.PrimaryAttempt, fmt.Errorf("AlbumSearch status %s", current.Status))
		}
		if err := s.Pause(ctx, s.PollInterval); err != nil {
			return err
		}
	}
}
func (s PrimarySearch) primaryError(ctx context.Context, jobID string, revision uint64, attempt int, cause error) error {
	deadline, err := s.Policy.PrimaryDeadline(s.now(), attempt)
	if err != nil {
		return err
	}
	_, transitionErr := s.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: jobID, Expected: revision, To: domain.StatePrimaryRetryableError, Actor: "gateway", Reason: cause.Error(), NextRetryAt: &deadline, IncrementPrimaryAttempt: true, OccurredAt: s.now()})
	if transitionErr != nil {
		return transitionErr
	}
	return cause
}
func (s PrimarySearch) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
