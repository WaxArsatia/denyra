# Simple Update, Rollback, and Backup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver `./denyra update`, automatic pre-healthy rollback, confirmed manual rollback, and simplified disaster backup without server-side compilation tools, test suites, lock files, or provenance evidence.

**Architecture:** The POSIX management command records the running image IDs and rendered Compose model, builds the candidate while the old stack runs, then briefly stops services to copy external configuration and state into a local update snapshot. A failed health gate restores that snapshot and starts exact prior image IDs through a generated Compose override. Restic remains an optional profile for disaster backup, while update snapshots never copy the music library.

**Tech Stack:** POSIX shell, Git, Docker Compose v2, Docker image IDs, Restic, Go restore verifier.

**Spec:** `plans/001-simplified-local-deployment-design.md`

## Global Constraints

- Require only Git, Docker Engine, Docker Compose v2, and standard Linux userland on the host.
- Build and pull while the old stack remains online; downtime starts only after every candidate image succeeds.
- Refuse updates when tracked source files are modified; allow ignored runtime locator files.
- Do not run gofmt, vet, tests, race detection, restore drills, lock rewriting, or provenance generation on the server.
- Snapshot configuration and service state, but exclude library, cache, raw downloads, processing media, and quarantine.
- Record resolvable content image IDs before pulling or rebuilding; never reconstruct a prior release from a mutable tag.
- Automatically roll back only before the candidate is declared healthy.
- Require confirmation for manual rollback because post-update writes may be discarded.
- Keep the two newest update snapshots and never prune images referenced by them.
- Restic stays optional and must use a repository outside `DENYRA_HOME`.
- Complete both earlier plans before this phase.

---

## File map

- `scripts/manage/update.sh`: clean-tree check, fast-forward, pull/build-before-stop, snapshot, candidate start, smoke, and automatic rollback trap.
- `scripts/manage/rollback.sh`: validated snapshot restore and exact image-ID start; confirmed manual entry.
- `scripts/manage/snapshot.sh`: explicit snapshot paths, metadata, image override generation, copy/restore, retention.
- `scripts/manage/backup.sh`: root-command adapter for the existing maintenance/Restic workflow.
- `scripts/backup/*.sh`: disaster backup under the external deployment root without lock/provenance files.
- `internal/platform/restore/restore.go`: manifest Git/config identity and checksum/database/layout validation.
- `cmd/denyra-restore-check/main.go`: simplified create/verify flags.
- `scripts/restore/*.sh`: verify restored data without an expected dependency lock.
- `tests/integration/operations/upgrade_test.go`: fake-command update/rollback behavior.
- `tests/integration/operations/backup_policy_test.go`, `restore_test.go`: simplified backup and restore contracts.
- `docs/runbooks/*.md`, `README.md`: only supported normal commands and retained safety boundaries.

### Task 1: Record exact prior images and a bounded update snapshot

**Files:**
- Create: `scripts/manage/snapshot.sh`
- Modify: `tests/integration/operations/upgrade_test.go`
- Test: `tests/integration/operations/upgrade_test.go`

**Interfaces:**
- Consumes: `denyra_compose`, `$DENYRA_HOME/config`, `$DENYRA_DATA_ROOT/state`, and running containers.
- Produces: `denyra_snapshot_prepare OLD_COMMIT`, `denyra_snapshot_name PENDING_DIR NEW_COMMIT`, `denyra_snapshot_capture SNAPSHOT`, `denyra_snapshot_restore SNAPSHOT`, `denyra_snapshot_latest`, and `denyra_snapshot_retain_two`; snapshot directories with `metadata.env`, `prior-compose.yaml`, `prior-images.yaml`, `config/`, and `state/`.

- [ ] **Step 1: Add snapshot fixture tests**

Use fake `docker compose config`, `docker compose ps -q SERVICE`, and `docker inspect --format '{{.Image}}' CONTAINER`. Assert the generated override has exactly these services:

```yaml
services:
  acquisition-gateway:
    image: sha256:<64-hex>
  media-pipeline:
    image: sha256:<64-hex>
  lidarr:
    image: sha256:<64-hex>
  slskd:
    image: sha256:<64-hex>
  sftpgo:
    image: sha256:<64-hex>
  navidrome:
    image: sha256:<64-hex>
```

