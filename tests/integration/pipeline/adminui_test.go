package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/handlers"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestAdminUIStreamsFolderUploadWithCSRFAndDeclaredSizeLimit(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	bundle, err := assets.New()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	uploading, incoming := filepath.Join(root, "uploading"), filepath.Join(root, "manual")
	uploads := &application.UploadService{
		Store: repository, Writer: denyrafs.UploadWriter{Root: uploading}, UploadingRoot: uploading, IncomingRoot: incoming,
		Policy: config.UploadConfig{MaxFileBytes: 1 << 20, MaxSessionBytes: 2 << 20, MaxEntries: 10, BrowserConcurrency: 3, ImageMaxBytes: 1, ImageMaxPixels: 1},
		Now:    func() time.Time { return now },
	}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	handler, err := handlers.New(handlers.Dependencies{Auth: auth, Reader: repository, Assets: bundle, Uploads: uploads})
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie, csrfCookie := loginAdmin(t, handler)

	pageRequest := httptest.NewRequest(http.MethodGet, "/incoming", nil)
	pageRequest.AddCookie(sessionCookie)
	pageRequest.AddCookie(csrfCookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "webkitdirectory") || !strings.Contains(page.Body.String(), `data-upload-concurrency="3"`) {
		t.Fatalf("incoming upload page=%d %s", page.Code, page.Body.String())
	}

	payload := bytes.Repeat([]byte("f"), 300<<10)
	manifest, _ := json.Marshal(map[string]any{"files": []map[string]any{{"relative_path": "OFF GUARD/01.flac", "size_bytes": len(payload), "media_type": "audio/flac"}}})
	create := authenticatedMutation(http.MethodPost, "/upload-sessions", bytes.NewReader(manifest), sessionCookie, csrfCookie)
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var upload domain.UploadSession
	if err := json.NewDecoder(created.Body).Decode(&upload); err != nil {
		t.Fatal(err)
	}

	missingCSRF := httptest.NewRequest(http.MethodPut, "/upload-sessions/"+upload.ID+"/files/"+upload.Files[0].ID, bytes.NewReader(payload))
	missingCSRF.AddCookie(sessionCookie)
	missingCSRF.AddCookie(csrfCookie)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, missingCSRF)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", denied.Code)
	}

	tooLarge := authenticatedMutation(http.MethodPut, "/upload-sessions/"+upload.ID+"/files/"+upload.Files[0].ID, bytes.NewReader(append(payload, 'x')), sessionCookie, csrfCookie)
	tooLargeResponse := httptest.NewRecorder()
	handler.ServeHTTP(tooLargeResponse, tooLarge)
	if tooLargeResponse.Code != http.StatusRequestEntityTooLarge || !strings.Contains(tooLargeResponse.Body.String(), "ENTRY_MISMATCH") || strings.Contains(tooLargeResponse.Body.String(), root) {
		t.Fatalf("oversize=%d %s", tooLargeResponse.Code, tooLargeResponse.Body.String())
	}

	put := authenticatedMutation(http.MethodPut, "/upload-sessions/"+upload.ID+"/files/"+upload.Files[0].ID, bytes.NewReader(payload), sessionCookie, csrfCookie)
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("put=%d %s", putResponse.Code, putResponse.Body.String())
	}
	finalize := authenticatedMutation(http.MethodPost, "/upload-sessions/"+upload.ID+"/finalize", nil, sessionCookie, csrfCookie)
	finalized := httptest.NewRecorder()
	handler.ServeHTTP(finalized, finalize)
	if finalized.Code != http.StatusOK {
		t.Fatalf("finalize=%d %s", finalized.Code, finalized.Body.String())
	}
	if _, err := os.Stat(filepath.Join(incoming, upload.SubmissionID, "OFF GUARD", "01.flac")); err != nil {
		t.Fatalf("final file: %v", err)
	}

	get := httptest.NewRequest(http.MethodGet, "/upload-sessions/"+upload.ID, nil)
	get.AddCookie(sessionCookie)
	get.AddCookie(csrfCookie)
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), domain.UploadSessionFinalized) {
		t.Fatalf("get=%d %s", got.Code, got.Body.String())
	}
}

