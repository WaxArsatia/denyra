package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/ids"
)

func (r *Repositories) InsertMetadataSnapshot(ctx context.Context, candidateID, candidateFileID, kind string, canonicalJSON []byte, sha256 string, at time.Time) (string, error) {
	id, err := ids.NewToken(16)
	if err != nil {
		return "", err
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO metadata_snapshots(id,candidate_id,candidate_file_id,kind,canonical_json,sha256,created_at)
		VALUES(?,?,NULLIF(?,''),?,?,?,?)`, id, candidateID, candidateFileID, kind, canonicalJSON, sha256, formatTime(at))
	if err != nil {
		return "", fmt.Errorf("insert metadata snapshot: %w", err)
	}
	return id, nil
}
