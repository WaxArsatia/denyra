# Acquisition Recovery and Fallback Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover completed slskd downloads into Lidarr, enforce bounded acquisition cycles, and ship a SpotiFLAC runtime whose CLI and extension bridge are verified before deployment.

**Architecture:** Keep Lidarr as the sole Managed-library writer. Extend late correlation through the persisted acquisition deadline, create pending candidates from correlated history, and let durable slskd events complete them through the existing handoff. Expired fallback cycles return to primary search with cleared cycle-local state and bounded backoff. Build SpotiFLAC from a release tag with the bridge embedded, then validate the frozen archive and supported CLI at image build time.

**Tech Stack:** Go 1.27, SQLite, Docker BuildKit, Python 3.12, PyInstaller, Lidarr API, slskd events, SpotiFLAC v3.0.8 source at commit `0306da57dd855175549af119d95539e51ffbd801`

**Spec:** `docs/superpowers/specs/2026-08-24-controlled-media-pipeline-design.md`

**Prerequisite:** Complete Tasks 1-5 of `2026-08-25-forward-only-development-lifecycle.md` before this plan's production deployment task.

## Global Constraints

- Lidarr alone mutates `/data/library`; Navidrome remains read-only.
- Only evidence matched to the original album ID, release-group MBID, selected release MBID, and persisted watermarks may create a primary candidate.
- Timeouts and provider errors are retryable outcomes, never zero-result evidence.
- A fallback cycle lasts at most the persisted `overall_deadline`; a later cycle starts with fresh search watermarks and deadlines.
- Completed download directories remain untouched until Lidarr Manual Import is verified.
- Provider stdout, stderr, and command provenance must be capped and redacted before persistence.

---

### Task 1: Characterize post-grace primary correlation

**Files:**
- Modify: `tests/integration/gateway/correlation_test.go`
- Modify: `tests/integration/gateway/late_primary_test.go`
- Modify: `internal/gateway/application/reconcile_primary.go`

**Interfaces:**
- Consumes: `PrimaryReconciler.LateEvidence(context.Context, string) ([]persistence.CorrelationEvidence, error)`
- Produces: `lateCorrelationRequest(domain.Job, persistence.PrimarySearchContext) (domain.CorrelationRequest, error)`

- [ ] **Step 1: Write a failing integration test for evidence after grace but before the overall deadline**

Create a real `PrimaryReconciler` fixture. Persist `grace_deadline = now+1m` and `overall_deadline = now+6h`, return a Lidarr history grab observed at `now+2m`, and call `LatePrimaryMonitor.Reconcile`. Assert one pending `slskd` candidate and one correlation row are stored.

```go
changed, err := monitor.Reconcile(context.Background())
if err != nil || changed != 1 {
    t.Fatalf("changed=%d err=%v", changed, err)
}
var pending int
if err := db.QueryRow(`SELECT COUNT(*) FROM pending_acquisition_candidates WHERE job_id=? AND source='slskd'`, job.ID).Scan(&pending); err != nil {
    t.Fatal(err)
}
if pending != 1 {
    t.Fatalf("pending=%d", pending)
}
```

- [ ] **Step 2: Write boundary tests that reject unrelated and too-late evidence**

Use table cases for a wrong album ID, wrong release-group MBID, history watermark at or below the original watermark, and an observation after `overall_deadline`. Each case must produce zero pending candidates.

- [ ] **Step 3: Run tests and confirm current behavior fails**

Run: `rtk go test ./tests/integration/gateway -run 'TestLatePrimaryMonitor(PostGrace|Rejects)' -count=1`

Expected: post-grace case fails because `correlationRequest` still uses `search.GraceDeadline`.

- [ ] **Step 4: Split initial and late correlation requests**

Keep `correlationRequest` unchanged for deciding when fallback may begin. Add:

