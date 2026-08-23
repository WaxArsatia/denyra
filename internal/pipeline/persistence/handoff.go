package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) CurrentConfigSnapshotID(ctx context.Context) (string, error) {
	var id string
	err := r.DB.QueryRowContext(ctx, `SELECT id FROM config_snapshots ORDER BY created_at DESC,id DESC LIMIT 1`).Scan(&id)
	return id, err
}

func (r *Repositories) RegisterPendingCandidate(ctx context.Context, request contracts.CandidateRegistered, key, requestHash string, payload []byte, at time.Time) (int, []byte, error) {
	status := httpStatusAccepted
	response, _ := json.Marshal(map[string]any{"candidate_id": request.CandidateID, "state": "PENDING_COMPLETION"})
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		replayed, storedStatus, storedBody, err := idempotencyInTx(ctx, tx, key, "candidate-registration", requestHash, payload, at)
		if err != nil {
			return err
		}
		if replayed {
			status, response = storedStatus, storedBody
			return nil
		}
		source := map[contracts.AcquisitionSource]string{contracts.SourceSlskd: "slskd", contracts.SourceSpotiFLAC: "spotiflac"}[request.Source]
		result, err := tx.ExecContext(ctx, `INSERT INTO pending_acquisition_candidates(candidate_id,job_id,source,source_locator,download_id,gateway_config_snapshot_id,registration_json,registration_sha256,registered_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, request.CandidateID, request.JobID, source, request.SourceLocator, request.DownloadID, request.ConfigSnapshotID, payload, requestHash, formatTime(request.RegisteredAt))
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			var storedHash string
			if err := tx.QueryRowContext(ctx, `SELECT registration_sha256 FROM pending_acquisition_candidates WHERE candidate_id=?`, request.CandidateID).Scan(&storedHash); err != nil {
				return err
			}
			if storedHash != requestHash {
				return contracts.ErrIdempotencyConflict
			}
		}
		return completeIdempotencyTx(ctx, tx, key, status, response, at)
	})
	return status, response, err
}

func (r *Repositories) AcceptCandidate(ctx context.Context, candidate domain.Candidate, request contracts.CandidateAccepted, key, requestHash string, payload []byte, at time.Time) (int, []byte, error) {
	status := httpStatusAccepted
	response, _ := json.Marshal(map[string]any{"candidate_id": candidate.ID, "state": candidate.State, "state_revision": candidate.StateRevision})
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		replayed, storedStatus, storedBody, err := idempotencyInTx(ctx, tx, key, "candidate-handoff", requestHash, payload, at)
		if err != nil {
			return err
		}
		if replayed {
			status, response = storedStatus, storedBody
			return nil
		}
		var pendingJob, pendingSource, pendingConfig sql.NullString
		pendingErr := tx.QueryRowContext(ctx, `SELECT job_id,source,gateway_config_snapshot_id FROM pending_acquisition_candidates WHERE candidate_id=?`, candidate.ID).Scan(&pendingJob, &pendingSource, &pendingConfig)
		if pendingErr != nil && pendingErr != sql.ErrNoRows {
			return pendingErr
		}
		if pendingErr == nil && (pendingJob.String != request.JobID || pendingSource.String != string(candidate.Source) || pendingConfig.String != request.ConfigSnapshotID) {
			return contracts.ErrIdempotencyConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO candidates(candidate_id,source,release_directory,config_snapshot_id,acquisition_evidence_id,gateway_job_id,state,state_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, candidate.ID, candidate.Source, candidate.ReleaseDirectory, candidate.ConfigSnapshotID, candidate.AcquisitionEvidenceID, candidate.GatewayJobID, candidate.State, candidate.StateRevision, formatTime(candidate.CreatedAt), formatTime(candidate.UpdatedAt))
		if err != nil {
			return err
		}
		provenance, err := json.Marshal(request.Provenance)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(provenance)
		evidenceID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO completion_evidence(id,candidate_id,gateway_config_snapshot_id,source_path,completed_at,provenance_json,provenance_sha256,received_at,target_release_mbid) VALUES(?,?,?,?,?,?,?,?,?)`, evidenceID, candidate.ID, request.ConfigSnapshotID, candidate.ReleaseDirectory, formatTime(request.CompletionAt), provenance, hex.EncodeToString(sum[:]), formatTime(at), request.MusicBrainzReleaseID)
		if err != nil {
			return err
		}
		return completeIdempotencyTx(ctx, tx, key, status, response, at)
	})
	return status, response, err
}

