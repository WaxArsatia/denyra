package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

type Candidate struct {
	ID, JobID, Source, SourceLocator, DownloadID, ProvenanceSHA256 string
	CompletedAt                                                    *time.Time
	OutputSHA256                                                   string
	OutputManifest                                                 []byte
	Provenance                                                     []byte
	CreatedAt                                                      time.Time
}

func (r *Repositories) InsertCandidate(ctx context.Context, c Candidate) error {
	if c.CompletedAt == nil || len(c.OutputSHA256) != 64 || len(c.OutputManifest) == 0 {
		return fmt.Errorf("completed candidate output evidence is incomplete")
	}
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO candidates(candidate_id,job_id,source,source_locator,download_id,completed_at,provenance_json,provenance_sha256,created_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, c.ID, c.JobID, c.Source, c.SourceLocator, c.DownloadID, formatTime(*c.CompletedAt), c.Provenance, c.ProvenanceSHA256, formatTime(c.CreatedAt))
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			var hash string
			if err := tx.QueryRowContext(ctx, `SELECT provenance_sha256 FROM candidates WHERE candidate_id=?`, c.ID).Scan(&hash); err != nil {
				return err
			}
			if hash != c.ProvenanceSHA256 {
				return contracts.ErrIdempotencyConflict
			}
		}
		manifestHash := sha256.Sum256(c.OutputManifest)
		result, err = tx.ExecContext(ctx, `INSERT INTO candidate_output_evidence(candidate_id,output_sha256,manifest_json,manifest_sha256,completed_at,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, c.ID, c.OutputSHA256, c.OutputManifest, hex.EncodeToString(manifestHash[:]), formatTime(*c.CompletedAt), formatTime(c.CreatedAt))
		if err != nil {
			return err
		}
		count, _ = result.RowsAffected()
		if count == 0 {
			var checksum string
			if err := tx.QueryRowContext(ctx, `SELECT output_sha256 FROM candidate_output_evidence WHERE candidate_id=?`, c.ID).Scan(&checksum); err != nil {
				return err
			}
			if checksum != c.OutputSHA256 {
				return contracts.ErrIdempotencyConflict
			}
		}
		return nil
	})
}
func (r *Repositories) Candidate(ctx context.Context, id string) (Candidate, error) {
	var c Candidate
	var completed sql.NullString
	var created string
	err := r.DB.QueryRowContext(ctx, `SELECT c.candidate_id,c.job_id,c.source,c.source_locator,COALESCE(c.download_id,''),c.completed_at,c.provenance_json,c.provenance_sha256,c.created_at,o.output_sha256,o.manifest_json FROM candidates c JOIN candidate_output_evidence o ON o.candidate_id=c.candidate_id WHERE c.candidate_id=?`, id).Scan(&c.ID, &c.JobID, &c.Source, &c.SourceLocator, &c.DownloadID, &completed, &c.Provenance, &c.ProvenanceSHA256, &created, &c.OutputSHA256, &c.OutputManifest)
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

type PendingCandidate struct {
	ID, JobID, Source, SourceLocator, DownloadID, ProvenanceSHA256 string
	Provenance                                                     []byte
	CreatedAt                                                      time.Time
}

func insertPendingCandidate(ctx context.Context, executor evidenceExecutor, candidate PendingCandidate) error {
	result, err := executor.ExecContext(ctx, `INSERT INTO pending_acquisition_candidates(candidate_id,job_id,source,source_locator,download_id,provenance_json,provenance_sha256,created_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, candidate.ID, candidate.JobID, candidate.Source, candidate.SourceLocator, candidate.DownloadID, candidate.Provenance, candidate.ProvenanceSHA256, formatTime(candidate.CreatedAt))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var hash string
	if err := executor.QueryRowContext(ctx, `SELECT provenance_sha256 FROM pending_acquisition_candidates WHERE candidate_id=?`, candidate.ID).Scan(&hash); err != nil {
		return err
	}
	if hash != candidate.ProvenanceSHA256 {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

func (r *Repositories) InsertPendingCandidate(ctx context.Context, candidate PendingCandidate) error {
	return insertPendingCandidate(ctx, r.DB, candidate)
}

func (r *Repositories) PendingCandidate(ctx context.Context, id string) (PendingCandidate, error) {
	var candidate PendingCandidate
	var created string
	err := r.DB.QueryRowContext(ctx, `SELECT candidate_id,job_id,source,source_locator,COALESCE(download_id,''),provenance_json,provenance_sha256,created_at FROM pending_acquisition_candidates WHERE candidate_id=?`, id).Scan(&candidate.ID, &candidate.JobID, &candidate.Source, &candidate.SourceLocator, &candidate.DownloadID, &candidate.Provenance, &candidate.ProvenanceSHA256, &created)
	if err == nil {
		candidate.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	}
	return candidate, err
}

func (r *Repositories) IncompletePendingCandidates(ctx context.Context, jobID string) ([]PendingCandidate, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT p.candidate_id,p.job_id,p.source,p.source_locator,COALESCE(p.download_id,''),p.provenance_json,p.provenance_sha256,p.created_at FROM pending_acquisition_candidates p LEFT JOIN candidates c ON c.candidate_id=p.candidate_id WHERE p.job_id=? AND c.candidate_id IS NULL ORDER BY p.created_at,p.candidate_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []PendingCandidate
	for rows.Next() {
		var candidate PendingCandidate
		var created string
		if err := rows.Scan(&candidate.ID, &candidate.JobID, &candidate.Source, &candidate.SourceLocator, &candidate.DownloadID, &candidate.Provenance, &candidate.ProvenanceSHA256, &created); err != nil {
			return nil, err
		}
		candidate.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (r *Repositories) CandidateForJobSource(ctx context.Context, jobID, source string, completed bool) (string, error) {
	table := "pending_acquisition_candidates"
	column := "candidate_id"
	if completed {
		table = "candidates"
		column = "candidate_id"
	}
	var id string
	err := r.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE job_id=? AND source=? ORDER BY created_at LIMIT 1`, column, table), jobID, source).Scan(&id)
	return id, err
}

func NewCandidateID() (string, error) { return ids.NewToken(16) }
