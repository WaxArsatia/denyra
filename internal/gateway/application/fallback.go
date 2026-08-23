package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/spotiflac"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type FallbackRunner interface {
	Run(context.Context, spotiflac.RunRequest) (spotiflac.RunResult, error)
}

type FallbackService struct {
	Runner         FallbackRunner
	Store          *persistence.Repositories
	Policy         domain.RetryPolicy
	Providers      []string
	OutputRoot     string
	OverallTimeout time.Duration
	Now            func() time.Time
	Handoff        interface {
		AcceptCompleted(context.Context, persistence.Candidate, contracts.AcquisitionProvenance) error
	}
}

func (service FallbackService) Run(ctx context.Context, jobID string) error {
	if service.Runner == nil || service.Store == nil || service.OutputRoot == "" || service.OverallTimeout <= 0 || len(service.Providers) == 0 {
		return fmt.Errorf("fallback service is not configured")
	}
	job, err := service.Store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	if job.State != domain.StateFallbackRunning {
		return fmt.Errorf("job %s cannot run fallback from %s", job.ID, job.State)
	}
	now := service.now()
	overallDeadline := now.Add(service.OverallTimeout)
	if job.OverallDeadline != nil {
		overallDeadline = *job.OverallDeadline
	} else {
		overallDeadline, err = service.Store.SetOverallDeadline(ctx, job.ID, job.Revision, overallDeadline, now)
		if err != nil {
			return err
		}
	}
	request := spotiflac.RunRequest{
		JobID:           job.ID,
		ReleaseGroupID:  job.ReleaseGroupMBID,
		SelectedRelease: job.SelectedReleaseMBID,
		OutputDirectory: filepath.Join(service.OutputRoot, job.ID),
		Providers:       append([]string(nil), service.Providers...),
		OverallDeadline: overallDeadline,
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return err
	}
	attemptID, err := service.Store.InsertAttempt(ctx, persistence.Attempt{JobID: job.ID, Kind: "SPOTIFLAC", Number: job.FallbackAttempt, StartedAt: now, Details: requestJSON})
	if err != nil {
		return err
	}
	requestSum := sha256.Sum256(requestJSON)
	effectKey := fmt.Sprintf("spotiflac-%s-%d", job.ID, job.FallbackAttempt)
	if err := service.Store.PutEffect(ctx, persistence.Effect{JobID: job.ID, Type: "SPOTIFLAC_RUN", IdempotencyKey: effectKey, RequestHash: hex.EncodeToString(requestSum[:]), Request: requestJSON, CreatedAt: now}); err != nil {
		return err
	}
	result, runErr := service.Runner.Run(ctx, request)
	if runErr != nil {
		completed := service.now()
		result = spotiflac.RunResult{StartedAt: now, CompletedAt: completed, Providers: []spotiflac.ProviderExecution{{Provider: "engine", Outcome: domain.OutcomeRetryableError, StartedAt: now, CompletedAt: &completed, ExitCode: -1, ErrorClass: "RUNNER_ERROR", ErrorMessage: runErr.Error()}}}
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	responseSum := sha256.Sum256(resultJSON)
	if err := service.Store.AcknowledgeEffect(ctx, effectKey, resultJSON, hex.EncodeToString(responseSum[:]), service.now()); err != nil {
		return err
	}
	for _, provider := range result.Providers {
		evidence, err := json.Marshal(provider)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(evidence)
		if err := service.Store.InsertProviderResult(ctx, persistence.ProviderEvidence{JobID: job.ID, AttemptID: attemptID, Provider: provider.Provider, Outcome: string(provider.Outcome), Evidence: evidence, EvidenceSHA256: hex.EncodeToString(sum[:]), StartedAt: provider.StartedAt, EstablishedAt: provider.EstablishedAt, CompletedAt: provider.CompletedAt}); err != nil {
			return err
		}
	}
	state, classificationErr := domain.ClassifyFallback(result.DomainResults())
	if classificationErr != nil {
		state = domain.StateFallbackRetryableError
		if runErr == nil {
			runErr = classificationErr
		}
	}
	errorClass := ""
	if state == domain.StateFallbackRetryableError {
		errorClass = "OPERATIONAL"
	}
	var completedCandidate *persistence.Candidate
	var acquisitionProvenance contracts.AcquisitionProvenance
	if state == domain.StateArbitrating {
		candidate, provenance, err := service.persistCompletedCandidate(ctx, job, request, result, resultJSON, responseSum)
		if err != nil {
			return err
		}
		completedCandidate = &candidate
		acquisitionProvenance = provenance
	}
	if err := service.Store.CompleteAttempt(ctx, attemptID, string(state), errorClass, resultJSON, service.now()); err != nil {
		return err
	}
	command := persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: state, Actor: "gateway-fallback", OccurredAt: service.now()}
	switch state {
	case domain.StateArbitrating:
		command.Reason = "SpotiFLAC completed a candidate"
	case domain.StateNoCandidate:
		command.Reason = "all fallback providers returned legitimate no-result"
		deadline, err := service.Policy.NoCandidateDeadline(service.now())
		if err != nil {
			return err
		}
		command.NextRetryAt = &deadline
	case domain.StateFallbackRetryableError:
		command.Reason = "fallback provider or runtime operational error"
		deadline, err := service.Policy.FallbackDeadline(service.now(), job.FallbackAttempt)
		if err != nil {
			return err
		}
		command.NextRetryAt = &deadline
		command.IncrementFallbackAttempt = true
	default:
		return fmt.Errorf("unsupported fallback classification %s", state)
	}
	_, err = service.Store.UpdateState(ctx, command)
	if err != nil {
		return err
	}
	if completedCandidate != nil && service.Handoff != nil {
		if err := service.Handoff.AcceptCompleted(ctx, *completedCandidate, acquisitionProvenance); err != nil {
			return err
		}
	}
	return runErr
}

func (service FallbackService) persistCompletedCandidate(ctx context.Context, job domain.Job, request spotiflac.RunRequest, result spotiflac.RunResult, provenanceJSON []byte, provenanceHash [sha256.Size]byte) (persistence.Candidate, contracts.AcquisitionProvenance, error) {
	outputChecksum, outputManifest, err := spotiflac.OutputManifestSHA256(result.Output)
	if err != nil {
		return persistence.Candidate{}, contracts.AcquisitionProvenance{}, err
	}
	candidateID, err := service.Store.CandidateForJobSource(ctx, job.ID, "spotiflac", true)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return persistence.Candidate{}, contracts.AcquisitionProvenance{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		candidateID, err = persistence.NewCandidateID()
		if err != nil {
			return persistence.Candidate{}, contracts.AcquisitionProvenance{}, err
		}
	}
	completedAt := result.CompletedAt
	candidate := persistence.Candidate{ID: candidateID, JobID: job.ID, Source: "spotiflac", SourceLocator: request.OutputDirectory, CompletedAt: &completedAt, OutputSHA256: outputChecksum, OutputManifest: outputManifest, Provenance: provenanceJSON, ProvenanceSHA256: hex.EncodeToString(provenanceHash[:]), CreatedAt: completedAt}
	if err := service.Store.InsertCandidate(ctx, candidate); err != nil {
		return persistence.Candidate{}, contracts.AcquisitionProvenance{}, err
	}
	provenance := contracts.AcquisitionProvenance{Provider: result.WinningProvider, EngineVersion: result.EngineVersion, OutputSHA256: outputChecksum}
	return candidate, provenance, nil
}

func (service FallbackService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}
