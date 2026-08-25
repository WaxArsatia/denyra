# Acceptance evidence

The deployment workflow is checked through automated integration tests and disposable Compose acceptance stacks.

## Covered operations

- a clean `./denyra setup` creates external config, secrets, data, and service accounts
- a second setup reconciles without resetting accounts or secrets
- update renders, pulls, and builds before changing the active release environment
- pre-cutover failure leaves the active release and protected paths unchanged
- post-cutover failure keeps the selected commit and converges through a later update retry
- Managed and Unmanaged libraries, service state, incomplete uploads, processing, quarantine, and unresolved downloads survive update failures
- legacy cleanup accepts only three fixed local paths and requires the exact `DELETE` token

The forced-update acceptance test uses a local temporary Git remote and an acceptance-only Compose health failure. It does not add a production switch that can disable healthchecks. The test records aggregate counts and file bytes before and after each failure boundary.

## Developer verification

```sh
make verify
go test ./tests/acceptance -count=1
make acceptance
scripts/check-runbooks.sh
```

The default test uses local fixtures and never contacts MusicBrainz, Spotify, or another public provider. `make acceptance` adds the disposable Compose stack.

`make live-compatibility` is an optional read-only schema smoke. Set `DENYRA_LIVE_MUSICBRAINZ_RELEASE_MBID`, `DENYRA_LIVE_LIDARR_URL`, `DENYRA_LIVE_LIDARR_API_KEY_FILE`, and `DENYRA_LIVE_NAVIDROME_PASSWORD_FILE` first. Optional URL and username overrides use the `DENYRA_LIVE_` prefix. The smoke logs in to Navidrome, reads libraries and scan status, reads Lidarr roots and profiles, and performs MusicBrainz search and lookup. It does not add, update, import, search Lidarr, or delete anything.

Acceptance evidence includes HTTP request counts, Manual Import command counts, and SHA-256 manifests for both library roots. The lost-ack fixture records one Manual Import even when the same request is observed again.

## Production acceptance on 2026-08-25

The forward-only release at commit `e4f1ce7` was accepted on the production host at `2026-08-25T10:39:26Z`.

- all six Compose services were running and healthy after cutover
- the Managed library contained 39 FLAC files, the Unmanaged library contained zero FLAC files, and 96 state files remained in place after cleanup
- Tulus releases Gajah, Manusia, and Monokrom reached `IMPORTED`; Lidarr reported 9, 10, and 10 track files respectively and zero Wanted/Missing tracks for those releases
- Navidrome indexed the same 9, 10, and 10 tracks for the accepted releases
- Tenxi fallback acquisition reached `FALLBACK_RETRYABLE_ERROR` after four bounded SpotiFLAC operational attempts; its next retry remained scheduled instead of being misclassified as no candidate
- the admin `/reviews`, `/acquisitions`, `/unmanaged`, and account-success requests returned HTTP 200 in 2-5 ms; acquisition detail rendered the timeline, attempts, candidates, and audit sections without raw secret markers
- readiness stayed locally ready and truthfully reported MusicBrainz and LRCLIB as degraded because no successful observation was available
- server-side accessibility checks found exactly one valid `aria-current="page"` marker on each accepted admin route

Cleanup permanently removed only `/srv/denyra/updates`, `/srv/denyra/data/backups`, and `/srv/denyra/secrets/restic_password`. Eleven unreferenced historical Denyra images and the unreferenced legacy Restic image were removed after checking every container image ID. No global Docker prune was used. The active library, state, unresolved downloads, processing work, quarantine evidence, and images used by other Compose projects were preserved.

Two exceptions were recorded during the initial acceptance:

- a second acquisition request can enter `ARBITRATING` or `PRIMARY_ACTIVE` after the first request hands off but before its import is reconciled; the winner lock still prevents two candidates from being imported, but pre-import request coalescing needs a separate correction
- exact browser acceptance was not completed because the requested Chrome DevTools session was unavailable; the authenticated admin acceptance above is server-side evidence and must not be treated as visual browser evidence

The acquisition exception was resolved in production at commit `31dadd9`. A durable Wanted-cycle lock now coalesces the Lidarr album and MusicBrainz release-group identity through `HANDED_OFF`, closes only after a complete paginated Wanted/Missing snapshot no longer contains the target, and permits a later genuinely new missing cycle. Deployment reconciliation cancelled two legacy active duplicates. One legacy duplicate reached an idempotent imported handoff during the preceding cutover, without changing the verified 39-file library layout. No active duplicate keys remained afterward. Exact Chrome visual acceptance was removed from the required acceptance scope by operator decision.
