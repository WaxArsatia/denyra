package spotiflac

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
)

func TestRunProviderUsesArgumentsSupportedByPinnedEngine(t *testing.T) {
	root := t.TempDir()
	engine := filepath.Join(root, "spotiflac")
	engineScript := `#!/bin/sh
output_dir="$2"
for argument in "$@"; do
	if [ "$argument" = "--no-extensions-fallback" ]; then
		echo "spotiflac: error: unrecognized arguments: --no-extensions-fallback" >&2
		exit 2
	fi
done
mkdir -p "$output_dir"
printf 'flac' > "$output_dir/track.flac"
`
	if err := os.WriteFile(engine, []byte(engineScript), 0o700); err != nil {
		t.Fatal(err)
	}

	jobID := "job-1"
	outputDirectory := filepath.Join(root, jobID)
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	runner := Runner{
		Runtime: VerifiedInstallation{Installation: Installation{
			EnginePath: engine,
			Manifest:   RuntimeManifest{Extensions: []ExtensionIdentity{{ID: "test"}}},
		}},
		BaseOutputDirectory: root,
		RuntimeHome:         root,
		ProviderTimeout:     time.Second,
		PollInterval:        time.Millisecond,
		TerminationGrace:    time.Second,
		OutputLimit:         1024,
		Concurrency:         2,
		Processes:           NewProcessRegistry(time.Second),
		Now:                 func() time.Time { return now },
	}
	request := RunRequest{
		JobID:           jobID,
		OutputDirectory: outputDirectory,
		OverallDeadline: now.Add(time.Hour),
	}

	execution := runner.runProvider(context.Background(), request, "https://open.spotify.com/album/test", "ext:test")

	if execution.Outcome != domain.OutcomeCandidate {
		t.Fatalf("outcome = %q, want %q; stderr = %q", execution.Outcome, domain.OutcomeCandidate, execution.Stderr)
	}
}
