package pipeline_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/handlers"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func TestAdminUIUsesAuthenticatedRealDataSecurityHeadersAndImmutableAssets(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='REVIEW_REQUIRED',state_revision=7,release_directory=?,updated_at=? WHERE candidate_id=?`, filepath.Join(t.TempDir(), candidate.ID), now.Format(time.RFC3339Nano), candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO validation_results(id,candidate_id,scope,subject,classification,code,evidence_json,evidence_sha256,created_at) VALUES('validation-ui',?,'TRACK','01.flac','MANUAL_REVIEW','DURATION','{"difference_ms":6000}','hash',?)`, candidate.ID, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	bundle, err := assets.New()
	if err != nil {
		t.Fatal(err)
	}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	quarantine := t.TempDir()
	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(quarantine, candidate.ID), 0o750); err != nil {
		t.Fatal(err)
	}
	handler, err := handlers.New(handlers.Dependencies{Auth: auth, Reader: repository, Assets: bundle, ConfigSnapshot: "config-hash", Reviews: application.ReviewDecisionService{Store: repository, WorkRoot: work, QuarantineRoot: quarantine, Now: func() time.Time { return now }}})
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/reviews", nil))
	if unauthenticated.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated status=%d", unauthenticated.Code)
	}
	session, csrf := loginAdmin(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/reviews", nil)
	request.AddCookie(session)
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), candidate.ID) || !strings.Contains(response.Body.String(), "REVIEW_REQUIRED") {
		t.Fatalf("review page status/body=%d %s", response.Code, response.Body.String())
	}
	for header, want := range map[string]string{"Cache-Control": "no-store", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if response.Header().Get(header) != want {
			t.Fatalf("%s=%q", header, response.Header().Get(header))
		}
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("CSP=%q", response.Header().Get("Content-Security-Policy"))
	}
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, httptest.NewRequest(http.MethodGet, bundle.Paths.HTMX, nil))
	if assetResponse.Code != http.StatusOK || assetResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset response=%d %q", assetResponse.Code, assetResponse.Header().Get("Cache-Control"))
	}
}

func TestAdminUIRejectsMissingCSRFAndReturnsStaleReviewFragment(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='REVIEW_REQUIRED',state_revision=3,updated_at=? WHERE candidate_id=?`, now.Format(time.RFC3339Nano), candidate.ID); err != nil {
		t.Fatal(err)
	}
	bundle, _ := assets.New()
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	handler, err := handlers.New(handlers.Dependencies{Auth: auth, Reader: repository, Assets: bundle, Reviews: application.ReviewDecisionService{Store: repository, WorkRoot: t.TempDir(), QuarantineRoot: t.TempDir(), Now: func() time.Time { return now }}})
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := loginAdmin(t, handler)
	form := url.Values{"state_revision": {"2"}, "release_mbid": {"12345678-1234-1234-1234-123456789abc"}, "reason": {"verified"}, "confirm": {"yes"}}
	request := httptest.NewRequest(http.MethodPost, "/reviews/"+candidate.ID+"/approve", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d", denied.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/reviews/"+candidate.ID+"/approve", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	request.Header.Set("X-CSRF-Token", csrf.Value)
	request.AddCookie(session)
	request.AddCookie(csrf)
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, request)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "current state REVIEW_REQUIRED at revision 3") || strings.Contains(stale.Body.String(), "<!doctype html>") {
		t.Fatalf("stale response=%d %s", stale.Code, stale.Body.String())
	}
}

func loginAdmin(t *testing.T, handler http.Handler) (*http.Cookie, *http.Cookie) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=password123"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login=%d %s", response.Code, response.Body.String())
	}
	var session, csrf *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == middleware.SessionCookie {
			session = cookie
		}
		if cookie.Name == middleware.CSRFCookie {
			csrf = cookie
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("missing auth cookies")
	}
	return session, csrf
}
