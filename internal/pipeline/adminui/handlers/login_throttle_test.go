package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func TestLoginThrottlePolicy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(5, 15*time.Minute, time.Second, time.Minute, 128)
	first := sha256.Sum256([]byte("first"))
	second := sha256.Sum256([]byte("second"))

	for attempt := 1; attempt <= 4; attempt++ {
		if blocked := throttle.Failure(first, now); blocked {
			t.Fatalf("attempt %d blocked before threshold", attempt)
		}
	}
	if blocked := throttle.Failure(first, now); !blocked {
		t.Fatal("fifth failure did not enter blocked state")
	}
	if retry, allowed := throttle.Allow(first, now); allowed || retry != time.Second {
		t.Fatalf("first delay = %s allowed=%t", retry, allowed)
	}
	if _, allowed := throttle.Allow(second, now); !allowed {
		t.Fatal("independent key was throttled")
	}

	now = now.Add(time.Second)
	if _, allowed := throttle.Allow(first, now); !allowed {
		t.Fatal("key remained blocked after base delay")
	}
	if blocked := throttle.Failure(first, now); blocked {
		t.Fatal("aggregate block event repeated in one window")
	}
	if retry, allowed := throttle.Allow(first, now); allowed || retry != 2*time.Second {
		t.Fatalf("exponential delay = %s allowed=%t", retry, allowed)
	}

	throttle.Success(first)
	if _, allowed := throttle.Allow(first, now); !allowed {
		t.Fatal("success did not reset key")
	}
	if blocked := throttle.Failure(first, now); blocked {
		t.Fatal("reset key retained failure count")
	}

	now = now.Add(16 * time.Minute)
	for attempt := 0; attempt < 5; attempt++ {
		throttle.Failure(first, now)
	}
	if retry, allowed := throttle.Allow(first, now.Add(16*time.Minute)); !allowed || retry != 0 {
		t.Fatalf("expired window retry=%s allowed=%t", retry, allowed)
	}

	for index := 0; index < 200; index++ {
		key := sha256.Sum256([]byte{byte(index), byte(index >> 8)})
		throttle.Failure(key, now)
	}
	if throttle.size() > 128 {
		t.Fatalf("tracked keys = %d, want <= 128", throttle.size())
	}
}

func TestLoginThrottleConcurrentAccess(t *testing.T) {
	t.Parallel()
	throttle := NewLoginThrottle(5, 15*time.Minute, time.Second, time.Minute, 128)
	now := time.Now().UTC()
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			key := sha256.Sum256([]byte{byte(index % 8)})
			throttle.Allow(key, now)
			throttle.Failure(key, now)
			if index%3 == 0 {
				throttle.Success(key)
			}
		}(index)
	}
	wait.Wait()
}

func TestLoginIsThrottledWithGenericResponseAndAggregateAudit(t *testing.T) {
	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	passwordHash, err := application.HashPassword("correct-password", 8, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	repository := &loginRepository{user: application.UserRecord{ID: "admin-id", Username: "admin", PasswordHash: passwordHash, Roles: []string{"admin"}}}
	auth := application.AuthService{Repository: repository, AbsoluteExpiry: time.Hour, PasswordMinLen: 8, Now: func() time.Time { return now }}
	handler := Login{Auth: auth, Throttle: NewLoginThrottle(5, 15*time.Minute, time.Second, time.Minute, 128), Now: func() time.Time { return now }}

	for attempt := 1; attempt <= 5; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=visible-wrong-password"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "192.0.2.10:4321"
		response := httptest.NewRecorder()
		handler.Post(response, request)
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want || !strings.Contains(response.Body.String(), "Authentication failed") || strings.Contains(response.Body.String(), "visible-wrong-password") {
			t.Fatalf("attempt %d response=%d %s", attempt, response.Code, response.Body.String())
		}
		if attempt == 5 && response.Header().Get("Retry-After") != "1" {
			t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
		}
	}

	blocked := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=visible-wrong-password"))
	blocked.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	blocked.RemoteAddr = "192.0.2.10:4321"
	blockedResponse := httptest.NewRecorder()
	handler.Post(blockedResponse, blocked)
	if blockedResponse.Code != http.StatusTooManyRequests || repository.auditCount() != 1 {
		t.Fatalf("blocked response=%d audits=%d", blockedResponse.Code, repository.auditCount())
	}
	if repository.auditActor == "" || strings.Contains(repository.auditActor, "admin") || strings.Contains(repository.auditActor, "192.0.2.10") {
		t.Fatalf("unsafe audit actor %q", repository.auditActor)
	}

	now = now.Add(time.Second)
	repeated := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=visible-wrong-password"))
	repeated.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	repeated.RemoteAddr = "192.0.2.10:4321"
	repeatedResponse := httptest.NewRecorder()
	handler.Post(repeatedResponse, repeated)
	if repeatedResponse.Code != http.StatusTooManyRequests || repeatedResponse.Header().Get("Retry-After") != "2" || repository.auditCount() != 1 {
		t.Fatalf("repeated response=%d retry=%q audits=%d", repeatedResponse.Code, repeatedResponse.Header().Get("Retry-After"), repository.auditCount())
	}

	now = now.Add(2 * time.Second)
	success := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=correct-password"))
	success.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	success.RemoteAddr = "192.0.2.10:4321"
	successResponse := httptest.NewRecorder()
	handler.Post(successResponse, success)
	if successResponse.Code != http.StatusSeeOther {
		t.Fatalf("successful login status=%d body=%s", successResponse.Code, successResponse.Body.String())
	}
}

type loginRepository struct {
	mu         sync.Mutex
	user       application.UserRecord
	audits     int
	auditActor string
}

func (r *loginRepository) UserByUsername(_ context.Context, username string) (application.UserRecord, error) {
	if username != r.user.Username {
		return application.UserRecord{}, application.ErrUserNotFound
	}
	return r.user, nil
}
func (r *loginRepository) UserByID(context.Context, string) (application.UserRecord, error) {
	return r.user, nil
}
func (r *loginRepository) CreateSession(context.Context, application.SessionRecord, string) error {
	return nil
}
func (r *loginRepository) SessionByTokenHash(context.Context, [32]byte) (application.SessionRecord, error) {
	return application.SessionRecord{}, application.ErrSessionNotFound
}
func (r *loginRepository) RevokeSession(context.Context, string, string, string, time.Time) error {
	return nil
}
func (r *loginRepository) RevokeAllSessions(context.Context, string, string, string, time.Time) error {
	return nil
}
func (r *loginRepository) ChangePasswordAndRevoke(context.Context, string, string, string, time.Time) error {
	return nil
}
func (r *loginRepository) AppendLoginThrottleAudit(_ context.Context, actor string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audits++
	r.auditActor = actor
	return nil
}
func (r *loginRepository) auditCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.audits
}
