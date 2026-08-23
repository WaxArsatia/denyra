package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func (r *Repositories) CreateCandidate(ctx context.Context, candidate domain.Candidate) error {
	if !candidate.State.Valid() {
		return fmt.Errorf("persist candidate: invalid state %q", candidate.State)
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO candidates(
		candidate_id, source, release_directory, config_snapshot_id, acquisition_evidence_id,
		gateway_job_id, state, state_revision, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`,
		candidate.ID, candidate.Source, candidate.ReleaseDirectory, candidate.ConfigSnapshotID,
		candidate.AcquisitionEvidenceID, candidate.GatewayJobID, candidate.State, candidate.StateRevision,
		formatTime(candidate.CreatedAt), formatTime(candidate.UpdatedAt))
	if err != nil {
		return fmt.Errorf("persist candidate: %w", err)
	}
	return nil
}

func (r *Repositories) Candidate(ctx context.Context, candidateID string) (domain.Candidate, error) {
	var candidate domain.Candidate
	var source, state, created, updated string
	var gatewayJob sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT candidate_id, source, release_directory, config_snapshot_id,
		acquisition_evidence_id, gateway_job_id, state, state_revision, created_at, updated_at
		FROM candidates WHERE candidate_id = ?`, candidateID).Scan(
		&candidate.ID, &source, &candidate.ReleaseDirectory, &candidate.ConfigSnapshotID,
		&candidate.AcquisitionEvidenceID, &gatewayJob, &state, &candidate.StateRevision, &created, &updated)
	if err != nil {
		return domain.Candidate{}, err
	}
	candidate.Source = domain.Source(source)
	if !candidate.Source.Valid() {
		return domain.Candidate{}, fmt.Errorf("persisted candidate source %q is invalid", source)
	}
	candidate.State, err = domain.ParseState(state)
	if err != nil {
		return domain.Candidate{}, err
	}
	candidate.GatewayJobID = gatewayJob.String
	if candidate.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return domain.Candidate{}, fmt.Errorf("parse created time: %w", err)
	}
	if candidate.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return domain.Candidate{}, fmt.Errorf("parse updated time: %w", err)
	}
	return candidate, nil
}

type TransitionCommand struct {
	CandidateID      string
	ExpectedRevision uint64
	To               domain.State
	Actor            string
	Reason           string
	TargetReleaseID  string
	DetailsJSON      []byte
	OccurredAt       time.Time
}

func (r *Repositories) UpdateState(ctx context.Context, command TransitionCommand) (domain.TransitionEvent, error) {
	var event domain.TransitionEvent
	err := denysqlite.WithinTx(ctx, r.DB, func(tx *sql.Tx) error {
		candidate, err := candidateInTx(ctx, tx, command.CandidateID)
		if err != nil {
			return err
		}
		event, err = candidate.Transition(command.ExpectedRevision, command.To, command.Actor, command.Reason, command.OccurredAt)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE candidates SET state=?, state_revision=?, updated_at=?
			WHERE candidate_id=? AND state_revision=?`, candidate.State, candidate.StateRevision, formatTime(candidate.UpdatedAt), candidate.ID, command.ExpectedRevision)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			current, loadErr := candidateInTx(ctx, tx, command.CandidateID)
			if loadErr != nil {
				return loadErr
			}
			return &domain.StaleRevisionError{Expected: command.ExpectedRevision, Current: current.StateRevision, State: current.State}
		}
		transitionID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions(id,candidate_id,actor,reason,previous_state,new_state,previous_revision,revision,occurred_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, transitionID, event.CandidateID, event.Actor, event.Reason, event.PreviousState, event.NewState,
			event.PreviousRevision, event.Revision, formatTime(event.OccurredAt)); err != nil {
			return err
		}
		auditID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		details := command.DetailsJSON
		if len(details) == 0 {
			details = []byte("{}")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,candidate_id,actor,action,reason,target_release_mbid,job_id,state_revision,details_json,occurred_at)
			VALUES(?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,?)`, auditID, candidate.ID, event.Actor, "STATE_TRANSITION", event.Reason,
			command.TargetReleaseID, candidate.GatewayJobID, event.Revision, details, formatTime(event.OccurredAt))
		return err
	})
	return event, err
}

func (r *Repositories) TransitionCandidate(ctx context.Context, candidateID string, expected uint64, to domain.State, actor, reason, targetReleaseID string, at time.Time) (domain.TransitionEvent, error) {
	return r.UpdateState(ctx, TransitionCommand{CandidateID: candidateID, ExpectedRevision: expected, To: to, Actor: actor, Reason: reason, TargetReleaseID: targetReleaseID, OccurredAt: at})
}

func candidateInTx(ctx context.Context, tx *sql.Tx, candidateID string) (domain.Candidate, error) {
	var candidate domain.Candidate
	var source, state, created, updated string
	var gatewayJob sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT candidate_id, source, release_directory, config_snapshot_id,
		acquisition_evidence_id, gateway_job_id, state, state_revision, created_at, updated_at
		FROM candidates WHERE candidate_id=?`, candidateID).Scan(&candidate.ID, &source, &candidate.ReleaseDirectory,
		&candidate.ConfigSnapshotID, &candidate.AcquisitionEvidenceID, &gatewayJob, &state, &candidate.StateRevision, &created, &updated)
	if err != nil {
		return domain.Candidate{}, err
	}
	candidate.Source, candidate.GatewayJobID = domain.Source(source), gatewayJob.String
	if !candidate.Source.Valid() {
		return domain.Candidate{}, fmt.Errorf("persisted candidate source %q is invalid", source)
	}
	candidate.State, err = domain.ParseState(state)
	if err != nil {
		return domain.Candidate{}, err
	}
	candidate.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err == nil {
		candidate.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	return candidate, err
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
