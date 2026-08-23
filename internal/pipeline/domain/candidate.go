package domain

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var candidateIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidateCandidateID(value string) error {
	if !candidateIDPattern.MatchString(value) {
		return fmt.Errorf("candidate ID must use 1..128 URL-safe characters")
	}
	return nil
}

type Source string

const (
	SourceSlskd     Source = "slskd"
	SourceSpotiFLAC Source = "spotiflac"
	SourceOther     Source = "other"
	SourceManual    Source = "manual"
)

func (s Source) Valid() bool {
	switch s {
	case SourceSlskd, SourceSpotiFLAC, SourceOther, SourceManual:
		return true
	default:
		return false
	}
}

type Candidate struct {
	ID                    string
	Source                Source
	ReleaseDirectory      string
	ConfigSnapshotID      string
	AcquisitionEvidenceID string
	GatewayJobID          string
	State                 State
	StateRevision         uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type NewCandidate struct {
	ID                    string
	Source                Source
	ReleaseDirectory      string
	ConfigSnapshotID      string
	AcquisitionEvidenceID string
	GatewayJobID          string
	Now                   time.Time
}

func CreateCandidate(input NewCandidate) (Candidate, error) {
	if err := ValidateCandidateID(input.ID); err != nil {
		return Candidate{}, err
	}
	if strings.TrimSpace(input.ConfigSnapshotID) == "" || strings.TrimSpace(input.AcquisitionEvidenceID) == "" {
		return Candidate{}, fmt.Errorf("candidate ID, config snapshot, and acquisition evidence are required")
	}
	if !input.Source.Valid() {
		return Candidate{}, fmt.Errorf("unknown candidate source %q", input.Source)
	}
	if !filepath.IsAbs(input.ReleaseDirectory) || filepath.Clean(input.ReleaseDirectory) != input.ReleaseDirectory {
		return Candidate{}, fmt.Errorf("release directory must be an absolute canonical path")
	}
	if input.Now.IsZero() || input.Now.Location() != time.UTC {
		return Candidate{}, fmt.Errorf("candidate timestamp must be explicit UTC")
	}
	return Candidate{
		ID: input.ID, Source: input.Source, ReleaseDirectory: input.ReleaseDirectory,
		ConfigSnapshotID: input.ConfigSnapshotID, AcquisitionEvidenceID: input.AcquisitionEvidenceID,
		GatewayJobID: input.GatewayJobID, State: StateReceived, StateRevision: 0,
		CreatedAt: input.Now, UpdatedAt: input.Now,
	}, nil
}
