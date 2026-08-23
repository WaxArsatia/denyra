# Operations and Clients Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish Denyra as an operable deployment with pinned Navidrome streaming, deterministic health/storage behavior, structured redacted logging, supported Restic backup/restore, explicit upgrades/rollback, client setup, recovery drills, and end-to-end acceptance evidence.

**Architecture:** Compose runs only locked server images. Navidrome observes the read-only library and owns playback/user state. Gateway and pipeline expose local readiness and degraded dependency data. Maintenance coordinates deterministic snapshots: custom SQLite databases use online backup, stateful third-party services stop briefly, Restic writes to an explicitly external repository, and restore always verifies a new tree before cutover.

**Tech Stack:** Docker Compose v2.40.3, Navidrome 0.63.2, Navidrome Lyrics Plugin 7.2.0, Restic 0.19.1, `log/slog` JSON, shell runbooks/scripts, Go integration/acceptance harness, Feishin 1.15.1, Tempus 4.25.0.

**Spec:** `docs/superpowers/specs/2026-08-24-operations-and-clients-design.md`

## Global Constraints

- Complete the foundation, pipeline, and gateway implementation plans first.
- Navidrome and every non-Lidarr process mount `/data/library` read-only.
- Filesystem watcher is primary discovery; one-minute scheduled scan is recovery.
- External providers may degrade service status but cannot fail local readiness.
- Low storage stops only new claims/acquisitions/imports; recovery-capable actions stay available.
- Active SQLite databases are backed up only with the online backup API.
- Restic repository must resolve outside the `/data` filesystem; restore never writes over live data.
- No runtime auto-update or floating dependency identity.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `deploy/config/navidrome.toml` | Read-only library scanning, plugin, lyrics, and transcoding state configuration |
| `deploy/config/lidarr/` | Completed Download Handling, Import Extra Files, metadata/artwork ownership settings |
| `deploy/compose.yaml` | Final services, health checks, backup profile, mounts, and start ordering |
| `internal/platform/storage/` | `/data` filesystem capacity admission policy |
| `internal/platform/logsafe/` | Structured fields and mandatory secret redaction |
| `scripts/backup/` | Maintenance, online database backup, Restic snapshot/verify/retention |
| `scripts/restore/` | New-directory restore and cutover checks |
| `scripts/upgrade/` | Lock-driven upgrade, smoke, and rollback helpers |
| `docs/runbooks/` | Installation, backup, restore, upgrade, incident recovery, clients |
| `tests/integration/operations/` | Health, storage, logging, Navidrome, Restic tests |
| `tests/acceptance/denyra_test.go` | Full-system acceptance harness |

## Task 1: Configure pinned Navidrome and lyrics behavior

**Files:**

- Create: `deploy/config/navidrome.toml`
- Create: `deploy/config/navidrome-lyrics.toml`
- Update: `deploy/compose.yaml`
- Create: `tests/integration/operations/navidrome_config_test.go`
- Create: `tests/integration/operations/navidrome_discovery_test.go`

- [ ] Configure music folder `/music`, state/cache/transcoding/plugin directories under `/data/state/navidrome`, startup scan enabled, watcher enabled with `Scanner.WatcherWait = "5s"`, and scheduled scan every `1m`.
- [ ] Install the verified `nd-lyrics.ndp` 7.2.0 in the derived pinned image and disable plugin auto-reload/update.
- [ ] Set lyrics priority exactly `.ttml,.elrc,.lrc,embedded,nd-lyrics`; configure LRCLIB for runtime fallback without a write path to `/music`.
- [ ] Assert Compose maps `/data/library:/music:ro` and separate RW state. Start the container and prove a write to `/music` fails.
- [ ] Import a synthetic release into a test library and observe watcher discovery within its bounded wait. Disable/delay watcher event and prove the scheduled scan recovers within one scan interval.
- [ ] Confirm sidecar lyrics win over plugin output and no runtime lookup changes any library checksum.
- [ ] Run `rtk go test ./tests/integration/operations -run Navidrome`; expect pass.
- [ ] Commit with message `feat(ops): configure read-only Navidrome streaming`.

