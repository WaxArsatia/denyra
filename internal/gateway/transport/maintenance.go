package transport

import (
	"net/http"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/httpx"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

type maintenanceRequest struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

type backupRequest struct {
	Target string `json:"target"`
}

func (routes Routes) maintenance(writer http.ResponseWriter, request *http.Request) {
	var input maintenanceRequest
	if err := contracts.DecodeStrictJSON(request.Body, routes.BodyLimit, &input); err != nil || (input.Enabled && input.Reason == "") {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_MAINTENANCE", "maintenance reason is required")
		return
	}
	if err := routes.Store.SetMaintenance(request.Context(), input.Enabled, input.Reason, time.Now().UTC()); err != nil {
		httpx.WriteError(writer, request, http.StatusInternalServerError, "MAINTENANCE_FAILED", "maintenance state could not be changed")
		return
	}
	var leases, effects int
	_ = routes.Store.DB.QueryRowContext(request.Context(), `SELECT COUNT(*) FROM leases WHERE expires_at>?`, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&leases)
	_ = routes.Store.DB.QueryRowContext(request.Context(), `SELECT COUNT(*) FROM external_effects WHERE acknowledged_at IS NULL`).Scan(&effects)
	httpx.WriteJSON(writer, http.StatusOK, map[string]any{"enabled": input.Enabled, "active_leases": leases, "unresolved_effects": effects, "safe": input.Enabled && leases == 0 && effects == 0})
}

func (routes Routes) onlineBackup(writer http.ResponseWriter, request *http.Request) {
	var input backupRequest
	if err := contracts.DecodeStrictJSON(request.Body, routes.BodyLimit, &input); err != nil || denysqlite.ValidateBackupTarget(routes.BackupRoot, input.Target) != nil {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_BACKUP", "absolute backup target is required")
		return
	}
	enabled, _, err := routes.Store.Maintenance(request.Context())
	if err != nil || !enabled {
		httpx.WriteError(writer, request, http.StatusConflict, "MAINTENANCE_REQUIRED", "enter maintenance before online backup")
		return
	}
	if err := denysqlite.Backup(request.Context(), routes.Store.DB, input.Target); err != nil {
		httpx.WriteError(writer, request, http.StatusInternalServerError, "BACKUP_FAILED", "online backup failed")
		return
	}
	httpx.WriteJSON(writer, http.StatusCreated, map[string]string{"target": input.Target, "status": "VERIFIED_SQLITE_BACKUP"})
}
