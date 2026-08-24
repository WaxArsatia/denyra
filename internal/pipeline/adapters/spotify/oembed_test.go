package spotify_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/spotify"
)

func TestOEmbedAcceptsOnlyExplicitSpotifyTrackAndHTTPSThumbnail(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oembed":
			if r.URL.Query().Get("url") != "https://open.spotify.com/track/abc123" {
				t.Errorf("unexpected oEmbed query %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"thumbnail_url":"` + server.URL + `/thumb.jpg"}`))
		case "/thumb.jpg":
			_, _ = w.Write([]byte("image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := spotify.OEmbed{BaseURL: server.URL, HTTP: server.Client(), ResponseLimit: 1024}
	body, evidence, err := client.FetchURL(context.Background(), "https://open.spotify.com/track/abc123")
	if err != nil || string(body) != "image" || evidence.ResponseSHA256 == "" || evidence.Endpoint != server.URL+"/thumb.jpg" {
		t.Fatalf("body=%q evidence=%+v err=%v", body, evidence, err)
	}
	if _, _, err := client.FetchURL(context.Background(), "https://open.spotify.com/album/abc123"); err == nil {
		t.Fatal("non-track Spotify URL accepted")
	}
}
