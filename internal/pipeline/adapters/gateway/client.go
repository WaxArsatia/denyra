package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Client struct {
	BaseURL   string
	Bearer    string
	HTTP      *http.Client
	BodyLimit int64
	RequestID func() string
}

func (c Client) AcquisitionEvidence(ctx context.Context, jobID string) (application.AcquisitionEvidence, error) {
	if c.HTTP == nil || c.BaseURL == "" || c.Bearer == "" || c.BodyLimit <= 0 {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway evidence client is not configured")
	}
	if strings.ContainsAny(jobID, "/\\") || jobID == "" {
		return application.AcquisitionEvidence{}, fmt.Errorf("invalid acquisition job ID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/internal/acquisitions/"+url.PathEscape(jobID), nil)
	if err != nil {
		return application.AcquisitionEvidence{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Bearer)
	if c.RequestID != nil {
		request.Header.Set("X-Request-ID", c.RequestID())
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return application.AcquisitionEvidence{}, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, c.BodyLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return application.AcquisitionEvidence{}, err
	}
	if int64(len(body)) > c.BodyLimit {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway evidence response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway evidence status %d", response.StatusCode)
	}
	var evidence application.AcquisitionEvidence
	if err := json.Unmarshal(body, &evidence); err != nil {
		return evidence, err
	}
	if evidence.JobID != jobID {
		return evidence, fmt.Errorf("gateway evidence identity mismatch")
	}
	return evidence, nil
}
