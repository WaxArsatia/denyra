package config_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
)

func TestDefaultsExposeApprovedPolicy(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	checks := map[string]struct {
		got  time.Duration
		want time.Duration
	}{
		"scanner recovery":  {time.Duration(cfg.Scanners.RecoveryInterval), 30 * time.Second},
		"stability":         {time.Duration(cfg.Scanners.StabilityInterval), 10 * time.Second},
		"album search":      {time.Duration(cfg.Acquisition.AlbumSearchTimeout), 10 * time.Minute},
		"poll":              {time.Duration(cfg.Acquisition.ReconciliationPoll), 2 * time.Second},
		"primary grace":     {time.Duration(cfg.Acquisition.PrimaryGraceWindow), time.Minute},
		"process poll":      {time.Duration(cfg.Acquisition.ProcessPollInterval), 250 * time.Millisecond},
		"process terminate": {time.Duration(cfg.Acquisition.ProcessTerminateGrace), 5 * time.Second},
		"acquisition lease": {time.Duration(cfg.Acquisition.LeaseDuration), 15 * time.Minute},
		"arbitration":       {time.Duration(cfg.Arbitration.Window), 30 * time.Minute},
		"session expiry":    {time.Duration(cfg.Sessions.AbsoluteExpiry), 30 * 24 * time.Hour},
		"ffprobe":           {time.Duration(cfg.Validation.FFProbeTimeout), 30 * time.Second},
		"flac test":         {time.Duration(cfg.Validation.FLACTestTimeout), 2 * time.Minute},
		"metaflac":          {time.Duration(cfg.Validation.MetaFLACTimeout), time.Minute},
		"beets":             {time.Duration(cfg.Validation.BeetsTimeout), 5 * time.Minute},
		"musicbrainz rate":  {time.Duration(cfg.Validation.MusicBrainzRateInterval), time.Second},
	}
	if time.Duration(cfg.HTTP.ReadHeaderTimeout) != 5*time.Second || time.Duration(cfg.HTTP.ServerIdleTimeout) != 30*time.Second ||
		time.Duration(cfg.HTTP.ShutdownTimeout) != 10*time.Second || time.Duration(cfg.HTTP.HealthcheckTimeout) != 5*time.Second {
		t.Fatalf("unexpected HTTP server policy: %+v", cfg.HTTP)
	}
	for name, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %s, want %s", name, check.got, check.want)
		}
	}
	if cfg.Storage.MinimumFreeBytes != 20*1024*1024*1024 || cfg.Storage.MinimumFreePercent != 5 {
		t.Fatalf("unexpected storage guardrail: %+v", cfg.Storage)
	}
	if time.Duration(cfg.HTTP.ExternalRequestTimeout) != 30*time.Second || cfg.HTTP.ExternalResponseLimit != 8<<20 {
		t.Fatalf("unexpected external HTTP policy: %+v", cfg.HTTP)
	}
	if cfg.HTTP.InternalReplayAttempts != 2 {
		t.Fatalf("unexpected internal replay attempts: %d", cfg.HTTP.InternalReplayAttempts)
	}
	if cfg.HTTP.AcquisitionEventAddress != "0.0.0.0:8082" {
		t.Fatalf("unexpected acquisition event address: %q", cfg.HTTP.AcquisitionEventAddress)
	}
	if cfg.Services.LidarrURL != "http://lidarr:8686" || cfg.Services.GatewayURL != "http://acquisition-gateway:8081" || cfg.Services.PipelineURL != "http://media-pipeline:8081" {
		t.Fatalf("unexpected internal service defaults: %+v", cfg.Services)
	}
	if cfg.Acquisition.LidarrPageSize != 100 {
		t.Fatalf("unexpected Lidarr page size: %d", cfg.Acquisition.LidarrPageSize)
	}
	if cfg.Acquisition.ProcessOutputLimit != 4<<20 {
		t.Fatalf("unexpected process output limit: %d", cfg.Acquisition.ProcessOutputLimit)
	}
	if cfg.Acquisition.MaxInlineTransitions != 8 {
		t.Fatalf("unexpected maximum inline transitions: %d", cfg.Acquisition.MaxInlineTransitions)
	}
	if got, want := durations(cfg.Acquisition.PrimaryRetry), []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}; !equalDurations(got, want) {
		t.Fatalf("primary retry = %v, want %v", got, want)
	}
}

