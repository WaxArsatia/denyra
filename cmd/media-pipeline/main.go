package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	gatewayadapter "github.com/waxarsatia/denyra/internal/pipeline/adapters/gateway"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/lrclib"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/navidrome"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/spotify"
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
		address := flags.String("address", "127.0.0.1:8081", "internal health address")
		timeout := flags.Duration("timeout", time.Duration(config.Defaults().HTTP.HealthcheckTimeout), "healthcheck timeout")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return servicehost.ProbeReady(ctx, *address, *timeout)
	}
	flags := flag.NewFlagSet("media-pipeline", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/denyra/config.toml", "configuration file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	serviceMigrations, err := migrations.For("pipeline")
	if err != nil {
		return err
	}
	var runtime *application.Runtime
	var migrationChecks application.MigrationCheckService
	var migrationService application.MigrationService
	return servicehost.Run(ctx, logger, servicehost.Options{
		Name:             "media-pipeline",
		ConfigPath:       *configPath,
		DatabasePath:     func(cfg config.Config) string { return cfg.Database.PipelinePath },
		Migrations:       serviceMigrations,
		RequiredBinaries: []string{"ffprobe", "flac", "metaflac", "beet"},
		CheckFilesystem: func(cfg config.Config) error {
			_, err := fscheck.Check(fscheck.Layout{
				DataRoot: cfg.Filesystem.DataRoot, DownloadsSlskd: cfg.Filesystem.DownloadsSlskd,
				DownloadsSpotiFLAC: cfg.Filesystem.DownloadsSpotiFLAC, DownloadsOther: cfg.Filesystem.DownloadsOther,
				IncomingManual: cfg.Filesystem.IncomingManual, IncomingUploading: cfg.Filesystem.IncomingUploading, Work: cfg.Filesystem.Work, Approved: cfg.Filesystem.Approved,
				Quarantine: cfg.Filesystem.Quarantine, Library: cfg.Filesystem.Library, LibraryUnmanaged: cfg.Filesystem.LibraryUnmanaged,
				ExpectedUID: os.Getuid(), ExpectedGID: os.Getgid(), MinimumMode: 0o700,
			})
			return err
		},
		ExternalDependencies: []string{"musicbrainz", "lrclib"},
		AdditionalSecrets:    func(cfg *config.Config) []*config.SecretRef { return []*config.SecretRef{&cfg.Secrets.NavidromeAdmin} },
		ServeAdmin:           true,
		Initialize: func(ctx context.Context, prepared *servicehost.Prepared) error {
			repositories := persistence.New(prepared.DB, time.Now)
			_, err := application.BootstrapAdmin(ctx, repositories, prepared.Config.Sessions.BootstrapUsername,
				prepared.Config.Secrets.BootstrapAdmin.Value, prepared.Config.Secrets.BootstrapAdmin.Name,
				prepared.Config.Sessions.PasswordMinLen, time.Now().UTC())
			if err != nil {
				return err
			}
			httpClient := &http.Client{Timeout: time.Duration(prepared.Config.HTTP.ExternalRequestTimeout)}
			commandRunner := media.Runner{MaxOutput: int(prepared.Config.Acquisition.ProcessOutputLimit)}
			ffprobe := media.FFProbe{Binary: "ffprobe", Version: "deployment-pinned", Timeout: time.Duration(prepared.Config.Validation.FFProbeTimeout), Runner: commandRunner}
			flac := media.FLAC{Binary: "flac", Version: "deployment-pinned", Timeout: time.Duration(prepared.Config.Validation.FLACTestTimeout), Runner: commandRunner}
			metaflac := media.MetaFLAC{Binary: "metaflac", Version: "deployment-pinned", Timeout: time.Duration(prepared.Config.Validation.MetaFLACTimeout), Runner: commandRunner}
			musicBrainz := &musicbrainz.Client{BaseURL: prepared.Config.Services.MusicBrainzURL, UserAgent: "Denyra/1 (+https://github.com/waxarsatia/denyra)", HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit, RateInterval: time.Duration(prepared.Config.Validation.MusicBrainzRateInterval)}
			navidromeClient := &navidrome.Client{BaseURL: prepared.Config.Services.NavidromeURL, Username: "admin", Password: prepared.Config.Secrets.NavidromeAdmin.Value, HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}
			lidarrClient := lidarr.Client{BaseURL: prepared.Config.Services.LidarrURL, APIKey: prepared.Config.Secrets.LidarrAPIKey.Value, HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}
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
			mutationService := application.MutationService{WorkRoot: prepared.Config.Filesystem.Work, QuarantineRoot: prepared.Config.Filesystem.Quarantine, Tags: metaflac, Integrity: flac, Checksum: media.SHA256}
			importService := application.ImportService{WorkRoot: prepared.Config.Filesystem.Work, ApprovedRoot: prepared.Config.Filesystem.Approved, Configuration: lidarr.ConfigVerifier{Client: lidarrClient}, Importer: lidarr.ManualImporter{Client: lidarrClient}, Verifier: lidarr.LibraryVerifier{Client: lidarrClient, LibraryRoot: prepared.Config.Filesystem.Library}, Store: repositories}
			unmanagedImport := application.UnmanagedImportService{Store: repositories, Metadata: application.UnmanagedMetadataService{}, Mutation: mutationService, Navidrome: navidromeClient, WorkRoot: prepared.Config.Filesystem.Work, LibraryRoot: prepared.Config.Filesystem.LibraryUnmanaged, ScanPoll: time.Duration(prepared.Config.Scanners.NavidromeWatcher)}
			durationPolicy := domain.DurationPolicy{TrackAutoFloorMS: prepared.Config.Validation.TrackAutoFloorMS, TrackAutoPercentBasisPoints: prepared.Config.Validation.TrackAutoPercentBasisPoints, TrackManualFloorMS: prepared.Config.Validation.TrackManualFloorMS, TrackManualPercentBasisPoints: prepared.Config.Validation.TrackManualPercentBasisPoints, ReleaseAutoFloorMS: prepared.Config.Validation.ReleaseAutoFloorMS, ReleaseAutoPercentBasisPoints: prepared.Config.Validation.ReleaseAutoPercentBasisPoints, ReleaseManualFloorMS: prepared.Config.Validation.ReleaseManualFloorMS, ReleaseManualPercentBasisPoints: prepared.Config.Validation.ReleaseManualPercentBasisPoints}
			workflow := application.ControlledWorkflow{
				Store:                repositories,
				Claim:                application.ClaimService{WorkRoot: prepared.Config.Filesystem.Work, LockRoot: prepared.Config.Filesystem.Work + "/.locks", StabilityInterval: time.Duration(prepared.Config.Scanners.StabilityInterval), Pause: pause},
				Validator:            application.TechnicalValidator{Inspector: ffprobe, Integrity: flac, Heuristic: media.NoHeuristic{}, Checksum: media.SHA256},
				Lookup:               musicBrainz,
				Matching:             application.MatchingService{DurationPolicy: durationPolicy, WorkRoot: prepared.Config.Filesystem.Work, QuarantineRoot: prepared.Config.Filesystem.Quarantine},
				Catalog:              application.LidarrCatalogService{Catalog: lidarr.Catalog{Client: lidarrClient}},
				Enrichment:           application.EnrichmentService{WorkRoot: prepared.Config.Filesystem.Work, EvidenceRoot: prepared.Config.Filesystem.Quarantine + "/.evidence", Lyrics: lrclib.Client{BaseURL: prepared.Config.Services.LRCLIBURL, UserAgent: "Denyra/1 (+https://github.com/waxarsatia/denyra)", HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}},
				Mutation:             mutationService,
				Quality:              application.QualityReporter{Store: repositories, Callback: internalapi.QualityClient{BaseURL: prepared.Config.Services.GatewayURL, Bearer: prepared.Config.Secrets.InternalBearer.Value, HTTP: httpClient, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit}},
				Import:               importService,
				Unmanaged:            unmanagedImport,
				SourceRoots:          map[domain.Source]string{domain.SourceSlskd: prepared.Config.Filesystem.DownloadsSlskd, domain.SourceSpotiFLAC: prepared.Config.Filesystem.DownloadsSpotiFLAC, domain.SourceOther: prepared.Config.Filesystem.DownloadsOther, domain.SourceManual: prepared.Config.Filesystem.IncomingManual},
				MaxInlineTransitions: prepared.Config.Acquisition.MaxInlineTransitions,
			}
			admission := &application.AdmissionGate{DataRoot: prepared.Config.Filesystem.Work, MinimumFreeBytes: prepared.Config.Storage.MinimumFreeBytes, MinimumFreePercent: prepared.Config.Storage.MinimumFreePercent}
			var maintenanceEnabled int
			if err := prepared.DB.QueryRowContext(ctx, `SELECT enabled FROM runtime_flags WHERE key='maintenance'`).Scan(&maintenanceEnabled); err != nil {
				return err
			}
			admission.SetMaintenance(maintenanceEnabled == 1)
			worker := &application.Worker{Store: repositories, Processor: workflow, Admission: admission, Concurrency: prepared.Config.Concurrency.Validation, LeaseDuration: time.Duration(prepared.Config.Acquisition.LeaseDuration), OwnerID: "media-pipeline", Queue: make(chan string, prepared.Config.Concurrency.Validation*4), OnError: func(candidateID string, err error) {
				logger.Error("pipeline candidate failed", "candidate_id", candidateID, "error", err)
			}}
			migrationChecks = application.MigrationCheckService{Store: repositories, Identity: application.IdentityService{Search: musicBrainz, DurationPolicy: durationPolicy}}
			migrationService = application.MigrationService{Store: repositories, Identity: application.IdentityService{Search: musicBrainz, DurationPolicy: durationPolicy}, Catalog: application.LidarrCatalogService{Catalog: lidarr.Catalog{Client: lidarrClient}}, Mutation: application.MigrationMutationService{ApprovedRoot: prepared.Config.Filesystem.Approved, Tags: metaflac, Integrity: flac, Checksum: media.SHA256}, Import: importService, Navidrome: navidromeClient, UnmanagedRoot: prepared.Config.Filesystem.LibraryUnmanaged, ApprovedRoot: prepared.Config.Filesystem.Approved, ScanPoll: time.Duration(prepared.Config.Scanners.NavidromeWatcher)}
			migrationRuntime := &application.MigrationRuntime{Store: repositories, Check: application.MigrationCoordinator{Check: migrationChecks, Migration: migrationService}, Concurrency: prepared.Config.Concurrency.MigrationCheck, LeaseDuration: time.Duration(prepared.Config.Acquisition.LeaseDuration), OwnerID: "media-pipeline-migration-check", OnError: func(itemID string, err error) {
				logger.Error("migration check failed", "item_id", itemID, "error", err)
			}}
			runtime = &application.Runtime{RecoveryInterval: time.Duration(prepared.Config.Scanners.RecoveryInterval), Discovery: application.DiscoveryService{Store: repositories, IncomingRoot: prepared.Config.Filesystem.IncomingManual}, Recovery: application.RecoveryService{Store: repositories, WorkRoot: prepared.Config.Filesystem.Work, ApprovedRoot: prepared.Config.Filesystem.Approved, QuarantineRoot: prepared.Config.Filesystem.Quarantine, Unmanaged: unmanagedImport}, Worker: worker, Migration: migrationRuntime}
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
			previewInspector := media.FFProbe{Binary: "ffprobe", Version: "deployment-release", Timeout: time.Duration(prepared.Config.Validation.FFProbeTimeout), Runner: media.Runner{MaxOutput: int(prepared.Config.Acquisition.ProcessOutputLimit)}}
			previewHTTP := &http.Client{Timeout: time.Duration(prepared.Config.HTTP.ExternalRequestTimeout)}
			previewMusicBrainz := &musicbrainz.Client{BaseURL: prepared.Config.Services.MusicBrainzURL, UserAgent: "Denyra/1 (+https://github.com/waxarsatia/denyra)", HTTP: previewHTTP, ResponseLimit: prepared.Config.HTTP.ExternalResponseLimit, RateInterval: time.Duration(prepared.Config.Validation.MusicBrainzRateInterval)}
			previewArtwork := &application.ArtworkService{
				Local:    media.Artwork{MetaFLAC: media.MetaFLAC{Binary: "metaflac", Version: "deployment-release", Timeout: time.Duration(prepared.Config.Validation.MetaFLACTimeout), Runner: media.Runner{MaxOutput: int(prepared.Config.Uploads.ImageMaxBytes + 1)}}, MaxBytes: prepared.Config.Uploads.ImageMaxBytes},
				Spotify:  &spotify.OEmbed{BaseURL: prepared.Config.Services.SpotifyOEmbedURL, HTTP: previewHTTP, ResponseLimit: prepared.Config.Uploads.ImageMaxBytes},
				CoverArt: &musicbrainz.CoverArt{BaseURL: prepared.Config.Services.CoverArtURL, HTTP: previewHTTP, ResponseLimit: prepared.Config.Uploads.ImageMaxBytes},
				Root:     filepath.Join(prepared.Config.Filesystem.Work, ".artwork"), MaxBytes: prepared.Config.Uploads.ImageMaxBytes, MaxPixels: prepared.Config.Uploads.ImageMaxPixels,
			}
			previews := &application.SubmissionPreviewService{Store: repositories, Inspector: previewInspector, Identity: &application.IdentityService{
				Search: previewMusicBrainz,
				DurationPolicy: domain.DurationPolicy{
					TrackAutoFloorMS: prepared.Config.Validation.TrackAutoFloorMS, TrackAutoPercentBasisPoints: prepared.Config.Validation.TrackAutoPercentBasisPoints,
					TrackManualFloorMS: prepared.Config.Validation.TrackManualFloorMS, TrackManualPercentBasisPoints: prepared.Config.Validation.TrackManualPercentBasisPoints,
					ReleaseAutoFloorMS: prepared.Config.Validation.ReleaseAutoFloorMS, ReleaseAutoPercentBasisPoints: prepared.Config.Validation.ReleaseAutoPercentBasisPoints,
					ReleaseManualFloorMS: prepared.Config.Validation.ReleaseManualFloorMS, ReleaseManualPercentBasisPoints: prepared.Config.Validation.ReleaseManualPercentBasisPoints,
				},
			}, Artwork: previewArtwork}
			uploads := &application.UploadService{
				Store: repositories, Writer: denyrafs.UploadWriter{Root: prepared.Config.Filesystem.IncomingUploading},
				UploadingRoot: prepared.Config.Filesystem.IncomingUploading, IncomingRoot: prepared.Config.Filesystem.IncomingManual,
				Policy: prepared.Config.Uploads,
			}
			return handlers.New(handlers.Dependencies{Auth: auth, Reader: repositories, Assets: bundle, ConfigSnapshot: fmt.Sprintf("%x", snapshot.Hash[:8]),
				Acquisition:     gatewayClient,
				MigrationReader: repositories, MigrationChecks: migrationChecks, Migrations: migrationService, NotifyMigrationBatch: runtime.NotifyMigrationBatch,
				Reviews:     application.ReviewDecisionService{Store: repositories, WorkRoot: prepared.Config.Filesystem.Work, QuarantineRoot: prepared.Config.Filesystem.Quarantine},
				Submissions: application.SubmissionService{Store: repositories, IncomingRoot: prepared.Config.Filesystem.IncomingManual},
				Uploads:     uploads, Previews: previews})
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
			return (internalapi.API{Service: service, BodyLimit: prepared.Config.HTTP.InternalBodyLimit, Bearer: []byte(prepared.Config.Secrets.InternalBearer.Value), NotifyManualDiscovery: runtime.NotifyManualDiscovery, DB: prepared.DB, Admission: runtime.Worker.Admission}).Handler()
		},
	})
}
