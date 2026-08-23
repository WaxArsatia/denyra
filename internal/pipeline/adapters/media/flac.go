package media

import (
	"context"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type FLAC struct {
	Binary  string
	Version string
	Timeout time.Duration
	Runner  Runner
}

func (f FLAC) Test(ctx context.Context, path string) (domain.CommandEvidence, error) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return f.Runner.Run(child, f.Binary, f.Version, "--totally-silent", "--test", path)
}
