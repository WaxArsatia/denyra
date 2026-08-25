package spotiflac

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
)

type LocatorResolver interface {
	Resolve(context.Context, string, string) (string, error)
}

type Runner struct {
	Runtime                          VerifiedInstallation
	Resolver                         LocatorResolver
	BaseOutputDirectory, RuntimeHome string
	ProviderTimeout, PollInterval    time.Duration
	TerminationGrace                 time.Duration
	OutputLimit                      int64
	Concurrency                      int
	Processes                        *ProcessRegistry
	Now                              func() time.Time
}

func (runner Runner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if err := runner.validateRequest(request); err != nil {
		return RunResult{}, err
	}
	defer runner.Processes.Clear(request.JobID)
	inputURL, err := runner.Resolver.Resolve(ctx, request.ReleaseGroupID, request.SelectedRelease)
	if err != nil {
		if errors.Is(err, ErrNoLocator) {
			return runner.noLocatorResult(request), nil
		}
		return runner.retryableResult(request, "RESOLUTION_ERROR", err), nil
	}
	if !strings.HasPrefix(inputURL, "https://") {
		return runner.retryableResult(request, "INVALID_RESOLUTION", fmt.Errorf("resolver returned a non-HTTPS locator")), nil
	}
	if err := os.MkdirAll(request.OutputDirectory, 0o750); err != nil {
		return RunResult{}, err
	}
	result := runner.baseResult()
	result.StartedAt = runner.now()
	for _, provider := range request.Providers {
		execution := runner.runProvider(ctx, request, inputURL, provider)
		result.Providers = append(result.Providers, execution)
		if execution.Outcome == domain.OutcomeCandidate {
			result.WinningProvider = provider
			result.Output = append([]OutputFile(nil), execution.Output...)
			break
		}
		if execution.ErrorClass == "SUPERSEDED_CANCELLED" {
			break
		}
		if !runner.now().Before(request.OverallDeadline) {
			break
		}
	}
	result.CompletedAt = runner.now()
	return result, nil
}

func (runner Runner) validateRequest(request RunRequest) error {
	if runner.Resolver == nil || runner.Processes == nil || runner.Runtime.Installation.EnginePath == "" || runner.BaseOutputDirectory == "" || runner.RuntimeHome == "" {
		return fmt.Errorf("SpotiFLAC runner is not configured")
	}
	if runner.ProviderTimeout <= 0 || runner.PollInterval <= 0 || runner.TerminationGrace <= 0 || runner.OutputLimit <= 0 || runner.Concurrency != 2 {
		return fmt.Errorf("SpotiFLAC runner policy is invalid")
	}
	if request.JobID == "" || request.ReleaseGroupID == "" || request.SelectedRelease == "" || request.OverallDeadline.IsZero() {
		return fmt.Errorf("SpotiFLAC request identity is incomplete")
	}
	if strings.ContainsAny(request.JobID, `/\\`) || request.JobID == "." || request.JobID == ".." {
		return fmt.Errorf("invalid SpotiFLAC job ID")
	}
	expectedDirectory := filepath.Join(filepath.Clean(runner.BaseOutputDirectory), request.JobID)
	if filepath.Clean(request.OutputDirectory) != expectedDirectory {
		return fmt.Errorf("SpotiFLAC output directory escapes the job root")
	}
	if !equalStrings(request.Providers, runner.Runtime.Installation.Manifest.Providers()) {
		return fmt.Errorf("SpotiFLAC provider allowlist/order mismatch")
	}
	if !request.OverallDeadline.After(runner.now()) {
		return fmt.Errorf("SpotiFLAC overall deadline has expired")
	}
	return nil
}

