package transport

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/platform/httpx"
)

type Routes struct {
	Quality   QualityCallbackAPI
	Store     *persistence.Repositories
	BodyLimit int64
	Bearer    []byte
	Notify    func()
}

func (routes Routes) Handler() (http.Handler, error) {
	if routes.Store == nil || routes.BodyLimit <= 0 || len(routes.Bearer) == 0 {
		return nil, errors.New("gateway internal routes are not configured")
	}
	quality, err := routes.Quality.Handler()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/internal/candidates/", quality)
	private := http.NewServeMux()
	private.HandleFunc("GET /internal/acquisitions", routes.acquisitions)
	private.HandleFunc("GET /internal/acquisitions/{jobID}", routes.evidence)
	private.HandleFunc("POST /internal/events/lidarr", routes.lidarrEvent)
	private.HandleFunc("POST /internal/maintenance", routes.maintenance)
	acquisitions := httpx.RequestID(httpx.BearerAuth(routes.Bearer, private))
	mux.Handle("/internal/acquisitions", acquisitions)
	mux.Handle("/internal/acquisitions/", acquisitions)
	mux.Handle("/internal/events/lidarr", httpx.RequestID(httpx.BearerAuth(routes.Bearer, httpx.LimitBody(routes.BodyLimit, httpx.RequireJSON(private)))))
	maintenance := httpx.RequestID(httpx.BearerAuth(routes.Bearer, httpx.LimitBody(routes.BodyLimit, httpx.RequireJSON(private))))
	mux.Handle("/internal/maintenance", maintenance)
	mux.Handle("/internal/maintenance/", maintenance)
	return mux, nil
}

func (routes Routes) acquisitions(writer http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_PAGE", "invalid acquisition page limit")
			return
		}
		limit = parsed
	}
	page, err := routes.Store.JobSummaries(request.Context(), limit, request.URL.Query().Get("cursor"), request.URL.Query().Get("state"))
	if err != nil {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_PAGE", "invalid acquisition page")
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(writer, http.StatusOK, page)
}

func (routes Routes) evidence(writer http.ResponseWriter, request *http.Request) {
	evidence, err := routes.Store.JobEvidence(request.Context(), request.PathValue("jobID"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(writer, request, http.StatusNotFound, "NOT_FOUND", "acquisition job not found")
			return
		}
		httpx.WriteError(writer, request, http.StatusInternalServerError, "READ_FAILED", "acquisition evidence unavailable")
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(writer, http.StatusOK, evidence)
}

func (routes Routes) lidarrEvent(writer http.ResponseWriter, request *http.Request) {
	var event struct {
		EventID string `json:"event_id"`
	}
	if err := contracts.DecodeStrictJSON(request.Body, routes.BodyLimit, &event); err != nil || event.EventID == "" {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_EVENT", "invalid Lidarr event")
		return
	}
	if routes.Notify != nil {
		routes.Notify()
	}
	httpx.WriteJSON(writer, http.StatusAccepted, map[string]string{"status": "RECONCILIATION_SCHEDULED", "request_id": httpx.RequestIDFromContext(request.Context())})
}
