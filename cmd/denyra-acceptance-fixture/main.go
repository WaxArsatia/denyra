package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type fixtureState struct {
	Requests             uint64                    `json:"requests"`
	RequestsByRoute      map[string]int            `json:"requests_by_route"`
	LidarrMutations      int                       `json:"lidarr_mutations"`
	LidarrSearchCommands int                       `json:"lidarr_search_commands"`
	ManualImportCommands int                       `json:"manual_import_commands"`
	ManualImportHashes   map[string]bool           `json:"manual_import_hashes,omitempty"`
	CatalogArtistAdded   bool                      `json:"catalog_artist_added"`
	CatalogRefreshed     bool                      `json:"catalog_refreshed"`
	CatalogMonitored     bool                      `json:"catalog_monitored"`
	NavidromeCreates     int                       `json:"navidrome_creates"`
	SFTPGoCreates        int                       `json:"sftpgo_creates"`
	Roots                []map[string]any          `json:"roots"`
	Singletons           map[string]map[string]any `json:"singletons"`
	Metadata             []map[string]any          `json:"metadata"`
	DownloadClients      []map[string]any          `json:"download_clients"`
	Indexers             []map[string]any          `json:"indexers"`
	SFTPGoUploadUser     map[string]any            `json:"sftpgo_upload_user,omitempty"`
	FilesystemManifests  map[string][]fixtureFile  `json:"filesystem_manifests,omitempty"`
}

type fixtureFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type fixture struct {
	mu        sync.Mutex
	state     fixtureState
	stateFile string
}

func main() {
	address := os.Getenv("DENYRA_ACCEPTANCE_FIXTURE_ADDRESS")
	if address == "" {
		address = "0.0.0.0:18080"
	}
	f := newFixture(os.Getenv("DENYRA_ACCEPTANCE_EVIDENCE_FILE"))
	server := &http.Server{Addr: address, Handler: f.routes(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newFixture(stateFile string) *fixture {
	f := &fixture{stateFile: stateFile, state: fixtureState{
		RequestsByRoute:    map[string]int{},
		ManualImportHashes: map[string]bool{},
		Roots: []map[string]any{{
			"id": 1, "path": "/data/library", "defaultQualityProfileId": 4, "defaultMetadataProfileId": 5,
		}},
		Singletons: map[string]map[string]any{
			"/api/v1/config/downloadclient":  {"id": 1, "enableCompletedDownloadHandling": true},
			"/api/v1/config/mediamanagement": {"id": 1, "importExtraFiles": false, "extraFileExtensions": "lrc", "recycleBinCleanupDays": 31},
			"/api/v1/config/naming":          {"id": 1, "renameTracks": false, "standardTrackFormat": "old", "multiDiscTrackFormat": "old", "artistFolderFormat": "old"},
		},
		Metadata: []map[string]any{{
			"id": 1, "implementation": "XbmcMetadata", "name": "Kodi (XBMC) / Emby", "enable": false,
			"fields": []any{
				map[string]any{"name": "artistImages", "value": false},
				map[string]any{"name": "albumImages", "value": false},
			},
		}},
		DownloadClients: []map[string]any{},
		Indexers:        []map[string]any{},
	}}
	if stateFile != "" {
		if content, err := os.ReadFile(stateFile); err == nil {
			if err := json.Unmarshal(content, &f.state); err != nil {
				log.Fatalf("decode acceptance state: %v", err)
			}
		} else if !os.IsNotExist(err) {
			log.Fatalf("read acceptance state: %v", err)
		}
	}
	f.normalize()
	return f
}

func (f *fixture) normalize() {
	if f.state.RequestsByRoute == nil {
		f.state.RequestsByRoute = map[string]int{}
	}
	if f.state.ManualImportHashes == nil {
		f.state.ManualImportHashes = map[string]bool{}
	}
	if f.state.Singletons == nil {
		f.state.Singletons = map[string]map[string]any{}
	}
}

func (f *fixture) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]any{"ok": true}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"live": true, "ready": true})
	})
	mux.HandleFunc("GET /acceptance/evidence", f.evidence)
	mux.HandleFunc("/", f.handle)
	return mux
}

func (f *fixture) evidence(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotFileTrees()
	writeJSON(w, f.state)
}

