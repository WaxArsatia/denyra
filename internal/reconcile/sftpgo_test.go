package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type sftpgoFixture struct {
	t       *testing.T
	server  *httptest.Server
	user    map[string]any
	creates int
}

func newSFTPGoFixture(t *testing.T) *sftpgoFixture {
	t.Helper()
	f := &sftpgoFixture{t: t}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func (f *sftpgoFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/token":
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "sftpgo-admin-secret" {
			http.Error(w, "bad basic auth", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/users/upload":
		if !f.requireToken(w, r) {
			return
		}
		if f.user == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(f.user)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/users":
		if !f.requireToken(w, r) {
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			f.t.Fatal(err)
		}
		f.user = payload
		f.creates++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(payload)
	default:
		http.NotFound(w, r)
	}
}

func (f *sftpgoFixture) requireToken(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, "bad bearer", http.StatusUnauthorized)
		return false
	}
	return true
}

func TestSFTPGoCreatesRestrictedUploadUserAndThenAdoptsIt(t *testing.T) {
	f := newSFTPGoFixture(t)
	sftpgo := SFTPGo{BaseURL: f.server.URL, AdminPassword: "sftpgo-admin-secret", UploadPassword: "upload-secret", HTTP: f.server.Client()}
	outcome, err := sftpgo.Apply(context.Background())
	if err != nil || !outcome.Changed || f.creates != 1 {
		t.Fatalf("outcome=%+v creates=%d err=%v", outcome, f.creates, err)
	}
	if number(f.user["status"]) != 1 || f.user["username"] != "upload" || f.user["password"] != "upload-secret" || f.user["home_dir"] != "/data/incoming/manual" {
		t.Fatalf("user=%v", f.user)
	}
	permissions, ok := f.user["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions=%T %v", f.user["permissions"], f.user["permissions"])
	}
	rootPermissions, ok := permissions["/"].([]any)
	if !ok || len(rootPermissions) != 1 || rootPermissions[0] != "*" {
		t.Fatalf("permissions=%v", permissions)
	}
	filesystem, ok := f.user["filesystem"].(map[string]any)
	if !ok || number(filesystem["provider"]) != 0 {
		t.Fatalf("filesystem=%v", f.user["filesystem"])
	}

	outcome, err = sftpgo.Apply(context.Background())
	if err != nil || outcome.Changed || f.creates != 1 {
		t.Fatalf("rerun outcome=%+v creates=%d err=%v", outcome, f.creates, err)
	}
}

func TestSFTPGoRejectsExistingUserWithExpandedHome(t *testing.T) {
	f := newSFTPGoFixture(t)
	f.user = map[string]any{"username": "upload", "home_dir": "/data"}
	sftpgo := SFTPGo{BaseURL: f.server.URL, AdminPassword: "sftpgo-admin-secret", UploadPassword: "upload-secret", HTTP: f.server.Client()}
	_, err := sftpgo.Apply(context.Background())
	if err == nil {
		t.Fatal("expanded upload home accepted")
	}
}
