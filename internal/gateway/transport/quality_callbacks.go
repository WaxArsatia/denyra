package transport

import (
	"errors"
	"net/http"

	"github.com/waxarsatia/denyra/internal/contracts"
	pipelineadapter "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/platform/httpx"
)

type QualityCallbackAPI struct {
	Service   application.ArbitrationService
	BodyLimit int64
	Bearer    []byte
}

func (api QualityCallbackAPI) Handler() (http.Handler, error) {
	if api.BodyLimit <= 0 || len(api.Bearer) == 0 {
		return nil, errors.New("quality callback body limit and bearer are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/candidates/{candidateID}/approved", api.approved)
	return httpx.RequestID(httpx.BearerAuth(api.Bearer, httpx.LimitBody(api.BodyLimit, httpx.RequireJSON(mux)))), nil
}

func (api QualityCallbackAPI) approved(writer http.ResponseWriter, request *http.Request) {
	var callback contracts.CandidateApproved
	if err := contracts.DecodeStrictJSON(request.Body, api.BodyLimit, &callback); err != nil || callback.CandidateID != request.PathValue("candidateID") || callback.RequestID != httpx.RequestIDFromContext(request.Context()) {
		httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid quality callback")
		return
	}
	status, body, err := api.Service.Approve(request.Context(), request.Header.Get("Idempotency-Key"), callback)
	if err == nil {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write(body)
		return
	}
	if errors.Is(err, contracts.ErrIdempotencyConflict) {
		httpx.WriteError(writer, request, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key conflicts with prior request")
		return
	}
	var retryable *pipelineadapter.RetryableError
	if errors.As(err, &retryable) {
		httpx.WriteError(writer, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "pipeline directive delivery is temporarily unavailable")
		return
	}
	httpx.WriteError(writer, request, http.StatusBadRequest, "INVALID_APPROVAL", err.Error())
}
