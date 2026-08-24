package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var ErrUnmanagedReview = errors.New("unmanaged import requires review")

type UnmanagedStore interface {
	Workflow(context.Context, string) (WorkflowContext, error)
	PutUnmanagedRelease(context.Context, domain.UnmanagedRelease, time.Time) error
	PutUnmanagedImportIntent(context.Context, domain.UnmanagedImportIntent, time.Time) error
	UnmanagedImportIntent(context.Context, string) (domain.UnmanagedImportIntent, error)
	UpdateUnmanagedImport(context.Context, domain.UnmanagedImportIntent, domain.UnmanagedRelease, time.Time) error
}

type UnmanagedMutator interface {
	MutateUnmanagedRelease(context.Context, string, map[string]domain.TagSet) (MutationResult, error)
}

type NavidromeLibrary interface {
	EnsureLibraries(context.Context) (int, int, bool, error)
	StartScan(context.Context, ...int) error
	WaitScan(context.Context, time.Duration) error
	ReleaseVisible(context.Context, int, navidrome.ReleaseIdentity) (bool, error)
}

type UnmanagedImportService struct {
	Store         UnmanagedStore
	Metadata      UnmanagedMetadataService
	Mutation      UnmanagedMutator
	Navidrome     NavidromeLibrary
	WorkRoot      string
	LibraryRoot   string
	MoveNoReplace func(string, string) error
	ScanPoll      time.Duration
	Now           func() time.Time
	Fault         func(string) error
}

func (s UnmanagedImportService) Import(ctx context.Context, candidateID string, approved domain.SubmissionDecision) (domain.UnmanagedRelease, error) {
	if err := s.validate(candidateID); err != nil {
		return domain.UnmanagedRelease{}, err
	}
	if existing, err := s.Store.UnmanagedImportIntent(ctx, candidateID); err == nil {
		return s.resume(ctx, existing)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.UnmanagedRelease{}, err
	}
	workflow, err := s.Store.Workflow(ctx, candidateID)
	if err != nil {
		return domain.UnmanagedRelease{}, err
	}
	plan, err := s.Metadata.Build(candidateID, approved, workflow.Technical)
	if err != nil {
		return domain.UnmanagedRelease{}, fmt.Errorf("%w: %v", ErrUnmanagedReview, err)
	}
	manifest, err := preflightUnmanaged(filepath.Join(s.WorkRoot, candidateID), plan)
	if err != nil {
		return domain.UnmanagedRelease{}, err
	}
	now := s.now()
	release := domain.UnmanagedRelease{CandidateID: candidateID, Plan: plan, Evidence: workflow.Technical, State: domain.StateUnmanagedImporting, Status: "PREPARED", Manifest: manifest, CreatedAt: now, UpdatedAt: now}
	intent := domain.UnmanagedImportIntent{ID: "unmanaged-" + candidateID, CandidateID: candidateID, IdempotencyKey: "unmanaged-import-" + candidateID, Plan: plan, Evidence: workflow.Technical, Manifest: manifest, Status: "PENDING", CreatedAt: now, UpdatedAt: now}
	if err := s.Store.PutUnmanagedRelease(ctx, release, now); err != nil {
		return release, err
	}
	if err := s.Store.PutUnmanagedImportIntent(ctx, intent, now); err != nil {
		return release, err
	}
	if err := s.fail("after_intent"); err != nil {
		return release, err
	}
	return s.resume(ctx, intent)
}

func (s UnmanagedImportService) Reconcile(ctx context.Context, candidateID string) (bool, error) {
	if err := s.validate(candidateID); err != nil {
		return false, err
	}
	intent, err := s.Store.UnmanagedImportIntent(ctx, candidateID)
	if err != nil {
		return false, err
	}
	release, err := s.resume(ctx, intent)
	return release.Status == "IMPORTED", err
}

