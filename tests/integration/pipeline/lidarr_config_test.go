package pipeline_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lidarr"
)

const (
	validDownloadConfig = `{
		"downloadClientWorkingFolders":"_UNPACK_|_FAILED_",
		"enableCompletedDownloadHandling":false,
		"autoRedownloadFailed":true,
		"autoRedownloadFailedFromInteractiveSearch":true,
		"id":1
	}`
	validMediaManagementConfig = `{
		"autoUnmonitorPreviouslyDownloadedTracks":false,
		"recycleBin":"",
		"recycleBinCleanupDays":7,
		"downloadPropersAndRepacks":"preferAndUpgrade",
		"createEmptyArtistFolders":false,
		"deleteEmptyFolders":false,
		"fileDate":"none",
		"watchLibraryForChanges":true,
		"rescanAfterRefresh":"always",
		"allowFingerprinting":"newFiles",
		"setPermissionsLinux":false,
		"chmodFolder":"755",
		"chownGroup":"",
		"skipFreeSpaceCheckWhenImporting":false,
		"minimumFreeSpaceWhenImporting":100,
		"copyUsingHardlinks":true,
		"enableMediaInfo":true,
		"useScriptImport":false,
		"scriptImportPath":"",
		"importExtraFiles":true,
		"extraFileExtensions":"lrc,elrc,ttml",
		"id":1
	}`
	validNamingConfig = `{
		"renameTracks":true,
		"replaceIllegalCharacters":true,
		"colonReplacementFormat":4,
		"standardTrackFormat":"{Album Title} ({Release Year})/{Artist Name} - {Album Title} - {track:00} - {Track Title}",
		"multiDiscTrackFormat":"{Album Title} ({Release Year})/{Medium Format} {medium:00}/{Artist Name} - {Album Title} - {track:00} - {Track Title}",
		"artistFolderFormat":"{Artist Name}",
		"includeArtistName":false,
		"includeAlbumTitle":false,
		"includeQuality":false,
		"replaceSpaces":false,
		"id":1
	}`
	validMetadataConfig = `[{
		"enable":true,
		"name":"Kodi (XBMC) / Emby",
		"fields":[
			{"order":0,"name":"artistMetadata","label":"Artist Metadata","value":false,"type":"checkbox","advanced":false},
			{"order":1,"name":"albumMetadata","label":"Album Metadata","value":false,"type":"checkbox","advanced":false},
			{"order":2,"name":"artistImages","label":"Artist Images","value":true,"type":"checkbox","advanced":false},
			{"order":3,"name":"albumImages","label":"Album Images","value":true,"type":"checkbox","advanced":false}
		],
		"implementationName":"Kodi (XBMC) / Emby",
		"implementation":"XbmcMetadata",
		"configContract":"XbmcMetadataSettings",
		"infoLink":"https://wiki.servarr.com/lidarr/supported#xbmcmetadata",
		"tags":[],
		"id":1
	}]`
)

func TestLidarrConfigVerifierAcceptsProductionContract(t *testing.T) {
	verifier, closeServer := newLidarrConfigVerifier(t, validMediaManagementConfig, validNamingConfig, validMetadataConfig)
	defer closeServer()

	if err := verifier.Verify(context.Background()); err != nil {
		t.Fatalf("valid production configuration rejected: %v", err)
	}
}

func TestLidarrConfigVerifierRejectsLayoutAndArtworkDrift(t *testing.T) {
	tests := []struct {
		name          string
		mediaConfig   string
		namingConfig  string
		metadata      string
		errorContains string
	}{
		{
			name:          "missing enhanced lyric extensions would drop sidecars",
			mediaConfig:   strings.Replace(validMediaManagementConfig, "lrc,elrc,ttml", "lrc", 1),
			namingConfig:  validNamingConfig,
			metadata:      validMetadataConfig,
			errorContains: "lrc, elrc, and ttml",
		},
		{
			name:          "disabled rename would flatten imported albums",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  strings.Replace(validNamingConfig, `"renameTracks":true`, `"renameTracks":false`, 1),
			metadata:      validMetadataConfig,
			errorContains: "Rename Tracks",
		},
		{
			name:          "standard format without album directory would flatten albums",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  strings.Replace(validNamingConfig, `{Album Title} ({Release Year})/{Artist Name}`, `{Artist Name}`, 1),
			metadata:      validMetadataConfig,
			errorContains: "Standard Track Format",
		},
		{
			name:          "multi disc format without album directory would flatten albums",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  strings.Replace(validNamingConfig, `{Album Title} ({Release Year})/{Medium Format}`, `{Medium Format}`, 1),
			metadata:      validMetadataConfig,
			errorContains: "Multi Disc Track Format",
		},
		{
			name:          "disabled metadata consumer would prevent cover downloads",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  validNamingConfig,
			metadata:      strings.Replace(validMetadataConfig, `"enable":true`, `"enable":false`, 1),
			errorContains: "Kodi (XBMC) / Emby",
		},
		{
			name:          "disabled artist images would leave artist art empty",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  validNamingConfig,
			metadata:      strings.Replace(validMetadataConfig, `"name":"artistImages","label":"Artist Images","value":true`, `"name":"artistImages","label":"Artist Images","value":false`, 1),
			errorContains: "Artist Images",
		},
		{
			name:          "disabled album images would leave album covers empty",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  validNamingConfig,
			metadata:      strings.Replace(validMetadataConfig, `"name":"albumImages","label":"Album Images","value":true`, `"name":"albumImages","label":"Album Images","value":false`, 1),
			errorContains: "Album Images",
		},
		{
			name:          "missing metadata consumer would prevent all artwork downloads",
			mediaConfig:   validMediaManagementConfig,
			namingConfig:  validNamingConfig,
			metadata:      `[]`,
			errorContains: "Kodi (XBMC) / Emby",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier, closeServer := newLidarrConfigVerifier(t, test.mediaConfig, test.namingConfig, test.metadata)
			defer closeServer()

			err := verifier.Verify(context.Background())
			if err == nil {
				t.Fatal("production-breaking configuration drift accepted")
			}
			if !strings.Contains(err.Error(), test.errorContains) {
				t.Fatalf("configuration error = %q, want fragment %q", err, test.errorContains)
			}
		})
	}
}

func newLidarrConfigVerifier(t *testing.T, mediaConfig, namingConfig, metadataConfig string) (lidarr.ConfigVerifier, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "lidarr-key" {
			t.Error("missing Lidarr API key")
		}
		var response string
		switch request.URL.Path {
		case "/api/v1/config/downloadclient":
			response = validDownloadConfig
		case "/api/v1/config/mediamanagement":
			response = mediaConfig
		case "/api/v1/config/naming":
			response = namingConfig
		case "/api/v1/metadata":
			response = metadataConfig
		default:
			http.Error(writer, "unexpected "+request.Method+" "+request.URL.String(), http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(response))
	}))
	client := lidarr.Client{BaseURL: server.URL, APIKey: "lidarr-key", HTTP: server.Client()}
	return lidarr.ConfigVerifier{Client: client}, server.Close
}
