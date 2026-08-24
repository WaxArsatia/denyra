# Simplified Build and Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove exact deployment locks, source-built runtimes, and digest-bound Compose configuration while preserving Denyra's runtime safety checks and media ownership boundaries.

**Architecture:** Docker Compose becomes the only image build model. Custom Go services compile in `golang:1.27`, the gateway runs in `node:24-slim`, and the pipeline runs in `python:3.14-slim`; derived Lidarr and Navidrome images fetch their current compatible plugins during each refreshed build. Runtime readiness validates executable behavior and provider compatibility, not exact hashes or provenance documents.

**Tech Stack:** Go 1.27, POSIX shell, Docker Engine, Docker Compose v2, official Node 24 and Python 3.14 images, GitHub Actions.

**Spec:** `plans/001-simplified-local-deployment-design.md`

## Global Constraints

- Keep exact Go module versions and checksums in `go.mod` and `go.sum`.
- Track `golang:1.27`, `python:3.14-slim`, and `node:24-slim`; do not pin patch releases or image digests.
- Install Beets from the current compatible major line with `beets>=2,<3`.
- Follow `nightly` for Lidarr and current stable tags for Navidrome, slskd, SFTPGo, and Restic.
- Do not compile CPython or manually download and unpack Node.
- Do not retain `dependencies.lock.json`, `deploy/images.lock.json`, generated build provenance, a Debian snapshot, exact APT versions, or pin-verification scripts.
- Preserve Lidarr as the only writer of the final library; Navidrome and the pipeline keep read-only final-library mounts.
- Preserve configuration, secret, filesystem, migration, required-binary, Lidarr import, and SpotiFLAC provider validation.
- This plan is phase 1 of 3. Complete it before `2026-08-24-one-command-setup-implementation.md`.

---

## File map

- `internal/platform/servicehost/host.go`: prepare a service without dependency-lock or provenance inputs and record only canonical configuration snapshots.
- `cmd/acquisition-gateway/main.go`, `cmd/media-pipeline/main.go`: remove obsolete flags and pass service-name health addresses.
- `internal/gateway/adapters/spotiflac/manifest.go`: discover installed provider manifests and observable runtime versions at startup.
- `internal/gateway/adapters/spotiflac/runner.go`: consume the discovered manifest without exact identity assumptions.
- `deploy/docker/*.Dockerfile`: build against compatible runtime lines and fetch current release assets.
- `deploy/compose.yaml`: use build definitions and movable upstream tags without static addresses or platform declarations.
- `deploy/config/gateway.toml`, `deploy/config/pipeline.toml`: route internal traffic by Compose service name.
- `tests/integration/compose_config_test.go`, `tests/integration/service_images_test.go`: test behavior, topology, mounts, ports, binaries, and plugins.
- `.github/workflows/ci.yml`, `Makefile`: retain development gates while deleting lock/provenance gates.
- Files listed for deletion in Task 6 have no remaining responsibility.

### Task 1: Remove dependency identity from service startup

**Files:**
- Modify: `internal/platform/servicehost/host.go`
- Modify: `internal/platform/servicehost/host_test.go`
- Modify: `cmd/acquisition-gateway/main.go`
- Modify: `cmd/media-pipeline/main.go`
- Test: `internal/platform/servicehost/host_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.NewSnapshot`, `denysqlite.Open`, and `denysqlite.Migrate` exactly as they exist.
- Produces: `servicehost.Options{Name, ConfigPath, DatabasePath, Migrations, RequiredBinaries, CheckFilesystem, ExternalDependencies, ...}` with no `LockPath` or `ProvenancePath`; `recordStartup(ctx, db, snapshot, now)` records only `config_snapshots`.

- [ ] **Step 1: Rewrite the service-host test around the retained startup contract**

Replace `TestPreparePersistsImmutableConfigAndBuildProvenance` with:

