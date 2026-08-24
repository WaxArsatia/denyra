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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lrclib"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
)

func TestCatalogImportAddsAbsentArtistBeforeMutationAndImportsOnce(t *testing.T) {
	scenario := newManagedCatalogScenario(t, false)
	if err := scenario.workflow.Process(context.Background(), application.WorkItem{CandidateID: scenario.candidateID, ConfigSnapshotID: "config-1"}); err != nil {
		t.Fatal(err)
	}
	candidate, err := scenario.store.Candidate(context.Background(), scenario.candidateID)
	if err != nil || candidate.State != domain.StateImported {
		t.Fatalf("candidate=%+v err=%v reason=%s", candidate, err, scenario.latestReason())
	}
	wantOrder := []string{"musicbrainz", "catalog", "enrichment", "mutation", "manual-prepare", "manual-submit", "reconcile"}
	if strings.Join(scenario.timeline, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("workflow order=%v want=%v", scenario.timeline, wantOrder)
	}
	if scenario.artistPosts != 1 || scenario.refreshPosts != 1 || scenario.manualPosts != 1 {
		t.Fatalf("effects artist=%d refresh=%d manual=%d", scenario.artistPosts, scenario.refreshPosts, scenario.manualPosts)
	}
}

func TestCatalogImportRetryStaysInWorkAndDoesNotDuplicateCatalogEffects(t *testing.T) {
	scenario := newManagedCatalogScenario(t, true)
	err := scenario.workflow.Process(context.Background(), application.WorkItem{CandidateID: scenario.candidateID, ConfigSnapshotID: "config-1"})
	var retryable *lidarr.RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("catalog refresh error=%v", err)
	}
	candidate, candidateErr := scenario.store.Candidate(context.Background(), scenario.candidateID)
	if candidateErr != nil || candidate.State != domain.StateReleaseMatching {
		t.Fatalf("candidate after retryable failure=%+v err=%v", candidate, candidateErr)
	}
	workflowState, stateErr := scenario.store.Workflow(context.Background(), scenario.candidateID)
	if stateErr != nil || workflowState.Release.ReleaseMBID != releaseMBID {
		t.Fatalf("durable exact identity=%+v err=%v", workflowState.Release, stateErr)
	}
	if _, statErr := os.Stat(filepath.Join(scenario.work, scenario.candidateID, "01.flac")); statErr != nil {
		t.Fatalf("work tree moved on catalog failure: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(scenario.approved, scenario.candidateID)); !os.IsNotExist(statErr) {
		t.Fatalf("approved staging created on catalog failure: %v", statErr)
	}
	if scenario.manualPosts != 0 {
		t.Fatalf("manual import submitted on catalog failure: %d", scenario.manualPosts)
	}

	if err := scenario.workflow.Process(context.Background(), application.WorkItem{CandidateID: scenario.candidateID, ConfigSnapshotID: "config-1"}); err != nil {
		t.Fatal(err)
	}
	candidate, candidateErr = scenario.store.Candidate(context.Background(), scenario.candidateID)
	if candidateErr != nil || candidate.State != domain.StateImported {
		t.Fatalf("candidate after retry=%+v err=%v", candidate, candidateErr)
	}
	if scenario.artistPosts != 1 || scenario.refreshPosts != 1 || scenario.manualPosts != 1 {
		t.Fatalf("duplicated effects after retry artist=%d refresh=%d manual=%d", scenario.artistPosts, scenario.refreshPosts, scenario.manualPosts)
	}
}

type managedCatalogScenario struct {
	workflow     application.ControlledWorkflow
	store        *persistence.Repositories
	candidateID  string
	work         string
	approved     string
	timeline     []string
	artistPosts  int
	refreshPosts int
	manualPosts  int
}

func (scenario *managedCatalogScenario) latestReason() string {
	var reason string
	_ = scenario.store.DB.QueryRow(`SELECT reason FROM state_transitions WHERE candidate_id=? ORDER BY revision DESC LIMIT 1`, scenario.candidateID).Scan(&reason)
	return reason
}

