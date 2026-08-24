package domain

import (
	"fmt"
	"time"
)

type MigrationState string

const (
	MigrationCheckPending       MigrationState = "CHECK_PENDING"
	MigrationChecking           MigrationState = "CHECKING"
	MigrationNoMatch            MigrationState = "NO_MATCH"
	MigrationAmbiguous          MigrationState = "AMBIGUOUS"
	MigrationExactMatch         MigrationState = "EXACT_MATCH"
	MigrationConfirmed          MigrationState = "CONFIRMED"
	MigrationLidarrCatalogReady MigrationState = "LIDARR_CATALOG_READY"
	MigrationImportSubmitted    MigrationState = "IMPORT_SUBMITTED"
	MigrationReconciling        MigrationState = "RECONCILING"
	MigrationMigrated           MigrationState = "MIGRATED"
	MigrationFailedRetryable    MigrationState = "FAILED_RETRYABLE"
)

type MigrationBatch struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	Actor          string    `json:"actor"`
	SelectionJSON  []byte    `json:"selection_json"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type MigrationItem struct {
	ID                   string         `json:"id"`
	BatchID              string         `json:"batch_id"`
	UnmanagedCandidateID string         `json:"unmanaged_candidate_id"`
	State                MigrationState `json:"state"`
	StateRevision        uint64         `json:"state_revision"`
	ResumeState          MigrationState `json:"resume_state,omitempty"`
	ApprovedReleaseMBID  string         `json:"approved_release_mbid,omitempty"`
	RequestEvidence      []byte         `json:"request_evidence,omitempty"`
	ResponseEvidence     []byte         `json:"response_evidence,omitempty"`
	MigrationEvidence    []byte         `json:"migration_evidence,omitempty"`
	IdempotencyKey       string         `json:"idempotency_key"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type MigrationItemError struct {
	ID        string         `json:"id"`
	ItemID    string         `json:"item_id"`
	State     MigrationState `json:"state"`
	Message   string         `json:"message"`
	Evidence  []byte         `json:"evidence,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func TransitionMigration(item MigrationItem, to MigrationState, at time.Time) (MigrationItem, error) {
	if item.ID == "" {
		return item, fmt.Errorf("migration item ID is required")
	}
	if !allowedMigrationTransition(item, to) {
		return item, fmt.Errorf("invalid migration transition %s -> %s", item.State, to)
	}
	if to == MigrationFailedRetryable {
		item.ResumeState = item.State
	} else if item.State == MigrationFailedRetryable {
		item.ResumeState = ""
	}
	item.State = to
	item.StateRevision++
	item.UpdatedAt = at.UTC()
	return item, nil
}

func allowedMigrationTransition(item MigrationItem, to MigrationState) bool {
	if item.State == MigrationFailedRetryable {
		return item.ResumeState != "" && to == item.ResumeState
	}
	switch item.State {
	case MigrationCheckPending:
		return to == MigrationChecking
	case MigrationChecking:
		return to == MigrationNoMatch || to == MigrationAmbiguous || to == MigrationExactMatch || to == MigrationFailedRetryable
	case MigrationExactMatch:
		return to == MigrationConfirmed
	case MigrationConfirmed:
		return to == MigrationLidarrCatalogReady || to == MigrationFailedRetryable
	case MigrationLidarrCatalogReady:
		return to == MigrationImportSubmitted || to == MigrationFailedRetryable
	case MigrationImportSubmitted:
		return to == MigrationReconciling
	case MigrationReconciling:
		return to == MigrationMigrated || to == MigrationFailedRetryable
	default:
		return false
	}
}

func (s MigrationState) CheckTerminal() bool {
	return s == MigrationNoMatch || s == MigrationAmbiguous || s == MigrationExactMatch
}
