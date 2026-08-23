// Package config loads and validates Denyra's centralized immutable policy.
package config

import (
	"encoding/json"
	"fmt"
	"time"
)

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", text, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

type Config struct {
	HTTP        HTTPConfig        `toml:"http" json:"http"`
	Database    DatabaseConfig    `toml:"database" json:"database"`
	Filesystem  FilesystemConfig  `toml:"filesystem" json:"filesystem"`
	Acquisition AcquisitionConfig `toml:"acquisition" json:"acquisition"`
	Validation  ValidationConfig  `toml:"validation" json:"validation"`
	Arbitration ArbitrationConfig `toml:"arbitration" json:"arbitration"`
	Sessions    SessionConfig     `toml:"sessions" json:"sessions"`
	Scanners    ScannerConfig     `toml:"scanners" json:"scanners"`
	Storage     StorageConfig     `toml:"storage" json:"storage"`
	Backup      BackupConfig      `toml:"backup" json:"backup"`
	Concurrency ConcurrencyConfig `toml:"concurrency" json:"concurrency"`
	Services    ServicesConfig    `toml:"services" json:"services"`
	Secrets     SecretsConfig     `toml:"secrets" json:"secrets"`
}

type HTTPConfig struct {
	AdminAddress           string   `toml:"admin_address" json:"admin_address"`
	InternalAddress        string   `toml:"internal_address" json:"internal_address"`
	InternalBodyLimit      int64    `toml:"internal_body_limit" json:"internal_body_limit"`
	AdminMutationLimit     int64    `toml:"admin_mutation_limit" json:"admin_mutation_limit"`
	ExternalRequestTimeout Duration `toml:"external_request_timeout" json:"external_request_timeout"`
	ExternalResponseLimit  int64    `toml:"external_response_limit" json:"external_response_limit"`
	InternalReplayAttempts int      `toml:"internal_replay_attempts" json:"internal_replay_attempts"`
	ReadHeaderTimeout      Duration `toml:"read_header_timeout" json:"read_header_timeout"`
	ServerIdleTimeout      Duration `toml:"server_idle_timeout" json:"server_idle_timeout"`
	ShutdownTimeout        Duration `toml:"shutdown_timeout" json:"shutdown_timeout"`
	HealthcheckTimeout     Duration `toml:"healthcheck_timeout" json:"healthcheck_timeout"`
}

type ServicesConfig struct {
	LidarrURL      string `toml:"lidarr_url" json:"lidarr_url"`
	GatewayURL     string `toml:"gateway_url" json:"gateway_url"`
	PipelineURL    string `toml:"pipeline_url" json:"pipeline_url"`
	MusicBrainzURL string `toml:"musicbrainz_url" json:"musicbrainz_url"`
	LRCLIBURL      string `toml:"lrclib_url" json:"lrclib_url"`
}

type DatabaseConfig struct {
	GatewayPath    string   `toml:"gateway_path" json:"gateway_path"`
	PipelinePath   string   `toml:"pipeline_path" json:"pipeline_path"`
	BusyTimeout    Duration `toml:"busy_timeout" json:"busy_timeout"`
	MaxOpenConns   int      `toml:"max_open_conns" json:"max_open_conns"`
	IdempotencyTTL Duration `toml:"idempotency_ttl" json:"idempotency_ttl"`
}

type FilesystemConfig struct {
	DataRoot           string `toml:"data_root" json:"data_root"`
	DownloadsSlskd     string `toml:"downloads_slskd" json:"downloads_slskd"`
	DownloadsSpotiFLAC string `toml:"downloads_spotiflac" json:"downloads_spotiflac"`
	DownloadsOther     string `toml:"downloads_other" json:"downloads_other"`
	IncomingManual     string `toml:"incoming_manual" json:"incoming_manual"`
	Work               string `toml:"work" json:"work"`
	Approved           string `toml:"approved" json:"approved"`
	Quarantine         string `toml:"quarantine" json:"quarantine"`
	Library            string `toml:"library" json:"library"`
}

type AcquisitionConfig struct {
	LidarrPageSize        int        `toml:"lidarr_page_size" json:"lidarr_page_size"`
	AlbumSearchTimeout    Duration   `toml:"album_search_timeout" json:"album_search_timeout"`
	ReconciliationPoll    Duration   `toml:"reconciliation_poll" json:"reconciliation_poll"`
	ReconciliationSafety  Duration   `toml:"reconciliation_safety" json:"reconciliation_safety"`
	PrimaryGraceWindow    Duration   `toml:"primary_grace_window" json:"primary_grace_window"`
	ProviderTimeout       Duration   `toml:"provider_timeout" json:"provider_timeout"`
	OverallTimeout        Duration   `toml:"overall_timeout" json:"overall_timeout"`
	ProcessPollInterval   Duration   `toml:"process_poll_interval" json:"process_poll_interval"`
	ProcessTerminateGrace Duration   `toml:"process_terminate_grace" json:"process_terminate_grace"`
	LeaseDuration         Duration   `toml:"lease_duration" json:"lease_duration"`
	ProcessOutputLimit    int64      `toml:"process_output_limit" json:"process_output_limit"`
	MaxInlineTransitions  int        `toml:"max_inline_transitions" json:"max_inline_transitions"`
	NoCandidateRetry      Duration   `toml:"no_candidate_retry" json:"no_candidate_retry"`
	PrimaryRetry          []Duration `toml:"primary_retry" json:"primary_retry"`
	FallbackRetry         []Duration `toml:"fallback_retry" json:"fallback_retry"`
}