func TestAdminUIEditsPreviewAndSubmitsSealedDecision(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	incoming := t.TempDir()
	source := filepath.Join(incoming, "submission-preview-ui")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "01.flac"), []byte("fixture"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := repository.DiscoverSubmission(context.Background(), "submission-preview-ui", source, now); err != nil {
		t.Fatal(err)
	}
	bundle, _ := assets.New()
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	previews := &application.SubmissionPreviewService{Store: repository, Inspector: uiPreviewInspector{}, Now: func() time.Time { return now }}
	handler, err := handlers.New(handlers.Dependencies{Auth: auth, Reader: repository, Assets: bundle, Previews: previews, Submissions: application.SubmissionService{Store: repository, IncomingRoot: incoming, Now: func() time.Time { return now }}})
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := loginAdmin(t, handler)
	get := httptest.NewRequest(http.MethodGet, "/incoming/submission-preview-ui", nil)
	get.AddCookie(session)
	get.AddCookie(csrf)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, get)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="album_artist" value="Kaleb J"`) || !strings.Contains(page.Body.String(), `name="destination"`) {
		t.Fatalf("preview page=%d %s", page.Code, page.Body.String())
	}
	preview, err := previews.Preview(context.Background(), "submission-preview-ui", false)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"_csrf": {csrf.Value}, "preview_fingerprint": {preview.Fingerprint}, "destination": {string(domain.DestinationUnmanaged)},
		"album_artist": {"Kaleb J"}, "album": {"OFF GUARD"}, "date": {"2024"}, "edition": {""},
		"track_path": {"01.flac"}, "track_title": {"Track"}, "track_artist": {"Kaleb J"}, "track_number": {"1"}, "disc_number": {"1"},
	}
	post := authenticatedMutation(http.MethodPost, "/incoming/submission-preview-ui/submit", strings.NewReader(form.Encode()), session, csrf)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("submit=%d %s", response.Code, response.Body.String())
	}
	record, err := repository.Submission(context.Background(), "submission-preview-ui")
	if err != nil || record.Status != "SEALED" || record.SealedFingerprint != preview.Fingerprint {
		t.Fatalf("sealed submission=%+v err=%v", record, err)
	}
}

type uiPreviewInspector struct{}

func (uiPreviewInspector) Inspect(context.Context, string) (domain.TechnicalInfo, map[string][]string, domain.CommandEvidence, error) {
	return domain.TechnicalInfo{Container: "flac", Codec: "flac", Channels: 2, DurationMS: 1000, SampleRate: 44100, BitDepth: 16}, map[string][]string{
		"ALBUMARTIST": {"Kaleb J"}, "ALBUM": {"OFF GUARD"}, "TITLE": {"Track"}, "ARTIST": {"Kaleb J"}, "TRACKNUMBER": {"1"}, "DISCNUMBER": {"1"},
	}, domain.CommandEvidence{}, nil
}

func authenticatedMutation(method, target string, body io.Reader, session, csrf *http.Cookie) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("X-CSRF-Token", csrf.Value)
	request.AddCookie(session)
	request.AddCookie(csrf)
	return request
}

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

func TestAdminUIRetriesQuarantinedCandidate(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='QUARANTINED',state_revision=7,updated_at=? WHERE candidate_id=?`, now.Format(time.RFC3339Nano), candidate.ID); err != nil {
		t.Fatal(err)
	}
	quarantine := t.TempDir()
	work := t.TempDir()
	quarantinedCandidate := filepath.Join(quarantine, candidate.ID)
	if err := os.Mkdir(quarantinedCandidate, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quarantinedCandidate, "01.flac"), []byte("candidate"), 0o640); err != nil {
		t.Fatal(err)
	}
	bundle, err := assets.New()
	if err != nil {
		t.Fatal(err)
	}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	handler, err := handlers.New(handlers.Dependencies{
		Auth: auth, Reader: repository, Assets: bundle,
		Reviews: application.ReviewDecisionService{
			Store: repository, WorkRoot: work, QuarantineRoot: quarantine,
			Now: func() time.Time { return now },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := loginAdmin(t, handler)
	form := url.Values{
		"_csrf":          {csrf.Value},
		"state_revision": {"7"},
		"reason":         {"retry after deterministic tool fix"},
		"confirm":        {"yes"},
	}
	request := httptest.NewRequest(http.MethodPost, "/reviews/"+candidate.ID+"/retry", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	request.AddCookie(session)
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	updated, err := repository.Candidate(context.Background(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.StateWorking || updated.StateRevision != 8 {
		t.Fatalf("candidate state=%s revision=%d", updated.State, updated.StateRevision)
	}
	if _, err := os.Stat(filepath.Join(work, candidate.ID, "01.flac")); err != nil {
		t.Fatalf("candidate was not returned to work: %v", err)
	}
	if _, err := os.Stat(quarantinedCandidate); !os.IsNotExist(err) {
		t.Fatalf("quarantined path still exists: %v", err)
	}
}

func TestAdminUIRetryMoveFailureKeepsCandidateQuarantined(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='QUARANTINED',state_revision=7,updated_at=? WHERE candidate_id=?`, now.Format(time.RFC3339Nano), candidate.ID); err != nil {
		t.Fatal(err)
	}
	quarantine := t.TempDir()
	work := t.TempDir()
	quarantinedCandidate := filepath.Join(quarantine, candidate.ID)
	if err := os.Mkdir(quarantinedCandidate, 0o750); err != nil {
		t.Fatal(err)
	}
	bundle, err := assets.New()
	if err != nil {
		t.Fatal(err)
	}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	handler, err := handlers.New(handlers.Dependencies{
		Auth: auth, Reader: repository, Assets: bundle,
		Reviews: application.ReviewDecisionService{
			Store: repository, WorkRoot: work, QuarantineRoot: quarantine,
			Move: func(string, string) error { return errors.New("injected move failure") },
			Now:  func() time.Time { return now },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := loginAdmin(t, handler)
	form := url.Values{
		"_csrf":          {csrf.Value},
		"state_revision": {"7"},
		"reason":         {"retry after deterministic tool fix"},
		"confirm":        {"yes"},
	}
	request := httptest.NewRequest(http.MethodPost, "/reviews/"+candidate.ID+"/retry", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	request.AddCookie(session)
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	updated, err := repository.Candidate(context.Background(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.StateQuarantined || updated.StateRevision != 7 {
		t.Fatalf("candidate state=%s revision=%d", updated.State, updated.StateRevision)
	}
	if _, err := os.Stat(quarantinedCandidate); err != nil {
		t.Fatalf("quarantined path changed after failed move: %v", err)
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
