package domain_test

import (
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestValidateMetadataPlanRequiresCanonicalReleaseAndContiguousTracks(t *testing.T) {
	t.Parallel()
	valid := domain.MetadataPlan{
		AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2024", DiscTotal: 1, TrackTotal: 2,
		Tracks: []domain.TrackMetadata{
			{RelativePath: "01.flac", Title: "First", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1000},
			{RelativePath: "02.flac", Title: "Second", Artist: "Kaleb J", Track: 2, Disc: 1, DurationMS: 1100},
		},
	}
	if err := domain.ValidateMetadataPlan(valid); err != nil {
		t.Fatalf("valid metadata rejected: %v", err)
	}
	tests := map[string]func(*domain.MetadataPlan){
		"album artist": func(plan *domain.MetadataPlan) { plan.AlbumArtist = "" },
		"album":        func(plan *domain.MetadataPlan) { plan.Album = "" },
		"title":        func(plan *domain.MetadataPlan) { plan.Tracks[0].Title = "" },
		"artist":       func(plan *domain.MetadataPlan) { plan.Tracks[0].Artist = "" },
		"duplicate":    func(plan *domain.MetadataPlan) { plan.Tracks[1].Track = 1 },
		"gap":          func(plan *domain.MetadataPlan) { plan.Tracks[1].Track = 3 },
		"totals":       func(plan *domain.MetadataPlan) { plan.TrackTotal = 3 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			plan := valid
			plan.Tracks = append([]domain.TrackMetadata(nil), valid.Tracks...)
			mutate(&plan)
			if err := domain.ValidateMetadataPlan(plan); err == nil {
				t.Fatal("invalid canonical metadata accepted")
			}
		})
	}
}

func TestSubmissionDecisionRequiresPreviewFingerprint(t *testing.T) {
	t.Parallel()
	decision := domain.SubmissionDecision{Destination: domain.DestinationUnmanaged, Metadata: domain.MetadataPlan{
		AlbumArtist: "Kaleb J", Album: "OFF GUARD", DiscTotal: 1, TrackTotal: 1,
		Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Track", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1000}},
	}}
	if err := domain.ValidateSubmissionDecision(decision); err == nil {
		t.Fatal("decision without preview fingerprint accepted")
	}
	decision.PreviewFingerprint = "tree-fingerprint"
	if err := domain.ValidateSubmissionDecision(decision); err != nil {
		t.Fatalf("valid unmanaged decision rejected: %v", err)
	}
	decision.Destination = domain.DestinationManaged
	if err := domain.ValidateSubmissionDecision(decision); err == nil {
		t.Fatal("managed decision without release MBID accepted")
	}
}
