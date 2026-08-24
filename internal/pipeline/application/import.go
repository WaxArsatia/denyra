package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type ImportAuthorization string

const (
	ImportManualApproved ImportAuthorization = "MANUAL_APPROVED"
	ImportGatewayWinner  ImportAuthorization = "GATEWAY_WINNER"
)

type ImportIntentStore interface {
	PutImportIntent(context.Context, domain.ImportIntent, time.Time) error
	MarkImportStatus(context.Context, string, string, []byte, time.Time) error
}

type ImportConfiguration interface{ Verify(context.Context) error }
type ImportPreparer interface {
	Prepare(context.Context, string, string, string, int) (domain.LidarrImportPlan, error)
	Submit(context.Context, domain.LidarrImportPlan) error
}
type ImportFinalVerifier interface {
	Verify(context.Context, domain.LidarrImportPlan, domain.ReleaseManifest) (domain.ImportVerification, error)
}

type ImportService struct {
	WorkRoot      string
	ApprovedRoot  string
	Configuration ImportConfiguration
	Importer      ImportPreparer
	Verifier      ImportFinalVerifier
	Store         ImportIntentStore
	Move          func(string, string) error
	RemoveAll     func(string) error
	Now           func() time.Time
}

type ImportSubmission struct {
	Intent            domain.ImportIntent
	ApprovedPath      string
	ReconcileRequired bool
}

func (s ImportService) Submit(ctx context.Context, candidateID, releaseMBID, downloadID string, authorization ImportAuthorization, catalogAlbumReleaseID int) (ImportSubmission, error) {
	if authorization != ImportManualApproved && authorization != ImportGatewayWinner {
		return ImportSubmission{}, fmt.Errorf("candidate lacks manual approval or gateway winner lock")
	}
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return ImportSubmission{}, err
	}
	if _, err := domain.CanonicalMBID(releaseMBID); err != nil {
		return ImportSubmission{}, err
	}
	if catalogAlbumReleaseID <= 0 {
		return ImportSubmission{}, fmt.Errorf("catalog album release ID is required")
	}
	if s.Configuration == nil || s.Importer == nil || s.Store == nil {
		return ImportSubmission{}, fmt.Errorf("import service is not configured")
	}
	if err := s.Configuration.Verify(ctx); err != nil {
		return ImportSubmission{}, fmt.Errorf("Lidarr import configuration drift: %w", err)
	}
	workPath, approvedPath := filepath.Join(s.WorkRoot, candidateID), filepath.Join(s.ApprovedRoot, candidateID)
	sourcePath := workPath
	if _, err := os.Stat(workPath); os.IsNotExist(err) {
		if info, approvedErr := os.Stat(approvedPath); approvedErr == nil && info.IsDir() {
			sourcePath = approvedPath
		}
	}
	manifest, err := buildReleaseManifest(candidateID, releaseMBID, sourcePath)
	if err != nil {
		return ImportSubmission{}, err
	}
	if err := os.MkdirAll(s.ApprovedRoot, 0o750); err != nil {
		return ImportSubmission{}, err
	}
	move := s.Move
	if move == nil {
		move = denyrafs.MoveAtomic
	}
	if sourcePath == workPath {
		if err := move(workPath, approvedPath); err != nil {
			return ImportSubmission{}, err
		}
	}
	flacCount := 0
	for _, file := range manifest.Files {
		if file.Kind == "FLAC" {
			flacCount++
		}
	}
	plan, err := s.Importer.Prepare(ctx, approvedPath, releaseMBID, downloadID, flacCount)
	if err != nil {
		return ImportSubmission{ApprovedPath: approvedPath}, err
	}
	if plan.AlbumReleaseID != catalogAlbumReleaseID {
		return ImportSubmission{ApprovedPath: approvedPath}, fmt.Errorf("catalog album release ID %d does not match Manual Import album release ID %d", catalogAlbumReleaseID, plan.AlbumReleaseID)
	}
	requestHash := sha256.Sum256(plan.RequestBody)
	intentID, err := ids.NewToken(16)
	if err != nil {
		return ImportSubmission{}, err
	}
	intent := domain.ImportIntent{ID: intentID, IdempotencyKey: "lidarr-import-" + candidateID, CandidateID: candidateID,
		TargetReleaseMBID: releaseMBID, RequestHash: hex.EncodeToString(requestHash[:]), Manifest: manifest, Plan: plan, DownloadID: downloadID}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if err := s.Store.PutImportIntent(ctx, intent, now().UTC()); err != nil {
		return ImportSubmission{Intent: intent, ApprovedPath: approvedPath}, err
	}
	if err := s.Importer.Submit(ctx, plan); err != nil {
		_ = s.Store.MarkImportStatus(ctx, intent.ID, "IMPORT_RECONCILING", []byte(err.Error()), now().UTC())
		return ImportSubmission{Intent: intent, ApprovedPath: approvedPath, ReconcileRequired: true}, nil
	}
	if err := s.Store.MarkImportStatus(ctx, intent.ID, "IMPORT_SUBMITTED", nil, now().UTC()); err != nil {
		return ImportSubmission{Intent: intent, ApprovedPath: approvedPath, ReconcileRequired: true}, err
	}
	return ImportSubmission{Intent: intent, ApprovedPath: approvedPath, ReconcileRequired: true}, nil
}

func buildReleaseManifest(candidateID, releaseMBID, root string) (domain.ReleaseManifest, error) {
	manifest := domain.ReleaseManifest{CandidateID: candidateID, ReleaseMBID: releaseMBID}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("manifest contains symlink %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("manifest contains non-regular file %s", path)
		}
		extension := strings.ToLower(filepath.Ext(path))
		kind := ""
		switch extension {
		case ".flac":
			kind = "FLAC"
		case ".lrc":
			kind = "LRC"
		default:
			return nil
		}
		checksum, err := media.SHA256(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, domain.ManifestFile{RelativePath: filepath.ToSlash(relative), SHA256: checksum, Kind: kind})
		return nil
	})
	if err != nil {
		return manifest, err
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].RelativePath < manifest.Files[j].RelativePath })
	if len(manifest.Files) == 0 {
		return manifest, fmt.Errorf("release manifest is empty")
	}
	return manifest, nil
}

func MarshalImportEvidence(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
