package lidarr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

const managedLibraryRoot = "/data/library"

type CatalogResult struct {
	ArtistID       int `json:"artist_id"`
	AlbumID        int `json:"album_id"`
	AlbumReleaseID int `json:"album_release_id"`
}

type Catalog struct {
	Client       Client
	PollAttempts int
	PollInterval time.Duration
	Pause        func(context.Context, time.Duration) error
}

type catalogArtist struct {
	ID                int    `json:"id"`
	ForeignArtistID   string `json:"foreignArtistId"`
	ArtistName        string `json:"artistName"`
	QualityProfileID  int    `json:"qualityProfileId"`
	MetadataProfileID int    `json:"metadataProfileId"`
}

type catalogAlbum struct {
	ID        int    `json:"id"`
	ArtistID  int    `json:"artistId"`
	Title     string `json:"title"`
	Monitored bool   `json:"monitored"`
	Releases  []struct {
		ID               int    `json:"id"`
		ForeignReleaseID string `json:"foreignReleaseId"`
	} `json:"releases"`
}

func (c Catalog) EnsureRelease(ctx context.Context, release domain.CanonicalRelease) (CatalogResult, error) {
	if _, err := domain.CanonicalMBID(release.ReleaseMBID); err != nil {
		return CatalogResult{}, fmt.Errorf("release MBID: %w", err)
	}
	if len(release.ArtistCredits) == 0 || len(release.Tracks) == 0 {
		return CatalogResult{}, fmt.Errorf("release requires a primary artist and tracks")
	}
	artistMBID, err := domain.CanonicalMBID(release.ArtistCredits[0].ArtistMBID)
	if err != nil {
		return CatalogResult{}, fmt.Errorf("primary artist MBID: %w", err)
	}

	root, qualityID, metadataID, err := c.catalogDefaults(ctx)
	if err != nil {
		return CatalogResult{}, err
	}
	lookup, err := c.lookupArtist(ctx, artistMBID)
	if err != nil {
		return CatalogResult{}, err
	}
	if lookup.QualityProfileID > 0 {
		qualityID = lookup.QualityProfileID
	}
	if lookup.MetadataProfileID > 0 {
		metadataID = lookup.MetadataProfileID
	}

	artist, found, err := c.existingArtist(ctx, artistMBID)
	if err != nil {
		return CatalogResult{}, err
	}
	if !found {
		artist, err = c.addArtist(ctx, lookup, root, qualityID, metadataID)
		if err != nil {
			return CatalogResult{}, err
		}
	}
	if artist.ID <= 0 {
		return CatalogResult{}, fmt.Errorf("Lidarr artist response omitted ID")
	}

	if result, album, found, err := c.findRelease(ctx, artist.ID, release.ReleaseMBID); err != nil {
		return CatalogResult{}, err
	} else if found {
		return c.monitorAndVerify(ctx, result, album, release.ReleaseMBID)
	}
	if err := c.refreshArtist(ctx, artist.ID); err != nil {
		return CatalogResult{}, err
	}
	for attempt := 0; attempt < c.attempts(); attempt++ {
		result, album, found, err := c.findRelease(ctx, artist.ID, release.ReleaseMBID)
		if err != nil {
			return CatalogResult{}, err
		}
		if found {
			return c.monitorAndVerify(ctx, result, album, release.ReleaseMBID)
		}
		if attempt+1 < c.attempts() {
			if err := c.pause(ctx); err != nil {
				return CatalogResult{}, err
			}
		}
	}
	return CatalogResult{}, fmt.Errorf("Lidarr refresh did not expose exact release %s", release.ReleaseMBID)
}

func (c Catalog) catalogDefaults(ctx context.Context) (string, int, int, error) {
	var roots []struct {
		Path                     string `json:"path"`
		DefaultQualityProfileID  int    `json:"defaultQualityProfileId"`
		DefaultMetadataProfileID int    `json:"defaultMetadataProfileId"`
	}
	if err := c.Client.Get(ctx, "/api/v1/rootfolder", nil, &roots); err != nil {
		return "", 0, 0, err
	}
	qualityIDs, err := c.profileIDs(ctx, "/api/v1/qualityprofile")
	if err != nil {
		return "", 0, 0, err
	}
	metadataIDs, err := c.profileIDs(ctx, "/api/v1/metadataprofile")
	if err != nil {
		return "", 0, 0, err
	}
	for _, root := range roots {
		if root.Path != managedLibraryRoot {
			continue
		}
		quality := usableProfile(root.DefaultQualityProfileID, qualityIDs)
		metadata := usableProfile(root.DefaultMetadataProfileID, metadataIDs)
		if quality == 0 || metadata == 0 {
			return "", 0, 0, fmt.Errorf("Lidarr managed root has no usable default quality or metadata profile")
		}
		return root.Path, quality, metadata, nil
	}
	return "", 0, 0, fmt.Errorf("Lidarr root folder %s is not configured", managedLibraryRoot)
}

func (c Catalog) profileIDs(ctx context.Context, path string) ([]int, error) {
	var values []struct {
		ID      int  `json:"id"`
		Default bool `json:"default"`
	}
	if err := c.Client.Get(ctx, path, nil, &values); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(values))
	for _, value := range values {
		if value.ID > 0 {
			ids = append(ids, value.ID)
		}
	}
	return ids, nil
}

