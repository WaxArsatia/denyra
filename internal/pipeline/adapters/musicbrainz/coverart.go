package musicbrainz

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

type CoverArt struct {
	BaseURL       string
	HTTP          *http.Client
	ResponseLimit int64
}

func (c CoverArt) FetchRelease(ctx context.Context, releaseMBID string) ([]byte, domain.ProviderEvidence, error) {
	id, err := domain.CanonicalMBID(releaseMBID)
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://coverartarchive.org"
	}
	body, _, err := c.get(ctx, base+"/release/"+id)
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	var payload struct {
		Images []struct {
			Front bool   `json:"front"`
			Image string `json:"image"`
		} `json:"images"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, domain.ProviderEvidence{}, fmt.Errorf("decode Cover Art Archive response: %w", err)
	}
	for _, image := range payload.Images {
		if !image.Front {
			continue
		}
		parsed, err := url.Parse(image.Image)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, domain.ProviderEvidence{}, fmt.Errorf("Cover Art Archive image URL must be HTTPS")
		}
		return c.get(ctx, parsed.String())
	}
	return nil, domain.ProviderEvidence{}, fmt.Errorf("Cover Art Archive has no front image")
}

func (c CoverArt) get(ctx context.Context, endpoint string) ([]byte, domain.ProviderEvidence, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	request.Header.Set("Accept", "application/json, image/jpeg, image/png")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, domain.ProviderEvidence{Provider: "cover-art-archive", Endpoint: endpoint}, err
	}
	defer response.Body.Close()
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = 8 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, domain.ProviderEvidence{}, err
	}
	hash := sha256.Sum256(body)
	evidence := domain.ProviderEvidence{Provider: "cover-art-archive", Endpoint: endpoint, StatusCode: response.StatusCode, ResponseSHA256: hex.EncodeToString(hash[:]), ResponseBody: body}
	if int64(len(body)) > limit {
		return nil, evidence, fmt.Errorf("Cover Art Archive response exceeds %d bytes", limit)
	}
	if response.StatusCode != http.StatusOK {
		return nil, evidence, fmt.Errorf("Cover Art Archive HTTP status %d", response.StatusCode)
	}
	return body, evidence, nil
}
