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
