package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) CreateUploadSession(ctx context.Context, session domain.UploadSession) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO upload_sessions(id,submission_id,actor,status,state_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			session.ID, session.SubmissionID, session.Actor, session.Status, session.Revision, formatTime(session.CreatedAt), formatTime(session.UpdatedAt)); err != nil {
			return err
		}
		for _, file := range session.Files {
			normalized, err := domain.UploadPathKey(file.RelativePath)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO upload_entries(id,session_id,relative_path,normalized_path,media_type,size_bytes,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				file.ID, session.ID, file.RelativePath, normalized, file.MediaType, file.SizeBytes, file.Status, formatTime(session.CreatedAt), formatTime(session.UpdatedAt)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repositories) UploadSession(ctx context.Context, id string) (domain.UploadSession, error) {
	var session domain.UploadSession
	var created, updated string
	err := r.DB.QueryRowContext(ctx, `SELECT id,submission_id,actor,status,state_revision,created_at,updated_at FROM upload_sessions WHERE id=?`, id).Scan(
		&session.ID, &session.SubmissionID, &session.Actor, &session.Status, &session.Revision, &created, &updated)
	if err != nil {
		return domain.UploadSession{}, err
	}
	if session.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return domain.UploadSession{}, err
	}
	if session.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return domain.UploadSession{}, err
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT id,relative_path,media_type,size_bytes,status FROM upload_entries WHERE session_id=? ORDER BY relative_path,id`, id)
	if err != nil {
		return domain.UploadSession{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var file domain.UploadFileSpec
		if err := rows.Scan(&file.ID, &file.RelativePath, &file.MediaType, &file.SizeBytes, &file.Status); err != nil {
			return domain.UploadSession{}, err
		}
		session.Files = append(session.Files, file)
	}
	return session, rows.Err()
}

func (r *Repositories) UploadSessions(ctx context.Context, actor string) ([]domain.UploadSession, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id FROM upload_sessions WHERE actor=? AND status<>'DELETED' ORDER BY updated_at DESC,id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]domain.UploadSession, 0, len(ids))
	for _, id := range ids {
		session, err := r.UploadSession(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, nil
}

func (r *Repositories) CompleteUploadEntry(ctx context.Context, sessionID, entryID string, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		var sessionStatus, entryStatus string
		if err := tx.QueryRowContext(ctx, `SELECT s.status,e.status FROM upload_sessions s JOIN upload_entries e ON e.session_id=s.id WHERE s.id=? AND e.id=?`, sessionID, entryID).Scan(&sessionStatus, &entryStatus); err != nil {
			return err
		}
		if sessionStatus != domain.UploadSessionOpen {
			return application.ErrUploadConflict
		}
		if entryStatus == domain.UploadEntryComplete {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE upload_entries SET status='COMPLETE',updated_at=? WHERE id=? AND session_id=?`, formatTime(at), entryID, sessionID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE upload_sessions SET state_revision=state_revision+1,updated_at=? WHERE id=? AND status='OPEN'`, formatTime(at), sessionID)
		return err
	})
}

func (r *Repositories) FinalizeUploadSession(ctx context.Context, sessionID, sourcePath string, provenance []byte, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		var submissionID, status string
		if err := tx.QueryRowContext(ctx, `SELECT submission_id,status FROM upload_sessions WHERE id=?`, sessionID).Scan(&submissionID, &status); err != nil {
			return err
		}
		if status == domain.UploadSessionFinalized {
			var storedPath string
			var storedProvenance []byte
			if err := tx.QueryRowContext(ctx, `SELECT source_path,provenance_json FROM submissions WHERE id=? AND ingress='browser'`, submissionID).Scan(&storedPath, &storedProvenance); err != nil {
				return err
			}
			if storedPath != sourcePath || string(storedProvenance) != string(provenance) {
				return application.ErrUploadConflict
			}
			return nil
		}
		if status != domain.UploadSessionOpen {
			return application.ErrUploadConflict
		}
		var incomplete int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM upload_entries WHERE session_id=? AND status<>'COMPLETE'`, sessionID).Scan(&incomplete); err != nil {
			return err
		}
		if incomplete != 0 {
			return application.ErrUploadConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO submissions(id,source_path,status,state_revision,ingress,provenance_json,created_at,updated_at) VALUES(?,?,'DISCOVERED',0,'browser',?,?,?)`, submissionID, sourcePath, provenance, formatTime(at), formatTime(at)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE upload_sessions SET status='FINALIZED',state_revision=state_revision+1,updated_at=? WHERE id=? AND status='OPEN'`, formatTime(at), sessionID)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return application.ErrUploadConflict
		}
		return nil
	})
}

func (r *Repositories) DeleteUploadSession(ctx context.Context, sessionID string, at time.Time) error {
	result, err := r.DB.ExecContext(ctx, `UPDATE upload_sessions SET status='DELETED',state_revision=state_revision+1,updated_at=? WHERE id=? AND status='OPEN'`, formatTime(at), sessionID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		var status string
		loadErr := r.DB.QueryRowContext(ctx, `SELECT status FROM upload_sessions WHERE id=?`, sessionID).Scan(&status)
		if errors.Is(loadErr, sql.ErrNoRows) {
			return loadErr
		}
		if loadErr != nil {
			return loadErr
		}
		if status == domain.UploadSessionDeleted {
			return nil
		}
		return fmt.Errorf("%w: cannot delete session in %s", application.ErrUploadConflict, status)
	}
	return nil
}