## Task 2: Implement health, degraded dependencies, storage admission, and redacted logs

**Files:**

- Create: `internal/platform/storage/admission.go`
- Create: `internal/platform/storage/admission_test.go`
- Update: `internal/platform/health/service.go`
- Update: `internal/platform/logsafe/redact.go`
- Create: `tests/integration/operations/health_test.go`
- Create: `tests/integration/operations/logging_test.go`

- [ ] Calculate free bytes/percent from the exact filesystem containing `/data`. Admission is blocked when free space is below `max(configured 20GiB, configured 5%)`, including exact-boundary tests.
- [ ] Apply admission only to new claim, acquisition, and import entry points. Cleanup, quarantine action, reconciliation, backup/recovery, cancellation, and capacity-restoring administration remain callable.
- [ ] Make readiness require config/pins, migrations/DB, tools, path permissions/device identity, and required internal service contract. Mark MusicBrainz, LRCLIB, Soulseek, and SpotiFLAC providers as named degraded dependencies without changing ready to false.
- [ ] Emit JSON logs with service, build, config hash, request/job/candidate/submission/correlation IDs, state, revision, and error class. Use UTC RFC3339Nano timestamps.
- [ ] Capture logs from auth, HTTP, subprocess, and adapter failures and assert passwords, API/bearer/session/CSRF tokens, secret-file contents, and provider-sensitive values never appear.
- [ ] Test database/disk/tool/internal failure as not-ready, external outage as ready+degraded, low-storage admission matrix, and secret-bearing nested errors.
- [ ] Run `rtk go test -race ./internal/platform/... ./tests/integration/operations -run 'Health|Storage|Logging'`; expect pass.
- [ ] Commit with message `feat(ops): enforce operational readiness and storage gates`.

Admission result stays explicit:

```go
type Admission struct {
	Allowed          bool
	AvailableBytes   uint64
	TotalBytes       uint64
	RequiredBytes    uint64
	RequiredPercent  float64
	FilesystemDevice uint64
}
```

## Task 3: Implement deterministic maintenance and online database backup

**Files:**

- Create: `internal/gateway/transport/maintenance.go`
- Create: `internal/pipeline/internalapi/maintenance.go`
- Create: `scripts/backup/backup.sh`
- Create: `scripts/backup/lib.sh`
- Create: `scripts/backup/verify-repository.sh`
- Update: `deploy/compose.yaml`
- Test: `tests/integration/operations/backup_test.go`

- [ ] Add authenticated maintenance endpoints that stop new mutations, drain active effects to a persisted safe point, report unresolved work, and invoke the SQLite online backup API into a unique `/data/backups/<backup-id>` workspace.
- [ ] Make backup fail before stopping services unless Restic repository/config/password are explicit and repository storage device/target is not the `/data` filesystem.
- [ ] Enter maintenance and drain gateway/pipeline. Briefly stop Lidarr, Navidrome, SFTPGo, and slskd for the simplest deterministic snapshot.
- [ ] Create online backups of both custom SQLite databases, then snapshot library, service state/config, incoming, processing, quarantine, and online-backup outputs. Raw acquisition downloads may be excluded explicitly.
- [ ] Run `restic check`/snapshot verification, apply retention `--keep-daily 7 --keep-weekly 4 --keep-monthly 12`, restart services, verify readiness, exit maintenance, and only then remove temporary workspace.
- [ ] Use traps so failure restarts previously running services and leaves evidence/workspace for diagnosis; do not claim a successful backup when snapshot verification fails.
- [ ] Test active SQLite writes during online backup, drain timeout, external repository enforcement, service stop/start ordering, snapshot content, retention arguments, verification failure, and secret redaction.
- [ ] Run `rtk go test ./tests/integration/operations -run Backup`; expect pass against a temporary local Restic repository on a different test filesystem/device fixture.
- [ ] Commit with message `feat(ops): add deterministic Restic backup path`.

