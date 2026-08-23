# Denyra system foundation design

## Purpose

Denyra is a Docker Compose deployment for a personal FLAC library. Lidarr is the control plane and the only component allowed to rename, move, or import files into `/data/library`. Every automated or manual acquisition passes through controlled validation before Lidarr receives a Manual Import request.

Public DNS, reverse proxies, public TLS, firewall rules, and Internet exposure are outside this design. The target host is one x86_64 Debian or Ubuntu system running Docker Engine `29.6.2`, Docker Compose `v2.40.3`, and Buildx `v0.36.1`. Compose remains on the explicitly selected v2 major line rather than the newer v5 line; changing that compatibility boundary requires an explicit update and full Compose acceptance run. All media paths live on the same filesystem under `/data` so moves between acquisition, processing, quarantine, and library directories are atomic renames.

## Component topology

The Compose project contains these services:

- Lidarr nightly with Lidarr.Plugin.Slskd
- slskd
- acquisition-gateway
- media-pipeline
- SFTPGo Community Edition
- Navidrome
- Restic in an optional Compose profile

Feishin and Tempus are clients, not server containers.

Lidarr handles wanted state, primary search through Lidarr.Plugin.Slskd, quality preferences, final naming, and final imports. The acquisition-gateway coordinates primary search and SpotiFLAC fallback. The media-pipeline validates release batches, performs deterministic staging mutations, manages quarantine and review, and submits a single approved candidate through the Lidarr Manual Import API.

Automatic Completed Download Handling is disabled in Lidarr. slskd completion events trigger processing but do not authorize import. A candidate must reach `APPROVED`, win any acquisition arbitration, and become `IMPORT_READY` before the pipeline calls Lidarr.

## Filesystem layout

```text
/data/
  downloads/
    slskd/
    spotiflac/
    other/
  incoming/
    manual/
  processing/
    work/
    approved/
  quarantine/
  library/
  state/
    acquisition-gateway/
    media-pipeline/
    lidarr/
    slskd/
    sftpgo/
    navidrome/
  backups/
```

`/data/backups` is temporary backup workspace. It is not a disaster backup while it remains on the same disk.

## Mounts and ownership

| Service | Mount | Access |
| --- | --- | --- |
| Lidarr | `/data/processing/approved` | read/write |
| Lidarr | `/data/library` | read/write |
| slskd | `/data/downloads/slskd` | read/write |
| acquisition-gateway | `/data/downloads/spotiflac` | read/write |
| media-pipeline | `/data/downloads/slskd` | read/write |
| media-pipeline | `/data/downloads/spotiflac` | read/write |
| media-pipeline | `/data/downloads/other` | read/write |
| media-pipeline | `/data/incoming/manual` | read/write |
| media-pipeline | `/data/processing/work` | read/write |
| media-pipeline | `/data/processing/approved` | read/write |
| media-pipeline | `/data/quarantine` | read/write |
| media-pipeline | `/data/library` | read-only |
| SFTPGo | `/data/incoming/manual` | read/write |
| Navidrome | `/data/library` mapped to `/music` | read-only |

Lidarr has no access to raw downloads, processing work, or quarantine. SFTPGo has no access to processing, quarantine, or the library. The acquisition-gateway has no library mount. The media-pipeline may touch a raw acquisition path only after it has durable completion evidence and a successful claim lock.

Containers use one configurable numeric UID and GID. Startup checks verify ownership, permissions, canonical paths, and filesystem device identity. The pipeline refuses a non-atomic cross-device layout.

## Networks and listeners

Gateway and pipeline communicate on a dedicated private Compose control network that no other container joins. Each internal JSON listener binds only to its address on that network, not to a wildcard address. Gateway and pipeline may join separate purpose networks for outbound Lidarr/slskd access, but those networks cannot reach the control listener. Their contract is JSON over HTTP with a request ID, idempotency key, explicit request body size limit, and bearer secret loaded from a secret file. Bearer token comparison is constant time.

The media-pipeline runs separate listeners:

- Admin Web UI binds to `0.0.0.0` and may be host-published.
- Internal API is available only on the dedicated private network and is not host-published.

The Admin Web UI uses plain HTTP. Its cookie is `HttpOnly`, `SameSite=Strict`, and `Path=/`, without `Secure`. Binding plain HTTP to every interface is an accepted security risk. Deployment and firewall controls determine who can reach the port.

