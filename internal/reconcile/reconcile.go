package reconcile

import (
	"context"
	"fmt"
	"net/http"
)

type Options struct {
	LidarrURL              string
	LidarrAPIKey           string
	SlskdURL               string
	SlskdAPIKey            string
	SFTPGoURL              string
	SFTPGoAdminPassword    string
	SFTPGoUploadPassword   string
	NavidromeURL           string
	NavidromeAdminPassword string
	HTTP                   *http.Client
}

type Outcome struct {
	Service string
	Changed bool
	Message string
}

type Service struct {
	Name  string
	Apply func(context.Context) (Outcome, error)
}

func Run(ctx context.Context, services []Service) ([]Outcome, error) {
	outcomes := make([]Outcome, 0, len(services))
	for _, service := range services {
		outcome, err := service.Apply(ctx)
		if err != nil {
			return outcomes, fmt.Errorf("reconcile %s: %w", service.Name, err)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// Services returns the configured service reconcilers. Concrete reconcilers
// are added incrementally while keeping command-line secret handling stable.
func Services(Options) []Service {
	return nil
}