```go
func TestPreparePersistsImmutableConfigWithoutDeploymentIdentity(t *testing.T) {
	options := makeOptions(t)
	prepared, err := servicehost.Prepare(context.Background(), slog.Default(), options)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer prepared.Close()

	var configCount int
	if err := prepared.DB.QueryRow("SELECT COUNT(*) FROM config_snapshots").Scan(&configCount); err != nil {
		t.Fatalf("count config snapshots: %v", err)
	}
	if configCount != 1 || !prepared.Health.Snapshot().Ready {
		t.Fatalf("prepared runtime: config=%d health=%+v", configCount, prepared.Health.Snapshot())
	}
	for _, dependency := range prepared.Health.Snapshot().Dependencies {
		if dependency.Name == "dependency-lock" {
			t.Fatal("dependency-lock remained in runtime health")
		}
	}
}
```

Delete `TestPrepareRejectsInvalidPin`, the lock/provenance fixture writes, and the `build_provenance` table from `foundationSQL`. Keep the existing missing-binary, filesystem, migration, graceful-shutdown, and duplicate-config-snapshot assertions.

- [ ] **Step 2: Run the focused test and verify the old API fails it**

Run: `go test ./internal/platform/servicehost -run 'Prepare|Run' -count=1`

Expected: FAIL because startup still requires lock/provenance paths and still publishes `dependency-lock` health.

- [ ] **Step 3: Remove lock and provenance handling from the service host**

Make these exact structural changes:

```go
type Options struct {
	Name                    string
	ConfigPath              string
	DatabasePath            func(config.Config) string
	Migrations              []denysqlite.Migration
	RequiredBinaries        []string
	CheckFilesystem         func(config.Config) error
	ExternalDependencies    []string
	ServeAdmin              bool
	Initialize              func(context.Context, *Prepared) error
	BuildInternalHandler    func(*Prepared) (http.Handler, error)
	BuildAcquisitionHandler func(*Prepared) (http.Handler, error)
	BuildAdminHandler       func(*Prepared) (http.Handler, error)
	Now                     func() time.Time
}
```

Call `recordStartup(ctx, db, snapshot, now())`, set local health only for `configuration`, `required-binaries`, `filesystem`, `database`, and `migrations`, and validate only `Name`, `ConfigPath`, `DatabasePath`, and nonempty migrations. Delete `verifyLockIdentity`; reduce `recordStartup` to the `config_snapshots` insert in one transaction. Add `git_commit` to the existing `service foundation ready` log from `DENYRA_GIT_COMMIT`, defaulting to `unknown`. Remove unused crypto/JSON/deplock imports.

- [ ] **Step 4: Remove obsolete command flags and call-site fields**

In both commands, retain only:

```go
flags := flag.NewFlagSet("service-name", flag.ContinueOnError)
configPath := flags.String("config", "/etc/denyra/config.toml", "configuration file")
```

Delete `--lock`, `--provenance`, and the matching `Options` fields. In the gateway, temporarily replace lock-derived slskd identity with `EngineVersion: "slskd"`; this is an observable provider label, not a version pin. Task 2 will supply discovered SpotiFLAC runtime values.

- [ ] **Step 5: Run service-host and command tests**