func (f *fixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Requests++
	f.state.RequestsByRoute[r.Method+" "+r.URL.Path]++
	defer f.persist()

	if f.handleMusicBrainz(w, r) || f.handleNavidrome(w, r) || f.handleLidarr(w, r) || f.handleAccounts(w, r) {
		return
	}
	switch r.URL.Path {
	case "/api/v1/system/status":
		writeJSON(w, map[string]any{"version": "acceptance-fixture"})
	case "/api/v1/wanted/missing", "/api/v1/queue", "/api/v1/history":
		writeJSON(w, map[string]any{"records": []any{}, "totalRecords": 0})
	case "/api/get", "/api/search":
		writeJSON(w, map[string]any{"recordings": []any{}, "releases": []any{}})
	case "/oembed":
		writeJSON(w, map[string]any{"thumbnail_url": ""})
	case "/release/" + acceptanceReleaseMBID:
		writeJSON(w, map[string]any{"images": []any{}})
	default:
		http.Error(w, "fixture no-result", http.StatusNotFound)
	}
}

func (f *fixture) snapshotFileTrees() {
	f.state.FilesystemManifests = map[string][]fixtureFile{}
	for name, root := range map[string]string{"managed": "/acceptance-library-managed", "unmanaged": "/acceptance-library-unmanaged"} {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		var records []fixtureFile
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
				return walkErr
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(content)
			records = append(records, fixtureFile{Path: filepath.ToSlash(relative), SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(content))})
			return nil
		})
		f.state.FilesystemManifests[name] = records
	}
}

func (f *fixture) handleMusicBrainz(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/ws/2/") {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/ws/2/release/") {
		id := strings.TrimPrefix(r.URL.Path, "/ws/2/release/")
		if id != acceptanceReleaseMBID && id != acceptanceAlternateReleaseMBID {
			http.NotFound(w, r)
			return true
		}
		writeJSON(w, acceptanceRelease(id))
		return true
	}
	if r.URL.Path == "/ws/2/release" {
		query := strings.ToLower(r.URL.Query().Get("query"))
		if strings.Contains(query, "provider error") {
			http.Error(w, "injected MusicBrainz outage", http.StatusServiceUnavailable)
			return true
		}
		ids := []string{acceptanceReleaseMBID}
		switch {
		case strings.Contains(query, "no match"):
			ids = nil
		case strings.Contains(query, "ambiguous"):
			ids = append(ids, acceptanceAlternateReleaseMBID)
		}
		releases := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			releases = append(releases, map[string]any{"id": id})
		}
		writeJSON(w, map[string]any{"count": len(releases), "releases": releases})
		return true
	}
	if r.URL.Path == "/ws/2/recording" {
		writeJSON(w, map[string]any{"count": 0, "recordings": []any{}})
		return true
	}
	http.NotFound(w, r)
	return true
}

func (f *fixture) handleNavidrome(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/auth/login":
		writeJSON(w, map[string]any{"token": "fixture-token", "subsonicToken": "token", "subsonicSalt": "salt"})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/library/":
		writeJSON(w, []map[string]any{{"id": 1, "name": "Managed", "path": "/music-managed", "defaultNewUsers": true}, {"id": 2, "name": "Unmanaged", "path": "/music-unmanaged", "defaultNewUsers": false}})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/rest/getMusicFolders.view":
		writeSubsonic(w, map[string]any{"musicFolders": map[string]any{"musicFolder": []map[string]any{{"id": 1, "name": "Managed"}, {"id": 2, "name": "Unmanaged"}}}})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/rest/getScanStatus.view":
		writeSubsonic(w, map[string]any{"scanStatus": map[string]any{"scanning": false}})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/rest/startScan.view":
		writeSubsonic(w, nil)
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/rest/search3.view":
		folder := r.URL.Query().Get("musicFolderId")
		albums := []map[string]any{}
		if folder == "1" || folder == "2" {
			albums = append(albums, map[string]any{"name": "OFF GUARD", "artist": "Kaleb J", "songCount": 1})
		}
		writeSubsonic(w, map[string]any{"searchResult3": map[string]any{"album": albums}})
		return true
	}
	return false
}

