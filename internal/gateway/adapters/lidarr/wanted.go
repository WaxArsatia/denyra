package lidarr

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type WantedAlbum struct {
	AlbumID                               int64
	ReleaseGroupMBID, SelectedReleaseMBID string
	Monitored                             bool
}

type releaseSelectionRecord struct {
	ForeignReleaseID string `json:"foreignReleaseId"`
	Monitored        bool   `json:"monitored"`
}

func (c Client) Wanted(ctx context.Context) ([]WantedAlbum, error) {
	type wantedPage struct {
		Records []struct {
			ID             int64                    `json:"id"`
			ForeignAlbumID string                   `json:"foreignAlbumId"`
			ReleaseID      string                   `json:"releaseId"`
			Monitored      bool                     `json:"monitored"`
			Releases       []releaseSelectionRecord `json:"releases"`
		} `json:"records"`
		TotalRecords int `json:"totalRecords"`
	}
	result := make([]WantedAlbum, 0)
	received := 0
	for page := 1; ; page++ {
		query := pageQuery(page, 1000)
		query.Set("sortKey", "id")
		query.Set("monitored", "true")
		var payload wantedPage
		if err := c.Get(ctx, "/api/v1/wanted/missing", query, &payload); err != nil {
			return nil, err
		}
		received += len(payload.Records)
		for _, record := range payload.Records {
			if record.Monitored {
				result = append(result, WantedAlbum{AlbumID: record.ID, ReleaseGroupMBID: record.ForeignAlbumID, SelectedReleaseMBID: selectedReleaseMBID(record.ReleaseID, record.Releases), Monitored: true})
			}
		}
		if len(payload.Records) == 0 || received >= payload.TotalRecords {
			break
		}
	}
	return result, nil
}
func (c Client) Album(ctx context.Context, id int64) (WantedAlbum, error) {
	var record struct {
		ID             int64                    `json:"id"`
		ForeignAlbumID string                   `json:"foreignAlbumId"`
		ReleaseID      string                   `json:"releaseId"`
		Monitored      bool                     `json:"monitored"`
		Releases       []releaseSelectionRecord `json:"releases"`
	}
	if err := c.Get(ctx, "/api/v1/album/"+strconv.FormatInt(id, 10), url.Values{}, &record); err != nil {
		return WantedAlbum{}, err
	}
	return WantedAlbum{AlbumID: record.ID, ReleaseGroupMBID: record.ForeignAlbumID, SelectedReleaseMBID: selectedReleaseMBID(record.ReleaseID, record.Releases), Monitored: record.Monitored}, nil
}

func selectedReleaseMBID(explicit string, releases []releaseSelectionRecord) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	selected := ""
	for _, release := range releases {
		id := strings.TrimSpace(release.ForeignReleaseID)
		if !release.Monitored || id == "" {
			continue
		}
		if selected != "" && selected != id {
			return ""
		}
		selected = id
	}
	return selected
}
