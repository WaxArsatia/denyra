package lidarr

import (
	"context"
	"fmt"
	"strconv"
)

type QueueRecord struct {
	ID                  int64  `json:"id"`
	AlbumID             int64  `json:"albumId"`
	DownloadID          string `json:"downloadId"`
	Title               string `json:"title"`
	Added               string `json:"added"`
	ReleaseGroupMBID    string `json:"releaseGroupId"`
	SelectedReleaseMBID string `json:"releaseId"`
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

func (c Client) QueueAfter(ctx context.Context, watermark string, pageSize int) ([]QueueRecord, error) {
	minimum, err := strconv.ParseInt(watermark, 10, 64)
	if err != nil || minimum < 0 || pageSize <= 0 {
		return nil, fmt.Errorf("invalid queue watermark or page size")
	}
	var result []QueueRecord
	for page := 1; ; page++ {
		records, total, err := c.QueuePage(ctx, page, pageSize)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.ID > minimum {
				result = append(result, record)
			}
		}
		if len(records) == 0 || page*pageSize >= total || records[len(records)-1].ID <= minimum {
			return result, nil
		}
	}
}
