# Forward-Only Development Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove disaster backup, restore, update snapshots, and rollback while making `./denyra update` safely fix-forward and preserving all media, service state, and unresolved work.

**Architecture:** Keep the existing exclusive management lock and build replacement images while the current Compose project is still running. Cut over only after source, configuration, pulls, builds, and static checks pass. Once cutover begins, never restore old config, databases, state, or images; retain the new evidence and retry forward. A separate confirmed cleanup command removes only the known legacy lifecycle directories and obsolete Restic secret after production acceptance.

**Tech Stack:** POSIX shell, Git, Docker Compose v2, Go 1.27 integration tests, SQLite migrations

**Spec:** `docs/superpowers/specs/2026-08-25-forward-only-active-development-lifecycle-design.md`

## Global Constraints

- Never reset, replace, truncate, or recursively delete `${DENYRA_DATA_ROOT}/library`, `library-unmanaged`, `state`, `incoming`, `processing`, or `quarantine`.
- Preserve unresolved directories under `downloads/slskd` and `downloads/spotiflac`; lifecycle cleanup is not acquisition cleanup.
- Keep Pipeline's per-candidate `.migration-backups` safety copies. They are transaction-local media mutation evidence, not lifecycle snapshots.
- Never run global `docker system prune`, `docker image prune`, `docker volume prune`, or `docker builder prune`.
- Before cutover, any failure leaves the active release environment and running containers unchanged.
- After cutover starts, no code path starts an older image or restores an older state/config tree.
- External Restic repositories are outside scope and must not be discovered or deleted.

---

### Task 1: Replace rollback-oriented update tests with forward-only invariants

**Files:**
- Modify: `tests/integration/operations/upgrade_test.go`
- Modify: `tests/integration/operations/management_command_test.go`
- Modify: `migrations/embed_test.go`

**Interfaces:**
- Consumes: `./denyra update`
- Produces: regression coverage for pre-cutover safety, post-cutover fix-forward behavior, migration atomicity, and durable-path preservation

- [ ] **Step 1: Delete snapshot and rollback fixture cases**

Remove tests that require `prior-compose.yaml`, `prior-images.yaml`, captured state, snapshot retention, automatic rollback, manual rollback, or `internal/platform/upgrade`. Keep the existing fake Git/Docker management fixture and update it to record the candidate and deployed commits separately.

- [ ] **Step 2: Add a complete durable-path sentinel fixture**

Create one sentinel in every protected tree before each update:

```go
protected := []string{
    "library/managed.flac",
    "library-unmanaged/unmanaged.flac",
    "state/gateway/denyra.db",
    "state/pipeline/denyra.db",
    "state/lidarr/config.xml",
    "state/slskd/slskd.db",
    "state/sftpgo/sftpgo.db",
    "state/navidrome/navidrome.db",
    "incoming/uploading/session.part",
    "processing/work/candidate/track.flac",
    "processing/approved/candidate/track.flac",
    "quarantine/candidate/track.flac",
    "downloads/slskd/complete-id/track.flac",
    "downloads/spotiflac/job-id/track.flac",
}
```

Record content and mode, run the command, then assert every sentinel is byte-identical and still at the same path.

- [ ] **Step 3: Add failing phase tests**

Cover dirty tree, fetch, fast-forward, Compose render, pull, and build failures. For every pre-cutover failure assert:

```go
if strings.Contains(f.log(), " up -d --remove-orphans") {
    t.Fatalf("pre-cutover failure started candidate stack:\n%s", f.log())
}
assertReleaseCommit(t, f, oldCommit)
assertProtectedTrees(t, f, before)
```

Cover candidate `up --wait`, migration/startup, health, and smoke failures after release environment activation. Assert the new commit remains deployed, no prior image/config appears in the command log, protected trees survive, and output contains `phase`, `deployed_commit`, `logs`, and `retry`.

- [ ] **Step 4: Add interruption and convergence tests**

Interrupt once before cutover and once after the release environment changes. Re-run `update` against the same repository HEAD. The first retry must perform cutover; the second must reconcile/start/smoke the selected commit instead of returning `already current` while unhealthy.

- [ ] **Step 5: Add a transactional migration failure test**

