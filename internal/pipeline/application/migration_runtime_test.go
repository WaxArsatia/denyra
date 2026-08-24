package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestMigrationRuntimeProcessesOnlyDurableItemsAndButtonNotifications(t *testing.T) {
	store := &migrationRuntimeStore{ready: []domain.MigrationItem{{ID: "startup", State: domain.MigrationCheckPending}}}
	checker := &migrationRuntimeChecker{checked: make(chan string, 2)}
	runtime := &application.MigrationRuntime{Store: store, Check: checker, Concurrency: 2, LeaseDuration: time.Minute, OwnerID: "migration-worker", Now: fixedMigrationTime}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	if got := waitMigrationCheck(t, checker.checked); got != "startup" {
		t.Fatalf("startup item=%q", got)
	}
	store.add(domain.MigrationItem{ID: "button", State: domain.MigrationCheckPending})
	runtime.NotifyBatch("batch-2")
	if got := waitMigrationCheck(t, checker.checked); got != "button" {
		t.Fatalf("button item=%q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if store.acquired != 2 || store.released != 2 {
		t.Fatalf("leases acquired=%d released=%d", store.acquired, store.released)
	}
}

type migrationRuntimeStore struct {
	mu                 sync.Mutex
	ready              []domain.MigrationItem
	acquired, released int
}

func (s *migrationRuntimeStore) add(item domain.MigrationItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = append(s.ready, item)
}

func (s *migrationRuntimeStore) ReadyMigrationChecks(_ context.Context, limit int) ([]domain.MigrationItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit > len(s.ready) {
		limit = len(s.ready)
	}
	return append([]domain.MigrationItem(nil), s.ready[:limit]...), nil
}

func (s *migrationRuntimeStore) AcquireMigrationLease(_ context.Context, item domain.MigrationItem, _ string, _, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.ready {
		if s.ready[index].ID == item.ID {
			s.ready = append(s.ready[:index], s.ready[index+1:]...)
			break
		}
	}
	s.acquired++
	return nil
}

func (s *migrationRuntimeStore) ReleaseMigrationLease(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released++
	return nil
}

type migrationRuntimeChecker struct{ checked chan string }

func (c *migrationRuntimeChecker) CheckItem(_ context.Context, itemID string) (domain.MigrationItem, error) {
	c.checked <- itemID
	return domain.MigrationItem{ID: itemID}, nil
}

func waitMigrationCheck(t *testing.T, checked <-chan string) string {
	t.Helper()
	select {
	case item := <-checked:
		return item
	case <-time.After(2 * time.Second):
		t.Fatal("migration runtime did not process item")
		return ""
	}
}
