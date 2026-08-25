# Admin Operations Experience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give operators a discoverable, truthful Admin journey for acquisition failures, recovered downloads, unmanaged review, and service degradation.

**Architecture:** Gateway remains source of truth for acquisition jobs and exposes paginated summaries plus structured detail. Pipeline Admin acts as an authenticated read-through client and renders timelines and tables without copying Gateway state. Shell health comes from the existing local health service. Unmanaged review receives state-specific actions instead of reusing quarantine movement.

**Tech Stack:** Go 1.27, `net/http`, SQLite, templ, HTMX, embedded CSS, Chrome DevTools

**Spec:** `docs/superpowers/specs/2026-08-24-controlled-media-pipeline-design.md`

**Prerequisite:** Complete the acquisition recovery plan through provider-evidence sanitization and the state/security/performance plan before final production acceptance.

## Global Constraints

- Admin never mutates Gateway tables directly.
- Acquisition list and detail responses are paginated or bounded.
- Raw provider stdout, stderr, and secret-bearing values are never rendered.
- Every mutating form keeps authentication, CSRF, explicit confirmation, reason, and `state_revision` checks.
- Degraded dependencies do not hide durable state already available locally.
- Desktop and 390 px mobile layouts must preserve all navigation and actions.

---

### Task 1: Add paginated summaries and bounded structured Gateway detail

**Files:**
- Modify: `internal/contracts/acquisition.go`
- Modify: `internal/gateway/persistence/readmodel.go`
- Modify: `internal/gateway/transport/routes.go`
- Create: `migrations/gateway/000007_admin_readmodel.sql`
- Create: `internal/gateway/persistence/readmodel_test.go`
- Create: `internal/gateway/transport/routes_test.go`

**Interfaces:**
- Produces: `contracts.AcquisitionJobSummary`, `contracts.AcquisitionJobPage`
- Produces: bounded `contracts.AcquisitionJobDetail` without raw execution/correlation payloads
- Produces: `Repositories.JobSummaries(ctx context.Context, limit int, cursor, state string) (contracts.AcquisitionJobPage, error)`
- Produces: `GET /internal/acquisitions?limit=50&cursor=<updated_at,id>&state=<state>`

- [ ] **Step 1: Define bounded list contracts**

```go
type AcquisitionJobSummary struct {
    JobID               string     `json:"job_id"`
    State               string     `json:"state"`
    ReleaseGroupMBID    string     `json:"release_group_mbid"`
    SelectedReleaseMBID string     `json:"selected_release_mbid,omitempty"`
    LidarrAlbumID       int64      `json:"lidarr_album_id"`
    StateRevision       uint64     `json:"state_revision"`
    PrimaryAttempt      int        `json:"primary_attempt"`
    FallbackAttempt     int        `json:"fallback_attempt"`
    NextRetryAt         *time.Time `json:"next_retry_at,omitempty"`
    UpdatedAt           time.Time  `json:"updated_at"`
}

type AcquisitionJobPage struct {
    Items []AcquisitionJobSummary `json:"items"`
    Next  string                  `json:"next,omitempty"`
}
```

Encode cursor as URL-safe base64 JSON containing `updated_at` and `job_id`. Reject malformed cursors and unknown state filters with HTTP 400.

Replace raw `json.RawMessage` fields in the route response with explicit attempt and correlation summaries. Attempt summaries expose provider/kind, outcome, error class, timestamps, and a redacted message capped at 2 KiB. Correlation summaries expose identities, timestamps, and evidence SHA-256 only. Cap transitions, attempts, candidates, and correlations at 100 each and return `truncated_sections` when a section has more rows. Historical raw evidence remains in SQLite for forensic use but never crosses this Admin API.

- [ ] **Step 2: Write failing persistence tests**

Seed 55 jobs with duplicate timestamps. Request 50, follow `Next`, and assert every job appears once. Add state-filter and `limit > 100` normalization cases.

- [ ] **Step 3: Run tests and confirm missing API**

Run: `rtk go test ./internal/gateway/persistence ./tests/integration/gateway -run TestAcquisitionJobPage -count=1`

Expected: FAIL because the contracts and query do not exist.

