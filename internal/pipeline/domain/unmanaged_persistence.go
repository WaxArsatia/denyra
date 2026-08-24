package domain

import "time"

type UnmanagedRelease struct {
	CandidateID   string                 `json:"candidate_id"`
	Plan          UnmanagedPlan          `json:"plan"`
	Evidence      TechnicalReleaseResult `json:"evidence"`
	State         State                  `json:"state"`
	StateRevision uint64                 `json:"state_revision"`
	FinalPath     string                 `json:"final_path,omitempty"`
	Manifest      []PlannedFile          `json:"manifest,omitempty"`
	Fingerprint   string                 `json:"fingerprint,omitempty"`
	Status        string                 `json:"status"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type UnmanagedImportIntent struct {
	ID             string                 `json:"id"`
	CandidateID    string                 `json:"candidate_id"`
	IdempotencyKey string                 `json:"idempotency_key"`
	Plan           UnmanagedPlan          `json:"plan"`
	Evidence       TechnicalReleaseResult `json:"evidence"`
	FinalPath      string                 `json:"final_path,omitempty"`
	Manifest       []PlannedFile          `json:"manifest,omitempty"`
	Fingerprint    string                 `json:"fingerprint,omitempty"`
	Status         string                 `json:"status"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}
