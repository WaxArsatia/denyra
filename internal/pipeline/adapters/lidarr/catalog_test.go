package lidarr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestCatalogExistingExactReleaseAvoidsPOSTAndMonitorsOnlyTargetAlbum(t *testing.T) {
	t.Parallel()
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.String())
		switch r.URL.Path {
		case "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/data/library","defaultQualityProfileId":4,"defaultMetadataProfileId":5}]`))
		case "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":4,"name":"Lossless"}]`))
		case "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":5,"name":"Standard"}]`))
		case "/api/v1/artist/lookup":
			_, _ = w.Write([]byte(`[{"foreignArtistId":"11111111-1111-1111-1111-111111111111","artistName":"Kaleb J"}]`))
		case "/api/v1/artist":
			_, _ = w.Write([]byte(`[{"id":7,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}]`))
		case "/api/v1/album":
			_, _ = w.Write([]byte(`[{"id":8,"artistId":7,"title":"OFF GUARD","monitored":false,"releases":[{"id":9,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}]`))
		case "/api/v1/album/8":
			if r.Method == http.MethodPut {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `"monitored":true`) {
					t.Errorf("target album not monitored: %s", body)
				}
			}
			_, _ = w.Write([]byte(`{"id":8,"artistId":7,"title":"OFF GUARD","monitored":true,"releases":[{"id":9,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	result, err := (lidarr.Catalog{Client: lidarr.Client{BaseURL: server.URL, APIKey: "key", HTTP: server.Client()}, PollAttempts: 2, PollInterval: time.Nanosecond}).EnsureRelease(context.Background(), catalogRelease())
	if err != nil || result.ArtistID != 7 || result.AlbumID != 8 || result.AlbumReleaseID != 9 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, request := range requests {
		if strings.HasPrefix(request, http.MethodPost+" ") {
			t.Fatalf("existing exact release issued POST: %v", requests)
		}
	}
}

func TestCatalogAddsAbsentArtistWithoutSearchAndUsesReturnedIDs(t *testing.T) {
	t.Parallel()
	artistAdded, refreshed := false, false
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/data/library","defaultQualityProfileId":4,"defaultMetadataProfileId":5}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":4}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":5}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/artist/lookup":
			if r.URL.Query().Get("term") != "lidarr:11111111-1111-1111-1111-111111111111" {
				t.Errorf("lookup=%q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"foreignArtistId":"11111111-1111-1111-1111-111111111111","artistName":"Kaleb J","qualityProfileId":14,"metadataProfileId":15}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/artist":
			if artistAdded {
				_, _ = w.Write([]byte(`[{"id":70,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/artist":
			artistAdded = true
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			options, _ := payload["addOptions"].(map[string]any)
			if payload["rootFolderPath"] != "/data/library" || payload["qualityProfileId"] != float64(14) || payload["metadataProfileId"] != float64(15) || payload["monitored"] != false || options["searchForMissingAlbums"] != false {
				t.Errorf("artist payload=%v", payload)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":70,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/album":
			if !refreshed {
				_, _ = w.Write([]byte(`[]`))
			} else {
				_, _ = w.Write([]byte(`[{"id":80,"artistId":70,"title":"OFF GUARD","monitored":false,"releases":[{"id":90,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}]`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
			refreshed = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"name":"RefreshArtist","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/command/42":
			_, _ = w.Write([]byte(`{"id":42,"status":"completed"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/album/80":
			_, _ = w.Write([]byte(`{"id":80,"artistId":70,"monitored":true,"releases":[{"id":90,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/album/80":
			_, _ = w.Write([]byte(`{"id":80,"artistId":70,"monitored":true,"releases":[{"id":90,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}`))
		default:
			http.Error(w, fmt.Sprintf("unexpected %s %s", r.Method, r.URL.String()), http.StatusNotFound)
		}
	}))
	defer server.Close()
	result, err := (lidarr.Catalog{Client: lidarr.Client{BaseURL: server.URL, APIKey: "key", HTTP: server.Client()}, PollAttempts: 2, PollInterval: time.Nanosecond}).EnsureRelease(context.Background(), catalogRelease())
	if err != nil || result.ArtistID != 70 || result.AlbumID != 80 || result.AlbumReleaseID != 90 || !artistAdded || !refreshed {
		t.Fatalf("result=%+v artistAdded=%v refreshed=%v err=%v", result, artistAdded, refreshed, err)
	}
	joined := strings.Join(bodies, "\n")
	for _, forbidden := range []string{"ArtistSearch", "AlbumSearch", `"searchForMissingAlbums":true`, "/data/library-unmanaged"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden catalog behavior %q in bodies:\n%s", forbidden, joined)
		}
	}
}

func catalogRelease() domain.CanonicalRelease {
	return domain.CanonicalRelease{ReleaseMBID: "22222222-2222-2222-2222-222222222222", ReleaseGroupMBID: "33333333-3333-3333-3333-333333333333", Title: "OFF GUARD", ArtistCredits: []domain.ArtistCredit{{Name: "Kaleb J", ArtistMBID: "11111111-1111-1111-1111-111111111111"}}, Tracks: []domain.CanonicalTrack{{ReleaseTrackMBID: "44444444-4444-4444-4444-444444444444", RecordingMBID: "55555555-5555-5555-5555-555555555555", Disc: 1, Track: 1}}}
}