func (s UnmanagedImportService) resume(ctx context.Context, intent domain.UnmanagedImportIntent) (domain.UnmanagedRelease, error) {
	release := domain.UnmanagedRelease{CandidateID: intent.CandidateID, Plan: intent.Plan, Evidence: intent.Evidence, State: domain.StateUnmanagedImporting, Status: "IMPORTING", Manifest: intent.Manifest, FinalPath: intent.FinalPath, Fingerprint: intent.Fingerprint, CreatedAt: intent.CreatedAt, UpdatedAt: s.now()}
	workPath := filepath.Join(s.WorkRoot, intent.CandidateID)
	layoutPath := filepath.Join(s.WorkRoot, "."+intent.CandidateID+".unmanaged-layout")
	finalPath := filepath.Join(s.LibraryRoot, intent.Plan.RelativeRoot)
	if intent.FinalPath != "" && filepath.Clean(intent.FinalPath) != finalPath {
		return release, fmt.Errorf("%w: persisted final path changed", ErrUnmanagedReview)
	}

	if _, err := os.Lstat(finalPath); err == nil {
		if intent.FinalPath == "" && intent.Status == "PENDING" {
			return release, fmt.Errorf("%w: destination already exists", ErrUnmanagedReview)
		}
		if _, layoutErr := os.Lstat(layoutPath); layoutErr == nil {
			return release, fmt.Errorf("%w: destination collision left staged layout", ErrUnmanagedReview)
		} else if !os.IsNotExist(layoutErr) {
			return release, layoutErr
		}
		if matches, matchErr := publishedMatchesManifest(finalPath, intent.Manifest); matchErr != nil {
			return release, matchErr
		} else if !matches {
			return release, fmt.Errorf("%w: destination manifest differs", ErrUnmanagedReview)
		}
		intent.FinalPath, release.FinalPath = finalPath, finalPath
		return s.verify(ctx, intent, release, workPath)
	} else if !os.IsNotExist(err) {
		return release, err
	}
	if _, err := os.Lstat(layoutPath); os.IsNotExist(err) {
		if s.Mutation == nil {
			return release, fmt.Errorf("unmanaged mutator is required")
		}
		mutation, mutationErr := s.Mutation.MutateUnmanagedRelease(ctx, intent.CandidateID, intent.Plan.Tags)
		if mutationErr != nil {
			return release, mutationErr
		}
		if !mutation.Approved || mutation.Quarantined {
			return release, fmt.Errorf("%w: %s", ErrUnmanagedReview, mutation.Reason)
		}
		intent.Status = "MUTATING"
		if err := s.update(ctx, &intent, &release); err != nil {
			return release, err
		}
		if err := s.fail("after_mutation"); err != nil {
			return release, err
		}
		if err := os.Mkdir(layoutPath, 0o750); err != nil && !os.IsExist(err) {
			return release, err
		}
	} else if err != nil {
		return release, err
	}
	if err := layoutUnmanaged(workPath, layoutPath, intent); err != nil {
		return release, err
	}
	if err := hashManifest(layoutPath, intent.Manifest); err != nil {
		return release, err
	}
	intent.Status = "LAYOUT_READY"
	if err := s.update(ctx, &intent, &release); err != nil {
		return release, err
	}
	if err := s.fail("after_layout"); err != nil {
		return release, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
		return release, err
	}
	move := s.MoveNoReplace
	if move == nil {
		move = denyrafs.MoveNoReplace
	}
	if err := move(layoutPath, finalPath); err != nil {
		if errors.Is(err, denyrafs.ErrTargetExists) {
			return release, fmt.Errorf("%w: destination already exists", ErrUnmanagedReview)
		}
		return release, err
	}
	if err := s.fail("after_final_rename"); err != nil {
		return release, err
	}
	intent.FinalPath, intent.Status = finalPath, "PUBLISHED"
	release.FinalPath = finalPath
	if err := s.update(ctx, &intent, &release); err != nil {
		return release, err
	}
	if err := s.fail("after_publish"); err != nil {
		return release, err
	}
	return s.verify(ctx, intent, release, workPath)
}

