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

type Navidrome struct {
	BaseURL       string
	AdminPassword string
	HTTP          *http.Client
}

func (n Navidrome) Apply(ctx context.Context) (Outcome, error) {
	payload, err := json.Marshal(map[string]string{"username": "admin", "password": n.AdminPassword})
	if err != nil {
		return Outcome{Service: "navidrome"}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.BaseURL, "/")+"/auth/createAdmin", bytes.NewReader(payload))
	if err != nil {
		return Outcome{Service: "navidrome"}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := n.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return Outcome{Service: "navidrome"}, fmt.Errorf("create initial administrator: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if readErr != nil {
		return Outcome{Service: "navidrome"}, fmt.Errorf("read create-administrator response: %w", readErr)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return Outcome{Service: "navidrome", Changed: true, Message: "created initial administrator"}, nil
	}
	if response.StatusCode == http.StatusForbidden {
		return Outcome{Service: "navidrome", Changed: false, Message: "existing administrator adopted"}, nil
	}
	safeBody := strings.TrimSpace(strings.ReplaceAll(string(responseBody), n.AdminPassword, "[redacted]"))
	if safeBody == "" {
		return Outcome{Service: "navidrome"}, fmt.Errorf("create initial administrator returned %s", response.Status)
	}
	return Outcome{Service: "navidrome"}, fmt.Errorf("create initial administrator returned %s: %s", response.Status, safeBody)
}
