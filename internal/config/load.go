package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type envSetter func(*Config, string) error

var environmentRegistry = map[string]envSetter{
	"DENYRA_HTTP_ADMIN_ADDRESS":               func(c *Config, value string) error { c.HTTP.AdminAddress = value; return nil },
	"DENYRA_HTTP_INTERNAL_ADDRESS":            func(c *Config, value string) error { c.HTTP.InternalAddress = value; return nil },
	"DENYRA_ACQUISITION_ALBUM_SEARCH_TIMEOUT": durationSetter(func(c *Config) *Duration { return &c.Acquisition.AlbumSearchTimeout }),
	"DENYRA_ACQUISITION_RECONCILIATION_POLL":  durationSetter(func(c *Config) *Duration { return &c.Acquisition.ReconciliationPoll }),
	"DENYRA_ACQUISITION_PRIMARY_GRACE_WINDOW": durationSetter(func(c *Config) *Duration { return &c.Acquisition.PrimaryGraceWindow }),
	"DENYRA_ARBITRATION_WINDOW":               durationSetter(func(c *Config) *Duration { return &c.Arbitration.Window }),
	"DENYRA_SESSIONS_ABSOLUTE_EXPIRY":         durationSetter(func(c *Config) *Duration { return &c.Sessions.AbsoluteExpiry }),
	"DENYRA_SCANNERS_RECOVERY_INTERVAL":       durationSetter(func(c *Config) *Duration { return &c.Scanners.RecoveryInterval }),
	"DENYRA_SCANNERS_STABILITY_INTERVAL":      durationSetter(func(c *Config) *Duration { return &c.Scanners.StabilityInterval }),
	"DENYRA_STORAGE_MINIMUM_FREE_BYTES": func(c *Config, value string) error {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("parse bytes: %w", err)
		}
		c.Storage.MinimumFreeBytes = parsed
		return nil
	},
	"DENYRA_STORAGE_MINIMUM_FREE_PERCENT": func(c *Config, value string) error {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("parse percent: %w", err)
		}
		c.Storage.MinimumFreePercent = parsed
		return nil
	},
}

func durationSetter(target func(*Config) *Duration) envSetter {
	return func(c *Config, value string) error {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse duration: %w", err)
		}
		*target(c) = Duration(parsed)
		return nil
	}
}

func Load(path string, environment []string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
		decoder := toml.NewDecoder(file).DisallowUnknownFields().EnableUnmarshalerInterface()
		err = decoder.Decode(&cfg)
		closeErr := file.Close()
		if err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		if closeErr != nil {
			return Config{}, fmt.Errorf("close config: %w", closeErr)
		}
	}
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, "DENYRA_") {
			continue
		}
		setter, exists := environmentRegistry[name]
		if !exists {
			return Config{}, fmt.Errorf("unknown environment key %q", name)
		}
		if err := setter(&cfg, value); err != nil {
			return Config{}, fmt.Errorf("environment %s: %w", name, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
