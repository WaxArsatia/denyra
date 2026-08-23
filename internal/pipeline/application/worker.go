package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/storage"
)

var ErrMaintenance = errors.New("pipeline is in maintenance mode")
var ErrStorageAdmission = errors.New("storage admission threshold reached")

type AdmissionGate struct {
	DataRoot           string
	MinimumFreeBytes   int64
	MinimumFreePercent float64
	maintenance        atomic.Bool
}

func (g *AdmissionGate) SetMaintenance(enabled bool) { g.maintenance.Store(enabled) }
func (g *AdmissionGate) AllowNew() error {
	if g.maintenance.Load() {
		return ErrMaintenance
	}
	result, err := storage.Evaluate(g.DataRoot, uint64(g.MinimumFreeBytes), g.MinimumFreePercent, nil)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("%w: free=%d required=%d", ErrStorageAdmission, result.AvailableBytes, result.RequiredBytes)
	}
	return nil
}

type WorkItem struct {
	CandidateID       string
	Revision          uint64
	ConfigSnapshotID  string
	AdmissionRequired bool
}
type WorkStore interface {
	ReadyWork(context.Context, int) ([]WorkItem, error)
	AcquireWorkLease(context.Context, WorkItem, string, time.Time, time.Time) error
	ReleaseWorkLease(context.Context, string, string) error
}
type CandidateProcessor interface {
	Process(context.Context, WorkItem) error
}

type Worker struct {
	Store         WorkStore
	Processor     CandidateProcessor
	Admission     *AdmissionGate
	Concurrency   int
	LeaseDuration time.Duration
	OwnerID       string
	Queue         chan string
	Now           func() time.Time
	OnError       func(string, error)
	once          sync.Once
}

func (w *Worker) Notify(candidateID string) {
	w.initialize()
	select {
	case w.Queue <- candidateID:
	default:
	}
}
func (w *Worker) Run(ctx context.Context, scanInterval time.Duration) error {
	if w.Store == nil || w.Processor == nil || w.Admission == nil || w.Concurrency <= 0 || w.LeaseDuration <= 0 || scanInterval <= 0 || w.OwnerID == "" {
		return fmt.Errorf("worker is not configured")
	}
	w.initialize()
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, w.Concurrency)
	scan := time.NewTicker(scanInterval)
	defer scan.Stop()
	defer wait.Wait()
	dispatch := func() {
		items, err := w.Store.ReadyWork(ctx, w.Concurrency*2)
		if err != nil {
			return
		}
		for _, item := range items {
			if item.AdmissionRequired && w.Admission.AllowNew() != nil {
				return
			}
			now := time.Now().UTC()
			if w.Now != nil {
				now = w.Now().UTC()
			}
			if err := w.Store.AcquireWorkLease(ctx, item, w.OwnerID, now, now.Add(w.LeaseDuration)); err != nil {
				continue
			}
			semaphore <- struct{}{}
			wait.Add(1)
			go func(item WorkItem) {
				defer wait.Done()
				defer func() { <-semaphore }()
				processContext, cancel := context.WithCancel(ctx)
				defer cancel()
				stopRenewal := make(chan struct{})
				go w.renewLease(processContext, item, now.Add(w.LeaseDuration), cancel, stopRenewal)
				err := w.Processor.Process(processContext, item)
				cancel()
				<-stopRenewal
				if err != nil && w.OnError != nil {
					w.OnError(item.CandidateID, err)
				}
				_ = w.Store.ReleaseWorkLease(context.WithoutCancel(ctx), item.CandidateID, w.OwnerID)
			}(item)
		}
	}
	dispatch()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.Queue:
			dispatch()
		case <-scan.C:
			dispatch()
		}
	}
}

type renewableWorkStore interface {
	RenewWorkLease(context.Context, string, string, time.Time, time.Time) error
}

func (w *Worker) renewLease(ctx context.Context, item WorkItem, expiry time.Time, cancel context.CancelFunc, stopped chan<- struct{}) {
	defer close(stopped)
	store, ok := w.Store.(renewableWorkStore)
	if !ok {
		return
	}
	interval := w.LeaseDuration / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := w.now().Add(w.LeaseDuration)
			if err := store.RenewWorkLease(ctx, item.CandidateID, w.OwnerID, expiry, next); err != nil {
				cancel()
				if w.OnError != nil {
					w.OnError(item.CandidateID, err)
				}
				return
			}
			expiry = next
		}
	}
}

func (w *Worker) initialize() {
	w.once.Do(func() {
		if w.Queue == nil {
			w.Queue = make(chan string, max(1, w.Concurrency*4))
		}
	})
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}
