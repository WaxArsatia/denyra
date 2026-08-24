package application_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestUploadServiceResumesAfterRestartAndFinalizesOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	uploading := filepath.Join(root, "uploading")
	incoming := filepath.Join(root, "manual")
	store := &memoryUploadStore{sessions: make(map[string]domain.UploadSession), finalizeFailures: 1}
	service := uploadService(store, uploading, incoming)

	session, err := service.Create(context.Background(), "admin-1", domain.UploadManifest{Files: []domain.UploadFileSpec{{RelativePath: "OFF GUARD/01 - Track.flac", SizeBytes: 12, MediaType: "audio/flac"}}})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.SubmissionID == "" || session.Files[0].ID == "" || session.Files[0].Status != domain.UploadEntryPending {
		t.Fatalf("incomplete created session: %+v", session)
	}
	if _, err := service.PutFile(context.Background(), "admin-1", session.ID, session.Files[0].ID, strings.NewReader("partial")); err == nil {
		t.Fatal("short upload accepted")
	}

	restarted := uploadService(store, uploading, incoming)
	if _, err := restarted.PutFile(context.Background(), "admin-1", session.ID, session.Files[0].ID, strings.NewReader("hello world!")); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Finalize(context.Background(), "admin-1", session.ID); err == nil {
		t.Fatal("injected persistence failure not returned")
	}
	if _, err := os.Stat(filepath.Join(incoming, session.SubmissionID, "OFF GUARD", "01 - Track.flac")); err != nil {
		t.Fatalf("filesystem move did not complete before injected failure: %v", err)
	}
	finalized, err := restarted.Finalize(context.Background(), "admin-1", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := restarted.Finalize(context.Background(), "admin-1", session.ID)
	if err != nil || again.SubmissionID != finalized.SubmissionID {
		t.Fatalf("idempotent finalize=%+v err=%v", again, err)
	}
	if store.finalizeCommits != 1 {
		t.Fatalf("submission commits=%d, want 1", store.finalizeCommits)
	}
}

func TestUploadServiceRejectsAnotherActor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := &memoryUploadStore{sessions: make(map[string]domain.UploadSession)}
	service := uploadService(store, filepath.Join(root, "uploading"), filepath.Join(root, "manual"))
	session, err := service.Create(context.Background(), "admin-1", domain.UploadManifest{Files: []domain.UploadFileSpec{{RelativePath: "track.flac", SizeBytes: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutFile(context.Background(), "admin-2", session.ID, session.Files[0].ID, strings.NewReader("data")); !errors.Is(err, application.ErrUploadForbidden) {
		t.Fatalf("cross-actor upload error=%v", err)
	}
}

func uploadService(store application.UploadStore, uploading, incoming string) application.UploadService {
	return application.UploadService{
		Store: store, Writer: denyrafs.UploadWriter{Root: uploading}, UploadingRoot: uploading, IncomingRoot: incoming,
		Policy: config.UploadConfig{MaxFileBytes: 20, MaxSessionBytes: 30, MaxEntries: 3, BrowserConcurrency: 3, ImageMaxBytes: 10, ImageMaxPixels: 10},
		Now:    func() time.Time { return time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC) },
	}
}

type memoryUploadStore struct {
	sessions         map[string]domain.UploadSession
	finalizeFailures int
	finalizeCommits  int
}

func (s *memoryUploadStore) CreateUploadSession(_ context.Context, session domain.UploadSession) error {
	s.sessions[session.ID] = session
	return nil
}

func (s *memoryUploadStore) UploadSession(_ context.Context, id string) (domain.UploadSession, error) {
	session, exists := s.sessions[id]
	if !exists {
		return domain.UploadSession{}, os.ErrNotExist
	}
	return session, nil
}

func (s *memoryUploadStore) UploadSessions(_ context.Context, actor string) ([]domain.UploadSession, error) {
	var result []domain.UploadSession
	for _, session := range s.sessions {
		if session.Actor == actor {
			result = append(result, session)
		}
	}
	return result, nil
}

func (s *memoryUploadStore) CompleteUploadEntry(_ context.Context, sessionID, entryID string, at time.Time) error {
	session := s.sessions[sessionID]
	for index := range session.Files {
		if session.Files[index].ID == entryID && session.Files[index].Status != domain.UploadEntryComplete {
			session.Files[index].Status = domain.UploadEntryComplete
			session.Revision++
			session.UpdatedAt = at
		}
	}
	s.sessions[sessionID] = session
	return nil
}

func (s *memoryUploadStore) FinalizeUploadSession(_ context.Context, sessionID, sourcePath string, provenance []byte, at time.Time) error {
	if sourcePath == "" || len(provenance) == 0 {
		return errors.New("missing finalization evidence")
	}
	if s.finalizeFailures > 0 {
		s.finalizeFailures--
		return errors.New("injected commit failure")
	}
	session := s.sessions[sessionID]
	if session.Status != domain.UploadSessionFinalized {
		session.Status = domain.UploadSessionFinalized
		session.Revision++
		session.UpdatedAt = at
		s.sessions[sessionID] = session
		s.finalizeCommits++
	}
	return nil
}

func (s *memoryUploadStore) DeleteUploadSession(_ context.Context, sessionID string, at time.Time) error {
	session := s.sessions[sessionID]
	session.Status = domain.UploadSessionDeleted
	session.Revision++
	session.UpdatedAt = at
	s.sessions[sessionID] = session
	return nil
}
