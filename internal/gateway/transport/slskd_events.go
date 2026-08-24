package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/platform/httpx"
)

type SlskdEventStore interface {
	RecordSlskdCompletionEvent(context.Context, persistence.SlskdCompletionEvent) error
}

type SlskdEventRoutes struct {
	Store     SlskdEventStore
	BodyLimit int64
	Notify    func()
	Now       func() time.Time
}

type slskdFileCompleteEvent struct {
	Type           string          `json:"type"`
	Version        int             `json:"version"`
	LocalFilename  string          `json:"localFilename"`
	RemoteFilename string          `json:"remoteFilename"`
	Transfer       json.RawMessage `json:"transfer"`
	ID             string          `json:"id"`
	Timestamp      time.Time       `json:"timestamp"`
}

type slskdTransferIdentity struct {
	ID      string `json:"id"`
	BatchID string `json:"batchId"`
	State   string `json:"state"`
}

func (routes SlskdEventRoutes) Handler() (http.Handler, error) {
	if routes.Store == nil || routes.BodyLimit <= 0 || routes.Notify == nil {
		return nil, fmt.Errorf("slskd event routes are not configured")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events/slskd", routes.receive)
	return httpx.RequestID(httpx.LimitBody(routes.BodyLimit, httpx.RequireJSON(mux))), nil
}

func (routes SlskdEventRoutes) receive(writer http.ResponseWriter, request *http.Request) {
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_EVENT", "invalid slskd event")
		return
	}
	var event slskdFileCompleteEvent
	if err := contracts.DecodeStrictJSON(bytes.NewReader(payload), routes.BodyLimit, &event); err != nil || event.Type != "DownloadFileComplete" || event.Version != 0 || strings.TrimSpace(event.ID) == "" || event.Timestamp.IsZero() || event.Timestamp.Location() != time.UTC || strings.TrimSpace(event.LocalFilename) == "" || strings.TrimSpace(event.RemoteFilename) == "" {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_EVENT", "invalid slskd event")
		return
	}
	var transfer slskdTransferIdentity
	if err := json.Unmarshal(event.Transfer, &transfer); err != nil || strings.TrimSpace(transfer.ID) == "" || !strings.Contains(strings.ToLower(transfer.State), "completed") {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_EVENT", "invalid slskd transfer evidence")
		return
	}
	sum := sha256.Sum256(payload)
	record := persistence.SlskdCompletionEvent{ID: event.ID, Version: event.Version, TransferID: transfer.ID, BatchID: transfer.BatchID, LocalFilename: event.LocalFilename, RemoteFilename: event.RemoteFilename, TransferState: transfer.State, Timestamp: event.Timestamp, ReceivedAt: routes.now(), Payload: payload, PayloadSHA256: hex.EncodeToString(sum[:])}
	if err := routes.Store.RecordSlskdCompletionEvent(request.Context(), record); err != nil {
		if errors.Is(err, contracts.ErrIdempotencyConflict) {
			httpx.WriteError(writer, request, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "slskd event identity conflict")
			return
		}
		httpx.WriteError(writer, request, http.StatusInternalServerError, "EVENT_PERSIST_FAILED", "slskd event could not be persisted")
		return
	}
	routes.Notify()
	httpx.WriteJSON(writer, http.StatusAccepted, map[string]string{"status": "RECONCILIATION_SCHEDULED", "request_id": httpx.RequestIDFromContext(request.Context())})
}

func (routes SlskdEventRoutes) now() time.Time {
	if routes.Now != nil {
		return routes.Now().UTC()
	}
	return time.Now().UTC()
}
