package media

import (
	"context"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type LosslessHeuristic interface {
	Analyze(context.Context, string, domain.TechnicalInfo) ([]domain.Warning, error)
}

type NoHeuristic struct{}

func (NoHeuristic) Analyze(context.Context, string, domain.TechnicalInfo) ([]domain.Warning, error) {
	return nil, nil
}
