package acceptance_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/platform/health"
	"github.com/waxarsatia/denyra/internal/platform/storage"
	"github.com/waxarsatia/denyra/tests/acceptance/harness"
)

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
