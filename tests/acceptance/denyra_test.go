package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/health"
	"github.com/waxarsatia/denyra/internal/platform/storage"
	"github.com/waxarsatia/denyra/tests/acceptance/harness"
)

func TestSetupReconcilesEmptyDeploymentAndRerunsCleanly(t *testing.T) {
	if os.Getenv("DENYRA_ACCEPTANCE_COMPOSE") != "1" {
		t.Skip("set DENYRA_ACCEPTANCE_COMPOSE=1 to run setup acceptance")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, path := range []string{"acceptance", filepath.Join("data", "library", "fixture-artist", "fixture-album")} {
		if err := os.MkdirAll(filepath.Join(home, path), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	libraryFixture := filepath.Join(home, "data", "library", "fixture-artist", "fixture-album", "keep.txt")
	if err := os.WriteFile(libraryFixture, []byte("keep-library-content\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("denyra-setup-acceptance-%d", os.Getpid())
	override := filepath.Join(repository, "deploy", "compose.acceptance.yaml")
	environment := append(os.Environ(),
		"DENYRA_HOME="+home,
		"DENYRA_PROJECT_NAME="+project,
		"DENYRA_COMPOSE_OVERRIDE="+override,
		"DENYRA_SOULSEEK_USERNAME=acceptance-disabled",
		"DENYRA_SOULSEEK_PASSWORD=acceptance-disabled",
		"DENYRA_WAIT_SECONDS=240",
	)
	t.Cleanup(func() {
		envFile := filepath.Join(home, "config", "denyra.env")
		if _, err := os.Stat(envFile); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "docker", "compose", "--project-name", project, "--env-file", envFile, "-f", filepath.Join(repository, "deploy", "compose.yaml"), "-f", override, "down", "--volumes", "--remove-orphans")
		command.Dir = repository
		command.Env = environment
		_ = command.Run()
		cleanup := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", home+":/cleanup", "debian:stable-slim", "sh", "-c", fmt.Sprintf("chown -R %d:%d /cleanup && chmod -R u+rwX /cleanup", os.Getuid(), os.Getgid()))
		_ = cleanup.Run()
	})

	runSetupAcceptance(t, repository, home, project, override, environment)
	first := readSetupEvidence(t, filepath.Join(home, "acceptance", "state.json"))
	if first.LidarrMutations == 0 || first.NavidromeCreates != 1 || first.SFTPGoCreates != 1 {
		t.Fatalf("first reconciliation evidence=%+v", first)
	}
	runSetupAcceptance(t, repository, home, project, override, environment)
	second := readSetupEvidence(t, filepath.Join(home, "acceptance", "state.json"))
	if second.LidarrMutations != first.LidarrMutations || second.NavidromeCreates != 1 || second.SFTPGoCreates != 1 {
		t.Fatalf("rerun was not idempotent: first=%+v second=%+v", first, second)
	}
	content, err := os.ReadFile(libraryFixture)
	if err != nil || string(content) != "keep-library-content\n" {
		t.Fatalf("library fixture changed: %q err=%v", content, err)
	}
}

func TestFailedUpdateRestoresPriorStateAndImages(t *testing.T) {
	if os.Getenv("DENYRA_ACCEPTANCE_COMPOSE") != "1" {
		t.Skip("set DENYRA_ACCEPTANCE_COMPOSE=1 to run update acceptance")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	remote := filepath.Join(temporary, "remote.git")
	working := filepath.Join(temporary, "working")
	candidate := filepath.Join(temporary, "candidate")
	runAcceptanceGit(t, "init", "--bare", remote)
	runAcceptanceGit(t, "-C", remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runAcceptanceGit(t, "clone", repository, working)
	runAcceptanceGit(t, "-C", working, "remote", "set-url", "origin", remote)
	runAcceptanceGit(t, "-C", working, "push", "-u", "origin", "HEAD:main")

	home := filepath.Join(temporary, "home")
	if err := os.MkdirAll(filepath.Join(home, "data", "library", "fixture-artist", "fixture-album"), 0o750); err != nil {
		t.Fatal(err)
	}
	libraryPath := filepath.Join(home, "data", "library", "fixture-artist", "fixture-album", "keep.txt")
	libraryBytes := []byte("update-rollback-library\n")
	if err := os.WriteFile(libraryPath, libraryBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("denyra-update-acceptance-%d", os.Getpid())
	override := filepath.Join(working, "deploy", "compose.acceptance.yaml")
	environment := append(os.Environ(),
		"DENYRA_HOME="+home,
		"DENYRA_PROJECT_NAME="+project,
		"DENYRA_COMPOSE_OVERRIDE="+override,
		"DENYRA_SOULSEEK_USERNAME=acceptance-disabled",
		"DENYRA_SOULSEEK_PASSWORD=acceptance-disabled",
		"DENYRA_WAIT_SECONDS=240",
	)
	t.Cleanup(func() {
		envFile := filepath.Join(home, "config", "denyra.env")
		if _, err := os.Stat(envFile); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "docker", "compose", "--project-name", project, "--env-file", envFile, "-f", filepath.Join(working, "deploy", "compose.yaml"), "-f", override, "down", "--volumes", "--remove-orphans")
			command.Env = environment
			_ = command.Run()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanup := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", home+":/cleanup", "debian:stable-slim", "sh", "-c", fmt.Sprintf("chown -R %d:%d /cleanup && chmod -R u+rwX /cleanup", os.Getuid(), os.Getgid()))
		_ = cleanup.Run()
	})

	runSetupAcceptance(t, working, home, project, override, environment)
	statePaths := map[string]string{
		"acquisition-gateway": "gateway", "media-pipeline": "pipeline", "lidarr": "lidarr",
		"slskd": "slskd", "sftpgo": "sftpgo", "navidrome": "navidrome",
	}
	for service, directory := range statePaths {
		path := filepath.Join(home, "data", "state", directory, "update-sentinel")
		if err := os.WriteFile(path, []byte("prior-"+service+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	priorImages := acceptanceImageIDs(t, working, home, project, override, environment, statePaths)

	runAcceptanceGit(t, "clone", remote, candidate)
	runAcceptanceGit(t, "-C", candidate, "config", "user.name", "Denyra Acceptance")
	runAcceptanceGit(t, "-C", candidate, "config", "user.email", "acceptance@denyra.invalid")
	if err := harness.InjectUpdateHealthFailure(filepath.Join(candidate, "deploy", "compose.acceptance.yaml")); err != nil {
		t.Fatal(err)
	}
	runAcceptanceGit(t, "-C", candidate, "add", "deploy/compose.acceptance.yaml")
	runAcceptanceGit(t, "-C", candidate, "commit", "-m", "test: inject candidate health failure")
	runAcceptanceGit(t, "-C", candidate, "push", "origin", "main")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	update := exec.CommandContext(ctx, filepath.Join(working, "denyra"), "update")
	update.Dir = working
	update.Env = append(environment, "DENYRA_ACCEPTANCE_FAIL_HEALTH=1", "DENYRA_WAIT_SECONDS=30")
	output, err := update.CombinedOutput()
	if err == nil {
		t.Fatalf("faulted update succeeded:\n%s", output)
	}

	for service, directory := range statePaths {
		path := filepath.Join(home, "data", "state", directory, "update-sentinel")
		content, readErr := os.ReadFile(path)
		if readErr != nil || string(content) != "prior-"+service+"\n" {
			t.Errorf("state %s=%q err=%v", service, content, readErr)
		}
	}
	afterImages := acceptanceImageIDs(t, working, home, project, override, environment, statePaths)
	for service, prior := range priorImages {
		if afterImages[service] != prior {
			t.Errorf("image %s=%s want %s", service, afterImages[service], prior)
		}
	}
	if content, readErr := os.ReadFile(libraryPath); readErr != nil || string(content) != string(libraryBytes) {
		t.Fatalf("library changed: %q err=%v", content, readErr)
	}
	snapshot := onlyUpdateSnapshot(t, filepath.Join(home, "updates"))
	metadata, err := os.ReadFile(filepath.Join(snapshot, "metadata.env"))
	if err != nil || !strings.Contains(string(metadata), "status=rolled_back\n") {
		t.Fatalf("snapshot metadata=%q err=%v", metadata, err)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "failed-state")); err != nil {
		t.Fatalf("failed candidate state not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "library")); !os.IsNotExist(err) {
		t.Fatalf("library appeared in update snapshot: %v", err)
	}
}

func runAcceptanceGit(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}

func acceptanceImageIDs(t *testing.T, repository, home, project, override string, environment []string, services map[string]string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(services))
	for service := range services {
		container := acceptanceDockerOutput(t, repository, environment, "compose", "--project-name", project, "--env-file", filepath.Join(home, "config", "denyra.env"), "-f", filepath.Join(repository, "deploy", "compose.yaml"), "-f", override, "ps", "-q", service)
		result[service] = acceptanceDockerOutput(t, repository, environment, "inspect", "--format", "{{.Image}}", container)
	}
	return result
}

func acceptanceDockerOutput(t *testing.T, repository string, environment []string, arguments ...string) string {
	t.Helper()
	command := exec.Command("docker", arguments...)
	command.Dir = repository
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func onlyUpdateSnapshot(t *testing.T, updates string) string {
	t.Helper()
	entries, err := os.ReadDir(updates)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".pending-") {
			snapshots = append(snapshots, filepath.Join(updates, entry.Name()))
		}
	}
	if len(snapshots) != 1 {
		t.Fatalf("update snapshots=%v", snapshots)
	}
	return snapshots[0]
}

type setupEvidence struct {
	LidarrMutations  int `json:"lidarr_mutations"`
	NavidromeCreates int `json:"navidrome_creates"`
	SFTPGoCreates    int `json:"sftpgo_creates"`
}

func runSetupAcceptance(t *testing.T, repository, home, project, override string, environment []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(repository, "denyra"), "setup")
	command.Dir = repository
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		diagnostic := exec.Command("docker", "compose", "--project-name", project, "--env-file", filepath.Join(home, "config", "denyra.env"), "-f", filepath.Join(repository, "deploy", "compose.yaml"), "-f", override, "logs", "--no-color", "--tail", "120", "lidarr", "slskd", "sftpgo", "navidrome")
		diagnostic.Dir = repository
		diagnostic.Env = environment
		logs, _ := diagnostic.CombinedOutput()
		t.Fatalf("setup acceptance failed: %v\n%s\nservice logs:\n%s", err, output, logs)
	}
}

func readSetupEvidence(t *testing.T, path string) setupEvidence {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var evidence setupEvidence
	if err := json.Unmarshal(content, &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestDenyraPinnedComposeStartsReadyWithLocalAdapters(t *testing.T) {
	stack := harness.StartCompose(t)
	stack.AssertReady(t)
}

func TestDenyraAcquisitionRetryArbitrationAndFaultPolicies(t *testing.T) {
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	policy := domain.RetryPolicy{
		Primary:     []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour},
		Fallback:    []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour},
		NoCandidate: 24 * time.Hour,
	}
	deadline, err := policy.NoCandidateDeadline(now)
	if err != nil || !deadline.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("no-candidate deadline=%s err=%v", deadline, err)
	}
	state, err := domain.ClassifyFallback([]domain.ProviderResult{
		{Provider: "tidal", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"},
		{Provider: "qobuz", Outcome: domain.OutcomeRetryableError, Evidence: "network"},
	})
	if err != nil || state != domain.StateFallbackRetryableError {
		t.Fatalf("operational fallback error became %s err=%v", state, err)
	}
	state, err = domain.ClassifyFallback([]domain.ProviderResult{
		{Provider: "tidal", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"},
		{Provider: "qobuz", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"},
		{Provider: "deezer", Outcome: domain.OutcomeLegitimateNoResult, Evidence: "none"},
	})
	if err != nil || state != domain.StateNoCandidate {
		t.Fatalf("legitimate no-result state=%s err=%v", state, err)
	}

	quality := domain.Quality{IdentityRank: 10, EditionRank: 8, SourceConfidence: 7, BitDepth: 24, SampleRate: 96000}
	decision, ready := domain.DecideArbitration([]domain.ApprovedCandidate{
		{ID: "fallback", Source: domain.CandidateSourceSpotiFLAC, ApprovedAt: now, CompletionAt: now, Quality: quality, NonBlockingWarningCount: 0},
		{ID: "primary", Source: domain.CandidateSourceSlskd, ApprovedAt: now.Add(time.Minute), CompletionAt: now.Add(time.Minute), Quality: quality, NonBlockingWarningCount: 20},
	}, now.Add(30*time.Minute), now.Add(2*time.Minute))
	if !ready || decision.Winner.ID != "primary" || len(decision.Losers) != 1 || decision.Reason != domain.DecisionProvenancePriority {
		t.Fatalf("dual-candidate decision=%+v ready=%t", decision, ready)
	}

	faults := harness.NewFaultBoundary()
	faults.Fail("after-effect-intent")
	if err := faults.Invoke("after-effect-intent"); err == nil {
		t.Fatal("injected boundary did not fail")
	}
	faults.Clear("after-effect-intent")
	if err := faults.Invoke("after-effect-intent"); err != nil || faults.Calls["after-effect-intent"] != 2 {
		t.Fatalf("fault recovery calls=%v err=%v", faults.Calls, err)
	}
}

func TestDenyraStorageAndExternalOutageMatrix(t *testing.T) {
	admission, err := storage.Evaluate("/data", 20<<30, 5, func(string) (storage.Capacity, error) {
		return storage.Capacity{AvailableBytes: 19 << 30, TotalBytes: 100 << 30, Device: 42}, nil
	})
	if err != nil || admission.Allowed || admission.RequiredBytes != 20<<30 {
		t.Fatalf("low-storage admission=%+v err=%v", admission, err)
	}
	service := health.New()
	service.Set(contracts.DependencyHealth{Name: "database", State: contracts.DependencyOK, Local: true})
	for _, name := range []string{"musicbrainz", "lrclib", "soulseek", "spotiflac-providers"} {
		service.Set(contracts.DependencyHealth{Name: name, State: contracts.DependencyDegraded, Details: "acceptance outage", Local: false})
	}
	if snapshot := service.Snapshot(); !snapshot.Ready || !snapshot.Live {
		t.Fatalf("external outage blocked local readiness: %+v", snapshot)
	}
}

func TestDenyraSyntheticFLACStreamingPreservesMasterAndSidecars(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is required for generated non-copyrighted acceptance media")
	}
	library := t.TempDir()
	track := harness.SyntheticFLAC(t, filepath.Join(library, "Artist", "Album"), "01 - Synthetic")
	lyrics := track[:len(track)-len(filepath.Ext(track))] + ".lrc"
	if err := os.WriteFile(lyrics, []byte("[00:00.00] synthetic acceptance tone\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(track), "folder.jpg"), []byte("synthetic-artwork-evidence"), 0o440); err != nil {
		t.Fatal(err)
	}
	before := harness.SHA256(t, track)
	server := httptest.NewServer(http.FileServer(http.Dir(library)))
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/Artist/Album/01%20-%20Synthetic.flac", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=0-1023")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || (response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusOK) {
		t.Fatalf("stream status=%d read=%v close=%v", response.StatusCode, readErr, closeErr)
	}
	if after := harness.SHA256(t, track); before != after {
		t.Fatal("streaming changed the FLAC master")
	}
	for _, path := range []string{lyrics, filepath.Join(filepath.Dir(track), "folder.jpg")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDenyraIntegratedFailureMatrix(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pattern := "Primary|Fallback|Correlation|Arbitration|Handoff|Claim|ReleaseMatching|TechnicalValidation|Mutation|Enrichment|Import|Recovery|AdminUI|Persistence|Admission|Backup|Restore|Navidrome"
	command := exec.Command("go", "test", "-count=1", "./tests/integration/gateway", "./tests/integration/pipeline", "./tests/integration/operations", "-run", pattern)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("integrated failure matrix failed: %v\n%s", err, output)
	}
}
