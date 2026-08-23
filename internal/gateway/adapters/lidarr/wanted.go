package lidarr

import (
	"context"
	"net/url"
	"strconv"
)

type WantedAlbum struct {
	AlbumID                               int64
	ReleaseGroupMBID, SelectedReleaseMBID string
	Monitored                             bool
}

func (c Client) Wanted(ctx context.Context) ([]WantedAlbum, error) {
	query := pageQuery(1, 1000)
	query.Set("sortKey", "id")
	query.Set("monitored", "true")
	var payload struct {
		Records []struct {
			ID             int64  `json:"id"`
			ForeignAlbumID string `json:"foreignAlbumId"`
			ReleaseID      string `json:"releaseId"`
			Monitored      bool   `json:"monitored"`
		} `json:"records"`
		TotalRecords int `json:"totalRecords"`
	}
	if err := c.Get(ctx, "/api/v1/wanted/missing", query, &payload); err != nil {
		return nil, err
	}
	result := make([]WantedAlbum, 0, len(payload.Records))
	for _, record := range payload.Records {
		if record.Monitored {
			result = append(result, WantedAlbum{AlbumID: record.ID, ReleaseGroupMBID: record.ForeignAlbumID, SelectedReleaseMBID: record.ReleaseID, Monitored: true})
		}
	}
	return result, nil
}
func (c Client) Album(ctx context.Context, id int64) (WantedAlbum, error) {
	var record struct {
		ID             int64  `json:"id"`
		ForeignAlbumID string `json:"foreignAlbumId"`
		ReleaseID      string `json:"releaseId"`
		Monitored      bool   `json:"monitored"`
	}
	if err := c.Get(ctx, "/api/v1/album/"+strconv.FormatInt(id, 10), url.Values{}, &record); err != nil {
		return WantedAlbum{}, err
	}
	return WantedAlbum{AlbumID: record.ID, ReleaseGroupMBID: record.ForeignAlbumID, SelectedReleaseMBID: record.ReleaseID, Monitored: record.Monitored}, nil
}
