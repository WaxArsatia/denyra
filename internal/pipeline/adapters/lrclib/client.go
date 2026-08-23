package lrclib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var ErrNotFound = errors.New("lyrics not found")

type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return "retryable LRCLIB error: " + e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

type Query = domain.LyricsQuery
type Result = domain.LyricsResult
type Evidence = domain.ProviderEvidence

type Client struct {
	BaseURL       string
	UserAgent     string
	HTTP          *http.Client
	ResponseLimit int64
}

func (c Client) Get(ctx context.Context, query Query) (Result, Evidence, error) {
	if query.TrackName == "" || query.ArtistName == "" || query.AlbumName == "" || query.DurationMS <= 0 {
		return Result{}, Evidence{}, fmt.Errorf("exact LRCLIB track signature is required")
	}
	if strings.TrimSpace(c.UserAgent) == "" || (!strings.Contains(c.UserAgent, "@") && !strings.Contains(c.UserAgent, "http")) {
		return Result{}, Evidence{}, fmt.Errorf("LRCLIB User-Agent must identify a version and contact")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://lrclib.net"
	}
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/api/get")
	if err != nil {
		return Result{}, Evidence{}, err
	}
	values := endpoint.Query()
	values.Set("track_name", query.TrackName)
	values.Set("artist_name", query.ArtistName)
	values.Set("album_name", query.AlbumName)
	values.Set("duration", strconv.FormatInt((query.DurationMS+500)/1000, 10))
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, Evidence{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.UserAgent)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, Evidence{Endpoint: endpoint.String()}, &RetryableError{Err: err}
	}
	defer response.Body.Close()
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return Result{}, Evidence{}, &RetryableError{Err: err}
	}
	hash := sha256.Sum256(body)
	evidence := Evidence{Provider: "LRCLIB", Endpoint: endpoint.String(), StatusCode: response.StatusCode, ResponseSHA256: hex.EncodeToString(hash[:]), ResponseBody: body}
	if int64(len(body)) > limit {
		return Result{}, evidence, fmt.Errorf("LRCLIB response exceeds %d bytes", limit)
	}
	if response.StatusCode == http.StatusNotFound {
		return Result{}, evidence, ErrNotFound
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return Result{}, evidence, &RetryableError{Err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, evidence, fmt.Errorf("LRCLIB HTTP status %d", response.StatusCode)
	}
	var payload struct {
		ID           int64  `json:"id"`
		Instrumental bool   `json:"instrumental"`
		PlainLyrics  string `json:"plainLyrics"`
		SyncedLyrics string `json:"syncedLyrics"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Result{}, evidence, fmt.Errorf("decode LRCLIB response: %w", err)
	}
	return Result{ID: payload.ID, Instrumental: payload.Instrumental, Plain: payload.PlainLyrics, Synced: payload.SyncedLyrics}, evidence, nil
}