- [ ] **Step 4: Implement indexed list and bounded-detail queries**

Query `acquisition_jobs` ordered by `updated_at DESC,id DESC`, use tuple cursor semantics, cap at 100, and return `limit+1` rows to calculate `Next`. Add `000007_admin_readmodel.sql` with indexes on `(updated_at DESC,id DESC)` and `(state,updated_at DESC,id DESC)`.

For detail, fetch `LIMIT 101` per section, map the first 100 rows, and set the truncation marker from row 101. Parse stored attempt JSON into a small allowlisted struct; redact/cap its message again at the read boundary so legacy pre-fix rows cannot leak.

- [ ] **Step 5: Expose the authenticated internal route**

Use the existing bearer middleware and JSON response helpers. Register both exact `GET /internal/acquisitions` and the existing detail subtree because the current trailing-slash handler does not match the list URL. Keep `/internal/acquisitions/{jobID}` unchanged.

- [ ] **Step 6: Run tests and commit**

Run: `rtk go test ./internal/gateway/... ./tests/integration/gateway/...`

```bash
rtk git add internal/contracts/acquisition.go internal/gateway/persistence/readmodel.go internal/gateway/persistence/readmodel_test.go internal/gateway/transport/routes.go internal/gateway/transport/routes_test.go migrations/gateway/000007_admin_readmodel.sql
rtk git commit -m "feat(admin): list acquisition jobs"
```

### Task 2: Build the Admin acquisition index and structured detail

**Files:**
- Modify: `internal/pipeline/application/admin.go`
- Modify: `internal/pipeline/adapters/gateway/client.go`
- Modify: `internal/pipeline/adapters/gateway/client_test.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/views/models.go`
- Create: `internal/pipeline/adminui/views/acquisitions.templ`
- Modify: `internal/pipeline/adminui/views/acquisition.templ`
- Modify: `internal/pipeline/adminui/views/layout.templ`
- Modify: `internal/pipeline/adminui/assets/css/app.css`
- Test: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Produces: `AcquisitionReader.ListAcquisitions(context.Context, int, string, string) (AcquisitionPage, error)`
- Produces: `GET /acquisitions`
- Consumes: Gateway summary and detail contracts from Task 1

- [ ] **Step 1: Replace opaque evidence with structured application models**

Define summary, transition, attempt, candidate, correlation, and truncation view models in `application/admin.go`. `AcquisitionEvidence` must hold those fields directly and omit raw `json.RawMessage Evidence`.

- [ ] **Step 2: Add client contract tests**

Verify list query escaping, bearer authentication, cursor propagation, response caps, and decoding. For detail, ensure provider details expose only provider, outcome, error class, timestamps, and a redacted summary. Raw stdout/stderr and command arrays must not cross into the Admin model.

- [ ] **Step 3: Add failing Admin route tests**

Assert `/acquisitions` appears in navigation, renders state filters and pagination, links each job ID to detail, and detail renders these sections:

```text
State timeline
Provider attempts
Candidates
Correlation evidence
```

Assert no `<code>{"job":` blob and no provider stderr are present.

- [ ] **Step 4: Implement list route and templates**

Register `GET /acquisitions`. Render state, album ID, attempts, next retry, and updated time. Use existing status classes extended for primary/fallback running, retryable, no-candidate, active, arbitrating, handed-off, and cancelled states.

- [ ] **Step 5: Render structured detail**

Use semantic tables and ordered timeline markup. Collapse long redacted error summaries behind `<details>`. Keep each summary at 2 KiB and each page below the configured Gateway response limit.

- [ ] **Step 6: Generate templ output and run tests**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test ./internal/pipeline/... ./tests/integration/pipeline/...`

Expected: PASS and generated files clean after a second templ generation.

- [ ] **Step 7: Commit**

```bash
rtk git add internal/pipeline/application/admin.go internal/pipeline/adapters/gateway internal/pipeline/adminui tests/integration/pipeline/adminui_test.go
rtk git commit -m "feat(admin): add acquisition operations view"
```

### Task 3: Make readiness and degradation truthful

**Files:**
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/views/models.go`
- Modify: `internal/pipeline/adminui/views/layout.templ`
- Modify: `cmd/media-pipeline/main.go`
- Test: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Produces: `Dependencies.Health func() contracts.Health`
- Consumes: `health.Service.Snapshot() contracts.Health`

