# State, Security, and Performance Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make recovery state trustworthy, throttle Admin authentication attacks, bound Admin data access, and align CI and documentation with active-development policy.

**Architecture:** Canonical state vocabularies live in application/domain constants and persistence queries consume those constants through explicit terminal sets. Login throttling is an in-memory single-instance guard keyed by hashed client address and normalized username, with bounded storage and aggregate audit events. Admin tables use indexed cursor pagination and set-based summary queries. CI enforces current policy without self-matching obsolete rules.

**Tech Stack:** Go 1.27, SQLite, `net/http`, templ, HTMX, shell verification scripts, GitHub Actions

**Spec:** `docs/superpowers/specs/2026-08-24-operations-and-clients-design.md`

**Prerequisite:** Complete the forward-only lifecycle plan before changing lifecycle-related CI and policy in Task 6.

## Global Constraints

- Do not change Managed-library ownership or controlled Manual Import behavior.
- Authentication responses remain generic and constant-shape.
- Throttle keys and audit events never store raw passwords, usernames, tokens, or full client addresses.
- List queries cap page size at 100 and use deterministic cursor ordering.
- Bulk operations accept at most 100 explicit IDs and retain `state_revision` checks.
- Current active-development floating dependency policy comes from `plans/001-simplified-local-deployment-design.md`.

---

### Task 1: Align import intent terminal states with recovery

**Files:**
- Modify: `internal/pipeline/application/import.go`
- Modify: `internal/pipeline/application/import_reconcile.go`
- Modify: `internal/pipeline/persistence/recovery.go`
- Modify: `tests/integration/pipeline/import_test.go`
- Modify: `tests/integration/pipeline/recovery_test.go`

**Interfaces:**
- Produces: canonical constants `ImportPending`, `ImportReconciling`, `ImportSubmitted`, `ImportImported`, `ImportFailed`
- Consumes: `Repositories.UnresolvedEffects(context.Context)`

- [ ] **Step 1: Write recovery tests for every import status**

Use a table test. `PENDING`, `IMPORT_RECONCILING`, and `IMPORT_SUBMITTED` must produce `IMPORT_PENDING`. `IMPORTED` and `FAILED` must not.

```go
tests := map[string]bool{
    application.ImportPending: true,
    application.ImportReconciling: true,
    application.ImportSubmitted: true,
    application.ImportImported: false,
    application.ImportFailed: false,
}
```

- [ ] **Step 2: Run the test and verify `IMPORTED` is falsely unresolved**

Run: `rtk go test ./tests/integration/pipeline -run TestRecoveryClassifiesImportIntentStatuses -count=1`

Expected: FAIL.

- [ ] **Step 3: Define and use one status vocabulary**

Add constants beside `ImportIntent`. Replace string literals in import services and tests. Change the recovery query terminal set to `('IMPORTED','FAILED')`.

- [ ] **Step 4: Run Pipeline tests and commit**

Run: `rtk go test -race ./internal/pipeline/... ./tests/integration/pipeline/...`

```bash
rtk git add internal/pipeline/application internal/pipeline/persistence/recovery.go tests/integration/pipeline
rtk git commit -m "fix(recovery): align import terminal states"
```

### Task 2: Add bounded Admin login throttling

**Files:**
- Create: `internal/pipeline/adminui/handlers/login_throttle.go`
- Create: `internal/pipeline/adminui/handlers/login_throttle_test.go`
- Modify: `internal/pipeline/adminui/handlers/login.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/persistence/auth.go`
- Modify: `internal/pipeline/application/sessions.go`
- Modify: `cmd/media-pipeline/main.go`
- Modify: `internal/config/types.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/validate.go`
- Modify: `deploy/config/pipeline.toml`

**Interfaces:**
- Produces: `LoginThrottle.Allow(key [32]byte, now time.Time) (time.Duration, bool)`
- Produces: `LoginThrottle.Failure(key [32]byte, now time.Time) (blocked bool)`
- Produces: `LoginThrottle.Success(key [32]byte)`
- Produces: `SessionRepository.AppendLoginThrottleAudit(context.Context, string, time.Time) error`

- [ ] **Step 1: Write deterministic throttle tests**

Configure 5 failures per 15 minutes, base delay 1 second, maximum delay 60 seconds, and maximum 4096 tracked keys. Test independent keys, expiry, success reset, exponential delay, capacity eviction, and concurrent access under `-race`.

- [ ] **Step 2: Write handler tests**

After five failed attempts, assert HTTP 429, `Retry-After`, generic `Authentication failed`, no password echo, and one aggregate `LOGIN_THROTTLED` audit event for the window. A successful login clears the key.

- [ ] **Step 3: Run tests and verify missing throttle**

Run: `rtk go test -race ./internal/pipeline/adminui/handlers -run 'TestLoginThrottle|TestLoginIsThrottled' -count=1`

Expected: FAIL.

- [ ] **Step 4: Implement the bounded throttle**

