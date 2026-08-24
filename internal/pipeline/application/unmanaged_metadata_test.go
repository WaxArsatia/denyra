package application_test

import (
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestUnmanagedMetadataBuildOverridesOnlyApprovedFieldsAndRejectsDrift(t *testing.T) {
	t.Parallel()
	plan := domain.MetadataPlan{AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2024", DiscTotal: 1, TrackTotal: 1, Preserved: map[string]map[string][]string{"01.flac": {
		"TITLE": {"Old"}, "ARTIST": {"Kaleb J"}, "ALBUM": {"OFF GUARD"}, "ALBUMARTIST": {"Kaleb J"}, "TRACKNUMBER": {"1"}, "DISCNUMBER": {"1"},
		"UPC": {"123456789012"}, "ISRC": {"IDABC2600001"}, "SOURCE_URL": {"https://example.invalid/source"}, "CUSTOM": {"keep"},
	}}, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Untukmu", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1000}}}
	observed := domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{{RelativePath: "01.flac", SHA256Before: "abc", Info: domain.TechnicalInfo{DurationMS: 1000}, OriginalComments: cloneTagMap(plan.Preserved["01.flac"]), EmbeddedPictures: 1}}}
	result, err := (application.UnmanagedMetadataService{}).Build("candidate-1", domain.SubmissionDecision{Destination: domain.DestinationUnmanaged, Metadata: plan}, observed)
	if err != nil || len(result.Files) != 1 {
		t.Fatalf("plan=%+v err=%v", result, err)
	}
	tags := result.Tags["01.flac"]
	for field, want := range map[string]string{"TITLE": "Untukmu", "ARTIST": "Kaleb J", "ALBUM": "OFF GUARD", "ALBUMARTIST": "Kaleb J", "TRACKNUMBER": "1", "TRACKTOTAL": "1", "DISCNUMBER": "1", "DISCTOTAL": "1", "UPC": "123456789012", "ISRC": "IDABC2600001", "SOURCE_URL": "https://example.invalid/source", "CUSTOM": "keep"} {
		if len(tags[field]) != 1 || tags[field][0] != want {
			t.Fatalf("tag %s=%v want %q", field, tags[field], want)
		}
	}
	observed.Files[0].OriginalComments["CUSTOM"] = []string{"drifted"}
	if _, err := (application.UnmanagedMetadataService{}).Build("candidate-1", domain.SubmissionDecision{Destination: domain.DestinationUnmanaged, Metadata: plan}, observed); err == nil {
		t.Fatal("post-preview unknown-tag drift accepted")
	}
}

func cloneTagMap(input map[string][]string) map[string][]string {
	result := make(map[string][]string, len(input))
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}
