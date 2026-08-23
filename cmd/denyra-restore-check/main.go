package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	denyrarestore "github.com/waxarsatia/denyra/internal/platform/restore"
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
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func create(arguments []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	source := flags.String("source", "", "absolute backup source root")
	workspace := flags.String("workspace", "", "absolute backup workspace")
	backupID := flags.String("backup-id", "", "backup identity")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	manifest, err := denyrarestore.Create(denyrarestore.CreateOptions{BackupID: *backupID, SourceRoot: *source, WorkspaceRoot: *workspace, CreatedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	return denyrarestore.WriteManifest(*workspace+"/manifest.json", manifest)
}

func verify(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	root := flags.String("root", "", "absolute restored tree root")
	expectedLock := flags.String("expected-lock", "/app/dependencies.lock.json", "current dependency lock")
	snapshot := flags.String("snapshot", "", "Restic snapshot identity")
	reportPath := flags.String("report", "", "JSON verification report path")
	cutoverPath := flags.String("cutover-report", "", "Markdown cutover report path")
	uid := flags.Int("uid", -1, "expected media UID")
	gid := flags.Int("gid", -1, "expected media GID")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	options := denyrarestore.VerifyOptions{RestoreRoot: *root, ExpectedLock: *expectedLock, SnapshotID: *snapshot}
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
