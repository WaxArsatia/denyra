package servicehost

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func ProbeReady(ctx context.Context, address string, timeout time.Duration) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("health address is required")
	}
	if timeout <= 0 {
		return fmt.Errorf("healthcheck timeout must be positive")
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+address+"/health/ready", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness status is %s", response.Status)
	}
	return nil
}
