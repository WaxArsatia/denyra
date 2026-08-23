# Acquisition orchestration design

## Purpose and boundaries

The acquisition-gateway is a Go service for secondary acquisition orchestration. Lidarr remains the authority for primary acquisition. The gateway never searches Soulseek directly and does not reproduce Lidarr's release management.

Primary flow remains:

```text
Lidarr
Lidarr.Plugin.Slskd
slskd
```

The gateway reads monitored Wanted albums, asks Lidarr to run `AlbumSearch`, observes the result, and starts SpotiFLAC only after a successful search produces no correlated primary grab. Manual uploads do not pass through the gateway.

## Stable job identity

Active acquisition deduplication uses:

```text
Lidarr album ID
+ MusicBrainz release-group ID
```

The selected release ID is a revisioned attribute rather than part of the dedup key because selection may change while the job exists. Correlation uses album and release identity, command context, timestamps, queue and history watermark, and download ID when available. Global queue changes alone are never accepted as proof.

## Acquisition lifecycle

```text
DISCOVERED
PRIMARY_SEARCH_REQUESTED
PRIMARY_SEARCH_RUNNING
PRIMARY_RECONCILING
PRIMARY_ACTIVE
PRIMARY_RETRYABLE_ERROR
FALLBACK_RUNNING
FALLBACK_RETRYABLE_ERROR
NO_CANDIDATE
DUAL_CANDIDATE
ARBITRATING
WINNER_LOCKED
HANDED_OFF
CANCELLED
```

`HANDED_OFF` means the pipeline durably accepted the candidate. It does not mean Lidarr imported it.

Every state transition uses a SQLite transaction and `state_revision`. An external effect is recorded as intent before execution. Workers use persisted leases. Restart recovery reconciles an intent with Lidarr, slskd, or the SpotiFLAC process before retrying it.

## Event and reconciliation model

Events and webhooks are primary triggers. A `30s` reconciliation loop repairs lost events and stale observations.

For a new Wanted job, the gateway:

1. records queue and history watermarks
2. persists the search intent
3. posts Lidarr `AlbumSearch`
4. records the command ID and context
5. waits for command completion
6. reconciles relevant queue and history changes through the grace window

The `AlbumSearch` command timeout is `10m`. Queue and history reconciliation polls every `2s`. A successful command has a `60s` grace window. The correlation window starts at the pre-command watermark and ends with that grace window.

Fallback starts only when the command succeeds and no correlated primary grab exists. A failed or timed-out command is `PRIMARY_RETRYABLE_ERROR`, not a zero-result search.

Primary retry schedule:

```text
1m
5m
15m
60m
then every 6h without limit
```

Retry continues until job state changes or an administrator cancels it.

Fallback outcomes keep legitimate no-result separate from operational failure. If every allowlisted provider completes successfully and returns a legitimate no-result, the job becomes `NO_CANDIDATE`. The album remains Wanted, and the gateway repeats the complete primary then fallback cycle every `24h`.

A network, runtime, provider, or subprocess error becomes `FALLBACK_RETRYABLE_ERROR`. It retries fallback after `5m`, `15m`, and `60m`, then every `6h` without limit. An operational error can never be converted to `NO_CANDIDATE`.

Wanted-state changes, release-selection changes, manual cancellation, and a correlated primary grab cancel the prior schedule and trigger new reconciliation. Backoff resets only after a material job or source-state change, or after successful acquisition. Provider attempts, results, no-result evidence, error classification, retry timestamps, and correlation evidence are persisted.

## Primary completion

Lidarr.Plugin.Slskd uses slskd batches and writes the Lidarr download ID into the destination path. The gateway and pipeline preserve this ID for correlation and Manual Import.

slskd file-completion webhooks are triggers, not release-completion authority. A complete batch must pass the pipeline stability and quality checks. Completed Download Handling remains disabled in Lidarr.

If a primary grab appears while fallback is running and SpotiFLAC has not completed a candidate, the gateway cancels fallback and continues the primary path. Operational cancellation and correlation evidence are persisted.

## SpotiFLAC runner

The canonical engine is `BartolomeoRusso9/SpotiFLAC-Module-Version` `v3.0.8`. The x86_64 Linux asset SHA-256 is:

```text
c008b5b59999f6f740d3f8e0290ce5fe18220dcd736aa903469e5b0ac062334a
```

The gateway executes the engine as an unprivileged child process. It does not import Python code into the Go process. The child receives a job-specific working directory, process group, resource limits, timeout, and a sanitized environment. Output is restricted to:

```text
/data/downloads/spotiflac/<job-id>
```

