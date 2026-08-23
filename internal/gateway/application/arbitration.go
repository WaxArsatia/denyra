package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	pipelineadapter "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type ArbitrationPipelineClient interface {
	Winner(context.Context, contracts.CandidateWinner, string, string) (pipelineadapter.Response, error)
	Supersede(context.Context, contracts.CandidateSuperseded, string, string) (pipelineadapter.Response, error)
}

type IncompleteTransferCanceller interface {
	CancelIncomplete(context.Context, persistence.PendingCandidate) ([]byte, error)
}

type ArbitrationService struct {
	Store          *persistence.Repositories
	Pipeline       ArbitrationPipelineClient
	Canceller      IncompleteTransferCanceller
	Window         time.Duration
	ReplayAttempts int
	Now            func() time.Time
}

type ApprovalResponse struct {
	JobID             string    `json:"job_id"`
	CandidateID       string    `json:"candidate_id"`
	State             string    `json:"state"`
	ArbitrationEndsAt time.Time `json:"arbitration_ends_at"`
	WinnerCandidateID string    `json:"winner_candidate_id,omitempty"`
}

func (service ArbitrationService) Approve(ctx context.Context, key string, request contracts.CandidateApproved) (int, []byte, error) {
	if err := service.validateRequest(key, request); err != nil {
		return 0, nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return 0, nil, err
	}
	sum := sha256.Sum256(payload)
	candidate, err := service.Store.Candidate(ctx, request.CandidateID)
	if err != nil {
		return 0, nil, err
	}
	if candidate.CompletedAt == nil || request.ApprovedAt.Before(*candidate.CompletedAt) {
		return 0, nil, fmt.Errorf("approval cannot predate acquisition completion")
	}
	approval := persistence.Approval{
		CandidateID: request.CandidateID, JobID: request.JobID, ConfigSnapshotID: request.ConfigSnapshotID,
		MusicBrainzReleaseID: request.MusicBrainzReleaseID, Source: domain.CandidateSource(candidate.Source),
		ApprovedAt: request.ApprovedAt, CompletionAt: *candidate.CompletedAt, Quality: request.Quality,
		Warnings: request.Warnings, PipelineStateRevision: request.StateRevision,
	}
	recorded, err := service.Store.RecordApproval(ctx, approval, service.Window, key, hex.EncodeToString(sum[:]), payload, service.now())
	if err != nil {
		return 0, nil, err
	}
	if recorded.Replay {
		return recorded.Status, recorded.Body, nil
	}
	if recorded.Arbitration.WinnerCandidateID != "" {
		if request.CandidateID != recorded.Arbitration.WinnerCandidateID {
			if err := service.ensureLateSuperseded(ctx, recorded.Arbitration, approval); err != nil {
				return 0, nil, err
			}
		}
		if err := service.deliverEffects(ctx, request.JobID); err != nil {
			return 0, nil, err
		}
		return service.completeResponse(ctx, key, ApprovalResponse{JobID: request.JobID, CandidateID: request.CandidateID, State: string(domain.StateWinnerLocked), ArbitrationEndsAt: recorded.Arbitration.Deadline, WinnerCandidateID: recorded.Arbitration.WinnerCandidateID})
	}
	decision, decided := domain.DecideArbitration(toDomainApprovals(recorded.Approvals), recorded.Arbitration.Deadline, service.now())
	if decided {
		if err := service.lockDecision(ctx, recorded.Arbitration, recorded.Approvals, decision); err != nil {
			return 0, nil, err
		}
		if err := service.deliverEffects(ctx, request.JobID); err != nil {
			return 0, nil, err
		}
		return service.completeResponse(ctx, key, ApprovalResponse{JobID: request.JobID, CandidateID: request.CandidateID, State: string(domain.StateWinnerLocked), ArbitrationEndsAt: recorded.Arbitration.Deadline, WinnerCandidateID: decision.Winner.ID})
	}
	return service.completeResponse(ctx, key, ApprovalResponse{JobID: request.JobID, CandidateID: request.CandidateID, State: string(domain.StateArbitrating), ArbitrationEndsAt: recorded.Arbitration.Deadline})
}

