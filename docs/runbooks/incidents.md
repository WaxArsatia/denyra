# Incident handling

Preserve SQLite databases, job evidence, quarantine contents, and structured logs before changing state. Do not delete a lease, retry row, candidate, or work directory only because it looks old.

## Low disk

When free space on the filesystem containing `/data` falls below `max(20 GiB, 5%)`, Denyra stops new claims, acquisitions, and imports. Cleanup, quarantine decisions, reconciliation, backup, restore, and administrative recovery remain available. Remove confirmed disposable data such as superseded transfers or move it through the documented retention process. Do not remove active work or the SQLite WAL files.

## Stuck lease or orphan directory

Restart the owning custom service once and inspect its recovery events. Startup reconciliation expires leases only after checking durable state and filesystem evidence. An orphan under processing or quarantine must be matched to its candidate ID before any move. If ownership cannot be established, leave it in quarantine and record the operator decision.

## External provider outage

MusicBrainz, LRCLIB, Soulseek, and SpotiFLAC provider failures are degraded dependencies. They must not make readiness fail or delete active jobs. Primary errors use `PRIMARY_RETRYABLE_ERROR`. Fallback network, runtime, and provider errors use `FALLBACK_RETRYABLE_ERROR`; they are never rewritten as `NO_CANDIDATE`. A legitimate all-provider no-result remains Wanted and starts a new primary-to-fallback cycle after 24 hours.

## Ambiguous or partial media work

Ambiguous identity goes to `REVIEW_REQUIRED` in quarantine without metadata mutation. A corrupt FLAC is rejected. If tagging stops partway through, keep the pre-mutation evidence and checksum, move the release batch to quarantine, and retry through the Admin UI. Never import the surviving tracks as a partial release.

## Database corruption

Enter maintenance if the service still responds. Stop stateful services, preserve the database and WAL files as evidence, and do not run ad hoc repair against the only copy. Follow `docs/runbooks/restore.md` into a new directory and use the last verified online-backup database. Cutover remains manual.

## Lost event or stalled reconciliation

Gateway reconciliation runs every 30 seconds as safety recovery; pipeline discovery also has a 30-second scanner. Triggering another webhook is safe because request IDs, idempotency keys, watermarks, correlation evidence, and state revisions prevent duplicate effects. Inspect the durable state before forcing Retry in the Admin UI.

For primary downloads, confirm slskd has the `denyra_completion` webhook configured for `DownloadFileComplete` and the gateway listens on `0.0.0.0:8082` inside its container. Do not publish port `8082` on the host. A missing webhook delays detection until the safety reconciliation; it never bypasses the Lidarr batch-completion gate.

## Session compromise

Use logout-all or explicit session revocation from the Admin UI. A password change revokes prior sessions. Session tokens are stored only as hashes. The accepted 30-day absolute expiry still applies; there is no idle timeout. If the audit or internal bearer secret may be exposed, stop the custom services, replace the affected secret, revoke sessions, and retain the audit trail.