```go
func lateCorrelationRequest(job domain.Job, search persistence.PrimarySearchContext) (domain.CorrelationRequest, error) {
    request, err := correlationRequest(job, search)
    if err != nil {
        return domain.CorrelationRequest{}, err
    }
    if job.OverallDeadline != nil && job.OverallDeadline.After(request.Deadline) {
        request.Deadline = job.OverallDeadline.UTC()
    }
    return request, request.Validate()
}
```

Change `LateEvidence` to use `lateCorrelationRequest`. Do not widen the initial reconciliation path.

- [ ] **Step 5: Run focused and full gateway tests**

Run: `rtk go test ./internal/gateway/... ./tests/integration/gateway/... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
rtk git add internal/gateway/application/reconcile_primary.go tests/integration/gateway/correlation_test.go tests/integration/gateway/late_primary_test.go
rtk git commit -m "fix(acquisition): correlate late primary grabs"
```

### Task 2: Complete recovered primary candidates from durable slskd events

**Files:**
- Modify: `internal/gateway/application/runtime.go`
- Modify: `internal/gateway/application/primary_completion.go`
- Modify: `tests/integration/gateway/primary_completion_test.go`
- Modify: `tests/integration/gateway/late_primary_test.go`

**Interfaces:**
- Consumes: pending candidate produced by `LatePrimaryMonitor.Reconcile`
- Produces: existing `PrimaryCompletionService.Reconcile(context.Context) (int, error)` handoff to Pipeline

- [ ] **Step 1: Add an end-to-end regression test for a missed grab followed by durable completion events**

Seed a fallback-state job, a post-grace Lidarr history record with `downloadId`, and completed slskd events under `/data/downloads/slskd/lidarr/<downloadId>`. Run late correlation, then primary completion. Assert:

```go
if changed != 1 || completed != 1 {
    t.Fatalf("late=%d completed=%d", changed, completed)
}
var candidates, accepted int
_ = db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE job_id=? AND source='slskd'`, job.ID).Scan(&candidates)
_ = db.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE job_id=? AND effect_type='PIPELINE_ACCEPT' AND status='ACKNOWLEDGED'`, job.ID).Scan(&accepted)
if candidates != 1 || accepted != 1 {
    t.Fatalf("candidates=%d accepted=%d", candidates, accepted)
}
```

- [ ] **Step 2: Run the test and verify ordering fails**

Run: `rtk go test ./tests/integration/gateway -run TestLatePrimaryDurableCompletionReachesPipeline -count=1`

Expected: FAIL until completion is triggered after late-candidate registration.

- [ ] **Step 3: Preserve the grab timestamp on late pending candidates**

Set `PendingCandidate.CreatedAt` to the earliest validated correlation-evidence `ObservedAt`, not the later reconciliation clock. Validate that every evidence timestamp is non-zero and not after the current reconciliation time. This makes `SlskdCompletionEventsSince` include completion events received after the original grab but before delayed correlation.

- [ ] **Step 4: Trigger completion after late reconciliation**

Add a non-blocking `PrimaryCompletion.Notify()` after `LatePrimary.Reconcile` in both event and safety-ticker branches of `GatewayRuntime.Run`. Keep the completion monitor's safety ticker as recovery if notification is lost.

```go
_, _ = runtime.LatePrimary.Reconcile(ctx)
if runtime.PrimaryCompletion != nil {
    runtime.PrimaryCompletion.Notify()
}
runtime.Worker.Notify("")
```

- [ ] **Step 5: Prove chronology and idempotency**

Persist a completion event between the original grab timestamp and late reconciliation timestamp. Run late and completion reconciliation twice. Assert one pending candidate identity, one completed candidate, one `PIPELINE_ACCEPT` effect, and unchanged source files.

- [ ] **Step 6: Run gateway integration tests**

Run: `rtk go test ./tests/integration/gateway/... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
rtk git add internal/gateway/application/runtime.go internal/gateway/application/primary_completion.go tests/integration/gateway/primary_completion_test.go tests/integration/gateway/late_primary_test.go
rtk git commit -m "fix(acquisition): recover durable primary completions"
```

### Task 3: End expired fallback cycles and restart primary search