Add failures for an absent container, non-`sha256:` identity, symlinked state/config root, snapshot outside `$DENYRA_UPDATES_DIR`, and an existing nonempty target. Assert snapshot content has no `library`, `downloads`, `processing`, `quarantine`, or `cache` path.

- [ ] **Step 2: Run snapshot tests and verify they fail**

Run: `go test ./tests/integration/operations -run 'UpdateSnapshot|PriorImage|SnapshotPath' -count=1`

Expected: FAIL because `snapshot.sh` is absent.

- [ ] **Step 3: Implement explicit snapshot validation and metadata**

`denyra_snapshot_prepare OLD_COMMIT` creates a mode-`0700` pending directory named `.pending-<old>-<pid>` under `$DENYRA_UPDATES_DIR`, records the prior Compose and image data from Step 4, and returns its path. After fast-forward, `denyra_snapshot_name PENDING_DIR NEW_COMMIT` atomically renames it to `YYYYMMDDTHHMMSSZ-<old>-to-<new>` and returns the final path. Validate both commits as exactly 40 lowercase hexadecimal characters, require each directory's canonical parent to equal `$DENYRA_UPDATES_DIR`, and reject symlinks and pre-existing final targets.

Write `metadata.env` atomically after the final rename with only:

```text
snapshot_schema=1
old_commit=<40-hex>
new_commit=<40-hex>
created_at=<RFC3339 UTC>
status=prepared
```

Do not source unvalidated metadata. Parse it through a helper that accepts only the five known keys and validates values before export.

- [ ] **Step 4: Record prior Compose and content image IDs**

Run `denyra_compose config > prior-compose.yaml` before source fast-forward. For each fixed default service, resolve the container ID with `denyra_compose ps -q "$service"`, then the content ID with:

```sh
docker inspect --format '{{.Image}}' "$container_id"
```

Require `sha256:` plus 64 lowercase hex characters and atomically render `prior-images.yaml`. Do not call `docker pull` or inspect a mutable repository tag.

- [ ] **Step 5: Copy only configuration and state**

After services stop, `denyra_snapshot_capture SNAPSHOT` uses `cp -a -- "$DENYRA_CONFIG_DIR/." "$snapshot/config/"` and `cp -a -- "$DENYRA_DATA_ROOT/state/." "$snapshot/state/"`. Validate the config source is within `DENYRA_HOME`, the state source is within the validated `DENYRA_DATA_ROOT`, both are real directories, and snapshot destinations are empty. Record `status=snapshotted` by atomically replacing `metadata.env`.

- [ ] **Step 6: Implement restore and two-snapshot retention**

Restore is allowed only while the stack is stopped. Move current config/state to `$snapshot/failed-config` and `$snapshot/failed-state`, copy the prior trees back, and leave failed trees for diagnosis. If any restore copy fails, stop and report both retained locations; do not delete either tree.

Retention selects successful or failed snapshot directories by validated timestamp prefix, sorts newest first, and removes only entries after the second. It must reject symlinks and paths whose canonical parent differs from `$DENYRA_UPDATES_DIR`. Do not prune Docker images automatically.

- [ ] **Step 7: Run snapshot tests**