func TestDefaultsIncludeUnmanagedUploadPolicy(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	if cfg.Filesystem.IncomingUploading != "/data/incoming/uploading" || cfg.Filesystem.LibraryUnmanaged != "/data/library-unmanaged" {
		t.Fatalf("unexpected unmanaged roots: %+v", cfg.Filesystem)
	}
	if cfg.Uploads.MaxFileBytes != 8<<30 || cfg.Uploads.MaxSessionBytes != 100<<30 || cfg.Uploads.MaxEntries != 1_000 {
		t.Fatalf("unexpected upload limits: %+v", cfg.Uploads)
	}
	if cfg.Uploads.BrowserConcurrency != 3 || cfg.Uploads.ImageMaxBytes != 20<<20 || cfg.Uploads.ImageMaxPixels != 40_000_000 {
		t.Fatalf("unexpected browser/image policy: %+v", cfg.Uploads)
	}
	if cfg.Concurrency.MigrationCheck != 3 {
		t.Fatalf("migration check concurrency = %d, want 3", cfg.Concurrency.MigrationCheck)
	}
	if cfg.Services.NavidromeURL != "http://navidrome:4533" || cfg.Services.SpotifyOEmbedURL != "https://open.spotify.com" || cfg.Services.CoverArtURL != "https://coverartarchive.org" {
		t.Fatalf("unexpected external service defaults: %+v", cfg.Services)
	}
}

func TestLoadAppliesDefaultsThenTOMLThenEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "denyra.toml")
	data := []byte("[acquisition]\nalbum_search_timeout = \"11m\"\n\n[storage]\nminimum_free_percent = 6.5\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	t.Setenv("DENYRA_ACQUISITION_ALBUM_SEARCH_TIMEOUT", "12m")
	t.Setenv("DENYRA_STORAGE_MINIMUM_FREE_PERCENT", "7.5")
	t.Setenv("DENYRA_LIDARR_URL", "http://lidarr-test:8686")
	t.Setenv("DENYRA_GATEWAY_URL", "http://gateway-test:8081")
	t.Setenv("DENYRA_ACQUISITION_LEASE_DURATION", "20m")
	t.Setenv("DENYRA_ACQUISITION_MAX_INLINE_TRANSITIONS", "12")
	t.Setenv("DENYRA_HTTP_ACQUISITION_EVENT_ADDRESS", "127.0.0.1:18082")

	cfg, err := config.Load(path, os.Environ())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := time.Duration(cfg.Acquisition.AlbumSearchTimeout); got != 12*time.Minute {
		t.Fatalf("album search timeout = %s, want 12m", got)
	}
	if cfg.Storage.MinimumFreePercent != 7.5 {
		t.Fatalf("minimum free percent = %v, want 7.5", cfg.Storage.MinimumFreePercent)
	}
	if cfg.Services.LidarrURL != "http://lidarr-test:8686" {
		t.Fatalf("Lidarr URL = %q", cfg.Services.LidarrURL)
	}
	if cfg.Services.GatewayURL != "http://gateway-test:8081" {
		t.Fatalf("Gateway URL = %q", cfg.Services.GatewayURL)
	}
	if got := time.Duration(cfg.Acquisition.LeaseDuration); got != 20*time.Minute {
		t.Fatalf("lease duration = %s, want 20m", got)
	}
	if cfg.Acquisition.MaxInlineTransitions != 12 {
		t.Fatalf("maximum inline transitions = %d, want 12", cfg.Acquisition.MaxInlineTransitions)
	}
	if cfg.HTTP.AcquisitionEventAddress != "127.0.0.1:18082" {
		t.Fatalf("acquisition event address = %q", cfg.HTTP.AcquisitionEventAddress)
	}
}

func TestLoadIgnoresRuntimeMetadataEnvironment(t *testing.T) {
	commit := strings.Repeat("a", 40)
	if _, err := config.Load("", []string{"DENYRA_GIT_COMMIT=" + commit}); err != nil {
		t.Fatalf("Load rejected runtime metadata: %v", err)
	}
}

