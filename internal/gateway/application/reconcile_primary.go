package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type PrimaryObservations interface {
	QueueAfter(context.Context, string, int) ([]lidarr.QueueRecord, error)
	HistoryAfter(context.Context, string, int) ([]lidarr.HistoryRecord, error)
	Album(context.Context, int64) (lidarr.WantedAlbum, error)
}

type PrimaryReconciler struct {
	Lidarr       PrimaryObservations
	Store        *persistence.Repositories
	Policy       domain.RetryPolicy
	PageSize     int
	PollInterval time.Duration
	Pause        func(context.Context, time.Duration) error
	Now          func() time.Time
}

func (service PrimaryReconciler) Run(ctx context.Context, jobID string) error {
	if service.Lidarr == nil || service.Store == nil || service.PageSize <= 0 || service.PollInterval <= 0 || service.Pause == nil {
		return fmt.Errorf("primary reconciler is not configured")
	}
	for {
		job, err := service.Store.Job(ctx, jobID)
		if err != nil {
			return err
		}
		if job.State == domain.StatePrimaryActive {
			return nil
		}
		if job.State != domain.StatePrimaryReconciling {
			return fmt.Errorf("job %s cannot reconcile primary evidence from %s", job.ID, job.State)
		}
		search, err := service.Store.PrimarySearchContext(ctx, job.ID)
		if err != nil {
			return service.retryableError(ctx, job, err)
		}
		request, err := correlationRequest(job, search)
		if err != nil {
			return service.retryableError(ctx, job, err)
		}
		evidence, err := service.correlatedEvidence(ctx, job, search, request)
		if err != nil {
			return service.retryableError(ctx, job, err)
		}
		if len(evidence) > 0 {
			_, err := service.Store.ActivatePrimary(ctx, persistence.TransitionCommand{
				JobID:      job.ID,
				Expected:   job.Revision,
				Actor:      "gateway-reconciliation",
				Reason:     "correlated primary grab",
				OccurredAt: service.now(),
			}, evidence)
			return err
		}
		now := service.now()
		if !now.Before(search.GraceDeadline) {
			_, err := service.Store.UpdateState(ctx, persistence.TransitionCommand{
				JobID:      job.ID,
				Expected:   job.Revision,
				To:         domain.StateFallbackRunning,
				Actor:      "gateway-reconciliation",
				Reason:     "successful AlbumSearch produced no correlated primary grab through grace deadline",
				OccurredAt: now,
			})
			return err
		}
		pause := service.PollInterval
		if remaining := search.GraceDeadline.Sub(now); remaining < pause {
			pause = remaining
		}
		if err := service.Pause(ctx, pause); err != nil {
			return err
		}
	}
}

func correlationRequest(job domain.Job, search persistence.PrimarySearchContext) (domain.CorrelationRequest, error) {
	queue, err := strconv.ParseInt(search.QueueWatermark, 10, 64)
	if err != nil {
		return domain.CorrelationRequest{}, fmt.Errorf("parse queue watermark: %w", err)
	}
	history, err := strconv.ParseInt(search.HistoryWatermark, 10, 64)
	if err != nil {
		return domain.CorrelationRequest{}, fmt.Errorf("parse history watermark: %w", err)
	}
	request := domain.CorrelationRequest{
		AlbumID:          job.LidarrAlbumID,
		ReleaseGroupMBID: job.ReleaseGroupMBID,
		ReleaseMBID:      job.SelectedReleaseMBID,
		CommandID:        search.CommandID,
		QueueWatermark:   queue,
		HistoryWatermark: history,
		StartedAt:        search.StartedAt,
		Deadline:         search.GraceDeadline,
	}
	return request, request.Validate()
}

