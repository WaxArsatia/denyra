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

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/logsafe"
)

const acquisitionSectionLimit = 100
const acquisitionMessageLimit = 2 << 10

func (r *Repositories) JobSummaries(ctx context.Context, limit int, cursor, state string) (contracts.AcquisitionJobPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	state = strings.ToUpper(strings.TrimSpace(state))
	if state != "" {
		if _, err := domain.ParseState(state); err != nil {
			return contracts.AcquisitionJobPage{}, err
		}
	}
	updated, id, err := decodeAcquisitionCursor(cursor)
	if err != nil {
		return contracts.AcquisitionJobPage{}, err
	}
	where := []string{"1=1"}
	arguments := make([]any, 0, 5)
	if state != "" {
		where = append(where, "state=?")
		arguments = append(arguments, state)
	}
	if updated != "" {
		where = append(where, "(updated_at<? OR (updated_at=? AND id<?))")
		arguments = append(arguments, updated, updated, id)
	}
	arguments = append(arguments, limit+1)
	rows, err := r.DB.QueryContext(ctx, `SELECT id,state,release_group_mbid,COALESCE(selected_release_mbid,''),lidarr_album_id,state_revision,primary_attempt,fallback_attempt,next_retry_at,updated_at FROM acquisition_jobs WHERE `+strings.Join(where, " AND ")+` ORDER BY updated_at DESC,id DESC LIMIT ?`, arguments...)
	if err != nil {
		return contracts.AcquisitionJobPage{}, err
	}
	defer rows.Close()
	page := contracts.AcquisitionJobPage{Items: make([]contracts.AcquisitionJobSummary, 0, limit+1)}
	for rows.Next() {
		var item contracts.AcquisitionJobSummary
		var retry sql.NullString
		var itemUpdated string
		if err := rows.Scan(&item.JobID, &item.State, &item.ReleaseGroupMBID, &item.SelectedReleaseMBID, &item.LidarrAlbumID, &item.StateRevision, &item.PrimaryAttempt, &item.FallbackAttempt, &retry, &itemUpdated); err != nil {
			return contracts.AcquisitionJobPage{}, err
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, itemUpdated)
		if err == nil && retry.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, retry.String)
			err, item.NextRetryAt = parseErr, &value
		}
		if err != nil {
			return contracts.AcquisitionJobPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return contracts.AcquisitionJobPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.Next, err = encodeAcquisitionCursor(last.UpdatedAt, last.JobID)
	}
	return page, err
}

