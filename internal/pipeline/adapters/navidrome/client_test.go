package navidrome

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestClientReconcilesLibrariesAndUsesTokenAuthentication(t *testing.T) {
	t.Parallel()

	libraries := []Library{
		{ID: 9, Name: "Podcasts", Path: "/podcasts", DefaultNewUsers: false},
		{ID: 1, Name: "Music", Path: "/music", DefaultNewUsers: true},
	}
	mutations := 0
	requests := make([]string, 0, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/auth/login":
			var credentials map[string]string
			if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
				t.Fatal(err)
			}
			if credentials["username"] != "admin" || credentials["password"] != "navidrome-secret" {
				t.Fatalf("credentials=%v", credentials)
			}
			writeJSON(t, w, map[string]any{"token": "fixture-token", "subsonicToken": "sub-token", "subsonicSalt": "sub-salt"})
		case "/api/library/":
			switch r.Method {
			case http.MethodGet:
				requireBearer(t, r, "fixture-token", "refreshed-token")
				w.Header().Set("X-ND-Authorization", "Bearer refreshed-token")
				writeJSON(t, w, libraries)
			case http.MethodPost:
				requireBearer(t, r, "refreshed-token")
				mutations++
				var got Library
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Name != "Unmanaged" || got.Path != "/music-unmanaged" || got.DefaultNewUsers {
					t.Fatalf("unmanaged create=%+v", got)
				}
				got.ID = 2
				libraries = append(libraries, got)
				writeJSON(t, w, got)
			default:
				http.Error(w, "method", http.StatusMethodNotAllowed)
			}
		case "/api/library/1":
			requireBearer(t, r, "refreshed-token")
			mutations++
			var got Library
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.ID != 1 || got.Name != "Managed" || got.Path != "/music-managed" || !got.DefaultNewUsers {
				t.Fatalf("managed update=%+v", got)
			}
			libraries[0] = got
			writeJSON(t, w, got)
		case "/rest/getMusicFolders.view":
			requireSubsonicToken(t, r.URL.Query())
			writeJSON(t, w, map[string]any{"subsonic-response": map[string]any{
				"status": "ok", "version": "1.16.1",
				"musicFolders": map[string]any{"musicFolder": []map[string]any{{"id": 1, "name": "Managed"}, {"id": 2, "name": "Unmanaged"}}},
			}})
		case "/rest/startScan.view":
			requireSubsonicToken(t, r.URL.Query())
			if got := r.URL.Query()["target"]; !slices.Equal(got, []string{"2:"}) {
				t.Fatalf("scan targets=%v", got)
			}
			writeJSON(t, w, map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1"}})
		case "/rest/getScanStatus.view":
			requireSubsonicToken(t, r.URL.Query())
			writeJSON(t, w, map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1", "scanStatus": map[string]any{"scanning": false}}})
		case "/rest/search3.view":
			requireSubsonicToken(t, r.URL.Query())
			if r.URL.Query().Get("musicFolderId") != "2" || r.URL.Query().Get("query") != "OFF GUARD" {
				t.Fatalf("search query=%s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1", "searchResult3": map[string]any{
				"album": []map[string]any{{"name": "OFF GUARD", "artist": "Kaleb J", "songCount": 5}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, Username: "admin", Password: "navidrome-secret", HTTP: server.Client(), ResponseLimit: 1 << 20}
	managedID, unmanagedID, changed, err := client.EnsureLibraries(context.Background())
	if err != nil || managedID != 1 || unmanagedID != 2 || !changed {
		t.Fatalf("first reconcile managed=%d unmanaged=%d changed=%v err=%v", managedID, unmanagedID, changed, err)
	}
	managedID, unmanagedID, changed, err = client.EnsureLibraries(context.Background())
	if err != nil || managedID != 1 || unmanagedID != 2 || changed {
		t.Fatalf("second reconcile managed=%d unmanaged=%d changed=%v err=%v", managedID, unmanagedID, changed, err)
	}
	if mutations != 2 {
		t.Fatalf("library mutations=%d, want 2", mutations)
	}
	if err := client.StartScan(context.Background(), unmanagedID); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitScan(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	visible, err := client.ReleaseVisible(context.Background(), unmanagedID, ReleaseIdentity{AlbumArtist: "Kaleb J", Album: "OFF GUARD", TrackCount: 5})
	if err != nil || !visible {
		t.Fatalf("visible=%v err=%v", visible, err)
	}
	if requests[0] != "POST /auth/login" {
		t.Fatalf("first request=%q", requests[0])
	}
}

func requireBearer(t *testing.T, r *http.Request, accepted ...string) {
	t.Helper()
	got := r.Header.Get("X-ND-Authorization")
	for _, token := range accepted {
		if got == "Bearer "+token {
			return
		}
	}
	t.Fatalf("X-ND-Authorization=%q, accepted=%v", got, accepted)
}

func requireSubsonicToken(t *testing.T, query url.Values) {
	t.Helper()
	want := map[string]string{"u": "admin", "t": "sub-token", "s": "sub-salt", "v": "1.16.1", "c": "denyra", "f": "json"}
	for name, value := range want {
		if query.Get(name) != value {
			t.Fatalf("query %s=%q, want %q (%s)", name, query.Get(name), value, query.Encode())
		}
	}
	if _, exists := query["p"]; exists {
		t.Fatalf("plaintext password parameter present: %s", query.Encode())
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(fmt.Errorf("encode response: %w", err))
	}
}
