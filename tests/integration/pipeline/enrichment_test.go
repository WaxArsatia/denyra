package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lrclib"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/internalapi"
)

func TestEnrichmentWritesSameBasenameLyricsAndKeepsArtworkAsEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/get" || request.URL.Query().Get("track_name") != "Track" || request.URL.Query().Get("duration") != "180" {
			t.Errorf("unexpected LRCLIB request: %s", request.URL.String())
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": 1, "instrumental": false, "plainLyrics": "plain", "syncedLyrics": "[00:01.00] line"})
	}))
	defer server.Close()
	root := t.TempDir()
	work := filepath.Join(root, "work", "candidate-1")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(work, "01 - Track.flac")
	if err := os.WriteFile(track, []byte("not touched by enrichment"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(track)
	service := application.EnrichmentService{
		WorkRoot: filepath.Join(root, "work"), EvidenceRoot: filepath.Join(root, "evidence"),
		Lyrics:  lrclib.Client{BaseURL: server.URL, UserAgent: "Denyra/0.0 (admin@example.invalid)", HTTP: server.Client()},
		Artwork: staticArtwork{bytes: []byte("artwork evidence")},
	}
	result, err := service.Enrich(context.Background(), "candidate-1", releaseMBID, []application.EnrichmentTrack{{RelativeFLAC: "01 - Track.flac", Query: domain.LyricsQuery{TrackName: "Track", ArtistName: "Artist", AlbumName: "Album", DurationMS: 180_000}}})
	if err != nil {
		t.Fatal(err)
	}
	lyrics, err := os.ReadFile(filepath.Join(work, "01 - Track.lrc"))
	if err != nil || string(lyrics) != "[00:01.00] line\n" {
		t.Fatalf("lyrics sidecar = %q, %v", lyrics, err)
	}
	after, _ := os.ReadFile(track)
	if string(after) != string(before) {
		t.Fatal("audio changed during enrichment")
	}
	if len(result.Warnings) != 0 || len(result.Items) != 2 || result.Items[1].Kind != "ARTWORK" || result.Items[1].SHA256 == "" {
		t.Fatalf("enrichment result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(work, "folder.jpg")); !os.IsNotExist(err) {
		t.Fatalf("pipeline wrote canonical final artwork into candidate: %v", err)
	}
}

func TestEnrichmentProviderFailuresAreNonBlockingWarningsOnly(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work", "candidate")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	service := application.EnrichmentService{WorkRoot: filepath.Join(root, "work"), EvidenceRoot: filepath.Join(root, "evidence"), Lyrics: failedLyrics{}, Artwork: staticArtwork{err: errors.New("artwork offline")}}
	result, err := service.Enrich(context.Background(), "candidate", releaseMBID, []application.EnrichmentTrack{{RelativeFLAC: "track.flac", Query: domain.LyricsQuery{TrackName: "Track", ArtistName: "Artist", AlbumName: "Album", DurationMS: 1}}})
	if err != nil || len(result.Warnings) != 2 {
		t.Fatalf("external enrichment failure blocked candidate: %+v, %v", result, err)
	}
	for _, warning := range result.Warnings {
		if warning.Kind != domain.WarningNonBlocking {
			t.Fatalf("enrichment warning affects quality: %+v", warning)
		}
	}
}

func TestQualityCallbackPersistsIntentBeforeIdempotentAuthenticatedEffect(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Header.Get("Authorization") != "Bearer internal-secret" || request.Header.Get("Idempotency-Key") != "quality-candidate-1" || request.Header.Get("X-Request-ID") != "request-1" {
			t.Errorf("callback boundary headers = %v", request.Header)
		}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()
	store := &intentStore{}
	client := internalapi.QualityClient{BaseURL: server.URL, Bearer: "internal-secret", HTTP: server.Client()}
	reporter := application.QualityReporter{Store: store, Callback: client, Now: func() time.Time { return time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC) }}
	payload := contracts.CandidateApproved{RequestID: "request-1", JobID: "job-1", CandidateID: "candidate-1", ConfigSnapshotID: "config-1", MusicBrainzReleaseID: releaseMBID, ApprovedAt: time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC), Quality: contracts.QualityVector{IdentityRank: 4}}
	if err := reporter.Report(context.Background(), payload, "quality-candidate-1"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !store.intentBeforeEffect || !store.completed {
		t.Fatalf("callback durability: calls=%d store=%+v", calls, store)
	}
}

type staticArtwork struct {
	bytes []byte
	err   error
}

func (a staticArtwork) Fetch(context.Context, string) ([]byte, domain.ProviderEvidence, error) {
	return a.bytes, domain.ProviderEvidence{Provider: "fixture"}, a.err
}

type failedLyrics struct{}

func (failedLyrics) Get(context.Context, domain.LyricsQuery) (domain.LyricsResult, domain.ProviderEvidence, error) {
	return domain.LyricsResult{}, domain.ProviderEvidence{Provider: "LRCLIB"}, errors.New("lyrics offline")
}

type intentStore struct {
	mu                 sync.Mutex
	intentBeforeEffect bool
	completed          bool
}

func (s *intentStore) PutQualityIntent(context.Context, string, string, string, []byte, time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intentBeforeEffect = true
	return nil
}

func (s *intentStore) CompleteQualityIntent(context.Context, string, int, string, time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.intentBeforeEffect {
		return errors.New("effect completed before intent")
	}
	s.completed = true
	return nil
}
