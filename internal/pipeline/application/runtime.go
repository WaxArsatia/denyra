package application

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Runtime struct {
	Discovery        DiscoveryService
	Recovery         RecoveryService
	Worker           *Worker
	Migration        *MigrationRuntime
	RecoveryInterval time.Duration
	discovery        chan struct{}
	once             sync.Once
}

func (r *Runtime) NotifyManualDiscovery() {
	r.initialize()
	select {
	case r.discovery <- struct{}{}:
	default:
	}
}
func (r *Runtime) NotifyCandidate(candidateID string) {
	if r.Worker != nil {
		r.Worker.Notify(candidateID)
	}
}
func (r *Runtime) NotifyMigrationBatch(batchID string) {
	if r.Migration != nil {
		r.Migration.NotifyBatch(batchID)
	}
}
func (r *Runtime) Run(ctx context.Context) error {
	if r.RecoveryInterval <= 0 {
		return fmt.Errorf("runtime recovery interval must be positive")
	}
	r.initialize()
	if _, err := r.Recovery.Reconcile(ctx); err != nil {
		return fmt.Errorf("startup recovery: %w", err)
	}
	if _, err := r.Discovery.Scan(ctx); err != nil {
		return fmt.Errorf("startup discovery: %w", err)
	}
	errors := make(chan error, 2)
	if r.Worker != nil {
		go func() {
			if err := r.Worker.Run(ctx, r.RecoveryInterval); err != nil {
				errors <- err
			}
		}()
	}
	if r.Migration != nil {
		go func() {
			if err := r.Migration.Run(ctx); err != nil {
				errors <- err
			}
		}()
	}
	ticker := time.NewTicker(r.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errors:
			return err
		case <-r.discovery:
			_, _ = r.Discovery.Scan(ctx)
		case <-ticker.C:
			_, _ = r.Discovery.Scan(ctx)
			_, _ = r.Recovery.Reconcile(ctx)
		}
	}
}

func (r *Runtime) initialize() {
	r.once.Do(func() { r.discovery = make(chan struct{}, 1) })
}