func (r *Repositories) JobEvidence(ctx context.Context, jobID string) (contracts.AcquisitionJobDetail, error) {
	job, err := r.Job(ctx, jobID)
	if err != nil {
		return contracts.AcquisitionJobDetail{}, err
	}
	result := contracts.AcquisitionJobDetail{Job: contracts.AcquisitionJobHeader{
		JobID: job.ID, LidarrAlbumID: job.LidarrAlbumID, ReleaseGroupMBID: job.ReleaseGroupMBID,
		SelectedReleaseMBID: job.SelectedReleaseMBID, State: string(job.State), StateRevision: job.Revision,
		PrimaryAttempt: job.PrimaryAttempt, FallbackAttempt: job.FallbackAttempt, NextRetryAt: job.NextRetryAt, UpdatedAt: job.UpdatedAt,
	}, Transitions: []contracts.AcquisitionTransition{}, Attempts: []contracts.AcquisitionAttemptSummary{}, Candidates: []contracts.AcquisitionCandidate{}, Correlation: []contracts.AcquisitionCorrelation{}}

	transitionRows, err := r.DB.QueryContext(ctx, `SELECT actor,reason,previous_state,new_state,revision,occurred_at FROM state_transitions WHERE job_id=? ORDER BY revision LIMIT 101`, jobID)
	if err != nil {
		return result, err
	}
	for transitionRows.Next() {
		var item contracts.AcquisitionTransition
		var occurred string
		if err := transitionRows.Scan(&item.Actor, &item.Reason, &item.PreviousState, &item.NewState, &item.Revision, &occurred); err != nil {
			transitionRows.Close()
			return result, err
		}
		item.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			transitionRows.Close()
			return result, err
		}
		result.Transitions = append(result.Transitions, item)
	}
	if err := transitionRows.Close(); err != nil {
		return result, err
	}
	result.Transitions, result.TruncatedSections = truncateSection(result.Transitions, "transitions", result.TruncatedSections)

	attemptRows, err := r.DB.QueryContext(ctx, `SELECT a.id,a.kind,a.number,COALESCE(p.provider,''),COALESCE(p.outcome,a.outcome,''),COALESCE(a.error_class,''),COALESCE(p.started_at,a.started_at),COALESCE(p.completed_at,a.completed_at),a.details_json,p.evidence_json
		FROM attempts a LEFT JOIN provider_results p ON p.attempt_id=a.id WHERE a.job_id=? ORDER BY a.started_at,a.id,p.started_at,p.id LIMIT 101`, jobID)
	if err != nil {
		return result, err
	}
	for attemptRows.Next() {
		var item contracts.AcquisitionAttemptSummary
		var started string
		var completed sql.NullString
		var attemptDetails []byte
		var providerDetails []byte
		if err := attemptRows.Scan(&item.ID, &item.Kind, &item.Number, &item.Provider, &item.Outcome, &item.ErrorClass, &started, &completed, &attemptDetails, &providerDetails); err != nil {
			attemptRows.Close()
			return result, err
		}
		item.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err == nil && completed.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, completed.String)
			err, item.CompletedAt = parseErr, &value
		}
		if err != nil {
			attemptRows.Close()
			return result, err
		}
		message, evidenceErrorClass := safeAttemptMessage(providerDetails, attemptDetails, item.Provider)
		if item.ErrorClass == "" {
			item.ErrorClass = evidenceErrorClass
		}
		item.Message = message
		result.Attempts = append(result.Attempts, item)
	}
	if err := attemptRows.Close(); err != nil {
		return result, err
	}
	result.Attempts, result.TruncatedSections = truncateSection(result.Attempts, "attempts", result.TruncatedSections)

	candidateRows, err := r.DB.QueryContext(ctx, `SELECT candidate_id,source,source_locator,download_id,completed_at,created_at,output_sha256 FROM (
		SELECT p.candidate_id,p.source,COALESCE(c.source_locator,p.source_locator) source_locator,COALESCE(c.download_id,p.download_id,'') download_id,c.completed_at,p.created_at,COALESCE(o.output_sha256,'') output_sha256
		FROM pending_acquisition_candidates p LEFT JOIN candidates c ON c.candidate_id=p.candidate_id LEFT JOIN candidate_output_evidence o ON o.candidate_id=c.candidate_id WHERE p.job_id=?
		UNION ALL
		SELECT c.candidate_id,c.source,c.source_locator,COALESCE(c.download_id,''),c.completed_at,c.created_at,COALESCE(o.output_sha256,'')
		FROM candidates c LEFT JOIN pending_acquisition_candidates p ON p.candidate_id=c.candidate_id LEFT JOIN candidate_output_evidence o ON o.candidate_id=c.candidate_id WHERE c.job_id=? AND p.candidate_id IS NULL
	) ORDER BY created_at,candidate_id LIMIT 101`, jobID, jobID)
	if err != nil {
		return result, err
	}
	for candidateRows.Next() {
		var item contracts.AcquisitionCandidate
		var completed sql.NullString
		var created string
		if err := candidateRows.Scan(&item.CandidateID, &item.Source, &item.SourceLocator, &item.DownloadID, &completed, &created, &item.OutputSHA256); err != nil {
			candidateRows.Close()
			return result, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err == nil && completed.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, completed.String)
			err, item.CompletedAt = parseErr, &value
		}
		if err != nil {
			candidateRows.Close()
			return result, err
		}
		result.Candidates = append(result.Candidates, item)
	}
	if err := candidateRows.Close(); err != nil {
		return result, err
	}
	result.Candidates, result.TruncatedSections = truncateSection(result.Candidates, "candidates", result.TruncatedSections)

	correlationRows, err := r.DB.QueryContext(ctx, `SELECT source_kind,source_record_id,COALESCE(command_id,''),COALESCE(download_id,''),observed_at,evidence_sha256 FROM correlation_evidence WHERE job_id=? ORDER BY observed_at,id LIMIT 101`, jobID)
	if err != nil {
		return result, err
	}
	defer correlationRows.Close()
	for correlationRows.Next() {
		var item contracts.AcquisitionCorrelation
		var observed string
		if err := correlationRows.Scan(&item.SourceKind, &item.SourceRecordID, &item.CommandID, &item.DownloadID, &observed, &item.EvidenceSHA256); err != nil {
			return result, err
		}
		item.ObservedAt, err = time.Parse(time.RFC3339Nano, observed)
		if err != nil {
			return result, err
		}
		result.Correlation = append(result.Correlation, item)
	}
	if err := correlationRows.Err(); err != nil {
		return result, err
	}
	result.Correlation, result.TruncatedSections = truncateSection(result.Correlation, "correlation", result.TruncatedSections)
	return result, nil
}

type acquisitionCursor struct {
	Updated string `json:"u"`
	ID      string `json:"i"`
}

func encodeAcquisitionCursor(updated time.Time, id string) (string, error) {
	payload, err := json.Marshal(acquisitionCursor{Updated: formatTime(updated), ID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeAcquisitionCursor(cursor string) (string, string, error) {
	if cursor == "" {
		return "", "", nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("invalid acquisition cursor")
	}
	var decoded acquisitionCursor
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Updated == "" || decoded.ID == "" {
		return "", "", fmt.Errorf("invalid acquisition cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, decoded.Updated); err != nil {
		return "", "", fmt.Errorf("invalid acquisition cursor")
	}
	return decoded.Updated, decoded.ID, nil
}

type legacyAttemptDetails struct {
	Provider     string `json:"provider"`
	ErrorClass   string `json:"errorClass"`
	ErrorMessage string `json:"errorMessage"`
	Providers    []struct {
		Provider     string `json:"provider"`
		ErrorClass   string `json:"errorClass"`
		ErrorMessage string `json:"errorMessage"`
	} `json:"providers"`
}

func safeAttemptMessage(providerDetails, attemptDetails []byte, provider string) (string, string) {
	var selected legacyAttemptDetails
	_ = json.Unmarshal(providerDetails, &selected)
	if selected.ErrorMessage == "" {
		var attempt legacyAttemptDetails
		_ = json.Unmarshal(attemptDetails, &attempt)
		selected = attempt
		for _, candidate := range attempt.Providers {
			if provider == "" || strings.EqualFold(candidate.Provider, provider) {
				selected.Provider, selected.ErrorClass, selected.ErrorMessage = candidate.Provider, candidate.ErrorClass, candidate.ErrorMessage
				break
			}
		}
	}
	return capUTF8(logsafe.RedactText(selected.ErrorMessage), acquisitionMessageLimit), selected.ErrorClass
}

func capUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func truncateSection[T any](items []T, name string, sections []string) ([]T, []string) {
	if len(items) <= acquisitionSectionLimit {
		return items, sections
	}
	return items[:acquisitionSectionLimit], append(sections, name)
}
