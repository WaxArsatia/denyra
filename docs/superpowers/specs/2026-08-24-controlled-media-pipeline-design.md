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

The Admin Web UI uses Go templ and locally vendored HTMX `2.0.10`. It has no CDN and no frontend Node runtime. Every stylesheet, script, font, and icon asset is embedded in the binary and served from the pipeline itself.

### Design read

The console is an operator instrument for a single technical administrator running their own deployment. Its job is to make a validation decision fast and correctly, with the evidence for that decision visible at once. It is not a marketing surface, and its visual language is deliberately dense rather than airy.

Landing-page and portfolio conventions do not apply here and must not be introduced later. There is no hero composition, no section-layout rotation requirement, no bento grid, no marquee, no logo wall, no stock photography, and no requirement that a screen carry an image. The general guidance against long lists and dense tables is also set aside with cause: a review queue and an append-only audit log are comparison surfaces, and folding them into cards would hide the scanning the operator came for.

The conventions that do carry over are recorded below as requirements: consistency locks, complete interactive state coverage, form structure, contrast floors, reduced motion, dark mode as a first-class target, and a documented layer scale.

### Design dials

The general-purpose baseline of moderate variance and moderate motion does not apply to an operator console. The console uses:

| Dial | Value | Consequence |
| --- | --- | --- |
| `DESIGN_VARIANCE` | `3` | Symmetric grid. Column positions stay identical across routes so the eye learns one layout. |
| `MOTION_INTENSITY` | `2` | State transitions only. No entrance choreography, parallax, or scroll-driven effects. |
| `VISUAL_DENSITY` | `8` | Hairline dividers instead of card containers. Monospace numerics. Compact rows. |

### Design tokens

Tokens are CSS custom properties defined once on `:root` and redefined under `@media (prefers-color-scheme: dark)`. Components reference tokens only. A component never hard-codes a color, radius, or spacing value.

Color is split into two roles. Interactive affordance is neutral and carries no hue, so that hue is reserved entirely for meaning. This is the console's answer to the single-accent rule: one accent for focus and selection, plus a bounded semantic ramp that exists because pipeline state is the primary information on every page.

| Token | Light | Dark | Role |
| --- | --- | --- | --- |
| `--surface` | `#fafafa` | `#0b0b0e` | Page ground |
| `--surface-sunken` | `#f4f4f5` | `#131316` | Table header, code blocks, inert regions |
| `--line` | `#e4e4e7` | `#27272a` | Hairline dividers, table rules |
| `--line-strong` | `#d4d4d8` | `#3f3f46` | Section boundaries |
| `--control-border` | `#7a7a83` | `#6e6e78` | Input, select, and button borders |
| `--ink` | `#18181b` | `#e4e4e7` | Primary text, primary button fill |
| `--ink-muted` | `#52525b` | `#a1a1aa` | Labels, secondary text |
| `--ink-faint` | `#6b6b73` | `#8a8a93` | Timestamps, disabled text |
| `--accent` | `#1d4ed8` | `#60a5fa` | Focus ring, active navigation, links |
| `--state-review` | `#b45309` | `#fbbf24` | `REVIEW_REQUIRED`, `WAITING_RESUBMIT`, review-band tolerance |
| `--state-blocked` | `#b91c1c` | `#f87171` | `QUARANTINED`, `REJECTED`, failed checks, retryable error |
| `--state-settled` | `#15803d` | `#4ade80` | `APPROVED`, `IMPORTED`, passed checks |

Neither mode uses pure black or pure white. No token carries the same value in both modes: one value cannot hold its contrast floor against both a near-white and a near-black ground. Every foreground token is validated against both `--surface` and `--surface-sunken` in both modes, because status text also appears inside sunken regions such as table headers.

Line tokens split by obligation. `--line` and `--line-strong` are decorative separators and carry no contrast floor, so they can stay quiet enough to organise a dense table without adding noise. `--control-border` bounds an interactive control and therefore takes the `3:1` floor for user interface components, which is why it is markedly darker in light mode than the separators it sits beside. Reusing a separator value as a control border is the mistake this split exists to prevent.

`scripts/verify-tokens/` parses this table and asserts every floor in both modes. It is part of the build checks, so a token edit that breaks a floor fails the build rather than review. In-flight states such as `STABILIZING`, `WORKING`, and `IMPORT_SUBMITTED` carry no hue; they read as `--ink-muted` with a monospace elapsed-time value. Terminal non-failure states such as `SUPERSEDED` and `CANCELLED` read as `--ink-faint`. Color is never the only carrier of a state: every status chip prints its state name as text.