Execute a fixture migration that creates a table and then raises an error in the same transaction. Assert neither the table nor its migration version remains. Then replace it with the corrected migration and assert one successful application. This proves fix-forward does not depend on a state snapshot.

- [ ] **Step 6: Run tests and verify current rollback behavior fails the new contract**

Run: `rtk go test ./tests/integration/operations ./migrations -run 'Test(ForwardUpdate|InterruptedUpdate|MigrationFailure)' -count=1`

Expected: FAIL because current update captures/restores snapshots and treats equal Git HEAD as fully deployed without reconciling health.

- [ ] **Step 7: Commit the failing lifecycle contract**

```bash
rtk git add tests/integration/operations/upgrade_test.go tests/integration/operations/management_command_test.go migrations/embed_test.go
rtk git commit -m "test(ops): define forward-only update contract"
```

### Task 2: Remove backup, restore, snapshot, and rollback capabilities

**Files:**
- Delete: `scripts/backup/backup.sh`
- Delete: `scripts/backup/lib.sh`
- Delete: `scripts/backup/verify-repository.sh`
- Delete: `scripts/restore/restore.sh`
- Delete: `scripts/restore/lib.sh`
- Delete: `scripts/restore/verify.sh`
- Delete: `scripts/restore/cutover-check.sh`
- Delete: `scripts/manage/backup.sh`
- Delete: `scripts/manage/rollback.sh`
- Delete: `scripts/manage/snapshot.sh`
- Delete: `cmd/denyra-restore-check/main.go`
- Delete: `internal/platform/restore/restore.go`
- Delete: `internal/platform/sqlite/backup.go`
- Delete: `internal/platform/upgrade/schema.go`
- Delete: `tests/integration/operations/backup_policy_test.go`
- Delete: `tests/integration/operations/restore_test.go`
- Modify: `denyra`
- Modify: `scripts/manage/common.sh`
- Modify: `scripts/manage/setup.sh`
- Modify: `scripts/bootstrap-data-layout.sh`
- Modify: `deploy/compose.yaml`
- Modify: `deploy/docker/pipeline.Dockerfile`
- Modify: `deploy/secrets/README.md`
- Modify: `internal/config/types.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/validate.go`
- Modify: `cmd/acquisition-gateway/main.go`
- Modify: `cmd/media-pipeline/main.go`
- Modify: `internal/gateway/transport/routes.go`
- Modify: `internal/gateway/transport/maintenance.go`
- Modify: `internal/gateway/transport/maintenance_test.go`
- Modify: `internal/pipeline/internalapi/routes.go`
- Modify: `internal/pipeline/internalapi/maintenance.go`
- Modify: `internal/pipeline/internalapi/maintenance_test.go`
- Modify: `internal/platform/sqlite/sqlite_test.go`
- Modify: `tests/acceptance/harness/compose.go`
- Modify: `tests/integration/compose_config_test.go`

**Interfaces:**
- Removes: `./denyra backup`, `./denyra rollback`, Restic profile, `/internal/maintenance/backup`, restore checker, and lifecycle snapshot helpers
- Preserves: authenticated `/internal/maintenance` admission pause and per-candidate mutation recovery

- [ ] **Step 1: Add negative surface tests**

Assert `backup`, `restore`, `rollback`, and `snapshot` return exit 2 with normal usage; help omits them; Compose contains no `restic` service/profile/secret/mount; and internal routers return 404 for `/internal/maintenance/backup` while authenticated maintenance enable/disable still works.

- [ ] **Step 2: Remove management and Compose paths**

Dispatch only `update` through the exclusive lifecycle lock. Remove `denyra_compose_snapshot`, `DENYRA_UPDATES_DIR`, setup creation of `updates`, setup generation of `restic_password`, the `/data/backups` bootstrap directory, Gateway's backup-only mount, Restic service/profile/secret, and all `DENYRA_RESTIC_*` variables.

- [ ] **Step 3: Remove backup-only Go surfaces**

Delete the restore command/package, online SQLite backup implementation, and rollback schema selector. Remove `BackupConfig`, defaults, and validation. Remove `BackupRoot` from Gateway/Pipeline route structs and main wiring. Keep `/internal/maintenance` registered whenever its DB/store and admission controller are configured, but delete only the `/internal/maintenance/backup` handler and request type.

