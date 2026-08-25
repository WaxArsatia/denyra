package gateway_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	pipelineadapter "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/spotiflac"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/gateway/transport"
)

type runtimeClock struct{ now time.Time }

type recoveryPipelineClient struct {
	acceptErr  error
	accepted   []contracts.CandidateAccepted
	acceptKeys []string
}

func (client *recoveryPipelineClient) Register(context.Context, contracts.CandidateRegistered, string, string) (pipelineadapter.Response, error) {
	return pipelineadapter.Response{Status: http.StatusAccepted, Body: []byte(`{"status":"ok"}`)}, nil
}

func (client *recoveryPipelineClient) Accept(_ context.Context, request contracts.CandidateAccepted, _, key string) (pipelineadapter.Response, error) {
	client.accepted = append(client.accepted, request)
	client.acceptKeys = append(client.acceptKeys, key)
	if client.acceptErr != nil {
		return pipelineadapter.Response{}, client.acceptErr
	}
	return pipelineadapter.Response{Status: http.StatusAccepted, Body: []byte(`{"status":"ok"}`)}, nil
}

func (clock *runtimeClock) Now() time.Time { return clock.now }
func (clock *runtimeClock) Pause(_ context.Context, duration time.Duration) error {
	clock.now = clock.now.Add(duration)
	return nil
}

type zeroResultLidarr struct {
	jobID   int64
	group   string
	release string
}

func (client zeroResultLidarr) QueueWatermark(context.Context) (string, error)   { return "0", nil }
func (client zeroResultLidarr) HistoryWatermark(context.Context) (string, error) { return "0", nil }
func (client zeroResultLidarr) StartAlbumSearch(context.Context, int64) (lidarr.Command, []byte, error) {
	return lidarr.Command{ID: 77, Name: "AlbumSearch", Status: "queued"}, []byte(`{"albumIds":[42],"name":"AlbumSearch"}`), nil
}
func (client zeroResultLidarr) Command(context.Context, int64) (lidarr.Command, error) {
	return lidarr.Command{ID: 77, Name: "AlbumSearch", Status: "completed"}, nil
}
func (client zeroResultLidarr) QueueAfter(context.Context, string, int) ([]lidarr.QueueRecord, error) {
	return nil, nil
}
func (client zeroResultLidarr) HistoryAfter(context.Context, string, int) ([]lidarr.HistoryRecord, error) {
	return nil, nil
}
func (client zeroResultLidarr) Album(context.Context, int64) (lidarr.WantedAlbum, error) {
	return lidarr.WantedAlbum{AlbumID: client.jobID, ReleaseGroupMBID: client.group, SelectedReleaseMBID: client.release, Monitored: true}, nil
}

type zeroResultRunner struct{ now func() time.Time }

func (runner zeroResultRunner) Run(_ context.Context, request spotiflac.RunRequest) (spotiflac.RunResult, error) {
	completed := runner.now()
	providers := make([]spotiflac.ProviderExecution, 0, len(request.Providers))
	for _, provider := range request.Providers {
		providerCompleted := completed
		providers = append(providers, spotiflac.ProviderExecution{Provider: provider, Outcome: domain.OutcomeLegitimateNoResult, StartedAt: completed, CompletedAt: &providerCompleted})
	}
	return spotiflac.RunResult{EngineVersion: "test", Providers: providers, StartedAt: completed, CompletedAt: completed}, nil
}

