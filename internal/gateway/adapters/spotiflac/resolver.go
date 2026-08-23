package spotiflac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var ErrNoLocator = errors.New("MusicBrainz release has no supported acquisition locator")

type MusicBrainzResolver struct {
	BaseURL, UserAgent string
	HTTP               *http.Client
	ResponseLimit      int64
}

func (resolver MusicBrainzResolver) Resolve(ctx context.Context, _, selectedRelease string) (string, error) {
	if resolver.HTTP == nil || resolver.ResponseLimit <= 0 || resolver.BaseURL == "" || resolver.UserAgent == "" || selectedRelease == "" {
		return "", fmt.Errorf("MusicBrainz locator resolver is not configured")
	}
	endpoint := strings.TrimRight(resolver.BaseURL, "/") + "/ws/2/release/" + url.PathEscape(selectedRelease) + "?fmt=json&inc=url-rels"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", resolver.UserAgent)
	response, err := resolver.HTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, resolver.ResponseLimit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > resolver.ResponseLimit {
		return "", fmt.Errorf("MusicBrainz locator response exceeds limit")
	}
	if response.StatusCode == http.StatusNotFound {
		return "", ErrNoLocator
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return "", fmt.Errorf("MusicBrainz locator retryable HTTP status %d", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("MusicBrainz locator HTTP status %d", response.StatusCode)
	}
	var payload struct {
		ID        string `json:"id"`
		Relations []struct {
			URL struct {
				Resource string `json:"resource"`
			} `json:"url"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode MusicBrainz locator response: %w", err)
	}
	if payload.ID != selectedRelease {
		return "", fmt.Errorf("MusicBrainz locator release identity mismatch")
	}
	priorities := []string{"open.spotify.com/", "tidal.com/", "music.apple.com/", "deezer.com/"}
	for _, host := range priorities {
		for _, relation := range payload.Relations {
			resource := relation.URL.Resource
			parsed, err := url.Parse(resource)
			if err == nil && parsed.Scheme == "https" && strings.Contains(strings.ToLower(parsed.Host+parsed.Path), host) {
				return resource, nil
			}
		}
	}
	return "", ErrNoLocator
}
