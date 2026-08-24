package musicbrainz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return "retryable MusicBrainz error: " + e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

type Evidence struct {
	Endpoint       string `json:"endpoint"`
	StatusCode     int    `json:"status_code"`
	ResponseSHA256 string `json:"response_sha256"`
	ResponseBody   []byte `json:"response_body"`
}

type SearchInput struct {
	TaggedReleaseMBIDs []string
	Barcodes           []string
	ISRCs              []string
	AlbumArtist        string
	Album              string
	Date               string
	TrackCount         int
}

type SearchResult struct {
	Releases []domain.CanonicalRelease
	Evidence []Evidence
}

type Client struct {
	BaseURL       string
	UserAgent     string
	HTTP          *http.Client
	ResponseLimit int64
	RateInterval  time.Duration
	mu            sync.Mutex
	lastRequest   time.Time
}

func (c *Client) LookupRelease(ctx context.Context, releaseMBID string) (domain.CanonicalRelease, Evidence, error) {
	releaseID, err := domain.CanonicalMBID(releaseMBID)
	if err != nil {
		return domain.CanonicalRelease{}, Evidence{}, err
	}
	if strings.TrimSpace(c.UserAgent) == "" || (!strings.Contains(c.UserAgent, "@") && !strings.Contains(c.UserAgent, "http")) {
		return domain.CanonicalRelease{}, Evidence{}, fmt.Errorf("MusicBrainz User-Agent must identify a version and contact")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://musicbrainz.org"
	}
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/ws/2/release/" + releaseID)
	if err != nil {
		return domain.CanonicalRelease{}, Evidence{}, err
	}
	query := endpoint.Query()
	query.Set("fmt", "json")
	query.Set("inc", "recordings+artist-credits+release-groups+isrcs")
	endpoint.RawQuery = query.Encode()
	if err := c.waitRate(ctx); err != nil {
		return domain.CanonicalRelease{}, Evidence{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.CanonicalRelease{}, Evidence{}, err
	}
	request.Header.Set("User-Agent", c.UserAgent)
	request.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return domain.CanonicalRelease{}, Evidence{Endpoint: endpoint.String()}, &RetryableError{Err: err}
	}
	defer response.Body.Close()
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = 4 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return domain.CanonicalRelease{}, Evidence{}, &RetryableError{Err: err}
	}
	bodyHash := sha256.Sum256(body)
	evidence := Evidence{Endpoint: endpoint.String(), StatusCode: response.StatusCode, ResponseBody: body, ResponseSHA256: hex.EncodeToString(bodyHash[:])}
	if int64(len(body)) > limit {
		return domain.CanonicalRelease{}, evidence, fmt.Errorf("MusicBrainz response exceeds %d bytes", limit)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return domain.CanonicalRelease{}, evidence, &RetryableError{Err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode != http.StatusOK {
		return domain.CanonicalRelease{}, evidence, fmt.Errorf("MusicBrainz HTTP status %d", response.StatusCode)
	}
	release, err := decodeRelease(body)
	if err != nil {
		return domain.CanonicalRelease{}, evidence, fmt.Errorf("decode MusicBrainz release: %w", err)
	}
	if release.ReleaseMBID != releaseID {
		return domain.CanonicalRelease{}, evidence, fmt.Errorf("MusicBrainz returned release %q for %q", release.ReleaseMBID, releaseID)
	}
	return release, evidence, nil
}

func (c *Client) SearchReleases(ctx context.Context, input SearchInput) (SearchResult, error) {
	result := SearchResult{}
	ids := make([]string, 0)
	seenIDs := make(map[string]bool)
	releases := make(map[string]domain.CanonicalRelease)
	addID := func(value string) error {
		id, err := domain.CanonicalMBID(strings.TrimSpace(value))
		if err != nil {
			return err
		}
		if !seenIDs[id] {
			seenIDs[id] = true
			ids = append(ids, id)
		}
		return nil
	}

	for _, id := range sortedUnique(input.TaggedReleaseMBIDs) {
		if err := addID(id); err != nil {
			return result, fmt.Errorf("tagged release MBID: %w", err)
		}
		release, evidence, err := c.LookupRelease(ctx, id)
		result.Evidence = append(result.Evidence, evidence)
		if err != nil {
			return result, err
		}
		releases[release.ReleaseMBID] = release
	}

	for _, barcode := range sortedUnique(input.Barcodes) {
		body, evidence, err := c.requestJSON(ctx, "/ws/2/release", url.Values{"fmt": {"json"}, "limit": {"10"}, "query": {"barcode:" + barcode}})
		result.Evidence = append(result.Evidence, evidence)
		if err != nil {
			return result, err
		}
		var payload struct {
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return result, fmt.Errorf("decode MusicBrainz release search: %w", err)
		}
		for index, release := range payload.Releases {
			if index == 10 {
				break
			}
			if err := addID(release.ID); err != nil {
				return result, fmt.Errorf("release search MBID: %w", err)
			}
		}
	}

	for _, isrc := range sortedUnique(input.ISRCs) {
		body, evidence, err := c.requestJSON(ctx, "/ws/2/recording", url.Values{"fmt": {"json"}, "limit": {"10"}, "query": {"isrc:" + isrc}})
		result.Evidence = append(result.Evidence, evidence)
		if err != nil {
			return result, err
		}
		var payload struct {
			Recordings []struct {
				ID string `json:"id"`
			} `json:"recordings"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return result, fmt.Errorf("decode MusicBrainz recording search: %w", err)
		}
		for index, recording := range payload.Recordings {
			if index == 10 {
				break
			}
			recordingID, err := domain.CanonicalMBID(recording.ID)
			if err != nil {
				return result, fmt.Errorf("recording search MBID: %w", err)
			}
			body, evidence, err := c.requestJSON(ctx, "/ws/2/recording/"+recordingID, url.Values{"fmt": {"json"}, "inc": {"releases"}})
			result.Evidence = append(result.Evidence, evidence)
			if err != nil {
				return result, err
			}
			var lookup struct {
				Releases []struct {
					ID string `json:"id"`
				} `json:"releases"`
			}
			if err := json.Unmarshal(body, &lookup); err != nil {
				return result, fmt.Errorf("decode MusicBrainz recording lookup: %w", err)
			}
			for _, release := range lookup.Releases {
				if err := addID(release.ID); err != nil {
					return result, fmt.Errorf("recording release MBID: %w", err)
				}
			}
		}
	}

	if strings.TrimSpace(input.AlbumArtist) != "" && strings.TrimSpace(input.Album) != "" && input.TrackCount > 0 {
		terms := []string{"artist:" + strings.TrimSpace(input.AlbumArtist), "release:" + strings.TrimSpace(input.Album)}
		if value := strings.TrimSpace(input.Date); value != "" {
			terms = append(terms, "date:"+value)
		}
		terms = append(terms, fmt.Sprintf("tracks:%d", input.TrackCount))
		body, evidence, err := c.requestJSON(ctx, "/ws/2/release", url.Values{"fmt": {"json"}, "limit": {"10"}, "query": {strings.Join(terms, " AND ")}})
		result.Evidence = append(result.Evidence, evidence)
		if err != nil {
			return result, err
		}
		var payload struct {
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return result, fmt.Errorf("decode MusicBrainz metadata search: %w", err)
		}
		for index, release := range payload.Releases {
			if index == 10 {
				break
			}
			if err := addID(release.ID); err != nil {
				return result, fmt.Errorf("metadata search MBID: %w", err)
			}
		}
	}

	for _, id := range ids {
		release, exists := releases[id]
		if !exists {
			var evidence Evidence
			var err error
			release, evidence, err = c.LookupRelease(ctx, id)
			result.Evidence = append(result.Evidence, evidence)
			if err != nil {
				return result, err
			}
		}
		result.Releases = append(result.Releases, release)
	}
	return result, nil
}

func (c *Client) requestJSON(ctx context.Context, path string, query url.Values) ([]byte, Evidence, error) {
	if strings.TrimSpace(c.UserAgent) == "" || (!strings.Contains(c.UserAgent, "@") && !strings.Contains(c.UserAgent, "http")) {
		return nil, Evidence{}, fmt.Errorf("MusicBrainz User-Agent must identify a version and contact")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://musicbrainz.org"
	}
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return nil, Evidence{}, err
	}
	endpoint.RawQuery = query.Encode()
	if err := c.waitRate(ctx); err != nil {
		return nil, Evidence{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, Evidence{}, err
	}
	request.Header.Set("User-Agent", c.UserAgent)
	request.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, Evidence{Endpoint: endpoint.String()}, &RetryableError{Err: err}
	}
	defer response.Body.Close()
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = 4 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, Evidence{}, &RetryableError{Err: err}
	}
	bodyHash := sha256.Sum256(body)
	evidence := Evidence{Endpoint: endpoint.String(), StatusCode: response.StatusCode, ResponseBody: body, ResponseSHA256: hex.EncodeToString(bodyHash[:])}
	if int64(len(body)) > limit {
		return nil, evidence, fmt.Errorf("MusicBrainz response exceeds %d bytes", limit)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return nil, evidence, &RetryableError{Err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode != http.StatusOK {
		return nil, evidence, fmt.Errorf("MusicBrainz HTTP status %d", response.StatusCode)
	}
	return body, evidence, nil
}

func sortedUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func (c *Client) waitRate(ctx context.Context) error {
	interval := c.RateInterval
	if interval <= 0 {
		interval = time.Second
	}
	c.mu.Lock()
	wait := time.Until(c.lastRequest.Add(interval))
	if wait < 0 {
		wait = 0
	}
	c.lastRequest = time.Now().Add(wait)
	c.mu.Unlock()
	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func decodeRelease(body []byte) (domain.CanonicalRelease, error) {
	var payload struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		Status       string `json:"status"`
		ReleaseGroup struct {
			ID string `json:"id"`
		} `json:"release-group"`
		ArtistCredit []creditPayload `json:"artist-credit"`
		Media        []struct {
			Position   int `json:"position"`
			TrackCount int `json:"track-count"`
			Tracks     []struct {
				ID           string          `json:"id"`
				Title        string          `json:"title"`
				Number       string          `json:"number"`
				Position     int             `json:"position"`
				Length       *int64          `json:"length"`
				ArtistCredit []creditPayload `json:"artist-credit"`
				Recording    struct {
					ID           string          `json:"id"`
					Title        string          `json:"title"`
					Length       *int64          `json:"length"`
					ISRCs        []string        `json:"isrcs"`
					ArtistCredit []creditPayload `json:"artist-credit"`
				} `json:"recording"`
			} `json:"tracks"`
		} `json:"media"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return domain.CanonicalRelease{}, err
	}
	release := domain.CanonicalRelease{ReleaseMBID: payload.ID, ReleaseGroupMBID: payload.ReleaseGroup.ID, Title: payload.Title, Date: payload.Date, Status: payload.Status, ArtistCredits: mapCredits(payload.ArtistCredit)}
	for _, medium := range payload.Media {
		if medium.TrackCount != len(medium.Tracks) {
			return domain.CanonicalRelease{}, fmt.Errorf("medium %d track-count=%d but tracks=%d", medium.Position, medium.TrackCount, len(medium.Tracks))
		}
		for _, track := range medium.Tracks {
			duration := track.Length
			if duration == nil {
				duration = track.Recording.Length
			}
			credits := track.ArtistCredit
			if len(credits) == 0 {
				credits = track.Recording.ArtistCredit
			}
			release.Tracks = append(release.Tracks, domain.CanonicalTrack{
				ReleaseTrackMBID: track.ID, RecordingMBID: track.Recording.ID, Title: track.Title,
				Disc: medium.Position, Track: track.Position, Number: track.Number, DurationMS: duration,
				ArtistCredits: mapCredits(credits), ISRCs: append([]string(nil), track.Recording.ISRCs...),
			})
		}
	}
	return release, nil
}

type creditPayload struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

func mapCredits(values []creditPayload) []domain.ArtistCredit {
	result := make([]domain.ArtistCredit, 0, len(values))
	for _, value := range values {
		name := value.Name
		if name == "" {
			name = value.Artist.Name
		}
		result = append(result, domain.ArtistCredit{Name: name, ArtistMBID: value.Artist.ID, JoinPhrase: value.JoinPhrase})
	}
	return result
}
