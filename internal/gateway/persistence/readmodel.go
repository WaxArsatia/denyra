package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
)

func (r *Repositories) JobEvidence(ctx context.Context, jobID string) (contracts.AcquisitionJobEvidence, error) {
	job, err := r.Job(ctx, jobID)
	if err != nil {
		return contracts.AcquisitionJobEvidence{}, err
	}
	result := contracts.AcquisitionJobEvidence{Job: contracts.AcquisitionJobHeader{
		JobID: job.ID, LidarrAlbumID: job.LidarrAlbumID, ReleaseGroupMBID: job.ReleaseGroupMBID,
		SelectedReleaseMBID: job.SelectedReleaseMBID, State: string(job.State), StateRevision: job.Revision,
		PrimaryAttempt: job.PrimaryAttempt, FallbackAttempt: job.FallbackAttempt, NextRetryAt: job.NextRetryAt,
		UpdatedAt: job.UpdatedAt,
	}, Transitions: []contracts.AcquisitionTransition{}, Attempts: []contracts.AcquisitionAttempt{}, Candidates: []contracts.AcquisitionCandidate{}, Correlation: []contracts.AcquisitionCorrelation{}}
	transitionRows, err := r.DB.QueryContext(ctx, `SELECT actor,reason,previous_state,new_state,revision,occurred_at FROM state_transitions WHERE job_id=? ORDER BY revision`, jobID)
	if err != nil {
		return result, err
	}
	for transitionRows.Next() {
		var item contracts.AcquisitionTransition
		var occurredAt string
		if err := transitionRows.Scan(&item.Actor, &item.Reason, &item.PreviousState, &item.NewState, &item.Revision, &occurredAt); err != nil {
			transitionRows.Close()
			return result, err
		}
		item.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			transitionRows.Close()
			return result, err
		}
		result.Transitions = append(result.Transitions, item)
	}
	if err := transitionRows.Close(); err != nil {
		return result, err
	}
	attemptRows, err := r.DB.QueryContext(ctx, `SELECT id,kind,number,started_at,completed_at,COALESCE(outcome,''),COALESCE(error_class,''),details_json FROM attempts WHERE job_id=? ORDER BY started_at,id`, jobID)
	if err != nil {
		return result, err
	}
	for attemptRows.Next() {
		var item contracts.AcquisitionAttempt
		var startedAt string
		var completedAt sql.NullString
		var details []byte
		if err := attemptRows.Scan(&item.ID, &item.Kind, &item.Number, &startedAt, &completedAt, &item.Outcome, &item.ErrorClass, &details); err != nil {
			attemptRows.Close()
			return result, err
		}
		item.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
		if err == nil && completedAt.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, completedAt.String)
			err = parseErr
			item.CompletedAt = &value
		}
		if err != nil {
			attemptRows.Close()
			return result, err
		}
		item.Details = json.RawMessage(details)
		result.Attempts = append(result.Attempts, item)
	}
	if err := attemptRows.Close(); err != nil {
		return result, err
	}
	candidateRows, err := r.DB.QueryContext(ctx, `
SELECT p.candidate_id,p.source,COALESCE(c.source_locator,p.source_locator),COALESCE(c.download_id,p.download_id,''),c.completed_at,p.created_at,COALESCE(o.output_sha256,'')
FROM pending_acquisition_candidates p
LEFT JOIN candidates c ON c.candidate_id=p.candidate_id
LEFT JOIN candidate_output_evidence o ON o.candidate_id=c.candidate_id
WHERE p.job_id=?
UNION ALL
SELECT c.candidate_id,c.source,c.source_locator,COALESCE(c.download_id,''),c.completed_at,c.created_at,COALESCE(o.output_sha256,'')
FROM candidates c
LEFT JOIN pending_acquisition_candidates p ON p.candidate_id=c.candidate_id
LEFT JOIN candidate_output_evidence o ON o.candidate_id=c.candidate_id
WHERE c.job_id=? AND p.candidate_id IS NULL
ORDER BY 6,1`, jobID, jobID)
	if err != nil {
		return result, err
	}
	for candidateRows.Next() {
		var item contracts.AcquisitionCandidate
		var completedAt sql.NullString
		var createdAt string
		if err := candidateRows.Scan(&item.CandidateID, &item.Source, &item.SourceLocator, &item.DownloadID, &completedAt, &createdAt, &item.OutputSHA256); err != nil {
			candidateRows.Close()
			return result, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err == nil && completedAt.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, completedAt.String)
			err = parseErr
			item.CompletedAt = &value
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
	correlationRows, err := r.DB.QueryContext(ctx, `SELECT source_kind,source_record_id,COALESCE(command_id,''),COALESCE(download_id,''),observed_at,evidence_json,evidence_sha256 FROM correlation_evidence WHERE job_id=? ORDER BY observed_at,id`, jobID)
	if err != nil {
		return result, err
	}
	defer correlationRows.Close()
	for correlationRows.Next() {
		var item contracts.AcquisitionCorrelation
		var observedAt string
		var evidence []byte
		if err := correlationRows.Scan(&item.SourceKind, &item.SourceRecordID, &item.CommandID, &item.DownloadID, &observedAt, &evidence, &item.EvidenceSHA256); err != nil {
			return result, err
		}
		item.ObservedAt, err = time.Parse(time.RFC3339Nano, observedAt)
		if err != nil {
			return result, err
		}
		item.Evidence = json.RawMessage(evidence)
		result.Correlation = append(result.Correlation, item)
	}
	return result, correlationRows.Err()
}
