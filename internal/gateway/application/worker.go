package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type AcquisitionWorker struct {
	Store                *persistence.Repositories
	Admission            AdmissionController
	Primary              PrimarySearch
	Reconciler           PrimaryReconciler
	Fallback             FallbackService
	Arbitration          ArbitrationService
	Concurrency          int
	Lease                time.Duration
	SafetyScan           time.Duration
	MaxInlineTransitions int
	Now                  func() time.Time
	OnError              func(string, error)

	events chan string
	once   sync.Once
}

func (worker *AcquisitionWorker) Notify(jobID string) {
	worker.initialize()
	select {
	case worker.events <- jobID:
	default:
	}
}

func (worker *AcquisitionWorker) Run(ctx context.Context) error {
	if worker.Store == nil || worker.Concurrency <= 0 || worker.Lease <= 0 || worker.SafetyScan <= 0 || worker.MaxInlineTransitions <= 0 {
		return fmt.Errorf("acquisition worker is not configured")
	}
	worker.initialize()
	semaphore := make(chan struct{}, worker.Concurrency)
	var wait sync.WaitGroup
	dispatch := func(jobID string) {
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-semaphore }()
			if err := worker.ProcessOne(ctx, jobID); err != nil && !errors.Is(err, ErrAdmissionBlocked) && !errors.Is(err, persistence.ErrLeaseHeld) {
				if worker.OnError != nil {
					worker.OnError(jobID, err)
				}
			}
		}()
	}
	queueAll := func() error {
		jobs, err := worker.Store.ActiveJobs(ctx)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			dispatch(job.ID)
		}
		return nil
	}
	if err := queueAll(); err != nil {
		return err
	}
	ticker := time.NewTicker(worker.SafetyScan)
	defer ticker.Stop()
	defer wait.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case jobID := <-worker.events:
			if jobID == "" {
				if err := queueAll(); err != nil && worker.OnError != nil {
					worker.OnError("", err)
				}
				continue
			}
			dispatch(jobID)
		case <-ticker.C:
			if err := queueAll(); err != nil && worker.OnError != nil {
				worker.OnError("", err)
			}
		}
	}
}

func (worker *AcquisitionWorker) ProcessOne(ctx context.Context, jobID string) error {
	job, err := worker.Store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	if !jobReady(job, worker.now()) {
		return nil
	}
	if startsNewAcquisition(job.State) {
		if err := worker.Admission.CheckNew(ctx); err != nil {
			return err
		}
	}
	owner, err := ids.NewToken(12)
	if err != nil {
		return err
	}
	now := worker.now()
	lease := persistence.Lease{ResourceType: "acquisition-job", ResourceID: job.ID, OwnerID: owner, ConfigSnapshotID: job.ConfigSnapshotID, AcquiredAt: now, ExpiresAt: now.Add(worker.Lease), ResourceRevision: job.Revision}
	if err := worker.Store.AcquireLease(ctx, lease, false); err != nil {
		return err
	}
	defer worker.Store.ReleaseLease(context.WithoutCancel(ctx), lease.ResourceType, lease.ResourceID, lease.OwnerID)
	leaseContext, cancelLease := context.WithCancel(ctx)
	renewalErrors := make(chan error, 1)
	go worker.renewLease(leaseContext, cancelLease, lease, renewalErrors)
	operationErr := worker.processLeased(leaseContext, job)
	cancelLease()
	renewalErr := <-renewalErrors
	if renewalErr != nil {
		return renewalErr
	}
	return operationErr
}

func (worker *AcquisitionWorker) processLeased(ctx context.Context, job domain.Job) error {
	for step := 0; step < worker.MaxInlineTransitions; step++ {
		previousRevision := job.Revision
		if err := worker.processStep(ctx, &job); err != nil {
			return err
		}
		current, err := worker.Store.Job(ctx, job.ID)
		if err != nil {
			return err
		}
		if current.Revision == previousRevision || !jobReady(current, worker.now()) {
			return nil
		}
		job = current
	}
	return fmt.Errorf("acquisition job %s exceeded bounded inline transitions", job.ID)
}

func (worker *AcquisitionWorker) processStep(ctx context.Context, job *domain.Job) error {
	switch job.State {
	case domain.StateDiscovered, domain.StatePrimaryRetryableError, domain.StateNoCandidate:
		return worker.Primary.Run(ctx, job.ID)
	case domain.StatePrimarySearchRunning:
		return worker.Primary.Resume(ctx, job.ID)
	case domain.StatePrimarySearchRequested:
		return worker.Reconciler.ReconcileUncertain(ctx, job.ID)
	case domain.StatePrimaryReconciling:
		return worker.Reconciler.Run(ctx, job.ID)
	case domain.StateFallbackRunning:
		if err := worker.Admission.CheckNew(ctx); err != nil {
			return err
		}
		return worker.Fallback.Run(ctx, job.ID)
	case domain.StateFallbackRetryableError:
		if job.OverallDeadline != nil && !worker.now().Before(*job.OverallDeadline) {
			return worker.Fallback.restartCycle(ctx, *job, worker.now())
		}
		event, err := worker.Store.UpdateState(ctx, persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: domain.StateFallbackRunning, Actor: "gateway-worker", Reason: "fallback retry deadline reached", OccurredAt: worker.now()})
		if err != nil {
			return err
		}
		job.Revision = event.Revision
		job.State = event.Next
		return nil
	case domain.StateArbitrating, domain.StateWinnerLocked:
		hasArbitration, err := worker.Store.HasArbitration(ctx, job.ID)
		if err != nil || !hasArbitration {
			return err
		}
		return worker.Arbitration.Evaluate(ctx, job.ID)
	default:
		return nil
	}
}

func (worker *AcquisitionWorker) renewLease(ctx context.Context, cancel context.CancelFunc, lease persistence.Lease, result chan<- error) {
	interval := worker.Lease / 3
	if interval <= 0 {
		interval = worker.Lease
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			if err := worker.Store.RenewLease(ctx, lease.ResourceType, lease.ResourceID, lease.OwnerID, lease.ResourceRevision, worker.now().Add(worker.Lease)); err != nil {
				if ctx.Err() != nil {
					result <- nil
					return
				}
				cancel()
				result <- err
				return
			}
		}
	}
}

func (worker *AcquisitionWorker) initialize() {
	worker.once.Do(func() { worker.events = make(chan string, 256) })
}

func (worker *AcquisitionWorker) now() time.Time {
	if worker.Now != nil {
		return worker.Now().UTC()
	}
	return time.Now().UTC()
}

func startsNewAcquisition(state domain.State) bool {
	switch state {
	case domain.StateDiscovered, domain.StatePrimaryRetryableError, domain.StateNoCandidate, domain.StateFallbackRunning, domain.StateFallbackRetryableError:
		return true
	default:
		return false
	}
}

func jobReady(job domain.Job, now time.Time) bool {
	if job.NextRetryAt == nil {
		return true
	}
	return !now.Before(*job.NextRetryAt)
}