Shape uses one radius for every element, including status chips, inputs, buttons, and swapped fragments:

```text
--radius: 4px
```

Spacing uses a `4px` base with a fixed step set of `4`, `8`, `12`, `16`, `24`, and `32`. Table rows are `32px` tall. Section separation uses `24px` and a hairline, not a card boundary and a shadow. The console defines no elevation shadows; grouping is expressed with `--line` and whitespace.

The layer scale is fixed and lives in a single constants file. No component invents a value outside it.

```text
0   document flow
10  sticky table header
20  application shell
30  inline confirmation
40  transient notification region
```

### Typography

The console ships two self-hosted typefaces, both from the Geist `1.7.2` archive pinned in the foundation design:

- Geist for interface text, in Regular, Medium, and SemiBold
- Geist Mono in Regular for identifiers, hashes, durations, byte counts, timestamps, and every other numeric or machine-generated value

These are static `woff2` faces rather than variable ones. The reason is recorded with the pin: the upstream release ships variable faces only as `.ttf`, and the console uses three weights, so a conversion step would add a build dependency that buys nothing.

Four faces are embedded in the binary. Only Geist Regular and Geist Mono Regular are preloaded, because the two heavier weights appear below the fold on every route. `font-display: swap` is set, and each face declares a metric-compatible system fallback with `size-adjust`, `ascent-override`, and `descent-override` so the swap does not shift layout.

Type scale:

| Role | Size | Weight | Notes |
| --- | --- | --- | --- |
| Page title | `20px` | `600` | One per route |
| Section heading | `15px` | `600` | Sentence case |
| Body and table cell | `13px` | `400` | Line height `1.5` |
| Label and column header | `12px` | `500` | Sentence case, not uppercase tracking |
| Monospace value | `12.5px` | `400` | Tabular figures, `font-variant-numeric: tabular-nums` |

Column headers and field labels use sentence case rather than the uppercase wide-tracking label style. At this density that style costs horizontal room and adds no information.

### Icons

Icons come from Phosphor Core `2.0.8`, regular weight only, pinned in the foundation design. The build compiles the referenced glyphs from the tarball's `assets/regular` sources into one `<symbol>` sprite. The full regular set is over twelve hundred glyphs, so the sprite is restricted to the glyphs the console references and the build fails on a reference to a glyph outside that set. Icons are referenced as `<svg><use href="/static/icons-<hash>.svg#<name>"></use></svg>`.

No icon path is hand-drawn. No second icon family is introduced. Stroke weight is uniform across the console. Every icon that is not purely decorative carries an accessible name; decorative icons carry `aria-hidden="true"` and are paired with a text label.

### Application shell

The shell is a `48px` top bar and a `200px` left navigation rail. Below `768px` the rail collapses into a disclosure list under the top bar and all multi-column layouts become single column.

The top bar holds the deployment name, the effective configuration snapshot hash in monospace, the readiness indicator, and the account menu. The readiness indicator reflects `/health/ready` and renders `--state-review` with the degraded dependency named when a dependency is degraded. The navigation rail lists `/incoming`, `/reviews`, `/audit`, and `/sessions`, with the active route marked by `--accent` and an accessible current-page state, not by color alone.

Content is contained at `max-width: 1400px`. A skip link precedes the shell and moves focus to the main region.

### Density validation

The token set and the review detail composition were validated in a static prototype rendered with the pinned Geist faces before any templ work began, because a density target of `8` is a claim about legibility that a table cannot settle on its own.

Two findings came out of it and are already reflected above. Separator lines and control borders needed different tokens, since a value quiet enough to rule a dense table is far too quiet to bound an input. Sticky table headers and the sticky decision panel needed explicit offsets against the `48px` top bar, or the first data row hides beneath the header on scroll.

The prototype is a reference for implementation, not an artifact the deployment ships.

### Route composition

