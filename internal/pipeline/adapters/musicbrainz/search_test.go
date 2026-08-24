package musicbrainz_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
)

func TestSearchReleasesUsesIdentifierThenMetadataOrderAndDeduplicates(t *testing.T) {
	t.Parallel()
	ids := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"44444444-4444-4444-4444-444444444444",
	}
	recordingID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/ws/2/release/"+ids[0]:
			_, _ = w.Write([]byte(fullReleaseJSON(ids[0])))
		case r.URL.Path == "/ws/2/release" && r.URL.Query().Get("query") == "barcode:123456789012":
			_, _ = fmt.Fprintf(w, `{"releases":[{"id":%q,"score":100}]}`, ids[1])
		case r.URL.Path == "/ws/2/recording" && r.URL.Query().Get("query") == "isrc:IDABC2600001":
			_, _ = fmt.Fprintf(w, `{"recordings":[{"id":%q,"score":100}]}`, recordingID)
		case r.URL.Path == "/ws/2/recording/"+recordingID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"releases":[{"id":%q},{"id":%q}]}`, recordingID, ids[1], ids[2])
		case r.URL.Path == "/ws/2/release" && strings.Contains(r.URL.Query().Get("query"), "artist:Kaleb J"):
			_, _ = fmt.Fprintf(w, `{"releases":[{"id":%q,"score":99},{"id":%q,"score":80}]}`, ids[2], ids[3])
		case strings.HasPrefix(r.URL.Path, "/ws/2/release/"):
			id := strings.TrimPrefix(r.URL.Path, "/ws/2/release/")
			_, _ = w.Write([]byte(fullReleaseJSON(id)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &musicbrainz.Client{BaseURL: server.URL, UserAgent: "Denyra/test (admin@example.invalid)", HTTP: server.Client(), RateInterval: time.Nanosecond, ResponseLimit: 1 << 20}
	result, err := client.SearchReleases(context.Background(), musicbrainz.SearchInput{
		TaggedReleaseMBIDs: []string{ids[0]}, Barcodes: []string{"123456789012"}, ISRCs: []string{"IDABC2600001"},
		AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2024", TrackCount: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Releases) != 4 || len(result.Evidence) != len(requests) {
		t.Fatalf("result releases=%d evidence=%d requests=%d", len(result.Releases), len(result.Evidence), len(requests))
	}
	wantPrefixes := []string{
		"/ws/2/release/" + ids[0] + "?",
		"/ws/2/release?fmt=json&limit=10&query=barcode%3A123456789012",
		"/ws/2/recording?fmt=json&limit=10&query=isrc%3AIDABC2600001",
		"/ws/2/recording/" + recordingID + "?fmt=json&inc=releases",
		"/ws/2/release?fmt=json&limit=10&query=artist%3AKaleb+J+AND+release%3AOFF+GUARD+AND+date%3A2024+AND+tracks%3A5",
	}
	for index, want := range wantPrefixes {
		if index >= len(requests) || !strings.HasPrefix(requests[index], want) {
			t.Fatalf("request[%d]=%q, want prefix %q; all=%v", index, requests[index], want, requests)
		}
	}
}

func TestSearchReleasesRetriesTransientRequestAndKeepsEvidence(t *testing.T) {
	t.Parallel()
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			http.Error(w, `{"error":"server busy"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"releases":[]}`))
	}))
	defer server.Close()

	client := &musicbrainz.Client{
		BaseURL: server.URL, UserAgent: "Denyra/test (admin@example.invalid)",
		HTTP: server.Client(), RateInterval: time.Nanosecond, ResponseLimit: 1 << 20,
	}
	result, err := client.SearchReleases(context.Background(), musicbrainz.SearchInput{Barcodes: []string{"3617385291670"}})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if len(result.Evidence) != 2 || result.Evidence[0].StatusCode != http.StatusServiceUnavailable || result.Evidence[1].StatusCode != http.StatusOK {
		t.Fatalf("evidence=%+v, want preserved 503 then 200", result.Evidence)
	}
}

func TestSearchReleasesRetriesTransientReleaseLookupAndKeepsEvidence(t *testing.T) {
	t.Parallel()
	releaseID := "11111111-1111-1111-1111-111111111111"
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			http.Error(w, `{"error":"server busy"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(fullReleaseJSON(releaseID)))
	}))
	defer server.Close()

	client := &musicbrainz.Client{
		BaseURL: server.URL, UserAgent: "Denyra/test (admin@example.invalid)",
		HTTP: server.Client(), RateInterval: time.Nanosecond, ResponseLimit: 1 << 20,
	}
	result, err := client.SearchReleases(context.Background(), musicbrainz.SearchInput{TaggedReleaseMBIDs: []string{releaseID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Releases) != 1 || result.Releases[0].ReleaseMBID != releaseID {
		t.Fatalf("releases=%+v, want %s", result.Releases, releaseID)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if len(result.Evidence) != 2 || result.Evidence[0].StatusCode != http.StatusServiceUnavailable || result.Evidence[1].StatusCode != http.StatusOK {
		t.Fatalf("evidence=%+v, want preserved 503 then 200", result.Evidence)
	}
}

func fullReleaseJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"title":"OFF GUARD","date":"2024","status":"Official","release-group":{"id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},"artist-credit":[{"name":"Kaleb J","artist":{"id":"cccccccc-cccc-cccc-cccc-cccccccccccc","name":"Kaleb J"}}],"media":[{"position":1,"track-count":1,"tracks":[{"id":"dddddddd-dddd-dddd-dddd-dddddddddddd","title":"Track","number":"1","position":1,"length":1000,"recording":{"id":"eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee","title":"Track","length":1000,"isrcs":["IDABC2600001"]}}]}]}`, id)
}