## Task 4: Implement restore-to-new-tree and checksum cutover verification

**Files:**

- Create: `scripts/restore/restore.sh`
- Create: `scripts/restore/verify.sh`
- Create: `scripts/restore/cutover-check.sh`
- Create: `docs/runbooks/restore.md`
- Test: `tests/integration/operations/restore_test.go`

- [ ] Require explicit snapshot ID, Restic repository, and new absolute restore target. Reject `/`, home, workspace root, live `/data`, symlink targets, existing non-empty target, and unresolved variables.
- [ ] Restore snapshot into the new tree only. Verify Restic repository, stored manifest/checksums, both SQLite `integrity_check`, migration checksums, dependency lock identity, ownership/modes, canonical paths, and same-device atomic layout.
- [ ] Generate a human-readable cutover report listing source snapshot, build/config identities, database versions, file counts, bytes, checksum failures, and required owner changes.
- [ ] Keep cutover manual and documented. Never remove or overwrite the live tree. Describe rollback as returning service mounts to the untouched prior tree.
- [ ] Automate a restore drill that backs up fixtures, restores to a new path, starts custom services against restored DBs, verifies readiness, and compares library hashes.
- [ ] Run `rtk go test ./tests/integration/operations -run Restore`; expect pass.
- [ ] Commit with message `feat(ops): add verified restore drill`.

## Task 5: Create explicit upgrade and rollback workflow

**Files:**

- Create: `scripts/upgrade/verify-update.sh`
- Create: `scripts/upgrade/deploy.sh`
- Create: `scripts/upgrade/rollback.sh`
- Create: `docs/runbooks/upgrade.md`
- Create: `tests/integration/operations/upgrade_test.go`

- [ ] Accept only a reviewed lock-file change. Verify full image/artifact identities, platform, signatures/hashes, Debian snapshot versions, Go/Python/Node graphs, templ regeneration, plugin compatibility, and provider manifest requirements.
- [ ] Build new immutable images, write derived image digest/provenance to the lock, and fail if the working lock and image labels differ.
- [ ] Require a verified backup, then restore that backup into a test directory and run migrations plus smoke tests against the new binaries before deployment.
- [ ] Deploy exact digests, run health/Compose/contract/acceptance smoke checks, and retain prior lock/images/database backup references.
- [ ] Roll back binaries/images directly only when schema compatibility test says safe. Otherwise stop and restore the prior database snapshot/new tree as documented.
- [ ] Test floating tag rejection, hash/platform mismatch, Python graph drift, incompatible provider/Node manifest, migration failure, smoke failure, and rollback branch selection.
- [ ] Run `rtk go test ./tests/integration/operations -run Upgrade`; expect pass.
- [ ] Commit with message `feat(ops): define locked upgrade and rollback`.

## Task 6: Write deployment, incident, and client runbooks

**Files:**

- Create: `docs/runbooks/install.md`
- Create: `docs/runbooks/backup.md`
- Create: `docs/runbooks/incidents.md`
- Create: `docs/runbooks/clients.md`
- Create: `docs/runbooks/security-boundary.md`
- Test: `scripts/check-runbooks.sh`

