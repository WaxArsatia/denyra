package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	denyrarestore "github.com/waxarsatia/denyra/internal/platform/restore"
	"github.com/waxarsatia/denyra/internal/platform/upgrade"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("create or verify command is required")
	}
	switch arguments[0] {
	case "create":
		return create(arguments[1:])
	case "verify":
		return verify(ctx, arguments[1:])
	case "schema-compatible":
		return schemaCompatible(ctx, arguments[1:])
	case "migration-smoke":
		return migrationSmoke(ctx, arguments[1:])
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func migrationSmoke(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("migration-smoke", flag.ContinueOnError)
	root := flags.String("root", "", "absolute restored source root")
	report := flags.String("report", "", "migration smoke report")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	versions, err := upgrade.SmokeMigrations(ctx, *root, time.Now().UTC())
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(map[string]any{"status": "PASSED", "database_versions": versions, "tested_at": time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o440)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func schemaCompatible(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("schema-compatible", flag.ContinueOnError)
	currentGateway := flags.String("current-gateway", "", "current gateway database")
	currentPipeline := flags.String("current-pipeline", "", "current pipeline database")
	priorGateway := flags.String("prior-gateway", "", "prior gateway database")
	priorPipeline := flags.String("prior-pipeline", "", "prior pipeline database")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	mode, err := upgrade.SelectRollback(ctx,
		upgrade.DatabasePair{Current: *currentGateway, Prior: *priorGateway},
		upgrade.DatabasePair{Current: *currentPipeline, Prior: *priorPipeline},
	)
	if err != nil {
		return err
	}
	fmt.Println(mode)
	return nil
}

func create(arguments []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	source := flags.String("source", "", "absolute backup source root")
	workspace := flags.String("workspace", "", "absolute backup workspace")
	backupID := flags.String("backup-id", "", "backup identity")
	gitCommit := flags.String("git-commit", "", "40-character Denyra Git commit")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	manifest, err := denyrarestore.Create(denyrarestore.CreateOptions{BackupID: *backupID, GitCommit: *gitCommit, SourceRoot: *source, WorkspaceRoot: *workspace, CreatedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	return denyrarestore.WriteManifest(*workspace+"/manifest.json", manifest)
}

func verify(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	root := flags.String("root", "", "absolute restored tree root")
	snapshot := flags.String("snapshot", "", "Restic snapshot identity")
	reportPath := flags.String("report", "", "JSON verification report path")
	cutoverPath := flags.String("cutover-report", "", "Markdown cutover report path")
	uid := flags.Int("uid", -1, "expected media UID")
	gid := flags.Int("gid", -1, "expected media GID")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	options := denyrarestore.VerifyOptions{RestoreRoot: *root, SnapshotID: *snapshot}
	if *uid >= 0 {
		value := uint32(*uid)
		options.ExpectedUID = &value
	}
	if *gid >= 0 {
		value := uint32(*gid)
		options.ExpectedGID = &value
	}
	report, err := denyrarestore.Verify(ctx, options)
	if err != nil {
		return err
	}
	if *reportPath == "" || *cutoverPath == "" {
		return errors.New("report and cutover-report paths are required")
	}
	if err := denyrarestore.WriteReport(*reportPath, report); err != nil {
		return err
	}
	return denyrarestore.WriteCutoverReport(*cutoverPath, report)
}