func (f *fixture) handleLidarr(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		var response any
		switch r.URL.Path {
		case "/api/v1/rootfolder":
			response = f.state.Roots
		case "/api/v1/metadata":
			response = f.state.Metadata
		case "/api/v1/downloadclient":
			response = f.state.DownloadClients
		case "/api/v1/indexer":
			response = f.state.Indexers
		case "/api/v1/downloadclient/schema":
			response = []map[string]any{slskdSchema("SlskdSettings", []string{"host", "port", "useSsl", "urlBase", "apiKey", "repairConfiguration"})}
		case "/api/v1/indexer/schema":
			response = []map[string]any{slskdSchema("SlskdIndexerSettings", []string{"baseUrl", "apiKey", "minimumPeerUploadSpeed", "maximumPeerQueueLength", "allowIncompleteReleases", "verifyDurations"})}
		case "/api/v1/qualityprofile":
			response = []map[string]any{{"id": 4, "name": "Lossless"}}
		case "/api/v1/metadataprofile":
			response = []map[string]any{{"id": 5, "name": "Standard"}}
		case "/api/v1/artist/lookup":
			response = []map[string]any{{"foreignArtistId": acceptanceArtistMBID, "artistName": "Kaleb J", "qualityProfileId": 4, "metadataProfileId": 5}}
		case "/api/v1/artist":
			response = []map[string]any{}
			if f.state.CatalogArtistAdded {
				response = []map[string]any{{"id": 70, "foreignArtistId": acceptanceArtistMBID, "artistName": "Kaleb J", "qualityProfileId": 4, "metadataProfileId": 5}}
			}
		case "/api/v1/album":
			response = []map[string]any{}
			if f.state.CatalogRefreshed {
				response = []map[string]any{acceptanceAlbum(f.state.CatalogMonitored)}
			}
		case "/api/v1/album/80":
			response = acceptanceAlbum(f.state.CatalogMonitored)
		case "/api/v1/command/42":
			response = map[string]any{"id": 42, "status": "completed"}
		default:
			singleton, ok := f.state.Singletons[r.URL.Path]
			if !ok {
				return false
			}
			response = singleton
		}
		writeJSON(w, response)
		return true
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		return false
	}
	isLidarrMutation := r.URL.Path == "/api/v1/rootfolder" ||
		r.URL.Path == "/api/v1/artist" || r.URL.Path == "/api/v1/command" || r.URL.Path == "/api/v1/album/80" ||
		r.URL.Path == "/api/v1/downloadclient" || r.URL.Path == "/api/v1/indexer" ||
		strings.HasPrefix(r.URL.Path, "/api/v1/metadata/") ||
		strings.HasPrefix(r.URL.Path, "/api/v1/downloadclient/") ||
		strings.HasPrefix(r.URL.Path, "/api/v1/indexer/") ||
		f.state.Singletons[r.URL.Path] != nil
	if !isLidarrMutation {
		return false
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/rootfolder":
		payload["id"] = 1
		f.state.Roots = append(f.state.Roots, payload)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/downloadclient":
		payload["id"] = 1
		f.state.DownloadClients = append(f.state.DownloadClients, payload)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/indexer":
		payload["id"] = 1
		f.state.Indexers = append(f.state.Indexers, payload)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/metadata/"):
		f.state.Metadata[0] = payload
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/downloadclient/"):
		f.state.DownloadClients[0] = payload
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/indexer/"):
		f.state.Indexers[0] = payload
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/artist":
		f.state.CatalogArtistAdded = true
		payload["id"] = 70
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
		name := fmt.Sprint(payload["name"])
		switch name {
		case "ManualImport":
			encoded, _ := json.Marshal(payload)
			hash := fmt.Sprintf("%x", sha256.Sum256(encoded))
			first := !f.state.ManualImportHashes[hash]
			if first {
				f.state.ManualImportHashes[hash] = true
				f.state.ManualImportCommands++
			}
			if r.URL.Query().Get("fault") == "lost-ack" && first {
				http.Error(w, "injected lost acknowledgement", http.StatusGatewayTimeout)
				return true
			}
			payload = map[string]any{"id": 43, "name": name, "status": "queued"}
		case "RefreshArtist":
			f.state.CatalogRefreshed = true
			payload = map[string]any{"id": 42, "name": name, "status": "queued"}
		case "ArtistSearch", "AlbumSearch":
			f.state.LidarrSearchCommands++
			payload = map[string]any{"id": 44, "name": name, "status": "queued"}
		}
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/album/80":
		f.state.CatalogMonitored = true
		payload = acceptanceAlbum(true)
	case r.Method == http.MethodPut && f.state.Singletons[r.URL.Path] != nil:
		f.state.Singletons[r.URL.Path] = payload
	default:
		return false
	}
	f.state.LidarrMutations++
	writeJSON(w, payload)
	return true
}

