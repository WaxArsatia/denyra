# Unmanaged FLAC ingestion design

## Purpose

Denyra must accept a local FLAC album even when its artist or release is not in Lidarr or MusicBrainz. Browser uploads and SFTPGo uploads share one validation workflow. Releases that MusicBrainz can identify continue through Lidarr. Releases without a reliable MusicBrainz match enter a separate Navidrome library owned by Denyra.

An unmanaged release can later move to Lidarr after an administrator runs a manual catalog check and confirms the match. Denyra never schedules this check and never migrates a release based on a match alone.

The initial example is `Kaleb J/OFF GUARD`. Its sampled FLAC has useful album, artist, date, track, UPC, ISRC, Spotify source, and embedded front-cover metadata, but no MusicBrainz identifiers.

## Goals

- Upload an album folder through the Admin Web UI.
- Keep SFTPGo as an equivalent ingress path.
- Validate and import releases that are absent from Lidarr.
- Support releases that are also absent from MusicBrainz.
- Keep managed and unmanaged files under separate ownership.
- Show unmanaged music in a separate Navidrome library.
- Let an administrator edit incomplete or conflicting metadata before import.
- Reuse embedded or sidecar artwork and try a bounded automatic lookup when needed.
- Check selected unmanaged releases for new MusicBrainz matches on demand.
- Migrate selected exact matches to Lidarr only after confirmation.
- Preserve restart safety, idempotency, audit evidence, backups, and one-command deployment.

## Non-goals

- Creating placeholder artists or albums inside Lidarr.
- Writing unmanaged files into Lidarr's library root.
- Submitting new data to MusicBrainz.
- Scheduling catalog checks.
- Migrating an ambiguous match automatically.
- Searching or downloading missing albums during catalog registration.
- Transcoding non-FLAC audio.
- Adding a new service, public port, antivirus engine, or general chunk-upload framework.
- Providing a public Internet upload service. The existing private-host security boundary still applies.

## Architecture and ownership

The feature stays inside `media-pipeline`. It adds application services and adapters, not another container or database.

```text
Browser upload --\
                  +--> manual submission --> FLAC validation --> identity decision
SFTPGo upload ----/                                             |
                                      +-------------------------+-------------------------+
                                      |                                                   |
                              exact MusicBrainz match                             no exact match
                                      |                                                   |
                         Lidarr catalog preparation                        metadata and artwork review
                                      |                                                   |
                         current Lidarr import                           unmanaged atomic import
                                      |                                                   |
                              /data/library                              /data/library-unmanaged
```

Filesystem ownership remains explicit:

| Path | Writer | Readers |
| --- | --- | --- |
| `/data/incoming/manual` | SFTPGo and media-pipeline browser ingress | media-pipeline |
| `/data/incoming/uploading` | media-pipeline browser ingress | media-pipeline |
| `/data/processing/work` | media-pipeline | media-pipeline |
| `/data/processing/approved` | media-pipeline and Lidarr during controlled handoff | media-pipeline and Lidarr |
| `/data/library` | Lidarr | media-pipeline read-only and Navidrome read-only |
| `/data/library-unmanaged` | media-pipeline | Navidrome read-only |

Lidarr never receives a mount for `/data/library-unmanaged`. SFTPGo and the browser ingress cannot write processing or library paths. Navidrome cannot write either library.

## Components

### Browser upload service

The browser upload service owns upload sessions, expected file manifests, per-file progress, temporary names, retries, and finalization. It writes only below `/data/incoming/uploading` until a session is complete.

### Manual submission service

The existing submission service becomes the common boundary for browser and SFTPGo ingress. The downstream workflow receives a source, submission ID, source path, actor, provenance, and sealed tree fingerprint. It does not need upload-protocol knowledge.

### Unmanaged metadata service

This service extracts album and track tags, reports conflicts, applies administrator edits, selects artwork, and produces a deterministic metadata plan. It never guesses a conflicting value from a majority of tracks.

### Unmanaged import service

This service applies the approved tag plan without re-encoding audio, verifies the result, builds a final manifest, and atomically moves the whole release into `/data/library-unmanaged`.