Run: `go test ./tests/integration/operations -run 'UpdateSnapshot|PriorImage|SnapshotPath' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit snapshot primitives**

```bash
git add scripts/manage/snapshot.sh tests/integration/operations/upgrade_test.go
git commit -m "feat(update): record prior state and image identities"
```

### Task 2: Implement build-before-stop update and automatic rollback

**Files:**
- Create: `scripts/manage/update.sh`
- Create: `scripts/manage/rollback.sh`
- Modify: `denyra`
- Modify: `scripts/manage/common.sh`
- Modify: `tests/integration/operations/upgrade_test.go`
- Test: `tests/integration/operations/upgrade_test.go`

**Interfaces:**
- Consumes: phase-2 `denyra_compose`, `denyra_smoke`, and snapshot functions.
- Produces: `denyra_update`, `denyra_rollback_to SNAPSHOT REASON`, and interactive `denyra_rollback`.

- [ ] **Step 1: Add update order and interruption tests**

With fake Git/Docker, assert this successful order:

```text
git diff --quiet
git diff --cached --quiet
docker compose config
docker inspect prior image IDs
git fetch origin main
git merge --ff-only origin/main
docker compose pull --policy always slskd sftpgo restic
docker compose build --pull
docker compose stop
copy config/state snapshot
docker compose up -d --wait
smoke
mark success
```

Assert dirty tracked files stop before fetch, failed pull/build leaves the old stack untouched, failed snapshot triggers prior stack restart, failed health triggers state restore plus prior image override, and an interrupt after stop follows the same rollback path.

Add a retry case where the Git worktree already contains the candidate commit but `denyra.env` still records the old deployed `DENYRA_GIT_COMMIT` after an earlier build failure. The second update must rebuild and deploy that candidate instead of reporting `already current`.

- [ ] **Step 2: Run update tests and verify they fail**

Run: `go test ./tests/integration/operations -run 'UpdateOrder|DirtyTree|BuildFailure|AutomaticRollback|InterruptedUpdate' -count=1`

Expected: FAIL because update/rollback are absent.

- [ ] **Step 3: Implement clean fast-forward and candidate build**

Use:

```sh
git diff --quiet --ignore-submodules -- || denyra_die "tracked source files have local changes"
git diff --cached --quiet --ignore-submodules -- || denyra_die "tracked source files have staged changes"
branch=$(git symbolic-ref --quiet --short HEAD) || denyra_die "updates require a checked-out branch"
active_commit=$(sed -n 's/^DENYRA_GIT_COMMIT=//p' "$DENYRA_CONFIG_DIR/denyra.env")
[ -n "$active_commit" ] || denyra_die "active deployment commit is missing; rerun setup"
pending_snapshot=$(denyra_snapshot_prepare "$active_commit")
git fetch origin "$branch"
git merge --ff-only "origin/$branch"
new_commit=$(git rev-parse HEAD)
active_snapshot=$(denyra_snapshot_name "$pending_snapshot" "$new_commit")
```

Validate `active_commit` as exactly 40 lowercase hexadecimal characters and require exactly one matching key in `denyra.env`. If `new_commit` equals the active deployed commit, remove the validated pending directory, print `already current`, and exit before pull/build. Otherwise export short `DENYRA_IMAGE_TAG`, full `DENYRA_GIT_COMMIT=$new_commit`, and a UTC `DENYRA_RELEASE_REFRESH`. Pull only non-build services with `denyra_compose pull --policy always slskd sftpgo restic`; Restic pull failure is nonfatal when no local Restic image exists and backup is unused. Build all custom/derived services with `denyra_compose build --pull`. No stop occurs before every command succeeds. A pull/build failure removes only the validated pending snapshot; it does not rewind source or touch the running stack, and the next update retries because `denyra.env` still names the old active commit.

- [ ] **Step 4: Implement the cutover trap**

Track `cutover_started=false`, `deployment_healthy=false`, and `active_snapshot`. The signal/exit trap calls `denyra_rollback_to "$active_snapshot" "interrupted or unhealthy update"` only when cutover started and health is false. Prevent recursive traps by clearing the trap at the start of rollback.

Stop the full stack, call `denyra_snapshot_capture "$active_snapshot"`, atomically set `DENYRA_IMAGE_TAG=<new-short>` and `DENYRA_GIT_COMMIT=<new-full>` in `denyra.env`, then run `denyra_compose up -d --remove-orphans --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}"` and `denyra_smoke`.

- [ ] **Step 5: Implement exact-image rollback**

`denyra_rollback_to` must:

1. validate and load snapshot metadata;
2. run `denyra_compose stop` and tolerate already-stopped services;
3. when status is `snapshotted` or `successful`, call `denyra_snapshot_restore "$snapshot"`; when status is still `prepared`, leave current config/state untouched because capture never mutated them; reject every other status;
4. verify every image in `prior-images.yaml` exists with `docker image inspect`;
5. run `docker compose --project-name denyra -f "$snapshot/prior-compose.yaml" -f "$snapshot/prior-images.yaml" up -d --remove-orphans --wait --wait-timeout ...`;
6. run the same smoke contract against the prior model;
7. mark snapshot `status=rolled_back` and print the failed tree locations.

If any image is absent, stop with `prior image missing: <service> <id>` and leave all state copies intact. Never rebuild an approximation.

- [ ] **Step 6: Mark success and retain snapshots**

Only after smoke passes, set `deployment_healthy=true`, replace metadata status with `successful`, clear rollback traps, and call `denyra_snapshot_retain_two`. Print old/new commits, elapsed time, and service URLs without secrets.

- [ ] **Step 7: Add confirmed manual rollback**

`denyra_rollback` obtains the newest `successful` snapshot through `denyra_snapshot_latest`, prints old/new commits and:

