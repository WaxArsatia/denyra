# Simplified local-build deployment design

> Historical design. Its backup, restore, update snapshot, and rollback sections are superseded by `docs/superpowers/specs/2026-08-25-forward-only-active-development-lifecycle-design.md`.

Status: approved for implementation planning

Base commit: `0d558f6`

Date: 2026-08-24

## Purpose

Denyra will remain a Docker Compose deployment, but installation and updates will no longer depend on exact artifact identities, local provenance records, or manual build commands. The server will build the custom Denyra images through one management command. Go compilation may occur inside Docker. Python and Node will come from official binary images and will not be compiled or unpacked manually on the host.

This design favors a short feedback loop during active development. It keeps controls that protect credentials and media data, but removes supply-chain and reproducibility controls whose maintenance cost is too high for the current stage of the project.

The existing media workflow remains unchanged. Lidarr is still the only writer of `/data/library`, Navidrome still mounts the library read-only, and approved imports still pass through the pipeline.

## Superseded decisions

This document supersedes the following parts of the existing design documents:

- `Dependency locking` in `docs/superpowers/specs/2026-08-24-system-foundation-design.md`
- dependency-pin checks in `Central configuration` and `Health and degraded state`
- dependency lock identity in the restore contract
- `Upgrades and rollback` in `docs/superpowers/specs/2026-08-24-operations-and-clients-design.md`
- exact-version statements for server images, plugins, Python, Node, Debian packages, and client applications
- provenance and exact dependency identity requirements in existing implementation plans and runbooks

The topology, filesystem ownership boundaries, media validation, import safety, database migrations, authentication, backup coverage, and recovery rules remain in force unless this document changes them explicitly.

## Goals

- A new server can be installed with `./denyra setup` after cloning the repository.
- Routine updates require only `./denyra update`.
- The host does not need Go, Python, Node, Buildx, or application-specific build tools.
- Python comes from an official image instead of a source build.
- Current compatible upstream releases are used when an image is rebuilt.
- Configuration and credentials are generated or reconciled automatically.
- A failed update restores the previous service state and images without copying the music library.
- Existing deployments can adopt the new tooling without moving their library or recreating accounts.

## Non-goals

- Reproducible builds across different dates
- Full software bill of materials or artifact attestation
- Automatic unattended updates
- Public TLS, DNS, reverse proxy, VPN, or host firewall configuration
- Zero-downtime stateful upgrades
- Cross-host clustering or orchestration beyond Docker Compose
- Automatic rollback after a deployment has already served healthy traffic and accepted new writes

## Audit summary

The current implementation couples deployment to two lock files, generated provenance documents, exact image digests, a Debian snapshot, exact APT package versions, a Python source build, and hash-locked Python packages. The lock identity is validated at service startup and is also embedded in backup, restore, Compose, CI, and upgrade workflows.

This creates several practical problems:

- `deploy/compose.yaml` references local custom image digests, so a new server must reproduce those images before it can start.
- `deploy/docker/pipeline.Dockerfile` builds CPython from source and resolves a fully hashed Python graph.
- `scripts/upgrade/verify-update.sh` rebuilds images and rewrites lock files before deployment.
- `scripts/upgrade/deploy.sh` runs formatting, vet, and the full race suite on the server.
- `internal/platform/servicehost` refuses startup when the lock and provenance files do not match.
- first-run setup requires several secret files and configuration work across multiple web interfaces.

These controls provide deterministic identity, but they do not match the project's current need for frequent updates and quick deployment.

## Deployment model

The repository contains one executable POSIX shell entry point named `denyra`. It wraps Docker Compose and the existing operational helpers. It does not introduce a CLI framework, daemon, package manager, or host runtime.

Supported commands are:

```text
./denyra setup
./denyra start
./denyra stop
./denyra restart
./denyra status
./denyra logs [service]
./denyra update
./denyra rollback
./denyra credentials
./denyra backup
```

The default deployment root is `/srv/denyra`. A user may override it with `DENYRA_HOME`. The deployment root contains:

```text
/srv/denyra/
  config/
    denyra.env
    gateway.toml
    pipeline.toml
    navidrome.toml
    navidrome-lyrics.toml
  secrets/
  data/
  updates/
```

The Git checkout remains separate from runtime data. A small ignored file in the checkout may record `DENYRA_HOME` for convenience. All other generated configuration stays outside Git so `git pull --ff-only` can require a clean source tree.

Every command constructs the same Compose invocation with the external environment and configuration paths. Operators do not need to repeat project names, override files, UID values, or secret directories.