### Lidarr catalog service

This service ensures that an exact MusicBrainz artist and target album exist in Lidarr before Manual Import preparation. Initial managed ingestion and later unmanaged migration use the same service. It discovers root-folder and profile IDs at runtime, disables automatic searching, and monitors only the target album.

### Migration check service

This service searches MusicBrainz when an administrator presses `Check now` or `Check selected`. It records evidence and candidate matches but cannot modify files or Lidarr.

### Migration service

This service runs only after explicit confirmation. It ensures the artist and target album exist in Lidarr, stages the unmanaged release, reuses the existing controlled Manual Import path, and reconciles the final files in Lidarr and Navidrome.

## Domain model and durable state

The current candidate lifecycle gains these states:

```text
UNMANAGED_REVIEW
UNMANAGED_READY
UNMANAGED_IMPORTING
UNMANAGED_IMPORTED
```

`UNMANAGED_IMPORTED` is terminal for the original ingestion candidate. Later catalog checks do not reopen that state machine. They use separate migration records so an imported candidate remains an accurate ingestion audit.

A migration batch owns selected release IDs and overall progress. Each item has its own durable state:

```text
CHECK_PENDING
CHECKING
NO_MATCH
AMBIGUOUS
EXACT_MATCH
CONFIRMED
LIDARR_CATALOG_READY
IMPORT_SUBMITTED
RECONCILING
MIGRATED
FAILED_RETRYABLE
```

A batch is an orchestration convenience, not a transaction. One item can fail without rolling back or blocking other items. Every migration item has its own intent, idempotency key, evidence, state revision, lease, and error history.

The pipeline database stores:

- browser upload sessions and expected file entries
- upload completion and retry state
- manual submission source and provenance
- approved unmanaged metadata and artwork evidence
- final unmanaged paths and checksums
- migration batches and items
- MusicBrainz request and response evidence
- Lidarr catalog and import intents
- final managed paths and reconciliation evidence

## Browser folder upload

The Admin UI accepts drag-and-drop directories. A folder picker using `webkitdirectory` is the fallback. The browser preserves each file's relative path through `File.webkitRelativePath` or directory entries from the drop event. If a browser cannot enumerate a directory, it can still select multiple files.

The upload protocol is intentionally small:

1. The browser creates a session with the relative path, size, and media type reported for each file.
2. It uploads files individually with a small concurrency limit.
3. The server streams each request to a `.partial` file and does not buffer a FLAC in memory.
4. On completion, the server verifies the byte count and renames the file inside the staging tree.
5. Retrying the same session and file entry replaces only its incomplete temporary file.
6. Finalization verifies the expected manifest, rejects unexpected or missing entries, and requires that no `.partial` file remains.
7. The server atomically renames the session directory to `/data/incoming/manual/<submission-id>`.
8. The submission appears on the existing Incoming page.

The server validates relative paths before opening a destination. It rejects absolute paths, traversal, empty segments, unsafe path aliases, symlinks, nested mounts, special files, and collisions after normalization. Admission checks run before session creation and while writing files.

Upload sessions survive a process restart. The Admin UI shows incomplete sessions and lets the administrator resume or delete them. Denyra does not add a background cleanup scheduler.

## SFTPGo ingestion

An SFTP upload uses one unique top-level directory per release. SFTPGo events update discovery promptly, while the recovery scanner covers missed events. Discovery does not authorize processing.

The same Incoming page previews browser and SFTP submissions. Pressing `Submit` seals the current tree fingerprint. If an SFTP client changes a sealed tree before claim, the existing `WAITING_RESUBMIT` behavior applies. The administrator must review and submit the new tree.

## Preview and submission

The preview reads metadata without moving or mutating source files. It displays:

- submission source and provenance
- track list and relative paths
- FLAC technical summary
- album-level and track-level tags
- conflicts and missing required fields
- MusicBrainz candidates when available
- selected artwork and its source
- intended destination mode

