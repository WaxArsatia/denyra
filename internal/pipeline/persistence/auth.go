package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) UserCount(ctx context.Context) (int, error) {
	var count int
	return count, r.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
}

func (r *Repositories) CreateFirstAdmin(ctx context.Context, username, passwordHash string, at time.Time) (string, error) {
	userID, err := ids.NewToken(16)
	if err != nil {
		return "", err
	}
	err = denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("first admin already exists")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO roles(id,name,created_at) VALUES('role-admin','admin',?) ON CONFLICT(name) DO NOTHING", formatTime(at)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,password_changed_at,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
			userID, username, passwordHash, formatTime(at), formatTime(at), formatTime(at)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO user_roles(user_id,role_id,granted_by,granted_at) VALUES(?,'role-admin',?,?)", userID, userID, formatTime(at)); err != nil {
			return err
		}
		return appendAuthAuditTx(ctx, tx, userID, "BOOTSTRAP_ADMIN", "first-run bootstrap", at)
	})
	return userID, err
}

func (r *Repositories) UserByUsername(ctx context.Context, username string) (application.UserRecord, error) {
	return r.user(ctx, "u.username = ?", username)
}

func (r *Repositories) UserByID(ctx context.Context, userID string) (application.UserRecord, error) {
	return r.user(ctx, "u.id = ?", userID)
}

func (r *Repositories) user(ctx context.Context, where string, argument any) (application.UserRecord, error) {
	var user application.UserRecord
	var disabled sql.NullString
	err := r.DB.QueryRowContext(ctx, "SELECT u.id,u.username,u.password_hash,u.disabled_at FROM users u WHERE "+where, argument).Scan(&user.ID, &user.Username, &user.PasswordHash, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return user, application.ErrUserNotFound
	}
	if err != nil {
		return user, err
	}
	user.Disabled = disabled.Valid
	rows, err := r.DB.QueryContext(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id=r.id WHERE ur.user_id=? ORDER BY r.name`, user.ID)
	if err != nil {
		return user, err
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return user, err
		}
		user.Roles = append(user.Roles, role)
	}
	return user, rows.Err()
}

func (r *Repositories) CreateSession(ctx context.Context, session application.SessionRecord, action string) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,user_id,token_hash,csrf_hash,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
			session.ID, session.User.ID, session.TokenHash[:], session.CSRFHash[:], formatTime(session.CreatedAt), formatTime(session.ExpiresAt)); err != nil {
			return err
		}
		return appendAuthAuditTx(ctx, tx, session.User.ID, action, "server-side session created", session.CreatedAt)
	})
}

func (r *Repositories) SessionByTokenHash(ctx context.Context, tokenHash [32]byte) (application.SessionRecord, error) {
	var record application.SessionRecord
	var csrf []byte
	var created, expires string
	var revoked sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT s.id,s.user_id,s.csrf_hash,s.created_at,s.expires_at,s.revoked_at FROM sessions s WHERE s.token_hash=?`, tokenHash[:]).Scan(
		&record.ID, &record.User.ID, &csrf, &created, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return record, application.ErrSessionNotFound
	}
	if err != nil {
		return record, err
	}
	if len(csrf) != 32 {
		return record, fmt.Errorf("stored CSRF hash has invalid length")
	}
	copy(record.TokenHash[:], tokenHash[:])
	copy(record.CSRFHash[:], csrf)
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err == nil {
		record.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	}
	if err != nil {
		return record, err
	}
	record.Revoked = revoked.Valid
	record.User, err = r.UserByID(ctx, record.User.ID)
	return record, err
}

func (r *Repositories) RevokeSession(ctx context.Context, sessionID, actorID, reason string, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=?,revocation_reason=? WHERE id=? AND revoked_at IS NULL", formatTime(at), reason, sessionID); err != nil {
			return err
		}
		return appendAuthAuditTx(ctx, tx, actorID, "SESSION_REVOKED", reason, at)
	})
}

func (r *Repositories) RevokeAllSessions(ctx context.Context, userID, actorID, reason string, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=?,revocation_reason=? WHERE user_id=? AND revoked_at IS NULL", formatTime(at), reason, userID); err != nil {
			return err
		}
		return appendAuthAuditTx(ctx, tx, actorID, "SESSIONS_REVOKED_ALL", reason, at)
	})
}

func (r *Repositories) ChangePasswordAndRevoke(ctx context.Context, userID, passwordHash, reason string, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=?,password_changed_at=?,updated_at=? WHERE id=?", passwordHash, formatTime(at), formatTime(at), userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=?,revocation_reason=? WHERE user_id=? AND revoked_at IS NULL", formatTime(at), reason, userID); err != nil {
			return err
		}
		return appendAuthAuditTx(ctx, tx, userID, "PASSWORD_CHANGED", reason, at)
	})
}

func (r *Repositories) AppendLoginThrottleAudit(ctx context.Context, actor string, at time.Time) error {
	id, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO audit_events(id,actor,action,reason,details_json,occurred_at) VALUES(?,?,?,?,?,?)`, id, actor, "LOGIN_THROTTLED", "authentication attempt limit reached", []byte("{}"), formatTime(at))
	return err
}

func appendAuthAuditTx(ctx context.Context, tx *sql.Tx, actorID, action, reason string, at time.Time) error {
	id, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor,action,reason,details_json,occurred_at) VALUES(?,?,?,?,?,?)`, id, actorID, action, reason, []byte("{}"), formatTime(at))
	return err
}