- [ ] **Step 1: Write shell health tests**

Inject healthy, locally unready, and remote-degraded snapshots. Assert badge text/class and a dependency banner listing only degraded dependencies. A nil health provider must render `health unknown`, never `local ready`.

- [ ] **Step 2: Run test and verify hard-coded readiness fails**

Run: `rtk go test ./tests/integration/pipeline -run TestAdminShellHealth -count=1`

Expected: FAIL.

- [ ] **Step 3: Wire health snapshot into Admin dependencies**

Set `Dependencies.Health = healthService.Snapshot` in `cmd/media-pipeline/main.go`. Map:

```go
ready && no degraded dependency => ("ready", "ok")
ready && degraded remote dependency => ("degraded", "review")
!ready => ("not ready", "blocked")
nil provider => ("health unknown", "blocked")
```

- [ ] **Step 4: Render route-level degraded banners**

Acquisition pages name Gateway degradation. Incoming and review pages name only dependencies used by those journeys. Do not suppress cached/local state.

- [ ] **Step 5: Generate templates, test, and commit**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test ./tests/integration/pipeline -run TestAdminShellHealth -count=1`

```bash
rtk git add cmd/media-pipeline/main.go internal/pipeline/adminui tests/integration/pipeline/adminui_test.go
rtk git commit -m "fix(admin): report real readiness"
```

### Task 4: Surface and operate `UNMANAGED_REVIEW`

**Files:**
- Modify: `internal/pipeline/persistence/admin.go`
- Modify: `internal/pipeline/application/review.go`
- Modify: `internal/pipeline/domain/transition.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/views/review_detail.templ`
- Create: `internal/pipeline/application/review_test.go`
- Test: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Produces: `ReviewDecisionService.RetryUnmanaged(ctx context.Context, candidateID string, expected uint64, actor, reason string) error`
- Consumes: candidate state `UNMANAGED_REVIEW`

- [ ] **Step 1: Add failing queue and action tests**

Persist a candidate in `UNMANAGED_REVIEW`. Assert it appears in `/reviews`, detail shows the latest transition reason, no Managed release-MBID approval form appears, and actions are `Retry unmanaged import`, `Reject`, and `Cancel`.

- [ ] **Step 2: Add a state-specific retry test**

`RetryUnmanaged` must require a non-empty reason, require current state `UNMANAGED_REVIEW`, and transition to `UNMANAGED_READY` without moving files between quarantine and work directories.

- [ ] **Step 3: Run tests and verify failure**

Run: `rtk go test ./internal/pipeline/application ./tests/integration/pipeline -run UnmanagedReview -count=1`

Expected: FAIL.

- [ ] **Step 4: Widen the review query and implement the action**

Include `UNMANAGED_REVIEW` in `Reviews`. Add `retry-unmanaged` to the handler switch and call `RetryUnmanaged`; keep revision and CSRF enforcement.

- [ ] **Step 5: Generate templates, test, and commit**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test -race ./internal/pipeline/... ./tests/integration/pipeline/...`

```bash
rtk git add internal/pipeline/persistence/admin.go internal/pipeline/application/review.go internal/pipeline/domain/transition.go internal/pipeline/adminui tests/integration/pipeline/adminui_test.go
rtk git commit -m "fix(admin): expose unmanaged review work"
```

### Task 5: Correct navigation semantics and account feedback