Run: `go test ./internal/platform/servicehost ./cmd/acquisition-gateway ./cmd/media-pipeline -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the runtime decoupling**

```bash
git add internal/platform/servicehost/host.go internal/platform/servicehost/host_test.go cmd/acquisition-gateway/main.go cmd/media-pipeline/main.go
git commit -m "refactor(runtime): remove deployment identity gate"
```

### Task 2: Discover SpotiFLAC compatibility at runtime

**Files:**
- Modify: `internal/gateway/adapters/spotiflac/manifest.go`
- Modify: `internal/gateway/adapters/spotiflac/runner.go`
- Modify: `internal/gateway/adapters/spotiflac/result.go`
- Modify: `cmd/acquisition-gateway/main.go`
- Modify: `tests/integration/gateway/spotiflac_test.go`
- Test: `tests/integration/gateway/spotiflac_test.go`

**Interfaces:**
- Consumes: installed extension directories containing `manifest.json`, the SpotiFLAC executable, and the Node executable.
- Produces: `Installation{EnginePath, NodePath, InstalledExtensionDirectory}.Verify(ctx, timeout, now) (VerifiedInstallation, error)`; `VerifiedInstallation.Installation.Manifest` contains observed versions, actual hashes for diagnostic evidence, and provider types discovered from installed manifests.

- [ ] **Step 1: Replace exact-identity fixtures with behavior fixtures**

Build fixture manifests with no hardcoded expected version:

```go
manifest := `{
  "name":"tidal-web",
  "version":"9.4.1",
  "minAppVersion":"4.7.0",
  "requiredRuntimeFeatures":["signedSession@1","sessionGrant@1"],
  "type":["download_provider"]
}`
```

Add tests named `TestInstallationDiscoversUsableProviders`, `TestInstallationRejectsNoDownloadProvider`, `TestInstallationRejectsInvalidManifest`, and `TestInstallationRejectsBrokenRuntime`. Assert providers equal `[]string{"ext:tidal-web"}` and observed Node/engine versions are nonempty. Remove assertions that mutate files to provoke SHA, registry, Node patch, provenance, or allowlist mismatches.

- [ ] **Step 2: Run the SpotiFLAC integration test and verify it fails**

Run: `go test ./tests/integration/gateway -run 'SpotiFLAC|Installation' -count=1`

Expected: FAIL because `Installation.Verify` still requires `ExpectedManifest`, exact hashes, and build provenance.

- [ ] **Step 3: Implement manifest discovery**

Retain the public result shape used by the runner, but populate it from the installation:

```go
type ExtensionIdentity struct {
	ID, Version, SHA256, MinAppVersion string
	RequiredRuntimeFeatures            []string
	Types                              []string
}

type RuntimeManifest struct {
	EngineVersion, EngineSHA256 string
	NodeVersion, NodeSHA256     string
	Extensions                  []ExtensionIdentity
}

