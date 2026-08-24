package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type ReviewSummary struct {
	CandidateID string
	Source      domain.Source
	State       domain.State
	Revision    uint64
	JobID       string
	UpdatedAt   time.Time
}

type SubmissionSummary struct {
	ID                string
	SourcePath        string
	Status            string
	Revision          uint64
	SealedFingerprint string
	UpdatedAt         time.Time
}

type EvidenceRow struct {
	Kind           string
	Subject        string
	Classification string
	Code           string
	Details        string
	OccurredAt     time.Time
}

type TrackEvidence struct {
	Path                string
	Medium              int
	Track               int
	ObservedDurationMS  int64
	ReferenceDurationMS *int64
	Status              string
	RecordingMBID       string
	ReleaseTrackMBID    string
}

type MetadataEvidence struct {
	Kind      string
	Path      string
	Canonical string
	Checksum  string
	CreatedAt time.Time
}

type ReviewDetail struct {
	Summary     ReviewSummary
	ReleaseMBID string
	Files       []EvidenceRow
	Tracks      []TrackEvidence
	Metadata    []MetadataEvidence
	Enrichment  []EvidenceRow
	History     []EvidenceRow
}

type AuditSummary struct {
	ID          string
	Actor       string
	Action      string
	Reason      string
	CandidateID string
	JobID       string
	Revision    *uint64
	OccurredAt  time.Time
}

type SessionSummary struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
	Current   bool
}

type UnmanagedSummary struct {
	CandidateID string
	AlbumArtist string
	Album       string
	Year        string
	State       string
	Revision    uint64
	UpdatedAt   time.Time
}

type MigrationItemSummary struct {
	ID, ReleaseID, AlbumArtist, Album, State, CandidateMBID, Error string
	Revision                                                       uint64
}

type MigrationBatchDetail struct {
	ID, Actor, State     string
	Items                []MigrationItemSummary
	CreatedAt, UpdatedAt time.Time
}

type MigrationAdminReader interface {
	UnmanagedSummaries(context.Context, UnmanagedFilter) ([]UnmanagedSummary, error)
	MigrationBatchDetail(context.Context, string) (MigrationBatchDetail, error)
}

type AdminReader interface {
	Reviews(context.Context, int, string) ([]ReviewSummary, string, error)
	Review(context.Context, string) (ReviewDetail, error)
	Submissions(context.Context, int, string) ([]SubmissionSummary, string, error)
	Audit(context.Context, int, string) ([]AuditSummary, string, error)
	Sessions(context.Context, string, string) ([]SessionSummary, error)
}

type AcquisitionEvidence struct {
	JobID          string          `json:"job_id"`
	State          string          `json:"state"`
	Revision       uint64          `json:"state_revision"`
	AlbumID        int64           `json:"lidarr_album_id"`
	ReleaseGroupID string          `json:"release_group_mbid"`
	Evidence       json.RawMessage `json:"evidence"`
	ObservedAt     time.Time       `json:"observed_at"`
}

type AcquisitionEvidenceReader interface {
	AcquisitionEvidence(context.Context, string) (AcquisitionEvidence, error)
}
