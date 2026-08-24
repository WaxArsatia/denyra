package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
)

func TestLiveCompatibility(t *testing.T) {
	if os.Getenv("DENYRA_LIVE_COMPATIBILITY") != "1" {
		t.Skip("set DENYRA_LIVE_COMPATIBILITY=1 with read-only smoke inputs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 20 * time.Second}
	releaseMBID := requiredLiveValue(t, "DENYRA_LIVE_MUSICBRAINZ_RELEASE_MBID")
	musicBrainzURL := liveValue("DENYRA_LIVE_MUSICBRAINZ_URL", "https://musicbrainz.org")
	mb := &musicbrainz.Client{BaseURL: musicBrainzURL, UserAgent: "Denyra/live-compatibility (https://github.com/WaxArsatia/denyra)", HTTP: httpClient, ResponseLimit: 4 << 20, RateInterval: time.Second}
	release, evidence, err := mb.LookupRelease(ctx, releaseMBID)
	if err != nil || evidence.StatusCode != http.StatusOK || len(release.Tracks) == 0 || len(release.ArtistCredits) == 0 {
		t.Fatalf("MusicBrainz lookup release=%s tracks=%d status=%d err=%v", release.ReleaseMBID, len(release.Tracks), evidence.StatusCode, err)
	}
	search, err := mb.SearchReleases(ctx, musicbrainz.SearchInput{AlbumArtist: release.ArtistCredits[0].Name, Album: release.Title, Date: release.Date, TrackCount: len(release.Tracks)})
	if err != nil || len(search.Evidence) == 0 {
		t.Fatalf("MusicBrainz search releases=%d evidence=%d err=%v", len(search.Releases), len(search.Evidence), err)
	}

	navidromeURL := strings.TrimRight(liveValue("DENYRA_LIVE_NAVIDROME_URL", "http://localhost:4533"), "/")
	navidromeUser := liveValue("DENYRA_LIVE_NAVIDROME_USERNAME", "admin")
	navidromePassword := readLiveSecret(t, "DENYRA_LIVE_NAVIDROME_PASSWORD_FILE")
	loginBody, _ := json.Marshal(map[string]string{"username": navidromeUser, "password": navidromePassword})
	login := liveJSONRequest(t, ctx, httpClient, http.MethodPost, navidromeURL+"/auth/login", "", loginBody)
	var auth struct {
		Token, SubsonicToken, SubsonicSalt string
	}
	decodeLiveJSON(t, login, &auth)
	if auth.Token == "" || auth.SubsonicToken == "" || auth.SubsonicSalt == "" {
		t.Fatal("Navidrome login omitted required tokens")
	}
	libraries := liveJSONRequest(t, ctx, httpClient, http.MethodGet, navidromeURL+"/api/library/", auth.Token, nil)
	var libraryRows []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	decodeLiveJSON(t, libraries, &libraryRows)
	if len(libraryRows) == 0 {
		t.Fatal("Navidrome returned no libraries")
	}
	query := url.Values{"u": {navidromeUser}, "t": {auth.SubsonicToken}, "s": {auth.SubsonicSalt}, "v": {"1.16.1"}, "c": {"denyra-live-compatibility"}, "f": {"json"}}
	scan := liveJSONRequest(t, ctx, httpClient, http.MethodGet, navidromeURL+"/rest/getScanStatus.view?"+query.Encode(), "", nil)
	var scanEnvelope map[string]json.RawMessage
	decodeLiveJSON(t, scan, &scanEnvelope)
	if len(scanEnvelope["subsonic-response"]) == 0 {
		t.Fatal("Navidrome scan status omitted OpenSubsonic envelope")
	}

	lidarrURL := strings.TrimRight(requiredLiveValue(t, "DENYRA_LIVE_LIDARR_URL"), "/")
	lidarrKey := readLiveSecret(t, "DENYRA_LIVE_LIDARR_API_KEY_FILE")
	for _, endpoint := range []string{
		"/api/v1/rootfolder",
		"/api/v1/qualityprofile",
		"/api/v1/metadataprofile",
		"/api/v1/artist/lookup?" + url.Values{"term": {"lidarr:" + release.ArtistCredits[0].ArtistMBID}}.Encode(),
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, lidarrURL+endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Api-Key", lidarrKey)
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatalf("Lidarr GET %s: %v", endpoint, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || len(body) == 0 || len(body) > 4<<20 {
			t.Fatalf("Lidarr GET %s status=%d bytes=%d read=%v", endpoint, response.StatusCode, len(body), readErr)
		}
	}
}

func liveJSONRequest(t *testing.T, ctx context.Context, client *http.Client, method, endpoint, bearer string, body []byte) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 || len(content) > 4<<20 {
		t.Fatalf("%s %s status=%d bytes=%d err=%v", method, endpoint, response.StatusCode, len(content), err)
	}
	return content
}

func decodeLiveJSON(t *testing.T, body []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func requiredLiveValue(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when DENYRA_LIVE_COMPATIBILITY=1", name)
	}
	return value
}

func liveValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func readLiveSecret(t *testing.T, name string) string {
	t.Helper()
	path := requiredLiveValue(t, name)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	value := strings.TrimSpace(string(content))
	if value == "" || len(value) > 64<<10 {
		t.Fatalf("%s is empty or too large", name)
	}
	return value
}
