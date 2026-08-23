# Controlled Media Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Denyra's release-atomic validation, quarantine, review, deterministic FLAC mutation, enrichment, dual-candidate quality reporting, and exactly-once Lidarr Manual Import service with its internal Admin Web UI.

**Architecture:** A pure domain state machine and policy engine sit behind application services. Filesystem, media tools, MusicBrainz, LRCLIB, Beets, Lidarr, SQLite, internal HTTP, and Admin UI are adapters. Every mutation and state transition is transactional, revision-checked, auditable, recoverable after restart, and constrained to staging paths.

**Tech Stack:** Go 1.27.0, SQLite, `ffprobe`, `flac`, `metaflac`, Beets 2.13.1 advisory process, MusicBrainz/LRCLIB HTTP adapters, templ 0.3.1020, vendored HTMX 2.0.10, Argon2id, embedded static assets.

**Spec:** `docs/superpowers/specs/2026-08-24-controlled-media-pipeline-design.md`

## Global Constraints

- Complete `2026-08-24-denyra-system-foundation-implementation.md` first.
- Treat a MusicBrainz release as the smallest automatic validation/import unit; never partially approve or import it.
- Never mutate files in quarantine or `/data/library`; move an approved review back to work before tagging.
- Never re-encode audio. Artwork stays evidence; Lidarr creates final `folder.jpg`. Lyrics are same-basename `.lrc` sidecars.
- `APPROVED` means mutation, final SHA-256, and post-mutation `flac -t` have succeeded.
- Route every UI/API mutation through the same application service and transaction boundary.
- All policy values come from a referenced immutable config snapshot.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/pipeline/domain/` | Candidate states, validation results, warnings, duration and quality policy, tag schema |
| `internal/pipeline/application/` | Claim, validate, review, enrich, arbitrate-report, import, recovery use cases |
| `internal/pipeline/adapters/media/` | `ffprobe`, `flac`, and `metaflac` process adapters |
| `internal/pipeline/adapters/musicbrainz/` | Canonical release lookup and mapping |
| `internal/pipeline/adapters/lrclib/` | Persistent lyrics lookup |
| `internal/pipeline/adapters/beets/` | Non-mutating manual-ingest evidence |
| `internal/pipeline/adapters/lidarr/` | Manual Import, reconciliation, and library verification |
| `internal/pipeline/persistence/` | SQLite repositories and transaction implementation |
| `internal/pipeline/adminui/` | Authenticated templ/HTMX operator console |
| `internal/pipeline/internalapi/` | Gateway candidate handoff and quality callbacks |
| `migrations/pipeline/` | Pipeline schema |
| `tests/fixtures/flac/` | Generated non-copyrighted technical fixtures |
| `tests/integration/pipeline/` | Real SQLite, filesystem, subprocess, and HTTP integration tests |

## Task 1: Encode the pipeline state machine and release aggregate

**Files:**

- Create: `internal/pipeline/domain/state.go`
- Create: `internal/pipeline/domain/candidate.go`
- Create: `internal/pipeline/domain/transition.go`
- Create: `internal/pipeline/domain/errors.go`
- Test: `internal/pipeline/domain/transition_test.go`
- Fuzz: `internal/pipeline/domain/transition_fuzz_test.go`

- [ ] Write a transition matrix test covering every legal and illegal edge, terminal states, review return-to-work, supersession, cancellation, and monotonic `state_revision`.
- [ ] Define states as a closed string type and reject unknown persisted values at repository boundaries.
- [ ] Make the aggregate require candidate ID, source, release directory, config snapshot ID, state, revision, timestamps, and optional gateway job ID. Keep acquisition provenance as an immutable external reference.
- [ ] Require `expectedRevision` on every command. Return a typed stale-revision error containing current state and revision.
- [ ] Emit domain events containing actor, reason, previous/new state, revision, and UTC timestamp; persistence appends them in the same transaction.
- [ ] Run `rtk go test ./internal/pipeline/domain -run Transition`; first expect red, then implement until green.
- [ ] Commit with message `feat(pipeline): define candidate state machine`.

State constants are exact:

```go
type State string