func (service ArbitrationService) Evaluate(ctx context.Context, jobID string) error {
	if err := service.validate(); err != nil {
		return err
	}
	arbitration, err := service.Store.Arbitration(ctx, jobID)
	if err != nil {
		return err
	}
	if arbitration.WinnerCandidateID == "" {
		approvals, err := service.Store.Approvals(ctx, jobID)
		if err != nil {
			return err
		}
		decision, decided := domain.DecideArbitration(toDomainApprovals(approvals), arbitration.Deadline, service.now())
		if !decided {
			return nil
		}
		if err := service.lockDecision(ctx, arbitration, approvals, decision); err != nil {
			return err
		}
	}
	return service.deliverEffects(ctx, jobID)
}

func (service ArbitrationService) lockDecision(ctx context.Context, arbitration persistence.Arbitration, approvals []persistence.Approval, decision domain.ArbitrationDecision) error {
	byID := make(map[string]persistence.Approval, len(approvals))
	for _, approval := range approvals {
		byID[approval.CandidateID] = approval
	}
	winner := byID[decision.Winner.ID]
	reason := string(decision.Reason)
	evidence, err := json.Marshal(struct {
		Winner    string                     `json:"winner"`
		Reason    domain.DecisionReason      `json:"reason"`
		Deadline  time.Time                  `json:"deadline"`
		Approvals []domain.ApprovedCandidate `json:"approvals"`
	}{Winner: winner.CandidateID, Reason: decision.Reason, Deadline: arbitration.Deadline, Approvals: toDomainApprovals(approvals)})
	if err != nil {
		return err
	}
	lockedAt := service.now()
	effects, err := service.decisionEffects(ctx, winner, approvals, decision, reason, lockedAt)
	if err != nil {
		return err
	}
	_, err = service.Store.LockWinnerWithEffects(ctx, persistence.WinnerLock{JobID: arbitration.JobID, CandidateID: winner.CandidateID, Reason: reason, ExpectedRevision: arbitration.StateRevision, Evidence: evidence, Effects: effects, LockedAt: lockedAt})
	return err
}

