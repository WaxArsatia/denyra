package lidarr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type ManualImporter struct{ Client Client }

type manualResource struct {
	ID                      int             `json:"id"`
	Path                    string          `json:"path"`
	Name                    string          `json:"name"`
	Artist                  resourceID      `json:"artist"`
	Album                   resourceID      `json:"album"`
	AlbumReleaseID          int             `json:"albumReleaseId"`
	Tracks                  []resourceID    `json:"tracks"`
	Quality                 json.RawMessage `json:"quality"`
	ReleaseGroup            string          `json:"releaseGroup"`
	IndexerFlags            int             `json:"indexerFlags"`
	DownloadID              string          `json:"downloadId"`
	AdditionalFile          bool            `json:"additionalFile"`
	ReplaceExistingFiles    bool            `json:"replaceExistingFiles"`
	DisableReleaseSwitching bool            `json:"disableReleaseSwitching"`
	Rejections              []struct {
		Reason string `json:"reason"`
	} `json:"rejections"`
}

type resourceID struct {
	ID int `json:"id"`
}

type albumResource struct {
	ID       int `json:"id"`
	Releases []struct {
		ID               int    `json:"id"`
		ForeignReleaseID string `json:"foreignReleaseId"`
	} `json:"releases"`
}

type manualUpdate struct {
	ID                      int             `json:"id"`
	Path                    string          `json:"path"`
	Name                    string          `json:"name"`
	ArtistID                int             `json:"artistId"`
	AlbumID                 int             `json:"albumId"`
	AlbumReleaseID          int             `json:"albumReleaseId"`
	TrackIDs                []int           `json:"trackIds"`
	Quality                 json.RawMessage `json:"quality"`
	ReleaseGroup            string          `json:"releaseGroup"`
	IndexerFlags            int             `json:"indexerFlags"`
	DownloadID              string          `json:"downloadId,omitempty"`
	AdditionalFile          bool            `json:"additionalFile"`
	ReplaceExistingFiles    bool            `json:"replaceExistingFiles"`
	DisableReleaseSwitching bool            `json:"disableReleaseSwitching"`
}

func (m ManualImporter) Prepare(ctx context.Context, approvedPath, releaseMBID, downloadID string, expectedFLAC int) (domain.LidarrImportPlan, error) {
	query := url.Values{"folder": {approvedPath}, "filterExistingFiles": {"true"}, "replaceExistingFiles": {"true"}}
	var resources []manualResource
	if err := m.Client.Get(ctx, "/api/v1/manualimport", query, &resources); err != nil {
		return domain.LidarrImportPlan{}, err
	}
	updates := make([]manualUpdate, 0, len(resources))
	trackIDs := make([]int, 0)
	albumID, albumReleaseID := 0, 0
	for _, resource := range resources {
		if resource.AdditionalFile || strings.ToLower(filepath.Ext(resource.Path)) != ".flac" {
			continue
		}
		if len(resource.Rejections) > 0 {
			return domain.LidarrImportPlan{}, fmt.Errorf("Lidarr rejected manual item %s: %s", resource.Path, resource.Rejections[0].Reason)
		}
		if albumID == 0 {
			albumID, albumReleaseID = resource.Album.ID, resource.AlbumReleaseID
		}
		if resource.Album.ID != albumID || resource.AlbumReleaseID != albumReleaseID {
			return domain.LidarrImportPlan{}, fmt.Errorf("Lidarr manual import resolved mixed releases")
		}
		ids := make([]int, 0, len(resource.Tracks))
		for _, track := range resource.Tracks {
			ids = append(ids, track.ID)
			trackIDs = append(trackIDs, track.ID)
		}
		updates = append(updates, manualUpdate{ID: resource.ID, Path: resource.Path, Name: resource.Name, ArtistID: resource.Artist.ID,
			AlbumID: resource.Album.ID, AlbumReleaseID: resource.AlbumReleaseID, TrackIDs: ids, Quality: resource.Quality,
			ReleaseGroup: resource.ReleaseGroup, IndexerFlags: resource.IndexerFlags, DownloadID: resource.DownloadID,
			ReplaceExistingFiles: true, DisableReleaseSwitching: true})
	}
	if len(updates) != expectedFLAC || albumID == 0 || albumReleaseID == 0 {
		return domain.LidarrImportPlan{}, fmt.Errorf("Lidarr manual import track count mismatch: items=%d expected=%d", len(updates), expectedFLAC)
	}
	var album albumResource
	if err := m.Client.Get(ctx, fmt.Sprintf("/api/v1/album/%d", albumID), nil, &album); err != nil {
		return domain.LidarrImportPlan{}, err
	}
	matchedRelease := false
	for _, release := range album.Releases {
		if release.ID == albumReleaseID && release.ForeignReleaseID == releaseMBID {
			matchedRelease = true
			break
		}
	}
	if !matchedRelease {
		return domain.LidarrImportPlan{}, fmt.Errorf("Lidarr internal release does not match target MusicBrainz release")
	}
	slices.Sort(trackIDs)
	body, err := json.Marshal(updates)
	if err != nil {
		return domain.LidarrImportPlan{}, err
	}
	return domain.LidarrImportPlan{RequestBody: body, AlbumID: albumID, AlbumReleaseID: albumReleaseID, TrackIDs: trackIDs}, nil
}

func (m ManualImporter) Submit(ctx context.Context, plan domain.LidarrImportPlan) error {
	command, err := json.Marshal(struct {
		Name                 string          `json:"name"`
		Files                json.RawMessage `json:"files"`
		ImportMode           string          `json:"importMode"`
		ReplaceExistingFiles bool            `json:"replaceExistingFiles"`
	}{Name: "ManualImport", Files: plan.RequestBody, ImportMode: "move", ReplaceExistingFiles: true})
	if err != nil {
		return err
	}
	var accepted map[string]any
	return m.Client.Post(ctx, "/api/v1/command", command, &accepted)
}