type ValidationConfig struct {
	FFProbeTimeout                  Duration `toml:"ffprobe_timeout" json:"ffprobe_timeout"`
	FLACTestTimeout                 Duration `toml:"flac_test_timeout" json:"flac_test_timeout"`
	MetaFLACTimeout                 Duration `toml:"metaflac_timeout" json:"metaflac_timeout"`
	BeetsTimeout                    Duration `toml:"beets_timeout" json:"beets_timeout"`
	MusicBrainzRateInterval         Duration `toml:"musicbrainz_rate_interval" json:"musicbrainz_rate_interval"`
	TrackAutoFloorMS                int64    `toml:"track_auto_floor_ms" json:"track_auto_floor_ms"`
	TrackAutoPercentBasisPoints     int64    `toml:"track_auto_percent_basis_points" json:"track_auto_percent_basis_points"`
	TrackManualFloorMS              int64    `toml:"track_manual_floor_ms" json:"track_manual_floor_ms"`
	TrackManualPercentBasisPoints   int64    `toml:"track_manual_percent_basis_points" json:"track_manual_percent_basis_points"`
	ReleaseAutoFloorMS              int64    `toml:"release_auto_floor_ms" json:"release_auto_floor_ms"`
	ReleaseAutoPercentBasisPoints   int64    `toml:"release_auto_percent_basis_points" json:"release_auto_percent_basis_points"`
	ReleaseManualFloorMS            int64    `toml:"release_manual_floor_ms" json:"release_manual_floor_ms"`
	ReleaseManualPercentBasisPoints int64    `toml:"release_manual_percent_basis_points" json:"release_manual_percent_basis_points"`
}

type ArbitrationConfig struct {
	Window Duration `toml:"window" json:"window"`
}

type SessionConfig struct {
	AbsoluteExpiry    Duration `toml:"absolute_expiry" json:"absolute_expiry"`
	PasswordMinLen    int      `toml:"password_min_length" json:"password_min_length"`
	BootstrapUsername string   `toml:"bootstrap_username" json:"bootstrap_username"`
}

type ScannerConfig struct {
	RecoveryInterval  Duration `toml:"recovery_interval" json:"recovery_interval"`
	StabilityInterval Duration `toml:"stability_interval" json:"stability_interval"`
	NavidromeSchedule Duration `toml:"navidrome_schedule" json:"navidrome_schedule"`
	NavidromeWatcher  Duration `toml:"navidrome_watcher" json:"navidrome_watcher"`
}

type StorageConfig struct {
	MinimumFreeBytes   int64   `toml:"minimum_free_bytes" json:"minimum_free_bytes"`
	MinimumFreePercent float64 `toml:"minimum_free_percent" json:"minimum_free_percent"`
}

type BackupConfig struct {
	Daily   int `toml:"daily" json:"daily"`
	Weekly  int `toml:"weekly" json:"weekly"`
	Monthly int `toml:"monthly" json:"monthly"`
}

type ConcurrencyConfig struct {
	Acquisition int `toml:"acquisition" json:"acquisition"`
	Validation  int `toml:"validation" json:"validation"`
	Import      int `toml:"import" json:"import"`
}

type SecretRef struct {
	Source string `toml:"source" json:"source"`
	Name   string `toml:"name" json:"name"`
	Value  string `toml:"-" json:"-"`
}

type SecretsConfig struct {
	InternalBearer SecretRef `toml:"internal_bearer" json:"internal_bearer"`
	AuditKey       SecretRef `toml:"audit_key" json:"audit_key"`
	LidarrAPIKey   SecretRef `toml:"lidarr_api_key" json:"lidarr_api_key"`
	BootstrapAdmin SecretRef `toml:"bootstrap_admin" json:"bootstrap_admin"`
}

type Policy struct {
	ScannerRecoveryInterval  Duration
	StabilityInterval        Duration
	AlbumSearchTimeout       Duration
	ReconciliationPoll       Duration
	PrimaryGraceWindow       Duration
	ArbitrationWindow        Duration
	SessionAbsoluteExpiry    Duration
	AcquisitionLeaseDuration Duration
	MinimumFreeBytes         int64
	MinimumFreePercent       float64
	PrimaryRetry             []Duration
	FallbackRetry            []Duration
}

func (c Config) Policy() Policy {
	return Policy{
		ScannerRecoveryInterval:  c.Scanners.RecoveryInterval,
		StabilityInterval:        c.Scanners.StabilityInterval,
		AlbumSearchTimeout:       c.Acquisition.AlbumSearchTimeout,
		ReconciliationPoll:       c.Acquisition.ReconciliationPoll,
		PrimaryGraceWindow:       c.Acquisition.PrimaryGraceWindow,
		ArbitrationWindow:        c.Arbitration.Window,
		SessionAbsoluteExpiry:    c.Sessions.AbsoluteExpiry,
		AcquisitionLeaseDuration: c.Acquisition.LeaseDuration,
		MinimumFreeBytes:         c.Storage.MinimumFreeBytes,
		MinimumFreePercent:       c.Storage.MinimumFreePercent,
		PrimaryRetry:             append([]Duration(nil), c.Acquisition.PrimaryRetry...),
		FallbackRetry:            append([]Duration(nil), c.Acquisition.FallbackRetry...),
	}
}