func TestFallbackRetryAfterOverallDeadlineRecoveryRestartsPrimary(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	prepareFallbackState(t, store, job, now)
	if _, err := store.SetOverallDeadline(context.Background(), job.ID, 4, now.Add(-time.Second), now); err != nil {
		t.Fatal(err)
	}
	ready := now
	if _, err := store.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: 4, To: domain.StateFallbackRetryableError, Actor: "test", Reason: "process interrupted", NextRetryAt: &ready, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	runner := &fallbackRunner{}
	worker := application.AcquisitionWorker{
		Store: store,
		Admission: application.AdmissionController{Store: store, DataRoot: t.TempDir(), Capacity: func(string) (application.Capacity, error) {
			return application.Capacity{FreeBytes: 1 << 40, TotalBytes: 1 << 40}, nil
		}},
		Fallback: application.FallbackService{
			Runner: runner, Store: store,
			Policy:    domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
			Providers: []string{"ext:tidal-web"}, OutputRoot: "/data/downloads/spotiflac", OverallTimeout: 6 * time.Hour,
			Now: func() time.Time { return now },
		},
		Lease: time.Minute, MaxInlineTransitions: 4, Now: func() time.Time { return now },
	}
	if err := worker.ProcessOne(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	assertRestartedPrimaryCycle(t, store, job.ID, now.Add(time.Minute))
	if runner.calls != 0 {
		t.Fatalf("recovery started provider %d times", runner.calls)
	}
}

func TestAcquisitionRecoveryReconcilesLeaseInterruptedFallbackAndOrphan(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	prepareFallbackState(t, store, job, now)
	if err := store.AcquireLease(context.Background(), persistence.Lease{ResourceType: "acquisition-job", ResourceID: job.ID, OwnerID: "dead-worker", ConfigSnapshotID: job.ConfigSnapshotID, AcquiredAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute), ResourceRevision: 4}, false); err != nil {
		t.Fatal(err)
	}
	outputRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(outputRoot, "orphan-job"), 0o750); err != nil {
		t.Fatal(err)
	}
	recovery := application.GatewayRecovery{Store: store, RetryPolicy: domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour}, SpotiFLACRoot: outputRoot, Now: func() time.Time { return now }}
	report, err := recovery.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ExpiredLeases != 1 || report.OrphanDirectories != 1 {
		t.Fatalf("report=%+v", report)
	}
	stored, err := store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateFallbackRetryableError || stored.NextRetryAt == nil || !stored.NextRetryAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("recovered job=%+v", stored)
	}
	var leaseCount, recoveryEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&leaseCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM recovery_events`).Scan(&recoveryEvents); err != nil {
		t.Fatal(err)
	}
	if leaseCount != 0 || recoveryEvents != 2 {
		t.Fatalf("leases=%d recovery events=%d", leaseCount, recoveryEvents)
	}
}

func TestAcquisitionRecoveryUnknownAlbumSearchNeverBecomesZeroResult(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, 0, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	job, _ = store.Job(context.Background(), job.ID)
	event, err := store.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: domain.StatePrimarySearchRequested, Actor: "test", Reason: "failure after intent", OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetInitialSearchContext(context.Background(), job.ID, event.Revision, "0", "0", now, now.Add(10*time.Minute), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEffect(context.Background(), persistence.Effect{JobID: job.ID, Type: "ALBUM_SEARCH", IdempotencyKey: "unknown-search", RequestHash: strings.Repeat("a", 64), Request: []byte(`{"album":42}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	clock := &runtimeClock{now: now.Add(11 * time.Minute)}
	client := zeroResultLidarr{jobID: 42, group: releaseGroupMBID, release: releaseMBID}
	policy := domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour}
	reconciler := application.PrimaryReconciler{Lidarr: client, Store: store, Policy: policy, PageSize: 100, Now: clock.Now}
	recovery := application.GatewayRecovery{Store: store, Reconciler: reconciler, RetryPolicy: policy, SpotiFLACRoot: t.TempDir(), Now: clock.Now}
	if _, err := recovery.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StatePrimaryRetryableError || stored.State == domain.StateNoCandidate {
		t.Fatalf("unknown command state=%s", stored.State)
	}
}

