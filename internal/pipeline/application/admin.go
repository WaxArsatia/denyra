package application

import (
	"context"
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

type UploadSessionSummary struct {
	ID, SubmissionID, Status string
	Revision                 uint64
	FileCount, CompleteCount int
	UpdatedAt                time.Time
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

type UnmanagedPage struct {
	Items []UnmanagedSummary
	Next  string
}

type MigrationItemSummary struct {
	ID, ReleaseID, AlbumArtist, Album, State, CandidateMBID, Error string
	Revision                                                       uint64
}

type MigrationBatchDetail struct {
	ID, Actor, State     string
	Revision             uint64
	Items                []MigrationItemSummary
	CreatedAt, UpdatedAt time.Time
}

type MigrationBatchStatus struct {
	State                     string
	Active, Completed, Failed int
	Revision                  uint64
}

type MigrationAdminReader interface {
	UnmanagedSummaries(context.Context, UnmanagedFilter, int, string) ([]UnmanagedSummary, string, error)
	MigrationBatchDetail(context.Context, string) (MigrationBatchDetail, error)
	MigrationBatchStatus(context.Context, string) (MigrationBatchStatus, error)
}

type AdminReader interface {
	Reviews(context.Context, int, string) ([]ReviewSummary, string, error)
	Review(context.Context, string) (ReviewDetail, error)
	Submissions(context.Context, int, string) ([]SubmissionSummary, string, error)
	Audit(context.Context, int, string) ([]AuditSummary, string, error)
	Sessions(context.Context, string, string) ([]SessionSummary, error)
}

type AcquisitionEvidence struct {
	JobID, State, ReleaseGroupID, SelectedReleaseID string
	Revision                                        uint64
	PrimaryAttempt, FallbackAttempt                 int
	AlbumID                                         int64
	NextRetryAt                                     *time.Time
	ObservedAt                                      time.Time
	Transitions                                     []AcquisitionTransition
	Attempts                                        []AcquisitionAttempt
	Candidates                                      []AcquisitionCandidate
	Correlations                                    []AcquisitionCorrelation
	TruncatedSections                               []string
}

type AcquisitionSummary struct {
	JobID, State, ReleaseGroupID, SelectedReleaseID string
	Revision                                        uint64
	AlbumID                                         int64
	PrimaryAttempt, FallbackAttempt                 int
	NextRetryAt                                     *time.Time
	UpdatedAt                                       time.Time
}

type AcquisitionPage struct {
	Items []AcquisitionSummary
	Next  string
}

type AcquisitionTransition struct {
	Actor, Reason, PreviousState, NewState string
	Revision                               uint64
	OccurredAt                             time.Time
}

type AcquisitionAttempt struct {
	ID, Kind, Provider, Outcome, ErrorClass, Message string
	Number                                           int
	StartedAt                                        time.Time
	CompletedAt                                      *time.Time
}

type AcquisitionCandidate struct {
	CandidateID, Source, SourceLocator, DownloadID, OutputSHA256 string
	CompletedAt                                                  *time.Time
	CreatedAt                                                    time.Time
}

type AcquisitionCorrelation struct {
	SourceKind, SourceRecordID, CommandID, DownloadID, EvidenceSHA256 string
	ObservedAt                                                        time.Time
}

type AcquisitionReader interface {
	ListAcquisitions(context.Context, int, string, string) (AcquisitionPage, error)
	AcquisitionEvidence(context.Context, string) (AcquisitionEvidence, error)
}
