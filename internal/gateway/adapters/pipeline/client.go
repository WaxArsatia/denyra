package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/waxarsatia/denyra/internal/contracts"
)

type RetryableError struct{ Err error }

func (err *RetryableError) Error() string { return "retryable pipeline API error: " + err.Err.Error() }
func (err *RetryableError) Unwrap() error { return err.Err }

type Response struct {
	Status int
	Body   []byte
}

type Client struct {
	BaseURL, Bearer string
	HTTP            *http.Client
	ResponseLimit   int64
}

func (client Client) Register(ctx context.Context, request contracts.CandidateRegistered, requestID, idempotencyKey string) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	return client.post(ctx, "/internal/candidates/register", payload, requestID, idempotencyKey)
}

func (client Client) Accept(ctx context.Context, request contracts.CandidateAccepted, requestID, idempotencyKey string) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	return client.post(ctx, "/internal/candidates", payload, requestID, idempotencyKey)
}

func (client Client) Winner(ctx context.Context, request contracts.CandidateWinner, requestID, idempotencyKey string) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	return client.post(ctx, "/internal/candidates/"+request.CandidateID+"/winner", payload, requestID, idempotencyKey)
}

func (client Client) Supersede(ctx context.Context, request contracts.CandidateSuperseded, requestID, idempotencyKey string) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	return client.post(ctx, "/internal/candidates/"+request.CandidateID+"/supersede", payload, requestID, idempotencyKey)
}

func (client Client) Cancel(ctx context.Context, request contracts.CandidateCancelled, requestID, idempotencyKey string) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	return client.post(ctx, "/internal/candidates/"+request.CandidateID+"/cancel", payload, requestID, idempotencyKey)
}

func (client Client) post(ctx context.Context, path string, payload []byte, requestID, idempotencyKey string) (Response, error) {
	if client.HTTP == nil || client.BaseURL == "" || client.Bearer == "" || client.ResponseLimit <= 0 || requestID == "" || idempotencyKey == "" {
		return Response{}, fmt.Errorf("pipeline client is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.BaseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.Bearer)
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return Response{}, &RetryableError{Err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, client.ResponseLimit+1))
	if err != nil {
		return Response{}, &RetryableError{Err: err}
	}
	result := Response{Status: response.StatusCode, Body: body}
	if int64(len(body)) > client.ResponseLimit {
		return result, fmt.Errorf("pipeline response exceeds limit")
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return result, &RetryableError{Err: fmt.Errorf("pipeline HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("pipeline HTTP status %d", response.StatusCode)
	}
	return result, nil
}