**Files:**
- Modify: `internal/gateway/domain/state.go`
- Modify: `internal/gateway/persistence/jobs.go`
- Modify: `internal/gateway/application/fallback.go`
- Modify: `internal/gateway/application/worker.go`
- Modify: `tests/integration/gateway/fallback_test.go`
- Modify: `tests/integration/gateway/recovery_test.go`

**Interfaces:**
- Produces: `Repositories.RestartAcquisitionCycle(ctx context.Context, jobID string, expected uint64, nextRetryAt time.Time, at time.Time) (domain.Transition, error)`
- Consumes: `domain.RetryPolicy.PrimaryDeadline(time.Time, int) (time.Time, error)`

- [ ] **Step 1: Write tests for retry deadlines at and beyond `overall_deadline`**

Add cases where `FallbackDeadline` would land after the overall deadline and where the worker wakes after the deadline. Both must transition to `PRIMARY_RETRYABLE_ERROR`, clear cycle-local columns, reset `fallback_attempt`, and schedule primary retry.

```go
stored, err := repositories.Job(ctx, job.ID)
if err != nil {
    t.Fatal(err)
}
if stored.State != domain.StatePrimaryRetryableError || stored.OverallDeadline != nil || stored.FallbackAttempt != 0 {
    t.Fatalf("job=%+v", stored)
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run: `rtk go test ./tests/integration/gateway -run 'TestFallback(Deadline|RetryAfterOverallDeadline)' -count=1`

Expected: FAIL because current code schedules another fallback retry.

- [ ] **Step 3: Add the legal state transition and atomic cycle reset**

Allow `FALLBACK_RUNNING` and `FALLBACK_RETRYABLE_ERROR` to transition to `PRIMARY_RETRYABLE_ERROR`. Implement `RestartAcquisitionCycle` in one SQLite transaction. Its update must set:

```sql
state='PRIMARY_RETRYABLE_ERROR',
state_revision=state_revision+1,
primary_attempt=primary_attempt+1,
fallback_attempt=0,
next_retry_at=?,
queue_watermark=NULL,
history_watermark=NULL,
command_id=NULL,
correlation_started_at=NULL,
command_deadline=NULL,
grace_deadline=NULL,
overall_deadline=NULL
```

Insert a state-transition row with reason `fallback acquisition cycle deadline expired`.

- [ ] **Step 4: Enforce the deadline in service and worker paths**

Before running SpotiFLAC and before `FALLBACK_RETRYABLE_ERROR -> FALLBACK_RUNNING`, compare `worker.now()` or `service.now()` with `job.OverallDeadline`. If expired, calculate primary backoff and call `RestartAcquisitionCycle`. When classifying a retryable provider result, restart the cycle instead of persisting a retry whose timestamp is after the overall deadline.

- [ ] **Step 5: Add restart recovery coverage**

Persist a job whose process stopped after `overall_deadline`, run gateway recovery and worker reconciliation, and assert it converges to one scheduled primary retry without starting SpotiFLAC.

- [ ] **Step 6: Run race and integration tests**

Run: `rtk go test -race ./internal/gateway/... ./tests/integration/gateway/...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
rtk git add internal/gateway/domain/state.go internal/gateway/persistence/jobs.go internal/gateway/application/fallback.go internal/gateway/application/worker.go tests/integration/gateway/fallback_test.go tests/integration/gateway/recovery_test.go
rtk git commit -m "fix(acquisition): bound fallback cycles"
```

### Task 4: Build a complete SpotiFLAC binary from release source

**Files:**
- Modify: `deploy/docker/gateway.Dockerfile`
- Modify: `tests/integration/service_images_test.go`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: `/opt/spotiflac/spotiflac` containing `SpotiFLAC/extensions/_bridge.js`
- Consumes: exact `SPOTIFLAC_COMMIT` and verified source archive SHA-256
- Consumes: extension registry commit `6a4227aec696cd98d6fa9d25d92b1a38f9ae1a07` and an allowlisted SHA-256 for each `.sflx`

- [ ] **Step 1: Add source-policy and archive-content tests**

Assert the Dockerfile no longer downloads `releases/latest/download/SpotiFLAC-Linux-x86_64`, builds from `SPOTIFLAC_TAG`, passes both PyInstaller data mappings, and checks the frozen archive for the bridge.

```go
for _, want := range []string{
    "ARG SPOTIFLAC_COMMIT=0306da57dd855175549af119d95539e51ffbd801",
    "ARG SPOTIFLAC_SOURCE_SHA256=66301166b55020ec5ad46082f5c7acbe4811112aed996cd36b1fd8220e1b3754",
    "ARG SPOTIFLAC_EXTENSIONS_COMMIT=6a4227aec696cd98d6fa9d25d92b1a38f9ae1a07",
    `--add-data "frontend:SpotiFLAC/frontend"`,
    `--add-data "extensions/_bridge.js:SpotiFLAC/extensions"`,
    "pyi-archive_viewer",
} {
    if !strings.Contains(dockerfile, want) {
        t.Errorf("gateway Dockerfile missing %q", want)
    }
}
```

- [ ] **Step 2: Run policy test and verify failure**

Run: `rtk go test ./tests/integration -run TestGatewayImageBuildsCompleteSpotiFLACRuntime -count=1`

Expected: FAIL against the release-binary download.

- [ ] **Step 3: Replace the binary download with a Python 3.12 builder**

Use a builder stage that downloads the exact commit archive, verifies it before extraction, installs `requirements.txt`, the package, and PyInstaller, then runs the upstream command with the missing bridge included:

```dockerfile
ARG SPOTIFLAC_COMMIT=0306da57dd855175549af119d95539e51ffbd801
ARG SPOTIFLAC_SOURCE_SHA256=66301166b55020ec5ad46082f5c7acbe4811112aed996cd36b1fd8220e1b3754
RUN curl -fsSL "https://github.com/BartolomeoRusso9/SpotiFLAC-Module-Version/archive/${SPOTIFLAC_COMMIT}.tar.gz" -o /tmp/spotiflac.tar.gz \
    && echo "${SPOTIFLAC_SOURCE_SHA256}  /tmp/spotiflac.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/spotiflac.tar.gz --strip-components=1 \
    && python -m pip install --no-cache-dir -r requirements.txt pyinstaller \
    && python -m pip install --no-cache-dir . \
    && cd SpotiFLAC \
    && pyinstaller --onefile --name spotiflac \
       --add-data "frontend:SpotiFLAC/frontend" \
       --add-data "extensions/_bridge.js:SpotiFLAC/extensions" \
       --console ../launcher.py \
    && pyi-archive_viewer -l dist/spotiflac | grep -F 'SpotiFLAC/extensions/_bridge.js'
