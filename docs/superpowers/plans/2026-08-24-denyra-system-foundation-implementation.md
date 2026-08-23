# Denyra System Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish the reproducible Denyra repository, shared Go contracts, typed immutable configuration, persistence primitives, dependency lock, container builds, and Compose security/filesystem boundaries that every later capability relies on.

**Architecture:** Two independently built Go services share only narrow configuration, contract, and platform packages. They use separate SQLite databases and communicate through authenticated JSON HTTP on a private Compose network. All artifacts are locked and verified before images are built; runtime containers are non-root and receive only their approved mounts.

**Tech Stack:** Go 1.27.0, `net/http`, `database/sql`, `mattn/go-sqlite3 v1.14.50`, `pelletier/go-toml/v2 v2.4.3`, `golang.org/x/crypto v0.55.0`, `golang.org/x/text v0.41.0`, `golang.org/x/sys v0.47.0`, SQLite WAL, Docker Engine 29.6.2, Docker Compose v2.40.3, Buildx v0.36.1, Debian snapshot packages, shell verification scripts.

**Spec:** `docs/superpowers/specs/2026-08-24-system-foundation-design.md`

## Global Constraints

- Keep Lidarr as the sole writer of `/data/library`; no shared package may offer a library-write primitive.
- Use `DENYRA_*` for environment overrides. Precedence is compiled defaults, TOML, then environment.
- Keep gateway and pipeline databases separate. Cross-service data moves only through immutable contract DTOs.
- Persist UTC timestamps and absolute deadlines. Never derive a persisted deadline again after restart.
- Never log or snapshot a secret value. Secret audit fingerprints use HMAC with a separate audit key.
- Every dependency change is an explicit `dependencies.lock.json` update followed by compatibility tests.
- Run all repository commands through `rtk` in this workspace.

---

## Dependency Audit Baseline

Audit date: `2026-08-24`. Release status is checked against official project release pages, the Go module proxy, PyPI, npm, Node distribution metadata, Debian release metadata, and Docker release notes. Execution must repeat this audit before changing any pin; it must not update dependencies automatically.

| Dependency group | Selected | Audit result |
| --- | --- | --- |
| Go | `1.27.0` | Latest stable toolchain; official Linux amd64 hash retained |
| Docker Engine | `29.6.2` | Latest stable Engine |
| Docker Compose | `v2.40.3` | Deliberate compatibility exception: latest stable on explicitly approved v2 line; v5 requires separate migration/acceptance |
| Docker Buildx | `v0.36.1` | Latest stable release |
| Debian | `13.6 trixie` | Current stable distribution/point release; packages still lock to one immutable snapshot |
| templ | `v0.3.1020` | Latest stable release |
| HTMX | `2.0.10` | Latest stable 2.x package; v4 releases are beta and outside the approved stable 2.x boundary |
| go-sqlite3 | `v1.14.50` | Latest stable release |
| go-toml/v2 | `v2.4.3` | Latest stable module |
| x/crypto, x/text, x/sys | `v0.55.0`, `v0.41.0`, `v0.47.0` | Latest stable modules |
| CPython | `3.14.7` | Latest stable Python release |
| Beets | `2.13.1` | Latest stable package; exact wheel and transitive graph remain hash-locked |
| Node.js | `24.19.0` | Deliberate compatibility exception: latest LTS, retained instead of Current `26.7.0` for the verified provider set |
| Lidarr | nightly image digest in spec | Deliberate channel exception: plugin support requires nightly; digest is runtime identity |
| Lidarr.Plugin.Slskd | `1.1.3.0` | Latest stable plugin release |
| slskd, SFTPGo, Navidrome, Restic | `0.26.0`, `2.7.5`, `0.63.2`, `0.19.1` | Latest stable releases |
| Navidrome Lyrics Plugin | `7.2.0` | Latest stable release and compatible with Navidrome 0.63.2 |
| SpotiFLAC module | `v3.0.8` | Latest stable canonical engine release |
| SpotiFLAC extensions | pinned registry commit and three versions in acquisition spec | Deliberate compatibility snapshot; never replaced by registry latest at runtime |
| Geist, Phosphor Core | `1.7.2`, `2.0.8` | Latest stable releases |
| Feishin, Tempus | `1.15.1`, `4.25.0` | Latest stable client releases |

