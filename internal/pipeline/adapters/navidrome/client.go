// Package navidrome provides the bounded HTTP operations Denyra needs for
// library reconciliation, scanning, and release visibility checks.
package navidrome

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
	"sync"
	"time"
)

const (
	managedName    = "Managed"
	managedPath    = "/music-managed"
	unmanagedName  = "Unmanaged"
	unmanagedPath  = "/music-unmanaged"
	subsonicClient = "denyra"
	subsonicAPI    = "1.16.1"
)

type Library struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	DefaultNewUsers bool   `json:"defaultNewUsers"`
}

type ReleaseIdentity struct {
	AlbumArtist string
	Album       string
	TrackCount  int
}

type Client struct {
	BaseURL       string
	Username      string
	Password      string
	HTTP          *http.Client
	ResponseLimit int64

	authMu        sync.Mutex
	jwt           string
	subsonicToken string
	subsonicSalt  string
}

type loginResponse struct {
	Token         string `json:"token"`
	SubsonicToken string `json:"subsonicToken"`
	SubsonicSalt  string `json:"subsonicSalt"`
}

func (c *Client) EnsureLibraries(ctx context.Context) (managedID, unmanagedID int, changed bool, err error) {
	libraries, err := c.libraries(ctx)
	if err != nil {
		return 0, 0, false, err
	}

	managed, found := findLibrary(libraries, managedPath)
	if !found {
		legacy, legacyFound := findLibrary(libraries, "/music")
		if !legacyFound {
			managed, err = c.createLibrary(ctx, Library{Name: managedName, Path: managedPath, DefaultNewUsers: true})
			if err != nil {
				return 0, 0, false, err
			}
		} else {
			managed = legacy
			managed.Name = managedName
			managed.Path = managedPath
			managed.DefaultNewUsers = true
			if err := c.updateLibrary(ctx, managed); err != nil {
				return 0, 0, false, err
			}
		}
		changed = true
	} else if managed.Name != managedName || !managed.DefaultNewUsers {
		managed.Name = managedName
		managed.DefaultNewUsers = true
		if err := c.updateLibrary(ctx, managed); err != nil {
			return 0, 0, false, err
		}
		changed = true
	}

	unmanaged, found := findLibrary(libraries, unmanagedPath)
	if !found {
		unmanaged, err = c.createLibrary(ctx, Library{Name: unmanagedName, Path: unmanagedPath, DefaultNewUsers: false})
		if err != nil {
			return 0, 0, false, err
		}
		changed = true
	} else if unmanaged.Name != unmanagedName || unmanaged.DefaultNewUsers {
		unmanaged.Name = unmanagedName
		unmanaged.DefaultNewUsers = false
		if err := c.updateLibrary(ctx, unmanaged); err != nil {
			return 0, 0, false, err
		}
		changed = true
	}

	if err := c.verifyMusicFolders(ctx, managed.ID, unmanaged.ID); err != nil {
		return 0, 0, false, err
	}
	return managed.ID, unmanaged.ID, changed, nil
}

func (c *Client) StartScan(ctx context.Context, libraryIDs ...int) error {
	query := url.Values{}
	for _, id := range libraryIDs {
		if id <= 0 {
			return fmt.Errorf("Navidrome library ID must be positive")
		}
		query.Add("target", strconv.Itoa(id)+":")
	}
	if len(libraryIDs) == 0 {
		return fmt.Errorf("at least one Navidrome library is required")
	}
	return c.subsonic(ctx, "/rest/startScan.view", query, nil)
}