The administrator can edit metadata and replace artwork. One `Submit` action stores the decision and sealed fingerprint, then starts controlled processing. Technical validation still runs after claim. A changed or unsafe tree cannot rely on preview results.

## Identity decision

The matcher uses evidence in this order:

1. MusicBrainz IDs already present in tags
2. UPC or barcode
3. ISRC coverage across tracks
4. normalized album artist, album title, date, disc count, and track count
5. per-track positions, titles, artist credits, ISRCs, and durations

An exact candidate must satisfy the existing release-atomic matching and duration policies. A barcode or high search score alone is not enough. Conflicting identifiers prevent an exact decision.

The preview presents one of three results:

- `Managed by Lidarr` when one exact MusicBrainz release is available
- a candidate selection or unmanaged choice when results are ambiguous
- `Unmanaged` when no candidate survives validation

Denyra never chooses between ambiguous releases without administrator input.

## Initial managed import

Selecting `Managed by Lidarr` authorizes catalog preparation as part of Submit. Denyra finds the artist by MusicBrainz identity. If the artist is absent, the Lidarr catalog service adds it with the existing `/data/library` root and the defaults reported by Lidarr. It does not start an automatic search. It waits for Lidarr refresh, selects the exact internal album release by foreign release ID, and monitors only the target album.

After catalog preparation, the release continues through the current deterministic tag mutation, enrichment, Manual Import intent, submission, and reconciliation flow. This path covers an artist or album that exists in MusicBrainz but was not previously registered in Lidarr. Profile and root IDs are discovered at runtime and are never hard-coded.

## Unmanaged metadata

An unmanaged release requires these canonical fields:

- album artist
- album title
- track title
- track artist
- track number
- disc number

Track and disc totals are derived from the approved release plan. Date, genre, composer, publisher, UPC, ISRC, source URLs, and unknown Vorbis comments are preserved when valid. User edits are stored as evidence before mutation.

Tag mutation follows the current safety rules. It changes Vorbis comments only, preserves unknown comments and embedded pictures, does not touch audio frames, records pre-mutation and post-mutation checksums, and runs `flac -t` afterward.

## Artwork

Artwork selection uses this order:

1. embedded front cover
2. uploaded `cover` or `folder` image
3. a credential-free lookup from an explicit supported source URL in the FLAC tags
4. a UPC or ISRC result only when every identity check points to the same album
5. administrator upload or replacement

The first supported source URL adapter is Spotify metadata for an explicit Spotify track URL already present in a tag. Denyra does not perform a broad title-based image search or scrape arbitrary pages.

Every candidate image is decoded and checked for an allowed MIME type, bounded dimensions, and bounded byte size. An automatic result is visible in preview before submission. Lookup failure is non-blocking. The selected image is written as `cover.jpg`; existing embedded artwork remains in the FLAC.

## Unmanaged import

After approval, the pipeline creates a complete release under processing work and applies the metadata and artwork plans. It then computes the manifest, runs integrity checks, records an unmanaged import intent, and atomically renames the directory into `/data/library-unmanaged`.

The default layout is:

```text
Album Artist/
  Album (Year)/
    01 - Track title.flac
    01 - Track title.lrc
    cover.jpg
```

Multi-disc releases use a `Disc 01` directory. A missing year omits the parenthesized suffix. Path components use deterministic sanitization. A destination collision enters review and never overwrites an existing release. The review can change the edition label or cancel the import.

`UNMANAGED_IMPORTED` requires all expected files and checksums at the destination plus successful discovery through the Navidrome API. Staging cleanup happens only after that evidence exists.

## Navidrome libraries

Navidrome exposes `/data/library` as `Managed` and `/data/library-unmanaged` as `Unmanaged`. Both mounts are read-only. The deployment reconciler creates or updates the `Unmanaged` library during setup without requiring a manual Navidrome configuration step.

The library separation is visible to Navidrome and OpenSubsonic clients that support music-folder selection. Moving a release between libraries triggers a scan so stale unmanaged records disappear and the managed copy becomes visible.

## Batch catalog checks

The Unmanaged page has a checkbox per release, filter-aware `Select all`, `Check selected`, and a per-item result. One album is the smallest check unit. Tracks are evidence within that album and never require separate clicks.

