package persistence

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func (r *Repositories) UnmanagedSummaries(ctx context.Context, filter application.UnmanagedFilter, limit int, cursor string) ([]application.UnmanagedSummary, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query != "" && utf8.RuneCountInString(query) < 2 {
		return nil, "", fmt.Errorf("unmanaged search requires at least two characters")
	}
	status := strings.ToUpper(strings.TrimSpace(filter.Status))
	cursorUpdated, cursorID, err := decodeUnmanagedCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	prefix := escapeLikePrefix(query) + "%"
	where := []string{"NOT EXISTS(SELECT 1 FROM migration_items mi WHERE mi.unmanaged_candidate_id=unmanaged_releases.candidate_id AND mi.state='MIGRATED')"}
	arguments := make([]any, 0, 8)
	if status != "" {
		where = append(where, "status=?")
		arguments = append(arguments, status)
	}
	if query != "" {
		where = append(where, `(candidate_id LIKE ? ESCAPE '\' OR album_artist_normalized LIKE ? ESCAPE '\' OR album_title_normalized LIKE ? ESCAPE '\' OR path_basename_normalized LIKE ? ESCAPE '\')`)
		arguments = append(arguments, prefix, prefix, prefix, prefix)
	}
	if cursorUpdated != "" {
		where = append(where, "(updated_at<? OR (updated_at=? AND candidate_id<?))")
		arguments = append(arguments, cursorUpdated, cursorUpdated, cursorID)
	}
	arguments = append(arguments, limit+1)
	statement := `SELECT candidate_id,album_artist,album_title,release_year,status,state_revision,updated_at FROM unmanaged_releases WHERE ` + strings.Join(where, " AND ") + ` ORDER BY updated_at DESC,candidate_id DESC LIMIT ?`
	rows, err := r.DB.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result := make([]application.UnmanagedSummary, 0, limit+1)
	for rows.Next() {
		var item application.UnmanagedSummary
		var updated string
		if err := rows.Scan(&item.CandidateID, &item.AlbumArtist, &item.Album, &item.Year, &item.State, &item.Revision, &updated); err != nil {
			return nil, "", err
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, "", err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(result) > limit {
		result = result[:limit]
		last := result[len(result)-1]
		next, err = encodeUnmanagedCursor(last.UpdatedAt, last.CandidateID)
		if err != nil {
			return nil, "", err
		}
	}
	return result, next, nil
}

type unmanagedCursor struct {
	Updated string `json:"u"`
	ID      string `json:"i"`
}

func encodeUnmanagedCursor(updated time.Time, id string) (string, error) {
	payload, err := json.Marshal(unmanagedCursor{Updated: formatTime(updated), ID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeUnmanagedCursor(cursor string) (string, string, error) {
	if cursor == "" {
		return "", "", nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("invalid unmanaged cursor")
	}
	var decoded unmanagedCursor
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Updated == "" || decoded.ID == "" {
		return "", "", fmt.Errorf("invalid unmanaged cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, decoded.Updated); err != nil {
		return "", "", fmt.Errorf("invalid unmanaged cursor")
	}
	return decoded.Updated, decoded.ID, nil
}

func escapeLikePrefix(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (r *Repositories) MigrationBatchDetail(ctx context.Context, batchID string) (application.MigrationBatchDetail, error) {
	batch, err := r.MigrationBatch(ctx, batchID)
	if err != nil {
		return application.MigrationBatchDetail{}, err
	}
	detail := application.MigrationBatchDetail{ID: batch.ID, Actor: batch.Actor, State: batch.Status, Revision: batch.StateRevision, CreatedAt: batch.CreatedAt, UpdatedAt: batch.UpdatedAt}
	rows, err := r.DB.QueryContext(ctx, `SELECT i.id,i.unmanaged_candidate_id,COALESCE(json_extract(u.approved_plan_json,'$.metadata.album_artist'),''),COALESCE(json_extract(u.approved_plan_json,'$.metadata.album'),''),i.state,COALESCE(i.approved_release_mbid,''),i.state_revision,COALESCE((SELECT error_text FROM migration_item_errors e WHERE e.item_id=i.id ORDER BY occurred_at DESC,id DESC LIMIT 1),'') FROM migration_items i JOIN unmanaged_releases u ON u.candidate_id=i.unmanaged_candidate_id WHERE i.batch_id=? ORDER BY i.unmanaged_candidate_id`, batchID)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	for rows.Next() {
		var item application.MigrationItemSummary
		if err := rows.Scan(&item.ID, &item.ReleaseID, &item.AlbumArtist, &item.Album, &item.State, &item.CandidateMBID, &item.Revision, &item.Error); err != nil {
			return detail, err
		}
		detail.Items = append(detail.Items, item)
	}
	return detail, rows.Err()
}

func (r *Repositories) MigrationBatchStatus(ctx context.Context, batchID string) (application.MigrationBatchStatus, error) {
	var status application.MigrationBatchStatus
	err := r.DB.QueryRowContext(ctx, `SELECT b.status,b.state_revision,
		COALESCE(SUM(CASE WHEN i.state IN ('CHECK_PENDING','CHECKING','CONFIRMED','LIDARR_CATALOG_READY','IMPORT_SUBMITTED','RECONCILING') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN i.state IN ('NO_MATCH','AMBIGUOUS','EXACT_MATCH','MIGRATED') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN i.state='FAILED_RETRYABLE' THEN 1 ELSE 0 END),0)
		FROM migration_batches b LEFT JOIN migration_items i ON i.batch_id=b.id WHERE b.id=? GROUP BY b.id,b.status,b.state_revision`, batchID).
		Scan(&status.State, &status.Revision, &status.Active, &status.Completed, &status.Failed)
	return status, err
}

func (r *Repositories) Reviews(ctx context.Context, limit int, cursor string) ([]application.ReviewSummary, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT candidate_id,source,state,state_revision,COALESCE(gateway_job_id,''),updated_at
		FROM candidates WHERE state IN ('REVIEW_REQUIRED','QUARANTINED') AND (?='' OR updated_at<?)
		ORDER BY updated_at DESC,candidate_id DESC LIMIT ?`, cursor, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var items []application.ReviewSummary
	for rows.Next() {
		var item application.ReviewSummary
		var source, state, updated string
		if err := rows.Scan(&item.CandidateID, &source, &state, &item.Revision, &item.JobID, &updated); err != nil {
			return nil, "", err
		}
		item.Source = domain.Source(source)
		item.State, err = domain.ParseState(state)
		if err != nil {
			return nil, "", err
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].UpdatedAt.Format(time.RFC3339Nano)
		items = items[:limit]
	}
	return items, next, nil
}

func (r *Repositories) Review(ctx context.Context, candidateID string) (application.ReviewDetail, error) {
	candidate, err := r.Candidate(ctx, candidateID)
	if err != nil {
		return application.ReviewDetail{}, err
	}
	detail := application.ReviewDetail{Summary: application.ReviewSummary{CandidateID: candidate.ID, Source: candidate.Source, State: candidate.State, Revision: candidate.StateRevision, JobID: candidate.GatewayJobID, UpdatedAt: candidate.UpdatedAt}}
	rows, err := r.DB.QueryContext(ctx, `SELECT scope,subject,classification,code,CAST(evidence_json AS TEXT),created_at FROM validation_results WHERE candidate_id=? ORDER BY created_at,id`, candidateID)
	if err != nil {
		return detail, err
	}
	for rows.Next() {
		var row application.EvidenceRow
		var created string
		if err := rows.Scan(&row.Kind, &row.Subject, &row.Classification, &row.Code, &row.Details, &created); err != nil {
			rows.Close()
			return detail, err
		}
		row.OccurredAt, _ = time.Parse(time.RFC3339Nano, created)
		detail.Files = append(detail.Files, row)
	}
	rows.Close()
	rows, err = r.DB.QueryContext(ctx, `SELECT cf.relative_path,tm.medium_position,tm.track_position,tm.observed_duration_ms,tm.reference_duration_ms,tm.status,tm.recording_mbid,tm.release_track_mbid,tm.release_mbid FROM track_matches tm JOIN candidate_files cf ON cf.id=tm.candidate_file_id WHERE tm.candidate_id=? ORDER BY tm.medium_position,tm.track_position`, candidateID)
	if err != nil {
		return detail, err
	}
	for rows.Next() {
		var row application.TrackEvidence
		var reference sql.NullInt64
		if err := rows.Scan(&row.Path, &row.Medium, &row.Track, &row.ObservedDurationMS, &reference, &row.Status, &row.RecordingMBID, &row.ReleaseTrackMBID, &detail.ReleaseMBID); err != nil {
			rows.Close()
			return detail, err
		}
		if reference.Valid {
			value := reference.Int64
			row.ReferenceDurationMS = &value
		}
		detail.Tracks = append(detail.Tracks, row)
	}
	rows.Close()
	rows, err = r.DB.QueryContext(ctx, `SELECT ms.kind,COALESCE(cf.relative_path,''),CAST(ms.canonical_json AS TEXT),ms.sha256,ms.created_at FROM metadata_snapshots ms LEFT JOIN candidate_files cf ON cf.id=ms.candidate_file_id WHERE ms.candidate_id=? ORDER BY ms.created_at,ms.kind`, candidateID)
	if err != nil {
		return detail, err
	}
	for rows.Next() {
		var row application.MetadataEvidence
		var created string
		if err := rows.Scan(&row.Kind, &row.Path, &row.Canonical, &row.Checksum, &created); err != nil {
			rows.Close()
			return detail, err
		}
		row.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		detail.Metadata = append(detail.Metadata, row)
	}
	rows.Close()
	rows, err = r.DB.QueryContext(ctx, `SELECT kind,provider,classification,'',CAST(evidence_json AS TEXT),created_at FROM enrichments WHERE candidate_id=? ORDER BY created_at,id`, candidateID)
	if err != nil {
		return detail, err
	}
	for rows.Next() {
		var row application.EvidenceRow
		var created string
		if err := rows.Scan(&row.Kind, &row.Subject, &row.Classification, &row.Code, &row.Details, &created); err != nil {
			rows.Close()
			return detail, err
		}
		row.OccurredAt, _ = time.Parse(time.RFC3339Nano, created)
		detail.Enrichment = append(detail.Enrichment, row)
	}
	rows.Close()
	rows, err = r.DB.QueryContext(ctx, `SELECT previous_state||' -> '||new_state,actor,'TRANSITION',reason,'revision '||revision,occurred_at FROM state_transitions WHERE candidate_id=? ORDER BY revision`, candidateID)
	if err != nil {
		return detail, err
	}
	for rows.Next() {
		var row application.EvidenceRow
		var created string
		if err := rows.Scan(&row.Kind, &row.Subject, &row.Classification, &row.Code, &row.Details, &created); err != nil {
			rows.Close()
			return detail, err
		}
		row.OccurredAt, _ = time.Parse(time.RFC3339Nano, created)
		detail.History = append(detail.History, row)
	}
	return detail, rows.Close()
}

func (r *Repositories) Submissions(ctx context.Context, limit int, cursor string) ([]application.SubmissionSummary, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT id,source_path,status,state_revision,COALESCE(sealed_fingerprint,''),updated_at FROM submissions WHERE (?='' OR updated_at<?) ORDER BY updated_at DESC,id DESC LIMIT ?`, cursor, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var items []application.SubmissionSummary
	for rows.Next() {
		var item application.SubmissionSummary
		var updated string
		if err := rows.Scan(&item.ID, &item.SourcePath, &item.Status, &item.Revision, &item.SealedFingerprint, &updated); err != nil {
			return nil, "", err
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].UpdatedAt.Format(time.RFC3339Nano)
		items = items[:limit]
	}
	return items, next, nil
}

func (r *Repositories) Audit(ctx context.Context, limit int, cursor string) ([]application.AuditSummary, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT id,actor,action,reason,COALESCE(candidate_id,''),COALESCE(job_id,''),state_revision,occurred_at FROM audit_events WHERE (?='' OR occurred_at<?) ORDER BY occurred_at DESC,id DESC LIMIT ?`, cursor, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var items []application.AuditSummary
	for rows.Next() {
		var item application.AuditSummary
		var revision sql.NullInt64
		var occurred string
		if err := rows.Scan(&item.ID, &item.Actor, &item.Action, &item.Reason, &item.CandidateID, &item.JobID, &revision, &occurred); err != nil {
			return nil, "", err
		}
		if revision.Valid {
			value := uint64(revision.Int64)
			item.Revision = &value
		}
		item.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].OccurredAt.Format(time.RFC3339Nano)
		items = items[:limit]
	}
	return items, next, nil
}

func (r *Repositories) Sessions(ctx context.Context, userID, currentID string) ([]application.SessionSummary, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,created_at,expires_at FROM sessions WHERE user_id=? AND revoked_at IS NULL AND expires_at>? ORDER BY created_at DESC`, userID, formatTime(r.Now().UTC()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []application.SessionSummary
	for rows.Next() {
		var item application.SessionSummary
		var created, expires string
		if err := rows.Scan(&item.ID, &created, &expires); err != nil {
			return nil, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err == nil {
			item.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
		}
		if err != nil {
			return nil, err
		}
		item.Current = item.ID == currentID
		items = append(items, item)
	}
	return items, rows.Err()
}
