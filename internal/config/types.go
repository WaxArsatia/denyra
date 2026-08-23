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
	Secrets     SecretsConfig     `toml:"secrets" json:"secrets"`
}

type HTTPConfig struct {
	AdminAddress       string `toml:"admin_address" json:"admin_address"`
	InternalAddress    string `toml:"internal_address" json:"internal_address"`
	InternalBodyLimit  int64  `toml:"internal_body_limit" json:"internal_body_limit"`
	AdminMutationLimit int64  `toml:"admin_mutation_limit" json:"admin_mutation_limit"`
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
	AlbumSearchTimeout   Duration   `toml:"album_search_timeout" json:"album_search_timeout"`
	ReconciliationPoll   Duration   `toml:"reconciliation_poll" json:"reconciliation_poll"`
	ReconciliationSafety Duration   `toml:"reconciliation_safety" json:"reconciliation_safety"`
	PrimaryGraceWindow   Duration   `toml:"primary_grace_window" json:"primary_grace_window"`
	ProviderTimeout      Duration   `toml:"provider_timeout" json:"provider_timeout"`
	OverallTimeout       Duration   `toml:"overall_timeout" json:"overall_timeout"`
	NoCandidateRetry     Duration   `toml:"no_candidate_retry" json:"no_candidate_retry"`
	PrimaryRetry         []Duration `toml:"primary_retry" json:"primary_retry"`
	FallbackRetry        []Duration `toml:"fallback_retry" json:"fallback_retry"`
}

type ValidationConfig struct {
	TrackAutoFloorMS     int64   `toml:"track_auto_floor_ms" json:"track_auto_floor_ms"`
	TrackAutoPercent     float64 `toml:"track_auto_percent" json:"track_auto_percent"`
	TrackManualFloorMS   int64   `toml:"track_manual_floor_ms" json:"track_manual_floor_ms"`
	TrackManualPercent   float64 `toml:"track_manual_percent" json:"track_manual_percent"`
	ReleaseAutoFloorMS   int64   `toml:"release_auto_floor_ms" json:"release_auto_floor_ms"`
	ReleaseAutoPercent   float64 `toml:"release_auto_percent" json:"release_auto_percent"`
	ReleaseManualFloorMS int64   `toml:"release_manual_floor_ms" json:"release_manual_floor_ms"`
	ReleaseManualPercent float64 `toml:"release_manual_percent" json:"release_manual_percent"`
}

type ArbitrationConfig struct {
	Window Duration `toml:"window" json:"window"`
}

type SessionConfig struct {
	AbsoluteExpiry Duration `toml:"absolute_expiry" json:"absolute_expiry"`
	PasswordMinLen int      `toml:"password_min_length" json:"password_min_length"`
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
}

type Policy struct {
	ScannerRecoveryInterval Duration
	StabilityInterval       Duration
	AlbumSearchTimeout      Duration
	ReconciliationPoll      Duration
	PrimaryGraceWindow      Duration
	ArbitrationWindow       Duration
	SessionAbsoluteExpiry   Duration
	MinimumFreeBytes        int64
	MinimumFreePercent      float64
	PrimaryRetry            []Duration
	FallbackRetry           []Duration
}

func (c Config) Policy() Policy {
	return Policy{
		ScannerRecoveryInterval: c.Scanners.RecoveryInterval,
		StabilityInterval:       c.Scanners.StabilityInterval,
		AlbumSearchTimeout:      c.Acquisition.AlbumSearchTimeout,
		ReconciliationPoll:      c.Acquisition.ReconciliationPoll,
		PrimaryGraceWindow:      c.Acquisition.PrimaryGraceWindow,
		ArbitrationWindow:       c.Arbitration.Window,
		SessionAbsoluteExpiry:   c.Sessions.AbsoluteExpiry,
		MinimumFreeBytes:        c.Storage.MinimumFreeBytes,
		MinimumFreePercent:      c.Storage.MinimumFreePercent,
		PrimaryRetry:            append([]Duration(nil), c.Acquisition.PrimaryRetry...),
		FallbackRetry:           append([]Duration(nil), c.Acquisition.FallbackRetry...),
	}
}
