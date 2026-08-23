package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/ids"
)

var ErrUserNotFound = errors.New("user not found")
var ErrSessionNotFound = errors.New("session not found")

type UserRecord struct {
	ID           string
	Username     string
	PasswordHash string
	Roles        []string
	Disabled     bool
}

type SessionRecord struct {
	ID        string
	User      UserRecord
	TokenHash [32]byte
	CSRFHash  [32]byte
	CreatedAt time.Time
	ExpiresAt time.Time
	Revoked   bool
}

type SessionRepository interface {
	UserByUsername(context.Context, string) (UserRecord, error)
	UserByID(context.Context, string) (UserRecord, error)
	CreateSession(context.Context, SessionRecord, string) error
	SessionByTokenHash(context.Context, [32]byte) (SessionRecord, error)
	RevokeSession(context.Context, string, string, string, time.Time) error
	RevokeAllSessions(context.Context, string, string, string, time.Time) error
	ChangePasswordAndRevoke(context.Context, string, string, string, time.Time) error
}

type SessionCredentials struct {
	SessionID string
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

type Principal struct {
	SessionID string
	UserID    string
	Username  string
	Roles     []string
	CSRFHash  [32]byte
	ExpiresAt time.Time
}

type AuthService struct {
	Repository     SessionRepository
	AbsoluteExpiry time.Duration
	PasswordMinLen int
	Random         io.Reader
	Now            func() time.Time
}

func (s AuthService) Login(ctx context.Context, username, password string) (SessionCredentials, error) {
	user, err := s.Repository.UserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			_ = VerifyPassword(dummyPasswordHash, password)
			return SessionCredentials{}, ErrAuthentication
		}
		return SessionCredentials{}, err
	}
	if user.Disabled || !VerifyPassword(user.PasswordHash, password) {
		return SessionCredentials{}, ErrAuthentication
	}
	credentials, err := s.newSession(ctx, user, "LOGIN_SUCCESS")
	if err != nil {
		return SessionCredentials{}, err
	}
	return credentials, nil
}

func (s AuthService) Authenticate(ctx context.Context, token string) (Principal, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return Principal{}, ErrAuthentication
	}
	hash := sha256.Sum256(raw)
	record, err := s.Repository.SessionByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return Principal{}, ErrAuthentication
		}
		return Principal{}, err
	}
	if record.Revoked || !record.ExpiresAt.After(s.now()) || record.User.Disabled {
		return Principal{}, ErrAuthentication
	}
	return Principal{SessionID: record.ID, UserID: record.User.ID, Username: record.User.Username, Roles: append([]string(nil), record.User.Roles...), CSRFHash: record.CSRFHash, ExpiresAt: record.ExpiresAt}, nil
}

func (s AuthService) ValidateCSRF(principal Principal, token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return false
	}
	hash := sha256.Sum256(raw)
	return subtle.ConstantTimeCompare(hash[:], principal.CSRFHash[:]) == 1
}

func (s AuthService) ChangePassword(ctx context.Context, principal Principal, currentPassword, newPassword string) (SessionCredentials, error) {
	user, err := s.Repository.UserByID(ctx, principal.UserID)
	if err != nil {
		return SessionCredentials{}, err
	}
	if !VerifyPassword(user.PasswordHash, currentPassword) {
		return SessionCredentials{}, ErrAuthentication
	}
	hash, err := HashPassword(newPassword, s.PasswordMinLen, s.random())
	if err != nil {
		return SessionCredentials{}, err
	}
	if err := s.Repository.ChangePasswordAndRevoke(ctx, user.ID, hash, "password changed", s.now()); err != nil {
		return SessionCredentials{}, err
	}
	return s.newSession(ctx, user, "PASSWORD_SESSION_ROTATED")
}

func (s AuthService) Logout(ctx context.Context, principal Principal) error {
	return s.Repository.RevokeSession(ctx, principal.SessionID, principal.UserID, "logout", s.now())
}

func (s AuthService) LogoutAll(ctx context.Context, principal Principal) error {
	return s.Repository.RevokeAllSessions(ctx, principal.UserID, principal.UserID, "logout all", s.now())
}

func (s AuthService) newSession(ctx context.Context, user UserRecord, action string) (SessionCredentials, error) {
	if s.AbsoluteExpiry <= 0 {
		return SessionCredentials{}, fmt.Errorf("absolute session expiry must be positive")
	}
	random := s.random()
	token, err := randomBytes(random, 32)
	if err != nil {
		return SessionCredentials{}, err
	}
	csrf, err := randomBytes(random, 32)
	if err != nil {
		return SessionCredentials{}, err
	}
	sessionID, err := ids.NewToken(16)
	if err != nil {
		return SessionCredentials{}, err
	}
	now := s.now()
	record := SessionRecord{ID: sessionID, User: user, TokenHash: sha256.Sum256(token), CSRFHash: sha256.Sum256(csrf), CreatedAt: now, ExpiresAt: now.Add(s.AbsoluteExpiry)}
	if err := s.Repository.CreateSession(ctx, record, action); err != nil {
		return SessionCredentials{}, err
	}
	return SessionCredentials{SessionID: sessionID, Token: base64.RawURLEncoding.EncodeToString(token), CSRFToken: base64.RawURLEncoding.EncodeToString(csrf), ExpiresAt: record.ExpiresAt}, nil
}

func (s AuthService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s AuthService) random() io.Reader {
	if s.Random != nil {
		return s.Random
	}
	return rand.Reader
}

func randomBytes(reader io.Reader, size int) ([]byte, error) {
	value := make([]byte, size)
	_, err := io.ReadFull(reader, value)
	return value, err
}

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
