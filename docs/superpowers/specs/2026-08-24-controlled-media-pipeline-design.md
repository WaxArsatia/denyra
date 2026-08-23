# Controlled media pipeline design

## Purpose and authority

The media-pipeline is a Go service that owns validation workflow, quarantine, manual review, deterministic staging mutations, and Lidarr Manual Import orchestration. It never organizes the final library. Lidarr remains the only process allowed to move, rename, or import files into `/data/library`.

The service runs an HTTP server for the Admin Web UI, a separate internal API listener, and background workers. Handlers translate requests and call application services. Domain transitions and quality decisions do not live in HTTP handlers, templ components, SQLite repositories, or CLI adapters.

## Candidate ownership and identity

An immutable `candidate_id` is created by the acquisition source and remains unchanged through handoff. The gateway database owns the acquisition candidate and its source provenance. The pipeline database owns the validation and import candidate. State is exchanged through the internal API; databases are never shared or synchronized directly.

Manual submissions create their candidate ID in the pipeline because they do not pass through the gateway.

## Pipeline lifecycle

```text
RECEIVED
CLAIMED
STABILIZING
WORKING
TECHNICAL_VALIDATION
RELEASE_MATCHING
REVIEW_REQUIRED
ENRICHING
APPROVED
ARBITRATION_PENDING
IMPORT_READY
IMPORT_SUBMITTED
IMPORT_RECONCILING
IMPORTED

QUARANTINED
REJECTED
SUPERSEDED
CANCELLED
```

`APPROVED` means validation, deterministic mutation, final checksum, and post-mutation `flac -t` have all completed. Arbitration never receives a partially prepared candidate.

Every transition uses a SQLite transaction and an optimistic `state_revision`. Workers claim jobs with persisted leases. A filesystem lock protects the claimed release directory. Expired leases can be reclaimed only after reconciliation.

## Completion and atomic movement

Events and webhooks are the primary trigger. A scanner runs every `30s` to recover missed events. Pipeline workers may claim only paths with durable completion evidence.

Before claim, the source must report the release batch complete. The pipeline then compares the complete directory tree twice, `10s` apart. Every path, size, and mtime must remain unchanged. Once stable, the pipeline moves the release directory atomically into `/data/processing/work/<candidate-id>`.

If a candidate goes to manual review, its files remain under `/data/quarantine/<candidate-id>`. An approval with an explicit MusicBrainz Release ID moves the directory atomically back to processing work before any tag or enrichment mutation. The pipeline never mutates media in quarantine.

The pipeline has read/write access to raw download paths so it can perform these atomic moves. It may touch only the job path covered by a completion signal and successful lock.

## Technical validation

Each FLAC file passes these hard checks:

- regular file and safe canonical path
- expected FLAC container and codec from `ffprobe`
- readable channels, duration, sample rate, and bit depth where available
- successful `flac -t`
- complete and stable file size

Container or codec mismatch, corrupt stream, invalid structure, path violation, or unreadable media rejects the whole release and moves it to quarantine. A valid FLAC stream does not prove a genuine lossless source. Lossy-transcode detection is advisory. A future analyzer can implement the lossless heuristic interface without changing the quality state machine.

## Release matching

MusicBrainz is the canonical metadata source. Automatic matching requires all of these conditions:

- one explicit MusicBrainz Release ID
- all tracks map one-to-one to the same release
- exact release track and disc count
- complete disc and track positions
- no conflicting recording ID, release ID, release-track ID, or ISRC
- no unresolved extra audio file
- no missing or corrupt track
- every track and the release total satisfy the accepted duration policy

Official singles are releases with one track. Multi-disc, deluxe, and Various Artists releases are allowed when every track belongs to the same MusicBrainz release.

beets is an advisory adapter only for manual ingestion matching and enrichment. It cannot move, copy, rename, tag, write artwork, or access `/data/library`. Its output is evidence containing confidence, candidate IDs, and source metadata. The pipeline owns the state machine and decision.

### Duration policy

All comparisons use integer milliseconds against the MusicBrainz reference duration for each track.

Per track:

