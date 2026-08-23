package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/platform/logsafe"
)

type Runner struct {
	MaxOutput int
}

func (r Runner) Run(ctx context.Context, tool, version string, arguments ...string) (domain.CommandEvidence, error) {
	started := time.Now()
	limit := r.MaxOutput
	if limit <= 0 {
		limit = 1 << 20
	}
	stdout := &limitedBuffer{limit: limit}
	stderr := &limitedBuffer{limit: limit}
	command := exec.CommandContext(ctx, tool, arguments...)
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	exitStatus := 0
	if err != nil {
		exitStatus = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exitStatus = exit.ExitCode()
		}
	}
	evidence := domain.CommandEvidence{
		Tool: tool, Version: version, Arguments: append([]string(nil), arguments...), ExitStatus: exitStatus,
		Stdout: stdout.String(), Stderr: logsafe.RedactText(stderr.String()), Duration: time.Since(started),
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Truncated: stdout.truncated || stderr.truncated,
	}
	if err != nil {
		return evidence, fmt.Errorf("%s failed with status %d: %w", tool, exitStatus, err)
	}
	return evidence, nil
}

func ToolVersion(ctx context.Context, runner Runner, tool string, arguments ...string) (string, error) {
	evidence, err := runner.Run(ctx, tool, "version-probe", arguments...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(evidence.Stdout)
	if newline := strings.IndexByte(line, '\n'); newline >= 0 {
		line = line[:newline]
	}
	if line == "" {
		return "", fmt.Errorf("%s returned an empty version", tool)
	}
	return line, nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(value)
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }
