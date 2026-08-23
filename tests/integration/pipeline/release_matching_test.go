package pipeline_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/beets"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

const (
	releaseMBID      = "11111111-1111-1111-1111-111111111111"
	releaseGroupMBID = "22222222-2222-2222-2222-222222222222"
	recordingMBID    = "33333333-3333-3333-3333-333333333333"
	releaseTrackMBID = "44444444-4444-4444-4444-444444444444"
)

func TestReleaseMatchingRequiresOneExactReleaseAndRoutesManualReviewWithoutMutation(t *testing.T) {
	root := t.TempDir()
	work, quarantine := filepath.Join(root, "work"), filepath.Join(root, "quarantine")
	candidatePath := filepath.Join(work, "candidate-1")
	if err := os.MkdirAll(candidatePath, 0o750); err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(candidatePath, "01.flac")
	if err := os.WriteFile(trackPath, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := canonicalSingle(nil)
	service := application.MatchingService{DurationPolicy: pipelineDurationPolicy(), WorkRoot: work, QuarantineRoot: quarantine}
	decision, err := service.Evaluate("candidate-1", releaseMBID, release, []domain.CandidateTrack{{RelativePath: "01.flac", Disc: 1, Track: 1, DurationMS: 180_000}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.State != domain.StateReviewRequired || decision.FilePath != filepath.Join(quarantine, "candidate-1") {
		t.Fatalf("manual review routing = %+v", decision)
	}
	bytes, err := os.ReadFile(filepath.Join(decision.FilePath, "01.flac"))
	if err != nil || string(bytes) != "unchanged" {
		t.Fatalf("ambiguous candidate mutated: %q, %v", bytes, err)
	}
	returned, err := service.ApproveReview("candidate-1", releaseMBID, "operator selected exact release")
	if err != nil || returned != candidatePath {
		t.Fatalf("review return-to-work = %q, %v", returned, err)
	}
}

func TestReleaseMatchingRejectsTrackCountPositionAndDurationReleaseWide(t *testing.T) {
	reference := int64(180_000)
	for _, test := range []struct {
		name   string
		tracks []domain.CandidateTrack
		state  domain.State
	}{
		{"missing track", nil, domain.StateReviewRequired},
		{"wrong position", []domain.CandidateTrack{{RelativePath: "01.flac", Disc: 2, Track: 1, DurationMS: reference}}, domain.StateReviewRequired},
		{"duration reject", []domain.CandidateTrack{{RelativePath: "01.flac", Disc: 1, Track: 1, DurationMS: reference + 20_000}}, domain.StateRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			work, quarantine := filepath.Join(root, "work"), filepath.Join(root, "quarantine")
			if err := os.MkdirAll(filepath.Join(work, "candidate"), 0o750); err != nil {
				t.Fatal(err)
			}
			service := application.MatchingService{DurationPolicy: pipelineDurationPolicy(), WorkRoot: work, QuarantineRoot: quarantine}
			decision, err := service.Evaluate("candidate", releaseMBID, canonicalSingle(&reference), test.tracks)
			if err != nil || decision.State != test.state {
				t.Fatalf("decision = %+v, %v", decision, err)
			}
		})
	}
}

func TestMusicBrainzClientUsesBoundedOfficialReleaseLookupAndPersistsEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ws/2/release/"+releaseMBID || request.URL.Query().Get("fmt") != "json" || !strings.Contains(request.URL.Query().Get("inc"), "recordings") || request.Header.Get("User-Agent") == "" {
			t.Errorf("unexpected MusicBrainz request: %s headers=%v", request.URL.String(), request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
          "id":"` + releaseMBID + `","title":"Single","date":"2026-08","status":"Official",
          "release-group":{"id":"` + releaseGroupMBID + `"},
          "artist-credit":[{"name":"Artist","artist":{"id":"55555555-5555-5555-5555-555555555555","name":"Artist"}}],
          "media":[{"position":1,"track-count":1,"tracks":[{
            "id":"` + releaseTrackMBID + `","title":"Track","number":"1","position":1,"length":180000,
            "recording":{"id":"` + recordingMBID + `","title":"Track","length":180000,"isrcs":["USAAA2600001"]}
          }]}]
        }`))
	}))
	defer server.Close()
	client := &musicbrainz.Client{BaseURL: server.URL, UserAgent: "Denyra/0.0 (https://example.invalid/contact)", HTTP: server.Client(), ResponseLimit: 64 << 10, RateInterval: time.Nanosecond}
	release, evidence, err := client.LookupRelease(context.Background(), releaseMBID)
	if err != nil {
		t.Fatal(err)
	}
	if release.ReleaseMBID != releaseMBID || release.ReleaseGroupMBID != releaseGroupMBID || len(release.Tracks) != 1 || release.Tracks[0].RecordingMBID != recordingMBID || release.Tracks[0].ReleaseTrackMBID != releaseTrackMBID || evidence.ResponseSHA256 == "" || len(evidence.ResponseBody) == 0 {
		t.Fatalf("incomplete canonical release/evidence: release=%+v evidence=%+v", release, evidence)
	}
}

func TestMusicBrainzOperationalFailureIsRetryableNotAmbiguity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	client := &musicbrainz.Client{BaseURL: server.URL, UserAgent: "Denyra/0.0 (admin@example.invalid)", HTTP: server.Client(), RateInterval: time.Nanosecond}
	_, _, err := client.LookupRelease(context.Background(), releaseMBID)
	var retryable *musicbrainz.RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("provider outage became match result: %v", err)
	}
}

func TestReleaseMatchingBeetsAdvisorIsIsolatedAndCannotAccessLibrary(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "library")
	work := filepath.Join(root, "work", "candidate")
	if err := os.MkdirAll(library, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	advisor := beets.Advisor{Binary: "/bin/true", Version: "test", Timeout: time.Second, ForbiddenLibraryRoot: library}
	if _, err := advisor.Advise(context.Background(), library); err == nil {
		t.Fatal("beets advisory accepted final library path")
	}
	evidence, err := advisor.Advise(context.Background(), work)
	if err != nil || evidence.OutputSHA256 == "" || evidence.Confidence != "advisory" {
		t.Fatalf("isolated advisory evidence = %+v, %v", evidence, err)
	}
}

func canonicalSingle(duration *int64) domain.CanonicalRelease {
	return domain.CanonicalRelease{
		ReleaseMBID: releaseMBID, ReleaseGroupMBID: releaseGroupMBID, Title: "Single", Status: "Official",
		Tracks: []domain.CanonicalTrack{{ReleaseTrackMBID: releaseTrackMBID, RecordingMBID: recordingMBID, Title: "Track", Disc: 1, Track: 1, Number: "1", DurationMS: duration}},
	}
}

func pipelineDurationPolicy() domain.DurationPolicy {
	return domain.DurationPolicy{
		TrackAutoFloorMS: 5_000, TrackAutoPercentBasisPoints: 200,
		TrackManualFloorMS: 15_000, TrackManualPercentBasisPoints: 500,
		ReleaseAutoFloorMS: 30_000, ReleaseAutoPercentBasisPoints: 100,
		ReleaseManualFloorMS: 90_000, ReleaseManualPercentBasisPoints: 300,
	}
}
