package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFixtureServesSFTPGoToken(t *testing.T) {
	server := httptest.NewServer(newFixture("").routes())
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v2/token")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.AccessToken != "acceptance-token" || response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d payload=%+v err=%v", response.StatusCode, payload, err)
	}
}

func TestFixtureServesDeterministicMusicBrainzOutcomes(t *testing.T) {
	server := httptest.NewServer(newFixture("").routes())
	defer server.Close()
	tests := []struct {
		album      string
		wantStatus int
		wantCount  int
	}{
		{album: "OFF GUARD", wantStatus: http.StatusOK, wantCount: 1},
		{album: "Ambiguous", wantStatus: http.StatusOK, wantCount: 2},
		{album: "No Match", wantStatus: http.StatusOK, wantCount: 0},
		{album: "Provider Error", wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.album, func(t *testing.T) {
			query := url.Values{"fmt": {"json"}, "query": {"artist:Kaleb J AND release:" + test.album + " AND tracks:1"}}
			response, err := server.Client().Get(server.URL + "/ws/2/release?" + query.Encode())
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status=%d", response.StatusCode)
			}
			if test.wantStatus != http.StatusOK {
				return
			}
			var payload struct {
				Releases []map[string]any `json:"releases"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || len(payload.Releases) != test.wantCount {
				t.Fatalf("releases=%v err=%v", payload.Releases, err)
			}
		})
	}
	response, err := server.Client().Get(server.URL + "/ws/2/release/22222222-2222-2222-2222-222222222222?fmt=json")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	for _, fragment := range []string{`"release-group"`, `"artist-credit"`, `"media"`, `"recording"`} {
		if !bytes.Contains(body, []byte(fragment)) {
			t.Errorf("MusicBrainz lookup missing %s: %s", fragment, body)
		}
	}
}

func TestFixtureServesCatalogNavidromeAndLostAcknowledgementEvidence(t *testing.T) {
	server := httptest.NewServer(newFixture("").routes())
	defer server.Close()
	for _, path := range []string{"/api/v1/rootfolder", "/api/v1/qualityprofile", "/api/v1/metadataprofile", "/api/v1/artist/lookup?term=lidarr:11111111-1111-1111-1111-111111111111", "/api/v1/artist", "/api/v1/album?artistId=70"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || len(bytes.TrimSpace(body)) == 0 {
			t.Fatalf("GET %s status=%d body=%s", path, response.StatusCode, body)
		}
	}
	login, err := server.Client().Post(server.URL+"/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if login.StatusCode != http.StatusOK {
		t.Fatalf("Navidrome login status=%d", login.StatusCode)
	}
	_ = login.Body.Close()
	for _, path := range []string{"/api/library/", "/rest/getMusicFolders.view?u=admin&t=token&s=salt&v=1.16.1&c=denyra&f=json", "/rest/getScanStatus.view?u=admin&t=token&s=salt&v=1.16.1&c=denyra&f=json"} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		request.Header.Set("Authorization", "Bearer fixture-token")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}
	command := `{"name":"ManualImport","files":[{"path":"/data/processing/approved/release/01.flac"}]}`
	for attempt := 0; attempt < 2; attempt++ {
		response, err := server.Client().Post(server.URL+"/api/v1/command?fault=lost-ack", "application/json", strings.NewReader(command))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if attempt == 0 && response.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("lost ACK status=%d", response.StatusCode)
		}
		if attempt == 1 && response.StatusCode != http.StatusOK {
			t.Fatalf("reconcile status=%d", response.StatusCode)
		}
	}
	evidenceResponse, err := server.Client().Get(server.URL + "/acceptance/evidence")
	if err != nil {
		t.Fatal(err)
	}
	defer evidenceResponse.Body.Close()
	var evidence fixtureState
	if err := json.NewDecoder(evidenceResponse.Body).Decode(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.ManualImportCommands != 1 || evidence.LidarrSearchCommands != 0 || evidence.RequestsByRoute["POST /api/v1/command"] != 2 {
		t.Fatalf("evidence=%+v", evidence)
	}
}
