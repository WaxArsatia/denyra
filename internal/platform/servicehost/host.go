// Package servicehost wires Denyra's validated process foundation.
package servicehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/waxarsatia/denyra/internal/config"
	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/platform/deplock"
	"github.com/waxarsatia/denyra/internal/platform/health"
	"github.com/waxarsatia/denyra/internal/platform/ids"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

type Options struct {
	Name                 string
	ConfigPath           string
	LockPath             string
	ProvenancePath       string
	DatabasePath         func(config.Config) string
	Migrations           []denysqlite.Migration
	RequiredBinaries     []string
	CheckFilesystem      func(config.Config) error
	ExternalDependencies []string
	ServeAdmin           bool
	Initialize           func(context.Context, *Prepared) error
	Now                  func() time.Time
}

type Prepared struct {
	Config config.Config
	DB     *sql.DB
	Health *health.Service
}

func Prepare(ctx context.Context, logger *slog.Logger, options Options) (_ *Prepared, err error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	cfg, err := config.Load(options.ConfigPath, os.Environ())
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	if err := resolveSnapshotSecrets(&cfg); err != nil {
		return nil, err
	}
	provenance, err := verifyLockIdentity(options.LockPath, options.ProvenancePath)
	if err != nil {
		return nil, err
	}
	for _, binary := range options.RequiredBinaries {
		if _, err := exec.LookPath(binary); err != nil {
			return nil, fmt.Errorf("required binary %q: %w", binary, err)
		}
	}
	if options.CheckFilesystem != nil {
		if err := options.CheckFilesystem(cfg); err != nil {
			return nil, fmt.Errorf("filesystem validation: %w", err)
		}
	}
	databasePath := options.DatabasePath(cfg)
	db, err := denysqlite.Open(ctx, databasePath, denysqlite.Options{
		BusyTimeout:  time.Duration(cfg.Database.BusyTimeout),
		MaxOpenConns: cfg.Database.MaxOpenConns,
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	if err := denysqlite.Migrate(ctx, db, options.Migrations, now()); err != nil {
		return nil, fmt.Errorf("database migration: %w", err)
	}
	snapshot, err := config.NewSnapshot(cfg, []byte(cfg.Secrets.AuditKey.Value))
	if err != nil {
		return nil, fmt.Errorf("create config snapshot: %w", err)
	}
	if err := recordStartup(ctx, db, snapshot, provenance, now()); err != nil {
		return nil, err
	}
	healthService := health.New()
	for _, dependency := range []string{"configuration", "dependency-lock", "required-binaries", "filesystem", "database", "migrations"} {
		healthService.Set(contracts.DependencyHealth{Name: dependency, State: contracts.DependencyOK, Local: true})
	}
	for _, dependency := range options.ExternalDependencies {
		healthService.Set(contracts.DependencyHealth{Name: dependency, State: contracts.DependencyDegraded, Details: "not yet observed", Local: false})
	}
	prepared := &Prepared{Config: cfg, DB: db, Health: healthService}
	if options.Initialize != nil {
		if err := options.Initialize(ctx, prepared); err != nil {
			healthService.Stop()
			return nil, fmt.Errorf("initialize service: %w", err)
		}
	}
	logger.Info("service foundation ready",
		"service", options.Name,
		"config_snapshot_sha256", hex.EncodeToString(snapshot.Hash[:]),
		"database", databasePath,
	)
	return prepared, nil
}

func (p *Prepared) Close() error {
	p.Health.Stop()
	return p.DB.Close()
}

func Run(ctx context.Context, logger *slog.Logger, options Options) error {
	prepared, err := Prepare(ctx, logger, options)
	if err != nil {
		return err
	}
	defer prepared.Close()

	listeners := make([]net.Listener, 0, 2)
	servers := make([]*http.Server, 0, 2)
	addServer := func(address string) error {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		listeners = append(listeners, listener)
		servers = append(servers, &http.Server{
			Handler:           health.Handler(prepared.Health),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       30 * time.Second,
		})
		return nil
	}
	if err := addServer(prepared.Config.HTTP.InternalAddress); err != nil {
		return fmt.Errorf("listen internal API: %w", err)
	}
	if options.ServeAdmin {
		if err := addServer(prepared.Config.HTTP.AdminAddress); err != nil {
			for _, listener := range listeners {
				_ = listener.Close()
			}
			return fmt.Errorf("listen admin HTTP: %w", err)
		}
	}

	serveErrors := make(chan error, len(servers))
	for index, server := range servers {
		go func(server *http.Server, listener net.Listener) {
			serveErrors <- server.Serve(listener)
		}(server, listeners[index])
	}
	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			prepared.Health.Stop()
			return fmt.Errorf("serve HTTP: %w", err)
		}
	}

	prepared.Health.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
	}
	return nil
}

