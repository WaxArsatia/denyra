package gateway_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waxarsatia/denyra/internal/gateway/transport"
)

func TestSlskdFileCompletionEventPersistsBeforeSchedulingReconciliation(t *testing.T) {
	db, store, _ := gatewayRepositories(t)
	defer db.Close()
	notifications := 0
	handler, err := (transport.SlskdEventRoutes{Store: store, BodyLimit: 1 << 20, Notify: func() { notifications++ }}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{
  "type":"DownloadFileComplete",
  "version":0,
  "localFilename":"/data/downloads/slskd/lidarr/download-1/Artist - Album/01 - Track.flac",
  "remoteFilename":"Artist\\Album\\01 - Track.flac",
  "transfer":{
    "batchId":"11111111-1111-1111-1111-111111111111",
    "id":"22222222-2222-2222-2222-222222222222",
    "username":"peer",
    "direction":"Download",
    "filename":"Artist\\Album\\01 - Track.flac",
    "size":1024,
    "state":"Completed, Succeeded",
    "requestedAt":"2026-08-24T10:00:00Z",
    "enqueuedAt":"2026-08-24T10:00:01Z",
    "startedAt":"2026-08-24T10:00:02Z",
    "endedAt":"2026-08-24T10:00:03Z",
    "bytesTransferred":1024,
    "averageSpeed":1024,
    "placeInQueue":null,
    "exception":null,
    "attempts":1,
    "nextAttemptAt":null,
    "removed":false,
    "bytesRemaining":0,
    "elapsedTime":"00:00:01",
    "percentComplete":100,
    "remainingTime":null,
    "startOffset":0
  },
  "id":"33333333-3333-3333-3333-333333333333",
  "timestamp":"2026-08-24T10:00:03Z"
}`)

	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/events/slskd", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("attempt %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM slskd_completion_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 || notifications != 2 {
		t.Fatalf("events=%d notifications=%d", events, notifications)
	}
}

func TestSlskdEventEndpointRejectsNonCompletionEvent(t *testing.T) {
	db, store, _ := gatewayRepositories(t)
	defer db.Close()
	handler, err := (transport.SlskdEventRoutes{Store: store, BodyLimit: 1024, Notify: func() {}}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/events/slskd", bytes.NewBufferString(`{"type":"SoulseekClientConnected","version":0,"id":"33333333-3333-3333-3333-333333333333","timestamp":"2026-08-24T10:00:03Z"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
