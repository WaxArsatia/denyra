# Acceptance evidence

The deployment workflow is checked through automated integration tests and disposable Compose acceptance stacks.

## Covered operations

- a clean `./denyra setup` creates external config, secrets, data, and service accounts
- a second setup reconciles without resetting accounts or secrets
- update pulls and builds before stopping the active stack
- an unhealthy candidate restores the prior config, state, and six running image IDs
- update snapshots exclude the library and retain failed candidate state
- backup includes both libraries, incomplete uploads, processing data, and durable migration state while excluding downloads, cache, and temporary backup data
- restore verifies Managed and Unmanaged checksums, incomplete uploads, both Denyra databases, migration ledgers, ownership, and filesystem layout

The forced-update acceptance test uses a local temporary Git remote and an acceptance-only Compose health failure. It does not add a production switch that can disable healthchecks.

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
