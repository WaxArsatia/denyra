package gateway_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/spotiflac"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
)

type staticLocator struct{ value string }

func (locator staticLocator) Resolve(_ context.Context, _, selected string) (string, error) {
	return strings.TrimSuffix(locator.value, "/") + "/" + selected, nil
}

func TestSpotiFLACStrictOutcomesAndTimeoutBoundaries(t *testing.T) {
	installation, base, home := fakeSpotiFLACInstallation(t)
	runner := spotiflac.Runner{
		Runtime:             installation,
		Resolver:            staticLocator{value: "https://example.test"},
		BaseOutputDirectory: base,
		RuntimeHome:         home,
		ProviderTimeout:     60 * time.Millisecond,
		PollInterval:        5 * time.Millisecond,
		TerminationGrace:    20 * time.Millisecond,
		OutputLimit:         1 << 20,
		Concurrency:         2,
	}
	providers := installation.Installation.Manifest.Providers()
	tests := map[string]struct {
		selected string
		state    domain.State
		winner   string
	}{
		"candidate":          {selected: "candidate", state: domain.StateArbitrating, winner: providers[0]},
		"all no result":      {selected: "no-result", state: domain.StateNoCandidate},
		"mixed with error":   {selected: "mixed", state: domain.StateFallbackRetryableError},
		"hung establishment": {selected: "hung", state: domain.StateFallbackRetryableError},
		"long transfer":      {selected: "long-transfer", state: domain.StateArbitrating, winner: providers[0]},
		"path escape":        {selected: "path-escape", state: domain.StateFallbackRetryableError},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			jobID := strings.ReplaceAll(name, " ", "-")
			request := spotiflac.RunRequest{JobID: jobID, ReleaseGroupID: releaseGroupMBID, SelectedRelease: test.selected, OutputDirectory: filepath.Join(base, jobID), Providers: providers, OverallDeadline: time.Now().Add(2 * time.Second)}
			result, err := runner.Run(context.Background(), request)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			state, err := domain.ClassifyFallback(result.DomainResults())
			if err != nil {
				t.Fatalf("ClassifyFallback: %v", err)
			}
			if state != test.state || result.WinningProvider != test.winner {
				t.Fatalf("state=%s winner=%q results=%+v", state, result.WinningProvider, result.Providers)
			}
			if test.selected == "long-transfer" && result.Providers[0].EstablishedAt == nil {
				t.Fatal("long transfer never recorded request establishment")
			}
			if test.selected == "mixed" && len(result.Providers) != len(providers) {
				t.Fatalf("mixed result did not preserve every provider attempt: %+v", result.Providers)
			}
		})
	}
}

func TestSpotiFLACRejectsProviderAndOutputOverrides(t *testing.T) {
	installation, base, home := fakeSpotiFLACInstallation(t)
	runner := spotiflac.Runner{Runtime: installation, Resolver: staticLocator{value: "https://example.test"}, BaseOutputDirectory: base, RuntimeHome: home, ProviderTimeout: time.Second, PollInterval: time.Millisecond, TerminationGrace: time.Millisecond, OutputLimit: 1 << 20, Concurrency: 2}
	request := spotiflac.RunRequest{JobID: "job", ReleaseGroupID: releaseGroupMBID, SelectedRelease: "candidate", OutputDirectory: filepath.Join(base, "job"), Providers: []string{"ext:unapproved"}, OverallDeadline: time.Now().Add(time.Second)}
	if _, err := runner.Run(context.Background(), request); err == nil {
		t.Fatal("unapproved provider accepted")
	}
	request.Providers = installation.Installation.Manifest.Providers()
	request.OutputDirectory = filepath.Join(base, "..", "library")
	if _, err := runner.Run(context.Background(), request); err == nil {
		t.Fatal("output path escape accepted")
	}
}

