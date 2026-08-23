package internalapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/internalapi"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestPipelineMaintenancePersistsAdmissionAndCreatesOnlineBackup(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "pipeline.db")
	db, err := denysqlite.Open(context.Background(), databasePath, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps, err := migrations.For("pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if err := denysqlite.Migrate(context.Background(), db, steps, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	gate := &application.AdmissionGate{DataRoot: t.TempDir(), MinimumFreeBytes: 1, MinimumFreePercent: 0}
	backupRoot := t.TempDir()
	handler, err := (internalapi.API{BodyLimit: 4096, Bearer: []byte("secret"), DB: db, Admission: gate, BackupRoot: backupRoot}).Handler()
	if err != nil {
		t.Fatal(err)
	}

	enable := pipelineRequest("/internal/maintenance", `{"enabled":true,"reason":"deterministic backup"}`)
	enableResponse := httptest.NewRecorder()
	handler.ServeHTTP(enableResponse, enable)
	if enableResponse.Code != http.StatusOK || !bytes.Contains(enableResponse.Body.Bytes(), []byte(`"safe":true`)) {
		t.Fatalf("enable status = %d body=%s", enableResponse.Code, enableResponse.Body.String())
	}
	if err := gate.AllowNew(); err != application.ErrMaintenance {
		t.Fatalf("admission error = %v, want maintenance", err)
	}
	var enabled int
	if err := db.QueryRow(`SELECT enabled FROM runtime_flags WHERE key='maintenance'`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("persisted maintenance = %d err=%v", enabled, err)
	}

	target := filepath.Join(backupRoot, "pipeline-backup.db")
	backup := pipelineRequest("/internal/maintenance/backup", `{"target":"`+target+`"}`)
	backupResponse := httptest.NewRecorder()
	handler.ServeHTTP(backupResponse, backup)
	if backupResponse.Code != http.StatusCreated {
		t.Fatalf("backup status = %d body=%s", backupResponse.Code, backupResponse.Body.String())
	}
}

func pipelineRequest(target, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	return request
}
