package lidarr

import (
	"context"
	"fmt"
)

type QueueRecord struct {
	ID         int64  `json:"id"`
	AlbumID    int64  `json:"albumId"`
	DownloadID string `json:"downloadId"`
	Title      string `json:"title"`
	Added      string `json:"added"`
}

func (c Client) QueuePage(ctx context.Context, page, size int) ([]QueueRecord, int, error) {
	query := pageQuery(page, size)
	query.Set("sortKey", "id")
	var payload struct {
		Records      []QueueRecord `json:"records"`
		TotalRecords int           `json:"totalRecords"`
	}
	err := c.Get(ctx, "/api/v1/queue", query, &payload)
	return payload.Records, payload.TotalRecords, err
}
func (c Client) QueueWatermark(ctx context.Context) (string, error) {
	records, _, err := c.QueuePage(ctx, 1, 1)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "0", nil
	}
	return fmt.Sprint(records[0].ID), nil
}