```text
Rollback will discard service-state writes made after this update. Continue? [y/N]
```

Accept only `y` or `yes` case-insensitively. On confirmation, call the same `denyra_rollback_to`; on anything else exit 0 with `rollback cancelled`. The Git worktree remains on its current commit; rollback controls the active images and state only.

- [ ] **Step 8: Run update and rollback tests**

Run: `go test ./tests/integration/operations -run 'Update|Rollback|DirtyTree|BuildFailure|Interrupted' -count=1`

Expected: PASS.

- [ ] **Step 9: Commit update and rollback**

```bash
git add denyra scripts/manage/common.sh scripts/manage/update.sh scripts/manage/rollback.sh tests/integration/operations/upgrade_test.go
git commit -m "feat(update): add health-gated update and rollback"
```

### Task 3: Remove dependency identity from backup and restore verification

**Files:**
- Modify: `internal/platform/restore/restore.go`
- Modify: `cmd/denyra-restore-check/main.go`
- Modify: `tests/integration/operations/restore_test.go`
- Modify: `scripts/restore/verify.sh`
- Modify: `scripts/restore/cutover-check.sh`
- Test: `tests/integration/operations/restore_test.go`

**Interfaces:**
- Consumes: restored source/workspace trees, database migration ledgers, expected UID/GID, and the Denyra Git commit recorded at backup.
- Produces: `restore.Manifest{SchemaVersion, BackupID, CreatedAt, GitCommit, SourceFiles, WorkspaceFiles}` and `VerifyOptions` with no expected lock.

- [ ] **Step 1: Rewrite restore fixtures without lock files**

Remove fixture creation of `dependencies.lock.json` and `images.lock.json`. Create `workspace/config/gateway.toml`, `pipeline.toml`, `navidrome.toml`, `navidrome-lyrics.toml`, and `slskd.yml`. Call:

```go
manifest, err := denyrarestore.Create(denyrarestore.CreateOptions{
	BackupID: "fixture-backup",
	GitCommit: strings.Repeat("a", 40),
	SourceRoot: source,
	WorkspaceRoot: workspace,
	CreatedAt: time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC),
})
```

Verify with `VerifyOptions{RestoreRoot: root}`. Assert the report contains the Git commit, checksums configuration and media/state files, verifies databases, and rejects a changed config file as well as a changed library file.

- [ ] **Step 2: Run restore tests and verify they fail**

Run: `go test ./tests/integration/operations -run 'RestoreFixture' -count=1`

Expected: FAIL because create/verify still require dependency-lock identity.

- [ ] **Step 3: Simplify manifest identity**

Replace `Identities map[string]string` with:

```go
type Manifest struct {
	SchemaVersion  int          `json:"schema_version"`
	BackupID       string       `json:"backup_id"`
	CreatedAt      time.Time    `json:"created_at"`
	GitCommit      string       `json:"git_commit"`
	SourceFiles    []FileRecord `json:"source_files"`
	WorkspaceFiles []FileRecord `json:"workspace_files"`
}
```

Require a 40-character lowercase hexadecimal Git commit. Scan the complete workspace except manifest/report outputs; do not special-case lock/provenance filenames. Remove `ExpectedLock` from `VerifyOptions`, the lock comparison, and identity rendering from the cutover report. Preserve file checksum, ownership, no-symlink, database integrity/migration, and single-filesystem checks.

- [ ] **Step 4: Simplify the restore-check command and scripts**

Add `--git-commit` to `create`. Remove `--expected-lock` from `verify`. Update restore scripts to pass the backup's recorded snapshot and ownership only. Preserve `--overwrite never`, verification before cutover, and the explicit manual cutover boundary.

- [ ] **Step 5: Run restore tests**