func (service ArbitrationService) decisionEffects(ctx context.Context, winner persistence.Approval, approvals []persistence.Approval, decision domain.ArbitrationDecision, reason string, lockedAt time.Time) ([]persistence.Effect, error) {
	winnerRequest := contracts.CandidateWinner{RequestID: "winner-" + winner.CandidateID, JobID: winner.JobID, CandidateID: winner.CandidateID, ConfigSnapshotID: winner.ConfigSnapshotID, WinnerLockedAt: lockedAt, Reason: reason, Quality: winner.Quality, StateRevision: winner.PipelineStateRevision}
	effects, err := effectFor(winner.JobID, "PIPELINE_WINNER", winnerRequest.RequestID, winnerRequest, lockedAt)
	if err != nil {
		return nil, err
	}
	result := []persistence.Effect{effects}
	for _, loser := range decision.Losers {
		approval := approvalByID(approvals, loser.ID)
		request := contracts.CandidateSuperseded{RequestID: "supersede-" + approval.CandidateID, JobID: approval.JobID, CandidateID: approval.CandidateID, ConfigSnapshotID: approval.ConfigSnapshotID, WinnerCandidateID: winner.CandidateID, Reason: "SUPERSEDED", SupersededAt: lockedAt, StateRevision: approval.PipelineStateRevision}
		effect, err := effectFor(winner.JobID, "PIPELINE_SUPERSEDE", request.RequestID, request, lockedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, effect)
	}
	incomplete, err := service.Store.IncompletePendingCandidates(ctx, winner.JobID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range incomplete {
		request := incompleteCancellation{JobID: winner.JobID, CandidateID: candidate.ID, Source: candidate.Source, SourceLocator: candidate.SourceLocator, DownloadID: candidate.DownloadID, Reason: "SUPERSEDED_CANCELLED", CancelledAt: lockedAt}
		effect, err := effectFor(winner.JobID, "TRANSFER_CANCEL", "cancel-"+candidate.ID, request, lockedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, effect)
	}
	return result, nil
}

func (service ArbitrationService) ensureLateSuperseded(ctx context.Context, arbitration persistence.Arbitration, approval persistence.Approval) error {
	at := service.now()
	request := contracts.CandidateSuperseded{RequestID: "supersede-" + approval.CandidateID, JobID: approval.JobID, CandidateID: approval.CandidateID, ConfigSnapshotID: approval.ConfigSnapshotID, WinnerCandidateID: arbitration.WinnerCandidateID, Reason: "SUPERSEDED", SupersededAt: at, StateRevision: approval.PipelineStateRevision}
	effect, err := effectFor(approval.JobID, "PIPELINE_SUPERSEDE", request.RequestID, request, at)
	if err != nil {
		return err
	}
	return service.Store.PutEffect(ctx, effect)
}

func (service ArbitrationService) deliverEffects(ctx context.Context, jobID string) error {
	arbitration, err := service.Store.Arbitration(ctx, jobID)
	if err != nil {
		return err
	}
	if arbitration.WinnerCandidateID != "" {
		if err := service.markHandedOffIfWinnerAccepted(ctx, jobID, "winner-"+arbitration.WinnerCandidateID); err != nil {
			return err
		}
	}
	effects, err := service.Store.UnresolvedEffects(ctx, "")
	if err != nil {
		return err
	}
	for _, effect := range effects {
		if effect.JobID != jobID || effect.Type != "PIPELINE_WINNER" && effect.Type != "PIPELINE_SUPERSEDE" && effect.Type != "TRANSFER_CANCEL" {
			continue
		}
		response, err := service.deliverEffect(ctx, effect)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(response)
		if err := service.Store.AcknowledgeEffect(ctx, effect.IdempotencyKey, response, hex.EncodeToString(sum[:]), service.now()); err != nil {
			return err
		}
		if effect.Type == "PIPELINE_WINNER" {
			if err := service.markHandedOffIfWinnerAccepted(ctx, jobID, effect.IdempotencyKey); err != nil {
				return err
			}
		}
	}
	arbitration, err = service.Store.Arbitration(ctx, jobID)
	if err != nil {
		return err
	}
	if arbitration.WinnerCandidateID != "" {
		if err := service.markHandedOffIfWinnerAccepted(ctx, jobID, "winner-"+arbitration.WinnerCandidateID); err != nil {
			return err
		}
	}
	return nil
}

func (service ArbitrationService) markHandedOffIfWinnerAccepted(ctx context.Context, jobID, winnerEffectKey string) error {
	effect, err := service.Store.Effect(ctx, winnerEffectKey)
	if err != nil {
		return err
	}
	if effect.AcknowledgedAt == nil {
		return nil
	}
	job, err := service.Store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	if job.State != domain.StateWinnerLocked {
		return nil
	}
	_, err = service.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: domain.StateHandedOff, Actor: "gateway-arbitration", Reason: "winner durably accepted by media-pipeline", OccurredAt: service.now()})
	return err
}

func (service ArbitrationService) deliverEffect(ctx context.Context, effect persistence.Effect) ([]byte, error) {
	var response pipelineadapter.Response
	var call func() (pipelineadapter.Response, error)
	switch effect.Type {
	case "PIPELINE_WINNER":
		var request contracts.CandidateWinner
		if err := json.Unmarshal(effect.Request, &request); err != nil {
			return nil, err
		}
		call = func() (pipelineadapter.Response, error) {
			return service.Pipeline.Winner(ctx, request, request.RequestID, effect.IdempotencyKey)
		}
	case "PIPELINE_SUPERSEDE":
		var request contracts.CandidateSuperseded
		if err := json.Unmarshal(effect.Request, &request); err != nil {
			return nil, err
		}
		call = func() (pipelineadapter.Response, error) {
			return service.Pipeline.Supersede(ctx, request, request.RequestID, effect.IdempotencyKey)
		}
	case "TRANSFER_CANCEL":
		if service.Canceller == nil {
			return nil, fmt.Errorf("incomplete transfer canceller is not configured")
		}
		var request incompleteCancellation
		if err := json.Unmarshal(effect.Request, &request); err != nil {
			return nil, err
		}
		return service.Canceller.CancelIncomplete(ctx, persistence.PendingCandidate{ID: request.CandidateID, JobID: request.JobID, Source: request.Source, SourceLocator: request.SourceLocator, DownloadID: request.DownloadID})
	default:
		return nil, fmt.Errorf("unsupported arbitration effect %q", effect.Type)
	}
	var last error
	for attempt := 0; attempt < service.ReplayAttempts; attempt++ {
		response, last = call()
		if last == nil {
			return response.Body, nil
		}
		var retryable *pipelineadapter.RetryableError
		if !errors.As(last, &retryable) {
			return nil, last
		}
	}
	return nil, last
}

