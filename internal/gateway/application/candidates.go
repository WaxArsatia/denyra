package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type SupersededCanceller interface {
	CancelSuperseded(string) error
}

type LatePrimaryService struct {
	Store     *persistence.Repositories
	Canceller SupersededCanceller
	Handoff   interface {
		RegisterPending(context.Context, persistence.PendingCandidate) error
	}
	Now func() time.Time
}

type LatePrimaryMonitor struct {
	Store      *persistence.Repositories
	Reconciler PrimaryReconciler
	Handler    LatePrimaryService
}

func (monitor LatePrimaryMonitor) Reconcile(ctx context.Context) (int, error) {
	jobs, err := monitor.Store.ActiveJobs(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, job := range jobs {
		if job.State != domain.StateFallbackRunning && job.State != domain.StateFallbackRetryableError && job.State != domain.StateNoCandidate && job.State != domain.StateArbitrating {
			continue
		}
		evidence, err := monitor.Reconciler.LateEvidence(ctx, job.ID)
		if err != nil {
			return changed, err
		}
		if len(evidence) == 0 {
			continue
		}
		if err := monitor.Handler.Handle(ctx, job.ID, evidence); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

func (service LatePrimaryService) Handle(ctx context.Context, jobID string, evidence []persistence.CorrelationEvidence) error {
	if service.Store == nil || service.Canceller == nil || len(evidence) == 0 {
		return fmt.Errorf("late primary service is not configured")
	}
	job, err := service.Store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	if job.State != domain.StateFallbackRunning && job.State != domain.StateFallbackRetryableError && job.State != domain.StateNoCandidate && job.State != domain.StateArbitrating {
		return fmt.Errorf("late primary cannot be applied from %s", job.State)
	}
	pending, err := newPrimaryPendingCandidate(job, evidence, service.now())
	if err != nil {
		return err
	}
	_, candidateErr := service.Store.CandidateForJobSource(ctx, job.ID, "spotiflac", true)
	completedFallback := candidateErr == nil
	if candidateErr != nil && !errors.Is(candidateErr, sql.ErrNoRows) {
		return candidateErr
	}
	target := domain.StateDualCandidate
	reason := "late correlated primary grab retained completed fallback candidate"
	if !completedFallback {
		if job.State == domain.StateFallbackRunning {
			if err := service.cancelFallback(ctx, job); err != nil {
				return err
			}
		} else if job.State == domain.StateArbitrating {
			return fmt.Errorf("fallback candidate state is inconsistent")
		}
		target = domain.StatePrimaryActive
		reason = "late correlated primary grab superseded fallback retry schedule"
	}
	_, err = service.Store.RegisterPrimaryCandidate(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: target, Actor: "gateway-late-primary", Reason: reason, OccurredAt: service.now()}, evidence, pending)
	if err != nil {
		return err
	}
	if service.Handoff != nil {
		return service.Handoff.RegisterPending(ctx, pending)
	}
	return nil
}

func (service LatePrimaryService) cancelFallback(ctx context.Context, job domain.Job) error {
	key := "spotiflac-cancel-" + job.ID
	payload, err := json.Marshal(map[string]string{"job_id": job.ID, "reason": "SUPERSEDED_CANCELLED"})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	if err := service.Store.PutEffect(ctx, persistence.Effect{JobID: job.ID, Type: "SPOTIFLAC_CANCEL", IdempotencyKey: key, RequestHash: hex.EncodeToString(sum[:]), Request: payload, CreatedAt: service.now()}); err != nil {
		return err
	}
	effect, err := service.Store.Effect(ctx, key)
	if err != nil {
		return err
	}
	if effect.AcknowledgedAt != nil {
		return nil
	}
	if err := service.Canceller.CancelSuperseded(job.ID); err != nil {
		return err
	}
	response := []byte(`{"status":"SUPERSEDED_CANCELLED"}`)
	responseSum := sha256.Sum256(response)
	return service.Store.AcknowledgeEffect(ctx, key, response, hex.EncodeToString(responseSum[:]), service.now())
}

func (service LatePrimaryService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}
