package application_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

const applicationMigrationReleaseMBID = "11111111-1111-1111-1111-111111111111"

func TestMigrationConfirmationAcceptsOnlySameExactReleaseAndRevision(t *testing.T) {
	store := newMigrationServiceStore()
	store.items["exact"] = domain.MigrationItem{ID: "exact", State: domain.MigrationExactMatch, StateRevision: 2, ApprovedReleaseMBID: applicationMigrationReleaseMBID}
	store.items["none"] = domain.MigrationItem{ID: "none", State: domain.MigrationNoMatch, StateRevision: 2}
	store.items["ambiguous"] = domain.MigrationItem{ID: "ambiguous", State: domain.MigrationAmbiguous, StateRevision: 2}
	service := application.MigrationService{Store: store, Now: fixedMigrationTime}
	if err := service.ConfirmSelected(context.Background(), []application.ConfirmedSelection{{ItemID: "none", ExpectedRevision: 2, ReleaseMBID: applicationMigrationReleaseMBID}}, "admin-1"); err == nil {
		t.Fatal("NO_MATCH item confirmed")
	}
	if err := service.ConfirmSelected(context.Background(), []application.ConfirmedSelection{{ItemID: "ambiguous", ExpectedRevision: 2, ReleaseMBID: applicationMigrationReleaseMBID}}, "admin-1"); err == nil {
		t.Fatal("AMBIGUOUS item confirmed")
	}
	if err := service.ConfirmSelected(context.Background(), []application.ConfirmedSelection{{ItemID: "exact", ExpectedRevision: 1, ReleaseMBID: applicationMigrationReleaseMBID}}, "admin-1"); err == nil {
		t.Fatal("stale exact item confirmed")
	}
	if err := service.ConfirmSelected(context.Background(), []application.ConfirmedSelection{{ItemID: "exact", ExpectedRevision: 2, ReleaseMBID: "22222222-2222-2222-2222-222222222222"}}, "admin-1"); err == nil {
		t.Fatal("different exact release confirmed")
	}
	if err := service.ConfirmSelected(context.Background(), []application.ConfirmedSelection{{ItemID: "exact", ExpectedRevision: 2, ReleaseMBID: applicationMigrationReleaseMBID}}, "admin-1"); err != nil {
		t.Fatal(err)
	}
	if store.items["exact"].State != domain.MigrationConfirmed || store.items["exact"].StateRevision != 3 || store.confirmActor != "admin-1" {
		t.Fatalf("confirmed item=%+v actor=%q", store.items["exact"], store.confirmActor)
	}
}

func TestMigrationIdentityDriftFailsBeforeExternalOrFilesystemEffects(t *testing.T) {
	root := t.TempDir()
	unmanaged := filepath.Join(root, "unmanaged", "Artist", "Album")
	if err := os.MkdirAll(unmanaged, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmanaged, "01.flac"), []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	store := newMigrationServiceStore()
	store.items["item"] = domain.MigrationItem{ID: "item", UnmanagedCandidateID: "candidate", State: domain.MigrationConfirmed, StateRevision: 3, ApprovedReleaseMBID: applicationMigrationReleaseMBID}
	store.releases["candidate"] = domain.UnmanagedRelease{CandidateID: "candidate", FinalPath: unmanaged, Status: "IMPORTED", Plan: migrationRelease("none").Plan, Evidence: migrationRelease("none").Evidence}
	catalog := &countingMigrationCatalog{}
	mutation := &countingMigrationMutation{}
	imports := &countingManagedImport{}
	service := application.MigrationService{Store: store, Identity: migrationIdentity{}, Catalog: application.LidarrCatalogService{Catalog: catalog}, Mutation: mutation, Import: imports, UnmanagedRoot: filepath.Join(root, "unmanaged"), ApprovedRoot: filepath.Join(root, "approved"), Now: fixedMigrationTime}
	if err := service.Process(context.Background(), "item"); err == nil {
		t.Fatal("identity drift accepted")
	}
	if store.items["item"].State != domain.MigrationFailedRetryable || store.items["item"].ResumeState != domain.MigrationConfirmed {
		t.Fatalf("identity drift state=%+v", store.items["item"])
	}
	if catalog.calls != 0 || mutation.apply != 0 || imports.submit != 0 {
		t.Fatalf("identity drift effects catalog=%d mutation=%d import=%d", catalog.calls, mutation.apply, imports.submit)
	}
	if data, err := os.ReadFile(filepath.Join(unmanaged, "01.flac")); err != nil || string(data) != "original" {
		t.Fatalf("unmanaged release changed: data=%q err=%v", data, err)
	}
}