func TestAcquisitionRecoveryAcknowledgesInterruptedSpotiFLACCancellation(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	request := []byte(`{"job_id":"` + job.ID + `","reason":"SUPERSEDED_CANCELLED"}`)
	requestSum := sha256.Sum256(request)
	if err := store.PutEffect(context.Background(), persistence.Effect{JobID: job.ID, Type: "SPOTIFLAC_CANCEL", IdempotencyKey: "spotiflac-cancel-" + job.ID, RequestHash: hex.EncodeToString(requestSum[:]), Request: request, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cancelled := 0
	recovery := application.GatewayRecovery{
		Store:         store,
		RetryPolicy:   domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		SpotiFLACRoot: t.TempDir(),
		ActiveProcess: func(string) bool { return true },
		CancelProcess: func(string) error { cancelled++; return nil },
		Now:           func() time.Time { return now },
	}
	if _, err := recovery.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	effect, err := store.Effect(context.Background(), "spotiflac-cancel-"+job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || effect.AcknowledgedAt == nil {
		t.Fatalf("cancelled=%d effect=%+v", cancelled, effect)
	}
	if _, err := recovery.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 {
		t.Fatalf("acknowledged cancellation replayed %d times", cancelled)
	}
}

func TestAcquisitionRecoveryReplaysExactPrimaryCompletionProvenance(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	candidateID, err := persistence.NewCandidateID()
	if err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(time.Minute)
	candidate := persistence.Candidate{ID: candidateID, JobID: job.ID, Source: "slskd", SourceLocator: "/data/downloads/slskd/lidarr/download-1", DownloadID: "download-1", CompletedAt: &completedAt, Provenance: []byte(`{"source":"slskd"}`), ProvenanceSHA256: strings.Repeat("a", 64), CreatedAt: completedAt}
	if err := store.InsertCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	lost := &recoveryPipelineClient{acceptErr: &pipelineadapter.RetryableError{Err: context.DeadlineExceeded}}
	handoff := application.CandidateHandoffService{Pipeline: lost, Store: store, ReplayAttempts: 1, Now: func() time.Time { return completedAt }}
	provenance := contracts.AcquisitionProvenance{Provider: "slskd", EngineVersion: "0.26.0", DownloadID: "download-1", ObservedStatus: "completed"}
	if err := handoff.AcceptCompleted(context.Background(), candidate, provenance); err == nil {
		t.Fatal("lost acknowledgement did not leave a retryable handoff")
	}

	replayed := &recoveryPipelineClient{}
	recovery := application.GatewayRecovery{Store: store, Handoff: application.CandidateHandoffService{Pipeline: replayed, Store: store, ReplayAttempts: 1, Now: func() time.Time { return completedAt }}, RetryPolicy: domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour}, SpotiFLACRoot: t.TempDir(), Now: func() time.Time { return completedAt }}
	report, err := recovery.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ReplayedHandoffs != 1 || len(replayed.accepted) != 1 || replayed.accepted[0].Provenance != provenance {
		t.Fatalf("report=%+v accepted=%+v", report, replayed.accepted)
	}
}

func TestAcquisitionRecoveryReplaysUnacknowledgedCompletionWithItsOriginalKey(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	job, err := store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidateID, err := persistence.NewCandidateID()
	if err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(time.Minute)
	candidate := persistence.Candidate{
		ID: candidateID, JobID: job.ID, Source: "slskd",
		SourceLocator: "/data/downloads/slskd/lidarr/download-1", DownloadID: "download-1",
		CompletedAt: &completedAt, Provenance: []byte(`{"source":"slskd"}`), ProvenanceSHA256: strings.Repeat("a", 64), CreatedAt: completedAt,
	}
	if err := store.InsertCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	request := contracts.CandidateAccepted{
		RequestID: "candidate-complete-" + candidateID, JobID: job.ID, CandidateID: candidateID,
		ConfigSnapshotID: job.ConfigSnapshotID, Source: contracts.SourceSlskd, Path: candidate.SourceLocator,
		CompletionAt: completedAt, MusicBrainzReleaseID: releaseMBID,
		Provenance: contracts.AcquisitionProvenance{Provider: "slskd", DownloadID: "download-1"},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	legacyKey := "candidate-complete-" + candidateID + "-job-5"
	if err := store.PutEffect(context.Background(), persistence.Effect{
		JobID: job.ID, Type: "PIPELINE_ACCEPT", IdempotencyKey: legacyKey,
		RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: completedAt,
	}); err != nil {
		t.Fatal(err)
	}

	pipeline := &recoveryPipelineClient{}
	recovery := application.GatewayRecovery{
		Store:         store,
		Handoff:       application.CandidateHandoffService{Pipeline: pipeline, Store: store, ReplayAttempts: 1, Now: func() time.Time { return completedAt }},
		RetryPolicy:   domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		SpotiFLACRoot: t.TempDir(), Now: func() time.Time { return completedAt },
	}
	if _, err := recovery.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(pipeline.acceptKeys) != 1 || pipeline.acceptKeys[0] != legacyKey {
		t.Fatalf("acceptance keys=%v, want original %q", pipeline.acceptKeys, legacyKey)
	}
}

func TestAcquisitionRecoveryDefersCompletionUntilReleaseSelectionIsBackfilled(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	candidateID, err := persistence.NewCandidateID()
	if err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(time.Minute)
	candidate := persistence.Candidate{
		ID: candidateID, JobID: job.ID, Source: "slskd",
		SourceLocator: "/data/downloads/slskd/lidarr/download-1", DownloadID: "download-1",
		CompletedAt: &completedAt, Provenance: []byte(`{"source":"slskd"}`), ProvenanceSHA256: strings.Repeat("a", 64), CreatedAt: completedAt,
	}
	if err := store.InsertCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	request := contracts.CandidateAccepted{
		RequestID: "candidate-complete-" + candidateID, JobID: job.ID, CandidateID: candidateID,
		ConfigSnapshotID: job.ConfigSnapshotID, Source: contracts.SourceSlskd, Path: candidate.SourceLocator,
		CompletionAt: completedAt, Provenance: contracts.AcquisitionProvenance{Provider: "slskd", DownloadID: "download-1"},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if err := store.PutEffect(context.Background(), persistence.Effect{
		JobID: job.ID, Type: "PIPELINE_ACCEPT", IdempotencyKey: request.RequestID,
		RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: completedAt,
	}); err != nil {
		t.Fatal(err)
	}

	pipeline := &recoveryPipelineClient{}
	recovery := application.GatewayRecovery{
		Store:         store,
		Handoff:       application.CandidateHandoffService{Pipeline: pipeline, Store: store, ReplayAttempts: 1, Now: func() time.Time { return completedAt }},
		RetryPolicy:   domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		SpotiFLACRoot: t.TempDir(), Now: func() time.Time { return completedAt },
	}
	report, err := recovery.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.ReplayedHandoffs != 0 || len(pipeline.accepted) != 0 {
		t.Fatalf("report=%+v accepted=%+v", report, pipeline.accepted)
	}
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, completedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	report, err = recovery.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile after backfill: %v", err)
	}
	if report.ReplayedHandoffs != 1 || len(pipeline.accepted) != 1 || pipeline.accepted[0].MusicBrainzReleaseID != releaseMBID {
		t.Fatalf("report=%+v accepted=%+v", report, pipeline.accepted)
	}
	var legacyStatus string
	if err := db.QueryRow(`SELECT status FROM external_effects WHERE idempotency_key=?`, request.RequestID).Scan(&legacyStatus); err != nil {
		t.Fatal(err)
	}
	if legacyStatus != "ACKNOWLEDGED" {
		t.Fatalf("legacy completion intent status=%q", legacyStatus)
	}
}

func TestAcquisitionRecoverySupersedesLegacyIntentAfterMatchingCompletionWasAcknowledged(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	job, err := store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidateID, err := persistence.NewCandidateID()
	if err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(time.Minute)
	candidate := persistence.Candidate{
		ID: candidateID, JobID: job.ID, Source: "slskd",
		SourceLocator: "/data/downloads/slskd/lidarr/download-1", DownloadID: "download-1",
		CompletedAt: &completedAt, Provenance: []byte(`{"source":"slskd"}`), ProvenanceSHA256: strings.Repeat("a", 64), CreatedAt: completedAt,
	}
	if err := store.InsertCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	request := contracts.CandidateAccepted{
		RequestID: "candidate-complete-" + candidateID, JobID: job.ID, CandidateID: candidateID,
		ConfigSnapshotID: job.ConfigSnapshotID, Source: contracts.SourceSlskd, Path: candidate.SourceLocator,
		CompletionAt: completedAt, MusicBrainzReleaseID: releaseMBID,
		Provenance: contracts.AcquisitionProvenance{Provider: "slskd", DownloadID: "download-1"},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	acknowledgedKey := "candidate-complete-" + candidateID + "-job-5"
	if err := store.PutEffect(context.Background(), persistence.Effect{
		JobID: job.ID, Type: "PIPELINE_ACCEPT", IdempotencyKey: acknowledgedKey,
		RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: completedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AcknowledgeEffect(context.Background(), acknowledgedKey, []byte(`{"status":"ok"}`), "ok", completedAt); err != nil {
		t.Fatal(err)
	}
	legacyKey := "candidate-complete-" + candidateID
	if err := store.PutEffect(context.Background(), persistence.Effect{
		JobID: job.ID, Type: "PIPELINE_ACCEPT", IdempotencyKey: legacyKey,
		RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: completedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	pipeline := &recoveryPipelineClient{}
	recovery := application.GatewayRecovery{
		Store:         store,
		Handoff:       application.CandidateHandoffService{Pipeline: pipeline, Store: store, ReplayAttempts: 1, Now: func() time.Time { return completedAt }},
		RetryPolicy:   domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		SpotiFLACRoot: t.TempDir(), Now: func() time.Time { return completedAt },
	}
	report, err := recovery.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(pipeline.accepted) != 0 {
		t.Fatalf("pipeline acceptance replayed: %+v", pipeline.accepted)
	}
	if report.ReplayedHandoffs != 0 {
		t.Fatalf("report=%+v", report)
	}
	effect, err := store.Effect(context.Background(), legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	if effect.AcknowledgedAt == nil || !bytes.Contains(effect.Response, []byte("SUPERSEDED_BY_ACKNOWLEDGED_COMPLETION")) {
		t.Fatalf("legacy effect=%+v", effect)
	}
}

func TestLidarrCancelDownloadUsesExactQueueItemAndSafeFlags(t *testing.T) {
	var deletedPath, deletedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/queue":
			if request.Header.Get("X-Api-Key") != "test-key" {
				t.Errorf("missing API key")
			}
			_, _ = writer.Write([]byte(`{"records":[{"id":91,"downloadId":"other"},{"id":92,"downloadId":"wanted-download"}],"totalRecords":2}`))
		case request.Method == http.MethodDelete:
			deletedPath, deletedQuery = request.URL.Path, request.URL.RawQuery
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20}
	if err := client.CancelDownload(context.Background(), "wanted-download"); err != nil {
		t.Fatal(err)
	}
	if deletedPath != "/api/v1/queue/92" || deletedQuery != "blocklist=false&removeFromClient=true" {
		t.Fatalf("DELETE %s?%s", deletedPath, deletedQuery)
	}
}

func TestAcquisitionAdmissionUsesMaximumStorageThresholdAndMaintenance(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	controller := application.AdmissionController{Store: store, DataRoot: "/data", MinimumFreeBytes: 20, MinimumFreePercent: 5, Capacity: func(string) (application.Capacity, error) {
		return application.Capacity{FreeBytes: 49, TotalBytes: 1000}, nil
	}}
	if err := controller.CheckNew(context.Background()); err == nil {
		t.Fatal("5 percent threshold was not enforced")
	}
	controller.Capacity = func(string) (application.Capacity, error) {
		return application.Capacity{FreeBytes: 50, TotalBytes: 1000}, nil
	}
	if err := controller.CheckNew(context.Background()); err != nil {
		t.Fatalf("boundary rejected: %v", err)
	}
	if err := store.SetMaintenance(context.Background(), true, "backup", now); err != nil {
		t.Fatal(err)
	}
	if err := controller.CheckNew(context.Background()); err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("maintenance admission=%v", err)
	}
}

func TestAcquisitionEvidenceRouteIsAuthenticatedReadOnlyAndEventFirst(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	notified := 0
	quality := transport.QualityCallbackAPI{BodyLimit: 4096, Bearer: []byte("secret")}
	handler, err := (transport.Routes{Quality: quality, Store: store, BodyLimit: 4096, Bearer: []byte("secret"), Notify: func() { notified++ }}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/internal/acquisitions/"+job.ID, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized evidence status=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/internal/acquisitions/"+job.ID, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"state_revision":0`)) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("evidence status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	event := httptest.NewRequest(http.MethodPost, "/internal/events/lidarr", strings.NewReader(`{"event_id":"lidarr-1"}`))
	event.Header.Set("Authorization", "Bearer secret")
	event.Header.Set("Content-Type", "application/json")
	eventResponse := httptest.NewRecorder()
	handler.ServeHTTP(eventResponse, event)
	if eventResponse.Code != http.StatusAccepted || notified != 1 {
		t.Fatalf("event status=%d notified=%d", eventResponse.Code, notified)
	}
}

func TestAcquisitionWorkerRunsPrimaryThenFallbackAndSchedulesLegitimateNoResult(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	clock := &runtimeClock{now: now}
	lidarrClient := zeroResultLidarr{jobID: 42, group: releaseGroupMBID, release: releaseMBID}
	policy := domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour}
	pause := clock.Pause
	primary := application.PrimarySearch{Lidarr: lidarrClient, Store: store, Policy: policy, CommandTimeout: 10 * time.Minute, PollInterval: 2 * time.Second, GraceWindow: time.Minute, Pause: pause, Now: clock.Now}
	reconciler := application.PrimaryReconciler{Lidarr: lidarrClient, Store: store, Policy: policy, PageSize: 100, PollInterval: 2 * time.Second, Pause: pause, Now: clock.Now}
	fallback := application.FallbackService{Runner: zeroResultRunner{now: clock.Now}, Store: store, Policy: policy, Providers: []string{"ext:tidal-web", "ext:qobuz-web", "ext:deezer"}, OutputRoot: t.TempDir(), OverallTimeout: 6 * time.Hour, Now: clock.Now}
	worker := application.AcquisitionWorker{Store: store, Admission: application.AdmissionController{Store: store, DataRoot: t.TempDir(), MinimumFreeBytes: 20, MinimumFreePercent: 5, Capacity: func(string) (application.Capacity, error) {
		return application.Capacity{FreeBytes: 100, TotalBytes: 1000}, nil
	}}, Primary: primary, Reconciler: reconciler, Fallback: fallback, Concurrency: 1, Lease: 15 * time.Minute, SafetyScan: 30 * time.Second, MaxInlineTransitions: 8, Now: clock.Now}
	if err := worker.ProcessOne(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateNoCandidate || stored.NextRetryAt == nil || !stored.NextRetryAt.Equal(clock.Now().Add(24*time.Hour)) {
		t.Fatalf("job=%+v clock=%s", stored, clock.Now())
	}
	var attempts, providerResults int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attempts WHERE job_id=?`, job.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_results WHERE job_id=?`, job.ID).Scan(&providerResults); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || providerResults != 3 {
		t.Fatalf("attempts=%d provider results=%d", attempts, providerResults)
	}
}