const (
	StateReceived            State = "RECEIVED"
	StateClaimed             State = "CLAIMED"
	StateStabilizing         State = "STABILIZING"
	StateWorking             State = "WORKING"
	StateTechnicalValidation State = "TECHNICAL_VALIDATION"
	StateReleaseMatching     State = "RELEASE_MATCHING"
	StateReviewRequired      State = "REVIEW_REQUIRED"
	StateEnriching           State = "ENRICHING"
	StateApproved            State = "APPROVED"
	StateArbitrationPending  State = "ARBITRATION_PENDING"
	StateImportReady         State = "IMPORT_READY"
	StateImportSubmitted     State = "IMPORT_SUBMITTED"
	StateImportReconciling   State = "IMPORT_RECONCILING"
	StateImported            State = "IMPORTED"
	StateQuarantined         State = "QUARANTINED"
	StateRejected            State = "REJECTED"
	StateSuperseded          State = "SUPERSEDED"
	StateCancelled           State = "CANCELLED"
)
```

## Task 2: Create pipeline persistence, audit, leases, and immutable evidence

**Files:**

- Create: `migrations/pipeline/000002_pipeline_core.sql`
- Create: `internal/pipeline/persistence/repositories.go`
- Create: `internal/pipeline/persistence/candidates.go`
- Create: `internal/pipeline/persistence/evidence.go`
- Create: `internal/pipeline/persistence/audit.go`
- Create: `internal/pipeline/persistence/leases.go`
- Test: `tests/integration/pipeline/persistence_test.go`

- [ ] Add normalized tables: `users`, `roles`, `user_roles`, `sessions`, `submissions`, `candidates`, `candidate_files`, `validation_results`, `track_matches`, `metadata_snapshots`, `mutations`, `enrichments`, `import_intents`, `idempotency_records`, `leases`, `audit_events`, `state_transitions`, `config_snapshots`, and `build_provenance`.
- [ ] Make pre/post snapshots and candidate provenance insert-only. Use foreign keys and unique constraints for candidate ID, source evidence identity, transition revision, import idempotency key, and immutable config hash.
- [ ] Implement `UpdateState` as `UPDATE ... WHERE candidate_id=? AND state_revision=?`; require one affected row.
- [ ] In one transaction: update aggregate state, append transition, append audit event, and store any decision/effect intent.
- [ ] Implement renewable leases with owner ID, acquired/expiry UTC timestamps, config snapshot, and revision. An expired lease cannot be reclaimed before application reconciliation authorizes it.
- [ ] Test two-writer stale decisions, crash rollback, duplicate handoff, immutable row update denial, append-only audit triggers, lease races, and WAL concurrency.
- [ ] Run `rtk go test -race ./tests/integration/pipeline -run Persistence`; expect pass.
- [ ] Commit with message `feat(pipeline): persist auditable pipeline state`.

## Task 3: Secure completion claims and atomic filesystem movement

**Files:**

- Create: `internal/pipeline/application/claim.go`
- Create: `internal/pipeline/adapters/filesystem/tree.go`
- Create: `internal/pipeline/adapters/filesystem/lock.go`
- Create: `internal/pipeline/adapters/filesystem/move.go`
- Test: `tests/integration/pipeline/claim_test.go`
- Fuzz: `internal/pipeline/adapters/filesystem/path_fuzz_test.go`

- [ ] Define durable completion evidence for slskd, SpotiFLAC, other acquisition, and sealed manual submissions. No event alone authorizes a claim.
- [ ] Open the canonical source root once, traverse with no-follow semantics, and reject symlinks, traversal, nested mounts, sockets, devices, FIFOs, and non-regular files. Recheck identity immediately before move.
- [ ] Compute a sorted tree fingerprint over relative path, size, mtime, and device/inode identity. Compare two scans separated by configured `10s` stability interval.
- [ ] Acquire both persisted lease and release-directory lock. Allow access only to the completion-evidence path.
- [ ] Move the whole directory with one same-device rename to `/data/processing/work/<candidate-id>`; `EXDEV` is a startup/layout error, never a copy fallback.
- [ ] For manual submissions, compare the sealed fingerprint. A mismatch moves state to `WAITING_RESUBMIT` without updating the sealed value.
- [ ] Test path-swap races, symlink insertion, nested mount, duplicate event, changing file, wrong device, lock contention, restart after rename, and scanner recovery.
- [ ] Run `rtk go test -race ./tests/integration/pipeline -run Claim`; expect pass.
- [ ] Commit with message `feat(pipeline): claim completed release batches safely`.

## Task 4: Implement technical validation and immutable pre-mutation evidence

**Files:**

- Create: `internal/pipeline/domain/technical.go`
- Create: `internal/pipeline/application/technical_validation.go`
- Create: `internal/pipeline/adapters/media/ffprobe.go`
- Create: `internal/pipeline/adapters/media/flac.go`
- Create: `internal/pipeline/adapters/media/checksum.go`
- Create: `internal/pipeline/adapters/media/heuristic.go`
- Create: `scripts/test-fixtures/generate-flac.sh`
- Test: `tests/integration/pipeline/technical_validation_test.go`

- [ ] Generate small synthetic FLAC fixtures: mono/stereo, multiple bit depths/sample rates, truncated stream, fake `.flac`, unreadable tags, and an extra non-audio sidecar. Check in only fixtures permitted by repository policy.
- [ ] Define narrow process-runner interfaces returning sanitized invocation, exact tool version, exit status, stdout evidence, redacted stderr, and timeout classification.
- [ ] Parse `ffprobe` JSON and require FLAC codec/container, positive duration/channels/sample rate, and structurally valid values. Run `flac -t` as an independent hard gate.
- [ ] Store before-mutation SHA-256, tree/file manifest, technical values, original Vorbis comments, embedded picture evidence, and command evidence before advancing.
- [ ] Classify corrupt/container/path/structure failures as release-wide reject plus quarantine. Expose a lossless heuristic interface whose findings can only create `QUALITY_WARNING` or review, never hard rejection.
- [ ] Make non-FLAC audio in manual ingest review/reject according to the approved no-transcode policy; never invoke ffmpeg conversion.
- [ ] Run `rtk go test ./internal/pipeline/... ./tests/integration/pipeline -run Technical`; expect pass.
- [ ] Commit with message `feat(pipeline): validate FLAC batches as hard gate`.

## Task 5: Implement MusicBrainz release matching and duration policy

**Files:**

- Create: `internal/pipeline/domain/duration.go`
- Create: `internal/pipeline/domain/release_match.go`
- Create: `internal/pipeline/domain/warning.go`
- Create: `internal/pipeline/application/release_matching.go`
- Create: `internal/pipeline/adapters/musicbrainz/client.go`
- Create: `internal/pipeline/adapters/beets/advisor.go`
- Test: `internal/pipeline/domain/duration_test.go`
- Test: `tests/integration/pipeline/release_matching_test.go`

- [ ] Write boundary tests in integer milliseconds for exactly below/equal/above `max(5000,2%)`, `max(15000,5%)`, `max(30000,1%)`, and `max(90000,3%)`; cover missing reference duration and integer-overflow inputs.
- [ ] Return one of `AUTO_APPROVE`, `MANUAL_REVIEW`, or `REJECT` per track and optional total. Final release status is the strictest; total never compensates for a track.
- [ ] On any ambiguity or manual-review result, atomically move the whole unmodified release from work to `/data/quarantine/<candidate-id>` and enter `REVIEW_REQUIRED`. Approval requires an explicit MusicBrainz Release ID and atomically returns the whole release to work before matching/tagging; reject leaves it quarantined. Never mutate an ambiguous release.
- [ ] Require one explicit lowercase canonical MusicBrainz Release ID before automatic metadata mutation. Map every file one-to-one to the same release/medium/track and verify counts, positions, recording/release-track IDs, ISRC, and no extra audio.
- [ ] Support official single, multi-disc, deluxe, and Various Artists through the same aggregate rules.
- [ ] Implement the MusicBrainz adapter with bounded responses, identifiable retryable errors, and persisted response/evidence hashes. External outage creates retryable operation, not ambiguity or rejection.
- [ ] Execute Beets only for manual-ingest advisory evidence with move/copy/write/import/art disabled and no library path. Persist confidence, candidate IDs, output hash, and original response; never accept Beets as authority.
- [ ] Split warnings into `QUALITY_WARNING` and `NON_BLOCKING_WARNING`; test that missing lyrics/artwork cannot change audio ranking.
- [ ] Run `rtk go test ./internal/pipeline/domain ./tests/integration/pipeline -run 'Duration|ReleaseMatching'`; expect pass.
- [ ] Commit with message `feat(pipeline): match release-atomic MusicBrainz identity`.

Use pure integer threshold evaluation:

```go
func thresholdMS(referenceMS int64, floorMS int64, percentBasisPoints int64) int64 {
	percent := (referenceMS*percentBasisPoints + 9_999) / 10_000
	if percent > floorMS {
		return percent
	}
	return floorMS
}
```

## Task 6: Implement deterministic Picard-compatible FLAC tagging

**Files:**

- Create: `internal/pipeline/domain/tags.go`
- Create: `internal/pipeline/domain/normalize.go`
- Create: `internal/pipeline/application/mutate.go`
- Create: `internal/pipeline/adapters/media/metaflac.go`
- Test: `internal/pipeline/domain/tags_test.go`
- Test: `tests/integration/pipeline/mutation_test.go`

- [ ] Lock the managed tag schema and canonical serialization in table/golden tests. Normalize NFC, trim edges, reject empty values, lowercase/validate MBID UUIDs, preserve date precision, and serialize track/disc integers without zero padding.
- [ ] Emit repeated Vorbis entries for multi-value fields. Preserve ARTIST/artist-ID and ALBUMARTIST/albumartist-ID order/alignment; normalize/deduplicate/sort GENRE and ISRC deterministically.
- [ ] Map `MUSICBRAINZ_TRACKID` to Recording MBID and `MUSICBRAINZ_RELEASETRACKID` to release-track MBID. Reject `MUSICBRAINZ_RECORDINGID` as a managed canonical field.
- [ ] Preserve unknown comments, replace managed fields only, and remove embedded pictures. Do not embed artwork and do not modify audio frames.
- [ ] Execute deterministic `metaflac` argument sequences against files in work only. Store before/after metadata and exact mutation diff.
- [ ] Recompute SHA-256 and rerun `flac -t` after mutation. Only then allow `APPROVED`; failure quarantines the entire release with both evidence sets.
- [ ] Test repeat mutation idempotency, Unicode, repeated values, unknown comments, embedded-picture removal, tool crash between tracks, rollback/recovery classification, and unchanged audio-frame signature.
- [ ] Treat an upgrade candidate as a complete release replacement. Tests must prove no automatic path can combine old-library tracks with a new candidate or import a mixed-source/mixed-quality release.
- [ ] Run `rtk go test ./internal/pipeline/domain ./tests/integration/pipeline -run Mutation`; expect pass.
- [ ] Commit with message `feat(pipeline): mutate FLAC tags deterministically`.

Canonical MusicBrainz fields are:

```go
var ManagedMusicBrainzFields = []string{
	"MUSICBRAINZ_TRACKID",
	"MUSICBRAINZ_RELEASETRACKID",
	"MUSICBRAINZ_ALBUMID",
	"MUSICBRAINZ_RELEASEGROUPID",
	"MUSICBRAINZ_ARTISTID",
	"MUSICBRAINZ_ALBUMARTISTID",
}
```

## Task 7: Add lyrics/artwork evidence and quality reporting

**Files:**

- Create: `internal/pipeline/application/enrich.go`
- Create: `internal/pipeline/adapters/lrclib/client.go`
- Create: `internal/pipeline/domain/quality.go`
- Create: `internal/pipeline/internalapi/quality_client.go`
- Test: `internal/pipeline/domain/quality_test.go`
- Test: `tests/integration/pipeline/enrichment_test.go`

- [ ] Fetch LRCLIB after exact release matching. Prefer word-synchronized, then line-synchronized, then plain lyrics; persist provider/evidence/result classification.
- [ ] Write `.lrc` only beside its work-directory FLAC using the exact current source basename. No result or provider outage produces `NON_BLOCKING_WARNING` and never blocks audio approval.
- [ ] Fetch/store artwork bytes or source evidence only in work/evidence storage. Do not embed it, create final `folder.jpg`, or perform any post-import write.
- [ ] Build the lexicographic quality vector in the approved order: identity, edition, quality-warning absence, source/lossless confidence, bit depth, sample rate. Keep provenance priority and completion time as gateway tie-break fields.
- [ ] Report `APPROVED` candidate and immutable quality evidence to gateway through authenticated, idempotent internal JSON. Persist intent before request and reconcile ambiguous acknowledgement.
- [ ] Test that lyrics/artwork errors do not affect ranking, while `QUALITY_WARNING` does; test idempotent callbacks and retryable external failures.
- [ ] Run `rtk go test ./internal/pipeline/... ./tests/integration/pipeline -run 'Quality|Enrichment'`; expect pass.
- [ ] Commit with message `feat(pipeline): enrich sidecars and report quality`.

## Task 8: Implement controlled Lidarr Manual Import and verification

**Files:**

- Create: `internal/pipeline/application/import.go`
- Create: `internal/pipeline/application/import_reconcile.go`
- Create: `internal/pipeline/adapters/lidarr/client.go`
- Create: `internal/pipeline/adapters/lidarr/manual_import.go`
- Create: `internal/pipeline/adapters/lidarr/verify.go`
- Test: `tests/integration/pipeline/import_test.go`

- [ ] Accept import readiness only for a manual approved candidate or the gateway-locked winner. Atomically move the complete directory to `/data/processing/approved/<candidate-id>`.
- [ ] Persist import intent, request hash, target MusicBrainz Release ID, candidate ID, release manifest, and optional Lidarr download ID before calling the Manual Import API.
- [ ] Configure/verify Lidarr Completed Download Handling disabled and `Import Extra Files` includes `lrc`. Treat configuration drift as not-ready for import.
- [ ] On timeout/ambiguous response, query commands, history, queue, track files, album files, and recorded final paths before any retry.
- [ ] Mark `IMPORTED` only after every expected track and available `.lrc` is confirmed through Lidarr and the pipeline's read-only library mount. Store final paths and checksums.
- [ ] Delete staging source only after verified import. Retain mutation/provenance/audit rows permanently according to policy.
- [ ] Test external success before acknowledgement, duplicate winner delivery, process crash after intent, incomplete album, missing sidecar, changed final checksum, and exactly one import effect.
- [ ] Run `rtk go test -race ./tests/integration/pipeline -run Import`; expect pass.
- [ ] Commit with message `feat(pipeline): import approved releases through Lidarr`.

## Task 9: Implement local admin authentication, sessions, and CSRF

**Files:**

- Create: `internal/pipeline/application/auth.go`
- Create: `internal/pipeline/application/sessions.go`
- Create: `internal/pipeline/adminui/middleware/auth.go`
- Create: `internal/pipeline/adminui/middleware/csrf.go`
- Create: `internal/pipeline/adminui/handlers/login.go`
- Create: `internal/pipeline/adminui/handlers/account.go`
- Test: `tests/integration/pipeline/auth_test.go`

- [ ] Bootstrap the first admin only when the users table is empty, reading password from configured environment secret or secret file. Persist no bootstrap login path after creation.
- [ ] Hash passwords with Argon2id: 64 MiB memory, 3 iterations, parallelism 2, 16-byte salt, 32-byte output; minimum length 8 and no composition rules.
- [ ] Generate 32-byte CSPRNG session tokens and store SHA-256 only. Set 30-day absolute expiry, no idle timeout, and cookie `HttpOnly; SameSite=Strict; Path=/` without `Secure` because internal HTTP is the accepted baseline.
- [ ] Rotate after login, password change, role/privilege change, and logout-all/revocation. Password change revokes older sessions. Do not rotate for Approve/Reject/Retry.
- [ ] Keep users/roles/sessions normalized for multiple admins. Use generic login errors and no throttling/lockout.
- [ ] Protect every mutation with a session-bound CSRF token validated in constant time. Audit login success, logout, password change, role change, and revocation without recording credentials/tokens.
- [ ] Test user enumeration resistance, Argon parameters, expiry boundary, token hash storage, rotation, revocation, concurrent logout-all, CSRF failure, and cookie attributes.
- [ ] Run `rtk go test -race ./tests/integration/pipeline -run Auth`; expect pass.
- [ ] Commit with message `feat(pipeline): secure local admin sessions`.

## Task 10: Build the templ and HTMX Admin Web UI

**Files:**

- Create: `internal/pipeline/adminui/views/layout.templ`
- Create: `internal/pipeline/adminui/views/login.templ`
- Create: `internal/pipeline/adminui/views/incoming.templ`
- Create: `internal/pipeline/adminui/views/reviews.templ`
- Create: `internal/pipeline/adminui/views/review_detail.templ`
- Create: `internal/pipeline/adminui/views/acquisition.templ`
- Create: `internal/pipeline/adminui/views/audit.templ`
- Create: `internal/pipeline/adminui/views/account.templ`
- Create: `internal/pipeline/adminui/views/sessions.templ`
- Create: `internal/pipeline/adminui/handlers/routes.go`
- Create: `internal/pipeline/adminui/assets/css/app.css`
- Create: `internal/pipeline/adminui/assets/assets.go`
- Create: `scripts/verify-tokens/main.go`
- Create: `scripts/verify-ui-source.sh`
- Test: `tests/integration/pipeline/adminui_test.go`

- [ ] Extract and verify exactly the four pinned Geist WOFF2 files, vendored HTMX 2.0.10 (`htmx.org-2.0.10.tgz` SHA-256 `577ad40c1c94c9de47edb89e0aec78a8353d36024c50017eb53e02992a55e889`; `dist/htmx.min.js` SHA-256 `71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de`), and only referenced regular Phosphor SVGs. Compile one icon sprite; fail on unknown icon references.
- [ ] Implement light/dark tokens, 4px radius, fixed spacing/layer scales, dense 32px rows, Geist/Geist Mono roles, responsive shell, skip link, sticky offsets, reduced motion, and WCAG AA contrast exactly as designed.
- [ ] Serve content-hashed assets with one-year immutable caching. Serve authenticated HTML with `no-store` and the exact CSP/security headers. Configure HTMX by meta tag with `allowEval=false` and `includeIndicatorStyles=false`.
- [ ] Implement `/login`, `/incoming`, `/reviews`, `/reviews/{candidate-id}`, `/acquisitions/{job-id}`, `/audit`, `/account/password`, and `/sessions`. Use real tables, captions/headings, cursor pagination, stable fragment IDs, and `aria-live` announcements.
- [ ] Render `/acquisitions/{job-id}` through an application-level `AcquisitionEvidenceReader` backed by the gateway's authenticated read-only contract endpoint. Do not read or replicate the gateway database and do not treat the fetched snapshot as pipeline state.
- [ ] Implement loading-after-150ms, empty, inline error, degraded, and stale-revision states for every fragment. A stale mutation returns `409`, refreshes evidence/form, names current state/revision, and never replays the action.
- [ ] Route Submit, Approve, Reject, Retry, Cancel, password, logout, and revoke actions to application services. Approve requires canonical MB Release ID plus reason; Reject/Retry require reason; consequential actions use two-step inline confirmation.
- [ ] Render review evidence: per-track/total duration values and band, before/after metadata diff, preserved unknown tags, removed pictures, artwork/lyrics results, checksums, provenance, correlation, and state history.
- [ ] Run templ generation and commit generated `.go`. Add CI checks for regeneration drift, inline style/script, `hx-on`, `js:`, evaluated expressions, CSP meta values, asset hashes, and token contrast in both themes.
- [ ] Run `rtk templ generate && rtk go test ./tests/integration/pipeline -run AdminUI && rtk scripts/verify-ui-source.sh && rtk go run ./scripts/verify-tokens`; expect pass and no generated diff.
- [ ] Commit with message `feat(pipeline): add operator review console`.

## Task 11: Expose internal handoff, manual submission, workers, and recovery

**Files:**

- Create: `internal/pipeline/internalapi/routes.go`
- Create: `internal/pipeline/internalapi/candidates.go`
- Create: `internal/pipeline/application/submissions.go`
- Create: `internal/pipeline/application/worker.go`
- Create: `internal/pipeline/application/recovery.go`
- Update: `cmd/media-pipeline/main.go`
- Test: `tests/integration/pipeline/recovery_test.go`
- Test: `tests/acceptance/pipeline_test.go`

- [ ] Add size-limited, bearer-authenticated, idempotent internal endpoints for gateway handoff, winner selection, supersession, and cancellation. Keep listener private and separate from Admin UI.
- [ ] Implement incoming discovery plus explicit Submit. Seal the tree fingerprint and expose `WAITING_RESUBMIT` on pre-claim drift.
- [ ] Run event-first queues plus configured 30-second recovery scanners. Workers obey leases, filesystem locks, concurrency, maintenance mode, and storage admission gate.
- [ ] On startup reconcile expired leases, unresolved effects, orphan work/approved/quarantine directories, partially mutated releases, import intents, and duplicate webhook deliveries before scheduling work.
- [ ] Preserve candidates on external dependency outage and classify network/tool failures as retryable operations without weakening validation outcomes.
- [ ] Add acceptance cases for corrupt/ambiguous release quarantine, manual review approval return-to-work, deterministic mutation, post-check failure, sidecar import, stale review, restart at every durable state, and unchanged master checksums.
- [ ] Run `rtk go test -race ./internal/pipeline/... ./tests/integration/pipeline ./tests/acceptance -run Pipeline`; expect pass.
- [ ] Commit with message `feat(pipeline): run recoverable controlled media workflow`.

## Completion Gate

- [ ] Generate all synthetic fixtures from documented commands; repository contains no copyrighted audio.
- [ ] `rtk go test -race ./internal/pipeline/... ./tests/integration/pipeline ./tests/acceptance -run Pipeline` passes twice.
- [ ] `rtk templ generate && rtk git diff --exit-code -- internal/pipeline/adminui/views` proves generated sources are committed.
- [ ] `rtk scripts/verify-ui-source.sh && rtk go run ./scripts/verify-tokens` pass for both color schemes and CSP rules.
- [ ] Failure injection at every intent/effect boundary reconciles without duplicate import or lost candidate.
- [ ] A full release reaches `IMPORTED` only after Lidarr-created final paths and `.lrc` sidecars verify; pipeline never writes `/data/library` or final `folder.jpg`.
- [ ] Record the verified pipeline commit hash before executing the acquisition orchestration plan.
