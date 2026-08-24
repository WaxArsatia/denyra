package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestIdentityDecisionCoversExactAmbiguousNoMatchAndError(t *testing.T) {
	t.Parallel()
	plan, observed := identityFixture()
	exact := identityRelease("11111111-1111-1111-1111-111111111111", 1000)
	other := identityRelease("22222222-2222-2222-2222-222222222222", 1000)
	mismatch := identityRelease("33333333-3333-3333-3333-333333333333", 50_000)
	tests := []struct {
		name     string
		searcher identitySearcher
		want     application.IdentityStatus
		wantErr  bool
	}{
		{name: "exact", searcher: identitySearcher{releases: []domain.CanonicalRelease{mismatch, exact}}, want: application.IdentityExact},
		{name: "ambiguous", searcher: identitySearcher{releases: []domain.CanonicalRelease{exact, other}}, want: application.IdentityAmbiguous},
		{name: "no match", searcher: identitySearcher{releases: []domain.CanonicalRelease{mismatch}}, want: application.IdentityNoMatch},
		{name: "error", searcher: identitySearcher{err: errors.New("provider unavailable")}, want: application.IdentityError, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := (application.IdentityService{Search: test.searcher, DurationPolicy: identityDurationPolicy()}).Decide(context.Background(), plan, observed)
			if decision.Status != test.want || (err != nil) != test.wantErr {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
			if test.want == application.IdentityExact && (decision.Exact == nil || decision.Exact.Release.ReleaseMBID != exact.ReleaseMBID) {
				t.Fatalf("exact candidate=%+v", decision.Exact)
			}
		})
	}
}

func TestIdentityConflictingTaggedReleaseIDsCannotBecomeExact(t *testing.T) {
	t.Parallel()
	plan, observed := identityFixture()
	plan.Preserved["01.flac"]["MUSICBRAINZ_ALBUMID"] = []string{"11111111-1111-1111-1111-111111111111"}
	plan.Preserved["02.flac"]["MUSICBRAINZ_ALBUMID"] = []string{"22222222-2222-2222-2222-222222222222"}
	decision, err := (application.IdentityService{Search: identitySearcher{releases: []domain.CanonicalRelease{identityRelease("11111111-1111-1111-1111-111111111111", 1000)}}, DurationPolicy: identityDurationPolicy()}).Decide(context.Background(), plan, observed)
	if err != nil || decision.Status == application.IdentityExact {
		t.Fatalf("conflicting IDs decision=%+v err=%v", decision, err)
	}
}

type identitySearcher struct {
	releases []domain.CanonicalRelease
	err      error
}

func (s identitySearcher) SearchReleases(context.Context, musicbrainz.SearchInput) (musicbrainz.SearchResult, error) {
	return musicbrainz.SearchResult{Releases: s.releases}, s.err
}

func identityFixture() (domain.MetadataPlan, domain.TechnicalReleaseResult) {
	plan := domain.MetadataPlan{AlbumArtist: "Kaleb J", Album: "OFF GUARD", Date: "2024", DiscTotal: 1, TrackTotal: 2, Preserved: map[string]map[string][]string{"01.flac": {}, "02.flac": {}}, Tracks: []domain.TrackMetadata{
		{RelativePath: "01.flac", Title: "First", Artist: "Kaleb J", Track: 1, Disc: 1, DurationMS: 1000, ISRCs: []string{"IDABC2600001"}},
		{RelativePath: "02.flac", Title: "Second", Artist: "Kaleb J", Track: 2, Disc: 1, DurationMS: 1000, ISRCs: []string{"IDABC2600002"}},
	}}
	observed := domain.TechnicalReleaseResult{Files: []domain.FileTechnicalEvidence{
		{RelativePath: "01.flac", Info: domain.TechnicalInfo{DurationMS: 1000}},
		{RelativePath: "02.flac", Info: domain.TechnicalInfo{DurationMS: 1000}},
	}}
	return plan, observed
}

func identityRelease(id string, duration int64) domain.CanonicalRelease {
	d1, d2 := duration, duration
	return domain.CanonicalRelease{ReleaseMBID: id, ReleaseGroupMBID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Title: "OFF GUARD", Date: "2024", ArtistCredits: []domain.ArtistCredit{{Name: "Kaleb J", ArtistMBID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}, Tracks: []domain.CanonicalTrack{
		{ReleaseTrackMBID: "cccccccc-cccc-cccc-cccc-cccccccccccc", RecordingMBID: "dddddddd-dddd-dddd-dddd-dddddddddddd", Title: "First", Disc: 1, Track: 1, DurationMS: &d1, ArtistCredits: []domain.ArtistCredit{{Name: "Kaleb J"}}, ISRCs: []string{"IDABC2600001"}},
		{ReleaseTrackMBID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", RecordingMBID: "ffffffff-ffff-ffff-ffff-ffffffffffff", Title: "Second", Disc: 1, Track: 2, DurationMS: &d2, ArtistCredits: []domain.ArtistCredit{{Name: "Kaleb J"}}, ISRCs: []string{"IDABC2600002"}},
	}}
}

func identityDurationPolicy() domain.DurationPolicy {
	return domain.DurationPolicy{TrackAutoFloorMS: 100, TrackAutoPercentBasisPoints: 100, TrackManualFloorMS: 500, TrackManualPercentBasisPoints: 500, ReleaseAutoFloorMS: 200, ReleaseAutoPercentBasisPoints: 100, ReleaseManualFloorMS: 1000, ReleaseManualPercentBasisPoints: 500}
}