- [ ] **Step 4: Protect transaction-local media recovery**

Add or retain a focused `MigrationMutationService` test proving `.migration-backups/<candidateID>` restores the exact FLAC bytes when tag mutation fails and is deleted after successful mutation. Do not rename or remove this path as part of lifecycle simplification.

- [ ] **Step 5: Remove obsolete tests and repair remaining fixtures**

Delete backup/restore suites, remove backup-specific cases from SQLite tests, and stop acceptance fixtures from creating `/data/backups`. Rewrite maintenance tests around admission pause only.

- [ ] **Step 6: Run focused removal tests**

Run: `rtk go test ./internal/gateway/transport ./internal/pipeline/internalapi ./internal/pipeline/application ./internal/platform/sqlite ./tests/integration/operations ./tests/integration -run 'Test(Management|Maintenance|MigrationMutation|Compose)' -count=1`

Expected: PASS; removed commands have no dispatch path and protected mutation recovery still passes.

- [ ] **Step 7: Commit**

```bash
rtk git add -A denyra scripts cmd/denyra-restore-check internal/platform/restore internal/platform/sqlite internal/platform/upgrade internal/config deploy/compose.yaml cmd internal/gateway/transport internal/pipeline/internalapi tests
rtk git commit -m "refactor(ops): remove backup and rollback lifecycle"
```

### Task 3: Implement the forward-only update state machine

**Files:**
- Modify: `scripts/manage/update.sh`
- Modify: `scripts/manage/common.sh`
- Modify: `deploy/docker/gateway.Dockerfile`
- Modify: `deploy/docker/pipeline.Dockerfile`
- Modify: `deploy/docker/lidarr.Dockerfile`
- Modify: `tests/integration/operations/upgrade_test.go`

**Interfaces:**
- Produces: `denyra_update` with phases `validate`, `fetch`, `render`, `pull`, `build`, `activate`, `recreate`, `smoke`, `cleanup`
- Produces: `denyra_cleanup_images` limited to `io.denyra.project=denyra`

- [ ] **Step 1: Replace snapshot validation with release validation**

Move the 40-character lowercase commit validator into `common.sh` as `denyra_validate_commit`. Read exactly one `DENYRA_GIT_COMMIT` and one `DENYRA_IMAGE_TAG` from `denyra.env`. Treat the env commit as deployed state and Git `HEAD` as selected source state.

- [ ] **Step 2: Add explicit phase-aware failure reporting**

Set phase and affected target before every fallible operation. The exit trap must unlock and, on failure, print only safe values:

```sh
printf 'denyra: update failed\n' >&2
printf 'phase=%s\naffected=%s\ndeployed_commit=%s\n' \
  "$denyra_update_phase" "$denyra_update_affected" "$denyra_update_deployed" >&2
printf 'logs=./denyra logs %s\nretry=./denyra update\n' \
  "$denyra_update_log_service" >&2
```

It must contain no rollback call, state copy, config copy, prior image file, or secret content.

- [ ] **Step 3: Keep all pre-cutover work non-disruptive**

Run in order: path and Docker validation, clean-tree checks, branch resolution, fetch, fast-forward merge, `denyra_compose config --quiet`, `denyra_compose pull --policy always slskd sftpgo navidrome`, and `denyra_compose build --pull`. Do not call `stop`, change `denyra.env`, or recreate a service before all these pass.

- [ ] **Step 4: Activate and converge the selected commit**

Always inspect health even when `HEAD == DENYRA_GIT_COMMIT`. If source and deployed commit match and all services plus smoke are healthy, print `already current`. Otherwise set `DENYRA_IMAGE_TAG`, `DENYRA_GIT_COMMIT`, and release refresh atomically, then run:

```sh
denyra_compose up -d --remove-orphans --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}"
denyra_smoke
```

Do not stop the whole project first. A failed recreation or smoke retains candidate containers and logs. If a service reaches a tested restart-count threshold, stop only that Compose service to prevent a noisy crash loop; do not touch healthy dependencies.

- [ ] **Step 5: Label and clean only Denyra-built images**

Add `LABEL io.denyra.project="denyra"` to the three local Dockerfiles. After a successful smoke, enumerate image IDs with that exact label, subtract image IDs referenced by running containers, and invoke `docker image rm` without `--force` for the remainder. A removal conflict is a warning, not update failure. Never delete external images or volumes.

