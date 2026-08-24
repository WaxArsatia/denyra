package application

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type PrimaryCompletionReconciler interface {
	Reconcile(context.Context) (int, error)
}

type PrimaryCompletionMonitor struct {
	Service PrimaryCompletionReconciler
	Safety  time.Duration
	OnError func(error)
	events  chan struct{}
	once    sync.Once
}

func (monitor *PrimaryCompletionMonitor) Notify() {
	monitor.initialize()
	select {
	case monitor.events <- struct{}{}:
	default:
	}
}

func (monitor *PrimaryCompletionMonitor) Run(ctx context.Context) error {
	if monitor.Service == nil || monitor.Safety <= 0 {
		return fmt.Errorf("primary completion monitor is not configured")
	}
	monitor.initialize()
	monitor.reconcile(ctx)
	ticker := time.NewTicker(monitor.Safety)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-monitor.events:
			monitor.reconcile(ctx)
		case <-ticker.C:
			monitor.reconcile(ctx)
		}
	}
}

func (monitor *PrimaryCompletionMonitor) reconcile(ctx context.Context) {
	if _, err := monitor.Service.Reconcile(ctx); err != nil && monitor.OnError != nil {
		monitor.OnError(err)
	}
}

func (monitor *PrimaryCompletionMonitor) initialize() {
	monitor.once.Do(func() { monitor.events = make(chan struct{}, 1) })
}
