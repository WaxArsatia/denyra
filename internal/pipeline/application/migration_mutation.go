package application

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type MigrationMutationFile struct {
	RelativePath string        `json:"relative_path"`
	BeforeTags   domain.TagSet `json:"before_tags"`
	BeforeSHA256 string        `json:"before_sha256"`
	AudioMD5     string        `json:"audio_md5"`
	PictureCount int           `json:"picture_count"`
}

type MigrationMutationResult struct {
	BackupRoot string                  `json:"backup_root"`
	Files      []MigrationMutationFile `json:"files"`
}

type MigrationMutator interface {
	Apply(context.Context, string, map[string]domain.TagSet) (MigrationMutationResult, error)
	Restore(context.Context, string, MigrationMutationResult) error
	Cleanup(string) error
}

type MigrationMutationService struct {
	ApprovedRoot string
	Tags         SelectedTagMutator
	Integrity    IntegrityTester
	Checksum     func(string) (string, error)
}

func (s MigrationMutationService) Apply(ctx context.Context, candidateID string, plans map[string]domain.TagSet) (MigrationMutationResult, error) {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return MigrationMutationResult{}, err
	}
	if s.ApprovedRoot == "" || s.Tags == nil || s.Integrity == nil || s.Checksum == nil || len(plans) == 0 {
		return MigrationMutationResult{}, fmt.Errorf("migration mutation service is not configured")
	}
	approved := filepath.Join(s.ApprovedRoot, candidateID)
	backup := filepath.Join(s.ApprovedRoot, ".migration-backups", candidateID)
	if err := os.MkdirAll(backup, 0o750); err != nil {
		return MigrationMutationResult{}, err
	}
	result := MigrationMutationResult{BackupRoot: backup}
	relatives := make([]string, 0, len(plans))
	for relative := range plans {
		if !safeMigrationRelative(relative) {
			return result, fmt.Errorf("invalid migration mutation path %q", relative)
		}
		relatives = append(relatives, relative)
	}
	sort.Strings(relatives)
	for _, relative := range relatives {
		path := filepath.Join(approved, filepath.FromSlash(relative))
		backupPath := filepath.Join(backup, filepath.FromSlash(relative))
		file, err := s.originalEvidence(ctx, path, backupPath, relative)
		result.Files = append(result.Files, file)
		if err != nil {
			return result, err
		}
		if _, err := s.Tags.ApplySelected(ctx, path, plans[relative], migrationManagedFields(), true); err != nil {
			return result, err
		}
		afterTags, _, err := s.Tags.ReadTags(ctx, path)
		if err != nil {
			return result, err
		}
		for field, want := range plans[relative] {
			if !slices.Equal(afterTags[field], want) {
				return result, fmt.Errorf("migration tag %s differs after mutation", field)
			}
		}
		pictures, _, err := s.Tags.PictureCount(ctx, path)
		if err != nil || pictures != file.PictureCount {
			return result, fmt.Errorf("migration picture count changed: before=%d after=%d error=%v", file.PictureCount, pictures, err)
		}
		md5, _, err := s.Tags.AudioMD5(ctx, path)
		if err != nil || md5 != file.AudioMD5 {
			return result, fmt.Errorf("migration audio MD5 changed: before=%s after=%s error=%v", file.AudioMD5, md5, err)
		}
		if _, err := s.Integrity.Test(ctx, path); err != nil {
			return result, fmt.Errorf("migration FLAC integrity: %w", err)
		}
	}
	return result, nil
}

func (s MigrationMutationService) Restore(ctx context.Context, candidateID string, result MigrationMutationResult) error {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return err
	}
	approved := filepath.Join(s.ApprovedRoot, candidateID)
	for _, file := range result.Files {
		if !safeMigrationRelative(file.RelativePath) {
			return fmt.Errorf("invalid migration restore path %q", file.RelativePath)
		}
		backupPath := filepath.Join(result.BackupRoot, filepath.FromSlash(file.RelativePath))
		target := filepath.Join(approved, filepath.FromSlash(file.RelativePath))
		temporary := target + ".denyra-restore"
		if err := copyMigrationFile(backupPath, temporary, true); err != nil {
			return err
		}
		if err := os.Rename(temporary, target); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		checksum, err := s.Checksum(target)
		if err != nil || checksum != file.BeforeSHA256 {
			return fmt.Errorf("migration restore checksum mismatch: got=%s want=%s error=%v", checksum, file.BeforeSHA256, err)
		}
		tags, _, err := s.Tags.ReadTags(ctx, target)
		if err != nil || !equalTagSets(tags, file.BeforeTags) {
			return fmt.Errorf("migration restore tags differ: error=%v", err)
		}
		pictures, _, err := s.Tags.PictureCount(ctx, target)
		if err != nil || pictures != file.PictureCount {
			return fmt.Errorf("migration restore pictures differ: error=%v", err)
		}
		md5, _, err := s.Tags.AudioMD5(ctx, target)
		if err != nil || md5 != file.AudioMD5 {
			return fmt.Errorf("migration restore audio MD5 differs: error=%v", err)
		}
	}
	return s.Cleanup(candidateID)
}

func (s MigrationMutationService) Cleanup(candidateID string) error {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.ApprovedRoot, ".migration-backups", candidateID))
}

func (s MigrationMutationService) originalEvidence(ctx context.Context, path, backupPath, relative string) (MigrationMutationFile, error) {
	file := MigrationMutationFile{RelativePath: relative}
	evidencePath := backupPath
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		evidencePath = path
		if err := copyMigrationFile(path, backupPath, false); err != nil {
			return file, err
		}
	} else if err != nil {
		return file, err
	}
	var err error
	file.BeforeSHA256, err = s.Checksum(backupPath)
	if err != nil {
		return file, err
	}
	file.BeforeTags, _, err = s.Tags.ReadTags(ctx, evidencePath)
	if err != nil {
		return file, err
	}
	file.AudioMD5, _, err = s.Tags.AudioMD5(ctx, evidencePath)
	if err != nil {
		return file, err
	}
	file.PictureCount, _, err = s.Tags.PictureCount(ctx, evidencePath)
	return file, err
}

func copyMigrationFile(source, target string, replace bool) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("migration source is not a regular file: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if replace {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, flags, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return copyErr
	}
	return closeErr
}

func safeMigrationRelative(relative string) bool {
	clean := filepath.Clean(filepath.FromSlash(relative))
	return relative != "" && !filepath.IsAbs(relative) && clean == filepath.FromSlash(relative) && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && strings.EqualFold(filepath.Ext(clean), ".flac")
}

func migrationManagedFields() []string {
	return append(append([]string(nil), domain.ManagedTagFields...), "MUSICBRAINZ_RECORDINGID")
}

func equalTagSets(left, right domain.TagSet) bool {
	if len(left) != len(right) {
		return false
	}
	for field, values := range left {
		if !slices.Equal(values, right[field]) {
			return false
		}
	}
	return true
}
