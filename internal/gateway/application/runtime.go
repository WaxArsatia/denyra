package application

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type GatewayRuntime struct {
	Discovery         WantedDiscovery
	Recovery          GatewayRecovery
	LatePrimary       LatePrimaryMonitor
	PrimaryCompletion *PrimaryCompletionMonitor
	Worker            *AcquisitionWorker
	Safety            time.Duration
	events            chan struct{}
	once              sync.Once
}

func (runtime *GatewayRuntime) NotifyLidarr() {
	runtime.initialize()
	select {
	case runtime.events <- struct{}{}:
	default:
	}
}

func (runtime *GatewayRuntime) NotifySlskd() {
	if runtime.PrimaryCompletion != nil {
		runtime.PrimaryCompletion.Notify()
	}
}

func (runtime *GatewayRuntime) Run(ctx context.Context) error {
	if runtime.Worker == nil || runtime.Safety <= 0 {
		return fmt.Errorf("gateway runtime is not configured")
	}
	runtime.initialize()
	if _, err := runtime.Recovery.Reconcile(ctx); err != nil {
		return fmt.Errorf("startup acquisition recovery: %w", err)
	}
	if _, err := runtime.Discovery.Reconcile(ctx); err != nil {
		// Lidarr is external. The worker retry model remains available and
		// readiness must stay true during an outage.
	}
	errors := make(chan error, 2)
	go func() { errors <- runtime.Worker.Run(ctx) }()
	if runtime.PrimaryCompletion != nil {
		go func() { errors <- runtime.PrimaryCompletion.Run(ctx) }()
	}
	ticker := time.NewTicker(runtime.Safety)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errors:
			return err
		case <-runtime.events:
			_, _ = runtime.Discovery.Reconcile(ctx)
			_, _ = runtime.LatePrimary.Reconcile(ctx)
			runtime.Worker.Notify("")
		case <-ticker.C:
			_, _ = runtime.Discovery.Reconcile(ctx)
			_, _ = runtime.LatePrimary.Reconcile(ctx)
			runtime.Worker.Notify("")
		}
	}
}

func (runtime *GatewayRuntime) initialize() {
	runtime.once.Do(func() { runtime.events = make(chan struct{}, 1) })
}