func (s UnmanagedImportService) verify(ctx context.Context, intent domain.UnmanagedImportIntent, release domain.UnmanagedRelease, workPath string) (domain.UnmanagedRelease, error) {
	_, unmanagedID, _, err := s.Navidrome.EnsureLibraries(ctx)
	if err != nil {
		return release, err
	}
	if err := s.Navidrome.StartScan(ctx, unmanagedID); err != nil {
		return release, err
	}
	if err := s.Navidrome.WaitScan(ctx, s.ScanPoll); err != nil {
		return release, err
	}
	identity := navidrome.ReleaseIdentity{AlbumArtist: intent.Plan.Metadata.AlbumArtist, Album: intent.Plan.Metadata.Album, TrackCount: intent.Plan.Metadata.TrackTotal}
	visible, err := s.Navidrome.ReleaseVisible(ctx, unmanagedID, identity)
	if err != nil {
		return release, err
	}
	if !visible {
		return release, fmt.Errorf("unmanaged release is not visible in Navidrome")
	}
	tree, err := denyrafs.Scan(release.FinalPath)
	if err != nil {
		return release, err
	}
	intent.Fingerprint, intent.Status = tree.Fingerprint, "VERIFIED"
	release.Fingerprint = tree.Fingerprint
	if err := s.update(ctx, &intent, &release); err != nil {
		return release, err
	}
	if err := s.fail("after_visibility"); err != nil {
		return release, err
	}
	intent.Status = "COMPLETED"
	release.Status, release.State = "IMPORTED", domain.StateUnmanagedImported
	if err := s.update(ctx, &intent, &release); err != nil {
		return release, err
	}
	for _, file := range intent.Manifest {
		if file.TargetRelative == "" {
			if err := os.Remove(filepath.Join(workPath, filepath.FromSlash(file.SourceRelative))); err != nil && !os.IsNotExist(err) {
				return release, err
			}
		}
	}
	if err := removeEmptyTree(workPath); err != nil {
		return release, err
	}
	return release, nil
}

func (s UnmanagedImportService) update(ctx context.Context, intent *domain.UnmanagedImportIntent, release *domain.UnmanagedRelease) error {
	now := s.now()
	intent.UpdatedAt, release.UpdatedAt = now, now
	release.Manifest, release.FinalPath, release.Fingerprint = intent.Manifest, intent.FinalPath, intent.Fingerprint
	return s.Store.UpdateUnmanagedImport(ctx, *intent, *release, now)
}

func (s UnmanagedImportService) validate(candidateID string) error {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return err
	}
	if s.Store == nil || s.Navidrome == nil || !filepath.IsAbs(s.WorkRoot) || !filepath.IsAbs(s.LibraryRoot) || s.ScanPoll <= 0 {
		return fmt.Errorf("unmanaged import service is not configured")
	}
	return nil
}

func (s UnmanagedImportService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s UnmanagedImportService) fail(point string) error {
	if s.Fault != nil {
		return s.Fault(point)
	}
	return nil
}

