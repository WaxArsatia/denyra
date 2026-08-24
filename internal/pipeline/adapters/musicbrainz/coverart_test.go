package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
)

func TestCoverArtFetchesDeclaredHTTPSFrontImage(t *testing.T) {
	t.Parallel()
	id := "11111111-1111-1111-1111-111111111111"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release/" + id:
			_, _ = w.Write([]byte(`{"images":[{"front":false,"image":"` + server.URL + `/back.jpg"},{"front":true,"image":"` + server.URL + `/front.jpg"}]}`))
		case "/front.jpg":
			_, _ = w.Write([]byte("front"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	body, evidence, err := (musicbrainz.CoverArt{BaseURL: server.URL, HTTP: server.Client(), ResponseLimit: 1024}).FetchRelease(context.Background(), id)
	if err != nil || string(body) != "front" || evidence.ResponseSHA256 == "" || evidence.Endpoint != server.URL+"/front.jpg" {
		t.Fatalf("body=%q evidence=%+v err=%v", body, evidence, err)
	}
}