Batch checks use bounded concurrency while the MusicBrainz adapter keeps its required request interval. Each item independently reports `No match`, `Ambiguous`, `Exact candidate`, or `Error`. A page refresh reads the durable batch and item states.

Checking is read-only. It may write evidence and state to the pipeline database, but it cannot change the release directory, mutate tags, add a Lidarr artist, or submit an import.

The migration review page displays match evidence and field differences. The administrator selects exact matches and presses `Confirm selected migrations`. Confirmation creates one independent migration intent per release. There is no unreviewed `migrate all` action.

## Confirmed migration

For each confirmed release, Denyra performs these steps:

1. Acquire the migration lease and verify the unmanaged manifest and fingerprint.
2. Resolve the MusicBrainz artist, release group, release, recordings, and track positions again.
3. Verify that the approved match remains exact.
4. Call the shared Lidarr catalog service used by initial managed ingestion.
5. Wait for Lidarr refresh completion and locate the internal album release whose foreign release ID equals the approved MusicBrainz release ID.
6. Atomically move the unmanaged release into approved staging.
7. Use the current Lidarr Manual Import preparation, intent, submission, and reconciliation flow.
8. Verify the final Lidarr track files, checksums, lyrics, and artwork.
9. Trigger a Navidrome scan and verify the release in `Managed` and absent from `Unmanaged`.
10. Mark the migration item `MIGRATED` and keep the previous unmanaged paths in audit history.

Adding an artist does not start a missing-album search. If later steps fail, Denyra leaves the catalog entry in Lidarr instead of trying to delete user-visible Lidarr state.

## Failure handling

Before Manual Import submission, a failure returns a staged directory to its original unmanaged path when the manifest still matches. A timeout or lost response after submission enters `RECONCILING`; Denyra does not send another Manual Import command until it has queried Lidarr command, history, queue, album, and track-file evidence.

A partial import remains in reconciliation. Denyra does not create another unmanaged copy or delete managed files to simulate rollback. Restart recovery resumes each item from its durable intent and state.

Retryable MusicBrainz, Lidarr, or Navidrome failures affect only the current migration item. A batch reports mixed results without discarding successful items. Permanent path, metadata, or identity conflicts return the item to review.

## Admin HTTP and UI boundaries

Browser upload uses the existing Admin UI listener and session model. It adds no host port. Upload endpoints have separate streaming limits from normal admin mutations. Authentication, CSRF, request IDs, state revisions, and audit actors remain mandatory.

The Admin UI adds these surfaces:

- upload session creation and progress on Incoming
- metadata and artwork preview before Submit
- unmanaged release list and filters
- batch check progress
- migration result comparison
- selected migration confirmation
- retry, resume, and recovery status

The production UI keeps its current local assets and has no frontend Node runtime. Folder selection uses small browser-native JavaScript embedded through the existing asset pipeline.

## Configuration, setup, and deployment

Defaults add an unmanaged library root and browser-upload staging root. Both must remain on the same filesystem as processing so release moves are atomic. Startup validation checks ownership, canonical paths, free space, and device identity.

`./denyra setup` remains idempotent. It creates the directories, reconciles container mounts, configures the Navidrome library, and preserves existing user configuration outside Denyra's owned fields. No extra installer, runtime binary, API credential, manual compile, or migration command is required.

The backup set includes the unmanaged library, incomplete upload sessions, selected artwork, pipeline database, and migration evidence. Restore verification checks both Navidrome libraries and resumes nonterminal upload and migration records. Update snapshots keep their existing purpose and do not replace disaster backups.

## Security and performance scope

The feature applies the existing private-host controls plus admin authentication, CSRF, canonical path checks, safe file creation, format validation, bounded image decoding, configurable upload limits, and free-space admission. It does not add antivirus scanning, public TLS termination, heavyweight isolation, or audio transcoding.

Files stream to disk. Browser upload uses a small per-session concurrency limit. MusicBrainz requests follow the configured rate interval. Migration may inspect several albums concurrently, but Lidarr Manual Import remains serialized. Hashing and `flac -t` run at boundaries where the result authorizes a state change.

