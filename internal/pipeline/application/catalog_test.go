package application_test

import (
	"context"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestLidarrCatalogServiceRequiresConfiguredCatalogAndCanonicalIdentity(t *testing.T) {
	t.Parallel()
	if _, err := (application.LidarrCatalogService{}).EnsureRelease(context.Background(), domain.CanonicalRelease{}); err == nil {
		t.Fatal("missing catalog accepted")
	}
	fake := catalogFake{result: application.CatalogResult{ArtistID: 1, AlbumID: 2, AlbumReleaseID: 3}}
	release := domain.CanonicalRelease{ReleaseMBID: "11111111-1111-1111-1111-111111111111", ArtistCredits: []domain.ArtistCredit{{Name: "Artist", ArtistMBID: "22222222-2222-2222-2222-222222222222"}}, Tracks: []domain.CanonicalTrack{{ReleaseTrackMBID: "33333333-3333-3333-3333-333333333333", RecordingMBID: "44444444-4444-4444-4444-444444444444", Disc: 1, Track: 1}}}
	result, err := (application.LidarrCatalogService{Catalog: fake}).EnsureRelease(context.Background(), release)
	if err != nil || result != fake.result {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := (application.LidarrCatalogService{Catalog: catalogFake{}}).EnsureRelease(context.Background(), release); err == nil {
		t.Fatal("catalog result without concrete Lidarr IDs accepted")
	}
}

type catalogFake struct{ result application.CatalogResult }

func (f catalogFake) EnsureRelease(context.Context, domain.CanonicalRelease) (application.CatalogResult, error) {
	return f.result, nil
}