**Files:**
- Modify: `internal/pipeline/adminui/views/layout.templ`
- Modify: `internal/pipeline/adminui/views/account.templ`
- Modify: `internal/pipeline/adminui/handlers/account.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Test: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Produces: valid `aria-current="page"` only on the active link
- Produces: `GET /account/password?changed=1` success notice

- [ ] **Step 1: Add rendered-HTML tests**

Assert exactly one navigation link has `aria-current="page"`, inactive links omit the attribute, no `{page true}` or `{page false}` text exists, and password success redirects to `/account/password?changed=1`.

- [ ] **Step 2: Run test and confirm current output fails**

Run: `rtk go test ./tests/integration/pipeline -run TestAdminNavigationAndAccountFeedback -count=1`

Expected: FAIL.

- [ ] **Step 3: Use conditional templ attributes and correct redirect**

Use templ conditional attribute blocks instead of passing `templ.KV` as an attribute value. Render a polite success notice when `changed=1`. Replace internal `err.Error()` responses with stable user-safe messages.

- [ ] **Step 4: Generate templates and run UI tests**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk go test ./tests/integration/pipeline -run 'TestAdmin(Navigation|Account)' -count=1`

Expected: PASS.

- [ ] **Step 5: Validate with Chrome DevTools**

Check desktop and 390 px mobile layouts, keyboard focus, acquisition navigation, active-link announcement, account success notice, console errors, network failures, Lighthouse accessibility, and a performance trace.

- [ ] **Step 6: Commit**

```bash
rtk git add internal/pipeline/adminui internal/pipeline/adminui/handlers/account.go tests/integration/pipeline/adminui_test.go
rtk git commit -m "fix(admin): correct navigation and account feedback"
```

### Task 6: Run integrated production acceptance

**Files:**
- Modify: `docs/runbooks/acceptance-evidence.md`
- Test: live production at `/home/nirwana/pribadi/denyra`

**Interfaces:**
- Consumes: all approved lifecycle, acquisition, state/security/performance, and Admin changes
- Produces: one evidence-backed production acceptance record

- [ ] **Step 1: Run complete local verification from a clean tree**

Run: `rtk go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`

Run: `rtk make verify`

Run: `rtk go test ./tests/integration/... -count=1`

Run: `rtk git status --short`

Expected: every command passes and the worktree is clean.

- [ ] **Step 2: Record a read-only production baseline**

Record deployed commit, service health, acquisition-state counts, Pipeline review counts, Lidarr TrackFiles for the affected albums, Managed/Unmanaged FLAC counts, Navidrome song counts, active upload sessions, migration-batch counts, and recent authentication audit counts. Do not print credentials, session tokens, raw provider output, or media filenames beyond the approved test albums.

- [ ] **Step 3: Deploy the integrated release**

Run: `rtk ssh production 'cd /home/nirwana/pribadi/denyra && ./denyra update'`

Expected: forward-only update reports the deployed commit, all services become healthy, and smoke checks pass.

- [ ] **Step 4: Validate the signed-in operator journey with Chrome DevTools**

Using the existing authenticated browser session, verify desktop and 390 px mobile journeys: acquisition list/filter/pagination, structured acquisition detail, truthful health/degradation banner, review queue including `UNMANAGED_REVIEW`, unmanaged page selection bounds, migration status polling, password-change success notice without changing the password again, keyboard navigation, accessible names/focus, console errors, failed network requests, Lighthouse accessibility/best-practice results, and a performance trace.

- [ ] **Step 5: Validate service outcomes outside the browser**

Verify `Gajah`, `Manusia`, and `Monokrom` by Tulus are no longer Wanted/Missing after Lidarr TrackFiles exist and are visible in Navidrome. Verify Tenxi either progresses through a real provider or records a legitimate bounded no-result/retry outcome without bridge errors. Confirm import recovery no longer reports `IMPORTED` intents as unresolved, Admin list requests remain bounded, and migration polling uses the status endpoint.

- [ ] **Step 6: Verify security and preservation**

Perform one successful Admin login and confirm no secrets appear in application logs or provider evidence. Do not intentionally throttle the real operator account; rely on the race/integration suite for 429 behavior. Compare protected media/state aggregates with baseline and confirm unrelated Docker projects and volumes are unchanged.

- [ ] **Step 7: Record evidence and commit**

Record timestamps, commit, aggregate before/after values, browser viewport/results, endpoint timings, service health, album outcomes, unresolved exceptions, and exact verification commands. Do not record secrets or raw user/media data.

```bash
rtk git add docs/runbooks/acceptance-evidence.md
rtk git commit -m "docs: record integrated production acceptance"
```