func (c *Client) WaitScan(ctx context.Context, poll time.Duration) error {
	if poll <= 0 {
		return fmt.Errorf("scan poll interval must be positive")
	}
	for {
		var response struct {
			ScanStatus struct {
				Scanning bool `json:"scanning"`
			} `json:"scanStatus"`
		}
		if err := c.subsonic(ctx, "/rest/getScanStatus.view", nil, &response); err != nil {
			return err
		}
		if !response.ScanStatus.Scanning {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) ReleaseVisible(ctx context.Context, libraryID int, identity ReleaseIdentity) (bool, error) {
	if libraryID <= 0 || strings.TrimSpace(identity.AlbumArtist) == "" || strings.TrimSpace(identity.Album) == "" || identity.TrackCount <= 0 {
		return false, fmt.Errorf("complete release identity and positive library ID are required")
	}
	query := url.Values{
		"query":         {identity.Album},
		"musicFolderId": {strconv.Itoa(libraryID)},
		"albumCount":    {"20"},
		"songCount":     {"0"},
		"artistCount":   {"0"},
	}
	var response struct {
		SearchResult struct {
			Albums []struct {
				Name      string `json:"name"`
				Artist    string `json:"artist"`
				SongCount int    `json:"songCount"`
			} `json:"album"`
		} `json:"searchResult3"`
	}
	if err := c.subsonic(ctx, "/rest/search3.view", query, &response); err != nil {
		return false, err
	}
	for _, album := range response.SearchResult.Albums {
		if equalText(album.Name, identity.Album) && equalText(album.Artist, identity.AlbumArtist) && album.SongCount == identity.TrackCount {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) libraries(ctx context.Context) ([]Library, error) {
	var libraries []Library
	if err := c.native(ctx, http.MethodGet, "/api/library/", nil, &libraries); err != nil {
		return nil, err
	}
	return libraries, nil
}

func (c *Client) createLibrary(ctx context.Context, library Library) (Library, error) {
	var created Library
	if err := c.native(ctx, http.MethodPost, "/api/library/", library, &created); err != nil {
		return Library{}, err
	}
	if created.ID <= 0 {
		return Library{}, fmt.Errorf("Navidrome created library without an ID")
	}
	return created, nil
}

func (c *Client) updateLibrary(ctx context.Context, library Library) error {
	return c.native(ctx, http.MethodPut, "/api/library/"+strconv.Itoa(library.ID), library, nil)
}

func (c *Client) verifyMusicFolders(ctx context.Context, expected ...int) error {
	var response struct {
		MusicFolders struct {
			Folders []struct {
				ID int `json:"id"`
			} `json:"musicFolder"`
		} `json:"musicFolders"`
	}
	if err := c.subsonic(ctx, "/rest/getMusicFolders.view", nil, &response); err != nil {
		return err
	}
	seen := make(map[int]bool, len(response.MusicFolders.Folders))
	for _, folder := range response.MusicFolders.Folders {
		seen[folder.ID] = true
	}
	for _, id := range expected {
		if !seen[id] {
			return fmt.Errorf("Navidrome library %d is absent from OpenSubsonic music folders", id)
		}
	}
	return nil
}

func (c *Client) native(ctx context.Context, method, path string, input, output any) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode Navidrome request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.currentJWT())
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("Navidrome %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	c.refreshJWT(response.Header.Get("Authorization"))
	data, err := c.readBody(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.statusError(method, path, response.Status, data)
	}
	if output != nil && len(bytes.TrimSpace(data)) != 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode Navidrome %s response: %w", path, err)
		}
	}
	return nil
}

func (c *Client) subsonic(ctx context.Context, path string, query url.Values, output any) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	if query == nil {
		query = url.Values{}
	} else {
		query = cloneValues(query)
	}
	c.authMu.Lock()
	query.Set("u", c.Username)
	query.Set("t", c.subsonicToken)
	query.Set("s", c.subsonicSalt)
	c.authMu.Unlock()
	query.Set("v", subsonicAPI)
	query.Set("c", subsonicClient)
	query.Set("f", "json")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("Navidrome OpenSubsonic %s: %w", path, err)
	}
	defer response.Body.Close()
	data, err := c.readBody(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.statusError(http.MethodGet, path, response.Status, data)
	}
	var envelope struct {
		Response json.RawMessage `json:"subsonic-response"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Response) == 0 {
		return fmt.Errorf("decode Navidrome OpenSubsonic envelope for %s", path)
	}
	var status struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(envelope.Response, &status); err != nil {
		return fmt.Errorf("decode Navidrome OpenSubsonic status for %s: %w", path, err)
	}
	if status.Status != "ok" {
		if status.Error != nil {
			return fmt.Errorf("Navidrome OpenSubsonic %s failed with code %d: %s", path, status.Error.Code, status.Error.Message)
		}
		return fmt.Errorf("Navidrome OpenSubsonic %s returned status %q", path, status.Status)
	}
	if output != nil {
		if err := json.Unmarshal(envelope.Response, output); err != nil {
			return fmt.Errorf("decode Navidrome OpenSubsonic response for %s: %w", path, err)
		}
	}
	return nil
}

func (c *Client) ensureAuth(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.jwt != "" && c.subsonicToken != "" && c.subsonicSalt != "" {
		return nil
	}
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Username) == "" || c.Password == "" {
		return fmt.Errorf("Navidrome URL and credentials are required")
	}
	payload, err := json.Marshal(map[string]string{"username": c.Username, "password": c.Password})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/auth/login", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("log in to Navidrome: %w", err)
	}
	defer response.Body.Close()
	data, err := c.readBody(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.statusError(http.MethodPost, "/auth/login", response.Status, data)
	}
	var auth loginResponse
	if err := json.Unmarshal(data, &auth); err != nil {
		return fmt.Errorf("decode Navidrome login: %w", err)
	}
	if auth.Token == "" || auth.SubsonicToken == "" || auth.SubsonicSalt == "" {
		return fmt.Errorf("Navidrome login omitted required tokens")
	}
	c.jwt = auth.Token
	c.subsonicToken = auth.SubsonicToken
	c.subsonicSalt = auth.SubsonicSalt
	return nil
}

func (c *Client) currentJWT() string {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.jwt
}

func (c *Client) refreshJWT(header string) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || strings.TrimSpace(strings.TrimPrefix(header, prefix)) == "" {
		return
	}
	c.authMu.Lock()
	c.jwt = strings.TrimSpace(strings.TrimPrefix(header, prefix))
	c.authMu.Unlock()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) readBody(reader io.Reader) ([]byte, error) {
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = 1 << 20
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read Navidrome response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("Navidrome response exceeds %d bytes", limit)
	}
	return data, nil
}

func (c *Client) statusError(method, path, status string, body []byte) error {
	detail := strings.TrimSpace(strings.ReplaceAll(string(body), c.Password, "[redacted]"))
	if detail == "" {
		return fmt.Errorf("Navidrome %s %s returned %s", method, path, status)
	}
	return fmt.Errorf("Navidrome %s %s returned %s: %s", method, path, status, detail)
}

func findLibrary(libraries []Library, path string) (Library, bool) {
	for _, library := range libraries {
		if library.Path == path {
			return library, true
		}
	}
	return Library{}, false
}

func equalText(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func cloneValues(input url.Values) url.Values {
	result := make(url.Values, len(input))
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}
