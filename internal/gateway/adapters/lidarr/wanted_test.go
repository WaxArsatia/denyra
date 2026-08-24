package lidarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWantedUsesTheSingleMonitoredReleaseWhenTopLevelReleaseIDIsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"records":[{
				"id":5,
				"foreignAlbumId":"31ad1f52-8a04-4ac2-a992-3941e64093a0",
				"monitored":true,
				"releases":[{
					"foreignReleaseId":"b7799052-6044-46f3-948c-5e47af201144",
					"monitored":true
				}]
			}],
			"totalRecords":1
		}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20}
	albums, err := client.Wanted(context.Background())
	if err != nil {
		t.Fatalf("wanted: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("albums=%d, want 1", len(albums))
	}
	if got := albums[0].SelectedReleaseMBID; got != "b7799052-6044-46f3-948c-5e47af201144" {
		t.Fatalf("selected release=%q", got)
	}
}
