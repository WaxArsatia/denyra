package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
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
	var stat syscall.Statfs_t
	if err := syscall.Statfs(g.DataRoot, &stat); err != nil {
		return err
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	total := int64(stat.Blocks) * int64(stat.Bsize)
	required := g.MinimumFreeBytes
	percent := int64(float64(total) * g.MinimumFreePercent / 100)
	if percent > required {
		required = percent
	}
	if free < required {
		return fmt.Errorf("%w: free=%d required=%d", ErrStorageAdmission, free, required)
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
}

func (w *Worker) Notify(candidateID string) {
	select {
	case w.Queue <- candidateID:
	default:
	}
}
func (w *Worker) Run(ctx context.Context, scanInterval time.Duration) error {
	if w.Store == nil || w.Processor == nil || w.Admission == nil || w.Concurrency <= 0 || w.LeaseDuration <= 0 || scanInterval <= 0 || w.OwnerID == "" {
		return fmt.Errorf("worker is not configured")
	}
	if w.Queue == nil {
		w.Queue = make(chan string, w.Concurrency*4)
	}
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
				_ = w.Processor.Process(ctx, item)
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
