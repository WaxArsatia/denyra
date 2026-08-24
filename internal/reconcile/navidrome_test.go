package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNavidromeCreatesFirstAdministrator(t *testing.T) {
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/createAdmin":
			if r.Method != http.MethodPost {
				http.Error(w, "method", http.StatusMethodNotAllowed)
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		case "/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fixture-token", "subsonicToken": "sub-token", "subsonicSalt": "sub-salt"})
		case "/api/library/":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "name": "Managed", "path": "/music-managed", "defaultNewUsers": true},
				{"id": 2, "name": "Unmanaged", "path": "/music-unmanaged", "defaultNewUsers": false},
			})
		case "/rest/getMusicFolders.view":
			_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "ok", "musicFolders": map[string]any{"musicFolder": []map[string]any{{"id": 1}, {"id": 2}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	navidrome := Navidrome{BaseURL: server.URL, AdminPassword: "navidrome-secret", HTTP: server.Client()}
	outcome, err := navidrome.Apply(context.Background())
	if err != nil || !outcome.Changed || received["username"] != "admin" || received["password"] != "navidrome-secret" {
		t.Fatalf("outcome=%+v received=%v err=%v", outcome, received, err)
	}
}

func TestNavidromeAdoptsExistingAdministrator(t *testing.T) {
	libraryReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/createAdmin":
			http.Error(w, "a user already exists", http.StatusForbidden)
		case "/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fixture-token", "subsonicToken": "sub-token", "subsonicSalt": "sub-salt"})
		case "/api/library/":
			libraryReads++
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "name": "Managed", "path": "/music-managed", "defaultNewUsers": true},
				{"id": 2, "name": "Unmanaged", "path": "/music-unmanaged", "defaultNewUsers": false},
			})
		case "/rest/getMusicFolders.view":
			_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "ok", "musicFolders": map[string]any{"musicFolder": []map[string]any{{"id": 1}, {"id": 2}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	navidrome := Navidrome{BaseURL: server.URL, AdminPassword: "navidrome-secret", HTTP: server.Client()}
	outcome, err := navidrome.Apply(context.Background())
	if err != nil || outcome.Changed || libraryReads != 1 {
		t.Fatalf("outcome=%+v libraryReads=%d err=%v", outcome, libraryReads, err)
	}
}

func TestNavidromeFailureIsBoundedAndRedacted(t *testing.T) {
	secret := "navidrome-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 9000) + secret))
	}))
	defer server.Close()
	navidrome := Navidrome{BaseURL: server.URL, AdminPassword: secret, HTTP: server.Client()}
	_, err := navidrome.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), secret) || len(err.Error()) > 8500 {
		t.Fatalf("unsafe error length=%d err=%v", len(err.Error()), err)
	}
}
