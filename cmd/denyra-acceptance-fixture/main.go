package main

import (
	"context"
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
	Requests         uint64                    `json:"requests"`
	LidarrMutations  int                       `json:"lidarr_mutations"`
	NavidromeCreates int                       `json:"navidrome_creates"`
	SFTPGoCreates    int                       `json:"sftpgo_creates"`
	Roots            []map[string]any          `json:"roots"`
	Singletons       map[string]map[string]any `json:"singletons"`
	Metadata         []map[string]any          `json:"metadata"`
	DownloadClients  []map[string]any          `json:"download_clients"`
	Indexers         []map[string]any          `json:"indexers"`
	SFTPGoUploadUser map[string]any            `json:"sftpgo_upload_user,omitempty"`
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
	return f
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
	writeJSON(w, f.state)
}

func (f *fixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Requests++
	defer f.persist()

	if f.handleLidarr(w, r) || f.handleAccounts(w, r) {
		return
	}
	switch r.URL.Path {
	case "/api/v1/system/status":
		writeJSON(w, map[string]any{"version": "acceptance-fixture"})
	case "/api/v1/wanted/missing", "/api/v1/queue", "/api/v1/history":
		writeJSON(w, map[string]any{"records": []any{}, "totalRecords": 0})
	case "/api/get", "/api/search":
		writeJSON(w, map[string]any{"recordings": []any{}, "releases": []any{}})
	default:
		http.Error(w, "fixture no-result", http.StatusNotFound)
	}
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
	case r.Method == http.MethodPut && f.state.Singletons[r.URL.Path] != nil:
		f.state.Singletons[r.URL.Path] = payload
	default:
		return false
	}
	f.state.LidarrMutations++
	writeJSON(w, payload)
	return true
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
