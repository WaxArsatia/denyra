# Forward-only active-development lifecycle design

Date: 2026-08-25

Status: Review requested

## Purpose

Denyra is still in active development. Its management lifecycle should favor short update cycles and direct fix-forward recovery while preserving media and service state. Disaster backup, restore, update snapshots, and rollback add code and operational branches that are not needed at this stage.

This document replaces the rollback and backup behavior in `plans/001-simplified-local-deployment-design.md`. It also replaces backup, restore, snapshot, and rollback requirements in older specifications and runbooks. Other data ownership, validation, migration, authentication, and filesystem rules remain in force.

## Decisions

Denyra uses a forward-only deployment lifecycle:

- `/data/library`, `/data/library-unmanaged`, and `/data/state` survive setup and update operations.
- Update never restores an older state tree, configuration tree, database, or image set.
- A failed update is repaired by a later update or an explicit operator fix. It does not trigger automatic rollback.
- Database migrations remain transactional, ordered, idempotent, and forward compatible with the application that introduces them.
- Build failures leave the running deployment unchanged.
- Failures after cutover leave the new deployment stopped or degraded with actionable evidence. Existing media and state remain in place.
- Management cleanup is scoped to inactive Denyra containers, images, networks, build cache, and temporary update workspaces. It never removes media, service state, unresolved processing data, or unrelated Docker resources.

The project does not provide disaster backup or historical rollback commands during active development.

## Removed capabilities

The following user-facing commands and implementation paths are removed:

- `./denyra backup`
- `./denyra restore`, if exposed by any wrapper or runbook
- `./denyra rollback`
- Restic Compose service and profile
- backup and restore scripts, manifests, verification tools, tests, configuration, secrets, and runbooks
- update snapshot capture, retention, restore, metadata, and prior-image override logic
- automatic rollback after failed startup or smoke checks

The root management command must reject removed command names as unsupported. It must not retain hidden compatibility paths that can mutate old backup or snapshot data.

## Preserved data

Setup and update treat these paths as durable:

- `${DENYRA_DATA_ROOT}/library`
- `${DENYRA_DATA_ROOT}/library-unmanaged`
- `${DENYRA_DATA_ROOT}/state`
- `${DENYRA_DATA_ROOT}/incoming`
- `${DENYRA_DATA_ROOT}/processing`
- `${DENYRA_DATA_ROOT}/quarantine`
- unresolved acquisition download directories referenced by Gateway or Pipeline state
- secrets and generated deployment configuration under `${DENYRA_CONFIG_DIR}`

No management command may reset, truncate, replace, or recursively delete these paths. Existing SQLite databases and WAL files remain service-owned. Final Managed-library mutation remains owned by Lidarr. Navidrome remains read-only for both libraries.

Downloads and cache are not durable by default, but cleanup must prove that an entry is inactive and unreferenced before deletion. A completed slskd or SpotiFLAC directory awaiting correlation, validation, or import is active state and must survive cleanup.

## Update flow

`./denyra update` performs these steps under the existing exclusive management lock:

1. Validate deployment paths, configuration, secrets, free space, Docker access, and current service identity.
2. Require a clean repository and a fast-forward update target.
3. Fetch and fast-forward the repository.
4. Render configuration and build replacement images without stopping the running stack.
5. Run static compatibility checks that do not mutate service state.
6. Recreate the stack with orphan removal and wait for container health.
7. Run service smoke checks and report the deployed Git commit and observed upstream versions.
8. Remove only inactive Denyra update workspaces and superseded Denyra images that no running container references.

The update command has no snapshot preparation, state copy, prior-image manifest, retention pass, or rollback branch.

## Migration rules

Custom-service schema migrations execute at service startup and follow these rules:

- Each migration runs in a database transaction.
- A migration records its version only after its transaction commits.
- Re-running startup after interruption is safe.
- Application code accepts the schema state produced by every migration in the same release.
- Migrations do not remove or reinterpret media identity without a separate, explicit data migration design.
- Destructive schema changes use additive replacement first: add new structures, backfill deterministically, switch readers and writers, then remove obsolete structures only in a later approved change.

