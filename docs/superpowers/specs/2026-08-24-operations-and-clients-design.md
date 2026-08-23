# Operations and clients design

## Streaming service

Navidrome `0.63.2` reads `/data/library` at `/music:ro`. Its database, cache, plugins, and transcoding cache live under `/data/state/navidrome` with read/write access. Navidrome cannot write to the master library.

Filesystem watching is the primary discovery mechanism. `Scanner.WatcherWait` is `5s`. Startup scan remains enabled. A scheduled scan runs every `1m` as recovery for missed filesystem events.

The plugin system is enabled with auto-reload disabled. Navidrome Lyrics Plugin `7.2.0` is installed as the verified `nd-lyrics.ndp` artifact. Lyrics priority is:

```text
.ttml,.elrc,.lrc,embedded,nd-lyrics
```

Persistent sidecars win before the runtime plugin. `nd-lyrics` uses LRCLIB as its primary runtime provider and does not write lyrics files because the library mount is read-only. Runtime lookup happens when an OpenSubsonic client requests lyrics.

Navidrome owns music-user authentication, playlists, favorites, streaming, and OpenSubsonic access. SFTPGo and media-pipeline admin accounts remain separate.

## Playback policy

Original FLAC is the default stream for LAN, Wi-Fi, and fast remote connections. Two logical bandwidth policies are documented:

- `opus-256` for normal cellular conditions
- `opus-160` for constrained cellular conditions

These names describe policy, not client-visible profile identifiers. OpenSubsonic clients may express the policy through `maxBitRate`, downsampling, or their own server settings. Transcoding occurs in Navidrome state/cache and never changes the master file.

Feishin `1.15.1` is the Linux client. It defaults to original quality and uses Navidrome/OpenSubsonic for browsing, playback, and lyrics.

Tempus `4.25.0` is the Android client. It defaults to original quality on LAN or Wi-Fi. Users choose a bandwidth policy for cellular use. Client binaries are not bundled into the server Compose project.

## Health and degraded state

Custom services expose:

- `/health/live` for process liveness
- `/health/ready` for local operational readiness
- degraded dependency details in health output and structured logs

Readiness blocks on database migrations, SQLite access, canonical filesystem paths, permissions, device identity, required binaries, dependency pins, valid configuration, and required internal services.

MusicBrainz, LRCLIB, Soulseek, and SpotiFLAC provider reachability do not fail readiness. Their outage is `degraded`, and affected jobs enter retryable operational states.

Storage thresholds are calculated on the filesystem that contains `/data`. When available space falls below `max(20GiB, 5%)`, the system stops new claims, acquisitions, and imports. It still permits cleanup, quarantine management, reconciliation, backup and restore operations, and administrative actions that can recover capacity.

## Logging and provenance

Custom services write structured JSON through `log/slog`. Relevant records include job, candidate, submission, state, request, and correlation IDs. Logs include build provenance and the effective configuration snapshot hash.

Passwords, API keys, bearer tokens, session tokens, CSRF values, provider-sensitive data, and secret values are redacted. The same policy applies to error wrapping and subprocess output. Sanitized command arguments can be stored as provenance when they contain no secret.

## Backup

Restic `0.19.1` is available through an optional Compose profile. The repository and credentials must be configured explicitly. A supported repository lives on another disk or host. Nothing defaults to `/data` as the disaster-backup target.

The deterministic backup runbook:

1. enable gateway and pipeline maintenance mode
2. drain active acquisition and import mutations
3. stop Lidarr, Navidrome, SFTPGo, and slskd briefly
4. create online SQLite backups for the custom services in `/data/backups`
5. snapshot library, state and configuration, incoming, processing, quarantine, and database backup outputs
6. verify the Restic snapshot
7. restart stopped services and check readiness
8. remove temporary workspace after successful verification

Raw acquisition downloads may be excluded. Retention is `7 daily`, `4 weekly`, and `12 monthly` snapshots.

Active SQLite files are never copied directly. Custom services use the SQLite online backup API. The short stop keeps third-party service state deterministic.

## Restore

A restore always targets a new directory. The runbook verifies:

- Restic repository integrity
- restored file checksums
- SQLite integrity checks
- dependency lock identity
- filesystem ownership and permissions
- expected `/data` device layout