The Admin Web UI's visual language, route composition, interactive states, accessibility contract, Content Security Policy, and static asset delivery are specified in the controlled media pipeline design.

Internal service ports are not published unless a user-facing function needs them. Navidrome, SFTPGo, and the Admin Web UI are the only server interfaces intended for host or LAN access. Public exposure remains out of scope.

## Central configuration

Both custom services load typed configuration in this order:

```text
compiled explicit defaults
< TOML config file
< DENYRA_* environment overrides
```

Durations require units such as `10s`, `30m`, and `6h`. Storage uses IEC units such as `20GiB`. Percentages use bounded decimal values. Unknown fields, invalid units, overflow, contradictory settings, missing pins, and invalid paths stop startup.

Runtime hot reload is not supported. Each effective non-secret configuration is serialized as canonical JSON, hashed, and inserted as a new immutable `config_snapshot`. Referenced snapshots are never updated. New configuration always creates a new row.

Jobs, submissions, and candidates reference the snapshot active when they were created. Existing work keeps that policy unless an explicit migration or re-evaluation is recorded. Deadlines and retry times are stored as absolute UTC timestamps.

Secret values never enter configuration snapshots, logs, or audit events. Audit data stores only the secret source and name. An optional fingerprint uses HMAC with a separate audit key so low-entropy secrets are not exposed through plain hashes.

## Dependency locking

`dependencies.lock.json` records every external dependency using its complete identity:

- canonical registry and repository
- version or tag
- full image reference including tag and digest
- target platform
- artifact filename and SHA-256 when applicable
- source commit for registry snapshots
- transitive dependency hashes where applicable

Lidarr nightly uses its digest as the deployment identity. Compose must use the exact `image@sha256` reference and `platform: linux/amd64`; it must not repull the floating `nightly` tag.

Approved server baseline:

| Dependency | Pin |
| --- | --- |
| Lidarr nightly | `lscr.io/linuxserver/lidarr:nightly@sha256:0b84fcf40449e800da92eccbf4a421dd39908a5e1e2a25b6e3e5b5dcc9697e95` |
| Lidarr.Plugin.Slskd | `1.1.3.0`, asset SHA-256 `5766c6563f7ed36911068c899778e0f935698c6866c85dffafb2f8a32a5fc0d8` |
| slskd | `ghcr.io/slskd/slskd:0.26.0@sha256:161e214a05b51404f6bb6ddd7e60b66ff674a2d8ad28a88dae5b3c1caf9c8c48` |
| SFTPGo | `docker.io/drakkan/sftpgo:v2.7.5@sha256:d819bcea946470940416b63604f820aee965a02127b07126785e279fa311258e` |
| Navidrome | `docker.io/deluan/navidrome:0.63.2@sha256:38246ebb80d6f7e2724eecab4acafa7b14ec66ae800b2454aa6da4c19f80a9ce` |
| Navidrome Lyrics Plugin | `7.2.0`, SHA-256 `a9196e5b4e2c2eb2aaccb9f35c9faf6f488fe9081ff5685b1556901686c7540f` |
| Restic | `docker.io/restic/restic:0.19.1@sha256:08916bcda4a4435f9d9828ebb4e91bb7ada3d2c8a53699788930e0ae1bd4fa67` |
| beets | `2.13.1`, wheel SHA-256 `6d74a610b934c8e7b86dc651ec9953b62bd5ec9c47beea10cc32ede41ea9d488`; CPython `3.14.7` XZ source SHA-256 `3b48dac8fb59f62eaa67ac83c1eb12bda1b7a08406dd286e252c11a66be27f81`; exact transitive hashes |
| Geist and Geist Mono | `1.7.2`, asset `geist-font-v1.7.2.zip` SHA-256 `7fc800d2ac6b92844895196e5041aca55d814c15db70c44f79b3b83ab82b04e2` |
| Phosphor Core icons | `2.0.8`, npm tarball `core-2.0.8.tgz` SHA-256 `c4d7eca2a776229c2e33c6749e09dbea32f5f3a83171c7502b3bc52f887a3551` |

