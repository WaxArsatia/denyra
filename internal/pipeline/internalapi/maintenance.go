package internalapi

import (
	"net/http"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/httpx"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (a API) maintenance(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if err := contracts.DecodeStrictJSON(request.Body, a.BodyLimit, &input); err != nil || (input.Enabled && input.Reason == "") {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_MAINTENANCE", "maintenance reason is required")
		return
	}
	value := 0
	if input.Enabled {
		value = 1
	}
	if _, err := a.DB.ExecContext(request.Context(), `UPDATE runtime_flags SET enabled=?,reason=?,updated_at=? WHERE key='maintenance'`, value, input.Reason, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		httpx.WriteError(writer, request, http.StatusInternalServerError, "MAINTENANCE_FAILED", "maintenance state could not be changed")
		return
	}
	a.Admission.SetMaintenance(input.Enabled)
	var leases, effects int
	_ = a.DB.QueryRowContext(request.Context(), `SELECT COUNT(*) FROM leases WHERE expires_at>?`, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&leases)
	_ = a.DB.QueryRowContext(request.Context(), `SELECT COUNT(*) FROM idempotency_records WHERE response_status IS NULL`).Scan(&effects)
	httpx.WriteJSON(writer, http.StatusOK, map[string]any{"enabled": input.Enabled, "active_leases": leases, "unresolved_effects": effects, "safe": input.Enabled && leases == 0 && effects == 0})
}

func (a API) onlineBackup(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Target string `json:"target"`
	}
	if err := contracts.DecodeStrictJSON(request.Body, a.BodyLimit, &input); err != nil || denysqlite.ValidateBackupTarget(a.BackupRoot, input.Target) != nil {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_BACKUP", "backup target must be a new file inside the backup workspace")
		return
	}
	var enabled int
	if err := a.DB.QueryRowContext(request.Context(), `SELECT enabled FROM runtime_flags WHERE key='maintenance'`).Scan(&enabled); err != nil || enabled != 1 {
		httpx.WriteError(writer, request, http.StatusConflict, "MAINTENANCE_REQUIRED", "enter maintenance before online backup")
		return
	}
	if err := denysqlite.Backup(request.Context(), a.DB, input.Target); err != nil {
		httpx.WriteError(writer, request, http.StatusInternalServerError, "BACKUP_FAILED", "online backup failed")
		return
	}
	httpx.WriteJSON(writer, http.StatusCreated, map[string]string{"target": input.Target, "status": "VERIFIED_SQLITE_BACKUP"})
}