func newManagedCatalogScenario(t *testing.T, failCommandPollOnce bool) *managedCatalogScenario {
	t.Helper()
	for _, tool := range []string{"ffprobe", "flac", "metaflac"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("deployment tool %s unavailable locally", tool)
		}
	}
	db, repository, now := pipelineRepositories(t)
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	incoming, work := filepath.Join(root, "incoming"), filepath.Join(root, "work")
	approved, library, quarantine := filepath.Join(root, "approved"), filepath.Join(root, "library"), filepath.Join(root, "quarantine")
	candidateID := "managed-absent-catalog"
	_, decision, technical := seedRealUnmanagedCandidate(t, incoming, candidateID)
	source := filepath.Join(incoming, candidateID)
	if err := repository.DiscoverSubmission(context.Background(), candidateID, source, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,password_changed_at,created_at,updated_at) VALUES('admin-1','admin','hash',?,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	tree, err := denyrafs.Scan(source)
	if err != nil {
		t.Fatal(err)
	}
	decision.PreviewFingerprint = tree.Fingerprint
	decision.Destination = domain.DestinationManaged
	decision.ReleaseMBID = releaseMBID
	if err := (application.SubmissionService{Store: repository, IncomingRoot: incoming, Now: func() time.Time { return now.Add(time.Millisecond) }}).Submit(context.Background(), candidateID, 0, "admin-1", decision); err != nil {
		t.Fatal(err)
	}

	scenario := &managedCatalogScenario{store: repository, candidateID: candidateID, work: work, approved: approved}
	artistAdded, refreshAccepted, refreshVisible, monitored := false, false, false, false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		switch {
		case strings.HasPrefix(request.URL.Path, "/ws/2/release/"):
			scenario.timeline = append(scenario.timeline, "musicbrainz")
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"id":%q,"title":"OFF GUARD","date":"2026-08-24","status":"Official","release-group":{"id":"12345678-1234-1234-1234-123456789abc"},"artist-credit":[{"name":"Kaleb J","artist":{"id":"11111111-1111-1111-1111-111111111111","name":"Kaleb J"}}],"media":[{"position":1,"track-count":1,"tracks":[{"id":"22222222-2222-2222-2222-222222222222","title":"Synthetic Track","position":1,"number":"1","length":%d,"artist-credit":[],"recording":{"id":"33333333-3333-3333-3333-333333333333","title":"Synthetic Track","length":%d,"isrcs":[],"artist-credit":[{"name":"Kaleb J","artist":{"id":"11111111-1111-1111-1111-111111111111","name":"Kaleb J"}}]}}]}]}`, releaseMBID, technical.Files[0].Info.DurationMS, technical.Files[0].Info.DurationMS)
		case request.URL.Path == "/api/get":
			scenario.timeline = append(scenario.timeline, "enrichment")
			http.NotFound(writer, request)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/rootfolder":
			_, _ = writer.Write([]byte(validRootFolderConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/qualityprofile":
			_, _ = writer.Write([]byte(validQualityProfiles))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/metadataprofile":
			_, _ = writer.Write([]byte(validMetadataProfiles))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/artist/lookup":
			_, _ = writer.Write([]byte(`[{"foreignArtistId":"11111111-1111-1111-1111-111111111111","artistName":"Kaleb J"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/artist":
			if artistAdded {
				_, _ = writer.Write([]byte(`[{"id":70,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}]`))
			} else {
				_, _ = writer.Write([]byte(`[]`))
			}
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/artist":
			artistAdded = true
			scenario.artistPosts++
			scenario.timeline = append(scenario.timeline, "catalog")
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":70,"foreignArtistId":"11111111-1111-1111-1111-111111111111"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/album" && request.URL.Query().Has("artistId"):
			if refreshVisible {
				_, _ = fmt.Fprintf(writer, `[{"id":80,"artistId":70,"title":"OFF GUARD","monitored":%t,"releases":[{"id":90,"foreignReleaseId":%q}]}]`, monitored, releaseMBID)
			} else {
				_, _ = writer.Write([]byte(`[]`))
			}
		case request.Method == http.MethodPut && request.URL.Path == "/api/v1/album/80":
			monitored = true
			writer.WriteHeader(http.StatusAccepted)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/album/80":
			_, _ = fmt.Fprintf(writer, `{"id":80,"artistId":70,"title":"OFF GUARD","monitored":%t,"releases":[{"id":90,"foreignReleaseId":%q}]}`, monitored, releaseMBID)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/command/42":
			if failCommandPollOnce {
				failCommandPollOnce = false
				refreshVisible = true
				http.Error(writer, "temporary Lidarr failure", http.StatusServiceUnavailable)
				return
			}
			refreshVisible = true
			_, _ = writer.Write([]byte(`{"id":42,"status":"completed"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/command":
			var command struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(body, &command); err != nil {
				t.Errorf("decode Lidarr command: %v", err)
			}
			switch command.Name {
			case "RefreshArtist":
				if refreshAccepted {
					t.Error("duplicate RefreshArtist command")
				}
				refreshAccepted = true
				scenario.refreshPosts++
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":42,"name":"RefreshArtist","status":"queued"}`))
			case "ManualImport":
				scenario.manualPosts++
				scenario.timeline = append(scenario.timeline, "manual-submit")
				finalDirectory := filepath.Join(library, "Kaleb J", "OFF GUARD")
				if err := os.MkdirAll(finalDirectory, 0o750); err != nil {
					t.Error(err)
				}
				if err := os.Rename(filepath.Join(approved, candidateID, "01.flac"), filepath.Join(finalDirectory, "01 - Synthetic Track.flac")); err != nil {
					t.Error(err)
				}
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":43,"name":"ManualImport","status":"queued"}`))
			default:
				http.Error(writer, "unexpected command "+command.Name, http.StatusBadRequest)
			}
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/config/downloadclient":
			_, _ = writer.Write([]byte(validDownloadConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/config/mediamanagement":
			_, _ = writer.Write([]byte(validMediaManagementConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/config/naming":
			_, _ = writer.Write([]byte(validNamingConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/metadata":
			_, _ = writer.Write([]byte(validMetadataConfig))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/manualimport":
			approvedTrack := filepath.Join(approved, candidateID, "01.flac")
			output, commandErr := exec.Command("metaflac", "--show-tag=TITLE", approvedTrack).CombinedOutput()
			if commandErr != nil || strings.TrimSpace(string(output)) != "TITLE=Synthetic Track" {
				t.Errorf("mutation before Manual Import: output=%q err=%v", output, commandErr)
			}
			scenario.timeline = append(scenario.timeline, "mutation", "manual-prepare")
			_, _ = fmt.Fprintf(writer, `[{"id":7,"path":%q,"name":"01.flac","artist":{"id":70},"album":{"id":80},"albumReleaseId":90,"tracks":[{"id":12}],"quality":{},"releaseGroup":"release","indexerFlags":0,"rejections":[]}]`, filepath.ToSlash(approvedTrack))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/command":
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/history":
			_, _ = writer.Write([]byte(`{"page":1,"records":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/queue":
			_, _ = writer.Write([]byte(`{"page":1,"records":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/trackfile":
			scenario.timeline = append(scenario.timeline, "reconcile")
			_, _ = fmt.Fprintf(writer, `[{"id":20,"path":%q,"trackIds":[12]}]`, filepath.ToSlash(filepath.Join(library, "Kaleb J", "OFF GUARD", "01 - Synthetic Track.flac")))
		default:
			http.Error(writer, "unexpected "+request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	runner := media.Runner{MaxOutput: 1 << 20}
	ffprobe := media.FFProbe{Binary: "ffprobe", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	flac := media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	metaflac := media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	client := lidarr.Client{BaseURL: server.URL, APIKey: "lidarr-key", HTTP: server.Client()}
	scenario.workflow = application.ControlledWorkflow{
		Store:       repository,
		Claim:       application.ClaimService{WorkRoot: work, LockRoot: filepath.Join(root, "locks"), StabilityInterval: time.Millisecond, Pause: func(context.Context, time.Duration) error { return nil }},
		Validator:   application.TechnicalValidator{Inspector: ffprobe, Integrity: flac, Heuristic: media.NoHeuristic{}, Checksum: media.SHA256},
		Lookup:      &musicbrainz.Client{BaseURL: server.URL, UserAgent: "Denyra/test (test@example.invalid)", HTTP: server.Client(), ResponseLimit: 1 << 20, RateInterval: time.Nanosecond},
		Matching:    application.MatchingService{DurationPolicy: durationPolicy(), WorkRoot: work, QuarantineRoot: quarantine},
		Catalog:     application.LidarrCatalogService{Catalog: lidarr.Catalog{Client: client, PollAttempts: 2, PollInterval: time.Nanosecond}},
		Enrichment:  application.EnrichmentService{WorkRoot: work, EvidenceRoot: filepath.Join(root, "evidence"), Lyrics: lrclib.Client{BaseURL: server.URL, UserAgent: "Denyra/test (test@example.invalid)", HTTP: server.Client(), ResponseLimit: 1 << 20}},
		Mutation:    application.MutationService{WorkRoot: work, QuarantineRoot: quarantine, Tags: metaflac, Integrity: flac, Checksum: media.SHA256},
		Import:      application.ImportService{WorkRoot: work, ApprovedRoot: approved, Configuration: lidarr.ConfigVerifier{Client: client}, Importer: lidarr.ManualImporter{Client: client}, Verifier: lidarr.LibraryVerifier{Client: client, LibraryRoot: library}, Store: repository},
		SourceRoots: map[domain.Source]string{domain.SourceManual: incoming}, MaxInlineTransitions: 16,
		Now: func() time.Time { now = now.Add(time.Millisecond); return now },
	}
	return scenario
}
