package contracts

import (
	"time"
)

type AcquisitionJobDetail struct {
	Job               AcquisitionJobHeader        `json:"job"`
	Transitions       []AcquisitionTransition     `json:"transitions"`
	Attempts          []AcquisitionAttemptSummary `json:"attempts"`
	Candidates        []AcquisitionCandidate      `json:"candidates"`
	Correlation       []AcquisitionCorrelation    `json:"correlation"`
	TruncatedSections []string                    `json:"truncated_sections,omitempty"`
}

type AcquisitionJobEvidence = AcquisitionJobDetail

type AcquisitionJobHeader struct {
	JobID               string     `json:"job_id"`
	LidarrAlbumID       int64      `json:"lidarr_album_id"`
	ReleaseGroupMBID    string     `json:"release_group_mbid"`
	SelectedReleaseMBID string     `json:"selected_release_mbid,omitempty"`
	State               string     `json:"state"`
	StateRevision       uint64     `json:"state_revision"`
	PrimaryAttempt      int        `json:"primary_attempt"`
	FallbackAttempt     int        `json:"fallback_attempt"`
	NextRetryAt         *time.Time `json:"next_retry_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type AcquisitionJobSummary struct {
	JobID               string     `json:"job_id"`
	State               string     `json:"state"`
	ReleaseGroupMBID    string     `json:"release_group_mbid"`
	SelectedReleaseMBID string     `json:"selected_release_mbid,omitempty"`
	LidarrAlbumID       int64      `json:"lidarr_album_id"`
	StateRevision       uint64     `json:"state_revision"`
	PrimaryAttempt      int        `json:"primary_attempt"`
	FallbackAttempt     int        `json:"fallback_attempt"`
	NextRetryAt         *time.Time `json:"next_retry_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type AcquisitionJobPage struct {
	Items []AcquisitionJobSummary `json:"items"`
	Next  string                  `json:"next,omitempty"`
}

type AcquisitionTransition struct {
	Actor         string    `json:"actor"`
	Reason        string    `json:"reason"`
	PreviousState string    `json:"previous_state"`
	NewState      string    `json:"new_state"`
	Revision      uint64    `json:"revision"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type AcquisitionAttemptSummary struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Provider    string     `json:"provider,omitempty"`
	Outcome     string     `json:"outcome,omitempty"`
	ErrorClass  string     `json:"error_class,omitempty"`
	Message     string     `json:"message,omitempty"`
	Number      int        `json:"number"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type AcquisitionCandidate struct {
	CandidateID   string     `json:"candidate_id"`
	Source        string     `json:"source"`
	SourceLocator string     `json:"source_locator"`
	DownloadID    string     `json:"download_id,omitempty"`
	OutputSHA256  string     `json:"output_sha256,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type AcquisitionCorrelation struct {
	SourceKind     string    `json:"source_kind"`
	SourceRecordID string    `json:"source_record_id"`
	CommandID      string    `json:"command_id,omitempty"`
	DownloadID     string    `json:"download_id,omitempty"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
	ObservedAt     time.Time `json:"observed_at"`
}
