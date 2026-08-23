package application

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	"time"
)

type WantedStore interface {
	FindActiveJob(context.Context, int64, string) (domain.Job, error)
	CreateJob(context.Context, domain.Job) error
	ReviseSelectedRelease(context.Context, string, uint64, string, time.Time) error
}
type WantedDiscovery struct {
	Lidarr interface {
		Wanted(context.Context) ([]lidarr.WantedAlbum, error)
	}
	Store            WantedStore
	ConfigSnapshotID string
	Now              func() time.Time
}

func (s WantedDiscovery) Reconcile(ctx context.Context) (int, error) {
	albums, err := s.Lidarr.Wanted(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	changed := 0
	for _, album := range albums {
		if !album.Monitored {
			continue
		}
		existing, err := s.Store.FindActiveJob(ctx, album.AlbumID, album.ReleaseGroupMBID)
		if errors.Is(err, sql.ErrNoRows) {
			id, idErr := ids.NewToken(16)
			if idErr != nil {
				return changed, idErr
			}
			job, newErr := domain.NewJob(id, album.AlbumID, album.ReleaseGroupMBID, s.ConfigSnapshotID, now)
			if newErr != nil {
				return changed, newErr
			}
			job.SelectedReleaseMBID = album.SelectedReleaseMBID
			if err := s.Store.CreateJob(ctx, job); err != nil {
				return changed, err
			}
			changed++
			continue
		}
		if err != nil {
			return changed, err
		}
		if existing.SelectedReleaseMBID != album.SelectedReleaseMBID {
			if err := s.Store.ReviseSelectedRelease(ctx, existing.ID, existing.Revision, album.SelectedReleaseMBID, now); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, nil
}