func validateOptions(options Options) error {
	if options.Name == "" || options.ConfigPath == "" || options.LockPath == "" || options.ProvenancePath == "" {
		return fmt.Errorf("service host paths and name are required")
	}
	if options.DatabasePath == nil || len(options.Migrations) == 0 {
		return fmt.Errorf("database path selector and migrations are required")
	}
	return nil
}

func resolveSnapshotSecrets(cfg *config.Config) error {
	references := []*config.SecretRef{&cfg.Secrets.InternalBearer, &cfg.Secrets.AuditKey, &cfg.Secrets.LidarrAPIKey}
	for _, reference := range references {
		if reference.Source != "file" {
			return fmt.Errorf("secret %q uses unsupported source %q", reference.Name, reference.Source)
		}
		value, err := os.ReadFile(reference.Name)
		if err != nil {
			return fmt.Errorf("read secret %q: %w", reference.Name, err)
		}
		reference.Value = string(bytes.TrimSpace(value))
		if reference.Value == "" {
			return fmt.Errorf("secret %q is empty", reference.Name)
		}
	}
	return nil
}

func verifyLockIdentity(lockPath, provenancePath string) ([]byte, error) {
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read dependency lock: %w", err)
	}
	if _, err := deplock.Decode(lockBytes); err != nil {
		return nil, fmt.Errorf("invalid dependency pin: %w", err)
	}
	provenanceBytes, err := os.ReadFile(provenancePath)
	if err != nil {
		return nil, fmt.Errorf("read build provenance: %w", err)
	}
	var provenance struct {
		LockSHA256 string `json:"lock_sha256"`
	}
	if err := json.Unmarshal(provenanceBytes, &provenance); err != nil {
		return nil, fmt.Errorf("decode build provenance: %w", err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(lockBytes))
	if provenance.LockSHA256 != want {
		return nil, fmt.Errorf("lock identity mismatch: provenance=%q actual=%q", provenance.LockSHA256, want)
	}
	return provenanceBytes, nil
}

func recordStartup(ctx context.Context, db *sql.DB, snapshot config.Snapshot, provenance []byte, now time.Time) error {
	snapshotID, err := ids.NewToken(16)
	if err != nil {
		return err
	}
	snapshotHash := hex.EncodeToString(snapshot.Hash[:])
	provenanceHash := fmt.Sprintf("%x", sha256.Sum256(provenance))
	return denysqlite.WithinTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO config_snapshots(id, canonical_json, sha256, created_at)
			VALUES (?, ?, ?, ?) ON CONFLICT(sha256) DO NOTHING`, snapshotID, snapshot.CanonicalJSON, snapshotHash, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record config snapshot: %w", err)
		}
		provenanceID, err := ids.NewToken(16)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO build_provenance(id, canonical_json, sha256, recorded_at)
			VALUES (?, ?, ?, ?) ON CONFLICT(sha256) DO NOTHING`, provenanceID, provenance, provenanceHash, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record build provenance: %w", err)
		}
		return nil
	})
}
