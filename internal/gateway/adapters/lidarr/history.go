package lidarr

import (
	"context"
	"fmt"
)

type HistoryRecord struct {
	ID         int64             `json:"id"`
	AlbumID    int64             `json:"albumId"`
	DownloadID string            `json:"downloadId"`
	EventType  string            `json:"eventType"`
	Date       string            `json:"date"`
	Data       map[string]string `json:"data"`
}

func (c Client) HistoryPage(ctx context.Context, page, size int) ([]HistoryRecord, int, error) {
	query := pageQuery(page, size)
	query.Set("sortKey", "id")
	var payload struct {
		Records      []HistoryRecord `json:"records"`
		TotalRecords int             `json:"totalRecords"`
	}
	err := c.Get(ctx, "/api/v1/history", query, &payload)
	return payload.Records, payload.TotalRecords, err
}
func (c Client) HistoryWatermark(ctx context.Context) (string, error) {
	records, _, err := c.HistoryPage(ctx, 1, 1)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "0", nil
	}
	return fmt.Sprint(records[0].ID), nil
}