| Route | Layout | Primary affordance | Empty state |
| --- | --- | --- | --- |
| `/login` | Single centered column, `max-width: 360px`, no shell | Sign in | Not applicable |
| `/incoming` | Full-width table of submissions with fingerprint status | Submit folder | States that SFTPGo uploads appear here and names the upload path |
| `/reviews` | Full-width queue table, newest first, filterable by state | Open review | States that releases needing a decision appear here |
| `/reviews/{candidate-id}` | Two columns at `lg`: evidence left, decision right, decision panel sticky | Approve, Reject, Retry | Not applicable |
| `/acquisitions/{job-id}` | Single column: job header, state timeline, attempt table, candidate table | Cancel job | Not applicable |
| `/audit` | Full-width append-only table with cursor pagination and filters | Filter | States that decisions and mutations are recorded here |
| `/account/password` | Single column, `max-width: 480px` | Change password | Not applicable |
| `/sessions` | Single column table of active sessions, current session marked | Revoke, revoke all others | Not applicable |

Review detail keeps its full evidence set: release and track results, duration comparisons in tabular monospace with the delta and its tolerance band, metadata before and after, artwork and lyrics status, provenance, checksums, job correlation, and state history. The evidence column is scrollable. The decision panel stays in view so a decision never requires scrolling back.

Metadata before and after renders as a two-column diff keyed on field name. Unchanged fields are collapsed behind a disclosure by default so the changed set is what the operator reads first. Removed embedded pictures and preserved unknown Vorbis comments are both reported explicitly.

Duration comparison prints the reference duration, the observed duration, the signed difference, and the band the difference fell into. The band is named in text, colored by the semantic ramp, and never communicated by color alone.

### Component states

Every list, fragment, and form defines four states. A screen that renders only its populated success state is incomplete.

**Loading.** HTMX requests use `hx-indicator` targeting the region being replaced. The indicator appears only after `150ms` so short requests do not flash. Loading placeholders are skeleton rows matching the final row height and column widths. The console uses no circular spinner.

**Empty.** Each list route composes an empty state that names what populates the list and links to the action or upstream surface that does so. An empty audit log and an empty review queue are normal operating conditions, not errors, and are styled as neutral text rather than warnings.

**Error.** A failed mutation renders its error inline, inside the swapped fragment, next to the control that failed. It never appears as a transient notification, because a transient notification can be missed and a failed approval must not be missed. The transient notification region is reserved for successful non-navigational actions such as a revoked session.

**Stale revision.** A stale `state_revision` returns HTTP `409` with an updated HTMX fragment. The fragment renders a persistent banner above the refreshed data stating that the candidate changed, naming the current state and revision, and re-rendering the form against the new revision. The action is never replayed automatically. This is the console's most important error path and is specified as a first-class state rather than a generic failure.

**Degraded.** When readiness reports a degraded dependency, affected routes render a banner naming the dependency and the consequence, for example that MusicBrainz matching is retrying. Degraded state does not disable navigation or hide existing evidence.

### Forms and decisions

Field structure is fixed: label above the input, optional helper text present in markup, error text below the input, `8px` between the parts of one field block. Placeholder text is never used as a label.

Approve, Reject, and Retry carry CSRF protection and the expected `state_revision`. Approve requires a MusicBrainz Release ID and a reason. Reject and Retry require a reason. The MusicBrainz Release ID field validates the canonical lowercase UUID form on the client through a native `pattern` attribute and again on the server. Client validation is a convenience; the server decision is authoritative.

Reject and Approve are consequential and use a two-step inline confirmation rendered into the same fragment. The console does not use browser `confirm()` dialogs and does not use modal overlays for confirmation. The confirmation step restates the release identity and the reason that will be recorded.

Primary buttons fill with `--ink` and print their label in `--surface`. Secondary buttons are outlined in `--control-border` with `--ink` text. Destructive-adjacent buttons keep the neutral fill and are distinguished by label and confirmation step, not by a red fill, so that red remains reserved for state. Every button, input, placeholder, helper text, error text, and focus ring meets WCAG AA contrast against its own background in both modes, and the control borders that bound them meet the `3:1` component floor. Both are asserted by the token verifier rather than checked by eye. Button labels are at most three words and never wrap at desktop widths.

Buttons show a pressed state with `translateY(1px)`. Disabled controls are never the only feedback for an in-flight request; the region indicator carries that.

### Motion

Motion is limited to state feedback. Interactive transitions run at `120ms` on `transform` and `opacity` only. HTMX swap classes fade at `100ms`. There are no infinite loops, no parallax, no scroll-driven animation, and no view transitions.

