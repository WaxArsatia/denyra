package application

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type MigrationRuntimeStore interface {
	ReadyMigrationChecks(context.Context, int) ([]domain.MigrationItem, error)
	AcquireMigrationLease(context.Context, domain.MigrationItem, string, time.Time, time.Time) error
	ReleaseMigrationLease(context.Context, string, string) error
}

type MigrationChecker interface {
	CheckItem(context.Context, string) (domain.MigrationItem, error)
}

type MigrationCoordinator struct {
	Check     MigrationCheckService
	Migration MigrationService
}

func (c MigrationCoordinator) CheckItem(ctx context.Context, itemID string) (domain.MigrationItem, error) {
	item, err := c.Check.Item(ctx, itemID)
	if err != nil {
		return item, err
	}
	if item.State == domain.MigrationCheckPending || item.State == domain.MigrationChecking || item.State == domain.MigrationFailedRetryable && item.ResumeState == domain.MigrationChecking {
		return c.Check.CheckItem(ctx, itemID)
	}
	if err := c.Migration.Process(ctx, itemID); err != nil {
		return item, err
	}
	return c.Check.Item(ctx, itemID)
}

type MigrationRuntime struct {
	Store         MigrationRuntimeStore
	Check         MigrationChecker
	Concurrency   int
	LeaseDuration time.Duration
	OwnerID       string
	Now           func() time.Time
	OnError       func(string, error)
	notify        chan struct{}
	once          sync.Once
}

func (r *MigrationRuntime) NotifyBatch(_ string) {
	r.initialize()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *MigrationRuntime) Run(ctx context.Context) error {
	if r.Store == nil || r.Check == nil || r.Concurrency <= 0 || r.LeaseDuration <= 0 || r.OwnerID == "" {
		return fmt.Errorf("migration runtime is not configured")
	}
	r.initialize()
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, r.Concurrency)
	dispatch := func() {
		items, err := r.Store.ReadyMigrationChecks(ctx, r.Concurrency*2)
		if err != nil {
			if r.OnError != nil {
				r.OnError("", err)
			}
			return
		}
		for _, item := range items {
			now := r.now()
			if err := r.Store.AcquireMigrationLease(ctx, item, r.OwnerID, now, now.Add(r.LeaseDuration)); err != nil {
				continue
			}
			semaphore <- struct{}{}
			workers.Add(1)
			go func(item domain.MigrationItem) {
				defer workers.Done()
				defer func() { <-semaphore }()
				if _, err := r.Check.CheckItem(ctx, item.ID); err != nil && r.OnError != nil {
					r.OnError(item.ID, err)
				}
				_ = r.Store.ReleaseMigrationLease(context.WithoutCancel(ctx), item.ID, r.OwnerID)
			}(item)
		}
	}
	dispatch()
	defer workers.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.notify:
			dispatch()
		}
	}
}

func (r *MigrationRuntime) initialize() {
	r.once.Do(func() { r.notify = make(chan struct{}, 1) })
}

func (r *MigrationRuntime) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
