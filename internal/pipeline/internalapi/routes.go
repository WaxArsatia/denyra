package internalapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/httpx"
)

type API struct {
	Service               application.HandoffService
	BodyLimit             int64
	Bearer                []byte
	NotifyManualDiscovery func()
}

func (a API) Handler() (http.Handler, error) {
	if a.BodyLimit <= 0 || len(a.Bearer) == 0 {
		return nil, errors.New("internal API body limit and bearer are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/candidates", a.accept)
	mux.HandleFunc("POST /internal/candidates/{candidateID}/winner", a.winner)
	mux.HandleFunc("POST /internal/candidates/{candidateID}/supersede", a.supersede)
	mux.HandleFunc("POST /internal/candidates/{candidateID}/cancel", a.cancel)
	mux.HandleFunc("POST /internal/events/manual-discovery", a.manualDiscovery)
	return httpx.RequestID(httpx.BearerAuth(a.Bearer, httpx.LimitBody(a.BodyLimit, httpx.RequireJSON(mux)))), nil
}

func (a API) manualDiscovery(w http.ResponseWriter, r *http.Request) {
	var event struct {
		EventID string `json:"event_id"`
	}
	if err := contracts.DecodeStrictJSON(r.Body, a.BodyLimit, &event); err != nil || event.EventID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid discovery event")
		return
	}
	if a.NotifyManualDiscovery != nil {
		a.NotifyManualDiscovery()
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "DISCOVERY_SCHEDULED", "request_id": httpx.RequestIDFromContext(r.Context())})
}

func (a API) accept(w http.ResponseWriter, r *http.Request) {
	var request contracts.CandidateAccepted
	if err := contracts.DecodeStrictJSON(r.Body, a.BodyLimit, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid candidate handoff")
		return
	}
	status, body, err := a.Service.Accept(r.Context(), r.Header.Get("Idempotency-Key"), request)
	a.respond(w, r, status, body, err)
}
func (a API) winner(w http.ResponseWriter, r *http.Request) {
	var request contracts.CandidateWinner
	if err := contracts.DecodeStrictJSON(r.Body, a.BodyLimit, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid winner directive")
		return
	}
	payload, _ := json.Marshal(request)
	status, body, err := a.Service.Directive(r.Context(), r.Header.Get("Idempotency-Key"), "WINNER_LOCKED", r.PathValue("candidateID"), request.StateRevision, domain.StateImportReady, "gateway", request.Reason, "", payload)
	a.respond(w, r, status, body, err)
}
func (a API) supersede(w http.ResponseWriter, r *http.Request) {
	var request contracts.CandidateSuperseded
	if err := contracts.DecodeStrictJSON(r.Body, a.BodyLimit, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid supersession directive")
		return
	}
	payload, _ := json.Marshal(request)
	status, body, err := a.Service.Directive(r.Context(), r.Header.Get("Idempotency-Key"), "SUPERSEDED", r.PathValue("candidateID"), request.StateRevision, domain.StateSuperseded, "gateway", request.Reason, "", payload)
	a.respond(w, r, status, body, err)
}
func (a API) cancel(w http.ResponseWriter, r *http.Request) {
	var request contracts.CandidateCancelled
	if err := contracts.DecodeStrictJSON(r.Body, a.BodyLimit, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid cancellation directive")
		return
	}
	payload, _ := json.Marshal(request)
	status, body, err := a.Service.Directive(r.Context(), r.Header.Get("Idempotency-Key"), "CANCELLED", r.PathValue("candidateID"), request.StateRevision, domain.StateCancelled, "gateway", request.Reason, "", payload)
	a.respond(w, r, status, body, err)
}

func (a API) respond(w http.ResponseWriter, r *http.Request, status int, body []byte, err error) {
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	var stale *domain.StaleRevisionError
	switch {
	case errors.Is(err, contracts.ErrIdempotencyConflict):
		httpx.WriteError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key conflicts with prior request")
	case errors.As(err, &stale):
		httpx.WriteError(w, r, http.StatusConflict, "STALE_REVISION", err.Error())
	default:
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_STATE", err.Error())
	}
}
