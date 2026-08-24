package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	pipelineadapter "github.com/waxarsatia/denyra/internal/gateway/adapters/pipeline"
	"github.com/waxarsatia/denyra/internal/gateway/adapters/spotiflac"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	"github.com/waxarsatia/denyra/internal/gateway/transport"
	"github.com/waxarsatia/denyra/internal/platform/deplock"
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
	if len(arguments) > 0 && arguments[0] == "live-provider-acceptance" {
		if os.Getenv("DENYRA_LIVE_PROVIDER_ACCEPTANCE") != "I_ACCEPT_EXTERNAL_PROVIDER_SIDE_EFFECTS" {
			return errors.New("live provider acceptance requires the explicit side-effect gate")
		}
		arguments = arguments[1:]
	}
	if len(arguments) > 0 && arguments[0] == "healthcheck" {
		flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
		address := flags.String("address", "172.30.0.2:8081", "internal health address")
		timeout := flags.Duration("timeout", time.Duration(config.Defaults().HTTP.HealthcheckTimeout), "healthcheck timeout")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return servicehost.ProbeReady(ctx, *address, *timeout)
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
	var runtime *application.GatewayRuntime
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
		Initialize: func(ctx context.Context, prepared *servicehost.Prepared) error {
			repositories := persistence.New(prepared.DB, time.Now)
			snapshotID, err := repositories.CurrentConfigSnapshotID(ctx)
			if err != nil {
				return err
			}
			httpClient := &http.Client{Timeout: time.Duration(prepared.Config.HTTP.ExternalRequestTimeout)}
			lidarrClient := &lidarr.Client{BaseURL: prepared.Config.Services.LidarrURL, APIKey: prepared.Config.Secrets.LidarrAPIKey.Value, HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}
			pipelineClient := &pipelineadapter.Client{BaseURL: prepared.Config.Services.PipelineURL, Bearer: prepared.Config.Secrets.InternalBearer.Value, HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}
			var lidarrStatus map[string]any
			if err := lidarrClient.Get(ctx, "/api/v1/system/status", nil, &lidarrStatus); err != nil {
				return err
			}
			if err := pipelineClient.Ready(ctx); err != nil {
				return err
			}
			prepared.Health.Set(contracts.DependencyHealth{Name: "lidarr", State: contracts.DependencyOK, Local: true})
			prepared.Health.Set(contracts.DependencyHealth{Name: "media-pipeline", State: contracts.DependencyOK, Local: true})
			manifest := spotiflac.ExpectedManifest()
			installation, err := (spotiflac.Installation{
				EnginePath: "/opt/spotiflac/spotiflac", NodePath: "/opt/node/bin/node",
				ArtifactDirectory: "/opt/spotiflac/artifacts", InstalledExtensionDirectory: "/opt/spotiflac/runtime-home/.spotiflac/extensions",
				BuildProvenancePath: *provenancePath, Manifest: manifest,
			}).Verify(ctx, time.Duration(prepared.Config.HTTP.ExternalRequestTimeout), time.Now().UTC())
			if err != nil {
				return err
			}
			processes := spotiflac.NewProcessRegistry(time.Duration(prepared.Config.Acquisition.ProcessTerminateGrace))
			runner := spotiflac.Runner{
				Runtime:             installation,
				Resolver:            spotiflac.MusicBrainzResolver{BaseURL: prepared.Config.Services.MusicBrainzURL, UserAgent: "Denyra/1 (+https://github.com/waxarsatia/denyra)", HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit},
				BaseOutputDirectory: prepared.Config.Filesystem.DownloadsSpotiFLAC, RuntimeHome: "/opt/spotiflac/runtime-home",
				ProviderTimeout: time.Duration(prepared.Config.Acquisition.ProviderTimeout), PollInterval: time.Duration(prepared.Config.Acquisition.ProcessPollInterval),
				TerminationGrace: time.Duration(prepared.Config.Acquisition.ProcessTerminateGrace), OutputLimit: prepared.Config.Acquisition.ProcessOutputLimit,
				Concurrency: prepared.Config.Concurrency.Acquisition, Processes: processes,
			}
			policy := domain.RetryPolicy{Primary: durations(prepared.Config.Acquisition.PrimaryRetry), Fallback: durations(prepared.Config.Acquisition.FallbackRetry), NoCandidate: time.Duration(prepared.Config.Acquisition.NoCandidateRetry)}
			pause := func(ctx context.Context, duration time.Duration) error {
				timer := time.NewTimer(duration)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
					return nil
				}
			}
			handoff := application.CandidateHandoffService{Pipeline: pipelineClient, Store: repositories, ReplayAttempts: prepared.Config.HTTP.InternalReplayAttempts}
			lockBytes, err := os.ReadFile(*lockPath)
			if err != nil {
				return err
			}
			dependencyLock, err := deplock.Decode(lockBytes)
			if err != nil {
				return err
			}
			slskdImage, err := dependencyLock.Image("slskd")
			if err != nil {
				return err
			}
			completion := application.PrimaryCompletionService{Queue: lidarrClient, Store: repositories, Handoff: handoff, DownloadsRoot: prepared.Config.Filesystem.DownloadsSlskd, PageSize: prepared.Config.Acquisition.LidarrPageSize, EngineVersion: slskdImage.Version}
			completionMonitor := &application.PrimaryCompletionMonitor{Service: completion, Safety: time.Duration(prepared.Config.Acquisition.ReconciliationSafety), OnError: func(err error) { logger.Error("primary completion reconciliation failed", "error", err) }}
			primary := application.PrimarySearch{Lidarr: lidarrClient, Store: repositories, Policy: policy, CommandTimeout: time.Duration(prepared.Config.Acquisition.AlbumSearchTimeout), PollInterval: time.Duration(prepared.Config.Acquisition.ReconciliationPoll), GraceWindow: time.Duration(prepared.Config.Acquisition.PrimaryGraceWindow), Pause: pause}
			reconciler := application.PrimaryReconciler{Lidarr: lidarrClient, Store: repositories, Policy: policy, PageSize: prepared.Config.Acquisition.LidarrPageSize, PollInterval: time.Duration(prepared.Config.Acquisition.ReconciliationPoll), Pause: pause, Handoff: handoff}
			canceller := application.TransferCancellationService{Lidarr: lidarrClient, SpotiFLAC: processes}
			arbitration := application.ArbitrationService{Store: repositories, Pipeline: pipelineClient, Canceller: canceller, Window: time.Duration(prepared.Config.Arbitration.Window), ReplayAttempts: prepared.Config.HTTP.InternalReplayAttempts}
			fallback := application.FallbackService{Runner: runner, Store: repositories, Policy: policy, Providers: manifest.Providers(), OutputRoot: prepared.Config.Filesystem.DownloadsSpotiFLAC, OverallTimeout: time.Duration(prepared.Config.Acquisition.OverallTimeout), Handoff: handoff}
			admission := application.AdmissionController{Store: repositories, DataRoot: prepared.Config.Filesystem.DownloadsSpotiFLAC, MinimumFreeBytes: prepared.Config.Storage.MinimumFreeBytes, MinimumFreePercent: prepared.Config.Storage.MinimumFreePercent}
			worker := &application.AcquisitionWorker{Store: repositories, Admission: admission, Primary: primary, Reconciler: reconciler, Fallback: fallback, Arbitration: arbitration, Concurrency: prepared.Config.Concurrency.Acquisition, Lease: time.Duration(prepared.Config.Acquisition.LeaseDuration), SafetyScan: time.Duration(prepared.Config.Acquisition.ReconciliationSafety), MaxInlineTransitions: prepared.Config.Acquisition.MaxInlineTransitions, OnError: func(jobID string, err error) { logger.Error("acquisition job failed", "job_id", jobID, "error", err) }}
			lateHandler := application.LatePrimaryService{Store: repositories, Canceller: processes, Handoff: handoff}
			recovery := application.GatewayRecovery{Store: repositories, Arbitration: arbitration, Handoff: handoff, Primary: primary, Reconciler: reconciler, RetryPolicy: policy, SpotiFLACRoot: prepared.Config.Filesystem.DownloadsSpotiFLAC, ActiveProcess: processes.Active, CancelProcess: processes.CancelSuperseded}
			runtime = &application.GatewayRuntime{Discovery: application.WantedDiscovery{Lidarr: lidarrClient, Store: repositories, ConfigSnapshotID: snapshotID}, Recovery: recovery, LatePrimary: application.LatePrimaryMonitor{Store: repositories, Reconciler: reconciler, Handler: lateHandler}, PrimaryCompletion: completionMonitor, Worker: worker, Safety: time.Duration(prepared.Config.Acquisition.ReconciliationSafety)}
			go func() {
				if err := runtime.Run(ctx); err != nil {
					logger.Error("acquisition runtime stopped", "error", err)
				}
			}()
			return nil
		},
		BuildInternalHandler: func(prepared *servicehost.Prepared) (http.Handler, error) {
			repositories := persistence.New(prepared.DB, time.Now)
			quality := transport.QualityCallbackAPI{Service: runtime.Worker.Arbitration, BodyLimit: prepared.Config.HTTP.InternalBodyLimit, Bearer: []byte(prepared.Config.Secrets.InternalBearer.Value)}
			return (transport.Routes{Quality: quality, Store: repositories, BodyLimit: prepared.Config.HTTP.InternalBodyLimit, Bearer: []byte(prepared.Config.Secrets.InternalBearer.Value), BackupRoot: filepath.Join(prepared.Config.Filesystem.DataRoot, "backups"), Notify: runtime.NotifyLidarr}).Handler()
		},
		BuildAcquisitionHandler: func(prepared *servicehost.Prepared) (http.Handler, error) {
			repositories := persistence.New(prepared.DB, time.Now)
			return (transport.SlskdEventRoutes{Store: repositories, BodyLimit: prepared.Config.HTTP.InternalBodyLimit, Notify: runtime.NotifySlskd}).Handler()
		},
	})
}

func durations(values []config.Duration) []time.Duration {
	result := make([]time.Duration, len(values))
	for index, value := range values {
		result[index] = time.Duration(value)
	}
	return result
}