- auto-approve when the absolute difference is at most `max(5_000ms, 2%)`
- manual review above that limit and at most `max(15_000ms, 5%)`
- reject above the manual limit
- missing reference duration always requires review

Release total:

- evaluate only when every track has a MusicBrainz duration
- auto-approve at most `max(30_000ms, 1%)`
- manual review above that limit and at most `max(90_000ms, 3%)`
- reject above the manual limit

The release total cannot compensate for an individual track mismatch. It also cannot raise or lower an individual result. The release takes the strictest status across all tracks and the total check.

## Warnings and quality comparison

Warnings have two classes:

```text
QUALITY_WARNING
NON_BLOCKING_WARNING
```

Only `QUALITY_WARNING` affects dual-candidate ranking. Missing lyrics or artwork is a `NON_BLOCKING_WARNING` and cannot make an otherwise equivalent audio candidate lose.

Candidate comparison is lexicographic:

1. release and recording correctness
2. preferred edition or master match
3. absence of quality warnings
4. source and lossless confidence
5. bit depth
6. sample rate
7. provenance priority
8. acquisition completion time

Bit depth and sample rate matter only after identity and edition are equal. A higher sample rate cannot override a mismatch or quality warning.

## Deterministic tag mutation

Before mutation, the pipeline stores SHA-256, file manifest, technical metadata, and original tags. Unknown Vorbis comments are preserved. Managed fields are replaced from the approved MusicBrainz release. Embedded pictures are removed. Audio frames are never re-encoded.

Canonical fields:

```text
TITLE
ARTIST
ALBUM
ALBUMARTIST
TRACKNUMBER
TRACKTOTAL
DISCNUMBER
DISCTOTAL
DATE
GENRE
ISRC

MUSICBRAINZ_TRACKID
MUSICBRAINZ_RELEASETRACKID
MUSICBRAINZ_ALBUMID
MUSICBRAINZ_RELEASEGROUPID
MUSICBRAINZ_ARTISTID
MUSICBRAINZ_ALBUMARTISTID
```

`MUSICBRAINZ_TRACKID` stores the Recording MBID. `MUSICBRAINZ_RELEASETRACKID` stores the release-track MBID. This follows Picard's FLAC/Vorbis mapping.

Multi-value fields use repeated Vorbis entries. `ARTIST` and `MUSICBRAINZ_ARTISTID` preserve artist-credit order and alignment. `ALBUMARTIST` and its IDs follow album-credit order. `GENRE` and `ISRC` are normalized, deduplicated, and sorted deterministically.

Strings use Unicode NFC, trimmed edge whitespace, and no empty values. MBIDs use lowercase canonical UUID format. MusicBrainz date precision remains `YYYY`, `YYYY-MM`, or `YYYY-MM-DD`. Track and disc numbers are base-10 integers without zero padding.

After mutation, the pipeline records a final SHA-256 and reruns at least `flac -t`. Both checksums, command arguments, tool versions, mutation diff, and pre/post metadata stay in the audit database. Post-mutation failure quarantines the whole release.

## Artwork and lyrics

Artwork is not embedded in FLAC. Pipeline artwork is evidence only. Lidarr's native metadata consumer creates the final `folder.jpg`, preserving Lidarr-only ownership of the library.

Persistent lyrics use `.lrc` with the same basename as the source FLAC. Lidarr `Import Extra Files` moves and renames lyrics with the track during Manual Import. The pipeline does not write lyrics into the final library after import.

LRCLIB is the primary ingestion provider. Word-synchronized lyrics are preferred when available, followed by line-synchronized and plain lyrics. A missing provider or no-result response is non-blocking. Runtime Navidrome lyrics remains the fallback.

## Controlled import

An approved automated candidate reports its quality evidence to the gateway. The gateway either selects it as winner or keeps it in `ARBITRATION_PENDING`. A manual candidate does not need gateway arbitration.

The winner moves atomically to `/data/processing/approved/<candidate-id>`. The pipeline records an import intent before calling Lidarr Manual Import. The request includes the candidate identity, MusicBrainz release identity, and download ID when present.

