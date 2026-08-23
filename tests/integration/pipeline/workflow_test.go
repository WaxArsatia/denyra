package pipeline_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lrclib"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestPipelineControlledWorkflowRunsAcceptedReleaseToArbitration(t *testing.T) {
	for _, tool := range []string{"ffprobe", "flac", "metaflac"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("deployment tool %s unavailable locally", tool)
		}
	}
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	root := t.TempDir()
	downloads, work, quarantine := filepath.Join(root, "downloads"), filepath.Join(root, "work"), filepath.Join(root, "quarantine")
	candidateID := "workflow-candidate"
	source := filepath.Join(downloads, candidateID)
	for _, path := range []string{source, work, quarantine} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	fixture := filepath.Join(generateFLACFixtures(t), "mono-16-44100.flac")
	track := filepath.Join(source, "01.flac")
	copyFile(t, fixture, track)
	if output, err := exec.Command("metaflac", "--set-tag=TRACKNUMBER=1", "--set-tag=DISCNUMBER=1", track).CombinedOutput(); err != nil {
		t.Fatalf("seed tags: %v %s", err, output)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/ws/2/release/"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"id":%q,"title":"Synthetic Album","date":"2026-08-24","status":"Official","release-group":{"id":"12345678-1234-1234-1234-123456789abc"},"artist-credit":[{"name":"Synthetic Artist","artist":{"id":"11111111-1111-1111-1111-111111111111","name":"Synthetic Artist"}}],"media":[{"position":1,"track-count":1,"tracks":[{"id":"22222222-2222-2222-2222-222222222222","title":"Synthetic Track","position":1,"number":"1","length":250,"artist-credit":[],"recording":{"id":"33333333-3333-3333-3333-333333333333","title":"Synthetic Track","length":250,"isrcs":[],"artist-credit":[{"name":"Synthetic Artist","artist":{"id":"11111111-1111-1111-1111-111111111111","name":"Synthetic Artist"}}]}}]}]}`, releaseMBID)
		case request.URL.Path == "/api/get":
			http.NotFound(writer, request)
		case request.URL.Path == "/internal/candidates/approved":
			if request.Header.Get("Authorization") != "Bearer internal-secret" {
				t.Errorf("quality callback missing bearer")
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"state":"ARBITRATING"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	handoff := application.HandoffService{Store: repository, LocalConfigSnapshotID: "config-1", SourceRoots: map[domain.Source]string{domain.SourceSlskd: downloads}, Now: func() time.Time { return now }}
	request := contracts.CandidateAccepted{RequestID: "completion-workflow", JobID: "job-workflow", CandidateID: candidateID, ConfigSnapshotID: "gateway-config", Source: contracts.SourceSlskd, Path: source, CompletionAt: now, MusicBrainzReleaseID: releaseMBID, Provenance: contracts.AcquisitionProvenance{Provider: "soulseek", EngineVersion: "0.26.0", OutputSHA256: strings.Repeat("a", 64)}}
	if _, _, err := handoff.Accept(context.Background(), "completion-workflow", request); err != nil {
		t.Fatal(err)
	}
	runner := media.Runner{MaxOutput: 1 << 20}
	ffprobe := media.FFProbe{Binary: "ffprobe", Version: "test", Timeout: time.Second, Runner: runner}
	flac := media.FLAC{Binary: "flac", Version: "test", Timeout: time.Second, Runner: runner}
	metaflac := media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: time.Second, Runner: runner}
	workflow := application.ControlledWorkflow{
		Store:       repository,
		Claim:       application.ClaimService{WorkRoot: work, LockRoot: filepath.Join(root, "locks"), StabilityInterval: time.Millisecond, Pause: func(context.Context, time.Duration) error { return nil }},
		Validator:   application.TechnicalValidator{Inspector: ffprobe, Integrity: flac, Heuristic: media.NoHeuristic{}, Checksum: media.SHA256},
		Lookup:      &musicbrainz.Client{BaseURL: server.URL, UserAgent: "Denyra/test (test@example.invalid)", HTTP: server.Client(), ResponseLimit: 1 << 20, RateInterval: time.Nanosecond},
		Matching:    application.MatchingService{DurationPolicy: durationPolicy(), WorkRoot: work, QuarantineRoot: quarantine},
		Enrichment:  application.EnrichmentService{WorkRoot: work, EvidenceRoot: filepath.Join(root, "evidence"), Lyrics: lrclib.Client{BaseURL: server.URL, UserAgent: "Denyra/test (test@example.invalid)", HTTP: server.Client(), ResponseLimit: 1 << 20}},
		Mutation:    application.MutationService{WorkRoot: work, QuarantineRoot: quarantine, Tags: metaflac, Integrity: flac, Checksum: media.SHA256},
		Quality:     application.QualityReporter{Store: repository, Callback: qualityHTTPClient{url: server.URL, client: server.Client()}},
		SourceRoots: map[domain.Source]string{domain.SourceSlskd: downloads}, MaxInlineTransitions: 12,
		Now: func() time.Time { now = now.Add(time.Millisecond); return now },
	}
	if err := workflow.Process(context.Background(), application.WorkItem{CandidateID: candidateID, Revision: 0, ConfigSnapshotID: "config-1", AdmissionRequired: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Candidate(context.Background(), candidateID)
	if err != nil || stored.State != domain.StateArbitrationPending {
		var reason string
		_ = db.QueryRow(`SELECT reason FROM state_transitions WHERE candidate_id=? ORDER BY revision DESC LIMIT 1`, candidateID).Scan(&reason)
		t.Fatalf("candidate=%+v err=%v reason=%s", stored, err, reason)
	}
	for table, minimum := range map[string]int{"candidate_files": 1, "validation_results": 2, "track_matches": 1, "metadata_snapshots": 2, "mutations": 1, "enrichments": 1} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE candidate_id=?", candidateID).Scan(&count); err != nil || count < minimum {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

type qualityHTTPClient struct {
	url    string
	client *http.Client
}

func (client qualityHTTPClient) ReportApproved(ctx context.Context, payload []byte, requestID, key string) (contracts.CallbackResult, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, client.url+"/internal/candidates/approved", strings.NewReader(string(payload)))
	request.Header.Set("Authorization", "Bearer internal-secret")
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("Idempotency-Key", key)
	response, err := client.client.Do(request)
	if err != nil {
		return contracts.CallbackResult{}, err
	}
	defer response.Body.Close()
	return contracts.CallbackResult{StatusCode: response.StatusCode, ResponseSHA256: strings.Repeat("b", 64)}, nil
}

func durationPolicy() domain.DurationPolicy {
	return domain.DurationPolicy{TrackAutoFloorMS: 5_000, TrackAutoPercentBasisPoints: 200, TrackManualFloorMS: 15_000, TrackManualPercentBasisPoints: 500, ReleaseAutoFloorMS: 30_000, ReleaseAutoPercentBasisPoints: 100, ReleaseManualFloorMS: 90_000, ReleaseManualPercentBasisPoints: 300}
}