func (r *Repositories) ApplyCandidateDirective(ctx context.Context, key, operation, candidateID, jobID, configSnapshotID string, expected uint64, to domain.State, actor, reason, target string, requestHash []byte, at time.Time) (int, []byte, error) {
	status := httpStatusOK
	var response []byte
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		hash := string(requestHash)
		replayed, storedStatus, storedBody, err := idempotencyInTx(ctx, tx, key, "directive:"+operation, hash, requestHash, at)
		if err != nil {
			return err
		}
		if replayed {
			status, response = storedStatus, storedBody
			return nil
		}
		candidate, err := candidateInTx(ctx, tx, candidateID)
		if err != nil {
			return err
		}
		if candidate.GatewayJobID != jobID || candidate.ConfigSnapshotID != configSnapshotID {
			return contracts.ErrIdempotencyConflict
		}
		event, err := candidate.Transition(expected, to, actor, reason, at)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE candidates SET state=?,state_revision=?,updated_at=? WHERE candidate_id=? AND state_revision=?`, candidate.State, candidate.StateRevision, formatTime(candidate.UpdatedAt), candidate.ID, expected)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			current, loadErr := candidateInTx(ctx, tx, candidateID)
			if loadErr != nil {
				return loadErr
			}
			return &domain.StaleRevisionError{Expected: expected, Current: current.StateRevision, State: current.State}
		}
		transitionID, _ := ids.NewToken(16)
		if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions(id,candidate_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at) VALUES(?,?,?,?,?,?,?,?,?)`, transitionID, candidateID, actor, reason, event.PreviousState, event.NewState, event.PreviousRevision, event.Revision, formatTime(at)); err != nil {
			return err
		}
		auditID, _ := ids.NewToken(16)
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,candidate_id,actor,action,reason,target_release_mbid,job_id,state_revision,details_json,occurred_at) VALUES(?,?,?,?,?,NULLIF(?,''),?,?,?,?)`, auditID, candidateID, actor, operation, reason, target, candidate.GatewayJobID, event.Revision, []byte(`{}`), formatTime(at)); err != nil {
			return err
		}
		response, _ = json.Marshal(map[string]any{"candidate_id": candidateID, "state": to, "state_revision": event.Revision})
		return completeIdempotencyTx(ctx, tx, key, status, response, at)
	})
	return status, response, err
}

func idempotencyInTx(ctx context.Context, tx *sql.Tx, key, scope, hash string, payload []byte, at time.Time) (bool, int, []byte, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO idempotency_records(key,scope,request_hash,request_body,created_at) VALUES(?,?,?,?,?) ON CONFLICT(key) DO NOTHING`, key, scope, hash, payload, formatTime(at))
	if err != nil {
		return false, 0, nil, err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return false, 0, nil, nil
	}
	var storedScope, storedHash string
	var status sql.NullInt64
	var body, storedPayload []byte
	err = tx.QueryRowContext(ctx, `SELECT scope,request_hash,request_body,response_status,response_body FROM idempotency_records WHERE key=?`, key).Scan(&storedScope, &storedHash, &storedPayload, &status, &body)
	if err != nil {
		return false, 0, nil, err
	}
	if storedScope != scope || storedHash != hash || string(storedPayload) != string(payload) {
		return false, 0, nil, contracts.ErrIdempotencyConflict
	}
	if !status.Valid {
		return false, 0, nil, nil
	}
	return true, int(status.Int64), body, nil
}
func completeIdempotencyTx(ctx context.Context, tx *sql.Tx, key string, status int, body []byte, at time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE idempotency_records SET response_status=?,response_body=?,completed_at=? WHERE key=? AND response_status IS NULL`, status, body, formatTime(at), key)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return contracts.ErrIdempotencyConflict
	}
	return nil
}

const (
	httpStatusOK       = 200
	httpStatusAccepted = 202
)
