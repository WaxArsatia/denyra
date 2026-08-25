package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func (c Config) Validate() error {
	if strings.TrimSpace(c.HTTP.AdminAddress) == "" || strings.TrimSpace(c.HTTP.InternalAddress) == "" || strings.TrimSpace(c.HTTP.AcquisitionEventAddress) == "" {
		return fmt.Errorf("HTTP listener addresses are required")
	}
	positiveDurations := map[string]Duration{
		"database.busy_timeout":                c.Database.BusyTimeout,
		"database.idempotency_ttl":             c.Database.IdempotencyTTL,
		"acquisition.album_search_timeout":     c.Acquisition.AlbumSearchTimeout,
		"acquisition.reconciliation_poll":      c.Acquisition.ReconciliationPoll,
		"acquisition.reconciliation_safety":    c.Acquisition.ReconciliationSafety,
		"acquisition.primary_grace_window":     c.Acquisition.PrimaryGraceWindow,
		"acquisition.provider_timeout":         c.Acquisition.ProviderTimeout,
		"acquisition.overall_timeout":          c.Acquisition.OverallTimeout,
		"acquisition.process_poll_interval":    c.Acquisition.ProcessPollInterval,
		"acquisition.process_terminate_grace":  c.Acquisition.ProcessTerminateGrace,
		"acquisition.lease_duration":           c.Acquisition.LeaseDuration,
		"acquisition.no_candidate_retry":       c.Acquisition.NoCandidateRetry,
		"arbitration.window":                   c.Arbitration.Window,
		"sessions.absolute_expiry":             c.Sessions.AbsoluteExpiry,
		"sessions.login_throttle.window":       c.Sessions.LoginThrottle.Window,
		"sessions.login_throttle.base_delay":   c.Sessions.LoginThrottle.BaseDelay,
		"sessions.login_throttle.max_delay":    c.Sessions.LoginThrottle.MaximumDelay,
		"scanners.recovery_interval":           c.Scanners.RecoveryInterval,
		"scanners.stability_interval":          c.Scanners.StabilityInterval,
		"scanners.navidrome_schedule":          c.Scanners.NavidromeSchedule,
		"scanners.navidrome_watcher":           c.Scanners.NavidromeWatcher,
		"http.external_request_timeout":        c.HTTP.ExternalRequestTimeout,
		"http.read_header_timeout":             c.HTTP.ReadHeaderTimeout,
		"http.server_idle_timeout":             c.HTTP.ServerIdleTimeout,
		"http.shutdown_timeout":                c.HTTP.ShutdownTimeout,
		"http.healthcheck_timeout":             c.HTTP.HealthcheckTimeout,
		"validation.ffprobe_timeout":           c.Validation.FFProbeTimeout,
		"validation.flac_test_timeout":         c.Validation.FLACTestTimeout,
		"validation.metaflac_timeout":          c.Validation.MetaFLACTimeout,
		"validation.beets_timeout":             c.Validation.BeetsTimeout,
		"validation.musicbrainz_rate_interval": c.Validation.MusicBrainzRateInterval,
	}
	for name, value := range map[string]int64{
		"http.internal_body_limit":  c.HTTP.InternalBodyLimit,
		"http.admin_mutation_limit": c.HTTP.AdminMutationLimit,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if c.HTTP.ExternalResponseLimit <= 0 {
		return fmt.Errorf("http.external_response_limit must be positive")
	}
	if c.HTTP.InternalReplayAttempts <= 0 {
		return fmt.Errorf("http.internal_replay_attempts must be positive")
	}
	if c.Acquisition.LidarrPageSize <= 0 {
		return fmt.Errorf("acquisition.lidarr_page_size must be positive")
	}
	if c.Acquisition.ProcessOutputLimit <= 0 {
		return fmt.Errorf("acquisition.process_output_limit must be positive")
	}
	if c.Acquisition.ProcessOutputLimit > 64<<10 {
		return fmt.Errorf("acquisition.process_output_limit must not exceed 65536 bytes")
	}
	if c.Acquisition.MaxInlineTransitions <= 0 {
		return fmt.Errorf("acquisition.max_inline_transitions must be positive")
	}
	if c.Database.MaxOpenConns <= 0 {
		return fmt.Errorf("database.max_open_conns must be positive")
	}
	if c.Concurrency.Acquisition != 2 {
		return fmt.Errorf("concurrency.acquisition must be exactly 2 for the pinned SpotiFLAC provider policy")
	}
	if c.Concurrency.Validation <= 0 || c.Concurrency.Import <= 0 || c.Concurrency.MigrationCheck <= 0 {
		return fmt.Errorf("validation, import, and migration check concurrency must be positive")
	}
	if c.Uploads.MaxFileBytes <= 0 || c.Uploads.MaxSessionBytes <= 0 || c.Uploads.MaxSessionBytes < c.Uploads.MaxFileBytes || c.Uploads.MaxEntries <= 0 || c.Uploads.BrowserConcurrency <= 0 || c.Uploads.ImageMaxBytes <= 0 || c.Uploads.ImageMaxPixels <= 0 {
		return fmt.Errorf("upload limits, entry counts, concurrency, and image limits must be positive, with session bytes at least file bytes")
	}
	for name, value := range map[string]string{"services.lidarr_url": c.Services.LidarrURL, "services.gateway_url": c.Services.GatewayURL, "services.pipeline_url": c.Services.PipelineURL, "services.navidrome_url": c.Services.NavidromeURL, "services.musicbrainz_url": c.Services.MusicBrainzURL, "services.lrclib_url": c.Services.LRCLIBURL, "services.spotify_oembed_url": c.Services.SpotifyOEmbedURL, "services.cover_art_url": c.Services.CoverArtURL} {
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return fmt.Errorf("%s must be an HTTP URL", name)
		}
	}
	for name, value := range positiveDurations {
		if time.Duration(value) <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if err := validateRetry("primary", c.Acquisition.PrimaryRetry); err != nil {
		return err
	}
	if err := validateRetry("fallback", c.Acquisition.FallbackRetry); err != nil {
		return err
	}
	if c.Storage.MinimumFreeBytes < 0 {
		return fmt.Errorf("storage.minimum_free_bytes must not be negative")
	}
	if c.Storage.MinimumFreePercent < 0 || c.Storage.MinimumFreePercent > 100 {
		return fmt.Errorf("storage.minimum_free_percent must be within 0..100")
	}
	if c.Sessions.PasswordMinLen < 8 {
		return fmt.Errorf("sessions.password_min_length must be at least 8")
	}
	if strings.TrimSpace(c.Sessions.BootstrapUsername) == "" {
		return fmt.Errorf("sessions.bootstrap_username is required")
	}
	if c.Sessions.LoginThrottle.Failures <= 0 {
		return fmt.Errorf("sessions.login_throttle.failures must be positive")
	}
	if c.Sessions.LoginThrottle.Capacity < 128 {
		return fmt.Errorf("sessions.login_throttle.capacity must be at least 128")
	}
	if c.Sessions.LoginThrottle.MaximumDelay < c.Sessions.LoginThrottle.BaseDelay {
		return fmt.Errorf("sessions.login_throttle.max_delay must not be lower than base_delay")
	}
	validationValues := map[string]int64{
		"validation.track_auto_floor_ms":                 c.Validation.TrackAutoFloorMS,
		"validation.track_auto_percent_basis_points":     c.Validation.TrackAutoPercentBasisPoints,
		"validation.track_manual_floor_ms":               c.Validation.TrackManualFloorMS,
		"validation.track_manual_percent_basis_points":   c.Validation.TrackManualPercentBasisPoints,
		"validation.release_auto_floor_ms":               c.Validation.ReleaseAutoFloorMS,
		"validation.release_auto_percent_basis_points":   c.Validation.ReleaseAutoPercentBasisPoints,
		"validation.release_manual_floor_ms":             c.Validation.ReleaseManualFloorMS,
		"validation.release_manual_percent_basis_points": c.Validation.ReleaseManualPercentBasisPoints,
	}
	for name, value := range validationValues {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if c.Validation.TrackManualFloorMS < c.Validation.TrackAutoFloorMS || c.Validation.TrackManualPercentBasisPoints < c.Validation.TrackAutoPercentBasisPoints ||
		c.Validation.ReleaseManualFloorMS < c.Validation.ReleaseAutoFloorMS || c.Validation.ReleaseManualPercentBasisPoints < c.Validation.ReleaseAutoPercentBasisPoints {
		return fmt.Errorf("validation manual thresholds must not be lower than auto thresholds")
	}
	paths := []string{c.Filesystem.DownloadsSlskd, c.Filesystem.DownloadsSpotiFLAC, c.Filesystem.DownloadsOther, c.Filesystem.IncomingManual, c.Filesystem.IncomingUploading, c.Filesystem.Work, c.Filesystem.Approved, c.Filesystem.Quarantine, c.Filesystem.Library, c.Filesystem.LibraryUnmanaged}
	cleaned := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if !filepath.IsAbs(clean) || (clean != c.Filesystem.DataRoot && !strings.HasPrefix(clean, filepath.Clean(c.Filesystem.DataRoot)+string(filepath.Separator))) {
			return fmt.Errorf("filesystem path %q is outside data root", path)
		}
		if slices.Contains(cleaned, clean) {
			return fmt.Errorf("filesystem paths must be distinct: %q", clean)
		}
		cleaned = append(cleaned, clean)
	}
	return nil
}

func validateRetry(name string, schedule []Duration) error {
	if len(schedule) == 0 {
		return fmt.Errorf("%s retry schedule is empty", name)
	}
	for index, value := range schedule {
		if time.Duration(value) <= 0 {
			return fmt.Errorf("%s retry schedule contains non-positive duration", name)
		}
		if index > 0 && value < schedule[index-1] {
			return fmt.Errorf("%s retry schedule must be non-decreasing", name)
		}
	}
	return nil
}