Run: `go test ./internal/platform/restore ./cmd/denyra-restore-check ./tests/integration/operations -run 'Restore|Create|Verify' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit simplified restore identity**

```bash
git add internal/platform/restore/restore.go cmd/denyra-restore-check/main.go tests/integration/operations/restore_test.go scripts/restore/verify.sh scripts/restore/cutover-check.sh
git commit -m "refactor(backup): verify state without dependency locks"
```

### Task 4: Adapt disaster backup to the external deployment root

**Files:**
- Create: `scripts/manage/backup.sh`
- Modify: `scripts/backup/backup.sh`
- Modify: `scripts/backup/lib.sh`
- Modify: `scripts/backup/verify-repository.sh`
- Modify: `deploy/compose.yaml`
- Modify: `tests/integration/operations/backup_policy_test.go`
- Test: `tests/integration/operations/backup_policy_test.go`

**Interfaces:**
- Consumes: `$DENYRA_HOME`, external Restic repository path, generated Restic password, and application maintenance endpoints.
- Produces: `./denyra backup` with a manifest, consistent SQLite backups, encrypted config/secrets/data coverage, integrity check, and existing retention policy.

- [ ] **Step 1: Rewrite backup-policy tests**

Assert the backup command:

- enables maintenance and waits safe;
- stops Lidarr, Navidrome, SFTPGo, and slskd;
- creates gateway/pipeline SQLite backups;
- copies `$DENYRA_CONFIG_DIR` into the backup workspace;
- records `git rev-parse HEAD` in the manifest;
- backs up `/source/config`, `/source/secrets`, `/source/data/library`, `/source/data/state`, `/source/data/incoming`, `/source/data/processing`, `/source/data/quarantine`, and the current manifest/database workspace at `/workspace/<backup-id>`;
- excludes downloads, cache, updates, live SQLite/WAL/SHM files, and the human-readable credential report;
- runs `restic check` and keeps daily 7, weekly 4, monthly 12.

Assert no script references lock, image-lock, or provenance filenames.

- [ ] **Step 2: Run backup tests and verify they fail**

Run: `go test ./tests/integration/operations -run 'Backup' -count=1`

Expected: FAIL because the old backup copies strict identity files and assumes `/data`.

- [ ] **Step 3: Mount the complete deployment root read-only for Restic**

For the optional Restic service, mount `${DENYRA_HOME}` at `/source:ro`, `${DENYRA_DATA_ROOT}/backups` at `/workspace:ro`, and the external repository at `/repository`. Keep `network_mode: none`, the password secret, and the `backup` profile. Reject a repository path inside `DENYRA_HOME` in `verify-repository.sh`.

- [ ] **Step 4: Rewrite backup workspace and manifest creation**

Use `$DENYRA_DATA_ROOT/backups/$DENYRA_BACKUP_ID` for temporary workspace. Copy config recursively into `$workspace/config`, make application SQLite backups as today, and run:

```sh
denyra_restore_tool \
  -v "$DENYRA_DATA_ROOT:/data-source:ro" -v "$workspace:/workspace" \
  media-pipeline create --source /data-source --workspace /workspace \
  --backup-id "$DENYRA_BACKUP_ID" --git-commit "$(git rev-parse HEAD)"