func TestSpotiFLACManifestFailsClosedOnArtifactChange(t *testing.T) {
	verified, _, _ := fakeSpotiFLACInstallation(t)
	installation := verified.Installation
	artifact := filepath.Join(installation.ArtifactDirectory, installation.Manifest.Extensions[0].ID+".sflx")
	if err := os.WriteFile(artifact, []byte("changed"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := installation.Verify(context.Background(), time.Second, time.Now()); err == nil {
		t.Fatal("modified extension artifact passed verification")
	}
}

func TestSpotiFLACManifestRejectsUnexpectedOrModifiedInstalledExtension(t *testing.T) {
	verified, _, _ := fakeSpotiFLACInstallation(t)
	installation := verified.Installation
	if err := os.Mkdir(filepath.Join(installation.InstalledExtensionDirectory, "rogue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installation.Verify(context.Background(), time.Second, time.Now()); err == nil {
		t.Fatal("unexpected installed extension passed verification")
	}
	if err := os.Remove(filepath.Join(installation.InstalledExtensionDirectory, "rogue")); err != nil {
		t.Fatal(err)
	}
	entryPoint := filepath.Join(installation.InstalledExtensionDirectory, installation.Manifest.Extensions[0].ID, "index.js")
	if err := os.Chmod(entryPoint, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPoint, []byte("changed"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := installation.Verify(context.Background(), time.Second, time.Now()); err == nil {
		t.Fatal("modified installed extension passed verification")
	}
}

func TestSpotiFLACMusicBrainzLocatorIsReleaseBoundAndDeterministic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ws/2/release/"+releaseMBID || request.URL.Query().Get("inc") != "url-rels" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"id":%q,"relations":[{"url":{"resource":"https://tidal.com/browse/album/2"}},{"url":{"resource":"https://open.spotify.com/album/1"}}]}`, releaseMBID)
	}))
	defer server.Close()
	resolver := spotiflac.MusicBrainzResolver{BaseURL: server.URL, UserAgent: "Denyra/test contact@example.invalid", HTTP: server.Client(), ResponseLimit: 1 << 20}
	locator, err := resolver.Resolve(context.Background(), releaseGroupMBID, releaseMBID)
	if err != nil {
		t.Fatal(err)
	}
	if locator != "https://open.spotify.com/album/1" {
		t.Fatalf("locator=%q", locator)
	}
}

func TestSpotiFLACCancellationTerminatesTheProcessGroup(t *testing.T) {
	installation, base, home := fakeSpotiFLACInstallation(t)
	runner := spotiflac.Runner{Runtime: installation, Resolver: staticLocator{value: "https://example.test"}, BaseOutputDirectory: base, RuntimeHome: home, ProviderTimeout: time.Second, PollInterval: 5 * time.Millisecond, TerminationGrace: 20 * time.Millisecond, OutputLimit: 1 << 20, Concurrency: 2}
	ctx, cancel := context.WithCancel(context.Background())
	request := spotiflac.RunRequest{JobID: "cancel", ReleaseGroupID: releaseGroupMBID, SelectedRelease: "cancel", OutputDirectory: filepath.Join(base, "cancel"), Providers: installation.Installation.Manifest.Providers(), OverallDeadline: time.Now().Add(2 * time.Second)}
	done := make(chan spotiflac.RunResult, 1)
	go func() {
		result, _ := runner.Run(ctx, request)
		done <- result
	}()
	pidPath := filepath.Join(request.OutputDirectory, "child.pid")
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake engine child PID was not published")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	result := <-done
	if len(result.Providers) == 0 || result.Providers[0].Outcome != domain.OutcomeRetryableError || result.Providers[0].ErrorClass != "CANCELLED" {
		t.Fatalf("cancel result=%+v", result.Providers)
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	processDeadline := time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(processDeadline) {
			t.Fatalf("child process %d survived group cancellation: %v", pid, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fakeSpotiFLACInstallation(t *testing.T) (spotiflac.VerifiedInstallation, string, string) {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "downloads")
	home := filepath.Join(root, "home")
	artifacts := filepath.Join(root, "artifacts")
	installed := filepath.Join(home, ".spotiflac", "extensions")
	for _, directory := range []string{base, artifacts, installed} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	engine := filepath.Join(root, "spotiflac")
	engineScript := `#!/bin/sh
input="$1"
output="$2"
provider=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--service" ]; then provider="$2"; break; fi
  shift
done
case "$input" in
  */candidate) printf 'fake-flac' > "$output/track.flac" ;;
  */no-result) printf 'DENYRA_SPOTIFLAC_RESULT={"outcome":"NO_RESULT"}\n' ;;
  */mixed)
    if [ "$provider" = "ext:qobuz-web" ]; then printf 'ERROR: provider network failure\n' >&2; exit 1; fi
    printf 'DENYRA_SPOTIFLAC_RESULT={"outcome":"NO_RESULT"}\n'
    ;;
  */hung) sleep 10 ;;
  */long-transfer) touch "$output/track.part"; sleep 0.12; mv "$output/track.part" "$output/track.flac" ;;
  */path-escape) ln -s /etc/passwd "$output/track.flac" ;;
  */cancel) sleep 10 & child=$!; printf '%s\n' "$child" > "$output/child.pid"; wait "$child" ;;
  *) printf 'ERROR: unknown test input\n' >&2; exit 2 ;;
esac
`
	writeExecutable(t, engine, engineScript)
	node := filepath.Join(root, "node")
	writeExecutable(t, node, "#!/bin/sh\nprintf 'v24.19.0\\n'\n")
	manifest := spotiflac.RuntimeManifest{EngineVersion: "3.0.8-test", EngineSHA256: fileSHA256(t, engine), RegistryCommit: spotiflac.RegistryCommit, NodeVersion: "24.19.0", NodeArtifactSHA256: strings.Repeat("a", 64)}
	for _, identity := range []struct{ id, version string }{{"tidal-web", "1.1.7"}, {"qobuz-web", "1.1.0"}, {"deezer", "1.2.0"}} {
		extension := spotiflac.ExtensionIdentity{ID: identity.id, Version: identity.version, MinAppVersion: "4.7.0", RequiredRuntimeFeatures: []string{"signedSession@1", "sessionGrant@1"}}
		archive := filepath.Join(artifacts, identity.id+".sflx")
		manifestJSON := fmt.Sprintf(`{"name":%q,"version":%q,"minAppVersion":"4.7.0","requiredRuntimeFeatures":["signedSession@1","sessionGrant@1"],"type":["download_provider"]}`, identity.id, identity.version)
		writeExtensionArchive(t, archive, manifestJSON)
		extension.SHA256 = fileSHA256(t, archive)
		manifest.Extensions = append(manifest.Extensions, extension)
		directory := filepath.Join(installed, identity.id)
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(manifestJSON), 0o444); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "index.js"), []byte("module.exports = {}"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	provenance := map[string]any{"artifacts": []map[string]string{{"id": "spotiflac-engine", "version": manifest.EngineVersion, "sha256": manifest.EngineSHA256}, {"id": "node-runtime", "version": manifest.NodeVersion, "sha256": manifest.NodeArtifactSHA256}}, "registries": []map[string]string{{"id": "spotiflac-extension-registry", "commit": manifest.RegistryCommit}}}
	for _, extension := range manifest.Extensions {
		provenance["artifacts"] = append(provenance["artifacts"].([]map[string]string), map[string]string{"id": "spotiflac-ext-" + extension.ID, "version": extension.Version, "sha256": extension.SHA256})
	}
	data, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	provenancePath := filepath.Join(root, "build-provenance.json")
	if err := os.WriteFile(provenancePath, data, 0o444); err != nil {
		t.Fatal(err)
	}
	installation := spotiflac.Installation{EnginePath: engine, NodePath: node, ArtifactDirectory: artifacts, InstalledExtensionDirectory: installed, BuildProvenancePath: provenancePath, Manifest: manifest}
	verified, err := installation.Verify(context.Background(), time.Second, time.Now())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return verified, base, home
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeExtensionArchive(t *testing.T, path, manifest string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, body := range map[string]string{"manifest.json": manifest, "index.js": "module.exports = {}"} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