Hash `normalized username + NUL + net.SplitHostPort(request.RemoteAddr).host` with SHA-256 before calling the throttle. Store only hash, failure count, first failure, blocked-until, and last-seen. Evict expired entries first, then oldest entry when capacity is reached.

- [ ] **Step 5: Record one aggregate audit event**

Persist actor `anonymous:<first 12 hex chars of key>`, action `LOGIN_THROTTLED`, reason `authentication attempt limit reached`, and empty JSON details. Never record attempted username or address.

- [ ] **Step 6: Add typed configuration and validation**

Use:

```toml
[sessions.login_throttle]
failures = 5
window = "15m"
base_delay = "1s"
max_delay = "60s"
capacity = 4096
```

Reject non-positive values, `max_delay < base_delay`, and capacity below 128.

- [ ] **Step 7: Run auth and integration tests**

Run: `rtk go test -race ./internal/pipeline/... ./tests/integration/pipeline/...`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
rtk git add internal/pipeline/adminui/handlers internal/pipeline/application/sessions.go internal/pipeline/persistence/auth.go internal/config deploy/config/pipeline.toml cmd/media-pipeline/main.go
rtk git commit -m "feat(security): throttle admin login"
```

### Task 3: Add indexed cursor pagination to unmanaged releases

**Files:**
- Create: `migrations/pipeline/000012_unmanaged_search.sql`
- Modify: `internal/pipeline/application/admin.go`
- Modify: `internal/pipeline/persistence/unmanaged.go`
- Modify: `internal/pipeline/persistence/migrations.go`
- Modify: `internal/pipeline/persistence/admin.go`
- Modify: `internal/pipeline/adminui/handlers/migrations.go`
- Modify: `internal/pipeline/adminui/views/unmanaged.templ`
- Test: `tests/integration/pipeline/migration_test.go`
- Test: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Produces: `UnmanagedPage{Items []UnmanagedSummary, Next string}`
- Produces: `Repositories.UnmanagedSummaries(ctx, filter, limit, cursor) (items []application.UnmanagedSummary, next string, err error)`

- [ ] **Step 1: Write migration and pagination tests**

Seed 120 releases, including identical `updated_at` values. Assert page sizes of 50, no duplicates across cursors, search by artist/album/path, and status filtering. Use `EXPLAIN QUERY PLAN` to assert the status/update index is selected for an unsearched page.

- [ ] **Step 2: Add searchable columns and indexes**

Migration adds `album_artist`, `album_title`, `release_year`, `album_artist_normalized`, `album_title_normalized`, and `path_basename_normalized`, backfills them from `approved_plan_json` and `final_path`, then creates:

```sql
CREATE INDEX unmanaged_status_updated ON unmanaged_releases(status,updated_at DESC,candidate_id DESC);
CREATE INDEX unmanaged_updated ON unmanaged_releases(updated_at DESC,candidate_id DESC);
CREATE INDEX unmanaged_artist_search ON unmanaged_releases(album_artist_normalized,candidate_id);
CREATE INDEX unmanaged_album_search ON unmanaged_releases(album_title_normalized,candidate_id);
CREATE INDEX unmanaged_path_search ON unmanaged_releases(path_basename_normalized,candidate_id);
```

Update every unmanaged-release write path to populate normalized columns in the same transaction as `approved_plan_json`.

- [ ] **Step 3: Replace ID hydration and JSON scans with one query**

Select all summary fields in one statement. Use `(updated_at < ? OR (updated_at = ? AND candidate_id < ?))` cursor semantics and cap limit at 100. Implement indexable prefix search across normalized artist, album, candidate ID, and normalized path basename; require at least two normalized characters. Label the UI field `Starts with` so search behavior is truthful. Do not retain leading-wildcard JSON scans.

- [ ] **Step 4: Bound bulk selection**

Remove `Select all filtered`. Add page-level selection and reject more than 100 submitted IDs server-side. Keep each ID and its `state_revision` in the confirmation form.

- [ ] **Step 5: Generate templates, run tests, and commit**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test ./tests/integration/pipeline -run 'Test(Unmanaged|Migration)' -count=1`

```bash
rtk git add migrations/pipeline internal/pipeline/application/admin.go internal/pipeline/persistence internal/pipeline/adminui tests/integration/pipeline
rtk git commit -m "perf(admin): paginate unmanaged releases"
```

### Task 4: Replace upload-session N+1 hydration

**Files:**
- Modify: `internal/pipeline/application/admin.go`
- Modify: `internal/pipeline/persistence/uploads.go`
- Modify: `internal/pipeline/adminui/views/models.go`
- Modify: `internal/pipeline/adminui/views/incoming.templ`
- Create: `internal/pipeline/persistence/uploads_test.go`
- Test: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Produces: `UploadSessionSummary{ID, SubmissionID, Status string; Revision uint64; FileCount, CompleteCount int; UpdatedAt time.Time}`
- Produces: `UploadSessionSummaries(context.Context, string, int) ([]application.UploadSessionSummary, error)`

