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

type metadataField struct {
	Name  string `json:"name"`
	Value bool   `json:"value"`
}

type metadataConsumer struct {
	Enable         bool            `json:"enable"`
	Implementation string          `json:"implementation"`
	Fields         []metadataField `json:"fields"`
}

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
	if !mediaConfig.ImportExtraFiles ||
		!containsExtension(mediaConfig.ExtraFileExtensions, "lrc") ||
		!containsExtension(mediaConfig.ExtraFileExtensions, "elrc") ||
		!containsExtension(mediaConfig.ExtraFileExtensions, "ttml") {
		return fmt.Errorf("Lidarr Import Extra Files must include lrc, elrc, and ttml")
	}
	var naming struct {
		RenameTracks         bool   `json:"renameTracks"`
		StandardTrackFormat  string `json:"standardTrackFormat"`
		MultiDiscTrackFormat string `json:"multiDiscTrackFormat"`
	}
	if err := v.Client.Get(ctx, "/api/v1/config/naming", nil, &naming); err != nil {
		return err
	}
	if !naming.RenameTracks {
		return fmt.Errorf("Lidarr Rename Tracks must be enabled")
	}
	if !hasAlbumDirectory(naming.StandardTrackFormat) {
		return fmt.Errorf("Lidarr Standard Track Format must create an album directory")
	}
	if !hasAlbumDirectory(naming.MultiDiscTrackFormat) {
		return fmt.Errorf("Lidarr Multi Disc Track Format must create an album directory")
	}
	var metadata []metadataConsumer
	if err := v.Client.Get(ctx, "/api/v1/metadata", nil, &metadata); err != nil {
		return err
	}
	var artworkConsumer *metadataConsumer
	for index := range metadata {
		if strings.EqualFold(metadata[index].Implementation, "XbmcMetadata") {
			artworkConsumer = &metadata[index]
			break
		}
	}
	if artworkConsumer == nil || !artworkConsumer.Enable {
		return fmt.Errorf("Lidarr Kodi (XBMC) / Emby metadata consumer must be enabled")
	}
	if !hasEnabledMetadataField(artworkConsumer.Fields, "artistImages") {
		return fmt.Errorf("Lidarr Kodi (XBMC) / Emby Artist Images must be enabled")
	}
	if !hasEnabledMetadataField(artworkConsumer.Fields, "albumImages") {
		return fmt.Errorf("Lidarr Kodi (XBMC) / Emby Album Images must be enabled")
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

func hasAlbumDirectory(format string) bool {
	parts := strings.FieldsFunc(format, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 2 {
		return false
	}
	albumDirectory := strings.ToLower(parts[0])
	return strings.Contains(albumDirectory, "{album title") || strings.Contains(albumDirectory, "{album cleantitle")
}

func hasEnabledMetadataField(fields []metadataField, expected string) bool {
	for _, field := range fields {
		if strings.EqualFold(field.Name, expected) {
			return field.Value
		}
	}
	return false
}

func contained(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