---

## File Map

| Path | Responsibility |
| --- | --- |
| `go.mod`, `go.sum` | Pinned Go module graph for both binaries |
| `dependencies.lock.json` | Canonical images, artifacts, platforms, versions, hashes, and registry commits |
| `cmd/acquisition-gateway/main.go` | Gateway composition root only |
| `cmd/media-pipeline/main.go` | Pipeline composition root only |
| `internal/config/` | Typed defaults, TOML/env loading, validation, canonical snapshots |
| `internal/contracts/` | Immutable handoff and health DTOs with golden compatibility tests |
| `internal/platform/clock/` | Injectable UTC clock |
| `internal/platform/ids/` | CSPRNG identifiers and request IDs |
| `internal/platform/httpx/` | Body limits, request IDs, constant-time bearer auth, JSON errors |
| `internal/platform/sqlite/` | Connection setup, migrations, transactions, online backup |
| `internal/platform/fscheck/` | Canonical path, ownership, permission, and device checks |
| `migrations/gateway/` | Gateway migration sequence |
| `migrations/pipeline/` | Pipeline migration sequence |
| `deploy/compose.yaml` | Pinned services, mounts, networks, health checks, optional backup profile |
| `deploy/docker/` | Reproducible images and build contexts |
| `deploy/config/` | Checked-in non-secret configuration examples |
| `deploy/secrets/README.md` | Required secret files and permissions; no secret values |
| `scripts/verify-pins/` | Fail-closed lock and artifact verification |
| `tests/contract/` | DTO compatibility and HTTP boundary tests |
| `tests/integration/` | SQLite, filesystem, and Compose boundary tests |

## Task 1: Bootstrap the Go workspace and package boundaries

**Files:**

- Create: `go.mod`
- Create: `cmd/acquisition-gateway/main.go`
- Create: `cmd/media-pipeline/main.go`
- Create: `internal/gateway/doc.go`
- Create: `internal/pipeline/doc.go`
- Create: `internal/contracts/doc.go`
- Create: `internal/platform/doc.go`
- Test: `internal/architecture/imports_test.go`

- [ ] Write an architecture test that parses imports below `internal/gateway/domain` and `internal/pipeline/domain` and rejects imports containing `/adapters`, `/persistence`, `/transport`, `/adminui`, `database/sql`, or `net/http`.
- [ ] Run `rtk go test ./internal/architecture`; expect failure because the module and packages do not exist.
- [ ] Initialize module `github.com/waxarsatia/denyra` with Go `1.27.0`. Pin direct modules `github.com/a-h/templ v0.3.1020`, `github.com/mattn/go-sqlite3 v1.14.50`, `github.com/pelletier/go-toml/v2 v2.4.3`, `golang.org/x/crypto v0.55.0`, `golang.org/x/text v0.41.0`, and `golang.org/x/sys v0.47.0`; commit the resulting full transitive `go.sum`. Add minimal `main` packages that load no business policy and exit only on composition failure.
- [ ] Add package documentation stating that domain packages may depend only on the standard library and other domain value packages.
- [ ] Run `rtk go test ./...`; expect pass.
- [ ] Commit with `rtk git commit -am "build: bootstrap Denyra Go workspace"` after staging the created files.

The composition roots must retain this shape:

```go
package main

import (
	"context"
	"log/slog"
	"os"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(ctx, logger); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
```

## Task 2: Create the canonical dependency lock and verifier

**Files:**

- Create: `dependencies.lock.json`
- Create: `internal/platform/deplock/model.go`
- Create: `internal/platform/deplock/verify.go`
- Create: `internal/platform/deplock/verify_test.go`
- Create: `scripts/verify-pins/verify.sh`
- Create: `scripts/verify-pins/verify-images.sh`