func (service PrimaryReconciler) correlatedEvidence(ctx context.Context, job domain.Job, search persistence.PrimarySearchContext, request domain.CorrelationRequest) ([]persistence.CorrelationEvidence, error) {
	queues, err := service.Lidarr.QueueAfter(ctx, search.QueueWatermark, service.PageSize)
	if err != nil {
		return nil, err
	}
	history, err := service.Lidarr.HistoryAfter(ctx, search.HistoryWatermark, service.PageSize)
	if err != nil {
		return nil, err
	}
	albumCache := make(map[int64]lidarr.WantedAlbum)
	lookup := func(albumID int64) (lidarr.WantedAlbum, error) {
		if album, found := albumCache[albumID]; found {
			return album, nil
		}
		album, err := service.Lidarr.Album(ctx, albumID)
		if err == nil {
			albumCache[albumID] = album
		}
		return album, err
	}
	observedAt := service.now()
	var result []persistence.CorrelationEvidence
	for _, record := range queues {
		if record.AlbumID != job.LidarrAlbumID {
			continue
		}
		album, err := lookup(record.AlbumID)
		if err != nil {
			return nil, err
		}
		group := record.ReleaseGroupMBID
		if group == "" {
			group = album.ReleaseGroupMBID
		}
		release := record.SelectedReleaseMBID
		if release == "" {
			release = album.SelectedReleaseMBID
		}
		observation := domain.CorrelationObservation{Source: domain.CorrelationQueue, RecordID: record.ID, AlbumID: record.AlbumID, ReleaseGroupMBID: group, ReleaseMBID: release, DownloadID: record.DownloadID, ObservedAt: observedAt}
		if request.Match(observation).Correlated {
			item, err := evidenceFromObservation(job, search.QueueWatermark, observation, record)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
	}
	for _, record := range history {
		if record.AlbumID != job.LidarrAlbumID {
			continue
		}
		album, err := lookup(record.AlbumID)
		if err != nil {
			return nil, err
		}
		group := record.DataString("releaseGroupId")
		if group == "" {
			group = album.ReleaseGroupMBID
		}
		release := record.DataString("releaseId")
		if release == "" {
			release = album.SelectedReleaseMBID
		}
		downloadID := record.DownloadID
		if downloadID == "" {
			downloadID = record.DataString("downloadId")
		}
		observation := domain.CorrelationObservation{Source: domain.CorrelationHistory, RecordID: record.ID, AlbumID: record.AlbumID, ReleaseGroupMBID: group, ReleaseMBID: release, CommandID: record.DataString("commandId"), DownloadID: downloadID, EventType: record.EventType, ObservedAt: observedAt}
		if request.Match(observation).Correlated {
			item, err := evidenceFromObservation(job, search.HistoryWatermark, observation, record)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func evidenceFromObservation(job domain.Job, watermark string, observation domain.CorrelationObservation, raw any) (persistence.CorrelationEvidence, error) {
	payload, err := json.Marshal(struct {
		Observation domain.CorrelationObservation `json:"observation"`
		Record      any                           `json:"record"`
	}{Observation: observation, Record: raw})
	if err != nil {
		return persistence.CorrelationEvidence{}, err
	}
	sum := sha256.Sum256(payload)
	return persistence.CorrelationEvidence{
		JobID:            job.ID,
		AlbumID:          observation.AlbumID,
		ReleaseGroupMBID: observation.ReleaseGroupMBID,
		ReleaseMBID:      observation.ReleaseMBID,
		CommandID:        observation.CommandID,
		DownloadID:       observation.DownloadID,
		SourceKind:       string(observation.Source),
		SourceRecordID:   strconv.FormatInt(observation.RecordID, 10),
		Watermark:        watermark,
		ObservedAt:       observation.ObservedAt,
		Evidence:         payload,
		EvidenceSHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func (service PrimaryReconciler) retryableError(ctx context.Context, job domain.Job, cause error) error {
	deadline, err := service.Policy.PrimaryDeadline(service.now(), job.PrimaryAttempt)
	if err != nil {
		return err
	}
	_, transitionErr := service.Store.UpdateState(ctx, persistence.TransitionCommand{
		JobID:                   job.ID,
		Expected:                job.Revision,
		To:                      domain.StatePrimaryRetryableError,
		Actor:                   "gateway-reconciliation",
		Reason:                  cause.Error(),
		NextRetryAt:             &deadline,
		IncrementPrimaryAttempt: true,
		OccurredAt:              service.now(),
	})
	if transitionErr != nil {
		return transitionErr
	}
	return cause
}

func (service PrimaryReconciler) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}