```

Keep the upstream release identity and exact source commit observable in the final image. Install `ffmpeg` and `flac` in the runtime because SpotiFLAC and downstream validation probe them even when transcoding is disabled.

Replace extension downloads from `main` with the exact registry commit. Verify before extraction against this allowlist:

```text
tidal-web d346f3e5fdb6f349d8f6ede1310d1961862936f64b3dabbbb4fba868cea31a9a
qobuz-web 3b5fab92608ada9eefe94dcc11a11bef79204804dee3ced2d86809df0d5306cd
deezer f6a2505da67c25d4ddfc5f233fdc14e127031f7e15a39fc9d2d0b82fc4b1fd60
```

Fail the image build on an unknown provider, checksum mismatch, missing manifest, or provider ID/version mismatch after extraction. Record engine and extension commits as OCI labels.

- [ ] **Step 4: Add a CI image smoke test**

Build the Gateway image, run `/opt/spotiflac/spotiflac --help`, and assert the exact arguments used by `runner.go` are present. Run the image as UID 1000 and verify extension directories are readable but not writable.

- [ ] **Step 5: Run image and Compose tests**

Run: `rtk go test ./tests/integration -run 'TestGatewayImage|TestServiceImages' -count=1`

Run: `rtk docker build -f deploy/docker/gateway.Dockerfile -t denyra-gateway-plan-check .`

Expected: both pass; build output shows the bridge archive check.

- [ ] **Step 6: Commit**

```bash
rtk git add deploy/docker/gateway.Dockerfile tests/integration/service_images_test.go .github/workflows/ci.yml
rtk git commit -m "fix(spotiflac): embed extension bridge"
```

### Task 5: Redact and cap persisted provider evidence

**Files:**
- Modify: `internal/gateway/adapters/spotiflac/runner.go`
- Modify: `internal/gateway/adapters/spotiflac/runner_test.go`
- Modify: `internal/config/defaults.go`
- Modify: `deploy/config/gateway.toml`

**Interfaces:**
- Consumes: `logsafe.RedactText(string) string`
- Produces: `sanitizeExecution(ProviderExecution, int) ProviderExecution`

- [ ] **Step 1: Write tests with bearer, password, query token, and oversized output**

Feed stdout/stderr containing `Authorization: Bearer abc`, `password=hunter2`, and `?access_token=xyz`, plus output larger than 64 KiB. Assert no secret remains and each stream is bounded with one truncation marker.

- [ ] **Step 2: Run tests and verify raw evidence leaks**

Run: `rtk go test ./internal/gateway/adapters/spotiflac -run TestProviderEvidenceIsRedactedAndCapped -count=1`

Expected: FAIL.

- [ ] **Step 3: Sanitize before returning provider execution**

Apply `logsafe.RedactText` after process completion and before `ProviderExecution` enters persistence. Keep command arguments only after rejecting arguments that contain secret-key patterns. Set the default process output limit to `65536` bytes and declare the same value in `gateway.toml`.

- [ ] **Step 4: Run adapter and Gateway tests**

Run: `rtk go test -race ./internal/gateway/... ./tests/integration/gateway/...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/gateway/adapters/spotiflac/runner.go internal/gateway/adapters/spotiflac/runner_test.go internal/config/defaults.go deploy/config/gateway.toml
rtk git commit -m "fix(security): sanitize provider evidence"
```

### Task 6: Deploy and reconcile existing production downloads

**Files:**
- Modify: `docs/runbooks/incidents.md`
- Test: live production deployment and APIs

**Interfaces:**
- Consumes: fixed late reconciliation, durable completion, and SpotiFLAC image
- Produces: Lidarr-imported Managed albums visible in Navidrome

- [ ] **Step 1: Run repository verification**

Run: `rtk make verify`

Run: `rtk go test ./tests/integration/...`

Expected: PASS.

- [ ] **Step 2: Record a read-only production baseline**

Record job states, slskd completion-event counts, exact FLAC counts under each active download ID, Pipeline candidate/import counts, Lidarr TrackFiles, and Managed-library FLAC count. Do not print credentials or provider tokens.

- [ ] **Step 3: Deploy through the supported update command**

Run: `rtk ssh production 'cd /home/nirwana/pribadi/denyra && ./denyra update'`

Expected: Gateway image bridge verification passes and all services become healthy.

- [ ] **Step 4: Wait for automatic reconciliation and verify every boundary**

For Tulus albums `Gajah`, `Manusia`, and `Monokrom`, verify in order: correlation evidence, pending candidate, completed candidate, Pipeline acceptance, validation, Lidarr Manual Import command, TrackFiles, final-library files, then Navidrome songs. For Tenxi, verify a new bounded fallback attempt no longer reports `Bridge JS not found`.

- [ ] **Step 5: Preserve unresolved files**

If any album does not reach verified Lidarr import, stop. Keep its slskd directory and all evidence. Do not mark it imported, delete it, or trigger another manual grab.

- [ ] **Step 6: Add the recovery procedure to the incident runbook**

Document read-only evidence checks and the rule that completed download directories are removed only by the normal post-import lifecycle.

- [ ] **Step 7: Commit**

```bash
rtk git add docs/runbooks/incidents.md
rtk git commit -m "docs: record acquisition recovery checks"
```
