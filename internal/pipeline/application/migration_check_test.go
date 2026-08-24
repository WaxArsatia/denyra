package application_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestMigrationCheckResolvesFilterAwareSelectionAndCreatesOnlyExplicitBatch(t *testing.T) {
	store := newMigrationCheckStore()
	store.selected = []string{"release-3", "release-1"}
	service := application.MigrationCheckService{Store: store, Identity: migrationIdentity{}, Now: fixedMigrationTime}
	ids, err := service.ResolveSelection(context.Background(), application.Selection{SelectAll: true, Filter: application.UnmanagedFilter{Query: "Kaleb", Status: "IMPORTED"}})
	if err != nil || !slices.Equal(ids, []string{"release-1", "release-3"}) {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if store.filter.Query != "Kaleb" || store.filter.Status != "IMPORTED" || len(store.batches) != 0 {
		t.Fatalf("selection changed state or lost filter: filter=%+v batches=%d", store.filter, len(store.batches))
	}
	batch, items, err := service.CreateBatch(context.Background(), application.Selection{ReleaseIDs: ids}, "admin-1")
	if err != nil || batch.ID == "" || len(items) != 2 || len(store.batches) != 1 {
		t.Fatalf("batch=%+v items=%+v err=%v", batch, items, err)
	}
	for _, item := range items {
		if item.State != domain.MigrationCheckPending || item.StateRevision != 0 || item.BatchID != batch.ID || item.IdempotencyKey == "" {
			t.Fatalf("initial migration item=%+v", item)
		}
	}
}

func TestMigrationCheckPersistsIndependentMixedOutcomesAndRetryState(t *testing.T) {
	store := newMigrationCheckStore()
	service := application.MigrationCheckService{Store: store, Identity: migrationIdentity{}, Now: fixedMigrationTime}
	ids := []string{"ambiguous", "error", "exact", "none"}
	for _, id := range ids {
		store.releases[id] = migrationRelease(id)
	}
	_, items, err := service.CreateBatch(context.Background(), application.Selection{ReleaseIDs: ids}, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		_, _ = service.CheckItem(context.Background(), item.ID)
	}
	want := map[string]domain.MigrationState{
		"ambiguous": domain.MigrationAmbiguous,
		"error":     domain.MigrationFailedRetryable,
		"exact":     domain.MigrationExactMatch,
		"none":      domain.MigrationNoMatch,
	}
	for _, item := range store.items {
		if item.State != want[item.UnmanagedCandidateID] {
			t.Errorf("%s state=%s want=%s", item.UnmanagedCandidateID, item.State, want[item.UnmanagedCandidateID])
		}
		if len(item.RequestEvidence) == 0 || item.StateRevision != 2 {
			t.Errorf("%s missing durable evidence/revision: %+v", item.UnmanagedCandidateID, item)
		}
		if item.UnmanagedCandidateID == "exact" && item.ApprovedReleaseMBID != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("exact release MBID=%q", item.ApprovedReleaseMBID)
		}
	}
	if len(store.failures) != 1 || store.failures[0].ItemID == "" || store.failures[0].State != domain.MigrationChecking {
		t.Fatalf("append-only failures=%+v", store.failures)
	}

	restarted := application.MigrationCheckService{Store: store, Identity: migrationIdentity{}, Now: fixedMigrationTime}
	for _, item := range store.items {
		loaded, loadErr := restarted.Item(context.Background(), item.ID)
		if loadErr != nil || loaded.State != item.State || loaded.StateRevision != item.StateRevision {
			t.Fatalf("restarted item=%+v err=%v want=%+v", loaded, loadErr, item)
		}
	}
}

type migrationIdentity struct{}

func (migrationIdentity) Decide(_ context.Context, plan domain.MetadataPlan, _ domain.TechnicalReleaseResult) (application.IdentityDecision, error) {
	switch plan.Album {
	case "none":
		return application.IdentityDecision{Status: application.IdentityNoMatch, Reason: "none"}, nil
	case "ambiguous":
		return application.IdentityDecision{Status: application.IdentityAmbiguous, Reason: "many", Candidates: []application.IdentityCandidate{{}, {}}}, nil
	case "exact":
		return application.IdentityDecision{Status: application.IdentityExact, Exact: &application.IdentityCandidate{Release: domain.CanonicalRelease{ReleaseMBID: "11111111-1111-1111-1111-111111111111"}}}, nil
	case "error":
		return application.IdentityDecision{Status: application.IdentityError, Reason: "temporary"}, errors.New("temporary MusicBrainz failure")
	default:
		return application.IdentityDecision{}, errors.New("unexpected album")
	}
}

type migrationCheckStore struct {
	selected []string
	filter   application.UnmanagedFilter
	batches  map[string]domain.MigrationBatch
	items    map[string]domain.MigrationItem
	releases map[string]domain.UnmanagedRelease
	failures []domain.MigrationItemError
}

func newMigrationCheckStore() *migrationCheckStore {
	return &migrationCheckStore{batches: map[string]domain.MigrationBatch{}, items: map[string]domain.MigrationItem{}, releases: map[string]domain.UnmanagedRelease{}}
}

func (s *migrationCheckStore) SelectUnmanaged(_ context.Context, filter application.UnmanagedFilter) ([]string, error) {
	s.filter = filter
	return append([]string(nil), s.selected...), nil
}

func (s *migrationCheckStore) PutMigrationBatch(_ context.Context, batch domain.MigrationBatch, items []domain.MigrationItem) error {
	s.batches[batch.ID] = batch
	for _, item := range items {
		s.items[item.ID] = item
	}
	return nil
}

func (s *migrationCheckStore) MigrationItem(_ context.Context, itemID string) (domain.MigrationItem, error) {
	item, ok := s.items[itemID]
	if !ok {
		return item, errors.New("not found")
	}
	return item, nil
}

func (s *migrationCheckStore) UnmanagedRelease(_ context.Context, candidateID string) (domain.UnmanagedRelease, error) {
	release, ok := s.releases[candidateID]
	if !ok {
		return release, errors.New("not found")
	}
	return release, nil
}

func (s *migrationCheckStore) UpdateMigrationItem(_ context.Context, itemID string, expected uint64, next domain.MigrationItem, failure *domain.MigrationItemError) error {
	current, ok := s.items[itemID]
	if !ok || current.StateRevision != expected || next.StateRevision != expected+1 {
		return errors.New("stale migration revision")
	}
	s.items[itemID] = next
	if failure != nil {
		s.failures = append(s.failures, *failure)
	}
	return nil
}

func migrationRelease(id string) domain.UnmanagedRelease {
	return domain.UnmanagedRelease{CandidateID: id, Status: "IMPORTED", Plan: domain.UnmanagedPlan{Metadata: domain.MetadataPlan{AlbumArtist: "Kaleb J", Album: id, TrackTotal: 1, DiscTotal: 1, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Track", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1}}}}, Evidence: domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{{RelativePath: "01.flac", Info: domain.TechnicalInfo{DurationMS: 1}}}}}
}

func fixedMigrationTime() time.Time { return time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC) }

func sortedMigrationItems(items map[string]domain.MigrationItem) []domain.MigrationItem {
	result := make([]domain.MigrationItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
