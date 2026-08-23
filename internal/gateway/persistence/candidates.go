package persistence

import (
	"context"
	"database/sql"
	"time"
)

type Candidate struct {
	ID, JobID, Source, SourceLocator, DownloadID, ProvenanceSHA256 string
	CompletedAt                                                    *time.Time
	Provenance                                                     []byte
	CreatedAt                                                      time.Time
}

func (r *Repositories) InsertCandidate(ctx context.Context, c Candidate) error {
	var completed any
	if c.CompletedAt != nil {
		completed = formatTime(*c.CompletedAt)
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO candidates(candidate_id,job_id,source,source_locator,download_id,completed_at,provenance_json,provenance_sha256,created_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?)`, c.ID, c.JobID, c.Source, c.SourceLocator, c.DownloadID, completed, c.Provenance, c.ProvenanceSHA256, formatTime(c.CreatedAt))
	return err
}
func (r *Repositories) Candidate(ctx context.Context, id string) (Candidate, error) {
	var c Candidate
	var completed sql.NullString
	var created string
	err := r.DB.QueryRowContext(ctx, `SELECT candidate_id,job_id,source,source_locator,COALESCE(download_id,''),completed_at,provenance_json,provenance_sha256,created_at FROM candidates WHERE candidate_id=?`, id).Scan(&c.ID, &c.JobID, &c.Source, &c.SourceLocator, &c.DownloadID, &completed, &c.Provenance, &c.ProvenanceSHA256, &created)
	if err != nil {
		return c, err
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if completed.Valid {
		value, _ := time.Parse(time.RFC3339Nano, completed.String)
		c.CompletedAt = &value
	}
	return c, nil
}