Cutover happens only after verification. The first deployment includes a restore drill. Another drill is required after a backup schema or coverage change.

## Upgrades and rollback

There is no Watchtower or automatic dependency update. Images, plugins, tools, assets, Python wheels, Go modules, Node, SpotiFLAC, extension artifacts, and Debian packages are pinned.

An update requires:

1. explicit lock-file change
2. artifact and manifest verification
3. compatibility tests
4. backup
5. migration test on restored data
6. deployment and smoke tests
7. documented rollback using the prior binary/image and restored database when schema rollback is unsafe

Lidarr nightly is always deployed by digest with `platform: linux/amd64`. The floating tag is never used as runtime identity.

## Startup recovery

Gateway and pipeline startup recovery:

- validates migrations and effective configuration
- reclaims expired leases after reconciliation
- checks unresolved external-effect intents before retry
- discovers orphan work directories
- matches filesystem state to durable candidate state
- preserves active work through dependency outages
- refuses duplicate winner or import effects

No cleanup removes data unless durable state proves it is safe. Completed losers remain in quarantine until an explicit retention policy removes them.

## Test strategy

### Unit and property tests

Go unit tests cover state transitions, retry schedules, arbitration, duration thresholds, quality ordering, warning taxonomy, tag serialization, configuration parsing, and storage limits.

Fuzz and property tests cover path traversal, no-follow path handling inputs, Unicode normalization, webhook payloads, stale revisions, invalid state transitions, and boundary values.

Configuration tests cover defaults, TOML values, environment overrides, invalid units, invalid combinations, boundary values, immutable snapshots, and policy retention for active jobs. Thresholds, timeouts, intervals, retry schedules, concurrency, storage limits, duration tolerance, arbitration, session expiry, scans, and stability checks are never hard-coded in domain logic.

### Integration tests

- real SQLite tests for WAL concurrency, migrations, leases, winner locking, intent-before-effect, session token hashing, and online backup
- same-filesystem tests for atomic move, device checks, sealed fingerprints, quarantine movement, and disk thresholds
- synthetic FLAC fixtures generated with ffmpeg and FLAC tools
- adapter contracts for Lidarr, slskd, SpotiFLAC, MusicBrainz, LRCLIB, SFTPGo, and Navidrome
- Admin Web UI tests for Argon2id, generic authentication errors, CSRF, rotation, revocation, session expiry, HTMX fragments, stale `409`, and transactional audit writes
- build tests for templ regeneration, vendored HTMX checksum, font and icon sprite artifact hashes, content-hashed asset path generation, and a source scan that fails on inline `style` attributes, inline `<style>` or `<script>` blocks, and evaluated HTMX attributes such as `hx-on` or `js:` expressions, plus an assertion on the rendered `htmx-config` values so that a CSP violation coming from library defaults rather than from source is also caught, and the `scripts/verify-tokens/` contrast assertion over the design token table in both modes

Tests use no copyrighted audio.

### Failure injection

Tests stop or crash workers at each external boundary, including:

- after an effect intent but before execution
- after external success but before local acknowledgement
- during an active lease
- during dual-winner races
- after partial tag mutation
- after an ambiguous Lidarr import response
- during duplicate webhook delivery

Recovery must reconcile rather than repeat an unsafe effect.

### Compose and acceptance tests

Compose smoke tests use pinned real containers without requiring Soulseek or provider credentials. A separately gated profile can use configured live providers; it is not part of automatic CI.

Acceptance criteria:

- primary success never launches fallback
- successful primary zero-result launches fallback immediately
- primary and fallback operational errors stay retryable
- dual acquisition produces one atomic winner and one import
- manual submissions remain sealed and release-atomic
- corrupt or ambiguous media never reaches the library
- a valid release enters through Lidarr Manual Import with `.lrc` sidecars
- Lidarr creates `folder.jpg`
- Navidrome watcher discovers an import and scheduled scan recovers a missed event
- playback leaves master checksums unchanged
- restart from every durable state neither loses work nor duplicates import
- Restic snapshot restores into a new path with valid checksums and databases

Dependency compatibility tests fail closed when a version, platform, manifest, hash, Python dependency graph, Node runtime, SpotiFLAC engine, extension, or registry identity differs from `dependencies.lock.json`.

