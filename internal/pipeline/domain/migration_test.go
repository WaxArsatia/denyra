package domain_test

import (
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestMigrationStateAllowsOnlyDeclaredTransitionsAndResumesFailures(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	item := domain.MigrationItem{ID: "item-1", State: domain.MigrationCheckPending}
	var err error
	item, err = domain.TransitionMigration(item, domain.MigrationChecking, now)
	if err != nil || item.StateRevision != 1 || item.State != domain.MigrationChecking {
		t.Fatalf("pending to checking=%+v err=%v", item, err)
	}
	failed, err := domain.TransitionMigration(item, domain.MigrationFailedRetryable, now.Add(time.Second))
	if err != nil || failed.ResumeState != domain.MigrationChecking || failed.StateRevision != 2 {
		t.Fatalf("retryable transition=%+v err=%v", failed, err)
	}
	resumed, err := domain.TransitionMigration(failed, domain.MigrationChecking, now.Add(2*time.Second))
	if err != nil || resumed.State != domain.MigrationChecking || resumed.StateRevision != 3 {
		t.Fatalf("resume transition=%+v err=%v", resumed, err)
	}
	if _, err := domain.TransitionMigration(resumed, domain.MigrationConfirmed, now.Add(3*time.Second)); err == nil {
		t.Fatal("checking item skipped directly to confirmed")
	}
}

func TestMigrationStateCoversCheckAndMigrationLifecycle(t *testing.T) {
	allowed := [][2]domain.MigrationState{
		{domain.MigrationCheckPending, domain.MigrationChecking},
		{domain.MigrationChecking, domain.MigrationNoMatch},
		{domain.MigrationChecking, domain.MigrationAmbiguous},
		{domain.MigrationChecking, domain.MigrationExactMatch},
		{domain.MigrationExactMatch, domain.MigrationConfirmed},
		{domain.MigrationConfirmed, domain.MigrationLidarrCatalogReady},
		{domain.MigrationLidarrCatalogReady, domain.MigrationImportSubmitted},
		{domain.MigrationImportSubmitted, domain.MigrationReconciling},
		{domain.MigrationReconciling, domain.MigrationMigrated},
	}
	for _, transition := range allowed {
		item := domain.MigrationItem{ID: "item", State: transition[0]}
		if _, err := domain.TransitionMigration(item, transition[1], time.Now()); err != nil {
			t.Errorf("%s -> %s rejected: %v", transition[0], transition[1], err)
		}
	}
}
