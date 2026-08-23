package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) Completion(ctx context.Context, candidateID string) (application.WorkflowCompletion, error) {
	var result application.WorkflowCompletion
	var completed, provenance []byte
	var target, download sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT c.source,c.release_directory,ce.id,ce.completed_at,COALESCE(ce.target_release_mbid,''),ce.provenance_json,COALESCE(p.download_id,'')
FROM candidates c JOIN completion_evidence ce ON ce.candidate_id=c.candidate_id
LEFT JOIN pending_acquisition_candidates p ON p.candidate_id=c.candidate_id WHERE c.candidate_id=?`, candidateID).
		Scan(&result.Source, &result.SourcePath, &result.EvidenceID, &completed, &target, &provenance, &download)
	if err != nil {
		return result, err
	}
	result.CompletedAt, err = time.Parse(time.RFC3339Nano, string(completed))
	result.TargetReleaseMBID, result.DownloadID, result.Provenance = target.String, download.String, provenance
	return result, err
}

func (r *Repositories) ManualCompletion(ctx context.Context, candidateID string) (application.WorkflowCompletion, error) {
	var result application.WorkflowCompletion
	var submitted sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT source_path,sealed_fingerprint,submitted_at FROM submissions WHERE id=? AND status='SEALED'`, candidateID).
		Scan(&result.SourcePath, &result.SealedFingerprint, &submitted)
	if err != nil {
		return result, err
	}
	result.Source = domain.SourceManual
	result.EvidenceID = "manual:" + candidateID
	if submitted.Valid {
		result.CompletedAt, err = time.Parse(time.RFC3339Nano, submitted.String)
	}
	return result, err
}

func (r *Repositories) SetWaitingResubmit(ctx context.Context, candidateID string, expected uint64, reason string, at time.Time) error {
	if _, err := r.UpdateState(ctx, TransitionCommand{CandidateID: candidateID, ExpectedRevision: expected, To: domain.StateWaitingResubmit, Actor: "pipeline-worker", Reason: reason, OccurredAt: at}); err != nil {
		return err
	}
	_, err := r.DB.ExecContext(ctx, `UPDATE submissions SET status='WAITING_RESUBMIT',state_revision=state_revision+1,updated_at=? WHERE id=?`, formatTime(at), candidateID)
	return err
}

func (r *Repositories) SetWorkLocation(ctx context.Context, candidateID, path string, expected uint64, at time.Time) error {
	return denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE candidates SET release_directory=?,state='WORKING',state_revision=state_revision+1,updated_at=? WHERE candidate_id=? AND state_revision=? AND state='STABILIZING'`, path, formatTime(at), candidateID, expected)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return fmt.Errorf("candidate claim state changed")
		}
		transitionID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO state_transitions(id,candidate_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,'stable release atomically claimed','STABILIZING','WORKING',?,?,?)`, transitionID, candidateID, "pipeline-worker", expected, expected+1, formatTime(at))
		return err
	})
}

func (r *Repositories) TargetRelease(ctx context.Context, candidateID string) (string, error) {
	var target string
	err := r.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT NULLIF(target_release_mbid,'') FROM candidate_workflow WHERE candidate_id=?),(SELECT NULLIF(target_release_mbid,'') FROM completion_evidence WHERE candidate_id=?),(SELECT target_release_mbid FROM audit_events WHERE candidate_id=? AND target_release_mbid IS NOT NULL ORDER BY occurred_at DESC,id DESC LIMIT 1),'')`, candidateID, candidateID, candidateID).Scan(&target)
	return target, err
}

func (r *Repositories) SaveWorkflow(ctx context.Context, candidateID, target string, release domain.CanonicalRelease, match domain.ReleaseMatch, technical domain.TechnicalReleaseResult, warnings []domain.Warning, downloadID string, at time.Time) error {
	releaseJSON, _ := json.Marshal(release)
	matchJSON, _ := json.Marshal(match)
	technicalJSON, _ := json.Marshal(technical)
	warningsJSON, _ := json.Marshal(warnings)
	_, err := r.DB.ExecContext(ctx, `INSERT INTO candidate_workflow(candidate_id,target_release_mbid,canonical_release_json,release_match_json,technical_result_json,warnings_json,download_id,updated_at) VALUES(?,?,?,?,?,?,NULLIF(?,''),?)
ON CONFLICT(candidate_id) DO UPDATE SET target_release_mbid=COALESCE(NULLIF(excluded.target_release_mbid,''),candidate_workflow.target_release_mbid),canonical_release_json=CASE WHEN length(excluded.canonical_release_json)>2 THEN excluded.canonical_release_json ELSE candidate_workflow.canonical_release_json END,release_match_json=CASE WHEN length(excluded.release_match_json)>2 THEN excluded.release_match_json ELSE candidate_workflow.release_match_json END,technical_result_json=CASE WHEN length(excluded.technical_result_json)>2 THEN excluded.technical_result_json ELSE candidate_workflow.technical_result_json END,warnings_json=excluded.warnings_json,download_id=COALESCE(excluded.download_id,candidate_workflow.download_id),updated_at=excluded.updated_at`, candidateID, target, releaseJSON, matchJSON, technicalJSON, warningsJSON, downloadID, formatTime(at))
	return err
}

func (r *Repositories) Workflow(ctx context.Context, candidateID string) (application.WorkflowContext, error) {
	var result application.WorkflowContext
	var releaseJSON, matchJSON, technicalJSON, warningsJSON []byte
	var target, download sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT target_release_mbid,canonical_release_json,release_match_json,technical_result_json,warnings_json,download_id FROM candidate_workflow WHERE candidate_id=?`, candidateID).
		Scan(&target, &releaseJSON, &matchJSON, &technicalJSON, &warningsJSON, &download)
	if err != nil {
		return result, err
	}
	result.TargetReleaseMBID, result.DownloadID = target.String, download.String
	for _, item := range []struct {
		data        []byte
		destination any
	}{{releaseJSON, &result.Release}, {matchJSON, &result.Match}, {technicalJSON, &result.Technical}, {warningsJSON, &result.Warnings}} {
		if len(item.data) > 0 && string(item.data) != "null" {
			if err := json.Unmarshal(item.data, item.destination); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (r *Repositories) ImportIntentForCandidate(ctx context.Context, candidateID string) (domain.ImportIntent, error) {
	var intent domain.ImportIntent
	var manifestJSON, planJSON []byte
	err := r.DB.QueryRowContext(ctx, `SELECT id,idempotency_key,candidate_id,target_release_mbid,request_hash,release_manifest_json,plan_json,COALESCE(download_id,'') FROM import_intents WHERE candidate_id=?`, candidateID).
		Scan(&intent.ID, &intent.IdempotencyKey, &intent.CandidateID, &intent.TargetReleaseMBID, &intent.RequestHash, &manifestJSON, &planJSON, &intent.DownloadID)
	if err != nil {
		return intent, err
	}
	if err := json.Unmarshal(manifestJSON, &intent.Manifest); err != nil {
		return intent, err
	}
	if err := json.Unmarshal(planJSON, &intent.Plan); err != nil {
		return intent, err
	}
	return intent, nil
}
