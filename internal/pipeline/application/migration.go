package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type ConfirmedSelection struct {
	ItemID           string
	ExpectedRevision uint64
	ReleaseMBID      string
}

type MigrationStore interface {
	MigrationItem(context.Context, string) (domain.MigrationItem, error)
	UnmanagedRelease(context.Context, string) (domain.UnmanagedRelease, error)
	ConfirmMigrationItem(context.Context, string, uint64, string, string, time.Time) (domain.MigrationItem, error)
	UpdateMigrationItem(context.Context, string, uint64, domain.MigrationItem, *domain.MigrationItemError) error
	SaveMigrationEvidence(context.Context, string, uint64, []byte, time.Time) error
	ImportIntentForCandidate(context.Context, string) (domain.ImportIntent, error)
}

type ManagedMigrationImport interface {
	Submit(context.Context, string, string, string, ImportAuthorization, int) (ImportSubmission, error)
	Reconcile(context.Context, domain.ImportIntent) (domain.ImportVerification, error)
}

type MigrationEvidence struct {
	Identity     IdentityCandidate       `json:"identity"`
	Catalog      CatalogResult           `json:"catalog"`
	Mutation     MigrationMutationResult `json:"mutation"`
	OriginalPath string                  `json:"original_path"`
	ApprovedPath string                  `json:"approved_path"`
}

type MigrationService struct {
	Store         MigrationStore
	Identity      MigrationIdentity
	Catalog       LidarrCatalogService
	Mutation      MigrationMutator
	Import        ManagedMigrationImport
	Navidrome     NavidromeLibrary
	UnmanagedRoot string
	ApprovedRoot  string
	MoveNoReplace func(string, string) error
	ScanPoll      time.Duration
	Now           func() time.Time
}

func (s MigrationService) ConfirmSelected(ctx context.Context, selections []ConfirmedSelection, actor string) error {
	if s.Store == nil || strings.TrimSpace(actor) == "" || len(selections) == 0 {
		return fmt.Errorf("migration store, actor, and selections are required")
	}
	for _, selection := range selections {
		if _, err := domain.CanonicalMBID(selection.ReleaseMBID); err != nil {
			return err
		}
		if _, err := s.Store.ConfirmMigrationItem(ctx, selection.ItemID, selection.ExpectedRevision, selection.ReleaseMBID, actor, s.now()); err != nil {
			return err
		}
	}
	return nil
}

func (s MigrationService) Retry(ctx context.Context, itemID string, expected uint64, actor string) error {
	if s.Store == nil || strings.TrimSpace(actor) == "" {
		return fmt.Errorf("migration store and actor are required")
	}
	item, err := s.Store.MigrationItem(ctx, itemID)
	if err != nil {
		return err
	}
	if item.StateRevision != expected || item.State != domain.MigrationFailedRetryable {
		return fmt.Errorf("migration retry conflicts with current state or revision")
	}
	next, err := domain.TransitionMigration(item, item.ResumeState, s.now())
	if err != nil {
		return err
	}
	return s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil)
}

