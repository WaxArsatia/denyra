package transport_test

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/gateway/transport"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestMaintenanceRequiresAuthenticationAndOnlineBackupRequiresMaintenance(t *testing.T) {
	db := migratedGatewayDB(t)
	backupRoot := t.TempDir()
	routes := transport.Routes{
		Quality: transport.QualityCallbackAPI{BodyLimit: 4096, Bearer: []byte("secret")},
		Store:   persistence.New(db, time.Now), BodyLimit: 4096, Bearer: []byte("secret"),
		BackupRoot: backupRoot,
	}
	handler, err := routes.Handler()
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/internal/maintenance", bytes.NewBufferString(`{"enabled":true,"reason":"backup"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	target := filepath.Join(backupRoot, "gateway-backup.db")
	before := authenticatedJSONRequest(http.MethodPost, "/internal/maintenance/backup", `{"target":"`+target+`"}`)
	beforeResponse := httptest.NewRecorder()
	handler.ServeHTTP(beforeResponse, before)
	if beforeResponse.Code != http.StatusConflict {
		t.Fatalf("backup before maintenance status = %d body=%s", beforeResponse.Code, beforeResponse.Body.String())
	}

	enable := authenticatedJSONRequest(http.MethodPost, "/internal/maintenance", `{"enabled":true,"reason":"deterministic backup"}`)
	enableResponse := httptest.NewRecorder()
	handler.ServeHTTP(enableResponse, enable)
	if enableResponse.Code != http.StatusOK || !bytes.Contains(enableResponse.Body.Bytes(), []byte(`"safe":true`)) {
		t.Fatalf("enable status = %d body=%s", enableResponse.Code, enableResponse.Body.String())
	}

	backup := authenticatedJSONRequest(http.MethodPost, "/internal/maintenance/backup", `{"target":"`+target+`"}`)
	backupResponse := httptest.NewRecorder()
	handler.ServeHTTP(backupResponse, backup)
	if backupResponse.Code != http.StatusCreated {
		t.Fatalf("backup status = %d body=%s", backupResponse.Code, backupResponse.Body.String())
	}
	assertSQLiteIntegrity(t, target)
}

func migratedGatewayDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := denysqlite.Open(context.Background(), filepath.Join(t.TempDir(), "gateway.db"), denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	steps, err := migrations.For("gateway")
	if err != nil {
		t.Fatal(err)
	}
	if err := denysqlite.Migrate(context.Background(), db, steps, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return db
}

func authenticatedJSONRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func assertSQLiteIntegrity(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("integrity_check = %q", result)
	}
}