- [ ] **Step 1: Add a set-based regression test and benchmark**

Load 50 sessions with 100 entries. Assert the new summary method returns aggregate counts without file manifests, add `EXPLAIN QUERY PLAN` coverage for the session/entry join, and add `BenchmarkUploadSessionSummaries50` as a durable performance signal. The production method must contain one `QueryContext` and must not call `UploadSession` inside a row loop.

- [ ] **Step 2: Implement the set-based summary query**

Join `upload_sessions` to `upload_entries`, group by session, and calculate counts with `COUNT(e.id)` and `SUM(CASE WHEN e.status='COMPLETE' THEN 1 ELSE 0 END)`. Cap the active-session page at 100.

- [ ] **Step 3: Render summaries without file manifests**

Incoming page uses summary fields. Upload resume/detail endpoints continue using `UploadSession` and full entries.

- [ ] **Step 4: Generate templates, test, and commit**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test ./internal/pipeline/persistence ./tests/integration/pipeline -run UploadSession -count=1`

```bash
rtk git add internal/pipeline/application/admin.go internal/pipeline/persistence/uploads.go internal/pipeline/adminui tests/integration/pipeline/adminui_test.go
rtk git commit -m "perf(admin): load upload summaries in one query"
```

### Task 5: Replace full migration-table polling with lightweight status polling

**Files:**
- Create: `migrations/pipeline/000013_migration_batch_revision.sql`
- Modify: `internal/pipeline/application/migration.go`
- Modify: `internal/pipeline/persistence/admin.go`
- Modify: `internal/pipeline/persistence/migrations.go`
- Modify: `internal/pipeline/adminui/handlers/migrations.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/views/migration_detail.templ`
- Test: `internal/pipeline/adminui/handlers/migrations_test.go`

**Interfaces:**
- Produces: `MigrationBatchStatus{State string; Active, Completed, Failed int; Revision uint64}`
- Produces: `GET /migration-batches/{batchID}/status?revision=<n>`

- [ ] **Step 1: Write status-fragment tests**

When revision is unchanged, return HTTP 204. When changed, return a small status fragment with counts and `HX-Trigger: migration-batch-changed` only when terminal or row detail should refresh.

- [ ] **Step 2: Add batch revision and implement the aggregate query**

Add `state_revision INTEGER NOT NULL DEFAULT 0` to `migration_batches`. Increment it in the same transaction as every migration-item state/evidence change and final batch-state update. Use one grouped query for counts and revision. Do not load item errors for status polling.

- [ ] **Step 3: Change HTMX polling**

Poll status every 5 seconds only while active. Refresh the full detail fragment on `migration-batch-changed`. Stop polling on terminal batch states.

- [ ] **Step 4: Run tests and commit**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test ./internal/pipeline/adminui/handlers ./tests/integration/pipeline -run MigrationBatch -count=1`

```bash
rtk git add migrations/pipeline/000013_migration_batch_revision.sql internal/pipeline/application/migration.go internal/pipeline/persistence/admin.go internal/pipeline/persistence/migrations.go internal/pipeline/adminui
rtk git commit -m "perf(admin): lighten migration polling"
```

### Task 6: Repair CI hygiene and policy documentation

**Files:**
- Modify: `scripts/check-clean-tree.sh`
- Modify: `scripts/check-runbooks.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/superpowers/specs/2026-08-24-system-foundation-design.md`
- Modify: `docs/superpowers/specs/2026-08-24-operations-and-clients-design.md`
- Modify: `plans/001-simplified-local-deployment-design.md`
- Test: `tests/integration/operations/management_command_test.go`

**Interfaces:**
- Produces: clean-tree gate consistent with active-development dependency policy

- [ ] **Step 1: Add a fixture test for the hygiene checker**

Assert a clean checkout passes, the retired product name in executable/docs outside superseded specs fails, unfinished markers fail, and floating selectors are accepted only in the explicit allowlist of active-development files.

- [ ] **Step 2: Fix self-matching and floating-selector policy**

Exclude all checker implementations from their own search. Replace blanket floating-selector rejection with an explicit allowlist for current Compose images, templ generation, and approved Dockerfile source selectors. Unknown floating selectors still fail.

- [ ] **Step 3: Mark superseded requirements at source**

Add a banner to both older specs naming the exact sections superseded by `plans/001-simplified-local-deployment-design.md` and the forward-only lifecycle spec. Do not rewrite historical decisions as current behavior.

- [ ] **Step 4: Run all repository gates**

Run: `rtk ./scripts/check-clean-tree.sh`

Run: `rtk ./scripts/check-runbooks.sh`

Run: `rtk make verify`

Expected: all pass from a clean worktree.

- [ ] **Step 5: Commit**

```bash
rtk git add scripts .github/workflows/ci.yml docs/superpowers/specs plans/001-simplified-local-deployment-design.md tests/integration/operations
rtk git commit -m "fix(ci): align hygiene checks with development policy"
```
