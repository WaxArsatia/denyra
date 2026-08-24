package lidarr_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
)

func TestQueueRecordsReadsEveryLidarrPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "test-key" || request.URL.Path != "/api/v1/queue" || request.URL.Query().Get("pageSize") != "2" {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Query().Get("page") {
		case "1":
			_, _ = fmt.Fprint(writer, `{"records":[{"id":3,"downloadId":"three"},{"id":2,"downloadId":"two"}],"totalRecords":3}`)
		case "2":
			_, _ = fmt.Fprint(writer, `{"records":[{"id":1,"downloadId":"one"}],"totalRecords":3}`)
		default:
			http.Error(writer, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20}
	records, err := client.QueueRecords(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].DownloadID != "three" || records[2].DownloadID != "one" {
		t.Fatalf("records=%+v", records)
	}
}