func (runner Runner) runProvider(ctx context.Context, request RunRequest, inputURL, provider string) ProviderExecution {
	started := runner.now()
	args := []string{
		inputURL,
		request.OutputDirectory,
		"--service", provider,
		"--quality", "LOSSLESS",
		"--max-concurrent", strconv.Itoa(runner.Concurrency),
		"--timeout", "0",
		"--retries", "0",
		"--transcode", "none",
		"--post-action", "none",
		"--no-lyrics",
		"--no-enrich",
	}
	execution := ProviderExecution{Provider: provider, StartedAt: started, Command: append([]string{runner.Runtime.Installation.EnginePath}, args...), ExitCode: -1}
	stdout := newCappedBuffer(runner.OutputLimit)
	stderr := newCappedBuffer(runner.OutputLimit)
	command := exec.Command(runner.Runtime.Installation.EnginePath, args...)
	command.Dir = runner.RuntimeHome
	command.Env = runner.environment()
	command.Stdout = stdout
	command.Stderr = stderr
	configureProcessGroup(command)
	if err := command.Start(); err != nil {
		return runner.failedExecution(execution, stdout, stderr, "PROCESS_START", err)
	}
	if err := runner.Processes.Track(request.JobID, command); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return runner.failedExecution(execution, stdout, stderr, "PROCESS_REGISTRY", err)
	}
	defer runner.Processes.Untrack(request.JobID, command)
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	establishmentDeadline := started.Add(runner.ProviderTimeout)
	ticker := time.NewTicker(runner.PollInterval)
	defer ticker.Stop()
	established := false
	var waitErr error
	for {
		select {
		case waitErr = <-waited:
			return runner.completedExecution(execution, stdout, stderr, command, waitErr, established, request.OutputDirectory, request.JobID)
		case <-ctx.Done():
			waitErr = terminateProcessGroup(command, waited, runner.TerminationGrace)
			return runner.failedExecutionWithProcess(execution, stdout, stderr, command, "CANCELLED", errors.Join(ctx.Err(), waitErr))
		case <-ticker.C:
			now := runner.now()
			if !now.Before(request.OverallDeadline) {
				waitErr = terminateProcessGroup(command, waited, runner.TerminationGrace)
				return runner.failedExecutionWithProcess(execution, stdout, stderr, command, "OVERALL_TIMEOUT", waitErr)
			}
			if !established {
				established = hasTransferEvidence(request.OutputDirectory) || strings.Contains(stdout.String(), "DENYRA_SPOTIFLAC_ESTABLISHED")
				if established {
					value := now
					execution.EstablishedAt = &value
				} else if !now.Before(establishmentDeadline) {
					waitErr = terminateProcessGroup(command, waited, runner.TerminationGrace)
					return runner.failedExecutionWithProcess(execution, stdout, stderr, command, "ESTABLISHMENT_TIMEOUT", waitErr)
				}
			}
		}
	}
}

func (runner Runner) completedExecution(execution ProviderExecution, stdout, stderr *cappedBuffer, command *exec.Cmd, waitErr error, established bool, outputDirectory, jobID string) ProviderExecution {
	completed := runner.now()
	execution.CompletedAt = &completed
	execution.Stdout = stdout.String()
	execution.Stderr = stderr.String()
	if command.ProcessState != nil {
		execution.ExitCode = command.ProcessState.ExitCode()
		execution.Signal = processSignal(command.ProcessState)
	}
	if stdout.Truncated() || stderr.Truncated() {
		execution.Outcome = domain.OutcomeRetryableError
		execution.ErrorClass = "OUTPUT_LIMIT"
		execution.ErrorMessage = "SpotiFLAC process output exceeded the configured limit"
		return execution
	}
	if waitErr != nil || execution.ExitCode != 0 {
		execution.Outcome = domain.OutcomeRetryableError
		if runner.Processes.WasSuperseded(jobID) {
			execution.ErrorClass = "SUPERSEDED_CANCELLED"
			execution.ErrorMessage = "active fallback transfer cancelled after correlated primary grab"
			return execution
		}
		execution.ErrorClass = "PROCESS_EXIT"
		execution.ErrorMessage = fmt.Sprint(waitErr)
		return execution
	}
	files, scanErr := scanOutput(runner.BaseOutputDirectory, outputDirectory)
	if scanErr != nil {
		execution.Outcome = domain.OutcomeRetryableError
		execution.ErrorClass = "OUTPUT_MANIFEST"
		execution.ErrorMessage = scanErr.Error()
		return execution
	}
	execution.Output = files
	if containsFLAC(files) {
		execution.Outcome = domain.OutcomeCandidate
		if execution.EstablishedAt == nil && established {
			execution.EstablishedAt = &completed
		}
		return execution
	}
	outcome, class, message := classifyEmptyOutput(execution.Stdout, execution.Stderr)
	execution.Outcome = outcome
	execution.ErrorClass = class
	execution.ErrorMessage = message
	return execution
}

func (runner Runner) failedExecution(execution ProviderExecution, stdout, stderr *cappedBuffer, class string, err error) ProviderExecution {
	completed := runner.now()
	execution.CompletedAt = &completed
	execution.Stdout = stdout.String()
	execution.Stderr = stderr.String()
	execution.Outcome = domain.OutcomeRetryableError
	execution.ErrorClass = class
	execution.ErrorMessage = err.Error()
	return execution
}

func (runner Runner) failedExecutionWithProcess(execution ProviderExecution, stdout, stderr *cappedBuffer, command *exec.Cmd, class string, err error) ProviderExecution {
	execution = runner.failedExecution(execution, stdout, stderr, class, err)
	if command.ProcessState != nil {
		execution.ExitCode = command.ProcessState.ExitCode()
		execution.Signal = processSignal(command.ProcessState)
	}
	return execution
}

func (runner Runner) environment() []string {
	nodeDirectory := filepath.Dir(runner.Runtime.Installation.NodePath)
	return []string{
		"HOME=" + runner.RuntimeHome,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"PATH=" + nodeDirectory + ":/usr/bin:/bin",
		"SPOTIFLAC_PROGRESS_BARS=0",
		"SPOTIFLAC_REGISTRIES= ",
		"SPOTIFLAC_DISABLE_AUTO_INSTALL=1",
		"SPOTIFLAC_DISABLE_AUTO_UPDATE=1",
		"SPOTIFLAC_CACHE_DIR=" + filepath.Join(runner.RuntimeHome, ".cache", "spotiflac"),
	}
}