All of it is gated behind `@media (prefers-reduced-motion: no-preference)`, with a `reduce` block that removes transition duration. Under reduced motion the console is fully static and fully usable.

### Accessibility

The console targets WCAG AA and treats keyboard operation as the primary input path for repetitive review work.

HTMX replaces regions without a page load, which is silent to assistive technology by default. The console therefore maintains a polite `aria-live` region that announces the outcome of each swap, including decision results, stale-revision conflicts, and validation failures. Swapped regions preserve stable element IDs, and the heading of a replaced region carries `tabindex="-1"` and receives focus after the swap so keyboard position is never lost.

The console adds no custom key handling. Native focus order, native form semantics, and native table semantics are the contract. Tables use real `<table>` markup with `<th scope>` and a caption. Status is conveyed by text plus color, never color alone. Focus is always visible, using a `2px` `--accent` outline with a `2px` offset that is never suppressed.

### Theming

The console has one theme per render and follows `prefers-color-scheme`. There is no theme toggle: a toggle requires a cookie and server-side class resolution and buys an operator console nothing. No route, section, or fragment inverts against the resolved theme.

Both modes are developed and reviewed together. A change that is verified in one mode only is not complete.

### Content Security Policy compatibility

The restrictive Content Security Policy is a design constraint, not only a header. It forbids inline styles and inline scripts, so the console has no `<style>` blocks, no `style` attributes, and no `<script>` blocks. All presentation lives in the embedded stylesheet and all behavior lives in vendored HTMX, configured declaratively through a `htmx-config` meta tag.

HTMX defaults are not CSP-safe and must be overridden. `htmx.config.includeIndicatorStyles` defaults to `true` and injects a `<style>` element into the document head at load, which `style-src 'self'` rejects. It is set to `false`, and the `.htmx-indicator` rules are authored in the embedded stylesheet instead. `htmx.config.allowEval` defaults to `true` and is set to `false`.

Both values are supplied through the `htmx-config` meta tag rather than a script, so configuration itself introduces no executable code. The console does not use `hx-on`, `js:` expressions, evaluated `hx-vals`, or evaluated trigger filters.

Two build checks enforce this. One scans templ sources for inline `style` attributes, `<style>` and `<script>` blocks, and evaluated HTMX attributes. The other asserts the rendered `htmx-config` values, because a violation originating in library defaults is invisible to a source scan.

Applied policy:

```text
default-src 'self';
script-src 'self';
style-src 'self';
img-src 'self';
font-src 'self';
connect-src 'self';
form-action 'self';
frame-ancestors 'none';
base-uri 'none';
object-src 'none'
```

### Asset delivery

Static assets are served from content-hashed paths of the form `/static/<name>-<hash>.<ext>` with `Cache-Control: public, max-age=31536000, immutable`. This is distinct from authenticated HTML, which stays `Cache-Control: no-store`. The hash is derived from the embedded asset at build time, so a redeploy invalidates changed assets without a manual cache decision.

The stylesheet, the HTMX bundle, the icon sprite, and the four font faces are the complete static asset set. There is no runtime asset fetch from any other origin.

Measured sizes of the pinned artifacts, before transport compression:

| Asset | Size |
| --- | --- |
| `Geist-Regular.woff2` | `45244 bytes` |
| `Geist-Medium.woff2` | `46372 bytes` |
| `Geist-SemiBold.woff2` | `46596 bytes` |
| `GeistMono-Regular.woff2` | `50356 bytes` |
| `htmx.min.js` `2.0.10` | `51238 bytes` |

The four faces total roughly `188 KiB`. Only Geist Regular and Geist Mono Regular are preloaded, which puts about `93 KiB` of font on the critical path. That is acceptable for a console reached over a LAN and is the reason the faces are not subset further: subsetting would add a build dependency and a risk of dropping glyphs from international artist and release names, which this console displays constantly.

`woff2` is already compressed, so the server does not attempt to compress font responses again. The stylesheet, the icon sprite, and the HTMX bundle are served compressed.

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

All mutation routes use CSRF protection. Authenticated HTML uses `Cache-Control: no-store`; content-hashed static assets are cached separately as described in the Admin Web UI asset delivery section. Responses include the restrictive Content Security Policy defined in that section, plus `X-Content-Type-Options`, `Referrer-Policy`, and frame denial. Because the UI uses HTTP, the session cookie is not `Secure`; this is an accepted risk recorded in the foundation design.

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
