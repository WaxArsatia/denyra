package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type HandoffStore interface {
	RegisterPendingCandidate(context.Context, contracts.CandidateRegistered, string, string, []byte, time.Time) (int, []byte, error)
	AcceptCandidate(context.Context, domain.Candidate, contracts.CandidateAccepted, string, string, []byte, time.Time) (int, []byte, error)
	ApplyCandidateDirective(context.Context, string, string, string, string, string, uint64, domain.State, string, string, string, []byte, time.Time) (int, []byte, error)
}

func (s HandoffService) Register(ctx context.Context, key string, request contracts.CandidateRegistered) (int, []byte, error) {
	if strings.TrimSpace(key) == "" || request.RequestID == "" || request.JobID == "" || request.CandidateID == "" || request.ConfigSnapshotID == "" || request.SourceLocator == "" || request.RegisteredAt.IsZero() {
		return 0, nil, fmt.Errorf("pending registration identity is incomplete")
	}
	if s.Store == nil {
		return 0, nil, fmt.Errorf("handoff service is not configured")
	}
	if request.Source != contracts.SourceSlskd && request.Source != contracts.SourceSpotiFLAC {
		return 0, nil, fmt.Errorf("unsupported pending acquisition source")
	}
	if err := domain.ValidateCandidateID(request.CandidateID); err != nil {
		return 0, nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return 0, nil, err
	}
	hash := sha256.Sum256(payload)
	return s.Store.RegisterPendingCandidate(ctx, request, key, hex.EncodeToString(hash[:]), payload, s.now())
}

type HandoffService struct {
	Store                 HandoffStore
	LocalConfigSnapshotID string
	SourceRoots           map[domain.Source]string
	Now                   func() time.Time
	OnAccepted            func(string)
}

func (s HandoffService) Accept(ctx context.Context, key string, request contracts.CandidateAccepted) (int, []byte, error) {
	if strings.TrimSpace(key) == "" {
		return 0, nil, fmt.Errorf("idempotency key is required")
	}
	if s.Store == nil || s.LocalConfigSnapshotID == "" {
		return 0, nil, fmt.Errorf("handoff service is not configured")
	}
	source := map[contracts.AcquisitionSource]domain.Source{contracts.SourceSlskd: domain.SourceSlskd, contracts.SourceSpotiFLAC: domain.SourceSpotiFLAC, contracts.SourceManual: domain.SourceManual}[request.Source]
	if !source.Valid() {
		return 0, nil, fmt.Errorf("unsupported acquisition source")
	}
	root := filepath.Clean(s.SourceRoots[source])
	path := filepath.Clean(request.Path)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return 0, nil, fmt.Errorf("candidate path is outside authorized source root")
	}
	if request.RequestID == "" || request.JobID == "" || request.ConfigSnapshotID == "" || request.CompletionAt.IsZero() {
		return 0, nil, fmt.Errorf("candidate handoff identity is incomplete")
	}
	if _, err := domain.CanonicalMBID(request.MusicBrainzReleaseID); err != nil {
		return 0, nil, fmt.Errorf("explicit MusicBrainz release ID: %w", err)
	}
	if request.Provenance.Provider == "" {
		return 0, nil, fmt.Errorf("candidate provenance is incomplete")
	}
	if request.Provenance.OutputSHA256 == "" {
		if source != domain.SourceSlskd {
			return 0, nil, fmt.Errorf("candidate output checksum is required")
		}
	} else {
		decodedChecksum, decodeErr := hex.DecodeString(request.Provenance.OutputSHA256)
		if decodeErr != nil || len(decodedChecksum) != sha256.Size || request.Provenance.OutputSHA256 != strings.ToLower(request.Provenance.OutputSHA256) {
			return 0, nil, fmt.Errorf("candidate output checksum must be lowercase SHA-256")
		}
	}
	candidate, err := domain.CreateCandidate(domain.NewCandidate{ID: request.CandidateID, Source: source, ReleaseDirectory: path, ConfigSnapshotID: s.LocalConfigSnapshotID, AcquisitionEvidenceID: request.RequestID, GatewayJobID: request.JobID, Now: s.now()})
	if err != nil {
		return 0, nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return 0, nil, err
	}
	hash := sha256.Sum256(payload)
	status, body, err := s.Store.AcceptCandidate(ctx, candidate, request, key, hex.EncodeToString(hash[:]), payload, s.now())
	if err == nil && s.OnAccepted != nil {
		s.OnAccepted(candidate.ID)
	}
	return status, body, err
}

func (s HandoffService) Directive(ctx context.Context, key, operation, candidateID, jobID, configSnapshotID string, expected uint64, to domain.State, actor, reason, target string, payload []byte) (int, []byte, error) {
	if key == "" || operation == "" || candidateID == "" || jobID == "" || configSnapshotID == "" || strings.TrimSpace(reason) == "" {
		return 0, nil, fmt.Errorf("directive identity and reason are required")
	}
	hash := sha256.Sum256(payload)
	return s.Store.ApplyCandidateDirective(ctx, key, operation, candidateID, jobID, configSnapshotID, expected, to, actor, reason, target, []byte(hex.EncodeToString(hash[:])), s.now())
}
func (s HandoffService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
