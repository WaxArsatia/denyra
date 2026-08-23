package gateway_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	pipelineclient "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	gatewayapp "github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	gatewaypersistence "github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/gateway/transport"
	pipelineapp "github.com/waxarsatia/denyra/internal/pipeline/application"
	pipelinedomain "github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/internalapi"
	pipelinepersistence "github.com/waxarsatia/denyra/internal/pipeline/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

const arbitrationReleaseMBID = "abcdefab-1234-5678-9abc-abcdefabcdef"

type countingArbitrationClient struct {
	client      pipelineclient.Client
	mu          sync.Mutex
	winnerCalls map[string]int
}

type flakyArbitrationClient struct {
	mu              sync.Mutex
	failWinnerCalls int
	winnerCalls     map[string]int
}

type recordingTransferCanceller struct {
	mu         sync.Mutex
	candidates []gatewaypersistence.PendingCandidate
}

func (canceller *recordingTransferCanceller) CancelIncomplete(_ context.Context, candidate gatewaypersistence.PendingCandidate) ([]byte, error) {
	canceller.mu.Lock()
	defer canceller.mu.Unlock()
	canceller.candidates = append(canceller.candidates, candidate)
	return []byte(`{"status":"SUPERSEDED_CANCELLED"}`), nil
}

func (client *flakyArbitrationClient) Winner(_ context.Context, _ contracts.CandidateWinner, _ string, key string) (pipelineclient.Response, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.winnerCalls[key]++
	if client.failWinnerCalls > 0 {
		client.failWinnerCalls--
		return pipelineclient.Response{}, &pipelineclient.RetryableError{Err: context.DeadlineExceeded}
	}
	return pipelineclient.Response{Status: http.StatusOK, Body: []byte(`{"state":"IMPORT_READY"}`)}, nil
}

func (client *flakyArbitrationClient) Supersede(_ context.Context, _ contracts.CandidateSuperseded, _, _ string) (pipelineclient.Response, error) {
	return pipelineclient.Response{Status: http.StatusOK, Body: []byte(`{"state":"SUPERSEDED"}`)}, nil
}

func (client *countingArbitrationClient) Winner(ctx context.Context, request contracts.CandidateWinner, requestID, key string) (pipelineclient.Response, error) {
	client.mu.Lock()
	client.winnerCalls[key]++
	client.mu.Unlock()
	return client.client.Winner(ctx, request, requestID, key)
}

func (client *countingArbitrationClient) Supersede(ctx context.Context, request contracts.CandidateSuperseded, requestID, key string) (pipelineclient.Response, error) {
	return client.client.Supersede(ctx, request, requestID, key)
}

