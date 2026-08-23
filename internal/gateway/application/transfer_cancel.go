package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type TransferCancellationService struct {
	Lidarr interface {
		CancelDownload(context.Context, string) error
	}
	SpotiFLAC interface {
		CancelSuperseded(string) error
	}
}

func (service TransferCancellationService) CancelIncomplete(ctx context.Context, candidate persistence.PendingCandidate) ([]byte, error) {
	var err error
	switch candidate.Source {
	case "slskd":
		if service.Lidarr == nil {
			return nil, fmt.Errorf("Lidarr cancellation is not configured")
		}
		err = service.Lidarr.CancelDownload(ctx, candidate.DownloadID)
	case "spotiflac":
		if service.SpotiFLAC == nil {
			return nil, fmt.Errorf("SpotiFLAC cancellation is not configured")
		}
		err = service.SpotiFLAC.CancelSuperseded(candidate.JobID)
	default:
		return nil, fmt.Errorf("unsupported incomplete candidate source %q", candidate.Source)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"candidate_id": candidate.ID, "status": "SUPERSEDED_CANCELLED"})
}
