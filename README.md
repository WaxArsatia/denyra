# Denyra

Denyra is a self-hosted FLAC acquisition, validation, library, and streaming
stack. Lidarr owns the final library. Denyra's Go services coordinate
acquisition and validate release batches before Lidarr imports them. Navidrome
exposes the library through OpenSubsonic without write access to the master
files.

The project targets a private, Spotify-like experience while keeping FLAC as the
canonical source format. Public exposure, DNS, TLS, reverse proxy, and firewall
design are intentionally outside this repository.

## Contents

- [What Denyra does](#what-denyra-does)
- [System boundaries](#system-boundaries)
- [Architecture](#architecture)
- [Components](#components)
- [Requirements](#requirements)
- [Try the local demo](#try-the-local-demo)
- [Run a production-like local stack](#run-a-production-like-local-stack)
- [Initial configuration](#initial-configuration)
- [Daily operation](#daily-operation)
- [Backup, restore, and upgrades](#backup-restore-and-upgrades)
- [Security model](#security-model)
- [Development](#development)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)
- [Project documentation](#project-documentation)

## What Denyra does

Denyra provides three ingestion paths that converge on one controlled media
pipeline:

1. Lidarr searches through Lidarr.Plugin.Slskd and slskd as the primary
   acquisition path.
2. The Acquisition Gateway invokes a pinned SpotiFLAC subprocess when the
   primary path returns a legitimate zero-result.
3. SFTPGo accepts manual uploads for explicit submission and review.

Every candidate is handled as a complete MusicBrainz release. The pipeline
checks the media, matches the release, writes approved deterministic tags,
creates lyrics sidecars, and hands exactly one winner to Lidarr Manual Import.
Ambiguous or invalid candidates remain in quarantine.

The main invariants are:

- Lidarr is the only process allowed to rename, move, or organize files in
  `/data/library`.
- Navidrome mounts the final library read-only.
- Downloaders write only to acquisition directories.
- Validation is release-atomic. One failed or missing track blocks the whole
  release.
- FLAC remains the master format. Playback transcoding never changes the master.
- SpotiFLAC receives no personal streaming-provider credentials.
- Operational errors never become false `NO_CANDIDATE` results.

## System boundaries

Denyra includes media acquisition orchestration, validation, metadata, lyrics,
import, streaming, local administration, backup, and recovery.

The following concerns require a separate deployment design:

- public Internet exposure
- DNS and TLS
- reverse proxies
- host firewall rules
- VPN topology
- detailed legal, copyright, or provider terms assessment

Only run acquisition sources you are permitted to use. Keep credentials out of
Git, logs, config snapshots, and chat messages.

## Architecture

```text
Lidarr Wanted
    |
    v
Acquisition Gateway
    |
    +--> Lidarr AlbumSearch
    |        |
    |        v
    |    Lidarr.Plugin.Slskd --> slskd --> Soulseek
    |
    +--> SpotiFLAC fallback, only after legitimate primary zero-result
             |
             v
      /data/downloads/*
             |
             v
       Media Pipeline <---- SFTPGo manual submission
             |
             +--> validation and MusicBrainz release matching
             +--> deterministic FLAC tags through metaflac
             +--> folder.jpg evidence and same-basename .lrc sidecars
             +--> quarantine or operator review
             |
             v
    /data/processing/approved
             |
             v
      Lidarr Manual Import
             |
             v
       /data/library
             |
             v
 Navidrome --> Feishin / Tempus
```

Gateway and Pipeline have separate SQLite databases. They exchange immutable
candidate IDs over an authenticated private HTTP API. They do not share business
state through a database.

## Components

| Component                | Responsibility                                                                                                      |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| Acquisition Gateway      | Wanted discovery, primary search orchestration, fallback, retries, correlation, arbitration, and candidate handoff  |
| Media Pipeline           | Claiming, technical validation, MusicBrainz matching, enrichment, deterministic mutation, review, and Lidarr import |
| Lidarr nightly           | Wanted state, release policy, final import, naming, and final library ownership                                     |
| Lidarr.Plugin.Slskd      | Native Lidarr integration with slskd                                                                                |
| slskd                    | Headless Soulseek client and primary downloader                                                                     |
| SpotiFLAC Module Version | Credentialless fallback engine run as an isolated subprocess                                                        |
| SFTPGo CE                | Manual upload endpoint                                                                                              |
| beets                    | Advisory matching evidence for manual ingest only                                                                   |
| MusicBrainz              | Canonical release metadata                                                                                          |
| LRCLIB                   | Persistent synchronized lyrics source                                                                               |
| Navidrome                | Read-only catalog and OpenSubsonic server                                                                           |
| Feishin                  | Recommended Linux desktop client                                                                                    |
| Tempus                   | Recommended Android client                                                                                          |
| Restic                   | Supported backup repository and retention path                                                                      |

Exact application, runtime, image, plugin, extension, and asset identities are
recorded in [`dependencies.lock.json`](dependencies.lock.json) and
[`deploy/images.lock.json`](deploy/images.lock.json). A dependency pin changes
only through an explicit update and compatibility test.

## Requirements

Use a Linux amd64 host with:

- Docker Engine and Docker Compose v2
- Docker Buildx
- Git
- Go for development and tests
- enough storage for the FLAC library, download staging, processing, and
  quarantine
- one numeric UID/GID shared by media containers

All paths below `/data` must be on the same filesystem so the pipeline can use
atomic rename operations. A supported Restic repository must be on another disk,
filesystem, or remote repository.

The locked local ports are:

| Port    | Service        | Purpose                            |
| ------- | -------------- | ---------------------------------- |
| `8090`  | Media Pipeline | Denyra Admin UI over internal HTTP |
| `8686`  | Lidarr         | Local setup and library management |
| `5030`  | slskd          | Local Web UI and configuration     |
| `50300` | slskd          | Soulseek incoming listen port      |
| `8080`  | SFTPGo         | Web administration                 |
| `2022`  | SFTPGo         | SFTP uploads                       |
| `4533`  | Navidrome      | Web UI and OpenSubsonic API        |

Change a host port through the corresponding `DENYRA_*_HOST_PORT` variable when
it is already occupied.

## Try the local demo

The local demo runs the real Gateway, Pipeline, SQLite databases, Admin UI, and
Navidrome. It replaces Lidarr, MusicBrainz, and LRCLIB with a local no-result
fixture. It does not contact Soulseek or a live fallback provider.

Build and verify the locked custom images:

```sh
scripts/verify-pins/verify.sh --offline
make generate-provenance
BUILDX_NO_DEFAULT_ATTESTATIONS=1 docker buildx bake \
  -f deploy/docker/docker-bake.hcl gateway pipeline navidrome --load
```

Choose an absolute directory outside the repository, then create the runtime
tree and local secrets:

```sh
export DENYRA_LOCAL_ROOT=/absolute/path/to/denyra-local
export DENYRA_DATA_ROOT="$DENYRA_LOCAL_ROOT/data"
export DENYRA_SECRETS_DIR="$DENYRA_LOCAL_ROOT/secrets"
export DENYRA_MEDIA_UID="$(id -u)"
export DENYRA_MEDIA_GID="$(id -g)"

install -d -m 0750 \
  "$DENYRA_DATA_ROOT"/{downloads/{slskd,spotiflac,other},incoming/manual} \
  "$DENYRA_DATA_ROOT"/{processing/{work,approved},quarantine,library,backups} \
  "$DENYRA_DATA_ROOT"/state/{gateway,pipeline,lidarr,slskd,sftpgo,navidrome} \
  "$DENYRA_DATA_ROOT"/cache/navidrome "$DENYRA_SECRETS_DIR"

for name in internal_bearer audit_key lidarr_api_key soulseek_username soulseek_password restic_password; do
  openssl rand -hex -out "$DENYRA_SECRETS_DIR/$name" 32
done
openssl rand -hex -out "$DENYRA_SECRETS_DIR/bootstrap_admin" 12
chmod 0400 "$DENYRA_SECRETS_DIR"/*
```

Start the persistent demo:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.acceptance.yaml \
  -f deploy/compose.local.yaml \
  up -d --wait \
  acceptance-fixture media-pipeline acquisition-gateway navidrome
```

Open:

- Denyra Admin UI: <http://127.0.0.1:8090>
- Navidrome: <http://127.0.0.1:4533>

The Denyra username is `admin`. Read the one-time password locally:

```sh
command cat "$DENYRA_SECRETS_DIR/bootstrap_admin"
```

Navidrome asks you to create its first administrator. Its account is independent
from the Denyra administrator.

Stop the demo without deleting its state:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.acceptance.yaml \
  -f deploy/compose.local.yaml \
  down
```

Do not use `down --volumes` or delete `DENYRA_DATA_ROOT` unless you intend to
remove the demo state.

## Run a production-like local stack

A production-like local stack uses real Lidarr, slskd, SFTPGo, MusicBrainz,
LRCLIB, and the pinned SpotiFLAC engine. It remains local HTTP and should not be
exposed to an untrusted network.

Prepare:

- a Soulseek account
- an empty or existing FLAC library under the chosen data root
- distinct passwords for Denyra, SFTPGo, Navidrome, and slskd
- an optional Restic repository outside the `/data` filesystem

Do not paste credentials into commands that enter shell history. Store them in
local secret files or enter them through the service's local setup UI.

Follow [`docs/runbooks/install.md`](docs/runbooks/install.md) for the complete
directory, ownership, secret, build, and verification procedure. For local
access, add `deploy/compose.local.yaml`:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.local.yaml \
  up -d lidarr slskd sftpgo navidrome
```

Finish the Lidarr, slskd, SFTPGo, and Navidrome setup described below. Put the
Lidarr API key in the local `lidarr_api_key` secret file, then start Denyra's Go
services:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.local.yaml \
  up -d --wait media-pipeline acquisition-gateway
```

Check service state:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.local.yaml \
  ps
```

## Initial configuration

### Lidarr

Open <http://127.0.0.1:8686> and complete its authentication setup.

Required settings:

- disable automatic Completed Download Handling
- set `/data/library` as the final root folder
- allow import only from `/data/processing/approved`
- enable Import Extra Files for lyrics sidecars such as `.lrc`, `.elrc`, and
  `.ttml`
- retain `folder.jpg` as Lidarr's album artwork filename
- configure the baked Lidarr.Plugin.Slskd integration against `slskd`

Copy Lidarr's API key into `DENYRA_SECRETS_DIR/lidarr_api_key` using a local
editor, set mode `0400`, and restart Gateway and Pipeline. Never commit the key.

### slskd

Open <http://127.0.0.1:5030>. Remote configuration is enabled by the local
Compose override.

Configure:

- Soulseek username and password
- download directory `/data/downloads/slskd`
- incomplete directory below the same mounted download tree
- strong Web UI authentication
- a read/write API key for Lidarr.Plugin.Slskd
- incoming listen port `50300`

The current deployment mounts slskd state at `/app`, so its saved configuration
survives container recreation.

### SFTPGo

Open <http://127.0.0.1:8080>, create the first SFTPGo administrator, then create
upload users restricted to `/data/incoming/manual`. SFTPGo must not receive
access to processing, quarantine, or the final library.

### Navidrome

Open <http://127.0.0.1:4533> and create the first music administrator. Navidrome
uses `/music:ro`; its database, cache, and transcoding data live in separate
writable volumes.

Configure Feishin or Tempus with the Navidrome URL and a Navidrome music
account. Prefer original FLAC on LAN. On constrained links, request an
appropriate OpenSubsonic maximum bitrate such as the logical `opus-256` or
`opus-160` policy.

### Denyra Admin UI

Open <http://127.0.0.1:8090> and sign in with the bootstrap username and
password. Change the password after first login, then empty the bootstrap secret
file. The UI provides candidate details, per-track results, metadata diffs,
checksums, provenance, artwork and lyrics status, plus Approve, Reject, and
Retry actions.

An approval requires a MusicBrainz Release ID and a reason. Mutations use
optimistic state revisions, CSRF protection, and the same domain services used
by the internal API.

## Daily operation

Useful commands:

```sh
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml ps
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml logs --since 10m acquisition-gateway media-pipeline
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml restart acquisition-gateway media-pipeline
```

Media locations:

| Path                        | Owner or purpose                                                    |
| --------------------------- | ------------------------------------------------------------------- |
| `/data/downloads/slskd`     | slskd raw downloads; Pipeline claims only completed and locked jobs |
| `/data/downloads/spotiflac` | Gateway fallback output; Pipeline claims completed candidates       |
| `/data/incoming/manual`     | SFTPGo manual submissions                                           |
| `/data/processing/work`     | Pipeline validation and deterministic mutation                      |
| `/data/processing/approved` | Lidarr-visible approved import batches                              |
| `/data/quarantine`          | Invalid, ambiguous, review-required, or superseded candidates       |
| `/data/library`             | Lidarr-owned final library                                          |

Low storage blocks new claim, acquisition, and import work when available
capacity falls below `max(20 GiB, 5%)`. Cleanup, quarantine handling,
reconciliation, backup, recovery, and capacity-restoring administration remain
available.

## Backup, restore, and upgrades

Restic is an optional Compose profile. The repository must be explicit and must
not share the `/data` filesystem. Denyra enters maintenance mode, drains
mutations, creates online SQLite backups, briefly stops stateful third-party
services, and verifies the snapshot before leaving maintenance.

Read these runbooks before relying on the deployment:

- [`docs/runbooks/backup.md`](docs/runbooks/backup.md)
- [`docs/runbooks/restore.md`](docs/runbooks/restore.md)
- [`docs/runbooks/upgrade.md`](docs/runbooks/upgrade.md)

Restore always targets a new directory. Cutover remains manual. Do not overwrite
the live data tree.

## Security model

The Admin UI intentionally uses HTTP on `0.0.0.0:8090`. Its cookie is
`HttpOnly`, `SameSite=Strict`, and has no `Secure` attribute because TLS is not
part of this internal stack. This is an accepted risk. The deployment and
firewall must limit who can reach the port.

Other controls include:

- Argon2id password hashes
- server-side sessions with 32-byte opaque CSPRNG tokens
- only session-token hashes stored in SQLite
- 30-day absolute session lifetime with no idle timeout
- CSRF protection on every mutation
- append-only audit evidence and optimistic state revisions
- generic authentication errors
- logout, logout-all, password-change, and explicit revocation
- private Gateway to Pipeline network and constant-time bearer comparison
- secret redaction from structured logs and config snapshots

Read [`docs/runbooks/security-boundary.md`](docs/runbooks/security-boundary.md)
before changing ports or network exposure.

## Development

The custom services use Go, `net/http`, `database/sql`, go-sqlite3 with CGO,
handwritten repositories, embedded migrations, templ, and locally vendored HTMX.
There is no Node frontend toolchain.

Common commands:

```sh
make fmt
make vet
make test
make race
make verify-lock
make compose-config
```

Regenerate the Admin UI after changing a `.templ` source:

```sh
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
scripts/verify-ui-source.sh
go run ./scripts/verify-tokens
```

Format and lint the entire codebase before committing:

```sh
make fmt
go vet ./...
```

## Testing

Run the full race suite:

```sh
go test -race -count=1 ./...
```

Run deterministic acceptance tests:

```sh
go test -count=1 ./tests/acceptance -run Denyra
```

Run the pinned Compose smoke after building the locked images:

```sh
DENYRA_ACCEPTANCE_COMPOSE=1 \
  go test -count=1 ./tests/acceptance \
  -run TestDenyraPinnedComposeStartsReadyWithLocalAdapters -v
```

Live-provider acceptance is excluded from CI and normal local tests. It starts
only with the exact explicit side-effect acknowledgement required by the
Gateway.

The last verified artifact identities, commands, and results are in
[`docs/runbooks/acceptance-evidence.md`](docs/runbooks/acceptance-evidence.md).

## Troubleshooting

Start with service status and recent logs:

```sh
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml ps
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml logs --since 10m
```

Common problems:

| Symptom                              | Check                                                                                                                |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| Pipeline is not ready                | Directory ownership, mode, filesystem device identity, required binaries, config, migrations, and secret-file access |
| Gateway restarts                     | Lidarr API key, Lidarr readiness, Pipeline readiness, locked SpotiFLAC artifacts, and private network addresses      |
| External dependency is degraded      | MusicBrainz, LRCLIB, Soulseek, or fallback provider availability; local readiness should stay healthy                |
| New work stops                       | Free capacity on the filesystem that contains the actual mounted `/data` paths                                       |
| Manual submission returns to waiting | The sealed tree fingerprint changed before claim; inspect and submit again                                           |
| Candidate stays in review            | Supply a definite MusicBrainz Release ID and record an approval reason                                               |
| Navidrome shows no new track         | Check watcher logs, then wait for the one-minute recovery scan                                                       |
| Login session disappears             | Check 30-day expiry, password change, logout-all, or explicit revocation                                             |

Incident-specific recovery steps are documented in
[`docs/runbooks/incidents.md`](docs/runbooks/incidents.md).

## Project documentation

- [Installation runbook](docs/runbooks/install.md)
- [Client setup](docs/runbooks/clients.md)
- [Security boundary](docs/runbooks/security-boundary.md)
- [Incident recovery](docs/runbooks/incidents.md)
- [Backup](docs/runbooks/backup.md)
- [Restore](docs/runbooks/restore.md)
- [Upgrade and rollback](docs/runbooks/upgrade.md)
- [System foundation design](docs/superpowers/specs/2026-08-24-system-foundation-design.md)
- [Acquisition orchestration design](docs/superpowers/specs/2026-08-24-acquisition-orchestration-design.md)
- [Controlled media pipeline design](docs/superpowers/specs/2026-08-24-controlled-media-pipeline-design.md)
- [Operations and clients design](docs/superpowers/specs/2026-08-24-operations-and-clients-design.md)

No license file is currently included. Treat the repository as all rights
reserved until the owner adds an explicit license.