func TestArbitrationCallbacksLockOneWinnerAndReplayExactly(t *testing.T) {
	gatewayDB, gatewayStore, now := gatewayRepositories(t)
	defer gatewayDB.Close()
	job := arbitrationJob(t, gatewayStore, now)
	completion := now.Add(time.Minute)
	insertAcquisitionCandidate(t, gatewayStore, job.ID, "primary", "slskd", completion)
	insertAcquisitionCandidate(t, gatewayStore, job.ID, "fallback", "spotiflac", completion.Add(time.Second))

	pipelineDB, pipelineServer := arbitrationPipeline(t, now, job.ID, map[string]string{"primary": "slskd", "fallback": "spotiflac"})
	defer pipelineDB.Close()
	defer pipelineServer.Close()
	client := &countingArbitrationClient{client: pipelineclient.Client{BaseURL: pipelineServer.URL, Bearer: "internal-secret", HTTP: pipelineServer.Client(), ResponseLimit: 1 << 20}, winnerCalls: map[string]int{}}
	clock := now.Add(2 * time.Minute)
	service := gatewayapp.ArbitrationService{Store: gatewayStore, Pipeline: client, Window: 30 * time.Minute, ReplayAttempts: 2, Now: func() time.Time { return clock }}
	handler, err := (transport.QualityCallbackAPI{Service: service, BodyLimit: 1 << 20, Bearer: []byte("internal-secret")}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	quality := contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	primary := approvedCallback(job.ID, "primary", clock, quality, []contracts.Warning{{Class: contracts.NonBlockingWarning, Code: "LYRICS_MISSING", Message: "lyrics unavailable"}})
	fallbackQuality := quality
	fallbackQuality.QualityWarningCount = 1
	fallbackQuality.BitDepth = 32
	fallbackQuality.SampleRate = 384_000
	fallback := approvedCallback(job.ID, "fallback", clock.Add(time.Second), fallbackQuality, []contracts.Warning{{Class: contracts.QualityWarning, Code: "LOSSY_HEURISTIC", Message: "possible lossy source"}})

	firstStatus, firstBody := postApproval(t, server.Client(), server.URL, primary, "approval-primary")
	if firstStatus != http.StatusAccepted || !bytes.Contains(firstBody, []byte(`"state":"ARBITRATING"`)) {
		t.Fatalf("first approval status=%d body=%s", firstStatus, firstBody)
	}
	secondStatus, secondBody := postApproval(t, server.Client(), server.URL, fallback, "approval-fallback")
	if secondStatus != http.StatusOK || !bytes.Contains(secondBody, []byte(`"winner_candidate_id":"primary"`)) {
		t.Fatalf("second approval status=%d body=%s", secondStatus, secondBody)
	}
	replayStatus, replayBody := postApproval(t, server.Client(), server.URL, fallback, "approval-fallback")
	if replayStatus != secondStatus || !bytes.Equal(replayBody, secondBody) {
		t.Fatalf("duplicate callback changed response: status=%d body=%s", replayStatus, replayBody)
	}
	conflicting := fallback
	conflicting.Quality.SampleRate++
	conflictStatus, _ := postApproval(t, server.Client(), server.URL, conflicting, "approval-fallback")
	if conflictStatus != http.StatusConflict {
		t.Fatalf("idempotency conflict status=%d", conflictStatus)
	}

	stored, err := gatewayStore.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateHandedOff {
		t.Fatalf("gateway state=%s", stored.State)
	}
	arbitration, err := gatewayStore.Arbitration(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if arbitration.WinnerCandidateID != "primary" || arbitration.WinnerLockedAt == nil {
		t.Fatalf("arbitration=%+v", arbitration)
	}
	var primaryState, fallbackState string
	if err := pipelineDB.QueryRow(`SELECT state FROM candidates WHERE candidate_id='primary'`).Scan(&primaryState); err != nil {
		t.Fatal(err)
	}
	if err := pipelineDB.QueryRow(`SELECT state FROM candidates WHERE candidate_id='fallback'`).Scan(&fallbackState); err != nil {
		t.Fatal(err)
	}
	if primaryState != string(pipelinedomain.StateImportReady) || fallbackState != string(pipelinedomain.StateSuperseded) {
		t.Fatalf("pipeline primary=%s fallback=%s", primaryState, fallbackState)
	}
	client.mu.Lock()
	winnerCalls := client.winnerCalls["winner-primary"]
	client.mu.Unlock()
	if winnerCalls != 1 {
		t.Fatalf("winner authorization calls=%d", winnerCalls)
	}
	var approvalCount, winnerEffectCount int
	if err := gatewayDB.QueryRow(`SELECT COUNT(*) FROM candidate_approvals`).Scan(&approvalCount); err != nil {
		t.Fatal(err)
	}
	if err := gatewayDB.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE effect_type='PIPELINE_WINNER'`).Scan(&winnerEffectCount); err != nil {
		t.Fatal(err)
	}
	if approvalCount != 2 || winnerEffectCount != 1 {
		t.Fatalf("approvals=%d winner effects=%d", approvalCount, winnerEffectCount)
	}
}

func TestArbitrationCallbackBoundaryRejectsUnauthorizedAndOversize(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	service := gatewayapp.ArbitrationService{Store: store, Pipeline: &countingArbitrationClient{}, Window: 30 * time.Minute, ReplayAttempts: 1, Now: func() time.Time { return now }}
	handler, err := (transport.QualityCallbackAPI{Service: service, BodyLimit: 32, Bearer: []byte("internal-secret")}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/candidates/candidate/approved", strings.NewReader(strings.Repeat("x", 64)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/candidates/candidate/approved", strings.NewReader(strings.Repeat("x", 64)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer internal-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversize status=%d", response.Code)
	}
}

func TestArbitrationSimultaneousApprovalsRemainSingleWinner(t *testing.T) {
	gatewayDB, store, now := gatewayRepositories(t)
	defer gatewayDB.Close()
	job := arbitrationJob(t, store, now)
	completion := now.Add(time.Minute)
	insertAcquisitionCandidate(t, store, job.ID, "primary", "slskd", completion)
	insertAcquisitionCandidate(t, store, job.ID, "fallback", "spotiflac", completion)
	pipelineDB, pipelineServer := arbitrationPipeline(t, now, job.ID, map[string]string{"primary": "slskd", "fallback": "spotiflac"})
	defer pipelineDB.Close()
	defer pipelineServer.Close()
	client := &countingArbitrationClient{client: pipelineclient.Client{BaseURL: pipelineServer.URL, Bearer: "internal-secret", HTTP: pipelineServer.Client(), ResponseLimit: 1 << 20}, winnerCalls: map[string]int{}}
	clock := now.Add(2 * time.Minute)
	service := gatewayapp.ArbitrationService{Store: store, Pipeline: client, Window: 30 * time.Minute, ReplayAttempts: 2, Now: func() time.Time { return clock }}
	quality := contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	callbacks := []contracts.CandidateApproved{approvedCallback(job.ID, "primary", clock, quality, nil), approvedCallback(job.ID, "fallback", clock, quality, nil)}
	var wait sync.WaitGroup
	errorsFound := make(chan error, len(callbacks))
	for index, callback := range callbacks {
		wait.Add(1)
		go func(index int, callback contracts.CandidateApproved) {
			defer wait.Done()
			_, _, err := service.Approve(context.Background(), "simultaneous-"+callback.CandidateID, callback)
			if err != nil {
				errorsFound <- err
			}
		}(index, callback)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("simultaneous approval: %v", err)
	}
	arbitration, err := store.Arbitration(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if arbitration.WinnerCandidateID != "primary" || arbitration.StateRevision != 1 {
		t.Fatalf("arbitration=%+v", arbitration)
	}
	var winnerEffects int
	if err := gatewayDB.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE effect_type='PIPELINE_WINNER'`).Scan(&winnerEffects); err != nil {
		t.Fatal(err)
	}
	if winnerEffects != 1 {
		t.Fatalf("winner effects=%d", winnerEffects)
	}
}

func TestArbitrationRecoversCrashAfterWinnerLock(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := arbitrationJob(t, store, now)
	completion := now.Add(time.Minute)
	insertAcquisitionCandidate(t, store, job.ID, "primary", "slskd", completion)
	insertAcquisitionCandidate(t, store, job.ID, "fallback", "spotiflac", completion)
	clock := now.Add(2 * time.Minute)
	client := &flakyArbitrationClient{failWinnerCalls: 1, winnerCalls: map[string]int{}}
	service := gatewayapp.ArbitrationService{Store: store, Pipeline: client, Window: 30 * time.Minute, ReplayAttempts: 1, Now: func() time.Time { return clock }}
	quality := contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	if _, _, err := service.Approve(context.Background(), "crash-primary", approvedCallback(job.ID, "primary", clock, quality, nil)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Approve(context.Background(), "crash-fallback", approvedCallback(job.ID, "fallback", clock.Add(time.Second), quality, nil)); err == nil {
		t.Fatal("winner delivery failure was hidden")
	}
	stored, err := store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateWinnerLocked {
		t.Fatalf("state after simulated crash=%s", stored.State)
	}
	if err := service.Evaluate(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	stored, err = store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateHandedOff {
		t.Fatalf("recovered state=%s", stored.State)
	}
	var winnerEffects, acknowledged int
	if err := db.QueryRow(`SELECT COUNT(*),SUM(CASE WHEN status='ACKNOWLEDGED' THEN 1 ELSE 0 END) FROM external_effects WHERE effect_type='PIPELINE_WINNER'`).Scan(&winnerEffects, &acknowledged); err != nil {
		t.Fatal(err)
	}
	if winnerEffects != 1 || acknowledged != 1 {
		t.Fatalf("winner effects=%d acknowledged=%d", winnerEffects, acknowledged)
	}
	client.mu.Lock()
	calls := client.winnerCalls["winner-primary"]
	client.mu.Unlock()
	if calls != 2 {
		t.Fatalf("winner delivery attempts=%d", calls)
	}
}

func TestArbitrationDeadlineCancelsIncompleteTransferWithoutFabricatingCandidate(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := arbitrationJob(t, store, now)
	completion := now.Add(time.Minute)
	insertAcquisitionCandidate(t, store, job.ID, "primary", "slskd", completion)
	pendingEvidence := []byte(`{"download_id":"fallback-active"}`)
	pendingSum := sha256.Sum256(pendingEvidence)
	if err := store.InsertPendingCandidate(context.Background(), gatewaypersistence.PendingCandidate{ID: "fallback-active", JobID: job.ID, Source: "spotiflac", SourceLocator: "/data/downloads/spotiflac/job-1", DownloadID: "fallback-download", Provenance: pendingEvidence, ProvenanceSHA256: hex.EncodeToString(pendingSum[:]), CreatedAt: completion}); err != nil {
		t.Fatal(err)
	}
	clock := now.Add(2 * time.Minute)
	client := &flakyArbitrationClient{winnerCalls: map[string]int{}}
	canceller := &recordingTransferCanceller{}
	service := gatewayapp.ArbitrationService{Store: store, Pipeline: client, Canceller: canceller, Window: 30 * time.Minute, ReplayAttempts: 1, Now: func() time.Time { return clock }}
	quality := contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	if status, _, err := service.Approve(context.Background(), "deadline-primary", approvedCallback(job.ID, "primary", clock, quality, nil)); err != nil || status != http.StatusAccepted {
		t.Fatalf("first approval status=%d err=%v", status, err)
	}
	clock = clock.Add(30 * time.Minute)
	if err := service.Evaluate(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	canceller.mu.Lock()
	cancelled := append([]gatewaypersistence.PendingCandidate(nil), canceller.candidates...)
	canceller.mu.Unlock()
	if len(cancelled) != 1 || cancelled[0].ID != "fallback-active" {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	var completed, cancellationEffects int
	if err := db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE candidate_id='fallback-active'`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE effect_type='TRANSFER_CANCEL' AND status='ACKNOWLEDGED'`).Scan(&cancellationEffects); err != nil {
		t.Fatal(err)
	}
	if completed != 0 || cancellationEffects != 1 {
		t.Fatalf("fabricated completed=%d cancellation effects=%d", completed, cancellationEffects)
	}
}

func TestArbitrationOutOfOrderCallbackUsesEarliestApprovedTimestamp(t *testing.T) {
	db, store, now := gatewayRepositories(t)
	defer db.Close()
	job := arbitrationJob(t, store, now)
	completion := now.Add(time.Minute)
	insertAcquisitionCandidate(t, store, job.ID, "primary", "slskd", completion)
	insertAcquisitionCandidate(t, store, job.ID, "fallback", "spotiflac", completion)
	clock := now.Add(10 * time.Minute)
	client := &flakyArbitrationClient{winnerCalls: map[string]int{}}
	service := gatewayapp.ArbitrationService{Store: store, Pipeline: client, Window: 30 * time.Minute, ReplayAttempts: 1, Now: func() time.Time { return clock }}
	quality := contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	later := approvedCallback(job.ID, "fallback", now.Add(5*time.Minute), quality, nil)
	earlier := approvedCallback(job.ID, "primary", now.Add(2*time.Minute), quality, nil)
	if _, _, err := service.Approve(context.Background(), "out-of-order-later", later); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Approve(context.Background(), "out-of-order-earlier", earlier); err != nil {
		t.Fatal(err)
	}
	arbitration, err := store.Arbitration(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !arbitration.FirstApprovedAt.Equal(earlier.ApprovedAt) || !arbitration.Deadline.Equal(earlier.ApprovedAt.Add(30*time.Minute)) || arbitration.WinnerCandidateID != "primary" {
		t.Fatalf("arbitration=%+v", arbitration)
	}
}

func arbitrationJob(t *testing.T, store *gatewaypersistence.Repositories, now time.Time) domain.Job {
	t.Helper()
	job := createJob(t, store, now)
	if err := store.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, arbitrationReleaseMBID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	job, _ = store.Job(context.Background(), job.ID)
	for _, state := range []domain.State{domain.StatePrimarySearchRequested, domain.StatePrimarySearchRunning, domain.StatePrimaryReconciling, domain.StateFallbackRunning, domain.StateDualCandidate} {
		event, err := store.UpdateState(context.Background(), gatewaypersistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: state, Actor: "test", Reason: "prepare dual arbitration", OccurredAt: now.Add(time.Duration(job.Revision+2) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		job.Revision = event.Revision
		job.State = event.Next
	}
	return job
}

func insertAcquisitionCandidate(t *testing.T, store *gatewaypersistence.Repositories, jobID, candidateID, source string, completedAt time.Time) {
	t.Helper()
	manifest := []byte(`[{"path":"track.flac"}]`)
	provenance := []byte(`{"provider":"test"}`)
	sum := sha256.Sum256(provenance)
	if err := store.InsertCandidate(context.Background(), gatewaypersistence.Candidate{ID: candidateID, JobID: jobID, Source: source, SourceLocator: "/data/downloads/" + source + "/" + candidateID, CompletedAt: &completedAt, OutputSHA256: strings.Repeat("a", 64), OutputManifest: manifest, Provenance: provenance, ProvenanceSHA256: hex.EncodeToString(sum[:]), CreatedAt: completedAt}); err != nil {
		t.Fatal(err)
	}
}

func arbitrationPipeline(t *testing.T, now time.Time, jobID string, candidates map[string]string) (*sql.DB, *httptest.Server) {
	t.Helper()
	db, err := denysqlite.Open(context.Background(), filepath.Join(t.TempDir(), "pipeline.db"), denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := migrations.For("pipeline")
	if err := denysqlite.Migrate(context.Background(), db, steps, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('pipeline-config','{}','pipeline-config-hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for id, source := range candidates {
		if _, err := db.Exec(`INSERT INTO candidates(candidate_id,source,release_directory,config_snapshot_id,acquisition_evidence_id,gateway_job_id,state,state_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, source, "/data/processing/work/"+id, "pipeline-config", "evidence-"+id, jobID, pipelinedomain.StateArbitrationPending, 7, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	service := pipelineapp.HandoffService{Store: pipelinepersistence.New(db, func() time.Time { return now.Add(3 * time.Minute) }), LocalConfigSnapshotID: "pipeline-config", Now: func() time.Time { return now.Add(3 * time.Minute) }}
	handler, err := (internalapi.API{Service: service, BodyLimit: 1 << 20, Bearer: []byte("internal-secret")}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	return db, httptest.NewServer(handler)
}

func approvedCallback(jobID, candidateID string, approvedAt time.Time, quality contracts.QualityVector, warnings []contracts.Warning) contracts.CandidateApproved {
	return contracts.CandidateApproved{RequestID: "approval-" + candidateID, JobID: jobID, CandidateID: candidateID, ConfigSnapshotID: "pipeline-config", MusicBrainzReleaseID: arbitrationReleaseMBID, ApprovedAt: approvedAt, Quality: quality, Warnings: warnings, StateRevision: 7}
}

func postApproval(t *testing.T, client *http.Client, baseURL string, callback contracts.CandidateApproved, key string) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(callback)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/internal/candidates/"+callback.CandidateID+"/approved", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer internal-secret")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-Request-ID", callback.RequestID)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body.Bytes()
}
