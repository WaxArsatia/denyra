package application_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestUnmanagedImportPublishesExactLayoutAndVerifiesNavidrome(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work, library := filepath.Join(root, "work"), filepath.Join(root, "library")
	seedUnmanagedWork(t, work, "candidate-1")
	store := &unmanagedStore{workflow: unmanagedWorkflow()}
	nav := &unmanagedNavidrome{visible: true}
	service := application.UnmanagedImportService{Store: store, Metadata: application.UnmanagedMetadataService{}, Mutation: unmanagedMutator{}, Navidrome: nav, WorkRoot: work, LibraryRoot: library, ScanPoll: time.Millisecond}
	release, err := service.Import(context.Background(), "candidate-1", unmanagedDecision())
	want := filepath.Join(library, "Kaleb J", "OFF GUARD (2024)")
	if err != nil || release.FinalPath != want || release.Status != "IMPORTED" {
		t.Fatalf("release=%+v err=%v", release, err)
	}
	for _, relative := range []string{"01 - Untukmu.flac", "01 - Untukmu.lrc"} {
		if _, err := os.Stat(filepath.Join(want, relative)); err != nil {
			t.Fatalf("published %s: %v", relative, err)
		}
	}
	if nav.scans != 1 || nav.identity.AlbumArtist != "Kaleb J" || nav.identity.Album != "OFF GUARD" || nav.identity.TrackCount != 1 {
		t.Fatalf("Navidrome calls=%+v", nav)
	}
}

func TestUnmanagedImportRecoversAfterPublishAndNeverOverwritesCollision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work, library := filepath.Join(root, "work"), filepath.Join(root, "library")
	seedUnmanagedWork(t, work, "candidate-1")
	store := &unmanagedStore{workflow: unmanagedWorkflow()}
	nav := &unmanagedNavidrome{visible: true}
	injected := true
	service := application.UnmanagedImportService{Store: store, Metadata: application.UnmanagedMetadataService{}, Mutation: unmanagedMutator{}, Navidrome: nav, WorkRoot: work, LibraryRoot: library, ScanPoll: time.Millisecond, Fault: func(point string) error {
		if point == "after_publish" && injected {
			injected = false
			return errors.New("crash after publish")
		}
		return nil
	}}
	if _, err := service.Import(context.Background(), "candidate-1", unmanagedDecision()); err == nil {
		t.Fatal("injected publish failure missing")
	}
	restarted := service
	restarted.Fault = nil
	if complete, err := restarted.Reconcile(context.Background(), "candidate-1"); err != nil || !complete {
		t.Fatalf("restart reconcile complete=%v err=%v", complete, err)
	}

	seedUnmanagedWork(t, work, "candidate-2")
	collision := filepath.Join(library, "Kaleb J", "OFF GUARD (2024)")
	keep := filepath.Join(collision, "keep.txt")
	if err := os.WriteFile(keep, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	store2 := &unmanagedStore{workflow: unmanagedWorkflow()}
	service.Store = store2
	_, err := service.Import(context.Background(), "candidate-2", unmanagedDecision())
	if !errors.Is(err, application.ErrUnmanagedReview) {
		t.Fatalf("collision error=%v", err)
	}
	body, readErr := os.ReadFile(keep)
	if readErr != nil || string(body) != "original" {
		t.Fatalf("collision target changed: %q err=%v", body, readErr)
	}
}

func TestUnmanagedImportRetriesNavidromeScanWithoutRepublishing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work, library := filepath.Join(root, "work"), filepath.Join(root, "library")
	seedUnmanagedWork(t, work, "candidate-1")
	store := &unmanagedStore{workflow: unmanagedWorkflow()}
	nav := &unmanagedNavidrome{visible: true, scanFailures: 1}
	service := application.UnmanagedImportService{Store: store, Metadata: application.UnmanagedMetadataService{}, Mutation: unmanagedMutator{}, Navidrome: nav, WorkRoot: work, LibraryRoot: library, ScanPoll: time.Millisecond}
	if _, err := service.Import(context.Background(), "candidate-1", unmanagedDecision()); err == nil {
		t.Fatal("injected Navidrome scan failure missing")
	}
	if _, err := service.Import(context.Background(), "candidate-1", unmanagedDecision()); err != nil {
		t.Fatalf("scan retry failed: %v", err)
	}
	if nav.scans != 2 {
		t.Fatalf("scan calls=%d", nav.scans)
	}
}

