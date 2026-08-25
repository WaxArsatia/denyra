package spotiflac

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestProviderEvidenceIsRedactedAndCapped(t *testing.T) {
	t.Parallel()
	secret := "provider-secret-value"
	oversized := "Authorization: Bearer " + secret + " " + strings.Repeat("x", maxExecutionEvidenceBytes)
	execution := ProviderExecution{
		Command:      []string{"spotiflac", "https://example.test/album?token=" + secret},
		ErrorMessage: "password=" + secret,
		Stdout:       oversized,
		Stderr:       "request https://example.test/api?access_token=" + secret,
		Output:       []OutputFile{{Path: "album?api_key=" + secret + ".flac"}},
	}

	sanitized := sanitizeExecution(execution, maxExecutionEvidenceBytes)
	allEvidence := strings.Join(append(append([]string{}, sanitized.Command...), sanitized.ErrorMessage, sanitized.Stdout, sanitized.Stderr, sanitized.Output[0].Path), "\n")
	if strings.Contains(allEvidence, secret) {
		t.Fatalf("sanitized execution leaked credential: %s", allEvidence)
	}
	for name, value := range map[string]string{"error": sanitized.ErrorMessage, "stdout": sanitized.Stdout, "stderr": sanitized.Stderr} {
		if len(value) > maxExecutionEvidenceBytes {
			t.Fatalf("%s evidence length = %d, want <= %d", name, len(value), maxExecutionEvidenceBytes)
		}
	}
	if !strings.Contains(sanitized.Stdout, "[TRUNCATED]") {
		t.Fatalf("oversized stdout was not marked truncated")
	}
}