- [ ] Document host prerequisites, numeric UID/GID creation, `/data` bootstrap, secret files, exact build/deploy commands, Lidarr/slskd/SFTPGo/Navidrome initial setup, bootstrap admin one-time behavior, and readiness verification.
- [ ] Record HTTP Admin UI bound to `0.0.0.0` without TLS/`Secure` cookie as an accepted risk; state that firewall/deployment limits port reachability and public exposure is out of scope.
- [ ] Document maintenance/backup/restore commands, expected state, failure recovery, required first restore drill, and repeated drill after backup schema/coverage changes.
- [ ] Document incidents: low disk, stuck lease, orphan directory, external outage, primary/fallback retry state, ambiguous import, partial mutation, corrupt DB, lost event, and session revocation.
- [ ] Document Feishin 1.15.1 and Tempus 4.25.0 setup against Navidrome/OpenSubsonic. Default original FLAC; explain logical `opus-256`/`opus-160` policies as requested bitrate/downsampling rather than hard-coded internal profile names.
- [ ] State that client binaries are not server containers and music authentication stays in Navidrome.
- [ ] Implement a runbook checker for valid commands/paths, no old product name, no floating tags, referenced file existence, and no secret examples that resemble real credentials.
- [ ] Run `rtk scripts/check-runbooks.sh`; expect pass.
- [ ] Commit with message `docs: add Denyra operations and client runbooks`.

## Task 7: Execute full-system failure and acceptance matrix

**Files:**

- Create: `tests/acceptance/denyra_test.go`
- Create: `tests/acceptance/harness/compose.go`
- Create: `tests/acceptance/harness/fixtures.go`
- Create: `tests/acceptance/harness/faults.go`
- Create: `deploy/compose.acceptance.yaml`
- Create: `docs/runbooks/acceptance-evidence.md`

- [ ] Start pinned real containers with fake/local adapters for external providers and generated non-copyrighted FLAC fixtures. Keep live-provider acceptance behind a separate explicit profile and credentials gate, outside automatic CI.
- [ ] Prove primary success never starts fallback; successful primary no-grab starts fallback; primary/fallback operational failures stay retryable; all legitimate fallback no-result schedules a 24-hour full cycle.
- [ ] Prove dual acquisition creates one atomic winner, one loser outcome, one pipeline import authorization, and one Lidarr Manual Import.
- [ ] Prove manual upload remains sealed, drift requires resubmit, all tracks resolve to one release, corrupt/ambiguous batches stay quarantined, and review approval moves back to work before mutation.
- [ ] Prove a valid release imports through Lidarr with same-basename `.lrc`, Lidarr creates `folder.jpg`, Navidrome watcher discovers it, scheduled scan repairs a missed event, and streaming leaves master FLAC checksums unchanged.
- [ ] Kill workers after each persisted intent and external-success boundary. Restart and prove no work loss, duplicate candidate, duplicate winner, duplicate mutation decision, or duplicate import.
- [ ] Trigger low storage and prove admission/allowed recovery matrix. Trigger all named external outages and prove ready+degraded with durable retries.
- [ ] Run backup, verify snapshot, restore to new tree, validate checksums/DBs/lock, and start services from restored state.
- [ ] Record exact image digests, build/config hashes, test commands, duration, and pass/fail evidence in `acceptance-evidence.md`.
- [ ] Run the final gate:

```sh
rtk go test -race ./...
rtk docker compose -f deploy/compose.yaml -f deploy/compose.acceptance.yaml config --quiet
rtk go test -count=1 ./tests/acceptance -run Denyra
rtk scripts/check-runbooks.sh
rtk scripts/verify-pins/verify.sh --offline
```

Expected result: all commands exit `0`; acceptance evidence identifies exact locked artifacts and confirms every system invariant.

- [ ] Commit with message `test: verify Denyra end to end`.

## Completion Gate

- [ ] Navidrome cannot write `/music`; watcher and recovery scan both have passing evidence.
- [ ] Readiness distinguishes local blocking failures from external degraded dependencies.
- [ ] Low-storage admission blocks only new claim/acquisition/import work.
- [ ] A verified Restic snapshot restores to a new directory with valid SQLite databases and file checksums.
- [ ] No service, script, runbook, or Compose manifest follows a floating dependency identity.
- [ ] `rtk go test -race ./...` and full acceptance pass twice from clean service state.
- [ ] Commit the final implementation evidence and record all four plan completion hashes.
