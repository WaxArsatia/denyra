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
	AcceptCandidate(context.Context, domain.Candidate, contracts.CandidateAccepted, string, string, []byte, time.Time) (int, []byte, error)
	ApplyCandidateDirective(context.Context, string, string, string, uint64, domain.State, string, string, string, []byte, time.Time) (int, []byte, error)
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
	if request.Provenance.Provider == "" || len(request.Provenance.OutputSHA256) != 64 {
		return 0, nil, fmt.Errorf("candidate provenance is incomplete")
	}
	decodedChecksum, decodeErr := hex.DecodeString(request.Provenance.OutputSHA256)
	if decodeErr != nil || len(decodedChecksum) != sha256.Size || request.Provenance.OutputSHA256 != strings.ToLower(request.Provenance.OutputSHA256) {
		return 0, nil, fmt.Errorf("candidate output checksum must be lowercase SHA-256")
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

func (s HandoffService) Directive(ctx context.Context, key, operation, candidateID string, expected uint64, to domain.State, actor, reason, target string, payload []byte) (int, []byte, error) {
	if key == "" || operation == "" || candidateID == "" || strings.TrimSpace(reason) == "" {
		return 0, nil, fmt.Errorf("directive identity and reason are required")
	}
	hash := sha256.Sum256(payload)
	return s.Store.ApplyCandidateDirective(ctx, key, operation, candidateID, expected, to, actor, reason, target, []byte(hex.EncodeToString(hash[:])), s.now())
}
func (s HandoffService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