func (s MigrationService) Process(ctx context.Context, itemID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	item, err := s.Store.MigrationItem(ctx, itemID)
	if err != nil {
		return err
	}
	if item.State == domain.MigrationFailedRetryable {
		next, transitionErr := domain.TransitionMigration(item, item.ResumeState, s.now())
		if transitionErr != nil {
			return transitionErr
		}
		if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
			return err
		}
		item = next
	}
	if item.State == domain.MigrationMigrated {
		return nil
	}
	if item.State == domain.MigrationReconciling || item.State == domain.MigrationImportSubmitted {
		_, err := s.Reconcile(ctx, item.ID)
		return err
	}
	release, err := s.Store.UnmanagedRelease(ctx, item.UnmanagedCandidateID)
	if err != nil {
		return err
	}
	if item.State == domain.MigrationConfirmed {
		decision, identityErr := s.Identity.Decide(ctx, release.Plan.Metadata, release.Evidence)
		if identityErr != nil || decision.Status != IdentityExact || decision.Exact == nil || decision.Exact.Release.ReleaseMBID != item.ApprovedReleaseMBID {
			if identityErr == nil {
				identityErr = fmt.Errorf("approved MusicBrainz identity drifted")
			}
			return s.fail(ctx, item, identityErr)
		}
		catalog, catalogErr := s.Catalog.EnsureRelease(ctx, decision.Exact.Release)
		if catalogErr != nil {
			return s.fail(ctx, item, catalogErr)
		}
		evidence := MigrationEvidence{Identity: *decision.Exact, Catalog: catalog, OriginalPath: release.FinalPath, ApprovedPath: filepath.Join(s.ApprovedRoot, release.CandidateID)}
		encoded, _ := json.Marshal(evidence)
		next, transitionErr := domain.TransitionMigration(item, domain.MigrationLidarrCatalogReady, s.now())
		if transitionErr != nil {
			return transitionErr
		}
		next.MigrationEvidence = encoded
		if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
			return err
		}
		item = next
	}
	if item.State != domain.MigrationLidarrCatalogReady {
		return fmt.Errorf("migration item %s is not processable from %s", item.ID, item.State)
	}
	evidence, err := decodeMigrationEvidence(item)
	if err != nil {
		return s.fail(ctx, item, err)
	}
	if err := s.stage(evidence.OriginalPath, evidence.ApprovedPath); err != nil {
		return s.fail(ctx, item, err)
	}
	plans, err := migrationTagPlans(release.Plan, evidence.Identity)
	if err != nil {
		return s.rollback(ctx, item, evidence, err)
	}
	mutation, err := s.Mutation.Apply(ctx, release.CandidateID, plans)
	evidence.Mutation = mutation
	encoded, _ := json.Marshal(evidence)
	if saveErr := s.Store.SaveMigrationEvidence(ctx, item.ID, item.StateRevision, encoded, s.now()); saveErr != nil {
		if err == nil {
			err = saveErr
		}
	}
	item.MigrationEvidence = encoded
	if err != nil {
		return s.rollback(ctx, item, evidence, err)
	}
	intent, intentErr := s.Store.ImportIntentForCandidate(ctx, release.CandidateID)
	if errors.Is(intentErr, sql.ErrNoRows) {
		submission, submitErr := s.Import.Submit(ctx, release.CandidateID, item.ApprovedReleaseMBID, "", ImportManualApproved, evidence.Catalog.AlbumReleaseID)
		if submitErr != nil && submission.Intent.ID == "" {
			return s.rollback(ctx, item, evidence, submitErr)
		}
		intent = submission.Intent
		intentErr = submitErr
	} else if intentErr != nil {
		return s.rollback(ctx, item, evidence, intentErr)
	}
	if intent.ID == "" {
		return s.rollback(ctx, item, evidence, fmt.Errorf("managed import omitted durable intent"))
	}
	next, err := domain.TransitionMigration(item, domain.MigrationImportSubmitted, s.now())
	if err != nil {
		return err
	}
	if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
		return err
	}
	reconciling, err := domain.TransitionMigration(next, domain.MigrationReconciling, s.now())
	if err != nil {
		return err
	}
	if err := s.Store.UpdateMigrationItem(ctx, item.ID, next.StateRevision, reconciling, nil); err != nil {
		return err
	}
	if intentErr != nil {
		return nil
	}
	_, err = s.Reconcile(ctx, item.ID)
	return err
}

