package application

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var UnmanagedTagFields = []string{"TITLE", "ARTIST", "ALBUM", "ALBUMARTIST", "TRACKNUMBER", "TRACKTOTAL", "DISCNUMBER", "DISCTOTAL"}

type UnmanagedMetadataService struct{}

func (UnmanagedMetadataService) Build(candidateID string, approved domain.SubmissionDecision, observed domain.TechnicalReleaseResult) (domain.UnmanagedPlan, error) {
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return domain.UnmanagedPlan{}, err
	}
	if approved.Destination != domain.DestinationUnmanaged {
		return domain.UnmanagedPlan{}, fmt.Errorf("approved destination is not unmanaged")
	}
	if err := domain.ValidateMetadataPlan(approved.Metadata); err != nil {
		return domain.UnmanagedPlan{}, err
	}
	if observed.Rejected || len(observed.Files) != len(approved.Metadata.Tracks) {
		return domain.UnmanagedPlan{}, fmt.Errorf("post-claim technical evidence is incomplete or rejected")
	}
	byPath := make(map[string]domain.FileTechnicalEvidence, len(observed.Files))
	for _, file := range observed.Files {
		if _, duplicate := byPath[file.RelativePath]; duplicate {
			return domain.UnmanagedPlan{}, fmt.Errorf("duplicate technical evidence for %q", file.RelativePath)
		}
		byPath[file.RelativePath] = file
	}
	result := domain.UnmanagedPlan{CandidateID: candidateID, Metadata: approved.Metadata, Artwork: approved.Artwork, Tags: make(map[string]domain.TagSet, len(approved.Metadata.Tracks))}
	for _, track := range approved.Metadata.Tracks {
		file, ok := byPath[track.RelativePath]
		if !ok || file.Info.DurationMS != track.DurationMS {
			return domain.UnmanagedPlan{}, fmt.Errorf("post-claim evidence drift for %q", track.RelativePath)
		}
		sealed := approved.Metadata.Preserved[track.RelativePath]
		if err := unchangedUnmanagedTags(sealed, file.OriginalComments); err != nil {
			return domain.UnmanagedPlan{}, fmt.Errorf("%s: %w", track.RelativePath, err)
		}
		tags := cloneTagSet(file.OriginalComments)
		for _, field := range UnmanagedTagFields {
			delete(tags, field)
		}
		tags["TITLE"] = []string{track.Title}
		tags["ARTIST"] = []string{track.Artist}
		tags["ALBUM"] = []string{approved.Metadata.Album}
		tags["ALBUMARTIST"] = []string{approved.Metadata.AlbumArtist}
		tags["TRACKNUMBER"] = []string{strconv.Itoa(track.Track)}
		tags["TRACKTOTAL"] = []string{strconv.Itoa(approved.Metadata.TrackTotal)}
		tags["DISCNUMBER"] = []string{strconv.Itoa(track.Disc)}
		tags["DISCTOTAL"] = []string{strconv.Itoa(approved.Metadata.DiscTotal)}
		result.Tags[track.RelativePath] = tags
	}
	root, files, err := domain.BuildUnmanagedLayout(approved.Metadata)
	if err != nil {
		return domain.UnmanagedPlan{}, err
	}
	result.RelativeRoot, result.Files = root, files
	return result, nil
}

func unchangedUnmanagedTags(sealed, observed map[string][]string) error {
	for field, expected := range sealed {
		if slices.Contains(UnmanagedTagFields, strings.ToUpper(field)) {
			continue
		}
		if !slices.Equal(expected, observed[field]) {
			return fmt.Errorf("preserved tag %s changed", field)
		}
	}
	for field, actual := range observed {
		if slices.Contains(UnmanagedTagFields, strings.ToUpper(field)) {
			continue
		}
		if !slices.Equal(actual, sealed[field]) {
			return fmt.Errorf("preserved tag %s changed", field)
		}
	}
	return nil
}

func cloneTagSet(input map[string][]string) domain.TagSet {
	result := make(domain.TagSet, len(input))
	for field, values := range input {
		result[strings.ToUpper(field)] = append([]string(nil), values...)
	}
	return result
}
