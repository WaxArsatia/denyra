package spotify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type OEmbed struct {
	BaseURL       string
	HTTP          *http.Client
	ResponseLimit int64
}

func (o OEmbed) FetchURL(ctx context.Context, trackURL string) ([]byte, domain.ProviderEvidence, error) {
	parsed, err := url.Parse(strings.TrimSpace(trackURL))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "open.spotify.com") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, domain.ProviderEvidence{}, fmt.Errorf("explicit Spotify track URL is invalid")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "track" || parts[1] == "" {
		return nil, domain.ProviderEvidence{}, fmt.Errorf("explicit Spotify track URL is invalid")
	}
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = "https://open.spotify.com"
	}
	endpoint := base + "/oembed?" + url.Values{"url": {parsed.String()}}.Encode()
	body, _, err := o.get(ctx, endpoint)
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	var payload struct {
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, domain.ProviderEvidence{}, fmt.Errorf("decode Spotify oEmbed: %w", err)
	}
	thumbnail, err := url.Parse(payload.ThumbnailURL)
	if err != nil || thumbnail.Scheme != "https" || thumbnail.Host == "" {
		return nil, domain.ProviderEvidence{}, fmt.Errorf("Spotify oEmbed thumbnail must be HTTPS")
	}
	return o.get(ctx, thumbnail.String())
}

func (o OEmbed) get(ctx context.Context, endpoint string) ([]byte, domain.ProviderEvidence, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	request.Header.Set("Accept", "application/json, image/jpeg, image/png")
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, domain.ProviderEvidence{Provider: "spotify-oembed", Endpoint: endpoint}, err
	}
	defer response.Body.Close()
	limit := o.ResponseLimit
	if limit <= 0 {
		limit = 8 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	hash := sha256.Sum256(body)
	evidence := domain.ProviderEvidence{Provider: "spotify-oembed", Endpoint: endpoint, StatusCode: response.StatusCode, ResponseSHA256: hex.EncodeToString(hash[:]), ResponseBody: body}
	if int64(len(body)) > limit {
		return nil, evidence, fmt.Errorf("Spotify response exceeds %d bytes", limit)
	}
	if response.StatusCode != http.StatusOK {
		return nil, evidence, fmt.Errorf("Spotify HTTP status %d", response.StatusCode)
	}
	return body, evidence, nil
}