- [ ] **Step 6: Make interruption idempotent**

The trap only reports and unlocks. On retry, use repository HEAD, deployed env, current Compose container image IDs, and health to choose between pre-cutover build, candidate recreation, or smoke/cleanup. Do not infer success only from Git equality.

- [ ] **Step 7: Run lifecycle tests**

Run: `rtk go test -race ./tests/integration/operations -run 'Test(ForwardUpdate|InterruptedUpdate|ImageCleanup|DirtyTree)' -count=1`

Expected: PASS, including proof that no update command argument targets a protected tree.

- [ ] **Step 8: Commit**

```bash
rtk git add scripts/manage/update.sh scripts/manage/common.sh deploy/docker tests/integration/operations/upgrade_test.go
rtk git commit -m "feat(ops): make updates forward-only"
```

### Task 4: Add confirmed cleanup for legacy local artifacts

**Files:**
- Create: `scripts/manage/cleanup.sh`
- Modify: `denyra`
- Modify: `scripts/manage/common.sh`
- Modify: `tests/integration/operations/management_command_test.go`

**Interfaces:**
- Produces: `./denyra cleanup legacy-lifecycle`
- Deletes only: `${DENYRA_HOME}/updates`, `${DENYRA_DATA_ROOT}/backups`, `${DENYRA_SECRETS_DIR}/restic_password`

- [ ] **Step 1: Add target-resolution and confirmation tests**

Test absent targets, non-empty directories, symlinked targets, non-canonical roots, wrong subcommand, cancellation, exact `DELETE` confirmation, partial deletion failure, and paths containing whitespace. Assert external Restic locations and every protected data path remain untouched.

- [ ] **Step 2: Implement canonical ownership checks**

Require absolute non-root `DENYRA_HOME` and `DENYRA_DATA_ROOT`; reject either root or target when it is a symlink; resolve each existing parent with `pwd -P`; and require each target to equal one of the three exact constructed paths. Do not accept user-supplied deletion paths, globs, or environment-provided target lists.

- [ ] **Step 3: Print the exact deletion set and require a typed token**

Output one shell-quoted target per line, then prompt:

```text
Type DELETE to permanently remove only these legacy Denyra artifacts:
```

Anything other than exact uppercase `DELETE` cancels without deletion. Remove directories with explicit validated arguments and the obsolete secret with `rm -f --`; report each completed target. If one deletion fails, stop and report remaining targets.

- [ ] **Step 4: Keep cleanup separate from update**

`update` may remove unreferenced labeled images automatically, but it must never invoke `cleanup legacy-lifecycle`. Legacy state deletion occurs only after production acceptance and explicit confirmation.

- [ ] **Step 5: Run destructive-boundary tests**

Run: `rtk go test ./tests/integration/operations -run 'TestLegacyLifecycleCleanup' -count=1`

Expected: PASS, including symlink escape and protected-tree cases.

- [ ] **Step 6: Commit**

```bash
rtk git add denyra scripts/manage/cleanup.sh scripts/manage/common.sh tests/integration/operations/management_command_test.go
rtk git commit -m "feat(ops): add scoped legacy cleanup"
```

### Task 5: Align documentation, CI, and repository policy

**Files:**
- Delete: `docs/runbooks/backup.md`
- Delete: `docs/runbooks/restore.md`
- Modify: `README.md`
- Modify: `docs/runbooks/install.md`
- Modify: `docs/runbooks/upgrade.md`
- Modify: `docs/runbooks/incidents.md`
- Modify: `docs/runbooks/security-boundary.md`
- Modify: `docs/runbooks/acceptance-evidence.md`
- Modify: `scripts/check-runbooks.sh`
- Modify: `scripts/check-compose.sh`
- Modify: `scripts/check-clean-tree.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/superpowers/specs/2026-08-24-system-foundation-design.md`
- Modify: `docs/superpowers/specs/2026-08-24-operations-and-clients-design.md`
- Modify: `plans/001-simplified-local-deployment-design.md`

**Interfaces:**
- Produces: one current forward-only operator journey and explicit supersession markers on historical design material

- [ ] **Step 1: Rewrite operator documentation around fix-forward**

