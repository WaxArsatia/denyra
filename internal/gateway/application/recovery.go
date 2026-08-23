package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type GatewayRecovery struct {
	Store         *persistence.Repositories
	Arbitration   ArbitrationService
	Handoff       CandidateHandoffService
	Primary       PrimarySearch
	Reconciler    PrimaryReconciler
	RetryPolicy   domain.RetryPolicy
	SpotiFLACRoot string
	ActiveProcess func(string) bool
	CancelProcess func(string) error
	Now           func() time.Time
}

type RecoveryReport struct {
	ExpiredLeases, ReplayedHandoffs, ReconciledWinners, OrphanDirectories int
}

func (recovery GatewayRecovery) Reconcile(ctx context.Context) (RecoveryReport, error) {
	var report RecoveryReport
	if recovery.Store == nil || recovery.SpotiFLACRoot == "" {
		return report, fmt.Errorf("gateway recovery is not configured")
	}
	var err error
	report.ExpiredLeases, err = recovery.Store.ReconcileExpiredLeases(ctx, recovery.now())
	if err != nil {
		return report, err
	}
	effects, err := recovery.Store.UnresolvedEffects(ctx, "")
	if err != nil {
		return report, err
	}
	arbitrationJobs := make(map[string]struct{})
	for _, effect := range effects {
		switch effect.Type {
		case "PIPELINE_REGISTER":
			var request struct {
				CandidateID string `json:"candidate_id"`
			}
			if err := json.Unmarshal(effect.Request, &request); err != nil {
				return report, err
			}
			candidate, err := recovery.Store.PendingCandidate(ctx, request.CandidateID)
			if err != nil {
				return report, err
			}
			if err := recovery.Handoff.RegisterPending(ctx, candidate); err != nil {
				return report, err
			}
			report.ReplayedHandoffs++
		case "PIPELINE_ACCEPT":
			var request struct {
				CandidateID string `json:"candidate_id"`
			}
			if err := json.Unmarshal(effect.Request, &request); err != nil {
				return report, err
			}
			candidate, err := recovery.Store.Candidate(ctx, request.CandidateID)
			if err != nil {
				return report, err
			}
			var original struct {
				Provenance map[string]any `json:"provenance"`
			}
			if err := json.Unmarshal(effect.Request, &original); err != nil {
				return report, err
			}
			provider, _ := original.Provenance["provider"].(string)
			engine, _ := original.Provenance["engine_version"].(string)
			provenance := contracts.AcquisitionProvenance{Provider: provider, EngineVersion: engine, OutputSHA256: candidate.OutputSHA256}
			if err := recovery.Handoff.AcceptCompleted(ctx, candidate, provenance); err != nil {
				return report, err
			}
			report.ReplayedHandoffs++
		case "PIPELINE_WINNER", "PIPELINE_SUPERSEDE", "TRANSFER_CANCEL":
			arbitrationJobs[effect.JobID] = struct{}{}
		case "SPOTIFLAC_CANCEL":
			if recovery.ActiveProcess != nil && recovery.ActiveProcess(effect.JobID) {
				if recovery.CancelProcess == nil {
					return report, fmt.Errorf("SpotiFLAC cancellation recovery is not configured")
				}
				if err := recovery.CancelProcess(effect.JobID); err != nil {
					return report, err
				}
			}
			response := []byte(`{"status":"SUPERSEDED_CANCELLED","reconciled":true}`)
			sum := sha256.Sum256(response)
			if err := recovery.Store.AcknowledgeEffect(ctx, effect.IdempotencyKey, response, hex.EncodeToString(sum[:]), recovery.now()); err != nil {
				return report, err
			}
		}
	}
	for jobID := range arbitrationJobs {
		if err := recovery.Arbitration.Evaluate(ctx, jobID); err != nil {
			return report, err
		}
		report.ReconciledWinners++
	}
	jobs, err := recovery.Store.ActiveJobs(ctx)
	if err != nil {
		return report, err
	}
	known := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		known[job.ID] = struct{}{}
		switch job.State {
		case domain.StatePrimarySearchRequested:
			search, contextErr := recovery.Store.PrimarySearchContext(ctx, job.ID)
			if contextErr != nil || !recovery.now().Before(search.GraceDeadline) {
				if reconcileErr := recovery.Reconciler.ReconcileUncertain(ctx, job.ID); reconcileErr != nil {
					_ = recovery.Store.AppendRecoveryEvent(ctx, job.ID, "PRIMARY_UNKNOWN_RECONCILED", map[string]string{"result": reconcileErr.Error()}, recovery.now())
				}
			}
		case domain.StatePrimarySearchRunning:
			search, contextErr := recovery.Store.PrimarySearchContext(ctx, job.ID)
			if contextErr != nil || !recovery.now().Before(search.CommandDeadline) {
				if resumeErr := recovery.Primary.Resume(ctx, job.ID); resumeErr != nil {
					_ = recovery.Store.AppendRecoveryEvent(ctx, job.ID, "PRIMARY_COMMAND_RECOVERED", map[string]string{"result": resumeErr.Error()}, recovery.now())
				}
			}
		case domain.StateFallbackRunning:
			active := recovery.ActiveProcess != nil && recovery.ActiveProcess(job.ID)
			if !active {
				deadline, deadlineErr := recovery.RetryPolicy.FallbackDeadline(recovery.now(), job.FallbackAttempt)
				if deadlineErr != nil {
					return report, deadlineErr
				}
				if _, transitionErr := recovery.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: domain.StateFallbackRetryableError, Actor: "gateway-recovery", Reason: "SpotiFLAC process missing after restart", NextRetryAt: &deadline, IncrementFallbackAttempt: true, OccurredAt: recovery.now()}); transitionErr != nil {
					return report, transitionErr
				}
			}
		}
		if job.State == domain.StateArbitrating || job.State == domain.StateWinnerLocked {
			has, err := recovery.Store.HasArbitration(ctx, job.ID)
			if err != nil {
				return report, err
			}
			if has {
				if err := recovery.Arbitration.Evaluate(ctx, job.ID); err != nil {
					return report, err
				}
				report.ReconciledWinners++
			}
		}
	}
	entries, err := os.ReadDir(recovery.SpotiFLACRoot)
	if err != nil && !os.IsNotExist(err) {
		return report, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, found := known[entry.Name()]; found || recovery.ActiveProcess != nil && recovery.ActiveProcess(entry.Name()) {
			continue
		}
		if err := recovery.Store.AppendRecoveryEvent(ctx, "", "ORPHAN_SPOTIFLAC_DIRECTORY", map[string]string{"path": filepath.Join(recovery.SpotiFLACRoot, entry.Name())}, recovery.now()); err != nil {
			return report, err
		}
		report.OrphanDirectories++
	}
	return report, nil
}

func (recovery GatewayRecovery) now() time.Time {
	if recovery.Now != nil {
		return recovery.Now().UTC()
	}
	return time.Now().UTC()
}
