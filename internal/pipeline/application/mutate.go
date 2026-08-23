package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type TagMutator interface {
	ReadTags(context.Context, string) (domain.TagSet, domain.CommandEvidence, error)
	AudioMD5(context.Context, string) (string, domain.CommandEvidence, error)
	PictureCount(context.Context, string) (int, domain.CommandEvidence, error)
	Apply(context.Context, string, domain.TagSet, bool) ([]domain.CommandEvidence, error)
}

type MutationEvidence struct {
	RelativePath    string                   `json:"relative_path"`
	BeforeTags      domain.TagSet            `json:"before_tags"`
	AfterTags       domain.TagSet            `json:"after_tags"`
	BeforeSHA256    string                   `json:"before_sha256"`
	AfterSHA256     string                   `json:"after_sha256"`
	AudioMD5        string                   `json:"audio_md5"`
	RemovedPictures int                      `json:"removed_pictures"`
	Commands        []domain.CommandEvidence `json:"commands"`
}

type MutationResult struct {
	Files       []MutationEvidence `json:"files"`
	Approved    bool               `json:"approved"`
	Quarantined bool               `json:"quarantined"`
	Reason      string             `json:"reason,omitempty"`
	Path        string             `json:"path"`
}

type MutationService struct {
	WorkRoot       string
	QuarantineRoot string
	Tags           TagMutator
	Integrity      IntegrityTester
	Checksum       func(string) (string, error)
	Move           func(string, string) error
}

func (s MutationService) MutateRelease(ctx context.Context, candidateID string, plans map[string]domain.TagSet) (MutationResult, error) {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return MutationResult{}, err
	}
	workPath := filepath.Join(s.WorkRoot, candidateID)
	result := MutationResult{Path: workPath}
	paths := make([]string, 0, len(plans))
	for relative := range plans {
		if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.ToLower(filepath.Ext(relative)) != ".flac" {
			return result, fmt.Errorf("invalid mutation path %q", relative)
		}
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	if len(paths) == 0 || s.Tags == nil || s.Integrity == nil || s.Checksum == nil {
		return result, fmt.Errorf("mutation service or plans are incomplete")
	}
	for _, relative := range paths {
		path := filepath.Join(workPath, relative)
		evidence, err := s.mutateFile(ctx, path, relative, plans[relative])
		result.Files = append(result.Files, evidence)
		if err != nil {
			result.Reason = err.Error()
			quarantinePath, moveErr := s.moveToQuarantine(candidateID)
			if moveErr != nil {
				return result, fmt.Errorf("mutation failed (%v) and quarantine move failed: %w", err, moveErr)
			}
			result.Path, result.Quarantined = quarantinePath, true
			return result, nil
		}
	}
	result.Approved = true
	return result, nil
}

func (s MutationService) mutateFile(ctx context.Context, path, relative string, desired domain.TagSet) (MutationEvidence, error) {
	evidence := MutationEvidence{RelativePath: relative}
	beforeChecksum, err := s.Checksum(path)
	if err != nil {
		return evidence, err
	}
	evidence.BeforeSHA256 = beforeChecksum
	beforeTags, tagsCommand, err := s.Tags.ReadTags(ctx, path)
	evidence.Commands = append(evidence.Commands, tagsCommand)
	if err != nil {
		return evidence, err
	}
	evidence.BeforeTags = beforeTags
	beforeMD5, md5Command, err := s.Tags.AudioMD5(ctx, path)
	evidence.Commands = append(evidence.Commands, md5Command)
	if err != nil {
		return evidence, err
	}
	pictures, pictureCommand, err := s.Tags.PictureCount(ctx, path)
	evidence.Commands = append(evidence.Commands, pictureCommand)
	if err != nil {
		return evidence, err
	}
	evidence.RemovedPictures = pictures
	commands, err := s.Tags.Apply(ctx, path, desired, true)
	evidence.Commands = append(evidence.Commands, commands...)
	if err != nil {
		return evidence, err
	}
	afterTags, afterTagsCommand, err := s.Tags.ReadTags(ctx, path)
	evidence.Commands = append(evidence.Commands, afterTagsCommand)
	evidence.AfterTags = afterTags
	if err != nil {
		return evidence, err
	}
	if err := validateMutationTags(beforeTags, afterTags, desired); err != nil {
		return evidence, err
	}
	afterPictures, afterPictureCommand, err := s.Tags.PictureCount(ctx, path)
	evidence.Commands = append(evidence.Commands, afterPictureCommand)
	if err != nil || afterPictures != 0 {
		return evidence, fmt.Errorf("embedded pictures remain after mutation: count=%d error=%v", afterPictures, err)
	}
	afterMD5, afterMD5Command, err := s.Tags.AudioMD5(ctx, path)
	evidence.Commands = append(evidence.Commands, afterMD5Command)
	if err != nil || afterMD5 != beforeMD5 {
		return evidence, fmt.Errorf("audio-frame signature changed: before=%s after=%s error=%v", beforeMD5, afterMD5, err)
	}
	evidence.AudioMD5 = afterMD5
	afterChecksum, err := s.Checksum(path)
	if err != nil {
		return evidence, err
	}
	evidence.AfterSHA256 = afterChecksum
	integrityCommand, err := s.Integrity.Test(ctx, path)
	evidence.Commands = append(evidence.Commands, integrityCommand)
	if err != nil {
		return evidence, fmt.Errorf("post-mutation flac integrity: %w", err)
	}
	return evidence, nil
}

func validateMutationTags(before, after, desired domain.TagSet) error {
	for field, beforeValues := range before {
		if domain.IsManagedTag(field) {
			continue
		}
		afterValues, ok := after[field]
		if !ok || !slices.Equal(beforeValues, afterValues) {
			return fmt.Errorf("unknown tag %s was not preserved", field)
		}
	}
	for _, field := range domain.ManagedTagFields {
		if !slices.Equal(after[field], desired[field]) {
			return fmt.Errorf("managed tag %s differs after mutation: got=%v want=%v", field, after[field], desired[field])
		}
	}
	if _, exists := after["MUSICBRAINZ_RECORDINGID"]; exists {
		return fmt.Errorf("noncanonical MUSICBRAINZ_RECORDINGID remains")
	}
	return nil
}

func (s MutationService) moveToQuarantine(candidateID string) (string, error) {
	if err := os.MkdirAll(s.QuarantineRoot, 0o750); err != nil {
		return "", err
	}
	move := s.Move
	if move == nil {
		move = denyrafs.MoveAtomic
	}
	source, target := filepath.Join(s.WorkRoot, candidateID), filepath.Join(s.QuarantineRoot, candidateID)
	if err := move(source, target); err != nil {
		return "", err
	}
	return target, nil
}
