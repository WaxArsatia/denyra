package lidarr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type ConfigVerifier struct{ Client Client }

func (v ConfigVerifier) Verify(ctx context.Context) error {
	var download struct {
		EnableCompletedDownloadHandling bool `json:"enableCompletedDownloadHandling"`
	}
	if err := v.Client.Get(ctx, "/api/v1/config/downloadclient", nil, &download); err != nil {
		return err
	}
	if download.EnableCompletedDownloadHandling {
		return fmt.Errorf("Lidarr Completed Download Handling must be disabled")
	}
	var mediaConfig struct {
		ImportExtraFiles    bool   `json:"importExtraFiles"`
		ExtraFileExtensions string `json:"extraFileExtensions"`
	}
	if err := v.Client.Get(ctx, "/api/v1/config/mediamanagement", nil, &mediaConfig); err != nil {
		return err
	}
	if !mediaConfig.ImportExtraFiles || !containsExtension(mediaConfig.ExtraFileExtensions, "lrc") {
		return fmt.Errorf("Lidarr Import Extra Files must include lrc")
	}
	return nil
}

type LibraryVerifier struct {
	Client      Client
	LibraryRoot string
}

func (v LibraryVerifier) Verify(ctx context.Context, plan domain.LidarrImportPlan, manifest domain.ReleaseManifest) (domain.ImportVerification, error) {
	verification := domain.ImportVerification{ReconciliationHashes: make(map[string]string)}
	probes := []struct {
		name  string
		path  string
		query url.Values
	}{
		{name: "commands", path: "/api/v1/command"},
		{name: "history", path: "/api/v1/history", query: url.Values{"page": {"1"}, "pageSize": {"100"}, "sortKey": {"date"}, "sortDirection": {"descending"}}},
		{name: "queue", path: "/api/v1/queue", query: url.Values{"page": {"1"}, "pageSize": {"100"}}},
		{name: "album", path: fmt.Sprintf("/api/v1/album/%d", plan.AlbumID)},
	}
	for _, probe := range probes {
		var raw json.RawMessage
		if err := v.Client.Get(ctx, probe.path, probe.query, &raw); err != nil {
			return verification, err
		}
		hash := sha256.Sum256(raw)
		verification.ReconciliationHashes[probe.name] = hex.EncodeToString(hash[:])
	}
	var trackFiles []struct {
		ID       int    `json:"id"`
		Path     string `json:"path"`
		TrackIDs []int  `json:"trackIds"`
	}
	query := url.Values{"albumId": {fmt.Sprintf("%d", plan.AlbumID)}}
	if err := v.Client.Get(ctx, "/api/v1/trackfile", query, &trackFiles); err != nil {
		return domain.ImportVerification{}, err
	}
	expectedAudio := make(map[string]int)
	expectedAudioCount := 0
	expectedLyrics := 0
	for _, file := range manifest.Files {
		switch file.Kind {
		case "FLAC":
			expectedAudio[file.SHA256]++
			expectedAudioCount++
		case "LRC":
			expectedLyrics++
		}
	}
	for _, trackFile := range trackFiles {
		if !contained(v.LibraryRoot, trackFile.Path) {
			continue
		}
		checksum, err := media.SHA256(trackFile.Path)
		if err != nil {
			return verification, err
		}
		if expectedAudio[checksum] == 0 {
			continue
		}
		expectedAudio[checksum]--
		expectedAudioCount--
		final := domain.FinalFile{Path: trackFile.Path, SHA256: checksum}
		if len(trackFile.TrackIDs) > 0 {
			final.TrackID = trackFile.TrackIDs[0]
		}
		sidecar := strings.TrimSuffix(trackFile.Path, filepath.Ext(trackFile.Path)) + ".lrc"
		if _, err := os.Stat(sidecar); err == nil {
			final.SidecarPath = sidecar
			expectedLyrics--
		}
		verification.Files = append(verification.Files, final)
	}
	if expectedAudioCount != 0 || expectedLyrics != 0 {
		verification.Reason = fmt.Sprintf("final verification incomplete: audio=%d lyrics=%d", expectedAudioCount, expectedLyrics)
		return verification, nil
	}
	verification.Complete = true
	return verification, nil
}

func containsExtension(value, expected string) bool {
	for _, extension := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(extension), "."), expected) {
			return true
		}
	}
	return false
}

func contained(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