- [ ] Define strict lock structs with `json.Decoder.DisallowUnknownFields`, canonical image references, `linux/amd64` platform, artifact SHA-256, and registry commit fields.
- [ ] Seed every approved pin from the foundation design, including Docker Engine `29.6.2`, the explicitly retained Compose v2 line at `v2.40.3`, Buildx `v0.36.1`, Debian `13.6` (`trixie`) plus its verified snapshot timestamp/package versions, full Lidarr/slskd/SFTPGo/Navidrome/Restic references, Go, templ, HTMX, SQLite, plugins, Beets/Python lock artifact, Geist, Phosphor, SpotiFLAC, extensions, registry commit, and Node. Pin CPython `3.14.7` from `Python-3.14.7.tar.xz` with SHA-256 `3b48dac8fb59f62eaa67ac83c1eb12bda1b7a08406dd286e252c11a66be27f81`, and Beets wheel `beets-2.13.1-py3-none-any.whl` with SHA-256 `6d74a610b934c8e7b86dc651ec9953b62bd5ec9c47beea10cc32ede41ea9d488`.
- [ ] Write table tests that reject a digest without repository identity, a floating `latest` or bare `nightly` reference, a short hash, wrong platform, duplicate dependency ID, or unknown JSON property.
- [ ] Run `rtk go test ./internal/platform/deplock`; expect the first red test to fail.
- [ ] Implement canonical validation and deterministic lock serialization. Ensure Lidarr's reference contains both `:nightly` metadata and `@sha256:` identity.
- [ ] Make `verify.sh` compare downloaded file hashes without printing URLs containing credentials. Make `verify-images.sh` inspect the manifest platform and digest.
- [ ] Run `rtk go test ./internal/platform/deplock && rtk scripts/verify-pins/verify.sh --offline`; expect pass using checked-in fixture artifacts/metadata only.
- [ ] Commit with message `build: lock and verify external dependencies`.

Use this image identity type:

