package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const sftpgoUploadHome = "/data/incoming/manual"

type SFTPGo struct {
	BaseURL        string
	AdminPassword  string
	UploadPassword string
	HTTP           *http.Client
}

func (s SFTPGo) Apply(ctx context.Context) (Outcome, error) {
	token, err := s.token(ctx)
	if err != nil {
		return Outcome{Service: "sftpgo"}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/api/v2/users/upload", nil)
	if err != nil {
		return Outcome{Service: "sftpgo"}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := s.client().Do(request)
	if err != nil {
		return Outcome{Service: "sftpgo"}, fmt.Errorf("read upload user: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		if err := s.createUploadUser(ctx, token); err != nil {
			return Outcome{Service: "sftpgo"}, err
		}
		return Outcome{Service: "sftpgo", Changed: true, Message: "created restricted upload user"}, nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<20))
		return Outcome{Service: "sftpgo"}, fmt.Errorf("read upload user returned %s", response.Status)
	}
	var existing struct {
		HomeDir string `json:"home_dir"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&existing); err != nil {
		return Outcome{Service: "sftpgo"}, fmt.Errorf("decode upload user: %w", err)
	}
	if existing.HomeDir != sftpgoUploadHome {
		return Outcome{Service: "sftpgo"}, fmt.Errorf("existing upload user home is %q; expected %q", existing.HomeDir, sftpgoUploadHome)
	}
	return Outcome{Service: "sftpgo", Changed: false, Message: "existing restricted upload user adopted"}, nil
}

func (s SFTPGo) token(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/api/v2/token", nil)
	if err != nil {
		return "", err
	}
	request.SetBasicAuth("admin", s.AdminPassword)
	response, err := s.client().Do(request)
	if err != nil {
		return "", fmt.Errorf("request administrator token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<20))
		return "", fmt.Errorf("request administrator token returned %s", response.Status)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode administrator token: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("administrator token response is empty")
	}
	return result.AccessToken, nil
}

func (s SFTPGo) createUploadUser(ctx context.Context, token string) error {
	payload := map[string]any{
		"status": 1, "username": "upload", "password": s.UploadPassword,
		"home_dir":    sftpgoUploadHome,
		"permissions": map[string][]string{"/": {"*"}},
		"filesystem":  map[string]int{"provider": 0},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.BaseURL, "/")+"/api/v2/users", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client().Do(request)
	if err != nil {
		return fmt.Errorf("create upload user: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("create upload user returned %s", response.Status)
	}
	return nil
}

func (s SFTPGo) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}
