package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/internalapi"
)

func TestPipelineInternalAPIAuthenticatesLimitsAndIdempotentlyAcceptsHandoff(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	service := application.HandoffService{Store: repository, LocalConfigSnapshotID: "config-1", SourceRoots: map[domain.Source]string{domain.SourceSlskd: "/data/downloads/slskd", domain.SourceSpotiFLAC: "/data/downloads/spotiflac"}, Now: func() time.Time { return now }}
	handler, err := (internalapi.API{Service: service, BodyLimit: 4096, Bearer: []byte("secret")}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.CandidateAccepted{RequestID: "completion-1", JobID: "job-1", CandidateID: "candidate-api-1", ConfigSnapshotID: "gateway-config", Source: contracts.SourceSlskd, Path: "/data/downloads/slskd/release-1", CompletionAt: now, MusicBrainzReleaseID: "abcdefab-1234-5678-9abc-abcdefabcdef", Provenance: contracts.AcquisitionProvenance{Provider: "soulseek", OutputSHA256: strings.Repeat("a", 64)}}
	payload, _ := json.Marshal(request)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/internal/candidates", bytes.NewReader(payload)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", unauthorized.Code)
	}
	call := func(key string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/candidates", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-Request-ID", "request-http-1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := call("handoff-key", payload)
	second := call("handoff-key", payload)
	if first.Code != http.StatusAccepted || second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("idempotent responses first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	request.JobID = "different"
	conflicting, _ := json.Marshal(request)
	conflict := call("handoff-key", conflicting)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	var candidates, evidence int
	if err := db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE candidate_id='candidate-api-1'`).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM completion_evidence WHERE candidate_id='candidate-api-1'`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if candidates != 1 || evidence != 1 {
		t.Fatalf("candidate/evidence=%d/%d", candidates, evidence)
	}
}

func TestPipelineInternalWinnerUsesOptimisticRevision(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='ARBITRATION_PENDING',state_revision=8 WHERE candidate_id=?`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	service := application.HandoffService{Store: repository, LocalConfigSnapshotID: "config-1", Now: func() time.Time { return now }}
	handler, _ := (internalapi.API{Service: service, BodyLimit: 4096, Bearer: []byte("secret")}).Handler()
	directive := contracts.CandidateWinner{RequestID: "winner-1", JobID: "job-1", CandidateID: candidate.ID, ConfigSnapshotID: "config-1", StateRevision: 8, WinnerLockedAt: now, Reason: "quality winner"}
	payload, _ := json.Marshal(directive)
	request := httptest.NewRequest(http.MethodPost, "/internal/candidates/"+candidate.ID+"/winner", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "winner-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("winner=%d %s", response.Code, response.Body.String())
	}
	stored, err := repository.Candidate(context.Background(), candidate.ID)
	if err != nil || stored.State != domain.StateImportReady || stored.StateRevision != 9 {
		t.Fatalf("winner state=%+v err=%v", stored, err)
	}
}

func TestPipelineInternalWinnerAcceptsRecordedApprovedRevisionAfterPendingTransition(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='ARBITRATION_PENDING',state_revision=8 WHERE candidate_id=?`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	service := application.HandoffService{Store: repository, LocalConfigSnapshotID: "config-1", Now: func() time.Time { return now }}
	handler, _ := (internalapi.API{Service: service, BodyLimit: 4096, Bearer: []byte("secret")}).Handler()
	directive := contracts.CandidateWinner{RequestID: "winner-approved-revision", JobID: "job-1", CandidateID: candidate.ID, ConfigSnapshotID: "config-1", StateRevision: 7, WinnerLockedAt: now, Reason: "quality winner"}
	payload, _ := json.Marshal(directive)
	request := httptest.NewRequest(http.MethodPost, "/internal/candidates/"+candidate.ID+"/winner", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "winner-approved-revision-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("winner=%d %s", response.Code, response.Body.String())
	}
	stored, err := repository.Candidate(context.Background(), candidate.ID)
	if err != nil || stored.State != domain.StateImportReady || stored.StateRevision != 9 {
		t.Fatalf("winner state=%+v err=%v", stored, err)
	}
}

func TestPipelineInternalSupersedeAcceptsRecordedApprovedRevisionAfterPendingTransition(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`UPDATE candidates SET state='ARBITRATION_PENDING',state_revision=8 WHERE candidate_id=?`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	service := application.HandoffService{Store: repository, LocalConfigSnapshotID: "config-1", Now: func() time.Time { return now }}
	handler, _ := (internalapi.API{Service: service, BodyLimit: 4096, Bearer: []byte("secret")}).Handler()
	directive := contracts.CandidateSuperseded{RequestID: "supersede-approved-revision", JobID: "job-1", CandidateID: candidate.ID, ConfigSnapshotID: "config-1", WinnerCandidateID: "candidate-winner", Reason: "SUPERSEDED", SupersededAt: now, StateRevision: 7}
	payload, _ := json.Marshal(directive)
	request := httptest.NewRequest(http.MethodPost, "/internal/candidates/"+candidate.ID+"/supersede", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "supersede-approved-revision-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("supersede=%d %s", response.Code, response.Body.String())
	}
	stored, err := repository.Candidate(context.Background(), candidate.ID)
	if err != nil || stored.State != domain.StateSuperseded || stored.StateRevision != 9 {
		t.Fatalf("superseded state=%+v err=%v", stored, err)
	}
}
