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

func TestMaintenanceRequiresAuthenticationAndBackupRouteIsAbsent(t *testing.T) {
	db := migratedGatewayDB(t)
	routes := transport.Routes{
		Quality: transport.QualityCallbackAPI{BodyLimit: 4096, Bearer: []byte("secret")},
		Store:   persistence.New(db, time.Now), BodyLimit: 4096, Bearer: []byte("secret"),
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

	enable := authenticatedJSONRequest(http.MethodPost, "/internal/maintenance", `{"enabled":true,"reason":"operator pause"}`)
	enableResponse := httptest.NewRecorder()
	handler.ServeHTTP(enableResponse, enable)
	if enableResponse.Code != http.StatusOK || !bytes.Contains(enableResponse.Body.Bytes(), []byte(`"safe":true`)) {
		t.Fatalf("enable status = %d body=%s", enableResponse.Code, enableResponse.Body.String())
	}

	backup := authenticatedJSONRequest(http.MethodPost, "/internal/maintenance/backup", `{"target":"/tmp/retired.db"}`)
	backupResponse := httptest.NewRecorder()
	handler.ServeHTTP(backupResponse, backup)
	if backupResponse.Code != http.StatusNotFound {
		t.Fatalf("backup status = %d body=%s", backupResponse.Code, backupResponse.Body.String())
	}
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