```go
type Image struct {
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Platform  string `json:"platform"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
}
```

## Task 3: Implement typed centralized configuration

**Files:**

- Create: `internal/config/types.go`
- Create: `internal/config/defaults.go`
- Create: `internal/config/load.go`
- Create: `internal/config/validate.go`
- Create: `internal/config/snapshot.go`
- Create: `internal/config/config_test.go`
- Create: `deploy/config/denyra.example.toml`

- [ ] Define typed nested sections for HTTP, database, filesystem, acquisition, validation, arbitration, sessions, scanners, storage, backup, and concurrency. Use `time.Duration`, byte counts, percentages, and explicit retry slices.
- [ ] Put every policy value from all four designs in `Defaults()`. Business packages must receive policy values through constructors and may not contain duplicated literals.
- [ ] Write failing tests for precedence, exact defaults, unknown TOML/env keys, invalid units, overflow, retry ordering, negative values, contradictory paths, storage boundaries, and `DENYRA_*` override mapping.
- [ ] Implement strict TOML decoding and a finite environment-key registry. Do not accept unknown `DENYRA_*` names silently.
- [ ] Serialize the effective non-secret config as recursively key-sorted canonical JSON and compute SHA-256. Replace each secret with `{source,name}` and optional HMAC fingerprint.
- [ ] Verify two equal configs produce identical bytes/hash, a policy change produces a new hash, and a secret value never occurs in bytes, errors, or logs.
- [ ] Run `rtk go test ./internal/config -count=1`; expect pass.
- [ ] Commit with message `feat: add typed immutable configuration`.

Core policy fields must include:

```go
type Policy struct {
	ScannerRecoveryInterval time.Duration
	StabilityInterval       time.Duration
	AlbumSearchTimeout      time.Duration
	ReconciliationPoll      time.Duration
	PrimaryGraceWindow      time.Duration
	ArbitrationWindow       time.Duration
	SessionAbsoluteExpiry   time.Duration
	MinimumFreeBytes        int64
	MinimumFreePercent      float64
	PrimaryRetry            []time.Duration
	FallbackRetry           []time.Duration
}
```

## Task 4: Add clocks, identifiers, secret redaction, and HTTP boundary primitives

**Files:**

- Create: `internal/platform/clock/clock.go`
- Create: `internal/platform/ids/ids.go`
- Create: `internal/platform/logsafe/redact.go`
- Create: `internal/platform/httpx/middleware.go`
- Create: `internal/platform/httpx/json.go`
- Test: `internal/platform/httpx/middleware_test.go`
- Test: `internal/platform/logsafe/redact_test.go`

- [ ] Write tests for UTC-only clock output, 32-byte random opaque tokens, constant-time bearer comparison behavior, request-ID propagation, exact body limits, generic auth errors, and recursive redaction.
- [ ] Implement an injectable `Clock` with production and fake implementations; domain/application services accept it rather than calling `time.Now()`.
- [ ] Implement CSPRNG byte generation with `crypto/rand`, base64url encoding, and SHA-256 token hashing.
- [ ] Implement structured redaction for password, authorization, bearer, token, session, CSRF, API-key, and configured sensitive keys. Redaction must apply to subprocess stderr before logging.
- [ ] Implement `http.MaxBytesReader`, `X-Request-ID` generation/validation, constant-time bearer middleware, JSON content-type enforcement, and non-leaking error envelopes.
- [ ] Run `rtk go test ./internal/platform/... -race`; expect pass.
- [ ] Commit with message `feat: add secure platform primitives`.

Use a stable internal error envelope:

```go
type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}
```

## Task 5: Implement SQLite setup, migrations, transactions, and online backup

**Files:**

- Create: `internal/platform/sqlite/open.go`
- Create: `internal/platform/sqlite/migrate.go`
- Create: `internal/platform/sqlite/tx.go`
- Create: `internal/platform/sqlite/backup.go`
- Create: `internal/platform/sqlite/sqlite_test.go`
- Create: `migrations/gateway/000001_foundation.sql`
- Create: `migrations/pipeline/000001_foundation.sql`

- [ ] Write integration tests against temporary real SQLite files for WAL mode, foreign keys, busy timeout, migration checksum mismatch, concurrent writers, rollback, and a readable online-backup destination.
- [ ] Embed migrations separately per service. Track sequence, name, SHA-256, and applied UTC timestamp; fail startup when an applied migration's checksum differs.
- [ ] Configure each connection with `_journal_mode=WAL`, `_foreign_keys=on`, `_busy_timeout`, bounded open connections, and a startup `PRAGMA quick_check`.
- [ ] Provide `WithinTx(ctx, db, fn)` and repositories that receive the transaction interface so the state transition and audit append can commit atomically.
- [ ] Use the go-sqlite3 backup API for active databases. Refuse source and target paths that resolve to the same file.
- [ ] Run `rtk go test ./internal/platform/sqlite -race -count=1`; expect pass.
- [ ] Commit with message `feat: add durable SQLite foundation`.

## Task 6: Define internal contracts and idempotency semantics

**Files:**

- Create: `internal/contracts/candidate.go`
- Create: `internal/contracts/quality.go`
- Create: `internal/contracts/health.go`
- Create: `internal/contracts/idempotency.go`
- Test: `tests/contract/contracts_test.go`
- Test: `tests/contract/golden/`

- [ ] Define `CandidateAccepted`, `CandidateApproved`, `CandidateWinner`, `CandidateSuperseded`, `Health`, and degraded-dependency DTOs. Every relevant DTO carries request, job, candidate, and config snapshot identity.
- [ ] Keep acquisition provenance owned by gateway and validation/import state owned by pipeline. A shared `candidate_id` is an immutable reference, never a shared mutable record.
- [ ] Write golden JSON tests that fail on accidental field removal or semantic type changes. Add strict decoding tests that reject unknown fields and oversized bodies. Do not add application generation labels such as `v1` or `v2`; compatibility is locked by the DTO golden files and explicit update review.
- [ ] Define idempotency records as request key, operation, canonical request hash, response status/body hash, creation time, and expiry. Reuse with a different request hash must return conflict.
- [ ] Run `rtk go test ./tests/contract ./internal/contracts`; expect pass.
- [ ] Commit with message `feat: define internal service contracts`.

## Task 7: Enforce filesystem layout and atomic-device invariants

**Files:**

- Create: `internal/platform/fscheck/layout.go`
- Create: `internal/platform/fscheck/stat_unix.go`
- Create: `internal/platform/fscheck/layout_test.go`
- Create: `scripts/bootstrap-data-layout.sh`
- Create: `tests/integration/filesystem_layout_test.go`

- [ ] Write tests that reject missing directories, wrong UID/GID, insufficient mode, symlinks, cross-device processing paths, a writable pipeline library mount, and a downloader path outside its root.
- [ ] Implement canonical root opening and `stat` device comparison for downloads, incoming, work, approved, quarantine, and library.
- [ ] Make the bootstrap script accept explicit numeric UID/GID and `/data` target only; refuse `/`, home directories, unresolved variables, and symlinked roots.
- [ ] Expose a read-only startup report listing canonical path, device ID, owner, and allowed access without secret data.
- [ ] Run `rtk go test ./internal/platform/fscheck ./tests/integration -run Filesystem`; expect pass on same-device fixtures and the expected rejection on cross-device fixtures.
- [ ] Commit with message `feat: enforce Denyra filesystem boundaries`.

## Task 8: Build reproducible service and derived third-party images

**Files:**

- Create: `deploy/docker/gateway.Dockerfile`
- Create: `deploy/docker/pipeline.Dockerfile`
- Create: `deploy/docker/lidarr.Dockerfile`
- Create: `deploy/docker/navidrome.Dockerfile`
- Create: `deploy/docker/assets/README.md`
- Create: `scripts/verify-pins/build-provenance.sh`
- Test: `tests/integration/image_provenance_test.go`

- [ ] Write tests that compare image labels and `/app/build-provenance.json` to `dependencies.lock.json`, including platform and source artifact hashes.
- [ ] Build gateway and pipeline in a Go 1.27.0 builder obtained from the verified official archive, not a floating toolchain image. Use immutable Debian snapshots and exact OS package versions.
- [ ] Build pipeline with `ffmpeg`, `flac`, `metaflac`, CPython 3.14.7 from its verified XZ source artifact, and Beets 2.13.1 from its verified wheel. Generate and commit `deploy/python/requirements.lock` containing exact versions, artifact URLs, and SHA-256 for every transitive dependency before the first image build; subsequent builds run offline with `--require-hashes` and fail if the graph differs. Install nothing at runtime.
- [ ] Build gateway with verified SpotiFLAC engine, registry snapshot, three allowlisted extensions, and Node 24.19.0. Disable runtime installers and updates.
- [ ] Build Lidarr/Navidrome derived images from canonical digest references. Verify and install Lidarr.Plugin.Slskd and `nd-lyrics.ndp`; disable plugin auto-update/reload.
- [ ] Run every runtime image as configured non-root UID/GID, with a read-only root filesystem where compatible and explicit writable mounts/tmpfs only.
- [ ] Run `rtk docker buildx bake --file deploy/docker/docker-bake.hcl --set '*.platform=linux/amd64'`; expect all provenance checks to pass.
- [ ] Commit with message `build: add reproducible Denyra images`.

## Task 9: Compose topology, listeners, and mount tests

**Files:**

- Create: `deploy/compose.yaml`
- Create: `deploy/config/gateway.toml`
- Create: `deploy/config/pipeline.toml`
- Create: `deploy/secrets/README.md`
- Create: `tests/integration/compose_config_test.go`
- Create: `scripts/check-compose.sh`

- [ ] Encode exact image references and `platform: linux/amd64`. Do not expose gateway or pipeline-internal ports.
- [ ] Create `denyra-control` with `internal: true`; only gateway and pipeline join it. Give its two members explicit configurable addresses and bind each internal JSON listener only to its `denyra-control` address, never wildcard. Gateway reaches Lidarr/slskd through a separate acquisition network; pipeline reaches Lidarr through a separate import network. Admin UI remains the only pipeline listener bound to `0.0.0.0` and host-published.
- [ ] Give each service only the mounts in the foundation ownership table. Assert `/music:ro`, pipeline `/data/library:ro`, and no library mount for gateway/slskd/SFTPGo.
- [ ] Publish the pipeline Admin UI on configurable host port, binding service listener to `0.0.0.0`. Record HTTP/no-`Secure`-cookie risk in the example config.
- [ ] Use secret files for internal bearer, bootstrap admin, Lidarr API key, Soulseek account, SFTPGo admin, and Restic credentials. Document mode `0400` or `0440` with the service GID.
- [ ] Test parsed Compose JSON, not text matching, for service membership, mounts, read-only flags, network membership/addressing, listener bind addresses, image digests, health checks, user IDs, and absence of forbidden published ports. From Lidarr, slskd, SFTPGo, and Navidrome test containers, prove the control-plane ports are unreachable.
- [ ] Make `scripts/check-compose.sh` fail when host Engine is not `29.6.2`, Compose is not `v2.40.3`, or Buildx is not `v0.36.1`. Report the explicitly approved Compose-v2 compatibility exception rather than silently accepting Compose v5.
- [ ] Run `rtk docker compose -f deploy/compose.yaml config --quiet && rtk go test ./tests/integration -run Compose`; expect pass.
- [ ] Commit with message `deploy: define Denyra Compose topology`.

## Task 10: Wire startup validation, health, and CI gates

**Files:**

- Create: `internal/platform/health/service.go`
- Create: `internal/platform/health/handler.go`
- Create: `internal/platform/health/service_test.go`
- Create: `.github/workflows/ci.yml`
- Create: `Makefile`
- Create: `scripts/check-clean-tree.sh`
- Update: `cmd/acquisition-gateway/main.go`
- Update: `cmd/media-pipeline/main.go`

- [ ] Make startup load/validate config, verify lock identity, open/migrate the service database, insert an immutable config snapshot, verify filesystem/device/tool prerequisites, and then start listeners/workers.
- [ ] Implement `/health/live` as process liveness and `/health/ready` as local readiness only. External MusicBrainz, LRCLIB, Soulseek, and provider outages appear as `degraded` details without changing ready to false.
- [ ] Write tests for migration failure, missing binary, bad device identity, invalid pin, internal-service failure, external outage, and graceful shutdown.
- [ ] Implement `scripts/check-clean-tree.sh` to fail on the retired product identifiers, floating dependency selectors, or unfinished implementation markers outside archived design/plan prose; encode retired identifiers so the checker does not flag its own source.
- [ ] Add CI jobs for format/vet/test/race, migration checksums, lock verification, Compose config, image provenance, architecture imports, and `git diff --exit-code` after generators.
- [ ] Run the full foundation gate:

```sh
rtk gofmt -w cmd internal tests
rtk go vet ./...
rtk go test -race ./...
rtk scripts/verify-pins/verify.sh --offline
rtk docker compose -f deploy/compose.yaml config --quiet
```

Expected result: every command exits `0`; both service binaries start against temporary data roots and report ready with external dependencies marked degraded when fixtures make them unavailable.

- [ ] Commit with message `ci: verify Denyra foundation invariants`.

## Completion Gate

- [ ] `rtk git status --short` shows only intentional plan-execution changes before the final task commit.
- [ ] `rtk go test -race ./...` passes twice to expose order-sensitive global state.
- [ ] `rtk scripts/check-clean-tree.sh` returns no legacy product-name, floating-pin, or unfinished implementation hits.
- [ ] Compose's rendered configuration proves every mount/network/listener invariant.
- [ ] Build provenance exactly matches `dependencies.lock.json`.
- [ ] Record the verified foundation commit hash in the execution handoff before starting the controlled media pipeline plan.
