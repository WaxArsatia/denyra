package application

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type countingCompletionReconciler struct {
	calls atomic.Int32
	done  chan struct{}
}

func (reconciler *countingCompletionReconciler) Reconcile(context.Context) (int, error) {
	reconciler.calls.Add(1)
	select {
	case reconciler.done <- struct{}{}:
	default:
	}
	return 0, nil
}

func TestPrimaryCompletionMonitorUsesEventAsImmediateTrigger(t *testing.T) {
	reconciler := &countingCompletionReconciler{done: make(chan struct{}, 2)}
	monitor := &PrimaryCompletionMonitor{Service: reconciler, Safety: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 1)
	go func() { errors <- monitor.Run(ctx) }()

	select {
	case <-reconciler.done:
	case <-time.After(time.Second):
		t.Fatal("startup completion reconciliation did not run")
	}
	monitor.Notify()
	select {
	case <-reconciler.done:
	case <-time.After(time.Second):
		t.Fatal("slskd event did not trigger immediate completion reconciliation")
	}
	if got := reconciler.calls.Load(); got != 2 {
		t.Fatalf("reconciliation calls=%d", got)
	}
	cancel()
	select {
	case err := <-errors:
		if err != nil {
			t.Fatalf("monitor shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop")
	}
}