## Dependency policy

The project keeps only compatibility pins that prevent unplanned major-version changes.

| Dependency type | Policy |
| --- | --- |
| Go modules | Keep exact versions and checksums in `go.mod` and `go.sum` |
| Go build image | Track the selected Go major and minor line, such as `golang:1.27` |
| Python runtime | Use an official `python:3.14-slim` image and follow patch releases |
| Node runtime | Use an official `node:24-slim` image and follow patch releases |
| Beets | Install the latest release below the next incompatible major version |
| Debian packages | Install current packages from the base image repositories without exact package versions |
| Lidarr | Follow `nightly` while Lidarr.Plugin.Slskd requires that channel |
| Navidrome, slskd, SFTPGo, Restic | Follow the upstream latest stable image tag |
| Lidarr and Navidrome plugins | Download the latest published binary release during image build |
| SpotiFLAC and extensions | Download the latest published binary artifacts during image build |
| GitHub Actions | Use official major-version tags instead of action commit hashes |
| Denyra custom images | Tag each local build with the current Git commit for rollback |

`dependencies.lock.json`, `deploy/images.lock.json`, the generated provenance files, the immutable Debian source list, and the lock verification scripts will be removed.

Downloaded artifacts use HTTPS. A checksum is verified when the upstream release publishes one in a form the build can consume automatically. Denyra will not maintain a second manual checksum catalog.

This policy accepts that two builds from the same Git commit may contain different upstream patch releases. Startup compatibility checks and update rollback handle failures. Bit-for-bit reproducibility is not a requirement.

## Container builds

### Acquisition gateway

The build stage uses the official Go image. The runtime stage uses the official Node slim image so the Dockerfile does not download or unpack Node itself. The build downloads the latest SpotiFLAC executable and extension releases, installs the extensions into the image, and copies the Go gateway binary from the build stage.

Startup verifies that the engine and Node executables run, that extension manifests parse, and that required provider types are present. It no longer checks exact hashes, registry commits, patch versions, or generated provenance.

### Media pipeline

The build stage uses the official Go image. The runtime stage uses the official Python slim image. It installs ffmpeg and FLAC tools through APT and installs the latest compatible Beets package through pip.

The Dockerfile does not build CPython, create a wheelhouse, or use `--require-hashes`. Startup continues to check that `ffprobe`, `flac`, `metaflac`, and `beet` are executable.

### Lidarr and Navidrome

Derived images remain because each service needs one plugin. Their Dockerfiles follow the upstream channel tag and download the latest plugin release. They do not use base-image digests or manually maintained asset hashes.

### Build behavior

`./denyra setup` and `./denyra update` run Docker Compose build with base-image pulling enabled. The update command passes a refresh build argument so layers that fetch latest release assets and Python packages are rebuilt. Normal Docker layer and Go module caches remain available.

Builds run while the previous stack is online. Downtime begins only after every new custom image builds successfully.

## First-run setup

`./denyra setup` performs these operations in order:

1. Confirm that Git, Docker Engine, and Docker Compose v2 are usable.
2. Resolve the deployment root, data root, current UID, and current GID.
3. Create the runtime directories. It may request sudo once when `/srv/denyra` does not exist.
4. Prompt for the Soulseek username and password. These are the only required external credentials.
5. Generate separate random credentials for Denyra, Navidrome, SFTPGo, the SFTP upload account, internal bearer authentication, audit signing, slskd API access, and optional Restic use.
6. Write secret files with mode `0600`, the secret directory with mode `0700`, and data and configuration directories with mode `0750`.
7. Render the external environment and service configuration files.
8. Build the custom and derived images.
9. Start Lidarr and slskd, wait for their local readiness, and obtain the Lidarr API key from the persistent Lidarr state.
10. Run the configuration reconciler.
11. Start the remaining services with Compose health waiting enabled.
12. Run endpoint smoke checks and print the service URLs plus the path used by `./denyra credentials`.

Setup is idempotent. A repeated run does not overwrite existing secret values, databases, administrators, or media. If it detects a partial setup, it resumes from the first incomplete step.

The generated credential report is readable only by the deployment owner. `./denyra credentials` displays it. The report is not copied into logs, backups intended for support, or Git.

## Service configuration reconciliation

Setup uses supported configuration files, environment variables, and service APIs. It does not automate browser clicks.

The reconciler performs the following work:

- read or adopt the Lidarr API key from persistent state
- create `/data/library` as the Lidarr root folder when missing
- disable Lidarr Completed Download Handling
- enable track renaming and approved album-directory formats
- enable extra-file import for `lrc`, `elrc`, and `ttml`
- enable Kodi/Emby artist and album images
- configure Lidarr.Plugin.Slskd against the internal slskd service
- configure slskd download paths, API access, and the Denyra completion webhook
- create the first SFTPGo administrator and a restricted upload account
- create the first Navidrome administrator
- bootstrap the Denyra administrator

The reconciler owns only settings required by Denyra. It does not reset unrelated user preferences. Existing administrators are detected and retained. Existing Lidarr libraries are not reorganized. New naming rules apply when Lidarr imports or the operator explicitly organizes files.

If an upstream service has no supported noninteractive first-user mechanism, setup starts that service and reports one specific remaining action. This is an exception path, not the default design. The implementation plan must confirm each upstream bootstrap interface before coding the reconciler.

## Compose simplification

Compose uses service names for internal routing. Static IPv4 addresses and the custom control subnet are removed. Internal listeners bind inside their containers and are reachable only on non-published Compose networks.

The deployment keeps these access controls:

- the media pipeline has read-only access to the final library
- Navidrome mounts `/music` read-only
- Lidarr is the only library writer
- SFTPGo sees only its state and manual incoming directory
- the acquisition gateway has no library mount
- no service mounts the Docker socket
- no service uses privileged mode
- secret files remain outside images and Git

Most read-only root filesystems, state-masking tmpfs mounts, fixed container IPs, and explicit platform declarations are removed. A service may retain a read-only root filesystem when its upstream image supports it without extra mounts or startup work.

The default server Compose publishes only Denyra, Navidrome, SFTPGo, and SFTP ports. Lidarr and slskd remain internal unless the local-development override is enabled. Public Internet exposure remains unsupported without a separate TLS and firewall deployment.

Restic remains an optional Compose profile. SFTPGo stays in the default stack because manual upload is an existing ingest path.

## Update flow

`./denyra update` performs these operations:

1. Refuse to continue when tracked source files have local changes.
2. Record the current Git commit, Compose model, environment, image references, and image IDs.
3. Fetch and fast-forward the selected branch.
4. Pull current upstream service images with the Compose always-pull policy.
5. Build new custom and derived images with base-image pulling and release-layer refresh enabled.
6. Tag custom images with the new short Git commit.
7. Stop the stack after all builds succeed.
8. Snapshot state, generated configuration, current image identities, and database files into a timestamped update directory. Library, cache, raw downloads, and processing media are excluded.
9. Start the new stack with health waiting enabled.
10. Run local readiness and service-contract smoke checks.
11. Mark the update successful and retain the last two update snapshots.

The server does not run gofmt, `go vet`, the Go test suite, the race detector, provenance generation, image-lock rewriting, or a full restore drill during update.

## Rollback

Before pulling or rebuilding, the update command records resolvable identities for every running image. The rollback metadata does not depend on a mutable tag after it has been updated.

If startup or smoke checks fail before the deployment is declared healthy, the command automatically:

1. stops the failed stack
2. restores the pre-update state and generated configuration
3. starts the recorded prior images through a generated Compose override
4. runs readiness checks
5. preserves both update logs and reports the failed service

The automatic path is safe because the new deployment has not been declared ready for use.

`./denyra rollback` performs the same operation for the latest successful update, but asks for confirmation. A manual rollback can discard writes created after that update, so it is never silent or automatic.

If the previous images are no longer present locally, rollback stops and reports the missing image instead of rebuilding an approximation. Update cleanup must retain the images referenced by the last two snapshots.

## Backup and restore

Routine updates use a local state snapshot, not a full Restic backup. This limits downtime and avoids copying the media library.

`./denyra backup` remains the supported disaster-backup command. Restic stays optional and keeps its separate-repository requirement. The backup manifest records file checksums, database versions, configuration, and the Denyra Git commit. It no longer includes dependency locks, image locks, or generated build provenance.

Restore verifies file checksums, database integrity, migration compatibility, ownership, and the single-filesystem media layout. It does not require the current deployment to match the exact build that created the backup.

## Runtime and health behavior

Custom-service startup continues to fail on:

- invalid typed configuration
- unreadable required secrets
- missing required binaries
- inaccessible or cross-device media paths
- failed database migrations
- unavailable required internal services
- invalid required Lidarr import configuration
- no usable installed SpotiFLAC provider manifest

Startup no longer reads a dependency lock or build-provenance file. Health output removes the `dependency-lock` component. Logs record the Denyra Git commit and observable upstream versions for diagnosis, but those values do not determine readiness.

