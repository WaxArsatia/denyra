package beets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/logsafe"
)

type Evidence struct {
	Version      string   `json:"version"`
	Invocation   []string `json:"invocation"`
	ExitStatus   int      `json:"exit_status"`
	Output       string   `json:"output"`
	OutputSHA256 string   `json:"output_sha256"`
	Confidence   string   `json:"confidence"`
	CandidateIDs []string `json:"candidate_ids"`
}

type Advisor struct {
	Binary               string
	Version              string
	Timeout              time.Duration
	ForbiddenLibraryRoot string
}

func (a Advisor) Advise(ctx context.Context, candidatePath string) (Evidence, error) {
	if !filepath.IsAbs(candidatePath) || filepath.Clean(candidatePath) != candidatePath {
		return Evidence{}, fmt.Errorf("candidate path must be absolute and canonical")
	}
	if a.ForbiddenLibraryRoot != "" {
		relative, err := filepath.Rel(a.ForbiddenLibraryRoot, candidatePath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return Evidence{}, fmt.Errorf("beets advisory cannot access the final library")
		}
	}
	temporary, err := os.MkdirTemp("", "denyra-beets-advisory-")
	if err != nil {
		return Evidence{}, err
	}
	defer os.RemoveAll(temporary)
	configPath := filepath.Join(temporary, "config.yaml")
	config := []byte("directory: " + filepath.Join(temporary, "never-library") + "\nlibrary: " + filepath.Join(temporary, "advisory.db") + "\nplugins: []\nimport:\n  copy: no\n  move: no\n  write: no\n  art: no\n  quiet: yes\n")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		return Evidence{}, err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	arguments := []string{"-c", configPath, "import", "--pretend", candidatePath}
	command := exec.CommandContext(child, a.Binary, arguments...)
	output, runErr := command.CombinedOutput()
	status := 0
	if runErr != nil {
		status = -1
		if exit, ok := runErr.(*exec.ExitError); ok {
			status = exit.ExitCode()
		}
	}
	hash := sha256.Sum256(output)
	evidence := Evidence{Version: a.Version, Invocation: []string{a.Binary, "-c", "<isolated-config>", "import", "--pretend", candidatePath}, ExitStatus: status,
		Output: logsafe.RedactText(string(output)), OutputSHA256: hex.EncodeToString(hash[:]), Confidence: "advisory"}
	if runErr != nil {
		return evidence, fmt.Errorf("beets advisory failed: %w", runErr)
	}
	return evidence, nil
}