func (service ArbitrationService) completeResponse(ctx context.Context, key string, response ApprovalResponse) (int, []byte, error) {
	body, err := json.Marshal(response)
	if err != nil {
		return 0, nil, err
	}
	status := http.StatusAccepted
	if response.WinnerCandidateID != "" {
		status = http.StatusOK
	}
	if err := service.Store.CompleteApprovalCallback(ctx, key, status, body, service.now()); err != nil {
		return 0, nil, err
	}
	return status, body, nil
}

func (service ArbitrationService) validateRequest(key string, request contracts.CandidateApproved) error {
	if err := service.validate(); err != nil {
		return err
	}
	if key == "" || request.RequestID == "" || request.JobID == "" || request.CandidateID == "" || request.ConfigSnapshotID == "" || request.ApprovedAt.IsZero() || request.ApprovedAt.Location() != time.UTC || request.StateRevision == 0 {
		return fmt.Errorf("approval identity and explicit UTC timestamp are required")
	}
	if _, err := domain.CanonicalMBID(request.MusicBrainzReleaseID); err != nil {
		return err
	}
	qualityWarnings := 0
	for _, warning := range request.Warnings {
		if warning.Code == "" || warning.Message == "" || warning.Class != contracts.QualityWarning && warning.Class != contracts.NonBlockingWarning {
			return fmt.Errorf("invalid warning evidence")
		}
		if warning.Class == contracts.QualityWarning {
			qualityWarnings++
		}
	}
	if request.Quality.QualityWarningCount != qualityWarnings || request.Quality.IdentityRank < 0 || request.Quality.EditionRank < 0 || request.Quality.SourceConfidence < 0 || request.Quality.BitDepth <= 0 || request.Quality.SampleRate <= 0 {
		return fmt.Errorf("quality vector does not match warning evidence")
	}
	return nil
}

func (service ArbitrationService) validate() error {
	if service.Store == nil || service.Pipeline == nil || service.Window <= 0 || service.ReplayAttempts <= 0 {
		return fmt.Errorf("arbitration service is not configured")
	}
	return nil
}

func (service ArbitrationService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

type incompleteCancellation struct {
	JobID         string    `json:"job_id"`
	CandidateID   string    `json:"candidate_id"`
	Source        string    `json:"source"`
	SourceLocator string    `json:"source_locator"`
	DownloadID    string    `json:"download_id,omitempty"`
	Reason        string    `json:"reason"`
	CancelledAt   time.Time `json:"cancelled_at"`
}

func effectFor(jobID, effectType, key string, request any, at time.Time) (persistence.Effect, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return persistence.Effect{}, err
	}
	sum := sha256.Sum256(payload)
	return persistence.Effect{JobID: jobID, Type: effectType, IdempotencyKey: key, RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: at}, nil
}

func approvalByID(approvals []persistence.Approval, id string) persistence.Approval {
	for _, approval := range approvals {
		if approval.CandidateID == id {
			return approval
		}
	}
	return persistence.Approval{}
}

func toDomainApprovals(approvals []persistence.Approval) []domain.ApprovedCandidate {
	result := make([]domain.ApprovedCandidate, 0, len(approvals))
	for _, approval := range approvals {
		nonBlocking := 0
		for _, warning := range approval.Warnings {
			if warning.Class == contracts.NonBlockingWarning {
				nonBlocking++
			}
		}
		result = append(result, domain.ApprovedCandidate{ID: approval.CandidateID, Source: approval.Source, ApprovedAt: approval.ApprovedAt, CompletionAt: approval.CompletionAt, StateRevision: approval.PipelineStateRevision, Quality: domain.Quality{IdentityRank: approval.Quality.IdentityRank, EditionRank: approval.Quality.EditionRank, QualityWarningCount: approval.Quality.QualityWarningCount, SourceConfidence: approval.Quality.SourceConfidence, BitDepth: approval.Quality.BitDepth, SampleRate: approval.Quality.SampleRate}, NonBlockingWarningCount: nonBlocking})
	}
	return result
}
