package servicehost

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func ProbeReady(ctx context.Context, address string) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("health address is required")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
