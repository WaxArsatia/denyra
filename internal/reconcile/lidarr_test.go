package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type lidarrFixture struct {
	t               *testing.T
	server          *httptest.Server
	mu              sync.Mutex
	mutations       int
	roots           []map[string]any
	singletons      map[string]map[string]any
	metadata        []map[string]any
	downloadClients []map[string]any
	indexers        []map[string]any
	downloadSchemas []map[string]any
	indexerSchemas  []map[string]any
}

func newLidarrFixture(t *testing.T) *lidarrFixture {
	t.Helper()
	f := &lidarrFixture{
		t: t,
		singletons: map[string]map[string]any{
			"/api/v1/config/downloadclient":  {"id": 1, "enableCompletedDownloadHandling": true},
			"/api/v1/config/mediamanagement": {"id": 1, "importExtraFiles": false, "extraFileExtensions": "lrc", "recycleBinCleanupDays": 31},
			"/api/v1/config/naming":          {"id": 1, "renameTracks": false, "standardTrackFormat": "old", "multiDiscTrackFormat": "old", "artistFolderFormat": "old"},
		},
		metadata: []map[string]any{{
			"id": 1, "implementation": "XbmcMetadata", "name": "Kodi (XBMC) / Emby", "enable": false,
			"fields": []any{
				map[string]any{"name": "artistMetadata", "value": false},
				map[string]any{"name": "albumMetadata", "value": false},
				map[string]any{"name": "artistImages", "value": false},
				map[string]any{"name": "albumImages", "value": false},
			},
		}},
		downloadClients: []map[string]any{{"id": 9, "name": "Existing client", "implementation": "Transmission", "unrelated": "keep"}},
		indexers:        []map[string]any{{"id": 8, "name": "Existing indexer", "implementation": "Newznab", "unrelated": "keep"}},
		downloadSchemas: []map[string]any{slskdSchema("SlskdSettings", []string{"host", "port", "useSsl", "urlBase", "apiKey", "repairConfiguration"})},
		indexerSchemas:  []map[string]any{slskdSchema("SlskdIndexerSettings", []string{"baseUrl", "apiKey", "minimumPeerUploadSpeed", "maximumPeerQueueLength", "allowIncompleteReleases", "verifyDurations"})},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func slskdSchema(contract string, names []string) map[string]any {
	fields := make([]any, 0, len(names))
	for _, name := range names {
		fields = append(fields, map[string]any{"name": name, "value": nil, "label": name})
	}
	return map[string]any{
		"implementation": "Slskd", "implementationName": "Slskd", "configContract": contract,
		"protocol": "torrent", "fields": fields, "schemaExtra": "keep",
	}
}

func (f *lidarrFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Api-Key") != "lidarr-secret" {
		http.Error(w, "missing API key", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		var response any
		switch r.URL.Path {
		case "/api/v1/rootfolder":
			response = f.roots
		case "/api/v1/metadata":
			response = f.metadata
		case "/api/v1/downloadclient":
			response = f.downloadClients
		case "/api/v1/indexer":
			response = f.indexers
		case "/api/v1/downloadclient/schema":
			response = f.downloadSchemas
		case "/api/v1/indexer/schema":
			response = f.indexerSchemas
		default:
			response = f.singletons[r.URL.Path]
			if response == nil {
				http.NotFound(w, r)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mutations++
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/rootfolder":
		payload["id"] = 1
		f.roots = append(f.roots, payload)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/downloadclient":
		payload["id"] = float64(10)
		f.downloadClients = append(f.downloadClients, payload)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/indexer":
		payload["id"] = float64(10)
		f.indexers = append(f.indexers, payload)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/metadata/"):
		f.metadata[0] = payload
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/downloadclient/"):
		f.replaceByID(&f.downloadClients, r.URL.Path, payload)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/indexer/"):
		f.replaceByID(&f.indexers, r.URL.Path, payload)
	case r.Method == http.MethodPut:
		if _, ok := f.singletons[r.URL.Path]; !ok {
			http.NotFound(w, r)
			return
		}
		f.singletons[r.URL.Path] = payload
	default:
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func (f *lidarrFixture) replaceByID(resources *[]map[string]any, path string, payload map[string]any) {
	want := path[strings.LastIndex(path, "/")+1:]
	for index, resource := range *resources {
		if fmt.Sprint(resource["id"]) == want || strconv.Itoa(int(number(resource["id"]))) == want {
			(*resources)[index] = payload
			return
		}
	}
	f.t.Errorf("resource %s not found", path)
}

func TestLidarrApplyCreatesOwnedContractAndIsIdempotent(t *testing.T) {
	f := newLidarrFixture(t)
	lidarr := Lidarr{BaseURL: f.server.URL, APIKey: "lidarr-secret", SlskdURL: "http://slskd:5030", SlskdAPIKey: "slskd-secret", HTTP: f.server.Client()}
	outcome, err := lidarr.Apply(context.Background())
	if err != nil || !outcome.Changed {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	firstMutations := f.mutations
	if firstMutations == 0 {
		t.Fatal("fresh Lidarr was not reconciled")
	}
	outcome, err = lidarr.Apply(context.Background())
	if err != nil || outcome.Changed || f.mutations != firstMutations {
		t.Fatalf("rerun outcome=%+v err=%v mutations=%d->%d", outcome, err, firstMutations, f.mutations)
	}

	if len(f.roots) != 1 || f.roots[0]["path"] != "/data/library" {
		t.Errorf("roots=%v", f.roots)
	}
	if got := f.singletons["/api/v1/config/downloadclient"]["enableCompletedDownloadHandling"]; got != false {
		t.Errorf("completed download handling=%v", got)
	}
	media := f.singletons["/api/v1/config/mediamanagement"]
	if media["importExtraFiles"] != true || media["extraFileExtensions"] != "lrc,elrc,ttml" || number(media["recycleBinCleanupDays"]) != 31 {
		t.Errorf("media config=%v", media)
	}
	naming := f.singletons["/api/v1/config/naming"]
	if naming["renameTracks"] != true || naming["standardTrackFormat"] != standardTrackFormat || naming["multiDiscTrackFormat"] != multiDiscTrackFormat || naming["artistFolderFormat"] != "{Artist Name}" {
		t.Errorf("naming config=%v", naming)
	}
	metadata := f.metadata[0]
	if metadata["enable"] != true || fieldValue(metadata, "artistImages") != true || fieldValue(metadata, "albumImages") != true {
		t.Errorf("metadata=%v", metadata)
	}
	assertSlskdResource(t, f.downloadClients, "Denyra slskd", map[string]any{
		"host": "slskd", "port": float64(5030), "useSsl": false, "urlBase": "", "apiKey": "slskd-secret", "repairConfiguration": false,
	})
	assertSlskdResource(t, f.indexers, "Denyra slskd indexer", map[string]any{
		"baseUrl": "http://slskd:5030/", "apiKey": "slskd-secret", "minimumPeerUploadSpeed": float64(0), "maximumPeerQueueLength": float64(0), "allowIncompleteReleases": false, "verifyDurations": true,
	})
	if f.downloadClients[0]["unrelated"] != "keep" || f.indexers[0]["unrelated"] != "keep" {
		t.Fatal("unrelated resources were changed")
	}
}

func TestLidarrRejectsIncompleteSlskdSchema(t *testing.T) {
	f := newLidarrFixture(t)
	f.downloadSchemas = []map[string]any{slskdSchema("SlskdSettings", []string{"host", "port", "useSsl", "urlBase", "apiKey"})}
	lidarr := Lidarr{BaseURL: f.server.URL, APIKey: "lidarr-secret", SlskdURL: "http://slskd:5030", SlskdAPIKey: "slskd-secret", HTTP: f.server.Client()}
	_, err := lidarr.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Lidarr Slskd schema lacks field repairConfiguration") {
		t.Fatalf("err=%v", err)
	}
}

func assertSlskdResource(t *testing.T, resources []map[string]any, name string, fields map[string]any) {
	t.Helper()
	for _, resource := range resources {
		if resource["implementation"] != "Slskd" {
			continue
		}
		if resource["name"] != name || resource["enable"] != true || resource["schemaExtra"] != "keep" {
			t.Errorf("resource=%v", resource)
		}
		for field, want := range fields {
			if got := fieldValue(resource, field); got != want {
				t.Errorf("field %s=%v want=%v", field, got, want)
			}
		}
		return
	}
	t.Fatalf("missing Slskd resource %q", name)
}

func fieldValue(resource map[string]any, name string) any {
	fields, _ := resource["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if field["name"] == name {
			return field["value"]
		}
	}
	return nil
}

func number(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}