func TestLoadRejectsUnknownAndInvalidConfiguration(t *testing.T) {
	tests := map[string]struct {
		toml string
		env  []string
	}{
		"unknown TOML":       {toml: "mystery = true\n"},
		"unknown env":        {env: []string{"DENYRA_MYSTERY=true"}},
		"invalid unit":       {toml: "[acquisition]\nalbum_search_timeout = \"ten minutes\"\n"},
		"negative":           {toml: "[storage]\nminimum_free_bytes = -1\n"},
		"percent":            {toml: "[storage]\nminimum_free_percent = 101\n"},
		"service URL":        {toml: "[services]\nlidarr_url = \"lidarr:8686\"\n"},
		"response cap":       {toml: "[http]\nexternal_response_limit = 0\n"},
		"replay attempts":    {toml: "[http]\ninternal_replay_attempts = 0\n"},
		"page size":          {toml: "[acquisition]\nlidarr_page_size = 0\n"},
		"process output":     {toml: "[acquisition]\nprocess_output_limit = 0\n"},
		"inline transitions": {toml: "[acquisition]\nmax_inline_transitions = 0\n"},
		"lease duration":     {toml: "[acquisition]\nlease_duration = \"0s\"\n"},
		"body limit":         {toml: "[http]\ninternal_body_limit = 0\n"},
		"database conns":     {toml: "[database]\nmax_open_conns = 0\n"},
		"concurrency":        {toml: "[concurrency]\nacquisition = 3\n"},
		"backup retention":   {toml: "[backup]\ndaily = 0\n"},
		"validation timeout": {toml: "[validation]\nffprobe_timeout = \"0s\"\n"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "denyra.toml")
			if err := os.WriteFile(path, []byte(test.toml), 0o600); err != nil {
				t.Fatalf("write config fixture: %v", err)
			}
			if _, err := config.Load(path, test.env); err == nil {
				t.Fatal("Load accepted invalid configuration")
			}
		})
	}
}

func TestValidateRejectsRetryAndPathContradictions(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.Acquisition.PrimaryRetry = []config.Duration{5 * config.Duration(time.Minute), config.Duration(time.Minute)}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted decreasing retry schedule")
	}

	cfg = config.Defaults()
	cfg.Filesystem.Work = cfg.Filesystem.Library
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted identical work and library paths")
	}
}

func TestValidateRejectsInvalidUploadPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*config.Config){
		"file bytes":          func(cfg *config.Config) { cfg.Uploads.MaxFileBytes = 0 },
		"session bytes":       func(cfg *config.Config) { cfg.Uploads.MaxSessionBytes = cfg.Uploads.MaxFileBytes - 1 },
		"entries":             func(cfg *config.Config) { cfg.Uploads.MaxEntries = 0 },
		"browser concurrency": func(cfg *config.Config) { cfg.Uploads.BrowserConcurrency = 0 },
		"image bytes":         func(cfg *config.Config) { cfg.Uploads.ImageMaxBytes = 0 },
		"image pixels":        func(cfg *config.Config) { cfg.Uploads.ImageMaxPixels = 0 },
		"migration workers":   func(cfg *config.Config) { cfg.Concurrency.MigrationCheck = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := config.Defaults()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted invalid unmanaged upload policy")
			}
		})
	}
}

func TestSnapshotIsCanonicalAndNeverContainsSecret(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.Secrets.InternalBearer = config.SecretRef{Source: "file", Name: "internal-bearer", Value: "low-entropy-secret"}
	cfg.Secrets.NavidromeAdmin = config.SecretRef{Source: "file", Name: "navidrome-admin", Value: "navidrome-password"}
	auditKey := []byte("separate-audit-key-with-enough-entropy")
	first, err := config.NewSnapshot(cfg, auditKey)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	second, err := config.NewSnapshot(cfg, auditKey)
	if err != nil {
		t.Fatalf("NewSnapshot second call: %v", err)
	}
	if !bytes.Equal(first.CanonicalJSON, second.CanonicalJSON) || first.Hash != second.Hash {
		t.Fatal("equal effective configs did not produce identical snapshots")
	}
	if strings.Contains(string(first.CanonicalJSON), "low-entropy-secret") {
		t.Fatal("snapshot contains secret value")
	}
	if strings.Contains(string(first.CanonicalJSON), "navidrome-password") {
		t.Fatal("snapshot contains Navidrome secret value")
	}
	expectedHash := sha256.Sum256(first.CanonicalJSON)
	if first.Hash != expectedHash {
		t.Fatal("snapshot hash does not cover canonical JSON")
	}
	changedSecret := cfg
	changedSecret.Secrets.NavidromeAdmin.Value = "different-navidrome-password"
	secretSnapshot, err := config.NewSnapshot(changedSecret, auditKey)
	if err != nil {
		t.Fatalf("NewSnapshot changed Navidrome secret: %v", err)
	}
	if secretSnapshot.Hash == first.Hash {
		t.Fatal("Navidrome secret rotation retained the old snapshot hash")
	}

	changed := cfg
	changed.Storage.MinimumFreePercent = 8
	third, err := config.NewSnapshot(changed, auditKey)
	if err != nil {
		t.Fatalf("NewSnapshot changed config: %v", err)
	}
	if third.Hash == first.Hash {
		t.Fatal("policy change retained the old snapshot hash")
	}
}

func durations(values []config.Duration) []time.Duration {
	result := make([]time.Duration, len(values))
	for i, value := range values {
		result[i] = time.Duration(value)
	}
	return result
}

func equalDurations(left, right []time.Duration) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