The custom Go toolchain uses Go `1.27.0`, templ `0.3.1020`, HTMX `2.0.10`, and `mattn/go-sqlite3 v1.14.50`. Direct supporting modules are `pelletier/go-toml/v2 v2.4.3`, `golang.org/x/crypto v0.55.0`, `golang.org/x/text v0.41.0`, and `golang.org/x/sys v0.47.0`. These were the latest stable module versions at the dependency audit date. The Go Linux amd64 archive SHA-256 is `675c26c449cbb18fc24b74650de1eabbae6e16f64326fd85a283fb3b58280685`. The HTMX npm tarball SHA-256 is `577ad40c1c94c9de47edb89e0aec78a8353d36024c50017eb53e02992a55e889`; the vendored `dist/htmx.min.js` SHA-256 is `71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de`.

The Admin Web UI adds two frontend artifacts to the lock file. Both are verified against the digest above at build time, unpacked, embedded in the pipeline binary, and served from the pipeline itself.

The Geist release contains its variable faces only as `.ttf` and ships `woff2` as static per-weight files. Converting the variable `.ttf` to `woff2` would add a font-tooling dependency to the build for no benefit, because the Admin Web UI type scale uses three weights. The build therefore extracts four static `woff2` faces and discards the rest of the archive:

```text
Geist-Regular.woff2
Geist-Medium.woff2
Geist-SemiBold.woff2
GeistMono-Regular.woff2
```

The icon sprite is compiled at build time from the `assets/regular` SVG sources in the pinned Phosphor tarball, restricted to the glyphs the console actually references. No icon path is authored by hand, and no other Phosphor weight is compiled in.

These are the only frontend assets. No other font, icon set, stylesheet, or script may be added without a lock update and the review it requires.

The beets environment is resolved into a hash-locked dependency set. The build uses the exact Python runtime and artifacts and fails if the graph changes. OS packages come from an immutable Debian snapshot with exact versions. Dependency changes require an explicit lock update and compatibility tests. Nothing follows `latest` automatically.

Denyra uses Debian `13.6` (`trixie`), the current stable release line at the dependency audit date, through one verified immutable snapshot timestamp with exact package versions. It builds derived Lidarr and Navidrome images from the pinned upstream manifests. The builds install the verified Lidarr.Plugin.Slskd and `nd-lyrics.ndp` artifacts without using a floating runtime installer. Plugin auto-update is disabled. The derived image digests are written back to the lock file before deployment.

## Repository layout

```text
cmd/
  acquisition-gateway/
  media-pipeline/
internal/
  config/
  contracts/
  platform/
  gateway/
    domain/
    application/
    adapters/
    persistence/
    transport/
  pipeline/
    domain/
    application/
    adapters/
    persistence/
    adminui/
      handlers/
      middleware/
      views/
      assets/
        css/
        fonts/
        icons/
        htmx/
    internalapi/
migrations/
  gateway/
  pipeline/
deploy/
  compose.yaml
  config/
  secrets/
  docker/
tests/
  fixtures/
  contract/
  integration/
  acceptance/
scripts/
  backup/
  restore/
  verify-pins/
docs/
  superpowers/specs/
  superpowers/plans/
  runbooks/
```

Domain packages do not import HTTP, adapters, persistence, SQLite, or subprocess implementations. `internal/platform` contains narrow technical primitives, not business policies. `internal/contracts` contains immutable handoff DTOs and compatibility tests. The gateway and pipeline have separate images because their runtime dependencies differ. Both images run non-root.

Go uses the standard `net/http` server and `http.ServeMux`, `database/sql`, handwritten repositories, embedded SQL migrations, and `log/slog` JSON logging. There is no ORM or custom web framework. templ source and generated Go are committed, with a CI regeneration check. HTMX and CSS are embedded in the binary; there is no Node frontend toolchain.

## System invariants

1. Lidarr is the only writer to `/data/library`.
2. Navidrome never modifies master FLAC files.
3. Downloaders write only to their acquisition directories.
4. Every source passes the controlled pipeline before import.
5. Invalid or uncertain releases stay outside the library.
6. SpotiFLAC receives no personal provider credentials.
7. Provider failure cannot affect existing playback or library ownership.
8. FLAC remains the canonical format.
9. Application policies are centralized, typed, snapshotted, and reproducible.
10. External dependency pins change only through explicit review and compatibility tests.
