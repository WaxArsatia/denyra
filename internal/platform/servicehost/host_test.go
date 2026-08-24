package servicehost_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	"github.com/waxarsatia/denyra/internal/platform/servicehost"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func TestPreparePersistsImmutableConfigWithoutDeploymentIdentity(t *testing.T) {
	options := makeOptions(t)
	prepared, err := servicehost.Prepare(context.Background(), slog.Default(), options)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer prepared.Close()

	var configCount int
	if err := prepared.DB.QueryRow("SELECT COUNT(*) FROM config_snapshots").Scan(&configCount); err != nil {
		t.Fatalf("count config snapshots: %v", err)
	}
	if configCount != 1 || !prepared.Health.Snapshot().Ready {
		t.Fatalf("prepared runtime: config=%d health=%+v", configCount, prepared.Health.Snapshot())
	}
	if prepared.Config.Secrets.NavidromeAdmin.Value != "navidrome-admin" {
		t.Fatalf("Navidrome secret was not resolved")
	}
	second, err := servicehost.Prepare(context.Background(), slog.Default(), options)
	if err != nil {
		t.Fatalf("Prepare second run: %v", err)
	}
	defer second.Close()
	if err := second.DB.QueryRow("SELECT COUNT(*) FROM config_snapshots").Scan(&configCount); err != nil {
		t.Fatalf("count snapshots after restart: %v", err)
	}
	if configCount != 1 {
		t.Fatalf("identical config created mutable/duplicate snapshot count %d", configCount)
	}
}

func TestPrepareRejectsMigrationFailure(t *testing.T) {
	options := makeOptions(t)
	options.Migrations = []denysqlite.Migration{{Sequence: 1, Name: "broken", SQL: "CREATE TABLE"}}
	if _, err := servicehost.Prepare(context.Background(), slog.Default(), options); err == nil || !strings.Contains(err.Error(), "migration") {
		t.Fatalf("migration error = %v", err)
	}
}

func TestPrepareRejectsMissingBinary(t *testing.T) {
	options := makeOptions(t)
	options.RequiredBinaries = []string{"definitely-not-a-denyra-binary"}
	if _, err := servicehost.Prepare(context.Background(), slog.Default(), options); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary error = %v", err)
	}
}

func TestPrepareRejectsBadDeviceIdentity(t *testing.T) {
	options := makeOptions(t)
	options.CheckFilesystem = func(config.Config) error { return errors.New("device identity differs") }
	if _, err := servicehost.Prepare(context.Background(), slog.Default(), options); err == nil || !strings.Contains(err.Error(), "device identity differs") {
		t.Fatalf("filesystem error = %v", err)
	}
}

func TestPrepareDoesNotRequireUnrequestedServiceSecret(t *testing.T) {
	options := makeOptions(t)
	options.AdditionalSecrets = nil
	if err := os.Remove(filepath.Join(filepath.Dir(options.ConfigPath), "navidrome.admin")); err != nil {
		t.Fatalf("remove Navidrome fixture: %v", err)
	}
	if _, err := servicehost.Prepare(context.Background(), slog.Default(), options); err != nil {
		t.Fatalf("Prepare required an unrequested service secret: %v", err)
	}
}

func TestRunShutsDownGracefully(t *testing.T) {
	options := makeOptions(t)
	options.ServeAdmin = true
	options.BuildAcquisitionHandler = func(*servicehost.Prepared) (http.Handler, error) {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusAccepted) }), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if err := servicehost.Run(ctx, slog.Default(), options); err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
}

func makeOptions(t *testing.T) servicehost.Options {
	t.Helper()
	root := t.TempDir()
	paths := map[string]string{
		"slskd": "downloads/slskd", "spotiflac": "downloads/spotiflac", "other": "downloads/other",
		"incoming": "incoming/manual", "uploading": "incoming/uploading", "work": "processing/work", "approved": "processing/approved",
		"quarantine": "quarantine", "library": "library", "unmanaged": "library-unmanaged", "gateway_state": "state/gateway", "pipeline_state": "state/pipeline",
	}
	for _, relative := range paths {
		if err := os.MkdirAll(filepath.Join(root, relative), 0o750); err != nil {
			t.Fatalf("create fixture path: %v", err)
		}
	}
	auditKeyPath := filepath.Join(root, "audit.key")
	internalBearerPath := filepath.Join(root, "internal.bearer")
	lidarrKeyPath := filepath.Join(root, "lidarr.key")
	navidromeAdminPath := filepath.Join(root, "navidrome.admin")
	for path, value := range map[string]string{auditKeyPath: "audit-key-with-adequate-entropy", internalBearerPath: "internal-bearer", lidarrKeyPath: "lidarr-key", navidromeAdminPath: "navidrome-admin"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatalf("write secret fixture: %v", err)
		}
	}
	configPath := filepath.Join(root, "denyra.toml")
	configText := fmt.Sprintf(`
[http]
admin_address = "127.0.0.1:0"
internal_address = "127.0.0.1:0"
acquisition_event_address = "127.0.0.1:0"

[database]
gateway_path = %q
pipeline_path = %q

[filesystem]
data_root = %q
downloads_slskd = %q
downloads_spotiflac = %q
downloads_other = %q
incoming_manual = %q
incoming_uploading = %q
work = %q
approved = %q
quarantine = %q
library = %q
library_unmanaged = %q

[secrets.internal_bearer]
source = "file"
name = %q

[secrets.audit_key]
source = "file"
name = %q

[secrets.lidarr_api_key]
source = "file"
name = %q

[secrets.navidrome_admin]
source = "file"
name = %q
`,
		filepath.Join(root, paths["gateway_state"], "denyra.db"), filepath.Join(root, paths["pipeline_state"], "denyra.db"), root,
		filepath.Join(root, paths["slskd"]), filepath.Join(root, paths["spotiflac"]), filepath.Join(root, paths["other"]),
		filepath.Join(root, paths["incoming"]), filepath.Join(root, paths["uploading"]), filepath.Join(root, paths["work"]), filepath.Join(root, paths["approved"]),
		filepath.Join(root, paths["quarantine"]), filepath.Join(root, paths["library"]), filepath.Join(root, paths["unmanaged"]), internalBearerPath, auditKeyPath, lidarrKeyPath, navidromeAdminPath,
	)
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return servicehost.Options{
		Name:                 "test-service",
		ConfigPath:           configPath,
		DatabasePath:         func(cfg config.Config) string { return cfg.Database.PipelinePath },
		Migrations:           []denysqlite.Migration{{Sequence: 1, Name: "foundation", SQL: foundationSQL}},
		CheckFilesystem:      func(config.Config) error { return nil },
		ExternalDependencies: []string{"musicbrainz", "lrclib"},
		AdditionalSecrets:    func(cfg *config.Config) []*config.SecretRef { return []*config.SecretRef{&cfg.Secrets.NavidromeAdmin} },
		Now:                  func() time.Time { return time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC) },
	}
}

const foundationSQL = `
CREATE TABLE config_snapshots (
    id TEXT PRIMARY KEY,
    canonical_json BLOB NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);
`
