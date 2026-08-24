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

	navidromeadapter "github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
)

type Navidrome struct {
	BaseURL       string
	AdminPassword string
	HTTP          *http.Client
}

func (n Navidrome) Apply(ctx context.Context) (Outcome, error) {
	adminChanged, err := n.ensureAdministrator(ctx)
	if err != nil {
		return Outcome{Service: "navidrome"}, err
	}
	client := &navidromeadapter.Client{
		BaseURL: n.BaseURL, Username: "admin", Password: n.AdminPassword,
		HTTP: n.client(), ResponseLimit: 1 << 20,
	}
	_, _, librariesChanged, err := client.EnsureLibraries(ctx)
	if err != nil {
		return Outcome{Service: "navidrome"}, fmt.Errorf("ensure music libraries: %w", err)
	}
	changed := adminChanged || librariesChanged
	message := "administrator and music libraries already configured"
	if changed {
		message = "configured administrator and music libraries"
	}
	return Outcome{Service: "navidrome", Changed: changed, Message: message}, nil
}

func (n Navidrome) ensureAdministrator(ctx context.Context) (bool, error) {
	payload, err := json.Marshal(map[string]string{"username": "admin", "password": n.AdminPassword})
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.BaseURL, "/")+"/auth/createAdmin", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := n.client().Do(request)
	if err != nil {
		return false, fmt.Errorf("create initial administrator: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if readErr != nil {
		return false, fmt.Errorf("read create-administrator response: %w", readErr)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return true, nil
	}
	if response.StatusCode == http.StatusForbidden {
		return false, nil
	}
	safeBody := strings.TrimSpace(strings.ReplaceAll(string(responseBody), n.AdminPassword, "[redacted]"))
	if safeBody == "" {
		return false, fmt.Errorf("create initial administrator returned %s", response.Status)
	}
	return false, fmt.Errorf("create initial administrator returned %s: %s", response.Status, safeBody)
}

func (n Navidrome) client() *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}
