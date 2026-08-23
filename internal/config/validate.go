package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func (c Config) Validate() error {
	positiveDurations := map[string]Duration{
		"database.busy_timeout":            c.Database.BusyTimeout,
		"acquisition.album_search_timeout": c.Acquisition.AlbumSearchTimeout,
		"acquisition.reconciliation_poll":  c.Acquisition.ReconciliationPoll,
		"acquisition.primary_grace_window": c.Acquisition.PrimaryGraceWindow,
		"acquisition.provider_timeout":     c.Acquisition.ProviderTimeout,
		"acquisition.overall_timeout":      c.Acquisition.OverallTimeout,
		"arbitration.window":               c.Arbitration.Window,
		"sessions.absolute_expiry":         c.Sessions.AbsoluteExpiry,
		"scanners.recovery_interval":       c.Scanners.RecoveryInterval,
		"scanners.stability_interval":      c.Scanners.StabilityInterval,
		"http.external_request_timeout":    c.HTTP.ExternalRequestTimeout,
	}
	if c.HTTP.ExternalResponseLimit <= 0 {
		return fmt.Errorf("http.external_response_limit must be positive")
	}
	if c.Acquisition.LidarrPageSize <= 0 {
		return fmt.Errorf("acquisition.lidarr_page_size must be positive")
	}
	for name, value := range map[string]string{"services.lidarr_url": c.Services.LidarrURL, "services.pipeline_url": c.Services.PipelineURL, "services.musicbrainz_url": c.Services.MusicBrainzURL, "services.lrclib_url": c.Services.LRCLIBURL} {
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
	paths := []string{c.Filesystem.DownloadsSlskd, c.Filesystem.DownloadsSpotiFLAC, c.Filesystem.DownloadsOther, c.Filesystem.IncomingManual, c.Filesystem.Work, c.Filesystem.Approved, c.Filesystem.Quarantine, c.Filesystem.Library}
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
