# Acquisition Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Denyra's deterministic Wanted orchestration: Lidarr-controlled primary search, strict correlation/reconciliation, SpotiFLAC fallback, durable retry/error semantics, dual-candidate arbitration, and idempotent pipeline handoff.

**Architecture:** The gateway domain models jobs, attempts, external-effect intents, retries, candidates, and winner arbitration without importing transport or process packages. Application services coordinate Lidarr and an isolated SpotiFLAC subprocess through adapters. SQLite persists every watermark, deadline, lease, attempt, evidence item, and state transition so restart recovery reconciles before repeating effects.

**Tech Stack:** Go 1.27.0, SQLite, Lidarr JSON API, Lidarr.Plugin.Slskd/slskd observation, pinned SpotiFLAC v3.0.8 subprocess, Node 24.19.0, pinned JavaScript provider extensions, authenticated internal JSON HTTP.

**Spec:** `docs/superpowers/specs/2026-08-24-acquisition-orchestration-design.md`

## Global Constraints

- Complete the system foundation and controlled media pipeline implementation plans first.
- Never search slskd/Soulseek directly. Only Lidarr may initiate the primary search.
- Fallback starts only after a successful AlbumSearch plus its reconciliation grace window proves no correlated primary grab.
- Never convert timeout, process, network, runtime, or provider errors into `NO_CANDIDATE`.
- Persist intent before every external effect and reconcile ambiguity before retry.
- Keep acquisition candidate provenance immutable. Pipeline validation state never enters the gateway database.
- Only a conditional atomic winner lock may authorize import handoff.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/gateway/domain/` | Job lifecycle, dedup identity, retries, correlation, candidates, arbitration |
| `internal/gateway/application/` | Wanted reconciliation, primary search, fallback, handoff, recovery workers |
| `internal/gateway/adapters/lidarr/` | Wanted, command, queue, history, and correlation observations |
| `internal/gateway/adapters/spotiflac/` | Sandboxed subprocess invocation and provider-result classification |
| `internal/gateway/adapters/pipeline/` | Candidate handoff and quality/winner coordination |
| `internal/gateway/persistence/` | SQLite repositories, intents, evidence, leases, and winner lock |
| `internal/gateway/transport/` | Private callbacks, health, and optional event endpoints |
| `migrations/gateway/` | Gateway schema |
| `tests/integration/gateway/` | Real SQLite/process/HTTP tests |
| `tests/acceptance/acquisition_test.go` | End-to-end deterministic acquisition cases |

## Task 1: Encode acquisition states, identity, and retry policies

**Files:**

- Create: `internal/gateway/domain/state.go`
- Create: `internal/gateway/domain/job.go`
- Create: `internal/gateway/domain/retry.go`
- Create: `internal/gateway/domain/outcome.go`
- Test: `internal/gateway/domain/state_test.go`
- Test: `internal/gateway/domain/retry_test.go`

- [ ] Define the exact closed lifecycle and a legal transition matrix. Require expected revision and record actor/reason/timestamp for every change.
- [ ] Use `(lidarr_album_id, musicbrainz_release_group_id)` as the active dedup key. Keep selected release ID as a revisioned attribute that triggers material-change reconciliation.
- [ ] Implement configured primary backoff `1m,5m,15m,60m`, then every `6h` without limit; fallback error backoff `5m,15m,60m`, then every `6h` without limit; and `NO_CANDIDATE` full-cycle retry every `24h`.
- [ ] Store computed retry as an absolute UTC timestamp. Do not recompute it from attempt count after restart.
- [ ] Reset backoff only on material Wanted/source/release change or acquisition success. Manual cancellation and correlated primary grab cancel prior schedules.
- [ ] Model provider outcomes as `CANDIDATE`, `LEGITIMATE_NO_RESULT`, or `RETRYABLE_ERROR`; make illegal conversion from error to no-result unrepresentable in the policy function.
- [ ] Run `rtk go test ./internal/gateway/domain`; expect red first, then green.
- [ ] Commit with message `feat(gateway): define acquisition state and retries`.

Exact states:

```go
type State string

