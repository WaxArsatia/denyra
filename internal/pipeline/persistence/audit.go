package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/ids"
)

func (r *Repositories) AppendAudit(ctx context.Context, candidateID, actor, action, reason string, details []byte, at time.Time) (string, error) {
	id, err := ids.NewToken(16)
	if err != nil {
		return "", err
	}
	if len(details) == 0 {
		details = []byte("{}")
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO audit_events(id,candidate_id,actor,action,reason,details_json,occurred_at)
		VALUES(?,NULLIF(?,''),?,?,?,?,?)`, id, candidateID, actor, action, reason, details, formatTime(at))
	if err != nil {
		return "", fmt.Errorf("append audit: %w", err)
	}
	return id, nil
}