On timeout or ambiguous response, the pipeline queries Lidarr history, queue, release files, and final paths before retrying. `IMPORTED` is reached only after the complete release and available `.lrc` files are confirmed through the Lidarr API and the read-only library mount. The database stores the final library paths and final checksums. Staging leftovers are removed only after verification.

## Manual ingestion

SFTPGo writes each upload under:

```text
/data/incoming/manual/<submission-id>/
```

File completion events only update discovery state. The Admin Web UI has an `/incoming` page where an admin explicitly submits a folder. Submit seals a tree fingerprint containing relative path, size, and mtime. The pipeline never silently updates a sealed fingerprint.

If any file changes before claim, the submission becomes `WAITING_RESUBMIT`. An admin must submit the new tree explicitly. After two stable scans `10s` apart, the folder moves atomically to processing work.

Path validation resolves the canonical root with no-follow semantics and enforces the expected filesystem device. It rejects symlinks, traversal, nested mounts, sockets, devices, and non-regular files, including race attempts. Non-FLAC audio is not transcoded and cannot enter automatic import.

Manual provenance includes uploader, SFTPGo event ID, submission ID, timestamps, and optional source note. SFTPGo accounts and media-pipeline admin accounts are separate.

## Admin Web UI

The Admin Web UI uses Go templ and locally vendored HTMX `2.0.9`. It has no CDN or frontend Node runtime.

Pages:

```text
/login
/incoming
/reviews
/reviews/{candidate-id}
/acquisitions/{job-id}
/audit
/account/password
/sessions
```

Review detail shows release and track results, duration comparisons, metadata before and after, artwork and lyrics status, provenance, checksums, job correlation, and state history.

Approve, Reject, and Retry forms include CSRF protection and expected `state_revision`. Approve requires a MusicBrainz Release ID and reason. Reject and Retry require a reason. A stale revision returns HTTP `409` and an updated HTMX fragment. All mutation handlers call the same application services used by internal workflows.

## Authentication and sessions

Users, roles, sessions, and audit actors are normalized for multiple administrators even though only the `admin` role exists initially.

Passwords use Argon2id with these parameters:

- memory `64 MiB`
- iterations `3`
- parallelism `2`
- salt `16 bytes`
- output `32 bytes`
- minimum password length `8`
- no composition rules

Bootstrap credentials are read only when the user table is empty. After the first administrator is created, the bootstrap login path is permanently ignored.

Session IDs are `32` random bytes from a CSPRNG. The database stores only SHA-256 session token hashes. Sessions expire after `30 days` and have no idle timeout. There is no login throttling or lockout. Authentication errors are generic.

Sessions rotate after login, password change, privilege change, and logout-all or explicit revocation. Approve, Reject, and Retry do not rotate the session. Password change revokes older sessions. The UI supports logout, logout-all, and individual revocation.

All mutation routes use CSRF protection. Authenticated HTML uses `Cache-Control: no-store`. Responses include a restrictive Content Security Policy, `X-Content-Type-Options`, `Referrer-Policy`, and frame denial. Because the UI uses HTTP, the session cookie is not `Secure`; this is an accepted risk recorded in the foundation design.

## Pipeline persistence

The pipeline SQLite database uses WAL, foreign keys, busy timeout, embedded migrations, and the SQLite online backup API. Active database files are never backed up through raw copy.

Core tables:

```text
users
sessions
submissions
candidates
candidate_files
validation_results
track_matches
metadata_snapshots
mutations
enrichments
import_intents
leases
audit_events
config_snapshots
build_provenance
```

Audit events are append-only. Actor, timestamp, decision, reason, target release ID, metadata snapshots, checksum, job ID, state revision, and state transition are written in the same transaction as a mutation decision.

## Error behavior

- Corrupt media, invalid container, unsafe path, or conflicting identity is rejected and quarantined.
- Ambiguity, missing duration, or tolerance review-band results require manual review.
- MusicBrainz, LRCLIB, and other network failures create retryable operational states.
- Lyrics or artwork no-result responses create non-blocking warnings.
- Disk or database failure stops new claims and preserves durable state.
- A post-mutation integrity failure quarantines the release with both evidence sets.
- No dependency outage deletes an active job or candidate.

