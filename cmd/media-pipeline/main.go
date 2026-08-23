package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
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
		address := flags.String("address", "172.30.0.3:8081", "internal health address")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return servicehost.ProbeReady(ctx, *address)
	}
	flags := flag.NewFlagSet("media-pipeline", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/denyra/config.toml", "configuration file")
	lockPath := flags.String("lock", "/app/dependencies.lock.json", "dependency lock")
	provenancePath := flags.String("provenance", "/app/build-provenance.json", "build provenance")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	serviceMigrations, err := migrations.For("pipeline")
	if err != nil {
		return err
	}
	return servicehost.Run(ctx, logger, servicehost.Options{
		Name:             "media-pipeline",
		ConfigPath:       *configPath,
		LockPath:         *lockPath,
		ProvenancePath:   *provenancePath,
		DatabasePath:     func(cfg config.Config) string { return cfg.Database.PipelinePath },
		Migrations:       serviceMigrations,
		RequiredBinaries: []string{"ffprobe", "flac", "metaflac", "beet"},
		CheckFilesystem: func(cfg config.Config) error {
			_, err := fscheck.Check(fscheck.Layout{
				DataRoot: cfg.Filesystem.DataRoot, DownloadsSlskd: cfg.Filesystem.DownloadsSlskd,
				DownloadsSpotiFLAC: cfg.Filesystem.DownloadsSpotiFLAC, DownloadsOther: cfg.Filesystem.DownloadsOther,
				IncomingManual: cfg.Filesystem.IncomingManual, Work: cfg.Filesystem.Work, Approved: cfg.Filesystem.Approved,
				Quarantine: cfg.Filesystem.Quarantine, Library: cfg.Filesystem.Library,
				ExpectedUID: os.Getuid(), ExpectedGID: os.Getgid(), MinimumMode: 0o700,
			})
			return err
		},
		ExternalDependencies: []string{"musicbrainz", "lrclib"},
		ServeAdmin:           true,
		Initialize: func(ctx context.Context, prepared *servicehost.Prepared) error {
			repositories := persistence.New(prepared.DB, time.Now)
			_, err := application.BootstrapAdmin(ctx, repositories, prepared.Config.Sessions.BootstrapUsername,
				prepared.Config.Secrets.BootstrapAdmin.Value, prepared.Config.Secrets.BootstrapAdmin.Name,
				prepared.Config.Sessions.PasswordMinLen, time.Now().UTC())
			return err
		},
	})
}