The invocation always disables SpotiFLAC-managed lyrics and enrichment:

```text
--no-lyrics
--no-enrich
--no-extensions-fallback
```

Provider search, resolution, and request establishment use a `180s` timeout per provider. FLAC transfer is not limited by that value. The overall acquisition timeout is `6h`, with concurrency `2`.

SpotiFLAC has no default registry or provider. Runtime installation and update mechanisms are disabled. Build-time verification installs only the approved extensions.

## Provider policy

Registry snapshot:

```text
repository: spotiflacapp/SpotiFLAC-Extension
commit: 8fc37551ead10683d7ab54cb4155dc5cca4948e6
```

Deterministic allowlist and order:

| Order | Extension | Version | SHA-256 |
| --- | --- | --- | --- |
| 1 | `ext:tidal-web` | `1.1.7` | `0d59043bab8229b5fd5664bc144aee25bfd3e6d031832cdce48b9d9ccef5ed22` |
| 2 | `ext:qobuz-web` | `1.1.0` | `9e6d14dc37623eed9ac6326c321b17fd802c36e907476f3068f7fcbe14d79f93` |
| 3 | `ext:deezer` | `1.2.0` | `dfead5b50889d2855b4409c6796421ccb35ffd3cac1e002498924e9a7c5446b3` |

All three extensions use JavaScript. Node.js is pinned to `v24.19.0` for Linux x86_64. The Node artifact SHA-256 is:

```text
14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647
```

The image downloads and verifies these exact artifacts at build time. It cannot fetch a floating registry, install a new extension, auto-install Node, or update any runtime component. Build and CI fail closed on engine, registry, manifest, extension, minimum runtime, Node, version, platform, or hash mismatch.

No personal Spotify, Deezer, Qobuz, TIDAL, or other provider credentials are passed to SpotiFLAC. Adding a provider requires an explicit allowlist and lock-file update plus compatibility tests.

## Candidate provenance

Each acquisition candidate records:

- candidate and job IDs
- source path and provider
- engine version and artifact hash
- registry repository and commit
- extension ID, version, and hash
- Node version and artifact hash
- sanitized command invocation
- process start, completion, exit status, and timeout data
- provider result and native identifiers when available
- output manifest and checksums
- Lidarr album, release-group, and selected release attributes
- watermark and correlation evidence
- effective configuration snapshot

Candidate records are immutable. Later status belongs to state transition tables, not mutable provenance fields.

## Dual-candidate arbitration

If SpotiFLAC has completed a candidate before a late primary grab appears, neither candidate is discarded. The job becomes `DUAL_CANDIDATE`. Every completed candidate is validated immediately by the pipeline.

The `30m` arbitration window starts when the first candidate reaches `APPROVED`. If another candidate reaches `APPROVED` within the window, the gateway compares both using the pipeline's lexicographic quality vector.

If all quality criteria are equal, tie-break order is:

1. slskd provenance over SpotiFLAC
2. earlier acquisition completion timestamp

If the window expires first, the already approved candidate wins. The winner lock is an atomic conditional transaction and is idempotent. Only one `candidate_id` can be recorded as winner.

A completed losing candidate moves to quarantine with reason `SUPERSEDED` and remains auditable. An incomplete active transfer is cancelled after the winner locks and recorded as `SUPERSEDED_CANCELLED`; it is not represented as a complete quarantine candidate.

Only the winner is handed to the pipeline's import path. Import delivery uses an idempotency key and is retried only after handoff reconciliation.

## Gateway persistence

The gateway SQLite database uses WAL, foreign keys, busy timeout, embedded migrations, and the SQLite online backup API.

Core tables:

```text
acquisition_jobs
attempts
external_effects
correlation_evidence
candidates
arbitrations
leases
state_transitions
config_snapshots
build_provenance
```

The gateway database contains acquisition candidates and provenance. It does not store pipeline validation state.

## Failure behavior

- Lidarr command failure or timeout becomes `PRIMARY_RETRYABLE_ERROR`.
- A successful search with no correlated grab starts fallback immediately.
- Successful no-result responses from every allowlisted provider become `NO_CANDIDATE` and restart the full cycle after `24h`.
- SpotiFLAC process, provider, network, or runtime failure becomes `FALLBACK_RETRYABLE_ERROR`.
- Provider operational errors are never converted into completed acquisition or legitimate zero-result states.
- External outages degrade status but do not delete the job.
- Network ambiguity triggers reconciliation before another effect.
- Restart resumes from persisted intent, lease, watermark, deadlines, and configuration snapshot.