func (s MigrationService) Reconcile(ctx context.Context, itemID string) (bool, error) {
	item, err := s.Store.MigrationItem(ctx, itemID)
	if err != nil {
		return false, err
	}
	if item.State == domain.MigrationFailedRetryable && item.ResumeState == domain.MigrationReconciling {
		next, transitionErr := domain.TransitionMigration(item, domain.MigrationReconciling, s.now())
		if transitionErr != nil {
			return false, transitionErr
		}
		if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
			return false, err
		}
		item = next
	}
	if item.State == domain.MigrationImportSubmitted {
		next, transitionErr := domain.TransitionMigration(item, domain.MigrationReconciling, s.now())
		if transitionErr != nil {
			return false, transitionErr
		}
		if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
			return false, err
		}
		item = next
	}
	if item.State == domain.MigrationMigrated {
		return true, nil
	}
	if item.State != domain.MigrationReconciling {
		return false, fmt.Errorf("migration item %s is not reconciling", item.ID)
	}
	intent, err := s.Store.ImportIntentForCandidate(ctx, item.UnmanagedCandidateID)
	if err != nil {
		return false, s.fail(ctx, item, err)
	}
	verification, err := s.Import.Reconcile(ctx, intent)
	if err != nil {
		return false, s.fail(ctx, item, err)
	}
	if !verification.Complete {
		return false, nil
	}
	evidence, err := decodeMigrationEvidence(item)
	if err != nil {
		return false, s.fail(ctx, item, err)
	}
	managedID, unmanagedID, _, err := s.Navidrome.EnsureLibraries(ctx)
	if err == nil {
		err = s.Navidrome.StartScan(ctx, managedID, unmanagedID)
	}
	if err == nil {
		err = s.Navidrome.WaitScan(ctx, s.ScanPoll)
	}
	identity := navidrome.ReleaseIdentity{AlbumArtist: joinCredits(evidence.Identity.Release.ArtistCredits), Album: evidence.Identity.Release.Title, TrackCount: len(evidence.Identity.Release.Tracks)}
	var managedVisible, unmanagedVisible bool
	if err == nil {
		managedVisible, err = s.Navidrome.ReleaseVisible(ctx, managedID, identity)
	}
	if err == nil {
		unmanagedVisible, err = s.Navidrome.ReleaseVisible(ctx, unmanagedID, identity)
	}
	if err != nil || !managedVisible || unmanagedVisible {
		if err == nil {
			err = fmt.Errorf("Navidrome migration visibility managed=%v unmanaged=%v", managedVisible, unmanagedVisible)
		}
		return false, s.fail(ctx, item, err)
	}
	next, err := domain.TransitionMigration(item, domain.MigrationMigrated, s.now())
	if err != nil {
		return false, err
	}
	if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, nil); err != nil {
		return false, err
	}
	if err := s.Mutation.Cleanup(item.UnmanagedCandidateID); err != nil {
		return false, err
	}
	return true, nil
}

func (s MigrationService) rollback(ctx context.Context, item domain.MigrationItem, evidence MigrationEvidence, cause error) error {
	if len(evidence.Mutation.Files) > 0 {
		if err := s.Mutation.Restore(ctx, item.UnmanagedCandidateID, evidence.Mutation); err != nil {
			return s.fail(ctx, item, fmt.Errorf("%v; restore failed: %w", cause, err))
		}
	}
	if _, err := os.Stat(evidence.ApprovedPath); err == nil {
		move := s.MoveNoReplace
		if move == nil {
			move = denyrafs.MoveNoReplace
		}
		if err := os.MkdirAll(filepath.Dir(evidence.OriginalPath), 0o750); err != nil {
			return s.fail(ctx, item, fmt.Errorf("%v; rollback parent: %w", cause, err))
		}
		if err := move(evidence.ApprovedPath, evidence.OriginalPath); err != nil {
			return s.fail(ctx, item, fmt.Errorf("%v; rollback move: %w", cause, err))
		}
	}
	return s.fail(ctx, item, cause)
}