## Testing strategy

### Unit tests

- browser manifest validation and safe relative paths
- upload retry and finalization idempotency
- metadata conflict detection and required-field validation
- deterministic unmanaged paths and collision handling
- artwork priority, decoding limits, and failed lookup fallback
- MusicBrainz query construction and exact-match rules
- migration and batch state transitions
- fingerprint and checksum drift detection

### Contract tests

- MusicBrainz responses for no match, ambiguous results, exact barcode match, exact ISRC match, and identifier conflicts
- Lidarr artist present and absent responses
- runtime profile and root-folder discovery
- Lidarr refresh completion and exact album-release discovery
- initial managed import when neither the artist nor album was previously registered in Lidarr
- Navidrome multi-library creation, scan, and release visibility

### Integration tests

- use generated real FLAC files and the production `ffprobe`, `flac`, and `metaflac` adapters
- prove browser and SFTP submissions produce the same downstream record
- prove tag edits do not re-encode audio
- prove unmanaged import, restart recovery, collision review, and atomic rollback
- prove embedded, sidecar, automatic, and uploaded artwork paths
- prove each migration intent causes at most one Lidarr Manual Import command
- prove partial imports enter reconciliation without duplicate files
- prove one batch item can fail while others complete

### Compose acceptance tests

The deterministic acceptance environment uses real media-pipeline behavior, SQLite, filesystem moves, media binaries, and released Lidarr and Navidrome containers. MusicBrainz responses use controlled fixtures so CI does not depend on public network availability.

The acceptance suite must prove:

1. A browser folder upload survives a dropped file request and resumes.
2. Preview and Submit work for browser and SFTPGo ingress.
3. A release with no MusicBrainz match imports into `Unmanaged` with artwork.
4. `Check selected` returns independent no-match, ambiguous, exact, and error results.
5. A check never writes to Lidarr or moves a release.
6. Confirmation adds a missing artist and target album without starting a search.
7. Manual Import is submitted once even when acknowledgement is lost.
8. Navidrome changes from `Unmanaged` to `Managed` after reconciliation.
9. Artwork and lyrics remain visible after migration.
10. A restart at every durable migration state resumes safely.
11. Backup and restore preserve unmanaged releases and pending operations.

An opt-in live compatibility smoke test performs read-only MusicBrainz queries and validates the current Lidarr and Navidrome schemas. It is not the source of deterministic CI success. Local acceptance also uses Chrome MCP to exercise drag-and-drop, preview, batch selection, confirmation, progress, and visible Navidrome results.

## Acceptance criteria

- Dropping a valid local album folder can produce an unmanaged Navidrome release without adding it to Lidarr.
- Uploading the same shape through SFTPGo reaches the same validation and review flow.
- Incomplete or conflicting required tags cannot bypass review.
- Artwork uses local evidence first, attempts a bounded automatic lookup, and remains replaceable before import.
- Lidarr cannot see or write the unmanaged library.
- Navidrome exposes separate `Managed` and `Unmanaged` libraries after one-command setup.
- `Check selected` operates per album, survives partial failures, and performs no external mutation.
- Migration requires confirmation and revalidates the exact match before changing state.
- A confirmed migration produces one managed release, removes the unmanaged visibility after verification, and does not duplicate Manual Import commands.
- CI covers no-match, ambiguous, exact, rollback, lost acknowledgement, partial import, restart, batch, artwork, and Navidrome visibility behavior.

## Compatibility references

- MusicBrainz release search fields: <https://musicbrainz.org/doc/Development/XML_Web_Service/Version_2/Search>
- Navidrome multiple libraries: <https://www.navidrome.org/docs/usage/features/multi-library/>
- Browser directory selection: <https://developer.mozilla.org/en-US/docs/Web/API/HTMLInputElement/webkitdirectory>
- Browser dropped directory entries: <https://developer.mozilla.org/en-US/docs/Web/API/DataTransferItem/webkitGetAsEntry>
