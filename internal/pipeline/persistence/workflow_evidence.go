package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

func (r *Repositories) RecordTechnical(ctx context.Context, candidateID, root string, result domain.TechnicalReleaseResult, at time.Time) error {
	for _, file := range result.Files {
		path := filepath.Join(root, filepath.FromSlash(file.RelativePath))
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		var device, inode uint64
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			device, inode = stat.Dev, stat.Ino
		}
		fileID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		if _, err := r.DB.ExecContext(ctx, `INSERT INTO candidate_files(id,candidate_id,relative_path,size_bytes,mtime_ns,device,inode,sha256_before,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(candidate_id,relative_path) DO NOTHING`, fileID, candidateID, file.RelativePath, info.Size(), info.ModTime().UnixNano(), device, inode, file.SHA256Before, formatTime(at)); err != nil {
			return err
		}
		original, _ := json.Marshal(file.OriginalComments)
		sum := sha256.Sum256(original)
		if _, err := r.InsertMetadataSnapshot(ctx, candidateID, r.candidateFileID(ctx, candidateID, file.RelativePath), "ORIGINAL", original, hex.EncodeToString(sum[:]), at); err != nil && !isUniqueConstraint(err) {
			return err
		}
	}
	return r.recordValidation(ctx, candidateID, "RELEASE", candidateID, technicalClassification(result), "TECHNICAL_GATE", result, at)
}

func (r *Repositories) RecordMatch(ctx context.Context, candidateID string, match domain.ReleaseMatch, at time.Time) error {
	for _, track := range match.Tracks {
		fileID := r.candidateFileID(ctx, candidateID, track.Candidate.RelativePath)
		evidence, _ := json.Marshal(track)
		id, _ := ids.NewToken(16)
		var reference any
		if track.Canonical.DurationMS != nil {
			reference = *track.Canonical.DurationMS
		}
		if _, err := r.DB.ExecContext(ctx, `INSERT INTO track_matches(id,candidate_id,candidate_file_id,release_mbid,recording_mbid,release_track_mbid,medium_position,track_position,reference_duration_ms,observed_duration_ms,status,evidence_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(candidate_id,candidate_file_id) DO NOTHING`, id, candidateID, fileID, match.Release.ReleaseMBID, track.Canonical.RecordingMBID, track.Canonical.ReleaseTrackMBID, track.Canonical.Disc, track.Canonical.Track, reference, track.Candidate.DurationMS, track.Duration.Status, evidence, formatTime(at)); err != nil {
			return err
		}
	}
	releaseJSON, _ := json.Marshal(match.Release)
	sum := sha256.Sum256(releaseJSON)
	if _, err := r.InsertMetadataSnapshot(ctx, candidateID, "", "CANONICAL", releaseJSON, hex.EncodeToString(sum[:]), at); err != nil && !isUniqueConstraint(err) {
		return err
	}
	return r.recordValidation(ctx, candidateID, "RELEASE", candidateID, string(match.Status), "MUSICBRAINZ_RELEASE_MATCH", match, at)
}

func (r *Repositories) RecordEnrichment(ctx context.Context, candidateID string, result application.EnrichmentResult, at time.Time) error {
	for _, item := range result.Items {
		payload, _ := json.Marshal(item)
		sum := sha256.Sum256(payload)
		id, _ := ids.NewToken(16)
		provider := item.Evidence.Provider
		if provider == "" {
			provider = "pipeline"
		}
		if _, err := r.DB.ExecContext(ctx, `INSERT INTO enrichments(id,candidate_id,kind,provider,classification,evidence_json,evidence_sha256,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, candidateID, item.Kind, provider, item.Classification, payload, hex.EncodeToString(sum[:]), formatTime(at)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repositories) RecordMutation(ctx context.Context, candidateID string, result application.MutationResult, at time.Time) error {
	for _, file := range result.Files {
		fileID := r.candidateFileID(ctx, candidateID, file.RelativePath)
		beforeJSON, _ := json.Marshal(file.BeforeTags)
		afterJSON, _ := json.Marshal(file.AfterTags)
		beforeSum, afterSum := sha256.Sum256(beforeJSON), sha256.Sum256(afterJSON)
		beforeID, err := r.InsertMetadataSnapshot(ctx, candidateID, fileID, "ORIGINAL", beforeJSON, hex.EncodeToString(beforeSum[:]), at)
		if err != nil && !isUniqueConstraint(err) {
			return err
		}
		if beforeID == "" {
			_ = r.DB.QueryRowContext(ctx, `SELECT id FROM metadata_snapshots WHERE candidate_id=? AND candidate_file_id=? AND kind='ORIGINAL' ORDER BY created_at LIMIT 1`, candidateID, fileID).Scan(&beforeID)
		}
		afterID, err := r.InsertMetadataSnapshot(ctx, candidateID, fileID, "FINAL", afterJSON, hex.EncodeToString(afterSum[:]), at)
		if err != nil && !isUniqueConstraint(err) {
			return err
		}
		invocation, _ := json.Marshal(file.Commands)
		diff, _ := json.Marshal(file)
		id, _ := ids.NewToken(16)
		if _, err := r.DB.ExecContext(ctx, `INSERT INTO mutations(id,candidate_id,candidate_file_id,before_snapshot_id,after_snapshot_id,invocation_json,diff_json,status,created_at,completed_at) VALUES(?,?,?,?,?,?,?,'COMPLETED',?,?)`, id, candidateID, fileID, beforeID, afterID, invocation, diff, formatTime(at), formatTime(at)); err != nil {
			return err
		}
		if _, err := r.DB.ExecContext(ctx, `UPDATE candidate_files SET sha256_after=? WHERE id=?`, file.AfterSHA256, fileID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repositories) candidateFileID(ctx context.Context, candidateID, relative string) string {
	var id string
	_ = r.DB.QueryRowContext(ctx, `SELECT id FROM candidate_files WHERE candidate_id=? AND relative_path=?`, candidateID, relative).Scan(&id)
	return id
}

func (r *Repositories) recordValidation(ctx context.Context, candidateID, scope, subject, classification, code string, evidence any, at time.Time) error {
	payload, _ := json.Marshal(evidence)
	sum := sha256.Sum256(payload)
	var exists int
	if err := r.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM validation_results WHERE candidate_id=? AND code=? AND evidence_sha256=?`, candidateID, code, hex.EncodeToString(sum[:])).Scan(&exists); err != nil || exists > 0 {
		return err
	}
	id, _ := ids.NewToken(16)
	_, err := r.DB.ExecContext(ctx, `INSERT INTO validation_results(id,candidate_id,scope,subject,classification,code,evidence_json,evidence_sha256,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, candidateID, scope, subject, classification, code, payload, hex.EncodeToString(sum[:]), formatTime(at))
	return err
}

func technicalClassification(result domain.TechnicalReleaseResult) string {
	if result.Rejected {
		return "REJECT"
	}
	if result.Retryable {
		return "RETRYABLE_ERROR"
	}
	return "PASS"
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
