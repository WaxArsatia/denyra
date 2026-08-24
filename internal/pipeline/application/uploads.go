package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

var (
	ErrUploadForbidden = errors.New("upload session belongs to another actor")
	ErrUploadConflict  = errors.New("upload session state conflict")
)

type UploadStore interface {
	CreateUploadSession(context.Context, domain.UploadSession) error
	UploadSession(context.Context, string) (domain.UploadSession, error)
	UploadSessions(context.Context, string) ([]domain.UploadSession, error)
	CompleteUploadEntry(context.Context, string, string, time.Time) error
	FinalizeUploadSession(context.Context, string, string, []byte, time.Time) error
	DeleteUploadSession(context.Context, string, time.Time) error
}

type UploadWriter interface {
	CreateSession(string) error
	PutFile(context.Context, string, domain.UploadFileSpec, io.Reader) (int64, error)
	VerifySession(string, []domain.UploadFileSpec) error
	FinalizeSession(string, string, string, []domain.UploadFileSpec) (string, error)
	DeleteSession(string) error
}

type UploadService struct {
	Store                       UploadStore
	Writer                      UploadWriter
	UploadingRoot, IncomingRoot string
	Policy                      config.UploadConfig
	Now                         func() time.Time
}

func (s UploadService) Create(ctx context.Context, actor string, manifest domain.UploadManifest) (domain.UploadSession, error) {
	if err := s.validateConfiguration(); err != nil {
		return domain.UploadSession{}, err
	}
	if strings.TrimSpace(actor) == "" {
		return domain.UploadSession{}, fmt.Errorf("upload actor is required")
	}
	if err := domain.ValidateUploadManifest(manifest, domain.UploadLimits{MaxFileBytes: s.Policy.MaxFileBytes, MaxSessionBytes: s.Policy.MaxSessionBytes, MaxEntries: s.Policy.MaxEntries}); err != nil {
		return domain.UploadSession{}, err
	}
	sessionID, err := ids.NewToken(16)
	if err != nil {
		return domain.UploadSession{}, err
	}
	submissionID, err := ids.NewToken(16)
	if err != nil {
		return domain.UploadSession{}, err
	}
	files := make([]domain.UploadFileSpec, len(manifest.Files))
	for index, file := range manifest.Files {
		entryID, err := ids.NewToken(16)
		if err != nil {
			return domain.UploadSession{}, err
		}
		file.ID = entryID
		file.Status = domain.UploadEntryPending
		files[index] = file
	}
	now := s.now()
	session := domain.UploadSession{ID: sessionID, SubmissionID: submissionID, Actor: actor, Status: domain.UploadSessionOpen, Files: files, CreatedAt: now, UpdatedAt: now}
	if err := s.Writer.CreateSession(session.ID); err != nil {
		return domain.UploadSession{}, err
	}
	if err := s.Store.CreateUploadSession(ctx, session); err != nil {
		_ = s.Writer.DeleteSession(session.ID)
		return domain.UploadSession{}, err
	}
	return session, nil
}

func (s UploadService) PutFile(ctx context.Context, actor, sessionID, entryID string, reader io.Reader) (domain.UploadSession, error) {
	session, err := s.ownedSession(ctx, actor, sessionID)
	if err != nil {
		return domain.UploadSession{}, err
	}
	if session.Status != domain.UploadSessionOpen {
		return domain.UploadSession{}, ErrUploadConflict
	}
	entry, found := uploadEntry(session.Files, entryID)
	if !found {
		return domain.UploadSession{}, fmt.Errorf("upload entry not found")
	}
	if _, err := s.Writer.PutFile(ctx, session.ID, entry, reader); err != nil {
		return domain.UploadSession{}, err
	}
	if err := s.Store.CompleteUploadEntry(ctx, session.ID, entry.ID, s.now()); err != nil {
		return domain.UploadSession{}, err
	}
	return s.Store.UploadSession(ctx, session.ID)
}

func (s UploadService) Finalize(ctx context.Context, actor, sessionID string) (domain.UploadSession, error) {
	session, err := s.ownedSession(ctx, actor, sessionID)
	if err != nil {
		return domain.UploadSession{}, err
	}
	if session.Status == domain.UploadSessionFinalized {
		return session, nil
	}
	if session.Status != domain.UploadSessionOpen || !allUploadEntriesComplete(session.Files) {
		return domain.UploadSession{}, ErrUploadConflict
	}
	provenance, err := json.Marshal(struct {
		Ingress string                  `json:"ingress"`
		Actor   string                  `json:"actor"`
		Files   []domain.UploadFileSpec `json:"files"`
	}{Ingress: "browser", Actor: actor, Files: session.Files})
	if err != nil {
		return domain.UploadSession{}, err
	}
	finalPath, err := s.Writer.FinalizeSession(session.ID, s.IncomingRoot, session.SubmissionID, session.Files)
	if err != nil {
		return domain.UploadSession{}, err
	}
	if err := s.Store.FinalizeUploadSession(ctx, session.ID, finalPath, provenance, s.now()); err != nil {
		return domain.UploadSession{}, err
	}
	return s.Store.UploadSession(ctx, session.ID)
}

func (s UploadService) Delete(ctx context.Context, actor, sessionID string) error {
	session, err := s.ownedSession(ctx, actor, sessionID)
	if err != nil {
		return err
	}
	if session.Status != domain.UploadSessionOpen {
		return ErrUploadConflict
	}
	if err := s.Writer.DeleteSession(session.ID); err != nil {
		return err
	}
	return s.Store.DeleteUploadSession(ctx, session.ID, s.now())
}

func (s UploadService) Session(ctx context.Context, actor, sessionID string) (domain.UploadSession, error) {
	return s.ownedSession(ctx, actor, sessionID)
}

func (s UploadService) Sessions(ctx context.Context, actor string) ([]domain.UploadSession, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, ErrUploadForbidden
	}
	return s.Store.UploadSessions(ctx, actor)
}

func (s UploadService) ownedSession(ctx context.Context, actor, sessionID string) (domain.UploadSession, error) {
	if err := s.validateConfiguration(); err != nil {
		return domain.UploadSession{}, err
	}
	session, err := s.Store.UploadSession(ctx, sessionID)
	if err != nil {
		return domain.UploadSession{}, err
	}
	if actor == "" || session.Actor != actor {
		return domain.UploadSession{}, ErrUploadForbidden
	}
	return session, nil
}

func (s UploadService) validateConfiguration() error {
	if s.Store == nil || s.Writer == nil || !filepath.IsAbs(s.UploadingRoot) || filepath.Clean(s.UploadingRoot) != s.UploadingRoot || !filepath.IsAbs(s.IncomingRoot) || filepath.Clean(s.IncomingRoot) != s.IncomingRoot {
		return fmt.Errorf("upload service is not configured")
	}
	return nil
}

func (s UploadService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func uploadEntry(files []domain.UploadFileSpec, id string) (domain.UploadFileSpec, bool) {
	for _, file := range files {
		if file.ID == id {
			return file, true
		}
	}
	return domain.UploadFileSpec{}, false
}

func allUploadEntriesComplete(files []domain.UploadFileSpec) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if file.Status != domain.UploadEntryComplete {
			return false
		}
	}
	return true
}