```

Remove every lock/provenance copy. Keep cleanup that restarts stopped services and disables maintenance even when backup fails.

- [ ] **Step 5: Simplify the Restic file set**

Run Restic against the explicit paths from Step 1, including `/workspace/$DENYRA_BACKUP_ID`. Exclude `/source/credentials.txt`, `/source/data/downloads`, `/source/data/cache`, `/source/updates`, `/source/data/backups`, and live Denyra SQLite files replaced by workspace copies. Do not exclude Lidarr/Navidrome/SFTPGo/slskd state databases because those services are stopped. The separate `/workspace` mount ensures the current manifest and consistent Denyra database copies remain included even though the temporary workspace is excluded through its `/source/data/backups` alias.

- [ ] **Step 6: Add the root-command adapter and run tests**

`denyra_backup` loads the generated Restic secret path, requires an explicit `DENYRA_RESTIC_REPOSITORY_PATH`, and invokes the backup script through the shared Compose context. Run:

```bash
go test ./tests/integration/operations -run 'Backup' -count=1
docker compose -f deploy/compose.yaml --profile backup config --quiet
```

Expected: PASS.

- [ ] **Step 7: Commit simplified disaster backup**

```bash
git add scripts/manage/backup.sh scripts/backup deploy/compose.yaml tests/integration/operations/backup_policy_test.go
git commit -m "refactor(backup): simplify external-root disaster backups"
```

### Task 5: Replace obsolete upgrade scripts and complete acceptance coverage

**Files:**
- Delete: `scripts/upgrade/deploy.sh`
- Delete: `scripts/upgrade/lib.sh`
- Delete: `scripts/upgrade/rollback.sh`
- Delete: `scripts/upgrade/verify-update.sh`
- Modify: `tests/acceptance/denyra_test.go`
- Modify: `tests/acceptance/harness/faults.go`
- Modify: `tests/integration/operations/upgrade_test.go`
- Test: `tests/acceptance/denyra_test.go`

**Interfaces:**
- Consumes: `./denyra update` and injected Compose health failure.
- Produces: acceptance proof that failed candidate startup restores old state and image IDs.

- [ ] **Step 1: Add an acceptance test for forced update failure**

The test must:

1. start a healthy fixture deployment and write sentinel files into gateway, pipeline, Lidarr, Navidrome, SFTPGo, and slskd state;
2. record every running container image ID;
3. point update at a local fast-forward fixture commit whose pipeline healthcheck intentionally fails;
4. run `./denyra update` and require nonzero exit;
5. assert all sentinel files match their pre-update bytes;
6. assert every running service uses its recorded prior image ID;
7. assert library fixture files are unchanged and were absent from the update snapshot;
8. assert snapshot status is `rolled_back` and failed candidate state was retained.

- [ ] **Step 2: Run the acceptance test and verify it fails**

Run: `go test ./tests/acceptance -run 'FailedUpdateRestoresPriorStateAndImages' -count=1`

Expected: FAIL until the harness fault and update path are connected.

- [ ] **Step 3: Add a health-failure fault without changing production images**

Use an acceptance-only Compose override that replaces the candidate pipeline healthcheck with `CMD-SHELL exit 1` when `DENYRA_ACCEPTANCE_FAIL_HEALTH=1`. Do not add a production environment switch that can disable health.

- [ ] **Step 4: Delete superseded upgrade scripts and update integration assertions**

Delete all four `scripts/upgrade` files. Replace token-search tests with behavior tests that invoke root management scripts using fakes. Search production paths:

```bash
rg -n 'scripts/upgrade|DENYRA_UPGRADE_|verified-update\.env|approval\.json|prior-dependencies|prior-images\.lock' --glob '!docs/**' --glob '!plans/**'
```

Expected: no output.

- [ ] **Step 5: Run update/rollback acceptance and operation tests**

Run:

```bash
go test ./tests/integration/operations -run 'Update|Rollback|Backup|Restore' -count=1
go test ./tests/acceptance -run 'Setup|FailedUpdateRestoresPriorStateAndImages' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit update acceptance and old-script deletion**

```bash
git add tests/acceptance tests/integration/operations/upgrade_test.go
git rm scripts/upgrade/deploy.sh scripts/upgrade/lib.sh scripts/upgrade/rollback.sh scripts/upgrade/verify-update.sh
git commit -m "test(update): prove automatic rollback restores prior release"
```

### Task 6: Rewrite operator documentation around the supported commands

**Files:**
- Modify: `README.md`
- Modify: `docs/runbooks/install.md`
- Modify: `docs/runbooks/upgrade.md`
- Modify: `docs/runbooks/backup.md`
- Modify: `docs/runbooks/restore.md`
- Modify: `docs/runbooks/incidents.md`
- Modify: `docs/runbooks/security-boundary.md`
- Modify: `docs/runbooks/clients.md`
- Modify: `docs/runbooks/acceptance-evidence.md`
- Modify: `scripts/check-runbooks.sh`
- Test: `scripts/check-runbooks.sh`

**Interfaces:**
- Consumes: the final root management command behavior.
- Produces: one supported operator path for install, normal lifecycle, update, rollback, credentials, backup, restore, and local-client URLs.

- [ ] **Step 1: Rewrite runbook contract checks first**

Require install and upgrade docs to contain:

```text
./denyra setup
./denyra update
./denyra rollback
./denyra credentials
./denyra backup
```

Reject references to `dependencies.lock.json`, `images.lock.json`, `verify-pins`, `docker buildx bake`, manual CPython/Node/Go installation, manual provenance, upgrade approval files, static `172.30.0.*` addresses, and manual Lidarr/slskd/SFTPGo/Navidrome browser setup.

- [ ] **Step 2: Run the runbook check and verify it fails**

Run: `scripts/check-runbooks.sh`

Expected: FAIL against the old manual install/upgrade procedures.

- [ ] **Step 3: Rewrite install and lifecycle documentation**

The normal install path is exactly:

```sh
git clone https://github.com/WaxArsatia/denyra.git
cd denyra
./denyra setup
```