func (s MigrationService) stage(original, approved string) error {
	if !containedMigrationRoot(s.UnmanagedRoot, original) || filepath.Clean(approved) != filepath.Join(s.ApprovedRoot, filepath.Base(approved)) {
		return fmt.Errorf("migration staging path escaped configured roots")
	}
	originalInfo, originalErr := os.Stat(original)
	approvedInfo, approvedErr := os.Stat(approved)
	switch {
	case originalErr == nil && approvedErr == nil:
		return fmt.Errorf("migration source and approved staging both exist")
	case originalErr == nil && os.IsNotExist(approvedErr):
		if !originalInfo.IsDir() {
			return fmt.Errorf("unmanaged release path is not a directory")
		}
		if err := os.MkdirAll(s.ApprovedRoot, 0o750); err != nil {
			return err
		}
		move := s.MoveNoReplace
		if move == nil {
			move = denyrafs.MoveNoReplace
		}
		return move(original, approved)
	case os.IsNotExist(originalErr) && approvedErr == nil && approvedInfo.IsDir():
		return nil
	default:
		return fmt.Errorf("migration staging paths are unavailable: original=%v approved=%v", originalErr, approvedErr)
	}
}

func (s MigrationService) fail(ctx context.Context, item domain.MigrationItem, cause error) error {
	next, err := domain.TransitionMigration(item, domain.MigrationFailedRetryable, s.now())
	if err != nil {
		return err
	}
	errorID, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	failure := &domain.MigrationItemError{ID: errorID, ItemID: item.ID, State: item.State, Message: cause.Error(), CreatedAt: s.now()}
	if err := s.Store.UpdateMigrationItem(ctx, item.ID, item.StateRevision, next, failure); err != nil {
		return err
	}
	return cause
}

func migrationTagPlans(plan domain.UnmanagedPlan, identity IdentityCandidate) (map[string]domain.TagSet, error) {
	canonical := make(map[[2]int]domain.CanonicalTrack, len(identity.Release.Tracks))
	for _, track := range identity.Release.Tracks {
		canonical[[2]int{track.Disc, track.Track}] = track
	}
	targets := make(map[string]string, len(plan.Files))
	for _, file := range plan.Files {
		if file.Kind == "FLAC" {
			targets[file.SourceRelative] = file.TargetRelative
		}
	}
	result := make(map[string]domain.TagSet, len(plan.Metadata.Tracks))
	for _, local := range plan.Metadata.Tracks {
		track, ok := canonical[[2]int{local.Disc, local.Track}]
		if !ok {
			return nil, fmt.Errorf("canonical migration track %d/%d missing", local.Disc, local.Track)
		}
		artists := track.ArtistCredits
		if len(artists) == 0 {
			artists = identity.Release.ArtistCredits
		}
		tags, err := domain.CanonicalTags(domain.TagInput{Title: track.Title, Artists: artists, Album: identity.Release.Title, AlbumArtists: identity.Release.ArtistCredits, TrackNumber: track.Track, DiscNumber: track.Disc, Date: identity.Release.Date, ISRCs: track.ISRCs, RecordingMBID: track.RecordingMBID, ReleaseTrackMBID: track.ReleaseTrackMBID, ReleaseMBID: identity.Release.ReleaseMBID, ReleaseGroupMBID: identity.Release.ReleaseGroupMBID})
		if err != nil {
			return nil, err
		}
		target := targets[local.RelativePath]
		if target == "" {
			target = local.RelativePath
		}
		result[target] = tags
	}
	return result, nil
}

func decodeMigrationEvidence(item domain.MigrationItem) (MigrationEvidence, error) {
	var evidence MigrationEvidence
	if len(item.MigrationEvidence) == 0 {
		return evidence, fmt.Errorf("migration evidence is missing")
	}
	if err := json.Unmarshal(item.MigrationEvidence, &evidence); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func containedMigrationRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s MigrationService) validate() error {
	if s.Store == nil || s.Identity == nil || s.Catalog.Catalog == nil || s.Mutation == nil || s.Import == nil || s.UnmanagedRoot == "" || s.ApprovedRoot == "" {
		return fmt.Errorf("migration service is not configured")
	}
	return nil
}

func (s MigrationService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
