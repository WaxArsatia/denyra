package domain_test

import (
	"path/filepath"
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

func TestBuildUnmanagedLayoutIsDeterministicAndSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		plan domain.MetadataPlan
		root string
		file string
	}{
		{name: "dated single disc", plan: layoutPlan("Kaleb J", "OFF GUARD", "2024", "", 1), root: filepath.Join("Kaleb J", "OFF GUARD (2024)"), file: "01 - Untukmu.flac"},
		{name: "multi disc", plan: layoutPlan("Artist", "Album", "", "", 2), root: filepath.Join("Artist", "Album"), file: filepath.Join("Disc 01", "01 - Untukmu.flac")},
		{name: "edition", plan: layoutPlan("Artist", "Album", "", "Deluxe", 1), root: filepath.Join("Artist", "Album [Deluxe]"), file: "01 - Untukmu.flac"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, files, err := domain.BuildUnmanagedLayout(test.plan)
			if err != nil || root != test.root || len(files) == 0 || files[0].TargetRelative != test.file {
				t.Fatalf("root=%q files=%+v err=%v", root, files, err)
			}
		})
	}
	if got, err := domain.SanitizeMusicComponent(" A/B\x00. "); err != nil || got != "A_B_" {
		t.Fatalf("sanitized=%q err=%v", got, err)
	}
	for _, invalid := range []string{".", "..", "  .  "} {
		if _, err := domain.SanitizeMusicComponent(invalid); err == nil {
			t.Fatalf("reserved component %q accepted", invalid)
		}
	}
	collision := layoutPlan("Artist", "Album", "", "", 1)
	collision.TrackTotal = 2
	collision.Tracks = append(collision.Tracks, domain.TrackMetadata{RelativePath: "02.flac", Title: "UNTUKMU", Artist: "Artist", Track: 1, Disc: 1, DurationMS: 1000})
	if _, _, err := domain.BuildUnmanagedLayout(collision); err == nil {
		t.Fatal("post-normalization target collision accepted")
	}
}

func layoutPlan(artist, album, date, edition string, discs int) domain.MetadataPlan {
	plan := domain.MetadataPlan{AlbumArtist: artist, Album: album, Date: date, Edition: edition, DiscTotal: discs, TrackTotal: 1, Preserved: map[string]map[string][]string{"01.flac": {}}, Tracks: []domain.TrackMetadata{{RelativePath: "01.flac", Title: "Untukmu", Artist: artist, Track: 1, Disc: 1, DurationMS: 1000}}}
	if discs > 1 {
		plan.TrackTotal = 2
		plan.Preserved["02.flac"] = map[string][]string{}
		plan.Tracks = append(plan.Tracks, domain.TrackMetadata{RelativePath: "02.flac", Title: "Other", Artist: artist, Track: 1, Disc: 2, DurationMS: 1000})
	}
	return plan
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
