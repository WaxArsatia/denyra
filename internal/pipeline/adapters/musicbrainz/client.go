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