type migrationServiceStore struct {
	items        map[string]domain.MigrationItem
	releases     map[string]domain.UnmanagedRelease
	failures     []domain.MigrationItemError
	confirmActor string
}

func newMigrationServiceStore() *migrationServiceStore {
	return &migrationServiceStore{items: map[string]domain.MigrationItem{}, releases: map[string]domain.UnmanagedRelease{}}
}

func (s *migrationServiceStore) MigrationItem(_ context.Context, id string) (domain.MigrationItem, error) {
	item, ok := s.items[id]
	if !ok {
		return item, errors.New("not found")
	}
	return item, nil
}

func (s *migrationServiceStore) UnmanagedRelease(_ context.Context, id string) (domain.UnmanagedRelease, error) {
	release, ok := s.releases[id]
	if !ok {
		return release, errors.New("not found")
	}
	return release, nil
}

func (s *migrationServiceStore) ConfirmMigrationItem(_ context.Context, id string, expected uint64, releaseMBID, actor string, at time.Time) (domain.MigrationItem, error) {
	item, ok := s.items[id]
	if !ok || item.StateRevision != expected || item.State != domain.MigrationExactMatch || item.ApprovedReleaseMBID != releaseMBID {
		return item, errors.New("confirmation conflict")
	}
	next, err := domain.TransitionMigration(item, domain.MigrationConfirmed, at)
	if err != nil {
		return item, err
	}
	s.items[id], s.confirmActor = next, actor
	return next, nil
}

func (s *migrationServiceStore) UpdateMigrationItem(_ context.Context, id string, expected uint64, next domain.MigrationItem, failure *domain.MigrationItemError) error {
	if s.items[id].StateRevision != expected {
		return errors.New("stale")
	}
	s.items[id] = next
	if failure != nil {
		s.failures = append(s.failures, *failure)
	}
	return nil
}

func (s *migrationServiceStore) SaveMigrationEvidence(_ context.Context, id string, expected uint64, evidence []byte, _ time.Time) error {
	item := s.items[id]
	if item.StateRevision != expected {
		return errors.New("stale")
	}
	item.MigrationEvidence = append([]byte(nil), evidence...)
	s.items[id] = item
	return nil
}

func (s *migrationServiceStore) ImportIntentForCandidate(context.Context, string) (domain.ImportIntent, error) {
	return domain.ImportIntent{}, sql.ErrNoRows
}

type countingMigrationCatalog struct{ calls int }

func (c *countingMigrationCatalog) EnsureRelease(context.Context, domain.CanonicalRelease) (application.CatalogResult, error) {
	c.calls++
	return application.CatalogResult{ArtistID: 1, AlbumID: 2, AlbumReleaseID: 3}, nil
}

type countingMigrationMutation struct{ apply, restore, cleanup int }

func (m *countingMigrationMutation) Apply(context.Context, string, map[string]domain.TagSet) (application.MigrationMutationResult, error) {
	m.apply++
	return application.MigrationMutationResult{}, nil
}
func (m *countingMigrationMutation) Restore(context.Context, string, application.MigrationMutationResult) error {
	m.restore++
	return nil
}
func (m *countingMigrationMutation) Cleanup(string) error { m.cleanup++; return nil }

type countingManagedImport struct{ submit, reconcile int }

func (i *countingManagedImport) Submit(context.Context, string, string, string, application.ImportAuthorization, int) (application.ImportSubmission, error) {
	i.submit++
	return application.ImportSubmission{}, nil
}
func (i *countingManagedImport) Reconcile(context.Context, domain.ImportIntent) (domain.ImportVerification, error) {
	i.reconcile++
	return domain.ImportVerification{}, nil
}
