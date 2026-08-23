package lidarr

import (
	"context"
	"fmt"
	"strconv"
)

type HistoryRecord struct {
	ID         int64          `json:"id"`
	AlbumID    int64          `json:"albumId"`
	DownloadID string         `json:"downloadId"`
	EventType  string         `json:"eventType"`
	Date       string         `json:"date"`
	Data       map[string]any `json:"data"`
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

func (record HistoryRecord) DataString(key string) string {
	value, _ := record.Data[key].(string)
	return value
}

func (c Client) HistoryAfter(ctx context.Context, watermark string, pageSize int) ([]HistoryRecord, error) {
	minimum, err := strconv.ParseInt(watermark, 10, 64)
	if err != nil || minimum < 0 || pageSize <= 0 {
		return nil, fmt.Errorf("invalid history watermark or page size")
	}
	var result []HistoryRecord
	for page := 1; ; page++ {
		records, total, err := c.HistoryPage(ctx, page, pageSize)
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