Lidarr, Navidrome, slskd, and SFTPGo own their databases. Denyra does not rewrite or downgrade third-party schemas.

## Failure behavior

Failure handling depends on when the failure occurs:

- Before cutover: keep the old containers running and remove the failed temporary build workspace.
- During recreation, before custom-service migrations commit: report the failed service and leave durable data unchanged by the failed transaction.
- After one or more migrations commit: keep the new state, do not start older application images, and require fix-forward recovery.
- During health or smoke checks: retain the new containers and logs for diagnosis. Stop a crash-looping service when continued retries would only create noise.
- On interruption: the operating system releases the process lock. A later `update` reads the deployed commit, repository commit, container identities, and health before deciding which phase to resume.

Every failure prints the failing phase, affected service, active Git commit, log command, and safe retry command. Secret values and secret file contents remain redacted.

## Clean development lifecycle

The lifecycle keeps only current operational state:

- one active Compose project
- current source checkout
- current generated configuration and secrets
- durable media and service state
- unresolved acquisition, processing, and quarantine data
- logs needed for current diagnosis

Successful update removes temporary build and update directories. Image cleanup uses resolved container references and Denyra ownership labels; it does not run global Docker prune commands.

Existing `${DENYRA_HOME}/updates` snapshots and obsolete backup workspaces are deleted only after the new update flow passes its acceptance tests on production. The deletion command resolves each target below the canonical Denyra root, rejects symlinks, prints the exact target set, and requires explicit operator confirmation. Restic repositories outside `DENYRA_HOME` are not discovered or deleted automatically.

## Security boundary

Removing backup and rollback removes their bearer-token subprocess paths and state-copy workspaces. Remaining management commands keep these controls:

- secret files never appear in process arguments or logs
- generated files use restrictive permissions and atomic rename
- update uses an exclusive lock
- cleanup validates canonical paths and ownership before deletion
- no command performs global Docker cleanup
- host-published services retain the documented private-network trust boundary

## Repository and interface changes

Implementation removes or updates:

- backup and restore directories under `scripts/`
- `scripts/manage/backup.sh`, `scripts/manage/rollback.sh`, and `scripts/manage/snapshot.sh`
- backup and rollback dispatch/help text in the root management command
- Restic mounts, profile, variables, and service in Compose
- backup, restore, rollback, and snapshot integration tests
- backup and restore runbooks and references from README, install, upgrade, incidents, and acceptance documentation
- snapshot-specific update configuration and status output
- CI checks that require removed artifacts

Update tests are rewritten around forward-only behavior. Tests for media/state preservation remain and become hard lifecycle invariants.

## Acceptance criteria

The lifecycle change is complete when all conditions hold:

1. No supported command, Compose profile, documentation path, or CI job advertises backup, restore, snapshot, or rollback.
2. A build failure leaves the running stack and durable data unchanged.
3. A transactional migration failure leaves its database at the last committed schema version.
4. A post-migration health failure never starts older application images against the newer schema.
5. Re-running `./denyra update` after interruption converges on the selected commit without state reset.
6. Managed and Unmanaged media, third-party state, Denyra databases, unresolved acquisition data, incoming uploads, processing data, and quarantine survive update.
7. Successful update removes temporary Denyra build/update artifacts and unreferenced Denyra images without touching unrelated Docker projects.
8. Production acceptance runs successfully before old `${DENYRA_HOME}/updates` snapshots are deleted.
9. Repository verification and integration tests pass from a clean checkout.

## Scope boundary

This design covers deployment lifecycle simplification. Acquisition recovery, SpotiFLAC packaging, Admin acquisition UX, authentication throttling, query performance, and state-model fixes remain separate capability changes. They may rely on the preservation and fix-forward rules defined here, but they get their own implementation plans.