func usableProfile(preferred int, available []int) int {
	for _, id := range available {
		if id == preferred {
			return id
		}
	}
	if preferred == 0 && len(available) == 1 {
		return available[0]
	}
	return 0
}

func (c Catalog) lookupArtist(ctx context.Context, artistMBID string) (catalogArtist, error) {
	var results []catalogArtist
	if err := c.Client.Get(ctx, "/api/v1/artist/lookup", url.Values{"term": {"lidarr:" + artistMBID}}, &results); err != nil {
		return catalogArtist{}, err
	}
	for _, artist := range results {
		if strings.EqualFold(artist.ForeignArtistID, artistMBID) {
			return artist, nil
		}
	}
	return catalogArtist{}, fmt.Errorf("Lidarr artist lookup omitted exact MusicBrainz artist %s", artistMBID)
}

func (c Catalog) existingArtist(ctx context.Context, artistMBID string) (catalogArtist, bool, error) {
	var artists []catalogArtist
	if err := c.Client.Get(ctx, "/api/v1/artist", nil, &artists); err != nil {
		return catalogArtist{}, false, err
	}
	for _, artist := range artists {
		if strings.EqualFold(artist.ForeignArtistID, artistMBID) {
			return artist, true, nil
		}
	}
	return catalogArtist{}, false, nil
}

func (c Catalog) addArtist(ctx context.Context, lookup catalogArtist, root string, qualityID, metadataID int) (catalogArtist, error) {
	payload := map[string]any{
		"foreignArtistId": lookup.ForeignArtistID, "artistName": lookup.ArtistName,
		"rootFolderPath": root, "qualityProfileId": qualityID, "metadataProfileId": metadataID,
		"monitored": false, "monitorNewItems": "none",
		"addOptions": map[string]any{"monitor": "none", "searchForMissingAlbums": false},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return catalogArtist{}, err
	}
	var artist catalogArtist
	if err := c.Client.Post(ctx, "/api/v1/artist", body, &artist); err != nil {
		return artist, err
	}
	return artist, nil
}

func (c Catalog) findRelease(ctx context.Context, artistID int, releaseMBID string) (CatalogResult, catalogAlbum, bool, error) {
	var albums []catalogAlbum
	if err := c.Client.Get(ctx, "/api/v1/album", url.Values{"artistId": {fmt.Sprint(artistID)}}, &albums); err != nil {
		return CatalogResult{}, catalogAlbum{}, false, err
	}
	for _, album := range albums {
		for _, release := range album.Releases {
			if strings.EqualFold(release.ForeignReleaseID, releaseMBID) {
				return CatalogResult{ArtistID: artistID, AlbumID: album.ID, AlbumReleaseID: release.ID}, album, true, nil
			}
		}
	}
	return CatalogResult{}, catalogAlbum{}, false, nil
}

func (c Catalog) refreshArtist(ctx context.Context, artistID int) error {
	body, _ := json.Marshal(map[string]any{"name": "RefreshArtist", "artistId": artistID})
	var command struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	if err := c.Client.Post(ctx, "/api/v1/command", body, &command); err != nil {
		return err
	}
	if strings.EqualFold(command.Status, "completed") {
		return nil
	}
	if command.ID <= 0 {
		return fmt.Errorf("Lidarr RefreshArtist command omitted ID")
	}
	for attempt := 0; attempt < c.attempts(); attempt++ {
		if err := c.Client.Get(ctx, fmt.Sprintf("/api/v1/command/%d", command.ID), nil, &command); err != nil {
			return err
		}
		switch strings.ToLower(command.Status) {
		case "completed":
			return nil
		case "failed", "aborted":
			return fmt.Errorf("Lidarr RefreshArtist ended with status %s", command.Status)
		}
		if attempt+1 < c.attempts() {
			if err := c.pause(ctx); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("Lidarr RefreshArtist did not complete within bounded polling")
}

func (c Catalog) monitorAndVerify(ctx context.Context, result CatalogResult, album catalogAlbum, releaseMBID string) (CatalogResult, error) {
	if !album.Monitored {
		album.Monitored = true
		body, err := json.Marshal(album)
		if err != nil {
			return CatalogResult{}, err
		}
		if err := c.Client.Put(ctx, fmt.Sprintf("/api/v1/album/%d", album.ID), body, nil); err != nil {
			return CatalogResult{}, err
		}
	}
	var verified catalogAlbum
	if err := c.Client.Get(ctx, fmt.Sprintf("/api/v1/album/%d", album.ID), nil, &verified); err != nil {
		return CatalogResult{}, err
	}
	if !verified.Monitored {
		return CatalogResult{}, fmt.Errorf("Lidarr target album is not monitored")
	}
	for _, release := range verified.Releases {
		if release.ID == result.AlbumReleaseID && strings.EqualFold(release.ForeignReleaseID, releaseMBID) {
			return result, nil
		}
	}
	return CatalogResult{}, fmt.Errorf("Lidarr verified album omitted exact release")
}

func (c Catalog) attempts() int {
	if c.PollAttempts > 0 {
		return c.PollAttempts
	}
	return 20
}

func (c Catalog) pause(ctx context.Context) error {
	interval := c.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	if c.Pause != nil {
		return c.Pause(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
