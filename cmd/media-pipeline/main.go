package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	gatewayadapter "github.com/waxarsatia/denyra/internal/pipeline/adapters/gateway"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/handlers"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/internalapi"
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
		timeout := flags.Duration("timeout", time.Duration(config.Defaults().HTTP.HealthcheckTimeout), "healthcheck timeout")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return servicehost.ProbeReady(ctx, *address, *timeout)
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
	var runtime *application.Runtime
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
			if err != nil {
				return err
			}
			runtime = &application.Runtime{RecoveryInterval: time.Duration(prepared.Config.Scanners.RecoveryInterval), Discovery: application.DiscoveryService{Store: repositories, IncomingRoot: prepared.Config.Filesystem.IncomingManual}, Recovery: application.RecoveryService{Store: repositories, WorkRoot: prepared.Config.Filesystem.Work, ApprovedRoot: prepared.Config.Filesystem.Approved, QuarantineRoot: prepared.Config.Filesystem.Quarantine}}
			if _, err := runtime.Recovery.Reconcile(ctx); err != nil {
				return err
			}
			if _, err := runtime.Discovery.Scan(ctx); err != nil {
				return err
			}
			go func() {
				if err := runtime.Run(ctx); err != nil {
					logger.Error("pipeline runtime stopped", "error", err)
				}
			}()
			return nil
		},
		BuildAdminHandler: func(prepared *servicehost.Prepared) (http.Handler, error) {
			repositories := persistence.New(prepared.DB, time.Now)
			bundle, err := assets.New()
			if err != nil {
				return nil, err
			}
			snapshot, err := config.NewSnapshot(prepared.Config, []byte(prepared.Config.Secrets.AuditKey.Value))
			if err != nil {
				return nil, err
			}
			auth := application.AuthService{Repository: repositories, AbsoluteExpiry: time.Duration(prepared.Config.Sessions.AbsoluteExpiry), PasswordMinLen: prepared.Config.Sessions.PasswordMinLen}
			gatewayClient := gatewayadapter.Client{BaseURL: prepared.Config.Services.GatewayURL, Bearer: prepared.Config.Secrets.InternalBearer.Value, HTTP: &http.Client{Timeout: time.Duration(prepared.Config.HTTP.ExternalRequestTimeout)}, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}
			return handlers.New(handlers.Dependencies{Auth: auth, Reader: repositories, Assets: bundle, ConfigSnapshot: fmt.Sprintf("%x", snapshot.Hash[:8]),
				Acquisition: gatewayClient,
				Reviews:     application.ReviewDecisionService{Store: repositories, WorkRoot: prepared.Config.Filesystem.Work, QuarantineRoot: prepared.Config.Filesystem.Quarantine},
				Submissions: application.SubmissionService{Store: repositories, IncomingRoot: prepared.Config.Filesystem.IncomingManual}})
		},
		BuildInternalHandler: func(prepared *servicehost.Prepared) (http.Handler, error) {
			repositories := persistence.New(prepared.DB, time.Now)
			snapshotID, err := repositories.CurrentConfigSnapshotID(ctx)
			if err != nil {
				return nil, err
			}
			service := application.HandoffService{Store: repositories, LocalConfigSnapshotID: snapshotID, SourceRoots: map[domain.Source]string{
				domain.SourceSlskd: prepared.Config.Filesystem.DownloadsSlskd, domain.SourceSpotiFLAC: prepared.Config.Filesystem.DownloadsSpotiFLAC,
				domain.SourceOther: prepared.Config.Filesystem.DownloadsOther,
			}, OnAccepted: runtime.NotifyCandidate}
			return (internalapi.API{Service: service, BodyLimit: prepared.Config.HTTP.InternalBodyLimit, Bearer: []byte(prepared.Config.Secrets.InternalBearer.Value), NotifyManualDiscovery: runtime.NotifyManualDiscovery}).Handler()
		},
	})
}