type unmanagedStore struct {
	workflow application.WorkflowContext
	release  domain.UnmanagedRelease
	intent   domain.UnmanagedImportIntent
}

func (s *unmanagedStore) Workflow(context.Context, string) (application.WorkflowContext, error) {
	return s.workflow, nil
}
func (s *unmanagedStore) PutUnmanagedRelease(_ context.Context, release domain.UnmanagedRelease, _ time.Time) error {
	if s.release.CandidateID != "" && s.release.Plan.RelativeRoot != release.Plan.RelativeRoot {
		return errors.New("release conflict")
	}
	s.release = release
	return nil
}
func (s *unmanagedStore) PutUnmanagedImportIntent(_ context.Context, intent domain.UnmanagedImportIntent, _ time.Time) error {
	if s.intent.ID != "" {
		return nil
	}
	s.intent = intent
	return nil
}
func (s *unmanagedStore) UnmanagedImportIntent(context.Context, string) (domain.UnmanagedImportIntent, error) {
	if s.intent.ID == "" {
		return domain.UnmanagedImportIntent{}, sql.ErrNoRows
	}
	return s.intent, nil
}
func (s *unmanagedStore) UpdateUnmanagedImport(_ context.Context, intent domain.UnmanagedImportIntent, release domain.UnmanagedRelease, _ time.Time) error {
	s.intent, s.release = intent, release
	return nil
}

type unmanagedMutator struct{}

func (unmanagedMutator) MutateUnmanagedRelease(context.Context, string, map[string]domain.TagSet) (application.MutationResult, error) {
	return application.MutationResult{Approved: true}, nil
}

type unmanagedNavidrome struct {
	visible      bool
	scans        int
	scanFailures int
	identity     navidrome.ReleaseIdentity
}

func (n *unmanagedNavidrome) EnsureLibraries(context.Context) (int, int, bool, error) {
	return 1, 2, false, nil
}
func (n *unmanagedNavidrome) StartScan(context.Context, ...int) error {
	n.scans++
	if n.scanFailures > 0 {
		n.scanFailures--
		return errors.New("scan unavailable")
	}
	return nil
}
func (n *unmanagedNavidrome) WaitScan(context.Context, time.Duration) error { return nil }
func (n *unmanagedNavidrome) ReleaseVisible(_ context.Context, _ int, identity navidrome.ReleaseIdentity) (bool, error) {
	n.identity = identity
	return n.visible, nil
}

func unmanagedDecision() domain.SubmissionDecision {
	metadata := domain.MetadataPlan{AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2024", DiscTotal: 1, TrackTotal: 1, Preserved: map[string]map[string][]string{"01.flac": {"TITLE": {"Old"}, "ARTIST": {"Kaleb J"}, "ALBUM": {"OFF GUARD"}, "ALBUMARTIST": {"Kaleb J"}, "TRACKNUMBER": {"1"}, "DISCNUMBER": {"1"}}}, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Untukmu", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1000}}}
	return domain.SubmissionDecision{Destination: domain.DestinationUnmanaged, Metadata: metadata}
}

func unmanagedWorkflow() application.WorkflowContext {
	decision := unmanagedDecision()
	return application.WorkflowContext{Technical: domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{{RelativePath: "01.flac", SHA256Before: "abc", Info: domain.TechnicalInfo{DurationMS: 1000}, OriginalComments: decision.Metadata.Preserved["01.flac"]}}}}
}

func seedUnmanagedWork(t *testing.T, work, candidate string) {
	t.Helper()
	directory := filepath.Join(work, candidate)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "01.flac"), []byte("flac"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "01.lrc"), []byte("lyrics"), 0o640); err != nil {
		t.Fatal(err)
	}
}
