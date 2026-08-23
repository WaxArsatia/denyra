package pipeline_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/handlers"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func TestAuthBootstrapArgonSessionHashExpiryAndCSRF(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	secret := filepath.Join(t.TempDir(), "bootstrap")
	if err := os.WriteFile(secret, []byte("password123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := application.BootstrapAdmin(context.Background(), repository, "admin", "", secret, 8, now)
	if err != nil || !created {
		t.Fatalf("bootstrap = %v, %v", created, err)
	}
	if created, err := application.BootstrapAdmin(context.Background(), repository, "replacement", "different-password", "", 8, now); err != nil || created {
		t.Fatalf("permanent bootstrap login path remained: created=%v err=%v", created, err)
	}
	user, err := repository.UserByUsername(context.Background(), "admin")
	if err != nil || !strings.HasPrefix(user.PasswordHash, "$argon2id$v=19$m=65536,t=3,p=2$") || !application.VerifyPassword(user.PasswordHash, "password123") {
		t.Fatalf("stored password parameters = %q, %v", user.PasswordHash, err)
	}
	current := now
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, 1024)), Now: func() time.Time { return current }}
	credentials, err := auth.Login(context.Background(), "admin", "password123")
	if err != nil {
		t.Fatal(err)
	}
	rawToken, err := base64.RawURLEncoding.DecodeString(credentials.Token)
	if err != nil || len(rawToken) != 32 || credentials.ExpiresAt.Sub(now) != 30*24*time.Hour {
		t.Fatalf("session credential = %+v, raw=%d, err=%v", credentials, len(rawToken), err)
	}
	var stored []byte
	if err := db.QueryRow("SELECT token_hash FROM sessions WHERE id=?", credentials.SessionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256(rawToken)
	if string(stored) != string(expectedHash[:]) || string(stored) == credentials.Token || string(stored) == string(rawToken) {
		t.Fatal("raw session token was stored or hash is wrong")
	}
	principal, err := auth.Authenticate(context.Background(), credentials.Token)
	if err != nil || !auth.ValidateCSRF(principal, credentials.CSRFToken) || auth.ValidateCSRF(principal, "wrong") {
		t.Fatalf("session authentication/CSRF = %+v, %v", principal, err)
	}
	current = credentials.ExpiresAt
	if _, err := auth.Authenticate(context.Background(), credentials.Token); err != application.ErrAuthentication {
		t.Fatalf("absolute expiry boundary accepted: %v", err)
	}
}

func TestAuthGenericLoginErrorsAndHTTPInternalCookieBaseline(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Random: strings.NewReader(strings.Repeat("x", 1024)), Now: func() time.Time { return now }}
	handler := handlers.Login{Auth: auth}
	responses := make([]*httptest.ResponseRecorder, 0, 2)
	for _, form := range []string{"username=missing&password=password123", "username=admin&password=incorrect"} {
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.Post(response, request)
		responses = append(responses, response)
	}
	if responses[0].Code != responses[1].Code || responses[0].Body.String() != responses[1].Body.String() || responses[0].Code != http.StatusUnauthorized {
		t.Fatalf("login errors reveal username validity: first=%d %q second=%d %q", responses[0].Code, responses[0].Body.String(), responses[1].Code, responses[1].Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=password123"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Post(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("valid login status = %d", response.Code)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case middleware.SessionCookie:
			sessionCookie = cookie
		case middleware.CSRFCookie:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != "/" || csrfCookie == nil {
		t.Fatalf("internal HTTP cookie baseline wrong: session=%+v csrf=%+v", sessionCookie, csrfCookie)
	}

	protected := middleware.RequireAuth(auth, middleware.RequireCSRF(auth, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })))
	mutation := httptest.NewRequest(http.MethodPost, "/reviews/candidate/approve", nil)
	mutation.AddCookie(sessionCookie)
	mutation.AddCookie(csrfCookie)
	denied := httptest.NewRecorder()
	protected.ServeHTTP(denied, mutation)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", denied.Code)
	}
	mutation = httptest.NewRequest(http.MethodPost, "/reviews/candidate/approve", nil)
	mutation.AddCookie(sessionCookie)
	mutation.AddCookie(csrfCookie)
	mutation.Header.Set("X-CSRF-Token", csrfCookie.Value)
	accepted := httptest.NewRecorder()
	protected.ServeHTTP(accepted, mutation)
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF status = %d body=%s", accepted.Code, accepted.Body.String())
	}
}

func TestAuthPasswordChangeRotatesAndRevokesOldSessions(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	if _, err := application.BootstrapAdmin(context.Background(), repository, "admin", "password123", "", 8, now); err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 2048)
	for index := range random {
		random[index] = byte(index)
	}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: 30 * 24 * time.Hour, PasswordMinLen: 8, Random: bytes.NewReader(random), Now: func() time.Time { return now }}
	old, err := auth.Login(context.Background(), "admin", "password123")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.Authenticate(context.Background(), old.Token)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := auth.ChangePassword(context.Background(), principal, "password123", "new-password")
	if err != nil || rotated.Token == old.Token {
		t.Fatalf("password rotation = %+v, %v", rotated, err)
	}
	if _, err := auth.Authenticate(context.Background(), old.Token); err != application.ErrAuthentication {
		t.Fatalf("old session retained after password change: %v", err)
	}
	if _, err := auth.Authenticate(context.Background(), rotated.Token); err != nil {
		t.Fatalf("rotated session invalid: %v", err)
	}
}
