package application_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestArtworkSelectionUsesLocalFirstPriorityAndExplicitLookups(t *testing.T) {
	t.Parallel()
	imageBytes := artworkJPEG(t, 8, 8)
	exact := artworkIdentity()
	tests := []struct {
		name       string
		local      artworkLocal
		tags       map[string]map[string][]string
		identity   application.IdentityDecision
		want       domain.ArtworkSource
		spotifyHit int
		coverHit   int
	}{
		{name: "embedded", local: artworkLocal{embedded: imageBytes}, tags: artworkSpotifyTags(), identity: exact, want: domain.ArtworkEmbedded},
		{name: "sidecar", local: artworkLocal{sidecar: imageBytes}, tags: artworkSpotifyTags(), identity: exact, want: domain.ArtworkSidecar},
		{name: "explicit spotify", tags: artworkSpotifyTags(), identity: exact, want: domain.ArtworkSpotifyExplicit, spotifyHit: 1},
		{name: "identifier exact", tags: map[string]map[string][]string{"01.flac": {"ISRC": {"IDABC2600001"}}}, identity: exact, want: domain.ArtworkIdentifierExact, coverHit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spotify, cover := &artworkLookup{bytes: imageBytes}, &artworkLookup{bytes: imageBytes}
			service := application.ArtworkService{Local: test.local, Spotify: spotify, CoverArt: cover, Root: t.TempDir(), MaxBytes: 1 << 20, MaxPixels: 1_000_000}
			selection, _, err := service.Select(context.Background(), "submission-1", t.TempDir(), test.tags, test.identity)
			if err != nil || selection.Source != test.want || spotify.calls != test.spotifyHit || cover.calls != test.coverHit {
				t.Fatalf("selection=%+v spotify=%d cover=%d err=%v", selection, spotify.calls, cover.calls, err)
			}
			if info, statErr := os.Stat(selection.Path); statErr != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("persisted cover stat=%v err=%v", info, statErr)
			}
		})
	}
}

func TestArtworkLookupFailureAndTitleOnlySpotifyRemainUsable(t *testing.T) {
	t.Parallel()
	spotify, cover := &artworkLookup{err: errors.New("offline")}, &artworkLookup{err: errors.New("offline")}
	service := application.ArtworkService{Local: artworkLocal{}, Spotify: spotify, CoverArt: cover, Root: t.TempDir(), MaxBytes: 1 << 20, MaxPixels: 1_000_000}
	selection, _, err := service.Select(context.Background(), "submission-1", t.TempDir(), map[string]map[string][]string{"01.flac": {"TITLE": {"https://open.spotify.com/track/abc"}}}, artworkIdentity())
	if err != nil || selection.Source != "" || spotify.calls != 0 || cover.calls != 0 {
		t.Fatalf("selection=%+v spotify=%d cover=%d err=%v", selection, spotify.calls, cover.calls, err)
	}
}

func TestArtworkReplacementRejectsOversizedPixelsAndCorruptImages(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		body      []byte
		maxBytes  int64
		maxPixels int64
	}{
		"oversized bytes": {body: bytes.Repeat([]byte("x"), 65), maxBytes: 64, maxPixels: 1_000_000},
		"excess pixels":   {body: artworkJPEG(t, 5, 5), maxBytes: 1 << 20, maxPixels: 16},
		"corrupt":         {body: []byte("not-an-image"), maxBytes: 1 << 20, maxPixels: 1_000_000},
	} {
		t.Run(name, func(t *testing.T) {
			service := application.ArtworkService{Root: t.TempDir(), MaxBytes: test.maxBytes, MaxPixels: test.maxPixels}
			if _, err := service.Replace(context.Background(), "submission-1", bytes.NewReader(test.body)); err == nil {
				t.Fatal("invalid artwork accepted")
			}
		})
	}
}

type artworkLocal struct{ embedded, sidecar []byte }

func (l artworkLocal) Embedded(context.Context, string) ([]byte, string, error) {
	if len(l.embedded) == 0 {
		return nil, "", application.ErrArtworkNotFound
	}
	return l.embedded, "01.flac", nil
}
func (l artworkLocal) Sidecar(string) ([]byte, string, error) {
	if len(l.sidecar) == 0 {
		return nil, "", application.ErrArtworkNotFound
	}
	return l.sidecar, "cover.png", nil
}

type artworkLookup struct {
	bytes []byte
	err   error
	calls int
}

func (l *artworkLookup) FetchURL(context.Context, string) ([]byte, domain.ProviderEvidence, error) {
	l.calls++
	return l.bytes, domain.ProviderEvidence{Provider: "spotify", Endpoint: "https://example.invalid/image", StatusCode: 200, ResponseSHA256: "abc"}, l.err
}
func (l *artworkLookup) FetchRelease(context.Context, string) ([]byte, domain.ProviderEvidence, error) {
	l.calls++
	return l.bytes, domain.ProviderEvidence{Provider: "coverart", Endpoint: "https://example.invalid/image", StatusCode: 200, ResponseSHA256: "def"}, l.err
}

func artworkSpotifyTags() map[string]map[string][]string {
	return map[string]map[string][]string{"01.flac": {"SPOTIFY_URL": {"https://open.spotify.com/track/abc123"}, "ISRC": {"IDABC2600001"}}}
}

func artworkIdentity() application.IdentityDecision {
	duration := int64(1000)
	candidate := application.IdentityCandidate{Release: domain.CanonicalRelease{ReleaseMBID: "11111111-1111-1111-1111-111111111111", Tracks: []domain.CanonicalTrack{{DurationMS: &duration, ISRCs: []string{"IDABC2600001"}}}}}
	return application.IdentityDecision{Status: application.IdentityExact, Exact: &candidate, Candidates: []application.IdentityCandidate{candidate}}
}

func artworkJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	if err := jpeg.Encode(&output, canvas, nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