func preflightUnmanaged(workPath string, plan domain.UnmanagedPlan) ([]domain.PlannedFile, error) {
	tree, err := denyrafs.Scan(workPath)
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]domain.PlannedFile, len(plan.Files))
	lyricsTargets := make(map[string]string, len(plan.Files))
	for _, file := range plan.Files {
		bySource[filepath.ToSlash(file.SourceRelative)] = file
		sourceBase := strings.TrimSuffix(filepath.ToSlash(file.SourceRelative), filepath.Ext(file.SourceRelative))
		targetBase := strings.TrimSuffix(filepath.ToSlash(file.TargetRelative), filepath.Ext(file.TargetRelative))
		lyricsTargets[sourceBase] = targetBase
	}
	manifest := append([]domain.PlannedFile(nil), plan.Files...)
	for _, entry := range tree.Entries {
		relative := filepath.ToSlash(entry.RelativePath)
		if _, found := bySource[relative]; found {
			continue
		}
		extension := strings.ToLower(filepath.Ext(relative))
		base := strings.TrimSuffix(relative, filepath.Ext(relative))
		if targetBase, found := lyricsTargets[base]; found && (extension == ".lrc" || extension == ".elrc" || extension == ".ttml") {
			manifest = append(manifest, domain.PlannedFile{SourceRelative: relative, TargetRelative: targetBase + extension, Kind: "lyrics"})
			continue
		}
		name := strings.ToLower(filepath.Base(base))
		if plan.Artwork.Path != "" && (name == "cover" || name == "folder") && (extension == ".jpg" || extension == ".jpeg" || extension == ".png") {
			manifest = append(manifest, domain.PlannedFile{SourceRelative: relative, Kind: "artwork-source"})
			continue
		}
		return nil, fmt.Errorf("%w: unplanned source entry %q", ErrUnmanagedReview, relative)
	}
	if plan.Artwork.Path != "" {
		manifest = append(manifest, domain.PlannedFile{SourceRelative: plan.Artwork.Path, TargetRelative: "cover.jpg", Kind: "artwork"})
	}
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].TargetRelative < manifest[j].TargetRelative })
	return manifest, nil
}

func layoutUnmanaged(workPath, layoutPath string, intent domain.UnmanagedImportIntent) error {
	for _, file := range intent.Manifest {
		if file.TargetRelative == "" {
			continue
		}
		target := filepath.Join(layoutPath, filepath.FromSlash(file.TargetRelative))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if file.Kind == "artwork" {
			if _, err := os.Lstat(target); err == nil {
				continue
			}
			if err := copyNoReplace(file.SourceRelative, target); err != nil {
				return err
			}
			continue
		}
		source := filepath.Join(workPath, filepath.FromSlash(file.SourceRelative))
		_, sourceErr := os.Lstat(source)
		_, targetErr := os.Lstat(target)
		switch {
		case sourceErr == nil && os.IsNotExist(targetErr):
			if err := denyrafs.MoveNoReplace(source, target); err != nil {
				return err
			}
		case os.IsNotExist(sourceErr) && targetErr == nil:
			continue
		case sourceErr == nil && targetErr == nil:
			return fmt.Errorf("%w: layout target exists for %q", ErrUnmanagedReview, file.TargetRelative)
		default:
			return fmt.Errorf("%w: source entry missing for %q", ErrUnmanagedReview, file.SourceRelative)
		}
	}
	return nil
}

func copyNoReplace(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	name := output.Name()
	defer func() { _ = output.Close() }()
	if _, err := io.Copy(output, input); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := output.Sync(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return output.Close()
}

func hashManifest(root string, manifest []domain.PlannedFile) error {
	for index := range manifest {
		if manifest[index].TargetRelative == "" {
			continue
		}
		hash, err := fileSHA256(filepath.Join(root, filepath.FromSlash(manifest[index].TargetRelative)))
		if err != nil {
			return err
		}
		manifest[index].SHA256 = hash
	}
	return nil
}

func publishedMatchesManifest(root string, manifest []domain.PlannedFile) (bool, error) {
	tree, err := denyrafs.Scan(root)
	if err != nil {
		return false, err
	}
	expected := make(map[string]string, len(manifest))
	for _, file := range manifest {
		if file.TargetRelative != "" {
			expected[filepath.ToSlash(file.TargetRelative)] = file.SHA256
		}
	}
	if len(tree.Entries) != len(expected) {
		return false, nil
	}
	for _, entry := range tree.Entries {
		want, found := expected[filepath.ToSlash(entry.RelativePath)]
		if !found || want == "" {
			return false, nil
		}
		got, err := fileSHA256(filepath.Join(root, filepath.FromSlash(entry.RelativePath)))
		if err != nil {
			return false, err
		}
		if got != want {
			return false, nil
		}
	}
	return true, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func removeEmptyTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		if err := os.Remove(directory); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