const (
	acceptanceArtistMBID           = "11111111-1111-1111-1111-111111111111"
	acceptanceReleaseMBID          = "22222222-2222-2222-2222-222222222222"
	acceptanceAlternateReleaseMBID = "66666666-6666-6666-6666-666666666666"
)

func acceptanceRelease(id string) map[string]any {
	duration := 1000
	credit := []map[string]any{{"name": "Kaleb J", "artist": map[string]any{"id": acceptanceArtistMBID, "name": "Kaleb J"}}}
	recording := map[string]any{
		"id": "55555555-5555-5555-5555-555555555555", "title": "Acceptance Tone", "length": duration,
		"isrcs": []string{"QZAAA2600001"}, "artist-credit": credit,
	}
	track := map[string]any{
		"id": "44444444-4444-4444-4444-444444444444", "title": "Acceptance Tone", "number": "1",
		"position": 1, "length": duration, "artist-credit": credit, "recording": recording,
	}
	medium := map[string]any{"position": 1, "track-count": 1, "tracks": []map[string]any{track}}
	return map[string]any{
		"id": id, "title": "OFF GUARD", "date": "2024", "status": "Official",
		"release-group": map[string]any{"id": "33333333-3333-3333-3333-333333333333"}, "artist-credit": credit,
		"media": []map[string]any{medium},
	}
}

func acceptanceAlbum(monitored bool) map[string]any {
	return map[string]any{"id": 80, "artistId": 70, "title": "OFF GUARD", "monitored": monitored, "releases": []map[string]any{{"id": 90, "foreignReleaseId": acceptanceReleaseMBID}}}
}

func writeSubsonic(w http.ResponseWriter, fields map[string]any) {
	response := map[string]any{"status": "ok", "version": "1.16.1"}
	for key, value := range fields {
		response[key] = value
	}
	writeJSON(w, map[string]any{"subsonic-response": response})
}

func (f *fixture) handleAccounts(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/auth/createAdmin":
		if f.state.NavidromeCreates > 0 {
			http.Error(w, "existing administrator", http.StatusForbidden)
			return true
		}
		f.state.NavidromeCreates++
		writeJSON(w, map[string]any{"created": true})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/token":
		writeJSON(w, map[string]any{"access_token": "acceptance-token"})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/users/upload":
		if f.state.SFTPGoUploadUser == nil {
			http.NotFound(w, r)
		} else {
			writeJSON(w, f.state.SFTPGoUploadUser)
		}
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/users":
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		f.state.SFTPGoUploadUser = payload
		f.state.SFTPGoCreates++
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, payload)
		return true
	}
	return false
}

func (f *fixture) persist() {
	if f.stateFile == "" {
		return
	}
	content, err := json.MarshalIndent(f.state, "", "  ")
	if err != nil {
		log.Printf("encode acceptance state: %v", err)
		return
	}
	temporary := f.stateFile + ".tmp"
	if err := os.MkdirAll(filepath.Dir(f.stateFile), 0o750); err != nil {
		log.Printf("create acceptance state directory: %v", err)
		return
	}
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		log.Printf("write acceptance state: %v", err)
		return
	}
	if err := os.Rename(temporary, f.stateFile); err != nil {
		log.Printf("publish acceptance state: %v", err)
	}
}

func slskdSchema(contract string, names []string) map[string]any {
	fields := make([]any, 0, len(names))
	for _, name := range names {
		fields = append(fields, map[string]any{"name": name, "value": nil})
	}
	return map[string]any{"implementation": "Slskd", "configContract": contract, "protocol": "torrent", "fields": fields}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func numericID(value any) string {
	switch value := value.(type) {
	case float64:
		return strconv.Itoa(int(value))
	default:
		return fmt.Sprint(value)
	}
}