type Installation struct {
	EnginePath                 string
	NodePath                   string
	InstalledExtensionDirectory string
	Manifest                   RuntimeManifest
}
```

`Verify` must:

1. require a positive timeout;
2. run `node --version` and `spotiflac --help` under the timeout, requiring both commands to exit successfully;
3. walk immediate child directories under `InstalledExtensionDirectory`;
4. decode each `manifest.json` through `map[string]json.RawMessage`, strictly validate the required compatibility fields, and ignore unrelated upstream fields;
5. require nonempty name/version and `download_provider` in `type`;
6. reject duplicate provider names and symlinked/non-regular manifests;
7. parse the engine's informational `Version` line when present and otherwise record `unreported`; hash the observed executables and manifest files for diagnostic result fields without comparing them to constants;
8. sort extensions by ID and require at least one usable provider.

Delete `RegistryCommit`, `ExpectedManifest`, artifact allowlist checks, archive-to-installed byte comparison, and build-provenance verification. Keep path-safety checks around installed files.

- [ ] **Step 4: Feed the discovered manifest into gateway startup**

Use the official Node path and no provenance fields:

```go
installation, err := (spotiflac.Installation{
	EnginePath: "/opt/spotiflac/spotiflac",
	NodePath: "/usr/local/bin/node",
	InstalledExtensionDirectory: "/opt/spotiflac/runtime-home/.spotiflac/extensions",
}).Verify(ctx, time.Duration(prepared.Config.HTTP.ExternalRequestTimeout), time.Now().UTC())
```

Set fallback providers from `installation.Installation.Manifest.Providers()`. Update `runner.go` and `result.go` to rename `NodeArtifactSHA256` to `NodeSHA256` and remove `RegistryCommit`; retain observed engine, Node, extension version, and hash data in `RunResult` for diagnosis. Log `spotiflac_engine_version`, `node_version`, and the sorted provider/version list once after verification. These values are diagnostic and never determine readiness beyond executable/provider checks.

- [ ] **Step 5: Run focused and package tests**

Run: `go test ./internal/gateway/adapters/spotiflac ./tests/integration/gateway ./cmd/acquisition-gateway -count=1`

Expected: PASS.

- [ ] **Step 6: Commit behavior-based provider validation**

```bash
git add internal/gateway/adapters/spotiflac cmd/acquisition-gateway/main.go tests/integration/gateway/spotiflac_test.go
git commit -m "refactor(gateway): discover provider compatibility at startup"
```

### Task 3: Replace source-built runtimes with official compatible images

**Files:**
- Modify: `deploy/docker/gateway.Dockerfile`
- Modify: `deploy/docker/pipeline.Dockerfile`
- Modify: `tests/integration/service_images_test.go`
- Delete: `deploy/python/requirements.in`
- Delete: `deploy/python/requirements.lock`
- Delete: `deploy/docker/debian.sources`
- Test: `tests/integration/service_images_test.go`

**Interfaces:**
- Consumes: Docker build argument `DENYRA_RELEASE_REFRESH`, the stable SpotiFLAC `latest/download` asset name, and current extension files from the official extension repository.
- Produces: images `denyra/acquisition-gateway:${DENYRA_IMAGE_TAG}` and `denyra/media-pipeline:${DENYRA_IMAGE_TAG}` containing required executables and no embedded dependency identity files.

- [ ] **Step 1: Rewrite Dockerfile source tests around compatibility lines**

Replace exact-provenance assertions with source assertions equivalent to:

```go
func TestApplicationDockerfilesUseOfficialRuntimeLines(t *testing.T) {
	gateway := readDockerfile(t, "gateway.Dockerfile")
	pipeline := readDockerfile(t, "pipeline.Dockerfile")
	for _, fragment := range []string{"FROM golang:1.27", "FROM node:24-slim", "ARG DENYRA_RELEASE_REFRESH"} {
		if !strings.Contains(gateway, fragment) {
			t.Errorf("gateway missing %q", fragment)
		}
	}
	for _, fragment := range []string{"FROM golang:1.27", "FROM python:3.14-slim", "beets>=2,<3"} {
		if !strings.Contains(pipeline, fragment) {
			t.Errorf("pipeline missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"@sha256:", "Python-3.", "node-v", "--require-hashes", "dependencies.lock", "build-provenance", "debian.sources"} {
		if strings.Contains(gateway+pipeline, forbidden) {
			t.Errorf("obsolete strict input remained: %q", forbidden)
		}
	}
}
```

Update runtime smoke fixtures to build image names from `DENYRA_IMAGE_TAG` (default `local`) and keep `DENYRA_TEST_IMAGE_SMOKE=1` as the single opt-in switch. The smoke test must no longer add `--read-only` or a fixed platform, because those controls are intentionally absent from the supported Compose model.

- [ ] **Step 2: Run the source contract and verify it fails**

Run: `go test ./tests/integration -run 'ApplicationDockerfilesUseOfficialRuntimeLines' -count=1`

Expected: FAIL against the Debian/CPython/Node source-build Dockerfiles.

- [ ] **Step 3: Rewrite the gateway Dockerfile**

Use these stages and responsibilities:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.27 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/acquisition-gateway ./cmd/acquisition-gateway

FROM golang:1.27 AS extension-installer
ARG DENYRA_RELEASE_REFRESH=manual
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY scripts/extract-sflx ./scripts/extract-sflx
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && mkdir -p /opt/spotiflac/artifacts /opt/spotiflac/runtime-home/.spotiflac/extensions \
    && curl -fsSL https://github.com/BartolomeoRusso9/SpotiFLAC-Module-Version/releases/latest/download/SpotiFLAC-Linux-x86_64 -o /opt/spotiflac/spotiflac \
    && for provider in tidal-web qobuz-web deezer; do \
         curl -fsSL "https://raw.githubusercontent.com/spotiflacapp/SpotiFLAC-Extension/main/extensions/$provider.sflx" -o "/opt/spotiflac/artifacts/$provider.sflx"; \
         go run ./scripts/extract-sflx -source "/opt/spotiflac/artifacts/$provider.sflx" -destination "/opt/spotiflac/runtime-home/.spotiflac/extensions/$provider"; \
       done

FROM node:24-slim
ARG DENYRA_GIT_COMMIT=unknown
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/acquisition-gateway /app/acquisition-gateway
COPY --from=extension-installer /opt/spotiflac /opt/spotiflac
```

Finish with existing runtime directories, permissions, SpotiFLAC environment, `ENV DENYRA_GIT_COMMIT=$DENYRA_GIT_COMMIT`, `USER 1000:1000`, and the existing entrypoint. Keep `SPOTIFLAC_DISABLE_AUTO_INSTALL=1` and `SPOTIFLAC_DISABLE_AUTO_UPDATE=1` because updates belong to image builds, not runtime mutation.

- [ ] **Step 4: Rewrite the pipeline Dockerfile**

Use `golang:1.27` for all Go binaries and `python:3.14-slim` directly for runtime:

```dockerfile
FROM python:3.14-slim
ARG DENYRA_RELEASE_REFRESH=manual
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates ffmpeg flac \
    && python -m pip install --no-cache-dir 'beets>=2,<3' \
    && python -m pip check \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/media-pipeline /app/media-pipeline
COPY --from=go-builder /out/denyra-restore-check /app/denyra-restore-check
COPY --from=go-builder /out/denyra-acceptance-fixture /app/denyra-acceptance-fixture
ARG DENYRA_GIT_COMMIT=unknown
ENV DENYRA_GIT_COMMIT=$DENYRA_GIT_COMMIT PYTHONNOUSERSITE=1 HOME=/tmp XDG_CONFIG_HOME=/tmp/.config
USER 1000:1000
ENTRYPOINT ["/app/media-pipeline"]
```

Keep `beets>=2,<3` directly beside the pip command as the single compatibility policy. Delete both old requirements files and the Debian source snapshot; a two-line dependency directory adds no value when the image installs one package.

- [ ] **Step 5: Build and smoke-test both application images**

Run:

```bash
docker build --pull --build-arg DENYRA_RELEASE_REFRESH=plan-test -f deploy/docker/gateway.Dockerfile -t denyra/acquisition-gateway:plan-test .
docker build --pull -f deploy/docker/pipeline.Dockerfile -t denyra/media-pipeline:plan-test .
DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=plan-test go test ./tests/integration -run 'ServiceImage|ApplicationDockerfile' -count=1
```

Expected: PASS; gateway reports Node and at least one provider, pipeline runs `python --version`, `beet version`, `ffprobe -version`, `flac --version`, and `metaflac --version`.

- [ ] **Step 6: Commit official runtime images**

```bash
git add deploy/docker/gateway.Dockerfile deploy/docker/pipeline.Dockerfile tests/integration/service_images_test.go
git rm deploy/python/requirements.in deploy/python/requirements.lock deploy/docker/debian.sources
git commit -m "build: use official node and python runtime lines"
```

### Task 4: Make derived images follow upstream release channels

**Files:**
- Modify: `deploy/docker/lidarr.Dockerfile`
- Modify: `deploy/docker/navidrome.Dockerfile`
- Modify: `tests/integration/lidarr_plugin_install_test.go`
- Modify: `tests/integration/service_images_test.go`

**Interfaces:**
- Consumes: Lidarr nightly, Navidrome latest stable, and each plugin repository's latest-release asset.
- Produces: derived `denyra/lidarr:${DENYRA_IMAGE_TAG}` and `denyra/navidrome:${DENYRA_IMAGE_TAG}` images with loadable plugins.

- [ ] **Step 1: Add source tests for channel pins and refreshed downloads**

Assert these allowed references and forbidden forms:

```go
for name, want := range map[string]string{
	"lidarr.Dockerfile": "FROM lscr.io/linuxserver/lidarr:nightly",
	"navidrome.Dockerfile": "FROM deluan/navidrome:latest",
} {
	text := readDockerfile(t, name)
	if !strings.Contains(text, want) || strings.Contains(text, "@sha256:") {
		t.Errorf("%s does not follow its compatible channel", name)
	}
	if !strings.Contains(text, "ARG DENYRA_RELEASE_REFRESH") {
		t.Errorf("%s cannot refresh latest plugin assets", name)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./tests/integration -run 'Derived|PluginInstall|ServiceImage' -count=1`

Expected: FAIL because both images use digests and exact plugin releases.

- [ ] **Step 3: Rewrite the Lidarr plugin acquisition stage**

Use a small Debian stage with unpinned `ca-certificates`, `curl`, and `unzip`. Resolve the latest release through GitHub's stable redirect:

```dockerfile
FROM debian:stable-slim AS plugin
ARG DENYRA_RELEASE_REFRESH=manual
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://github.com/allquiet-hub/Lidarr.Plugin.Slskd/releases/latest/download/Lidarr.Plugin.Slskd.net8.0.zip -o /tmp/plugin.zip \
    && mkdir /plugin && unzip -q /tmp/plugin.zip -d /plugin

FROM lscr.io/linuxserver/lidarr:nightly
```

Keep the existing init script that copies the plugin into persistent Lidarr state. Remove exact-version labels.

- [ ] **Step 4: Rewrite the Navidrome plugin acquisition**

Use `FROM deluan/navidrome:latest`, switch to root for download, declare and reference `ARG DENYRA_RELEASE_REFRESH=manual` in the download `RUN`, and fetch `nd-lyrics.ndp` from the latest-release redirect. Return to `USER 1000:1000`. If the upstream release asset name changes, the build must fail with the URL in its error; do not add a maintained version catalog.

- [ ] **Step 5: Build and smoke-test both derived images**

Run:

```bash
docker build --pull --build-arg DENYRA_RELEASE_REFRESH=plan-test -f deploy/docker/lidarr.Dockerfile -t denyra/lidarr:plan-test .
docker build --pull --build-arg DENYRA_RELEASE_REFRESH=plan-test -f deploy/docker/navidrome.Dockerfile -t denyra/navidrome:plan-test .
DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=plan-test go test ./tests/integration -run 'LidarrPluginInstall|ServiceImage' -count=1
```

Expected: PASS; Lidarr plugin files survive its init hook and Navidrome can read `/plugins/nd-lyrics.ndp` while reporting a version.

- [ ] **Step 6: Commit derived-image channel tracking**

```bash
git add deploy/docker/lidarr.Dockerfile deploy/docker/navidrome.Dockerfile tests/integration/lidarr_plugin_install_test.go tests/integration/service_images_test.go
git commit -m "build: follow current service plugin releases"
```

### Task 5: Simplify Compose topology and image selection

**Files:**
- Modify: `deploy/compose.yaml`
- Modify: `deploy/compose.local.yaml`
- Modify: `deploy/config/gateway.toml`
- Modify: `deploy/config/pipeline.toml`
- Modify: `tests/integration/compose_config_test.go`
- Modify: `tests/integration/operations/health_test.go`
- Modify: `cmd/acquisition-gateway/main.go`
- Modify: `cmd/media-pipeline/main.go`

**Interfaces:**
- Consumes: `DENYRA_IMAGE_TAG` with default `local`, `DENYRA_DATA_ROOT`, `DENYRA_CONFIG_DIR`, `DENYRA_SECRETS_DIR`, and media UID/GID.
- Produces: one default Compose model with dynamic DNS routing and one local override that publishes Lidarr and slskd.

- [ ] **Step 1: Rewrite topology tests to express the retained boundary**

Remove platform, digest, image-lock, static subnet, and static IPv4 assertions. Assert:

```go
func TestComposeUsesServiceDNSAndCompatibleTags(t *testing.T) {
	document := renderCompose(t)
	for _, name := range []string{"acquisition-gateway", "media-pipeline", "lidarr", "slskd", "sftpgo", "navidrome"} {
		service := document.Services[name]
		if service.Platform != "" || strings.Contains(service.Image, "@sha256:") {
			t.Errorf("service %s retained strict image identity", name)
		}
		if len(service.Healthcheck) == 0 {
			t.Errorf("service %s has no healthcheck", name)
		}
	}
	if got := document.Services["acquisition-gateway"].Environment["DENYRA_HTTP_INTERNAL_ADDRESS"]; got != "0.0.0.0:8081" {
		t.Fatalf("gateway listener = %q", got)
	}
	if got := document.Services["media-pipeline"].Environment["DENYRA_HTTP_INTERNAL_ADDRESS"]; got != "0.0.0.0:8081" {
		t.Fatalf("pipeline listener = %q", got)
	}
}
```

Retain tests for health checks, internal control network membership, published default ports, secret-backed slskd startup, user IDs, and every mount ownership rule.

- [ ] **Step 2: Run Compose tests and verify they fail**

Run: `go test ./tests/integration -run 'Compose|Health' -count=1`

Expected: FAIL on digests, platforms, static addresses, and old listener URLs.

- [ ] **Step 3: Add Compose build declarations and compatible image tags**

For each custom image use this pattern:

```yaml
image: denyra/acquisition-gateway:${DENYRA_IMAGE_TAG:-local}
build:
  context: ..
  dockerfile: deploy/docker/gateway.Dockerfile
  args:
    DENYRA_RELEASE_REFRESH: ${DENYRA_RELEASE_REFRESH:-cached}
    DENYRA_GIT_COMMIT: ${DENYRA_GIT_COMMIT:-unknown}
```

Use `slskd/slskd:latest`, `drakkan/sftpgo:latest`, and `restic/restic:latest`. Use equivalent build declarations for pipeline, Lidarr, and Navidrome. Remove every `platform`, digest, fixed `ipv4_address`, control subnet, state-masking tmpfs, and `read_only` root setting. Keep non-privileged execution, mounts, secrets, profiles, health checks, restart policy, and internal control network.

- [ ] **Step 4: Route custom services through DNS**

Set both custom services' internal listeners to `0.0.0.0:8081`. Health commands and both command binaries' healthcheck default addresses use `127.0.0.1:8081`. Set config URLs to `http://acquisition-gateway:8081` and `http://media-pipeline:8081`; remove all `172.30.0.*` defaults from tracked TOML, Go command defaults, and Compose.

Default published ports remain pipeline `8090`, Navidrome `4533`, SFTPGo WebAdmin `8080`, and SFTP `2022`. Lidarr and slskd remain unpublished except through `deploy/compose.local.yaml`.

- [ ] **Step 5: Validate and test the Compose model**

Run:

```bash
docker compose -f deploy/compose.yaml config --quiet
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml config --quiet
go test ./tests/integration -run 'Compose|Health' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit simplified Compose**

```bash
git add deploy/compose.yaml deploy/compose.local.yaml deploy/config/gateway.toml deploy/config/pipeline.toml cmd/acquisition-gateway/main.go cmd/media-pipeline/main.go tests/integration/compose_config_test.go tests/integration/operations/health_test.go
git commit -m "refactor(deploy): simplify compose image and network model"
```

### Task 6: Delete strict-lock infrastructure and simplify development gates

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Modify: `tests/integration/operations/upgrade_test.go`
- Delete: `dependencies.lock.json`
- Delete: `deploy/images.lock.json`
- Delete: `deploy/docker/docker-bake.hcl`
- Delete: `deploy/docker/generated/gateway-build-provenance.json`
- Delete: `deploy/docker/generated/pipeline-build-provenance.json`
- Delete: `internal/platform/deplock/model.go`
- Delete: `internal/platform/deplock/verify.go`
- Delete: `internal/platform/deplock/verify_test.go`
- Delete: `scripts/verify-pins/build-provenance.sh`
- Delete: `scripts/verify-pins/verify-images.sh`
- Delete: `scripts/verify-pins/verify.sh`
- Delete: `scripts/verify-pins/fixtures/payload.txt`
- Delete: `tests/integration/image_provenance_test.go`

**Interfaces:**
- Consumes: standard Go tooling and `docker compose`.
- Produces: `make verify` as a local code gate and CI jobs that test code, validate Compose, and build all four derived/custom images.

- [ ] **Step 1: Replace lock-centric operation assertions**

Remove `TestUpgradeRejectsFloatingOrMismatchedDependencyIdentity` and the old script-token test from `tests/integration/operations/upgrade_test.go`. Keep migration-ledger rollback tests for phase 3, where update/rollback behavior will replace the script assertions.

- [ ] **Step 2: Run the suite and identify every remaining lock reference**

Run:

```bash
go test ./... -count=1
rg -n 'dependencies\.lock|images\.lock|build-provenance|dependency-lock|verify-pins|deplock|docker buildx bake' --glob '!docs/**' --glob '!plans/**'
```

Expected: tests may still fail and the search lists only files scheduled for deletion or CI/Make targets scheduled for rewrite.

- [ ] **Step 3: Simplify the Makefile**

Use these targets:

```make
.PHONY: fmt fmt-check vet test race compose-config images verify

fmt:
	gofmt -w $$(find cmd internal migrations tests scripts -type f -name '*.go')

fmt-check:
	test -z "$$(gofmt -l $$(find cmd internal migrations tests scripts -type f -name '*.go'))"

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

compose-config:
	docker compose -f deploy/compose.yaml config --quiet

images:
	DENYRA_RELEASE_REFRESH=$$(date -u +%Y%m%dT%H%M%SZ) docker compose -f deploy/compose.yaml build --pull

verify: fmt-check vet race compose-config
```

- [ ] **Step 4: Rewrite CI around supported major action tags**

Use `actions/checkout@v5`, `actions/setup-go@v6` with `go-version: '1.27.x'`, and `docker/setup-buildx-action@v3`. The verify job runs format check, vet, templ regeneration, UI/token checks, `go test -race ./...`, migration checks, Compose config, architecture tests, clean-tree checks, and `git diff --exit-code`. The image job runs:

```yaml
- name: Build service images
  run: DENYRA_RELEASE_REFRESH=ci-${{ github.run_id }} docker compose -f deploy/compose.yaml build --pull
- name: Smoke service images
  run: DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=local go test ./tests/integration -run 'ServiceImage|LidarrPluginInstall' -count=1
```

Do not publish images and do not generate attestations, locks, or provenance files.

- [ ] **Step 5: Delete obsolete files and verify no production reference remains**

Delete every file listed in this task. Then run:

```bash
rg -n 'dependencies\.lock|images\.lock|build-provenance|dependency-lock|verify-pins|deplock|docker buildx bake' --glob '!docs/**' --glob '!plans/**'
```

Expected: no output. Historical specs and plans may retain references because the approved spec explicitly supersedes them.

- [ ] **Step 6: Run the full phase gate**

Run:

```bash
make verify
DENYRA_RELEASE_REFRESH=phase-1 docker compose -f deploy/compose.yaml build --pull
DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=local go test ./tests/integration -run 'Compose|ServiceImage|LidarrPluginInstall' -count=1
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 7: Commit strict-lock removal and CI simplification**

```bash
git add .github/workflows/ci.yml Makefile tests/integration/operations/upgrade_test.go
git add -u
git commit -m "build: remove strict deployment pinning"
```