func (runner Runner) baseResult() RunResult {
	manifest := runner.Runtime.Installation.Manifest
	return RunResult{EngineVersion: manifest.EngineVersion, EngineSHA256: manifest.EngineSHA256, NodeVersion: manifest.NodeVersion, NodeSHA256: manifest.NodeSHA256, Extensions: append([]ExtensionIdentity(nil), manifest.Extensions...)}
}

func (runner Runner) retryableResult(request RunRequest, class string, err error) RunResult {
	result := runner.baseResult()
	now := runner.now()
	result.StartedAt = now
	result.CompletedAt = now
	provider := "resolver"
	if len(request.Providers) > 0 {
		provider = request.Providers[0]
	}
	result.Providers = []ProviderExecution{{Provider: provider, Outcome: domain.OutcomeRetryableError, StartedAt: now, CompletedAt: &now, ExitCode: -1, ErrorClass: class, ErrorMessage: err.Error()}}
	return result
}

func (runner Runner) noLocatorResult(request RunRequest) RunResult {
	result := runner.baseResult()
	now := runner.now()
	result.StartedAt = now
	result.CompletedAt = now
	for _, provider := range request.Providers {
		completed := now
		result.Providers = append(result.Providers, ProviderExecution{Provider: provider, Outcome: domain.OutcomeLegitimateNoResult, StartedAt: now, CompletedAt: &completed, ExitCode: 0, ErrorMessage: ErrNoLocator.Error()})
	}
	return result
}

func (runner Runner) now() time.Time {
	if runner.Now != nil {
		return runner.Now().UTC()
	}
	return time.Now().UTC()
}

type cappedBuffer struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func newCappedBuffer(limit int64) *cappedBuffer { return &cappedBuffer{limit: limit} }

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	original := len(data)
	remaining := buffer.limit - int64(buffer.buffer.Len())
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
		buffer.truncated = true
	}
	_, _ = buffer.buffer.Write(data)
	return original, nil
}

func (buffer *cappedBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.buffer.String()
}

func (buffer *cappedBuffer) Truncated() bool {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.truncated
}

func classifyEmptyOutput(stdout, stderr string) (domain.ProviderOutcome, string, string) {
	var marker struct {
		Outcome string `json:"outcome"`
	}
	for _, line := range strings.Split(stdout, "\n") {
		payload, found := strings.CutPrefix(strings.TrimSpace(line), "DENYRA_SPOTIFLAC_RESULT=")
		if found && json.Unmarshal([]byte(payload), &marker) == nil {
			if marker.Outcome == "NO_RESULT" {
				return domain.OutcomeLegitimateNoResult, "", "explicit successful provider no-result"
			}
			return domain.OutcomeRetryableError, "MALFORMED_RESULT", "unknown SpotiFLAC result marker"
		}
	}
	combined := strings.ToLower(stdout + "\n" + stderr)
	for _, indicator := range []string{"traceback", "metadata fetch failed", "provider timed out", "all providers failed", "unexpected exception", "error:", "✗"} {
		if strings.Contains(combined, indicator) {
			return domain.OutcomeRetryableError, "PROVIDER_ERROR", "SpotiFLAC reported an operational failure"
		}
	}
	if strings.Contains(combined, "spotiflac") && strings.TrimSpace(stderr) == "" {
		return domain.OutcomeLegitimateNoResult, "", "successful pinned engine run produced no candidate"
	}
	return domain.OutcomeRetryableError, "MALFORMED_RESULT", "SpotiFLAC produced neither candidate nor explicit successful no-result"
}

func hasTransferEvidence(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && path != root && !entry.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func scanOutput(baseRoot, outputDirectory string) ([]OutputFile, error) {
	baseRoot = filepath.Clean(baseRoot)
	outputDirectory = filepath.Clean(outputDirectory)
	if outputDirectory != baseRoot && !strings.HasPrefix(outputDirectory, baseRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("output manifest root escapes configured storage")
	}
	var files []OutputFile
	err := filepath.WalkDir(outputDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == outputDirectory || entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular output rejected: %s", path)
		}
		if strings.HasSuffix(strings.ToLower(path), ".part") {
			return fmt.Errorf("incomplete transfer output: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		relative, err := filepath.Rel(outputDirectory, path)
		if err != nil {
			return err
		}
		files = append(files, OutputFile{Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size()})
		return nil
	})
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, err
}

func containsFLAC(files []OutputFile) bool {
	for _, file := range files {
		if strings.EqualFold(filepath.Ext(file.Path), ".flac") {
			return true
		}
	}
	return false
}

func processSignal(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return status.Signal().String()
	}
	return ""
}
