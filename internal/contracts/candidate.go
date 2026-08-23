package contracts

import "time"

type AcquisitionSource string

const (
	SourceSlskd     AcquisitionSource = "SLSKD"
	SourceSpotiFLAC AcquisitionSource = "SPOTIFLAC"
	SourceManual    AcquisitionSource = "MANUAL"
)

type AcquisitionProvenance struct {
	Provider      string `json:"provider"`
	EngineVersion string `json:"engine_version"`
	OutputSHA256  string `json:"output_sha256"`
}

type CandidateRegistered struct {
	RequestID        string            `json:"request_id"`
	JobID            string            `json:"job_id"`
	CandidateID      string            `json:"candidate_id"`
	ConfigSnapshotID string            `json:"config_snapshot_id"`
	Source           AcquisitionSource `json:"source"`
	SourceLocator    string            `json:"source_locator"`
	DownloadID       string            `json:"download_id,omitempty"`
	RegisteredAt     time.Time         `json:"registered_at"`
}

type CandidateAccepted struct {
	RequestID        string                `json:"request_id"`
	JobID            string                `json:"job_id"`
	CandidateID      string                `json:"candidate_id"`
	ConfigSnapshotID string                `json:"config_snapshot_id"`
	Source           AcquisitionSource     `json:"source"`
	Path             string                `json:"path"`
	CompletionAt     time.Time             `json:"completion_at"`
	Provenance       AcquisitionProvenance `json:"provenance"`
}

type CandidateApproved struct {
	RequestID            string        `json:"request_id"`
	JobID                string        `json:"job_id"`
	CandidateID          string        `json:"candidate_id"`
	ConfigSnapshotID     string        `json:"config_snapshot_id"`
	MusicBrainzReleaseID string        `json:"musicbrainz_release_id"`
	ApprovedAt           time.Time     `json:"approved_at"`
	Quality              QualityVector `json:"quality"`
	Warnings             []Warning     `json:"warnings"`
	StateRevision        uint64        `json:"state_revision"`
}

type CandidateWinner struct {
	RequestID        string        `json:"request_id"`
	JobID            string        `json:"job_id"`
	CandidateID      string        `json:"candidate_id"`
	ConfigSnapshotID string        `json:"config_snapshot_id"`
	WinnerLockedAt   time.Time     `json:"winner_locked_at"`
	Reason           string        `json:"reason"`
	Quality          QualityVector `json:"quality"`
	StateRevision    uint64        `json:"state_revision"`
}

type CandidateSuperseded struct {
	RequestID         string    `json:"request_id"`
	JobID             string    `json:"job_id"`
	CandidateID       string    `json:"candidate_id"`
	ConfigSnapshotID  string    `json:"config_snapshot_id"`
	WinnerCandidateID string    `json:"winner_candidate_id"`
	Reason            string    `json:"reason"`
	SupersededAt      time.Time `json:"superseded_at"`
	StateRevision     uint64    `json:"state_revision"`
}

type CandidateCancelled struct {
	RequestID        string    `json:"request_id"`
	JobID            string    `json:"job_id"`
	CandidateID      string    `json:"candidate_id"`
	ConfigSnapshotID string    `json:"config_snapshot_id"`
	StateRevision    uint64    `json:"state_revision"`
	Reason           string    `json:"reason"`
	CancelledAt      time.Time `json:"cancelled_at"`
}
