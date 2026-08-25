package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestImportPersistsIntentBeforeOneManualImportAndVerifiesFinalLibrary(t *testing.T) {
	root := t.TempDir()
	workRoot, approvedRoot, libraryRoot := filepath.Join(root, "work"), filepath.Join(root, "approved"), filepath.Join(root, "library")
	candidatePath := filepath.Join(workRoot, "candidate-1")
	if err := os.MkdirAll(candidatePath, 0o750); err != nil {
		t.Fatal(err)
	}
	fixtures := generateFLACFixtures(t)
	copyFile(t, filepath.Join(fixtures, "mono-16-44100.flac"), filepath.Join(candidatePath, "01.flac"))
	if err := os.WriteFile(filepath.Join(candidatePath, "01.lrc"), []byte("[00:00.00] lyrics\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &importStore{}
	var postCount int
	var finalTrackPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "lidarr-key" {
			t.Errorf("missing Lidarr API key")
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/config/downloadclient":
			_, _ = writer.Write([]byte(validDownloadConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/config/mediamanagement":
			_, _ = writer.Write([]byte(validMediaManagementConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/config/naming":
			_, _ = writer.Write([]byte(validNamingConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/metadata":
			_, _ = writer.Write([]byte(validMetadataConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/manualimport":
			if request.URL.Query().Has("downloadId") {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			folder := request.URL.Query().Get("folder")
			_, _ = writer.Write([]byte(`[{
                  "id":7,"path":"` + filepath.ToSlash(filepath.Join(folder, "01.flac")) + `","name":"01.flac",
                  "artist":{"id":8},"album":{"id":10},"albumReleaseId":11,
                  "tracks":[{"id":12}],"quality":{},"releaseGroup":"release","indexerFlags":0,"rejections":[]
                }]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/album/10":
			_, _ = writer.Write([]byte(`{"id":10,"releases":[{"id":11,"foreignReleaseId":"` + releaseMBID + `"}]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/command":
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/history":
			_, _ = writer.Write([]byte(`{"page":1,"records":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/queue":
			_, _ = writer.Write([]byte(`{"page":1,"records":[]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/command":
			postCount++
			if !store.hasIntent() {
				t.Error("Manual Import effect occurred before durable intent")
			}
			var command struct {
				Name                 string            `json:"name"`
				Files                []json.RawMessage `json:"files"`
				ImportMode           string            `json:"importMode"`
				ReplaceExistingFiles bool              `json:"replaceExistingFiles"`
			}
			if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
				t.Error(err)
			}
			if command.Name != "ManualImport" || command.ImportMode != "move" || !command.ReplaceExistingFiles || len(command.Files) != 1 {
				t.Errorf("manual import command = %+v", command)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":42,"name":"ManualImport","status":"queued"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/trackfile":
			_ = json.NewEncoder(writer).Encode([]map[string]any{{"id": 20, "path": finalTrackPath, "trackIds": []int{12}}})
		default:
			http.Error(writer, "unexpected "+request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := lidarr.Client{BaseURL: server.URL, APIKey: "lidarr-key", HTTP: server.Client()}
	service := application.ImportService{
		WorkRoot: workRoot, ApprovedRoot: approvedRoot, Configuration: lidarr.ConfigVerifier{Client: client},
		Importer: lidarr.ManualImporter{Client: client}, Verifier: lidarr.LibraryVerifier{Client: client, LibraryRoot: libraryRoot}, Store: store,
		Now: func() time.Time { return time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC) },
	}
	submission, err := service.Submit(context.Background(), "candidate-1", releaseMBID, "download-1", application.ImportGatewayWinner, 11)
	if err != nil || !submission.ReconcileRequired || postCount != 1 || store.status != application.ImportSubmitted {
		t.Fatalf("import submission = %+v store=%+v posts=%d error=%v", submission, store, postCount, err)
	}
	if err := os.MkdirAll(filepath.Join(libraryRoot, "Artist", "Album"), 0o750); err != nil {
		t.Fatal(err)
	}
	finalTrackPath = filepath.Join(libraryRoot, "Artist", "Album", "01 - Track.flac")
	if err := os.Rename(filepath.Join(submission.ApprovedPath, "01.flac"), finalTrackPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(submission.ApprovedPath, "01.lrc"), strings.TrimSuffix(finalTrackPath, ".flac")+".lrc"); err != nil {
		t.Fatal(err)
	}
	verification, err := service.Reconcile(context.Background(), submission.Intent)
	if err != nil || !verification.Complete || store.status != application.ImportImported || postCount != 1 {
		t.Fatalf("import verification = %+v store=%+v posts=%d error=%v", verification, store, postCount, err)
	}
	if _, err := os.Stat(submission.ApprovedPath); !os.IsNotExist(err) {
		t.Fatalf("verified staging source retained: %v", err)
	}
}

func TestCatalogPreparationIsIdempotentThroughApplicationService(t *testing.T) {
	artistAdded, refreshed, monitored := false, false, false
	artistPosts, refreshPosts, albumPuts := 0, 0, 0
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests = append(requests, request.Method+" "+request.URL.String()+" "+string(body))
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/rootfolder":
			_, _ = writer.Write([]byte(validRootFolderConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/qualityprofile":
			_, _ = writer.Write([]byte(validQualityProfiles))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/metadataprofile":
			_, _ = writer.Write([]byte(validMetadataProfiles))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/artist/lookup":
			if request.URL.Query().Get("term") != "lidarr:11111111-1111-1111-1111-111111111111" {
				t.Errorf("artist lookup query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"foreignArtistId":"11111111-1111-1111-1111-111111111111","artistName":"Kaleb J"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/artist":
			if artistAdded {
				_, _ = writer.Write([]byte(`[{"id":70,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}]`))
			} else {
				_, _ = writer.Write([]byte(`[]`))
			}
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/artist":
			artistAdded = true
			artistPosts++
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":70,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/album":
			if !refreshed {
				_, _ = writer.Write([]byte(`[]`))
			} else {
				_, _ = fmt.Fprintf(writer, `[{"id":80,"artistId":70,"title":"OFF GUARD","monitored":%t,"releases":[{"id":90,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}]`, monitored)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/command":
			refreshed = true
			refreshPosts++
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":42,"name":"RefreshArtist","status":"queued"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/command/42":
			_, _ = writer.Write([]byte(`{"id":42,"status":"completed"}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/v1/album/80":
			monitored = true
			albumPuts++
			writer.WriteHeader(http.StatusAccepted)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/album/80":
			_, _ = writer.Write([]byte(`{"id":80,"artistId":70,"title":"OFF GUARD","monitored":true,"releases":[{"id":90,"foreignReleaseId":"22222222-2222-2222-2222-222222222222"}]}`))
		default:
			http.Error(writer, "unexpected "+request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	release := domain.CanonicalRelease{
		ReleaseMBID: "22222222-2222-2222-2222-222222222222",
		ArtistCredits: []domain.ArtistCredit{{
			Name: "Kaleb J", ArtistMBID: "11111111-1111-1111-1111-111111111111",
		}},
		Tracks: []domain.CanonicalTrack{{
			ReleaseTrackMBID: "33333333-3333-3333-3333-333333333333",
			RecordingMBID:    "44444444-4444-4444-4444-444444444444",
			Disc:             1, Track: 1,
		}},
	}
	client := lidarr.Client{BaseURL: server.URL, APIKey: "lidarr-key", HTTP: server.Client()}
	service := application.LidarrCatalogService{Catalog: lidarr.Catalog{
		Client: client, PollAttempts: 2, PollInterval: time.Nanosecond,
	}}
	first, err := service.EnsureRelease(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.EnsureRelease(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if first != (application.CatalogResult{ArtistID: 70, AlbumID: 80, AlbumReleaseID: 90}) || second != first {
		t.Fatalf("catalog results first=%+v second=%+v", first, second)
	}
	if artistPosts != 1 || refreshPosts != 1 || albumPuts != 1 {
		t.Fatalf("non-idempotent catalog effects artist=%d refresh=%d monitor=%d", artistPosts, refreshPosts, albumPuts)
	}
	joined := strings.Join(requests, "\n")
	for _, forbidden := range []string{"ArtistSearch", "AlbumSearch", `"searchForMissingAlbums":true`, "/data/library-unmanaged"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden catalog behavior %q in requests:\n%s", forbidden, joined)
		}
	}
}

func TestImportConfigurationDriftBlocksBeforeMoveOrIntent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/config/downloadclient":
			_, _ = writer.Write([]byte(`{"enableCompletedDownloadHandling":true}`))
		default:
			t.Fatal("import continued after configuration drift")
		}
	}))
	defer server.Close()
	root := t.TempDir()
	work := filepath.Join(root, "work", "candidate")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	store := &importStore{}
	client := lidarr.Client{BaseURL: server.URL, APIKey: "key", HTTP: server.Client()}
	service := application.ImportService{WorkRoot: filepath.Join(root, "work"), ApprovedRoot: filepath.Join(root, "approved"), Configuration: lidarr.ConfigVerifier{Client: client}, Importer: lidarr.ManualImporter{Client: client}, Store: store}
	if _, err := service.Submit(context.Background(), "candidate", releaseMBID, "", application.ImportManualApproved, 1); err == nil {
		t.Fatal("configuration drift accepted")
	}
	if _, err := os.Stat(work); err != nil || store.hasIntent() {
		t.Fatalf("configuration drift moved candidate or wrote intent: stat=%v store=%+v", err, store)
	}
}

func TestImportAmbiguousAcknowledgementEntersReconciliationWithoutRetry(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work", "candidate")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "01.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &importStore{}
	importer := &fakeAmbiguousImporter{}
	service := application.ImportService{WorkRoot: filepath.Join(root, "work"), ApprovedRoot: filepath.Join(root, "approved"), Configuration: passingConfig{}, Importer: importer, Store: store}
	submission, err := service.Submit(context.Background(), "candidate", releaseMBID, "", application.ImportManualApproved, 2)
	if err != nil || !submission.ReconcileRequired || importer.calls != 1 || store.status != application.ImportReconciling {
		t.Fatalf("ambiguous submit = %+v store=%+v calls=%d err=%v", submission, store, importer.calls, err)
	}
}

func TestImportRejectsCatalogAlbumReleaseMismatchBeforeIntentOrSubmit(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work", "candidate")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "01.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &importStore{}
	importer := &fakeAmbiguousImporter{}
	service := application.ImportService{
		WorkRoot: filepath.Join(root, "work"), ApprovedRoot: filepath.Join(root, "approved"),
		Configuration: passingConfig{}, Importer: importer, Store: store,
	}
	_, err := service.Submit(context.Background(), "candidate", releaseMBID, "", application.ImportManualApproved, 99)
	if err == nil || !strings.Contains(err.Error(), "catalog album release") {
		t.Fatalf("catalog mismatch error=%v", err)
	}
	if importer.calls != 0 || store.hasIntent() {
		t.Fatalf("catalog mismatch caused effects calls=%d store=%+v", importer.calls, store)
	}
}

type importStore struct {
	mu     sync.Mutex
	intent *domain.ImportIntent
	status string
}

func (s *importStore) PutImportIntent(_ context.Context, intent domain.ImportIntent, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent != nil && (s.intent.IdempotencyKey != intent.IdempotencyKey || s.intent.RequestHash != intent.RequestHash) {
		return errors.New("idempotency conflict")
	}
	s.intent = &intent
	return nil
}

func (s *importStore) MarkImportStatus(_ context.Context, _ string, status string, _ []byte, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	return nil
}

func (s *importStore) hasIntent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.intent != nil
}

type passingConfig struct{}

func (passingConfig) Verify(context.Context) error { return nil }

type fakeAmbiguousImporter struct{ calls int }

func (f *fakeAmbiguousImporter) Prepare(context.Context, string, string, string, int) (domain.LidarrImportPlan, error) {
	return domain.LidarrImportPlan{RequestBody: []byte(`[{"path":"candidate"}]`), AlbumID: 1, AlbumReleaseID: 2, TrackIDs: []int{3}}, nil
}

func (f *fakeAmbiguousImporter) Submit(context.Context, domain.LidarrImportPlan) error {
	f.calls++
	return fmt.Errorf("connection reset after request")
}