Explain the Soulseek prompt, optional `DENYRA_HOME`, one possible sudo request for `/srv/denyra`, generated credential lookup, service URLs, and `./denyra start|stop|restart|status|logs`. Do not document underlying Compose commands as normal operation.

- [ ] **Step 4: Rewrite update, rollback, backup, and restore documentation**

Document build-before-stop, local snapshot exclusions, automatic pre-healthy rollback, manual rollback data-loss confirmation, exact prior image requirement, two-snapshot retention, and the unchanged Git worktree after rollback. Distinguish local update snapshots from optional encrypted Restic disaster backups. Keep restore checksum/database/layout verification and manual cutover instructions.

- [ ] **Step 5: Update security and client guidance**

State the basic boundary: local/private deployment, password-protected UIs, internal networks, secrets outside Git/images, no TLS/firewall/VPN automation, and no exact upstream provenance promise. Document local URLs: Denyra `http://localhost:8090`, Navidrome/Feishin `http://localhost:4533`, SFTPGo WebAdmin `http://localhost:8080`, and SFTP `localhost:2022`. Note that a server deployment uses the server's LAN address in clients.

- [ ] **Step 6: Run documentation and repository-wide gates**

Run:

```bash
scripts/check-runbooks.sh
make verify
go test ./tests/integration/operations -count=1
docker compose -f deploy/compose.yaml --profile setup --profile backup config --quiet
rg -n 'dependencies\.lock|images\.lock|build-provenance|dependency-lock|verify-pins|DENYRA_UPGRADE_|172\.30\.0\.' --glob '!docs/superpowers/**' --glob '!plans/**'
git diff --check
```

Expected: every command exits 0 and the final search has no output.

- [ ] **Step 7: Commit final operator workflow**

```bash
git add README.md docs/runbooks scripts/check-runbooks.sh
git commit -m "docs: document one-command denyra operations"
```

### Task 7: Final end-to-end verification

**Files:**
- Modify only if a verification failure exposes a plan-scoped defect.

**Interfaces:**
- Consumes: all three completed implementation plans.
- Produces: release-ready evidence for a clean setup, repeated setup, healthy update, failed update rollback, backup, and restore verification.

- [ ] **Step 1: Verify the source and Compose model**

Run:

```bash
make verify
DENYRA_RELEASE_REFRESH=final-verification docker compose -f deploy/compose.yaml build --pull
DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=local go test ./tests/integration -run 'Compose|ServiceImage|LidarrPluginInstall' -count=1
```

Expected: PASS.

- [ ] **Step 2: Verify clean and repeated setup**

Use a disposable absolute deployment root and non-production Soulseek fixture credentials:

```bash
DENYRA_HOME=/tmp/denyra-final-verification DENYRA_SOULSEEK_USERNAME=fixture DENYRA_SOULSEEK_PASSWORD=fixture-password ./denyra setup
DENYRA_HOME=/tmp/denyra-final-verification DENYRA_SOULSEEK_USERNAME=fixture DENYRA_SOULSEEK_PASSWORD=fixture-password ./denyra setup
DENYRA_HOME=/tmp/denyra-final-verification ./denyra status
```

Expected: both setup runs exit 0, the second reports no account/secret resets, and every service is healthy. Use a path created specifically for this verification; do not reuse a live deployment.

- [ ] **Step 3: Verify healthy and failed updates through acceptance tests**

Run: `go test ./tests/acceptance -run 'Setup|Update|Rollback' -count=1`

Expected: PASS, including exact prior image IDs after forced failure.

- [ ] **Step 4: Verify backup and restore contracts**

Run: `go test ./internal/platform/restore ./cmd/denyra-restore-check ./tests/integration/operations -run 'Backup|Restore' -count=1`

Expected: PASS.

- [ ] **Step 5: Verify removal and tree cleanliness**

Run:

```bash
rg -n 'dependencies\.lock|images\.lock|build-provenance|dependency-lock|verify-pins|DENYRA_UPGRADE_|docker buildx bake|Python-3\.|node-v[0-9]' --glob '!docs/superpowers/**' --glob '!plans/**'
git diff --check
git status --short
```

Expected: the search and `git diff --check` produce no output; `git status` contains only the intentional implementation changes before their final commit.

- [ ] **Step 6: Commit any verification-only correction**

If Step 1-5 required a scoped correction, commit only that correction:

```bash
git add <files-changed-by-the-correction>
git commit -m "fix(deploy): address final deployment verification"
```

If no correction was required, do not create an empty commit.
