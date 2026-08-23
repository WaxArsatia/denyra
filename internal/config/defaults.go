package config

import "time"

func Defaults() Config {
	return Config{
		HTTP: HTTPConfig{
			AdminAddress:           "0.0.0.0:8090",
			InternalAddress:        "172.30.0.3:8081",
			InternalBodyLimit:      1 << 20,
			AdminMutationLimit:     256 << 10,
			ExternalRequestTimeout: Duration(30 * time.Second),
			ExternalResponseLimit:  8 << 20,
		},
		Database: DatabaseConfig{
			GatewayPath:    "/data/state/gateway/denyra.db",
			PipelinePath:   "/data/state/pipeline/denyra.db",
			BusyTimeout:    Duration(5 * time.Second),
			MaxOpenConns:   4,
			IdempotencyTTL: Duration(30 * 24 * time.Hour),
		},
		Filesystem: FilesystemConfig{
			DataRoot:           "/data",
			DownloadsSlskd:     "/data/downloads/slskd",
			DownloadsSpotiFLAC: "/data/downloads/spotiflac",
			DownloadsOther:     "/data/downloads/other",
			IncomingManual:     "/data/incoming/manual",
			Work:               "/data/processing/work",
			Approved:           "/data/processing/approved",
			Quarantine:         "/data/quarantine",
			Library:            "/data/library",
		},
		Acquisition: AcquisitionConfig{
			LidarrPageSize:        100,
			AlbumSearchTimeout:    Duration(10 * time.Minute),
			ReconciliationPoll:    Duration(2 * time.Second),
			ReconciliationSafety:  Duration(30 * time.Second),
			PrimaryGraceWindow:    Duration(time.Minute),
			ProviderTimeout:       Duration(3 * time.Minute),
			OverallTimeout:        Duration(6 * time.Hour),
			ProcessPollInterval:   Duration(250 * time.Millisecond),
			ProcessTerminateGrace: Duration(5 * time.Second),
			ProcessOutputLimit:    4 << 20,
			NoCandidateRetry:      Duration(24 * time.Hour),
			PrimaryRetry:          []Duration{Duration(time.Minute), Duration(5 * time.Minute), Duration(15 * time.Minute), Duration(time.Hour), Duration(6 * time.Hour)},
			FallbackRetry:         []Duration{Duration(5 * time.Minute), Duration(15 * time.Minute), Duration(time.Hour), Duration(6 * time.Hour)},
		},
		Validation: ValidationConfig{
			TrackAutoFloorMS: 5_000, TrackAutoPercentBasisPoints: 200,
			TrackManualFloorMS: 15_000, TrackManualPercentBasisPoints: 500,
			ReleaseAutoFloorMS: 30_000, ReleaseAutoPercentBasisPoints: 100,
			ReleaseManualFloorMS: 90_000, ReleaseManualPercentBasisPoints: 300,
		},
		Arbitration: ArbitrationConfig{Window: Duration(30 * time.Minute)},
		Sessions: SessionConfig{
			AbsoluteExpiry:    Duration(30 * 24 * time.Hour),
			PasswordMinLen:    8,
			BootstrapUsername: "admin",
		},
		Scanners: ScannerConfig{
			RecoveryInterval:  Duration(30 * time.Second),
			StabilityInterval: Duration(10 * time.Second),
			NavidromeSchedule: Duration(time.Minute),
			NavidromeWatcher:  Duration(5 * time.Second),
		},
		Storage: StorageConfig{
			MinimumFreeBytes:   20 * 1024 * 1024 * 1024,
			MinimumFreePercent: 5,
		},
		Backup:      BackupConfig{Daily: 7, Weekly: 4, Monthly: 12},
		Concurrency: ConcurrencyConfig{Acquisition: 2, Validation: 2, Import: 1},
		Services:    ServicesConfig{LidarrURL: "http://lidarr:8686", PipelineURL: "http://media-pipeline:8081", MusicBrainzURL: "https://musicbrainz.org", LRCLIBURL: "https://lrclib.net"},
		Secrets: SecretsConfig{
			InternalBearer: SecretRef{Source: "file", Name: "internal-bearer"},
			AuditKey:       SecretRef{Source: "file", Name: "audit-key"},
			LidarrAPIKey:   SecretRef{Source: "file", Name: "lidarr-api-key"},
			BootstrapAdmin: SecretRef{Source: "file", Name: "bootstrap-admin"},
		},
	}
}
