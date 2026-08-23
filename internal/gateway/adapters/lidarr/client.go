package lidarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return "retryable Lidarr error: " + e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

type Client struct {
	BaseURL, APIKey string
	HTTP            *http.Client
	ResponseLimit   int64
}

func (c Client) Get(ctx context.Context, path string, query url.Values, destination any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, destination)
}
func (c Client) Post(ctx context.Context, path string, body []byte, destination any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, destination)
}
func (c Client) Delete(ctx context.Context, path string, query url.Values) error {
	return c.do(ctx, http.MethodDelete, path, query, nil, nil)
}
func (c Client) do(ctx context.Context, method, path string, query url.Values, body []byte, destination any) error {
	if c.HTTP == nil || c.ResponseLimit <= 0 || c.BaseURL == "" || c.APIKey == "" {
		return fmt.Errorf("Lidarr client is not configured")
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", c.APIKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return &RetryableError{Err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, c.ResponseLimit+1))
	if err != nil {
		return &RetryableError{Err: err}
	}
	if int64(len(data)) > c.ResponseLimit {
		return fmt.Errorf("Lidarr response exceeds limit")
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return &RetryableError{Err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Lidarr HTTP status %d", response.StatusCode)
	}
	if destination != nil && len(data) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(data))
		if err := decoder.Decode(destination); err != nil {
			return fmt.Errorf("decode Lidarr response: %w", err)
		}
	}
	return nil
}
func pageQuery(page, size int) url.Values {
	return url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)}, "sortDirection": {"descending"}}
}
