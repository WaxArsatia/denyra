package internalapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
)

type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return "retryable quality callback error: " + e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

type QualityClient struct {
	BaseURL       string
	Bearer        string
	HTTP          *http.Client
	ResponseLimit int64
}

func (c QualityClient) ReportApproved(ctx context.Context, payload []byte, requestID, idempotencyKey string) (contracts.CallbackResult, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/internal/candidates/approved"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return contracts.CallbackResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Bearer)
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return contracts.CallbackResult{}, &RetryableError{Err: err}
	}
	defer response.Body.Close()
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return contracts.CallbackResult{}, &RetryableError{Err: err}
	}
	hash := sha256.Sum256(body)
	evidence := contracts.CallbackResult{StatusCode: response.StatusCode, ResponseSHA256: hex.EncodeToString(hash[:])}
	if int64(len(body)) > limit {
		return evidence, fmt.Errorf("quality callback response exceeds limit")
	}
	if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests {
		return evidence, &RetryableError{Err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return evidence, fmt.Errorf("quality callback HTTP status %d", response.StatusCode)
	}
	return evidence, nil
}
