package domain_test

import (
	"reflect"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestCanonicalTagsUsePicardCompatibleRepeatedVorbisFields(t *testing.T) {
	tags, err := domain.CanonicalTags(domain.TagInput{
		Title: " Cafe\u0301 ", Album: "Album", Date: "2026-08", TrackNumber: 1, DiscNumber: 2,
		Artists:      []domain.ArtistCredit{{Name: "First", ArtistMBID: "11111111-1111-1111-1111-111111111111"}, {Name: "Second", ArtistMBID: "22222222-2222-2222-2222-222222222222"}},
		AlbumArtists: []domain.ArtistCredit{{Name: "Various Artists", ArtistMBID: "33333333-3333-3333-3333-333333333333"}},
		Genres:       []string{"Rock", " ambient ", "rock"}, ISRCs: []string{"us-aaa-26-00001", "USAAA2600001", "GBBBB2600002"},
		RecordingMBID: "44444444-4444-4444-4444-444444444444", ReleaseTrackMBID: "55555555-5555-5555-5555-555555555555",
		ReleaseMBID: "66666666-6666-6666-6666-666666666666", ReleaseGroupMBID: "77777777-7777-7777-7777-777777777777",
	})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string][]string{
		"TITLE": {"Café"}, "TRACKNUMBER": {"1"}, "DISCNUMBER": {"2"}, "DATE": {"2026-08"},
		"ARTIST": {"First", "Second"}, "MUSICBRAINZ_ARTISTID": {"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"},
		"GENRE": {"ambient", "Rock"}, "ISRC": {"GBBBB2600002", "USAAA2600001"},
		"MUSICBRAINZ_TRACKID":        {"44444444-4444-4444-4444-444444444444"},
		"MUSICBRAINZ_RELEASETRACKID": {"55555555-5555-5555-5555-555555555555"},
	}
	for field, want := range checks {
		if !reflect.DeepEqual(tags[field], want) {
			t.Errorf("%s = %v, want %v", field, tags[field], want)
		}
	}
	if domain.IsManagedTag("MUSICBRAINZ_RECORDINGID") != true {
		t.Fatal("legacy recording field is not removed by mutation schema")
	}
	entries, err := tags.OrderedEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0] != "TITLE=Café" {
		t.Fatalf("deterministic entries = %v", entries)
	}
}

func TestCanonicalTagsRejectEmptyMisalignedOrNoncanonicalValues(t *testing.T) {
	base := domain.TagInput{
		Title: "Track", Album: "Album", Date: "2026", TrackNumber: 1, DiscNumber: 1,
		Artists:       []domain.ArtistCredit{{Name: "Artist", ArtistMBID: "11111111-1111-1111-1111-111111111111"}},
		AlbumArtists:  []domain.ArtistCredit{{Name: "Artist", ArtistMBID: "11111111-1111-1111-1111-111111111111"}},
		RecordingMBID: "22222222-2222-2222-2222-222222222222", ReleaseTrackMBID: "33333333-3333-3333-3333-333333333333",
		ReleaseMBID: "44444444-4444-4444-4444-444444444444", ReleaseGroupMBID: "55555555-5555-5555-5555-555555555555",
	}
	for name, mutate := range map[string]func(*domain.TagInput){
		"empty title":    func(value *domain.TagInput) { value.Title = " " },
		"uppercase MBID": func(value *domain.TagInput) { value.RecordingMBID = "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA" },
		"zero track":     func(value *domain.TagInput) { value.TrackNumber = 0 },
		"bad date":       func(value *domain.TagInput) { value.Date = "2026-8" },
		"empty artist":   func(value *domain.TagInput) { value.Artists[0].Name = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Artists = append([]domain.ArtistCredit(nil), base.Artists...)
			mutate(&value)
			if _, err := domain.CanonicalTags(value); err == nil {
				t.Fatalf("invalid tag input accepted: %+v", value)
			}
		})
	}
}
