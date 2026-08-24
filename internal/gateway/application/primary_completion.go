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
	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type PrimaryCompletionQueue interface {
	QueueRecords(context.Context, int) ([]lidarr.QueueRecord, error)
}

type PrimaryCompletionHandoff interface {
	AcceptCompleted(context.Context, persistence.Candidate, contracts.AcquisitionProvenance) error
}

type PrimaryCompletionStore interface {
	IncompletePendingCandidatesBySource(context.Context, string) ([]persistence.PendingCandidate, error)
	CandidatesWithoutEffect(context.Context, string, string) ([]persistence.Candidate, error)
	InsertCandidate(context.Context, persistence.Candidate) error
}

type PrimaryCompletionService struct {
	Queue         PrimaryCompletionQueue
	Store         PrimaryCompletionStore
	Handoff       PrimaryCompletionHandoff
	DownloadsRoot string
	PageSize      int
	EngineVersion string
	Now           func() time.Time
}

func (service PrimaryCompletionService) Reconcile(ctx context.Context) (int, error) {
	if service.Queue == nil || service.Store == nil || service.Handoff == nil || service.PageSize <= 0 || service.EngineVersion == "" {
		return 0, fmt.Errorf("primary completion service is not configured")
	}
	root := filepath.Clean(service.DownloadsRoot)
	if !filepath.IsAbs(root) {
		return 0, fmt.Errorf("slskd downloads root must be absolute")
	}
	completed := 0
	orphans, err := service.Store.CandidatesWithoutEffect(ctx, "slskd", "PIPELINE_ACCEPT")
	if err != nil {
		return 0, err
	}
	for _, candidate := range orphans {
		if _, err := primaryCompletedPath(root, candidate.DownloadID, candidate.SourceLocator); err != nil {
			return completed, err
		}
		provenance := contracts.AcquisitionProvenance{Provider: "slskd", EngineVersion: service.EngineVersion, DownloadID: candidate.DownloadID, ObservedStatus: "completed"}
		if err := service.Handoff.AcceptCompleted(ctx, candidate, provenance); err != nil {
			return completed, err
		}
		completed++
	}
	pending, err := service.Store.IncompletePendingCandidatesBySource(ctx, "slskd")
	if err != nil || len(pending) == 0 {
		return completed, err
	}
	records, err := service.Queue.QueueRecords(ctx, service.PageSize)
	if err != nil {
		return completed, err
	}
	byDownloadID := make(map[string]lidarr.QueueRecord, len(records))
	for _, record := range records {
		if record.DownloadID != "" {
			byDownloadID[record.DownloadID] = record
		}
	}
	for _, candidate := range pending {
		record, found := byDownloadID[candidate.DownloadID]
		if !found || !healthyCompletedQueueRecord(record) {
			continue
		}
		path, err := primaryCompletedPath(root, candidate.DownloadID, record.OutputPath)
		if err != nil {
			return completed, err
		}
		completedAt := service.now()
		provenance := contracts.AcquisitionProvenance{Provider: "slskd", EngineVersion: service.EngineVersion, DownloadID: candidate.DownloadID, ObservedStatus: strings.ToLower(strings.TrimSpace(record.Status))}
		evidence, err := json.Marshal(struct {
			PendingProvenance json.RawMessage    `json:"pending_provenance"`
			Queue             lidarr.QueueRecord `json:"lidarr_queue"`
			ObservedAt        time.Time          `json:"observed_at"`
		}{PendingProvenance: candidate.Provenance, Queue: record, ObservedAt: completedAt})
		if err != nil {
			return completed, err
		}
		sum := sha256.Sum256(evidence)
		stored := persistence.Candidate{ID: candidate.ID, JobID: candidate.JobID, Source: candidate.Source, SourceLocator: path, DownloadID: candidate.DownloadID, CompletedAt: &completedAt, Provenance: evidence, ProvenanceSHA256: hex.EncodeToString(sum[:]), CreatedAt: completedAt}
		if err := service.Store.InsertCandidate(ctx, stored); err != nil {
			return completed, err
		}
		if err := service.Handoff.AcceptCompleted(ctx, stored, provenance); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

func healthyCompletedQueueRecord(record lidarr.QueueRecord) bool {
	return strings.EqualFold(strings.TrimSpace(record.Status), "completed") &&
		(strings.TrimSpace(record.TrackedDownloadStatus) == "" || strings.EqualFold(strings.TrimSpace(record.TrackedDownloadStatus), "ok")) &&
		strings.TrimSpace(record.ErrorMessage) == ""
}

func primaryCompletedPath(root, downloadID, observed string) (string, error) {
	if strings.TrimSpace(downloadID) == "" || filepath.Base(downloadID) != downloadID || downloadID == "." || downloadID == ".." {
		return "", fmt.Errorf("invalid primary download ID")
	}
	expected := filepath.Join(root, "lidarr", downloadID)
	path := filepath.Clean(observed)
	if !filepath.IsAbs(path) || path != expected {
		return "", fmt.Errorf("Lidarr primary output path %q does not match authorized path %q", observed, expected)
	}
	return path, nil
}

func (service PrimaryCompletionService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}
