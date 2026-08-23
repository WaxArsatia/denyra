package gateway_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	pipelineclient "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	gatewayapp "github.com/waxarsatia/denyra/internal/gateway/application"
	gatewaypersistence "github.com/waxarsatia/denyra/internal/gateway/persistence"
	pipelineapp "github.com/waxarsatia/denyra/internal/pipeline/application"
	pipelinedomain "github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/internalapi"
	pipelinepersistence "github.com/waxarsatia/denyra/internal/pipeline/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

type acknowledgementLossClient struct {
	client                       pipelineclient.Client
	loseRegister, loseAcceptance bool
}

func (client *acknowledgementLossClient) Register(ctx context.Context, request contracts.CandidateRegistered, requestID, key string) (pipelineclient.Response, error) {
	response, err := client.client.Register(ctx, request, requestID, key)
	if err == nil && client.loseRegister {
		client.loseRegister = false
		return pipelineclient.Response{}, &pipelineclient.RetryableError{Err: context.DeadlineExceeded}
	}
	return response, err
}

func (client *acknowledgementLossClient) Accept(ctx context.Context, request contracts.CandidateAccepted, requestID, key string) (pipelineclient.Response, error) {
	response, err := client.client.Accept(ctx, request, requestID, key)
	if err == nil && client.loseAcceptance {
		client.loseAcceptance = false
		return pipelineclient.Response{}, &pipelineclient.RetryableError{Err: context.DeadlineExceeded}
	}
	return response, err
}

func TestHandoffReplaysLostAcknowledgementWithoutSharingState(t *testing.T) {
	gatewayDB, gatewayStore, now := gatewayRepositories(t)
	defer gatewayDB.Close()
	job := createJob(t, gatewayStore, now)
	if err := gatewayStore.ReviseSelectedRelease(context.Background(), job.ID, job.Revision, releaseMBID, now); err != nil {
		t.Fatal(err)
	}
	job, _ = gatewayStore.Job(context.Background(), job.ID)

	pipelineDB, err := denysqlite.Open(context.Background(), filepath.Join(t.TempDir(), "pipeline.db"), denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer pipelineDB.Close()
	steps, err := migrations.For("pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if err := denysqlite.Migrate(context.Background(), pipelineDB, steps, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pipelineDB.Exec(`INSERT INTO config_snapshots(id,canonical_json,sha256,created_at) VALUES('pipeline-config','{}','pipeline-config-hash',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	pipelineStore := pipelinepersistence.New(pipelineDB, func() time.Time { return now })
	sourceRoot := t.TempDir()
	service := pipelineapp.HandoffService{Store: pipelineStore, LocalConfigSnapshotID: "pipeline-config", SourceRoots: map[pipelinedomain.Source]string{pipelinedomain.SourceSlskd: sourceRoot}, Now: func() time.Time { return now }}
	handler, err := (internalapi.API{Service: service, BodyLimit: 1 << 20, Bearer: []byte("internal-secret")}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &acknowledgementLossClient{client: pipelineclient.Client{BaseURL: server.URL, Bearer: "internal-secret", HTTP: server.Client(), ResponseLimit: 1 << 20}, loseRegister: true, loseAcceptance: true}
	handoff := gatewayapp.CandidateHandoffService{Pipeline: client, Store: gatewayStore, ReplayAttempts: 2, Now: func() time.Time { return now }}

	candidateID, err := gatewaypersistence.NewCandidateID()
	if err != nil {
		t.Fatal(err)
	}
	pendingEvidence := []byte(`{"download_id":"download-1"}`)
	pendingSum := sha256.Sum256(pendingEvidence)
	pending := gatewaypersistence.PendingCandidate{ID: candidateID, JobID: job.ID, Source: "slskd", SourceLocator: "download-1", DownloadID: "download-1", Provenance: pendingEvidence, ProvenanceSHA256: hex.EncodeToString(pendingSum[:]), CreatedAt: now}
	if err := gatewayStore.InsertPendingCandidate(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	if err := handoff.RegisterPending(context.Background(), pending); err != nil {
		t.Fatalf("RegisterPending: %v", err)
	}
	var pendingCount, candidateCount int
	if err := pipelineDB.QueryRow(`SELECT COUNT(*) FROM pending_acquisition_candidates WHERE candidate_id=?`, candidateID).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if err := pipelineDB.QueryRow(`SELECT COUNT(*) FROM candidates WHERE candidate_id=?`, candidateID).Scan(&candidateCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 1 || candidateCount != 0 {
		t.Fatalf("before completion pending=%d candidates=%d", pendingCount, candidateCount)
	}

	releaseDirectory := filepath.Join(sourceRoot, candidateID)
	if err := os.MkdirAll(releaseDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	completed := now.Add(time.Minute)
	outputChecksum := strings.Repeat("a", 64)
	candidate := gatewaypersistence.Candidate{ID: candidateID, JobID: job.ID, Source: "slskd", SourceLocator: releaseDirectory, DownloadID: "download-1", CompletedAt: &completed, OutputSHA256: outputChecksum, OutputManifest: []byte(`[{"path":"track.flac"}]`), Provenance: pendingEvidence, ProvenanceSHA256: hex.EncodeToString(pendingSum[:]), CreatedAt: completed}
	if err := gatewayStore.InsertCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	provenance := contracts.AcquisitionProvenance{Provider: "slskd", EngineVersion: "0.26.0", OutputSHA256: outputChecksum}
	if err := handoff.AcceptCompleted(context.Background(), candidate, provenance); err != nil {
		t.Fatalf("AcceptCompleted: %v", err)
	}
	if err := pipelineDB.QueryRow(`SELECT COUNT(*) FROM candidates WHERE candidate_id=?`, candidateID).Scan(&candidateCount); err != nil {
		t.Fatal(err)
	}
	var completionCount int
	if err := pipelineDB.QueryRow(`SELECT COUNT(*) FROM completion_evidence WHERE candidate_id=?`, candidateID).Scan(&completionCount); err != nil {
		t.Fatal(err)
	}
	if candidateCount != 1 || completionCount != 1 {
		t.Fatalf("after completion candidates=%d evidence=%d", candidateCount, completionCount)
	}
	var pipelineConfig string
	if err := pipelineDB.QueryRow(`SELECT config_snapshot_id FROM candidates WHERE candidate_id=?`, candidateID).Scan(&pipelineConfig); err != nil {
		t.Fatal(err)
	}
	if pipelineConfig != "pipeline-config" {
		t.Fatalf("pipeline candidate referenced gateway config: %q", pipelineConfig)
	}
	var acknowledged int
	if err := gatewayDB.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE job_id=? AND status='ACKNOWLEDGED' AND effect_type IN ('PIPELINE_REGISTER','PIPELINE_ACCEPT')`, job.ID).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if acknowledged != 2 {
		t.Fatalf("acknowledged handoff effects=%d", acknowledged)
	}
}
