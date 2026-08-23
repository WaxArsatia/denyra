package persistence

import (
	"context"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type CorrelationEvidence struct {
	ID, JobID, ReleaseGroupMBID, ReleaseMBID, CommandID, DownloadID string
	SourceKind, SourceRecordID, Watermark, EvidenceSHA256           string
	AlbumID                                                         int64
	ObservedAt                                                      time.Time
	Evidence                                                        []byte
}

func (r *Repositories) InsertCorrelationEvidence(ctx context.Context, item CorrelationEvidence) error {
	id := item.ID
	if id == "" {
		var err error
		id, err = ids.NewToken(16)
		if err != nil {
			return err
		}
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO correlation_evidence(id,job_id,album_id,release_group_mbid,release_mbid,command_id,download_id,source_kind,source_record_id,watermark,observed_at,evidence_json,evidence_sha256) VALUES(?,?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?,?,?,?,?)`, id, item.JobID, item.AlbumID, item.ReleaseGroupMBID, item.ReleaseMBID, item.CommandID, item.DownloadID, item.SourceKind, item.SourceRecordID, item.Watermark, formatTime(item.ObservedAt), item.Evidence, item.EvidenceSHA256)
	return err
}