## Error handling

Management commands print one actionable error and return a nonzero status. They do not continue after a failed prerequisite, partial secret write, failed build, failed snapshot, or unhealthy start.

Generated files use a temporary file followed by an atomic rename. Setup does not expose secret values in command arguments. Update logs redact configured secret paths and values.

Interruption before the old stack stops leaves it running. Interruption after it stops invokes the same rollback path used for failed health checks. The update lock prevents two setup, update, backup, or rollback commands from changing the deployment concurrently.

## Basic security boundary

The deployment retains:

- random internal credentials
- password-protected user interfaces
- secret files outside Git and images
- private internal networks
- non-privileged containers
- read-only final-library mounts for non-owner services
- request size limits, CSRF protection, session hashing, constant-time bearer comparison, and log redaction in application code

The deployment does not promise immutable artifacts, exact upstream provenance, automatic vulnerability remediation, TLS termination, or hardened read-only container roots. These are accepted tradeoffs for simpler active development.

## Compatibility with existing deployments

The migration keeps the current `/data` directory structure and database migrations. Existing users may point `DENYRA_HOME` at their current data and secret roots. Setup adopts readable secrets and existing service state before generating anything new.

The migration must not:

- move or rename `/data/library`
- reset Lidarr, Navidrome, SFTPGo, slskd, or Denyra databases
- replace existing administrator passwords
- reorganize existing albums
- remove unresolved acquisition, processing, quarantine, or import state

Legacy lock and provenance files may remain in old backup snapshots. New code ignores them.

## Repository changes in scope

Implementation will update or replace:

- the root management entry point and supporting shell helpers
- Dockerfiles and Compose files
- service-host startup and health reporting
- SpotiFLAC installation verification
- backup, restore, upgrade, and rollback scripts
- CI workflows and Make targets
- deployment and operations tests
- README and runbooks

Implementation will remove obsolete lock files, generated provenance, immutable Debian sources, lock verifiers, and tests whose only purpose is exact dependency identity.

Application domain logic, media policy, database schema history, admin UI behavior, and external API contracts are outside this refactor except where startup currently requires dependency provenance.

## Test strategy

CI remains the full development gate:

- gofmt check
- `go vet ./...`
- `go test ./...`
- race tests for packages that own concurrent state
- Compose configuration validation
- builds for every custom and derived image
- image smoke tests for required binaries and plugins
- existing contract and acceptance suites

Management-command tests run with fake `git` and `docker` executables placed first on `PATH`. They cover clean and dirty trees, first setup, repeated setup, partial setup recovery, failed build, failed snapshot, failed health check, automatic rollback, missing prior image, and update-lock contention.

Compose integration tests keep checking network and mount boundaries, required health checks, published ports, and service ownership. They stop requiring image digests, a fixed platform, or deployment lock equality.

Container tests verify behavior instead of exact versions:

- Python is supplied by the official runtime image and `beet version` succeeds.
- Node runs and the SpotiFLAC engine reports a version.
- expected extension provider types are installed.
- Lidarr loads its slskd plugin.
- Navidrome loads its lyrics plugin.

Acceptance tests verify that setup reaches a healthy stack from an empty deployment root and that a forced update failure restores the previous state and images.

## Acceptance criteria

The change is complete when all of the following are true:

- a Linux server with Git, Docker Engine, and Compose v2 can run `./denyra setup` without Go, Python, Node, or Buildx installed
- setup requires no external input except Soulseek credentials and an optional deployment-root override
- rerunning setup does not reset state or credentials
- no Dockerfile compiles CPython or manually downloads Node
- no Compose service requires an exact image digest
- custom services start without dependency lock or provenance files
- `./denyra update` fast-forwards source, refreshes upstream images and artifacts, builds, snapshots state, starts, and checks health
- a forced health failure restores the prior state and image set
- `./denyra rollback` requires confirmation
- existing libraries and databases remain usable
- CI and acceptance tests pass
- installation and update documentation contain only the supported management commands for normal operation

## Accepted risks

- Rebuilding the same commit later may select newer upstream patch releases.
- An upstream `latest` or `nightly` release may build successfully but fail at runtime.
- Local builds consume server CPU, network bandwidth, Docker cache space, and more update time than prebuilt images.
- Basic endpoint smoke tests cannot prove full provider compatibility.
- A manual rollback after healthy use can lose state written since the snapshot.

The update flow limits these risks with build-before-stop behavior, health checks, state snapshots, retained prior images, and automatic rollback before a failed deployment is exposed as healthy.