Document build-before-cutover, equal-commit reconciliation, phase diagnostics, safe retry, preservation guarantees, and the one-time `cleanup legacy-lifecycle` procedure. Remove links, examples, and claims for backup, restore, snapshot retention, or rollback.

- [ ] **Step 2: Mark historical requirements as superseded**

Add a banner to each older active spec/plan that points to the approved forward-only design. Preserve historical text for auditability, but make it impossible to mistake those lifecycle sections for current instructions.

- [ ] **Step 3: Update policy scripts**

Remove backup/restore runbooks from required files and removed commands from expected operator docs. Add checks that current executable code, Compose, README, and runbooks do not expose `./denyra backup`, `./denyra restore`, `./denyra rollback`, Restic, prior-image manifests, or automatic rollback. Exclude explicitly marked historical specs/plans and checker source from self-matching.

- [ ] **Step 4: Run all repository gates from a clean tree**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk ./scripts/check-clean-tree.sh`

Run: `rtk ./scripts/check-runbooks.sh`

Run: `rtk ./scripts/check-compose.sh`

Run: `rtk make verify`

Run: `rtk go test ./tests/integration/... -count=1`

Expected: PASS, and `rtk git status --short` shows only the intended documentation/policy changes before commit.

- [ ] **Step 5: Commit**

```bash
rtk git add -A README.md docs scripts/check-runbooks.sh scripts/check-compose.sh scripts/check-clean-tree.sh .github/workflows/ci.yml plans/001-simplified-local-deployment-design.md
rtk git commit -m "docs(ops): adopt fix-forward lifecycle"
```

### Task 6: Deploy, verify preservation, then remove production legacy artifacts

**Files:**
- Test: live production at `/home/nirwana/pribadi/denyra`
- Modify: `docs/runbooks/acceptance-evidence.md`

**Interfaces:**
- Consumes: production Compose project `denyra`
- Produces: accepted forward-only deployment with legacy local snapshots/workspaces removed and media/state retained

- [ ] **Step 1: Record a read-only production baseline**

Record the deployed commit, service/container/image identities, health, exact existing legacy cleanup targets, FLAC file count and total bytes in both libraries, unresolved download directory count, and existence/size of every service database. Do not print credential contents. Store acceptance output outside protected media/state trees.

- [ ] **Step 2: Verify the candidate locally before production**

Run: `rtk make verify`

Run: `rtk go test ./tests/integration/... -count=1`

Run: `rtk git status --short`

Expected: all checks pass and the worktree is clean.

- [ ] **Step 3: Deploy through the supported production command**

Run: `rtk ssh production 'cd /home/nirwana/pribadi/denyra && ./denyra update'`

Expected: all services healthy, smoke passes, the deployed commit equals repository HEAD, and no rollback/snapshot path is invoked.

- [ ] **Step 4: Verify end-to-end state and media before deletion**

Compare library counts/bytes, unresolved downloads, database presence, active acquisition/import jobs, Lidarr TrackFiles, and Navidrome visibility against baseline. Exercise one Admin login, the currently supported acquisition detail, and one read-only playback/library query. Stop if any protected item is missing or an active workflow regresses.

- [ ] **Step 5: Preview and confirm exact legacy targets**

Run interactively:

```bash
rtk ssh -t production 'cd /home/nirwana/pribadi/denyra && ./denyra cleanup legacy-lifecycle'
```

Confirm only after output contains `${DENYRA_HOME}/updates`, `${DENYRA_DATA_ROOT}/backups`, and `${DENYRA_SECRETS_DIR}/restic_password` when those paths exist. Type `DELETE`. Do not delete or inspect any external Restic repository.

- [ ] **Step 6: Re-run preservation and health checks**

Assert the three legacy local targets are absent, all protected data counts match the pre-cleanup baseline, services remain healthy, unresolved work remains addressable, and unrelated Docker projects/images/volumes are unchanged.

- [ ] **Step 7: Record acceptance evidence and commit**

Record timestamps, commit IDs, aggregate counts, health results, exact removed paths, and any retained unresolved jobs. Do not commit secrets, raw provider output, or host-specific credentials.

```bash
rtk git add docs/runbooks/acceptance-evidence.md
rtk git commit -m "docs(ops): record forward-only production acceptance"
```
