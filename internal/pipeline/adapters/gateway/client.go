package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Client struct {
	BaseURL, Bearer string
	HTTP            *http.Client
	ResponseLimit   int64
}

func (client Client) AcquisitionEvidence(ctx context.Context, jobID string) (application.AcquisitionEvidence, error) {
	if client.BaseURL == "" || client.Bearer == "" || client.HTTP == nil || client.ResponseLimit <= 0 || jobID == "" {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway evidence client is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(client.BaseURL, "/")+"/internal/acquisitions/"+url.PathEscape(jobID), nil)
	if err != nil {
		return application.AcquisitionEvidence{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.Bearer)
	request.Header.Set("Accept", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return application.AcquisitionEvidence{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, client.ResponseLimit+1))
	if err != nil {
		return application.AcquisitionEvidence{}, err
	}
	if int64(len(data)) > client.ResponseLimit {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway evidence response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway evidence HTTP status %d", response.StatusCode)
	}
	var evidence contracts.AcquisitionJobEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return application.AcquisitionEvidence{}, err
	}
	return application.AcquisitionEvidence{JobID: evidence.Job.JobID, State: evidence.Job.State, Revision: evidence.Job.StateRevision, AlbumID: evidence.Job.LidarrAlbumID, ReleaseGroupID: evidence.Job.ReleaseGroupMBID, Evidence: json.RawMessage(data), ObservedAt: evidence.Job.UpdatedAt}, nil
}
