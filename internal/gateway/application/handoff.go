package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	pipelineadapter "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type PipelineHandoffClient interface {
	Register(context.Context, contracts.CandidateRegistered, string, string) (pipelineadapter.Response, error)
	Accept(context.Context, contracts.CandidateAccepted, string, string) (pipelineadapter.Response, error)
}

type CandidateHandoffService struct {
	Pipeline       PipelineHandoffClient
	Store          *persistence.Repositories
	ReplayAttempts int
	Now            func() time.Time
}

func (service CandidateHandoffService) RegisterPending(ctx context.Context, candidate persistence.PendingCandidate) error {
	if err := service.validate(); err != nil {
		return err
	}
	job, err := service.Store.Job(ctx, candidate.JobID)
	if err != nil {
		return err
	}
	source := contractSource(candidate.Source)
	if source == "" {
		return fmt.Errorf("unsupported acquisition candidate source %q", candidate.Source)
	}
	requestID := "candidate-register-" + candidate.ID
	request := contracts.CandidateRegistered{
		RequestID:        requestID,
		JobID:            candidate.JobID,
		CandidateID:      candidate.ID,
		ConfigSnapshotID: job.ConfigSnapshotID,
		Source:           source,
		SourceLocator:    candidate.SourceLocator,
		DownloadID:       candidate.DownloadID,
		RegisteredAt:     candidate.CreatedAt,
	}
	return service.deliverRegistration(ctx, request, requestID)
}

func (service CandidateHandoffService) AcceptCompleted(ctx context.Context, candidate persistence.Candidate, provenance contracts.AcquisitionProvenance) error {
	if err := service.validate(); err != nil {
		return err
	}
	if candidate.CompletedAt == nil {
		return fmt.Errorf("completed candidate has no completion timestamp")
	}
	job, err := service.Store.Job(ctx, candidate.JobID)
	if err != nil {
		return err
	}
	source := contractSource(candidate.Source)
	if source == "" {
		return fmt.Errorf("unsupported acquisition candidate source %q", candidate.Source)
	}
	requestID := "candidate-complete-" + candidate.ID
	request := contracts.CandidateAccepted{
		RequestID:            requestID,
		JobID:                candidate.JobID,
		CandidateID:          candidate.ID,
		ConfigSnapshotID:     job.ConfigSnapshotID,
		Source:               source,
		Path:                 candidate.SourceLocator,
		CompletionAt:         *candidate.CompletedAt,
		MusicBrainzReleaseID: job.SelectedReleaseMBID,
		Provenance:           provenance,
	}
	if request.Provenance.OutputSHA256 != candidate.OutputSHA256 {
		return fmt.Errorf("handoff provenance checksum differs from immutable candidate output")
	}
	return service.deliverCompletion(ctx, request, requestID)
}

func (service CandidateHandoffService) deliverRegistration(ctx context.Context, request contracts.CandidateRegistered, key string) error {
	if err := service.validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := service.persistIntent(ctx, request.JobID, "PIPELINE_REGISTER", key, payload, request.RegisteredAt); err != nil {
		return err
	}
	return service.replay(ctx, key, func() (pipelineadapter.Response, error) {
		return service.Pipeline.Register(ctx, request, request.RequestID, key)
	})
}

func (service CandidateHandoffService) deliverCompletion(ctx context.Context, request contracts.CandidateAccepted, key string) error {
	if err := service.validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := service.persistIntent(ctx, request.JobID, "PIPELINE_ACCEPT", key, payload, request.CompletionAt); err != nil {
		return err
	}
	return service.replay(ctx, key, func() (pipelineadapter.Response, error) {
		return service.Pipeline.Accept(ctx, request, request.RequestID, key)
	})
}

func (service CandidateHandoffService) persistIntent(ctx context.Context, jobID, kind, key string, payload []byte, at time.Time) error {
	sum := sha256.Sum256(payload)
	return service.Store.PutEffect(ctx, persistence.Effect{JobID: jobID, Type: kind, IdempotencyKey: key, RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: at})
}

func (service CandidateHandoffService) replay(ctx context.Context, key string, call func() (pipelineadapter.Response, error)) error {
	var last error
	for attempt := 0; attempt < service.ReplayAttempts; attempt++ {
		response, err := call()
		if err == nil {
			sum := sha256.Sum256(response.Body)
			return service.Store.AcknowledgeEffect(ctx, key, response.Body, hex.EncodeToString(sum[:]), service.now())
		}
		last = err
		var retryable *pipelineadapter.RetryableError
		if !errors.As(err, &retryable) {
			return err
		}
	}
	return last
}

func (service CandidateHandoffService) validate() error {
	if service.Pipeline == nil || service.Store == nil || service.ReplayAttempts <= 0 {
		return fmt.Errorf("candidate handoff service is not configured")
	}
	return nil
}

func (service CandidateHandoffService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func contractSource(source string) contracts.AcquisitionSource {
	return map[string]contracts.AcquisitionSource{"slskd": contracts.SourceSlskd, "spotiflac": contracts.SourceSpotiFLAC}[source]
}
