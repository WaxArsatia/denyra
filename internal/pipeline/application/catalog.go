package application

import (
	"context"
	"fmt"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type CatalogResult = lidarr.CatalogResult

type LidarrCatalog interface {
	EnsureRelease(context.Context, domain.CanonicalRelease) (CatalogResult, error)
}

type LidarrCatalogService struct{ Catalog LidarrCatalog }

func (s LidarrCatalogService) EnsureRelease(ctx context.Context, release domain.CanonicalRelease) (CatalogResult, error) {
	if s.Catalog == nil {
		return CatalogResult{}, fmt.Errorf("Lidarr catalog is not configured")
	}
	if _, err := domain.CanonicalMBID(release.ReleaseMBID); err != nil {
		return CatalogResult{}, err
	}
	if len(release.ArtistCredits) == 0 || len(release.Tracks) == 0 {
		return CatalogResult{}, fmt.Errorf("canonical release artist and tracks are required")
	}
	result, err := s.Catalog.EnsureRelease(ctx, release)
	if err != nil {
		return CatalogResult{}, err
	}
	if result.ArtistID <= 0 || result.AlbumID <= 0 || result.AlbumReleaseID <= 0 {
		return CatalogResult{}, fmt.Errorf("Lidarr catalog response omitted concrete artist, album, or album release ID")
	}
	return result, nil
}
