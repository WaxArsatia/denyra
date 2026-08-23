package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/waxarsatia/denyra/internal/config"
	"github.com/waxarsatia/denyra/internal/platform/fscheck"
	"github.com/waxarsatia/denyra/internal/platform/servicehost"
	"github.com/waxarsatia/denyra/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger, os.Args[1:]); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, arguments []string) error {
	if len(arguments) > 0 && arguments[0] == "healthcheck" {
		flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
		address := flags.String("address", "172.30.0.2:8081", "internal health address")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return servicehost.ProbeReady(ctx, *address)
	}
	flags := flag.NewFlagSet("acquisition-gateway", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/denyra/config.toml", "configuration file")
	lockPath := flags.String("lock", "/app/dependencies.lock.json", "dependency lock")
	provenancePath := flags.String("provenance", "/app/build-provenance.json", "build provenance")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	serviceMigrations, err := migrations.For("gateway")
	if err != nil {
		return err
	}
	return servicehost.Run(ctx, logger, servicehost.Options{
		Name:             "acquisition-gateway",
		ConfigPath:       *configPath,
		LockPath:         *lockPath,
		ProvenancePath:   *provenancePath,
		DatabasePath:     func(cfg config.Config) string { return cfg.Database.GatewayPath },
		Migrations:       serviceMigrations,
		RequiredBinaries: []string{"node", "/opt/spotiflac/spotiflac"},
		CheckFilesystem: func(cfg config.Config) error {
			_, err := fscheck.CheckDirectories(cfg.Filesystem.DataRoot, []fscheck.Directory{
				{Name: "downloads_spotiflac", Path: cfg.Filesystem.DownloadsSpotiFLAC},
				{Name: "gateway_state", Path: filepath.Dir(cfg.Database.GatewayPath)},
			}, os.Getuid(), os.Getgid(), 0o700)
			return err
		},
		ExternalDependencies: []string{"soulseek", "spotiflac-providers"},
	})
}
