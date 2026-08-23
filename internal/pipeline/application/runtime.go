package application

import (
	"context"
	"fmt"
	"time"
)

type Runtime struct {
	Discovery        DiscoveryService
	Recovery         RecoveryService
	Worker           *Worker
	RecoveryInterval time.Duration
	discovery        chan struct{}
}

func (r *Runtime) NotifyManualDiscovery() {
	if r.discovery == nil {
		r.discovery = make(chan struct{}, 1)
	}
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
func (r *Runtime) Run(ctx context.Context) error {
	if r.RecoveryInterval <= 0 {
		return fmt.Errorf("runtime recovery interval must be positive")
	}
	if r.discovery == nil {
		r.discovery = make(chan struct{}, 1)
	}
	if _, err := r.Recovery.Reconcile(ctx); err != nil {
		return fmt.Errorf("startup recovery: %w", err)
	}
	if _, err := r.Discovery.Scan(ctx); err != nil {
		return fmt.Errorf("startup discovery: %w", err)
	}
	errors := make(chan error, 1)
	if r.Worker != nil {
		go func() { errors <- r.Worker.Run(ctx, r.RecoveryInterval) }()
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
