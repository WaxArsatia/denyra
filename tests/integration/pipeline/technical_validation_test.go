package pipeline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestTechnicalValidationUsesFFProbeAndFLACHardGates(t *testing.T) {
	fixtures := generateFLACFixtures(t)
	validator := realTechnicalValidator()
	result := validator.Validate(context.Background(), fixtures, []string{"mono-16-44100.flac", "stereo-24-96000.flac", "notes.txt"})
	if result.Rejected || len(result.Files) != 2 {
		t.Fatalf("valid release rejected: %+v", result)
	}
	for _, file := range result.Files {
		if !file.Info.ValidFLAC() || file.SHA256Before == "" || len(file.Commands) != 2 {
			t.Errorf("incomplete immutable technical evidence: %+v", file)
		}
	}
}

func TestTechnicalValidationRejectsCorruptAndFakeFLACReleaseWide(t *testing.T) {
	fixtures := generateFLACFixtures(t)
	validator := realTechnicalValidator()
	for _, filename := range []string{"truncated.flac", "fake.flac"} {
		t.Run(filename, func(t *testing.T) {
			result := validator.Validate(context.Background(), fixtures, []string{"mono-16-44100.flac", filename})
			if !result.Rejected || result.Reason == "" {
				t.Fatalf("invalid FLAC passed: %+v", result)
			}
		})
	}
}

func TestTechnicalValidationRejectsNonFLACAudioWithoutConversion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "track.mp3"), []byte("not invoked"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := realTechnicalValidator().Validate(context.Background(), root, []string{"track.mp3"})
	if !result.Rejected {
		t.Fatalf("non-FLAC audio accepted: %+v", result)
	}
}

func TestTechnicalValidationLosslessHeuristicCanWarnButNotReject(t *testing.T) {
	fixtures := generateFLACFixtures(t)
	validator := realTechnicalValidator()
	validator.Heuristic = warningHeuristic{}
	result := validator.Validate(context.Background(), fixtures, []string{"mono-16-44100.flac"})
	if result.Rejected || len(result.Warnings) != 1 || result.Warnings[0].Kind != domain.WarningQuality {
		t.Fatalf("heuristic changed hard gate incorrectly: %+v", result)
	}
}

func TestTechnicalValidationClassifiesToolTimeoutAsRetryableNotMediaRejection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	if err := os.WriteFile(path, []byte("candidate bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	validator := application.TechnicalValidator{Inspector: timedOutInspector{}, Integrity: media.FLAC{}, Checksum: media.SHA256}
	result := validator.Validate(context.Background(), root, []string{"track.flac"})
	if !result.Retryable || result.Rejected {
		t.Fatalf("tool timeout weakened validation classification: %+v", result)
	}
}

type timedOutInspector struct{}

func (timedOutInspector) Inspect(context.Context, string) (domain.TechnicalInfo, map[string][]string, domain.CommandEvidence, error) {
	return domain.TechnicalInfo{}, nil, domain.CommandEvidence{Tool: "ffprobe", ExitStatus: -1, TimedOut: true}, context.DeadlineExceeded
}

type warningHeuristic struct{}

func (warningHeuristic) Analyze(context.Context, string, domain.TechnicalInfo) ([]domain.Warning, error) {
	return []domain.Warning{{Kind: domain.WarningQuality, Code: "POSSIBLE_LOSSY_TRANSCODE", Details: "advisory only"}}, nil
}

func realTechnicalValidator() application.TechnicalValidator {
	runner := media.Runner{MaxOutput: 1 << 20}
	return application.TechnicalValidator{
		Inspector: media.FFProbe{Binary: "ffprobe", Version: "test-pinned", Timeout: 10 * time.Second, Runner: runner},
		Integrity: media.FLAC{Binary: "flac", Version: "test-pinned", Timeout: 10 * time.Second, Runner: runner},
		Heuristic: media.NoHeuristic{}, Checksum: media.SHA256,
	}
}

func generateFLACFixtures(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("sh", filepath.Join(repositoryRoot(t), "scripts/test-fixtures/generate-flac.sh"), root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate FLAC fixtures: %v\n%s", err, output)
	}
	return root
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