const (
	StateDiscovered             State = "DISCOVERED"
	StatePrimarySearchRequested State = "PRIMARY_SEARCH_REQUESTED"
	StatePrimarySearchRunning   State = "PRIMARY_SEARCH_RUNNING"
	StatePrimaryReconciling     State = "PRIMARY_RECONCILING"
	StatePrimaryActive          State = "PRIMARY_ACTIVE"
	StatePrimaryRetryableError  State = "PRIMARY_RETRYABLE_ERROR"
	StateFallbackRunning        State = "FALLBACK_RUNNING"
	StateFallbackRetryableError State = "FALLBACK_RETRYABLE_ERROR"
	StateNoCandidate            State = "NO_CANDIDATE"
	StateDualCandidate          State = "DUAL_CANDIDATE"
	StateArbitrating            State = "ARBITRATING"
	StateWinnerLocked           State = "WINNER_LOCKED"
	StateHandedOff              State = "HANDED_OFF"
	StateCancelled              State = "CANCELLED"
)
```

## Task 2: Persist jobs, intents, evidence, immutable candidates, and leases

**Files:**

- Create: `migrations/gateway/000002_acquisition_core.sql`
- Create: `internal/gateway/persistence/jobs.go`
- Create: `internal/gateway/persistence/attempts.go`
- Create: `internal/gateway/persistence/effects.go`
- Create: `internal/gateway/persistence/evidence.go`
- Create: `internal/gateway/persistence/candidates.go`
- Create: `internal/gateway/persistence/arbitration.go`
- Create: `internal/gateway/persistence/leases.go`
- Test: `tests/integration/gateway/persistence_test.go`

- [ ] Add tables `acquisition_jobs`, `attempts`, `provider_results`, `external_effects`, `correlation_evidence`, `candidates`, `arbitrations`, `leases`, `idempotency_records`, `state_transitions`, `config_snapshots`, and `build_provenance`.
- [ ] Enforce one active job per stable dedup key, monotonic transition revision, immutable candidate rows, one winner per job, unique effect idempotency key, and immutable config references.
- [ ] Persist queue/history watermark, command ID/context, correlation window bounds, poll/grace/timeout deadlines, provider attempts/results, error class, retry timestamp, subprocess identity, and handoff acknowledgement.
- [ ] Insert effect intent before execution and update acknowledgement separately. Repositories must allow querying unresolved intents by effect type.
- [ ] Implement atomic `LockWinner(jobID, candidateID, expectedRevision)` with a conditional insert/update transaction; repeated selection of the same winner succeeds idempotently and a different winner conflicts.
- [ ] Test duplicate discovery, concurrent workers, candidate update denial, winner race, lease expiry, transaction crash, and exact persistence across database reopen.
- [ ] Run `rtk go test -race ./tests/integration/gateway -run Persistence`; expect pass.
- [ ] Commit with message `feat(gateway): persist deterministic acquisition state`.

## Task 3: Implement Lidarr Wanted discovery and primary search intent

**Files:**

- Create: `internal/gateway/adapters/lidarr/client.go`
- Create: `internal/gateway/adapters/lidarr/wanted.go`
- Create: `internal/gateway/adapters/lidarr/commands.go`
- Create: `internal/gateway/application/discovery.go`
- Create: `internal/gateway/application/primary_search.go`
- Test: `tests/integration/gateway/primary_search_test.go`

- [ ] Build bounded Lidarr adapter methods for monitored Wanted albums, album/release identity, queue watermark/page reads, history watermark/page reads, AlbumSearch command creation, and command status.
- [ ] Treat events/webhooks as primary discovery. Run configured 30-second Wanted reconciliation as safety recovery.
- [ ] Before AlbumSearch, persist current queue/history watermarks and the search effect intent. Post search, persist command ID, album/release context, request/response hashes, and absolute 10-minute deadline.
- [ ] Poll the command every configured 2 seconds. Command failure, cancellation, malformed response, or deadline becomes `PRIMARY_RETRYABLE_ERROR`, never zero-result.
- [ ] Respect Wanted removal, monitoring changes, selected-release revision, and cancellation while the command runs. Record why an old schedule/effect was superseded.
- [ ] Test webhook plus poll dedup, history pagination, clock boundaries, command timeout/failure, changed selection, restart after external success, and no direct slskd endpoint call.
- [ ] Run `rtk go test ./tests/integration/gateway -run PrimarySearch`; expect pass.
- [ ] Commit with message `feat(gateway): orchestrate Lidarr primary search`.

## Task 4: Correlate primary grabs through the explicit window

**Files:**

- Create: `internal/gateway/domain/correlation.go`
- Create: `internal/gateway/application/reconcile_primary.go`
- Create: `internal/gateway/adapters/lidarr/queue.go`
- Create: `internal/gateway/adapters/lidarr/history.go`
- Test: `internal/gateway/domain/correlation_test.go`
- Test: `tests/integration/gateway/correlation_test.go`

- [ ] Define correlation evidence over Lidarr album ID, release-group/release identity, command/job context, observation timestamp, watermark-relative queue/history record, and download ID when available.
- [ ] Reject global queue change alone, a record before watermark, wrong album/release, or a timestamp after the grace window.
- [ ] After successful command, reconcile every 2 seconds through the configured 60-second grace window. The correlation window begins at pre-command watermarks and ends at the persisted absolute grace deadline.
- [ ] If correlated grab appears, transition atomically to `PRIMARY_ACTIVE` and persist the complete evidence set. If none appears only after a successful command and full grace window, authorize fallback.
- [ ] Keep reconciliation active while fallback runs so a late primary grab can cancel an incomplete fallback or create a dual-candidate condition.
- [ ] Test delayed history visibility, queue-only then history confirmation, duplicate observations, unrelated global activity, missing download ID, late primary, and restart inside the grace window.
- [ ] Run `rtk go test ./internal/gateway/domain ./tests/integration/gateway -run Correlation`; expect pass.
- [ ] Commit with message `feat(gateway): correlate primary acquisition evidence`.

## Task 5: Implement the isolated deterministic SpotiFLAC runner

**Files:**

- Create: `internal/gateway/adapters/spotiflac/runner.go`
- Create: `internal/gateway/adapters/spotiflac/process_unix.go`
- Create: `internal/gateway/adapters/spotiflac/result.go`
- Create: `internal/gateway/adapters/spotiflac/manifest.go`
- Create: `internal/gateway/application/fallback.go`
- Test: `tests/integration/gateway/spotiflac_test.go`

- [ ] At startup verify engine v3.0.8/hash, registry commit, Node 24.19.0/hash, and exactly the ordered extensions `tidal-web@1.1.7`, `qobuz-web@1.1.0`, `deezer@1.2.0` with approved hashes/manifest compatibility.
- [ ] Reject extra/default providers, extension fallback, registry network access, auto-install, auto-update, and personal provider credentials.
- [ ] Run one unprivileged process group in `/data/downloads/spotiflac/<job-id>` with sanitized environment, configured resource limits, concurrency 2, and flags `--no-lyrics --no-enrich --no-extensions-fallback` plus explicit ordered `ext:<id>` allowlist.
- [ ] Apply 180-second timeout to each provider's search/resolve/request establishment, not completed FLAC transfer. Enforce one persisted six-hour overall deadline for the acquisition.
- [ ] Capture engine/registry/extension/Node identity, sanitized command, timestamps, exit/signal status, provider-native result, output manifest, and SHA-256 checksums as immutable provenance.
- [ ] Classify only an explicit successful provider no-result as legitimate. Process exit failure, malformed output, network/runtime/provider error, request timeout, or interrupted transfer becomes `FALLBACK_RETRYABLE_ERROR`.
- [ ] When every provider returns legitimate no-result, set `NO_CANDIDATE` and schedule a full primary-to-fallback cycle after 24 hours.
- [ ] Test fake engine behaviors: provider candidate, all no-result, first error, mixed no-result/error, hung establishment, long successful transfer, path escape, unexpected extension, process-group cancellation, and restart reconciliation.
- [ ] Run `rtk go test -race ./tests/integration/gateway -run SpotiFLAC`; expect pass.
- [ ] Commit with message `feat(gateway): add pinned SpotiFLAC fallback runner`.

The build and runtime verifier must use these identities verbatim:

```text
engine: BartolomeoRusso9/SpotiFLAC-Module-Version v3.0.8
engine sha256: c008b5b59999f6f740d3f8e0290ce5fe18220dcd736aa903469e5b0ac062334a
registry commit: 8fc37551ead10683d7ab54cb4155dc5cca4948e6
ext:tidal-web 1.1.7 sha256: 0d59043bab8229b5fd5664bc144aee25bfd3e6d031832cdce48b9d9ccef5ed22
ext:qobuz-web 1.1.0 sha256: 9e6d14dc37623eed9ac6326c321b17fd802c36e907476f3068f7fcbe14d79f93
ext:deezer 1.2.0 sha256: dfead5b50889d2855b4409c6796421ccb35ffd3cac1e002498924e9a7c5446b3
node: v24.19.0 linux-x64
node sha256: 14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647
```

The runner request must be explicit:

```go
type RunRequest struct {
	JobID           string
	ReleaseGroupID  string
	SelectedRelease string
	OutputDirectory string
	Providers       []string
	OverallDeadline time.Time
}
```

## Task 6: Handoff completed candidates and react to late primary grabs

**Files:**

- Create: `internal/gateway/application/candidates.go`
- Create: `internal/gateway/application/handoff.go`
- Create: `internal/gateway/adapters/pipeline/client.go`
- Create: `internal/gateway/adapters/spotiflac/cancel.go`
- Test: `tests/integration/gateway/handoff_test.go`

- [ ] Create one immutable acquisition candidate for each complete source output and preserve the same candidate ID in the pipeline handoff. `HANDED_OFF` means durable pipeline acceptance only.
- [ ] When primary correlation first identifies a grab, allocate its immutable candidate ID and register the pending source locator/download ID with the pipeline. This registration is not completion evidence: the pipeline may claim it only after the approved slskd completion trigger, lock, and stability checks succeed.
- [ ] Persist handoff intent and idempotency key before the internal API call. On timeout, query/replay with the same key and request hash.
- [ ] If a correlated primary grab appears while fallback has no completed candidate, cancel its process group and persist `SUPERSEDED_CANCELLED`; continue primary path.
- [ ] If fallback already has a completed candidate, retain it, transition to `DUAL_CANDIDATE`, hand both completed candidates for immediate pipeline validation, and cancel only still-active transfers after a winner locks.
- [ ] Never delete a completed candidate. A losing complete candidate is instructed to quarantine as `SUPERSEDED`; incomplete cancellation is not fabricated as a completed candidate.
- [ ] Test acceptance acknowledgement loss, duplicated callbacks, late primary before/after fallback completion, cancel failure, changed Wanted state, and ownership separation between databases.
- [ ] Run `rtk go test -race ./tests/integration/gateway -run Handoff`; expect pass.
- [ ] Commit with message `feat(gateway): hand candidates to controlled pipeline`.

## Task 7: Implement quality arbitration and one winner lock

**Files:**

- Create: `internal/gateway/domain/arbitration.go`
- Create: `internal/gateway/application/arbitration.go`
- Create: `internal/gateway/transport/quality_callbacks.go`
- Test: `internal/gateway/domain/arbitration_test.go`
- Test: `tests/integration/gateway/arbitration_test.go`

- [ ] Accept pipeline quality callbacks through authenticated, body-limited, idempotent internal HTTP with request ID. Persist exact approved timestamp, vector, warning classes, completion timestamp, and source.
- [ ] Start the persisted 30-minute arbitration window when the first candidate reaches `APPROVED`, not when dual detection or validation begins.
- [ ] If another candidate becomes approved before the deadline, compare pipeline quality vectors lexicographically. If equal, prefer slskd, then earliest acquisition completion timestamp.
- [ ] If the deadline expires first, select the first approved candidate. A candidate not approved by then cannot reopen the locked decision.
- [ ] Lock winner with one conditional transaction, persist all comparison evidence and timestamps, then send exactly one winner authorization to pipeline.
- [ ] Send loser outcome: complete candidate `SUPERSEDED` to quarantine; active incomplete transfer `SUPERSEDED_CANCELLED` after cancellation. Preserve audit/evidence.
- [ ] Test simultaneous approvals, equal vectors, quality-warning ordering, non-blocking warnings ignored, exact deadline boundary, duplicate callback, database contention, process crash after lock, and exactly-one authorization.
- [ ] Run `rtk go test -race ./internal/gateway/domain ./tests/integration/gateway -run Arbitration`; expect pass.
- [ ] Commit with message `feat(gateway): arbitrate one acquisition winner`.

## Task 8: Wire listeners, workers, maintenance, and startup recovery

**Files:**

- Create: `internal/gateway/transport/routes.go`
- Create: `internal/gateway/application/worker.go`
- Create: `internal/gateway/application/recovery.go`
- Create: `internal/gateway/application/maintenance.go`
- Update: `cmd/acquisition-gateway/main.go`
- Test: `tests/integration/gateway/recovery_test.go`
- Test: `tests/acceptance/acquisition_test.go`

- [ ] Compose database, repositories, policies, Lidarr, SpotiFLAC, pipeline client, event handlers, 30-second reconciliation, retry scheduler, health, and graceful shutdown in `main`.
- [ ] Expose an authenticated, read-only job-evidence endpoint for the pipeline Admin UI's `/acquisitions/{job-id}` route. Return a contract DTO containing job header, transitions, attempts, candidates, correlation evidence, and current revision; never expose the gateway database or a mutable repository.
- [ ] Admission blocks new acquisition when maintenance is active or `/data` free space is below `max(20GiB,5%)`; cleanup/reconciliation/cancellation/admin recovery remain available.
- [ ] Startup validates dependencies and paths, then reconciles expired leases, running/unknown subprocesses, unresolved Lidarr/pipeline intents, watermarks, retry deadlines, orphan output directories, and locked winners before new work.
- [ ] External dependency outages report degraded and leave ready true when local requirements pass. They move affected jobs to the correct retryable state without deletion.
- [ ] Add failure injection after intent, after external success, before acknowledgement, during lease, during cancellation, during winner race, and during duplicate event delivery.
- [ ] Run acceptance cases: primary success no fallback; successful primary zero-result immediate fallback; primary/fallback operational errors remain retryable; all legitimate fallback no-result schedules 24h full cycle; late primary handling; one dual winner/one import handoff; restart from every durable state.
- [ ] Run `rtk go test -race ./internal/gateway/... ./tests/integration/gateway ./tests/acceptance -run Acquisition`; expect pass twice.
- [ ] Commit with message `feat(gateway): run recoverable acquisition orchestration`.

## Completion Gate

- [ ] Tests prove no gateway code calls slskd search APIs and no SpotiFLAC path can target `/data/library`.
- [ ] Every command/provider/retry/correlation/arbitration deadline survives database close/reopen unchanged.
- [ ] Mixed no-result/error always ends in `FALLBACK_RETRYABLE_ERROR`; only all-successful legitimate no-result ends in `NO_CANDIDATE`.
- [ ] Failure injection produces no duplicate AlbumSearch, fallback candidate, winner, or handoff without prior reconciliation.
- [ ] `rtk go test -race ./internal/gateway/... ./tests/integration/gateway ./tests/acceptance -run Acquisition` passes twice.
- [ ] Record the verified gateway commit hash before executing the operations and clients plan.
