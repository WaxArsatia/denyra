# Unmanaged FLAC Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add restart-safe browser and SFTP album ingestion, a Denyra-owned unmanaged Navidrome library, manual batch MusicBrainz checks, and confirmed migration into Lidarr.

**Architecture:** Keep all behavior inside `media-pipeline` and its existing SQLite database. Browser uploads and SFTP discoveries converge at the existing manual-submission boundary; exact MusicBrainz matches use a shared Lidarr catalog-preparation service, while unmatched releases use a separate atomic import path under `/data/library-unmanaged`. Later catalog checks are durable, read-only batches, and migration changes external state only after a second explicit confirmation.

**Tech Stack:** Go 1.27, SQLite, `net/http`, templ, browser-native JavaScript, ffprobe/flac/metaflac, MusicBrainz WS/2, Lidarr API v1, Navidrome native and OpenSubsonic APIs, Docker Compose, Restic.

**Spec:** `docs/superpowers/specs/2026-08-24-unmanaged-flac-ingestion-design.md`

## Global Constraints

- Add no service, database, public port, frontend Node runtime, scheduled catalog check, antivirus engine, transcoder, or general chunk-upload framework.
- Lidarr remains the only writer of `/data/library` and never receives `/data/library-unmanaged`.
- `media-pipeline` is the only writer of `/data/library-unmanaged`; Navidrome mounts both libraries read-only.
- Browser files stream to disk as one request per file; no complete FLAC is buffered in memory.
- MusicBrainz checks are release-atomic and read-only. Ambiguous results never migrate automatically.
- MusicBrainz requests keep the configured interval, and Lidarr Manual Import remains serialized at concurrency 1.
- Migration requires explicit confirmation after checking and revalidates the exact release before any external mutation.
- No new runtime compiler, Python build, host-side Go/Node dependency, or manually installed service is allowed.
- Continue using released container images and existing broad development-compatible version ranges; do not introduce digest locks or narrow dependency pins.
- Deterministic CI uses controlled HTTP fixtures. Public MusicBrainz, current Lidarr, and current Navidrome are covered only by an opt-in compatibility smoke test.
- Use TDD, retain immutable evidence, keep file moves same-device and atomic, and commit after every independently testable task.

## File structure

New files are grouped by responsibility:

- `internal/pipeline/domain/upload.go`: upload manifest, session, and safe relative-path rules.
- `internal/pipeline/domain/unmanaged.go`: approved metadata, deterministic layout, artwork reference, and unmanaged states.
- `internal/pipeline/domain/migration.go`: batch/item state machines and exact-match confirmation rules.
- `internal/pipeline/application/uploads.go`: restart-safe per-file upload orchestration.
- `internal/pipeline/application/preview.go`: read-only submission inspection plus persisted decision drafts.
- `internal/pipeline/application/identity.go`: MusicBrainz candidate collection and exact/ambiguous/no-match decision.
- `internal/pipeline/application/artwork.go`: local-first artwork selection and bounded validation.
- `internal/pipeline/application/unmanaged.go`: metadata mutation plan and atomic unmanaged import.
- `internal/pipeline/application/catalog.go`: shared Lidarr catalog preparation.
- `internal/pipeline/application/migration_check.go`: durable read-only batch checks.
- `internal/pipeline/application/migration.go`: confirmed migration and reconciliation.
- `internal/pipeline/application/migration_runtime.go`: button-triggered queues and restart recovery, without a scheduler.
- `internal/pipeline/persistence/uploads.go`, `unmanaged.go`, and `migrations.go`: focused SQLite repositories.
- `internal/pipeline/adapters/filesystem/upload.go`: no-follow streaming writes and finalization.
- `internal/pipeline/adapters/media/artwork.go`: embedded/sidecar extraction and JPEG normalization.
- `internal/pipeline/adapters/spotify/oembed.go`: explicit Spotify URL artwork lookup only.
- `internal/pipeline/adapters/navidrome/client.go`: library setup, scans, and library-scoped visibility checks.
- `internal/pipeline/adapters/lidarr/catalog.go`: runtime root/profile discovery and artist/album registration.
- `internal/pipeline/adminui/assets/upload.js`: directory enumeration, bounded parallel upload, retry, and progress.
- `internal/pipeline/adminui/views/incoming_detail.templ`, `unmanaged.templ`, and `migration_detail.templ`: server-rendered admin surfaces.

Existing large workflow and routing files remain orchestration points. Do not move unrelated code or redesign the current packages.

---

### Task 1: Filesystem, configuration, mounts, and defaults

**Files:**
- Modify: `internal/config/types.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/validate.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/platform/fscheck/layout.go`
- Modify: `internal/platform/fscheck/layout_test.go`
- Modify: `cmd/media-pipeline/main.go`
- Modify: `deploy/config/pipeline.toml`
- Modify: `deploy/config/navidrome.toml`
- Modify: `deploy/compose.yaml`
- Modify: `deploy/compose.local.yaml`
- Modify: `deploy/compose.acceptance.yaml`
- Modify: `scripts/bootstrap-data-layout.sh`
- Modify: `tests/integration/filesystem_layout_test.go`
- Modify: `tests/integration/compose_config_test.go`

**Interfaces:**
- Consumes: existing default-overlay configuration and `fscheck.Check` startup gate.
- Produces: `Filesystem.IncomingUploading`, `Filesystem.LibraryUnmanaged`, `Services.NavidromeURL`, `Services.SpotifyOEmbedURL`, `Services.CoverArtURL`, `Secrets.NavidromeAdmin`, `Concurrency.MigrationCheck`, and `Uploads UploadConfig`.

- [ ] **Step 1: Write failing configuration and Compose tests**

Add assertions equivalent to:

```go
func TestDefaultsIncludeUnmanagedUploadPolicy(t *testing.T) {
	got := config.Defaults()
	if got.Filesystem.IncomingUploading != "/data/incoming/uploading" || got.Filesystem.LibraryUnmanaged != "/data/library-unmanaged" {
		t.Fatalf("unexpected roots: %+v", got.Filesystem)
	}
	if got.Uploads.MaxFileBytes <= 0 || got.Uploads.MaxSessionBytes < got.Uploads.MaxFileBytes || got.Uploads.MaxEntries <= 0 || got.Uploads.BrowserConcurrency != 3 || got.Concurrency.MigrationCheck != 3 {
		t.Fatalf("invalid upload defaults: %+v", got.Uploads)
	}
}
```

Extend Compose assertions so:

```go
required := []string{"/data/incoming/uploading", "/data/library-unmanaged", "/music-managed", "/music-unmanaged"}
for _, value := range required {
	if !strings.Contains(renderedCompose, value) { t.Errorf("missing %s", value) }
}
if strings.Contains(lidarrService, "library-unmanaged") { t.Fatal("Lidarr can see unmanaged library") }
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/config ./internal/platform/fscheck ./tests/integration -run 'DefaultsIncludeUnmanaged|FilesystemLayout|ComposeConfig' -count=1`

Expected: FAIL because the new fields, paths, and mounts do not exist.

- [ ] **Step 3: Add minimal configuration and deployment wiring**

Add:

```go
type UploadConfig struct {
	MaxFileBytes       int64 `toml:"max_file_bytes" json:"max_file_bytes"`
	MaxSessionBytes    int64 `toml:"max_session_bytes" json:"max_session_bytes"`
	MaxEntries         int   `toml:"max_entries" json:"max_entries"`
	BrowserConcurrency int   `toml:"browser_concurrency" json:"browser_concurrency"`
	ImageMaxBytes      int64 `toml:"image_max_bytes" json:"image_max_bytes"`
	ImageMaxPixels     int64 `toml:"image_max_pixels" json:"image_max_pixels"`
}
```

Use defaults of 8 GiB per file, 100 GiB per session, 1,000 entries, 3 browser requests, 3 migration-check workers, 20 MiB per image, and 40 million decoded pixels. Validate positive values and `MaxSessionBytes >= MaxFileBytes`. Default `CoverArtURL` to `https://coverartarchive.org` and keep it overrideable for deterministic tests. Add the two filesystem roots to same-device startup validation. Mount managed music at `/music-managed`, unmanaged music at `/music-unmanaged`, keep both Navidrome mounts read-only, keep the pipeline's managed mount read-only, and give the pipeline its existing `navidrome_admin` secret plus `denyra-playback` network access. Set Navidrome's default `MusicFolder` to `/music-managed`.

- [ ] **Step 4: Run focused tests and Compose validation**

Run: `rtk go test ./internal/config ./internal/platform/fscheck ./tests/integration -run 'DefaultsIncludeUnmanaged|FilesystemLayout|ComposeConfig' -count=1 && rtk docker compose -f deploy/compose.yaml config --quiet`

Expected: PASS; rendered Compose exposes no additional host port and Lidarr has no unmanaged mount.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/config internal/platform/fscheck cmd/media-pipeline/main.go deploy scripts/bootstrap-data-layout.sh tests/integration/filesystem_layout_test.go tests/integration/compose_config_test.go
rtk git commit -m "feat(deploy): add unmanaged media roots"
```

### Task 2: Navidrome multi-library reconciliation and client

**Files:**
- Create: `internal/pipeline/adapters/navidrome/client.go`
- Create: `internal/pipeline/adapters/navidrome/client_test.go`
- Modify: `internal/reconcile/navidrome.go`
- Modify: `internal/reconcile/navidrome_test.go`
- Modify: `internal/reconcile/reconcile.go`
- Modify: `cmd/denyra-reconcile/main.go`
- Modify: `tests/integration/operations/navidrome_config_test.go`
- Modify: `tests/integration/operations/navidrome_discovery_test.go`

**Interfaces:**
- Consumes: Navidrome admin username/password and the mounted `/music-managed` and `/music-unmanaged` paths.
- Produces: `navidrome.Client.EnsureLibraries`, `StartScan`, `WaitScan`, and `ReleaseVisible`.

- [ ] **Step 1: Write failing adapter and reconciler tests**

Use an `httptest.Server` that requires this sequence:

```text
POST /auth/login
GET  /api/library/
PUT  /api/library/1
POST /api/library/
GET  /rest/getMusicFolders.view
GET  /rest/startScan.view?target=2%3A
GET  /rest/getScanStatus.view
GET  /rest/search3.view?musicFolderId=2&query=OFF+GUARD
```

Assert every native `/api` request carries `Authorization: Bearer fixture-token`. Assert every `/rest` request carries `u=admin`, the `t`/`s` values returned by login, `v=1.16.1`, `c=denyra`, and `f=json`, with no plaintext `p` parameter. Library 1 becomes `{name:"Managed",path:"/music-managed"}`, library 2 becomes `{name:"Unmanaged",path:"/music-unmanaged"}`, and a second reconcile sends no PUT or POST.

- [ ] **Step 2: Run tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adapters/navidrome ./internal/reconcile ./tests/integration/operations -run Navidrome -count=1`

Expected: FAIL because the adapter and library reconciliation are absent.

- [ ] **Step 3: Implement the current supported HTTP flow**

Define:

```go
type Library struct {
	ID int `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	DefaultNewUsers bool `json:"defaultNewUsers"`
}
type ReleaseIdentity struct { AlbumArtist, Album string; TrackCount int }
type Client struct { BaseURL, Username, Password string; HTTP *http.Client; ResponseLimit int64 }

func (c Client) EnsureLibraries(ctx context.Context) (managedID, unmanagedID int, changed bool, err error)
func (c Client) StartScan(ctx context.Context, libraryIDs ...int) error
func (c Client) WaitScan(ctx context.Context, poll time.Duration) error
func (c Client) ReleaseVisible(ctx context.Context, libraryID int, identity ReleaseIdentity) (bool, error)
```

Authenticate through `POST /auth/login`. Store its JWT plus `subsonicToken` and `subsonicSalt`; preserve refreshed JWTs returned in `Authorization`. Use the JWT for native `/api/library/` CRUD. Use token-and-salt OpenSubsonic authentication, never the plaintext password parameter, for `startScan`, `getScanStatus`, and library-scoped `search3`. Bound all bodies. Never write Navidrome's SQLite database. `reconcile.Navidrome.Apply` creates the admin when needed, logs in, and idempotently ensures both libraries.

- [ ] **Step 4: Run tests**

Run: `rtk go test ./internal/pipeline/adapters/navidrome ./internal/reconcile ./tests/integration/operations -run Navidrome -count=1`

Expected: PASS, including idempotent second reconciliation.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/adapters/navidrome internal/reconcile cmd/denyra-reconcile tests/integration/operations
rtk git commit -m "feat(navidrome): reconcile managed and unmanaged libraries"
```

### Task 3: Durable upload sessions and safe streaming writes

**Files:**
- Create: `migrations/pipeline/000009_upload_sessions.sql`
- Create: `internal/pipeline/domain/upload.go`
- Create: `internal/pipeline/domain/upload_test.go`
- Create: `internal/pipeline/adapters/filesystem/upload.go`
- Create: `internal/pipeline/adapters/filesystem/upload_test.go`
- Create: `internal/pipeline/application/uploads.go`
- Create: `internal/pipeline/application/uploads_test.go`
- Create: `internal/pipeline/persistence/uploads.go`
- Modify: `migrations/embed_test.go`
- Modify: `tests/integration/pipeline/persistence_test.go`

**Interfaces:**
- Consumes: `UploadConfig`, `/data/incoming/uploading`, `/data/incoming/manual`, current clock/ID helpers, and `DiscoveryStore.DiscoverSubmission`.
- Produces: `UploadService.Create`, `PutFile`, `Finalize`, `Delete`, `Session`, and `Sessions`.

- [ ] **Step 1: Write failing domain, filesystem, and persistence tests**

Cover traversal, absolute paths, empty segments, normalized collisions, total limits, retry, restart, and finalize idempotency:

```go
manifest := domain.UploadManifest{Files: []domain.UploadFileSpec{{RelativePath: "OFF GUARD/01 - Track.flac", SizeBytes: 12, MediaType: "audio/flac"}}}
session, err := service.Create(ctx, "admin-1", manifest)
if err != nil { t.Fatal(err) }
if _, err := service.PutFile(ctx, "admin-1", session.ID, session.Files[0].ID, strings.NewReader("partial")); err == nil { t.Fatal("short upload accepted") }
if _, err := os.Stat(filepath.Join(uploading, session.ID, "OFF GUARD/01 - Track.flac.partial")); err != nil { t.Fatal(err) }
```

Recreate the service against the same SQLite database and root, retry with 12 bytes, finalize twice, and assert exactly one browser submission under `incoming/manual`.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/domain ./internal/pipeline/adapters/filesystem ./internal/pipeline/application ./tests/integration/pipeline -run 'Upload|PersistenceUpload' -count=1`

Expected: FAIL because upload types, migration, and services are missing.

- [ ] **Step 3: Implement migration, domain rules, and streaming service**

The migration creates `upload_sessions`, `upload_entries`, and `submission_previews`; it also adds `ingress`, `provenance_json`, `preview_fingerprint`, and `decision_json` to `submissions`. Use these public types:

```go
type UploadFileSpec struct { ID, RelativePath, MediaType string; SizeBytes int64 }
type UploadManifest struct { Files []UploadFileSpec }
type UploadSession struct { ID, Actor, Status string; Revision uint64; Files []UploadFileSpec; CreatedAt, UpdatedAt time.Time }

type UploadService struct {
	Store UploadStore
	Writer UploadWriter
	UploadingRoot, IncomingRoot string
	Policy config.UploadConfig
	Now func() time.Time
}
```

Generate opaque entry IDs server-side. `PutFile` streams through an exact byte-count limiter into `<path>.partial`, fsyncs, verifies size, and renames only that file. Retrying truncates only `.partial`; a completed identical entry is idempotent. `Finalize` verifies the durable expected manifest, rejects missing/unexpected entries and any `.partial`, scans the safe tree, atomically renames the whole session to `incoming/manual/<submission-id>`, and records one browser submission with actor and manifest provenance. `Delete` accepts only non-finalized sessions and removes only the resolved session directory after checking it is beneath `IncomingUploading`.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./migrations ./internal/pipeline/domain ./internal/pipeline/adapters/filesystem ./internal/pipeline/application ./tests/integration/pipeline -run 'Upload|PersistenceUpload' -count=1`

Expected: PASS; restart resumes incomplete entries and finalize is idempotent.

- [ ] **Step 5: Commit**

```bash
rtk git add migrations/pipeline/000009_upload_sessions.sql migrations/embed_test.go internal/pipeline/domain/upload* internal/pipeline/adapters/filesystem/upload* internal/pipeline/application/uploads* internal/pipeline/persistence/uploads.go tests/integration/pipeline/persistence_test.go
rtk git commit -m "feat(upload): add durable folder upload sessions"
```

### Task 4: Browser folder upload HTTP and Admin UI

**Files:**
- Create: `internal/pipeline/adminui/assets/upload.js`
- Create: `internal/pipeline/adminui/handlers/uploads.go`
- Create: `internal/pipeline/adminui/handlers/uploads_test.go`
- Modify: `internal/pipeline/adminui/assets/assets.go`
- Modify: `internal/pipeline/adminui/views/layout.templ`
- Modify: `internal/pipeline/adminui/views/incoming.templ`
- Modify: `internal/pipeline/adminui/views/models.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/assets/css/app.css`
- Modify: `scripts/verify-ui-source.sh`
- Modify: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Consumes: Task 3 `UploadService` and existing authenticated/CSRF-protected Admin listener.
- Produces: JSON endpoints under `/upload-sessions` and browser directory selection on `/incoming`.

- [ ] **Step 1: Write failing handler and UI-source tests**

Test authenticated calls to:

```text
POST   /upload-sessions
PUT    /upload-sessions/{sessionID}/files/{entryID}
POST   /upload-sessions/{sessionID}/finalize
DELETE /upload-sessions/{sessionID}
GET    /upload-sessions/{sessionID}
```

Assert raw PUT requires `X-CSRF-Token`, uses the upload file limit rather than `AdminMutationLimit`, returns `413` above the declared file size/limit, and never emits a stack trace or filesystem root. Source checks must find `webkitdirectory`, `webkitRelativePath`, `webkitGetAsEntry`, `.partial` retry status, and a concurrency value sourced from the server page model.

- [ ] **Step 2: Run tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adminui/... ./tests/integration/pipeline -run 'Upload|AdminUI' -count=1 && rtk scripts/verify-ui-source.sh`

Expected: FAIL because routes and the local script do not exist.

- [ ] **Step 3: Implement upload endpoints and browser-native client**

Embed `upload.js` through `assets.Bundle` and load it with `defer` from the existing CSP-compatible layout. The script must:

```js
const entryFile = (entry) => new Promise((resolve, reject) => entry.file(resolve, reject))
const readEntries = (reader) => new Promise((resolve, reject) => reader.readEntries(resolve, reject))

async function walkEntry(entry, prefix, output) {
  const relative = prefix ? `${prefix}/${entry.name}` : entry.name
  if (entry.isFile) {
    output.push({ file: await entryFile(entry), path: relative })
    return
  }
  const reader = entry.createReader()
  for (;;) {
    const children = await readEntries(reader)
    if (children.length === 0) return
    for (const child of children) await walkEntry(child, relative, output)
  }
}

async function collectFiles(event) {
  const output = []
  const items = [...(event.dataTransfer?.items || [])]
  if (items.length) {
    for (const item of items) {
      const entry = item.webkitGetAsEntry?.()
      if (entry) await walkEntry(entry, "", output)
    }
    return output
  }
  return [...event.target.files].map((file) => ({ file, path: file.webkitRelativePath || file.name }))
}

async function createSession(files, csrf) {
  const response = await fetch('/upload-sessions', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify({ files: files.map(({ file, path }) => ({ relative_path: path, size_bytes: file.size, media_type: file.type })) }),
  })
  if (!response.ok) throw new Error(await response.text())
  return response.json()
}

function uploadEntry(sessionID, entry, csrf, onProgress) {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest()
    request.open('PUT', `/upload-sessions/${sessionID}/files/${entry.id}`)
    request.setRequestHeader('X-CSRF-Token', csrf)
    request.upload.onprogress = ({ loaded, total }) => onProgress(loaded, total)
    request.onload = () => request.status >= 200 && request.status < 300 ? resolve() : reject(new Error(request.responseText))
    request.onerror = () => reject(new Error('upload request failed'))
    request.send(entry.file)
  })
}

async function runPool(entries, concurrency, worker) {
  let next = 0
  await Promise.all(Array.from({ length: Math.min(concurrency, entries.length) }, async () => {
    while (next < entries.length) await worker(entries[next++])
  }))
}

async function finalizeSession(sessionID, csrf) {
  const response = await fetch(`/upload-sessions/${sessionID}/finalize`, { method: 'POST', headers: { 'X-CSRF-Token': csrf } })
  if (!response.ok) throw new Error(await response.text())
  return response.json()
}
```

Implement the bodies fully with `DataTransferItem.webkitGetAsEntry()` recursion, `File.webkitRelativePath` fallback, `XMLHttpRequest.upload` progress, failed-entry retry buttons, and page refresh after finalization. Do not implement byte-range chunks. Render incomplete persisted sessions with Resume and Delete controls. JSON errors use stable codes: `INVALID_MANIFEST`, `UPLOAD_LIMIT`, `ENTRY_MISMATCH`, `SESSION_CONFLICT`, and `FINALIZE_CONFLICT`.

- [ ] **Step 4: Generate views and run tests**

Run: `rtk templ generate && rtk go test ./internal/pipeline/adminui/... ./tests/integration/pipeline -run 'Upload|AdminUI' -count=1 && rtk scripts/verify-ui-source.sh`

Expected: PASS; generated `_templ.go` files are current and all assets remain local.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/adminui scripts/verify-ui-source.sh tests/integration/pipeline/adminui_test.go
rtk git commit -m "feat(ui): add resumable folder upload"
```

### Task 5: Submission preview, metadata draft, and common ingress boundary

**Files:**
- Create: `internal/pipeline/domain/unmanaged.go`
- Create: `internal/pipeline/domain/unmanaged_test.go`
- Create: `internal/pipeline/application/preview.go`
- Create: `internal/pipeline/application/preview_test.go`
- Create: `internal/pipeline/persistence/previews.go`
- Create: `internal/pipeline/adminui/views/incoming_detail.templ`
- Modify: `internal/pipeline/application/submissions.go`
- Modify: `internal/pipeline/persistence/submissions.go`
- Modify: `internal/pipeline/application/admin.go`
- Modify: `internal/pipeline/persistence/admin.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/views/incoming.templ`
- Modify: `internal/pipeline/adminui/views/models.go`
- Modify: `tests/integration/pipeline/adminui_test.go`
- Modify: `tests/integration/pipeline/persistence_test.go`

**Interfaces:**
- Consumes: Task 3 browser submissions, existing SFTP discovery, `filesystem.Scan`, and `media.FFProbe`.
- Produces: `SubmissionPreviewService.Preview`, `SaveDraft`, and `SubmissionService.Submit` with a sealed `SubmissionDecision`.

- [ ] **Step 1: Write failing metadata and preview tests**

Build a two-track fixture where album tags agree and one track title is missing. Assert the preview reports exactly that missing field and never invents a majority value for a conflict. Add a changed-tree test:

```go
preview, err := service.Preview(ctx, "submission-1", false)
if err != nil { t.Fatal(err) }
decision := domain.SubmissionDecision{PreviewFingerprint: preview.Fingerprint, Destination: domain.DestinationUnmanaged, Metadata: correctedPlan}
os.WriteFile(filepath.Join(source, "new.flac"), []byte("changed"), 0o640)
if err := submissions.Submit(ctx, "submission-1", 0, "admin-1", decision); !errors.Is(err, application.ErrPreviewChanged) {
	t.Fatalf("changed preview error=%v", err)
}
```

Prove browser and SFTP records expose different ingress/provenance but produce the same `SubmissionDecision` shape and downstream manual candidate source.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/domain ./internal/pipeline/application ./tests/integration/pipeline -run 'Metadata|Preview|SubmissionIngress' -count=1`

Expected: FAIL because preview and decision types do not exist.

- [ ] **Step 3: Implement canonical metadata and preview caching**

Define:

```go
type Destination string
const (DestinationManaged Destination = "MANAGED"; DestinationUnmanaged Destination = "UNMANAGED")

type TrackMetadata struct {
	RelativePath, Title, Artist string
	Track, Disc int
	DurationMS int64
	ISRCs []string
}
type MetadataPlan struct {
	AlbumArtist, Album, Date, Edition string
	Tracks []TrackMetadata
	DiscTotal, TrackTotal int
	Preserved map[string]map[string][]string
}
type MetadataConflict struct { Field, RelativePath string; Values []string }
type SubmissionDecision struct {
	PreviewFingerprint string
	Destination Destination
	ReleaseMBID string
	Metadata MetadataPlan
	Artwork ArtworkSelection
}
```

Parse `TRACKNUMBER` and `DISCNUMBER` values in `N` or `N/total` form, require positive unique contiguous positions per disc, derive totals, and require album artist, album, title, track artist, track, and disc. Date remains optional. Preserve valid optional and unknown comments without treating them as canonical required fields. Cache preview JSON by tree fingerprint; a matching GET uses the cache. `SaveDraft` validates edited canonical fields and stores the draft without moving files. `Submit` rescans, requires the submitted preview fingerprint, stores decision/provenance and sealed fingerprint in one transaction, then creates the existing manual candidate. SFTP discovery writes `ingress='sftp'`; browser finalization writes `ingress='browser'`.

- [ ] **Step 4: Generate views and run tests**

Run: `rtk templ generate && rtk go test ./internal/pipeline/domain ./internal/pipeline/application ./tests/integration/pipeline -run 'Metadata|Preview|SubmissionIngress|AdminUI' -count=1`

Expected: PASS; invalid metadata stays editable on the detail page and a changed tree returns HTTP 409.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/domain/unmanaged* internal/pipeline/application/preview* internal/pipeline/application/submissions.go internal/pipeline/persistence/previews.go internal/pipeline/persistence/submissions.go internal/pipeline/application/admin.go internal/pipeline/persistence/admin.go internal/pipeline/adminui tests/integration/pipeline
rtk git commit -m "feat(ingest): add editable submission previews"
```

### Task 6: MusicBrainz candidate search and exact identity decision

**Files:**
- Create: `internal/pipeline/application/identity.go`
- Create: `internal/pipeline/application/identity_test.go`
- Modify: `internal/pipeline/adapters/musicbrainz/client.go`
- Create: `internal/pipeline/adapters/musicbrainz/search_test.go`
- Modify: `internal/pipeline/application/preview.go`
- Create: `tests/contract/golden/musicbrainz_no_match.json`
- Create: `tests/contract/golden/musicbrainz_ambiguous.json`
- Create: `tests/contract/golden/musicbrainz_exact_barcode.json`
- Create: `tests/contract/golden/musicbrainz_identifier_conflict.json`
- Modify: `tests/contract/contracts_test.go`
- Modify: `tests/integration/pipeline/release_matching_test.go`

**Interfaces:**
- Consumes: Task 5 `MetadataPlan`, observed durations/tags, and existing `domain.MatchRelease` policy.
- Produces: `IdentityService.Decide` and `musicbrainz.Client.SearchReleases`.

- [ ] **Step 1: Write failing query and decision tests**

Assert the adapter constructs deterministic WS/2 queries in this order:

```text
tagged release MBID -> GET /ws/2/release/{mbid}
barcode             -> GET /ws/2/release?query=barcode:{value}
ISRC search          -> GET /ws/2/recording?query=isrc:{value}
ISRC release browse  -> GET /ws/2/recording/{recording-mbid}?inc=releases
metadata fallback    -> GET /ws/2/release?query=artist:{artist} AND release:{album} AND date:{year} AND tracks:{count}
```

Use fixtures where a high search score has a duration mismatch, two candidates pass search but only one passes complete track identity, and conflicting tag MBIDs block an exact result. Assert the outcome is one of `NO_MATCH`, `AMBIGUOUS`, `EXACT`, or `ERROR`.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adapters/musicbrainz ./internal/pipeline/application ./tests/contract ./tests/integration/pipeline -run 'MusicBrainz|Identity|ReleaseMatching' -count=1`

Expected: FAIL because search and identity orchestration are absent.

- [ ] **Step 3: Implement bounded candidate collection and exact validation**

Define:

```go
type IdentityStatus string
const (IdentityNoMatch IdentityStatus = "NO_MATCH"; IdentityAmbiguous IdentityStatus = "AMBIGUOUS"; IdentityExact IdentityStatus = "EXACT"; IdentityError IdentityStatus = "ERROR")
type IdentityCandidate struct { Release domain.CanonicalRelease; Match domain.ReleaseMatch; Evidence []musicbrainz.Evidence }
type IdentityDecision struct { Status IdentityStatus; Exact *IdentityCandidate; Candidates []IdentityCandidate; Reason string }
type IdentityService struct { Search ReleaseSearcher; DurationPolicy domain.DurationPolicy }
func (s IdentityService) Decide(ctx context.Context, plan domain.MetadataPlan, observed domain.TechnicalReleaseResult) (IdentityDecision, error)
```

Cap each search response at 10 entities. For ISRC evidence, search recordings first and then browse releases from each bounded recording lookup. Deduplicate release MBIDs, use the existing client-wide one-second rate gate, look up full releases, and apply release-atomic positions, counts, ISRCs, normalized names, dates, and duration policy. Search score and barcode alone never create `EXACT`. One surviving candidate is exact, multiple are ambiguous, none is no-match, and network/schema failures are errors. Persist request/response hashes in the cached preview. The preview labels exact as Managed, no-match as Unmanaged, and requires an explicit candidate choice only for ambiguity.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./internal/pipeline/adapters/musicbrainz ./internal/pipeline/application ./tests/contract ./tests/integration/pipeline -run 'MusicBrainz|Identity|ReleaseMatching' -count=1`

Expected: PASS; controlled fixtures prove all four outcomes and rate ordering.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/application/identity* internal/pipeline/application/preview.go internal/pipeline/adapters/musicbrainz tests/contract tests/integration/pipeline/release_matching_test.go
rtk git commit -m "feat(metadata): search MusicBrainz release candidates"
```

### Task 7: Local-first artwork selection and explicit Spotify lookup

**Files:**
- Create: `internal/pipeline/adapters/media/artwork.go`
- Create: `internal/pipeline/adapters/media/artwork_test.go`
- Create: `internal/pipeline/adapters/spotify/oembed.go`
- Create: `internal/pipeline/adapters/spotify/oembed_test.go`
- Create: `internal/pipeline/adapters/musicbrainz/coverart.go`
- Create: `internal/pipeline/adapters/musicbrainz/coverart_test.go`
- Create: `internal/pipeline/application/artwork.go`
- Create: `internal/pipeline/application/artwork_test.go`
- Modify: `internal/pipeline/domain/unmanaged.go`
- Modify: `internal/pipeline/application/preview.go`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/views/incoming_detail.templ`
- Modify: `cmd/media-pipeline/main.go`
- Modify: `tests/integration/pipeline/enrichment_test.go`

**Interfaces:**
- Consumes: `UploadConfig.ImageMaxBytes/ImageMaxPixels`, FLAC paths, sidecars, explicit source URLs, UPC/ISRC evidence, and admin image uploads.
- Produces: `ArtworkService.Select`, `Replace`, and a normalized `ArtworkSelection` whose persisted file is backed up under `processing/work/.artwork`.

- [ ] **Step 1: Write failing priority and image-bound tests**

Cover embedded JPEG, sidecar PNG, explicit Spotify track URL, missing lookup, oversized bytes, excessive pixels, corrupt image, and admin replacement. Assert order:

```go
want := []domain.ArtworkSource{
	domain.ArtworkEmbedded,
	domain.ArtworkSidecar,
	domain.ArtworkSpotifyExplicit,
	domain.ArtworkIdentifierExact,
	domain.ArtworkAdminUpload,
}
```

Also assert a title-only Spotify search is never issued and lookup failure leaves submission usable.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adapters/media ./internal/pipeline/adapters/spotify ./internal/pipeline/adapters/musicbrainz ./internal/pipeline/application ./tests/integration/pipeline -run Artwork -count=1`

Expected: FAIL because artwork adapters and selection types are missing.

- [ ] **Step 3: Implement bounded extraction, validation, and replacement**

Define:

```go
type ArtworkSource string
type ArtworkSelection struct { Source ArtworkSource; Path, MIME, SHA256, SourceURL string; Width, Height int }
type URLArtworkLookup interface { FetchURL(context.Context, string) ([]byte, domain.ProviderEvidence, error) }
type ReleaseArtworkLookup interface { FetchRelease(context.Context, string) ([]byte, domain.ProviderEvidence, error) }
type ArtworkService struct { Local LocalArtwork; Spotify URLArtworkLookup; CoverArt ReleaseArtworkLookup; Root string; MaxBytes, MaxPixels int64 }
func (s ArtworkService) Select(ctx context.Context, submissionID, releaseRoot string, tags map[string]map[string][]string, identity IdentityDecision) (domain.ArtworkSelection, []domain.ProviderEvidence, error)
func (s ArtworkService) Replace(ctx context.Context, submissionID string, body io.Reader) (domain.ArtworkSelection, error)
```

Use `metaflac --export-picture-to` for the first front cover, then case-insensitive `cover`/`folder` JPEG or PNG sidecars. Accept only explicit `https://open.spotify.com/track/{id}` tags; call the configurable oEmbed base, then fetch only its HTTPS thumbnail URL. If no local or explicit-URL art exists and `IdentityDecision.Exact` was reached through agreeing UPC/ISRC evidence, query the credential-free Cover Art Archive release endpoint for that exact MusicBrainz release and fetch its declared front image. Do not call Cover Art Archive for an ambiguous/no-match decision. Read at most `MaxBytes+1`, call `image.DecodeConfig` before full decode, reject dimensions whose product exceeds `MaxPixels`, decode JPEG/PNG, and encode the selected result as `cover.jpg` with mode 0640. Persist hash, dimensions, MIME, source, URL, and provider response hash. Admin replacement has the same bounds and wins after the operator saves it.

- [ ] **Step 4: Generate views and run tests**

Run: `rtk templ generate && rtk go test ./internal/pipeline/adapters/media ./internal/pipeline/adapters/spotify ./internal/pipeline/adapters/musicbrainz ./internal/pipeline/application ./tests/integration/pipeline -run Artwork -count=1`

Expected: PASS; lookup failures are warnings, not import blockers.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/adapters/media/artwork* internal/pipeline/adapters/spotify internal/pipeline/adapters/musicbrainz/coverart* internal/pipeline/application/artwork* internal/pipeline/domain/unmanaged.go internal/pipeline/application/preview.go internal/pipeline/adminui cmd/media-pipeline/main.go tests/integration/pipeline/enrichment_test.go
rtk git commit -m "feat(artwork): add local-first album covers"
```

### Task 8: Unmanaged state, tag plan, and deterministic layout

**Files:**
- Create: `migrations/pipeline/000010_unmanaged_library.sql`
- Modify: `internal/pipeline/domain/state.go`
- Modify: `internal/pipeline/domain/transition.go`
- Modify: `internal/pipeline/domain/transition_test.go`
- Modify: `internal/pipeline/domain/unmanaged.go`
- Modify: `internal/pipeline/domain/unmanaged_test.go`
- Create: `internal/pipeline/application/unmanaged_metadata.go`
- Create: `internal/pipeline/application/unmanaged_metadata_test.go`
- Modify: `internal/pipeline/adapters/media/metaflac.go`
- Create: `internal/pipeline/adapters/media/metaflac_unmanaged_test.go`
- Create: `internal/pipeline/persistence/unmanaged.go`
- Modify: `migrations/embed_test.go`
- Modify: `tests/integration/pipeline/mutation_test.go`
- Modify: `tests/integration/pipeline/persistence_test.go`

**Interfaces:**
- Consumes: Task 5 approved metadata, Task 7 artwork, existing `MutationService` safety evidence, and candidate transitions.
- Produces: unmanaged candidate states, `UnmanagedMetadataService.Build`, `MetaFLAC.ApplySelected`, and durable unmanaged import intents.

- [ ] **Step 1: Write failing state, path, mutation, and schema tests**

Assert these transitions and no others:

```text
RELEASE_MATCHING -> UNMANAGED_REVIEW | UNMANAGED_READY
UNMANAGED_REVIEW -> UNMANAGED_READY | REJECTED | CANCELLED
UNMANAGED_READY -> UNMANAGED_IMPORTING
UNMANAGED_IMPORTING -> UNMANAGED_IMPORTED | UNMANAGED_REVIEW
```

Test layouts:

```text
Kaleb J/OFF GUARD (2024)/01 - Untukmu.flac
Artist/Album/Disc 01/01 - Track.flac
Artist/Album [Deluxe]/01 - Track.flac
```

Test path component cleanup for slashes, controls, trailing dots/spaces, reserved `.`/`..`, and post-normalization collisions. Verify unmanaged mutation changes only `TITLE`, `ARTIST`, `ALBUM`, `ALBUMARTIST`, `TRACKNUMBER`, `TRACKTOTAL`, `DISCNUMBER`, and `DISCTOTAL`; it preserves unknown comments, UPC, ISRC, source URLs, audio MD5, and embedded pictures.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./migrations ./internal/pipeline/domain ./internal/pipeline/application ./internal/pipeline/adapters/media ./tests/integration/pipeline -run 'Unmanaged|Mutation|Transition|Persistence' -count=1`

Expected: FAIL because unmanaged state and storage are absent.

- [ ] **Step 3: Implement unmanaged state and deterministic plans**

Migration `000010` creates `unmanaged_releases` and `unmanaged_import_intents`, both keyed to the original candidate, with immutable approved plan/evidence JSON, current state/revision, final path, manifest, fingerprint, status, timestamps, and unique idempotency key. Add:

```go
type UnmanagedPlan struct { CandidateID string; Metadata MetadataPlan; Artwork ArtworkSelection; RelativeRoot string; Files []PlannedFile }
type PlannedFile struct { SourceRelative, TargetRelative, Kind string }
func BuildUnmanagedLayout(plan MetadataPlan) (string, []PlannedFile, error)
func SanitizeMusicComponent(value string) (string, error)
```

`UnmanagedMetadataService.Build` re-derives tags from post-claim technical evidence, applies only the approved overrides, rejects drift/conflict, and detects destination collisions before mutation. Add `MetaFLAC.ApplySelected(ctx,path,tags,fields,preservePictures)`; keep existing managed `Apply` behavior by delegating with the existing full field list and `preservePictures=false`. The unmanaged caller passes the eight fields above and `preservePictures=true`. Verify tags, picture count, audio MD5, SHA-256, and `flac -t` after mutation.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./migrations ./internal/pipeline/domain ./internal/pipeline/application ./internal/pipeline/adapters/media ./tests/integration/pipeline -run 'Unmanaged|Mutation|Transition|Persistence' -count=1`

Expected: PASS; `UNMANAGED_IMPORTED` is terminal and existing managed mutation tests remain unchanged.

- [ ] **Step 5: Commit**

```bash
rtk git add migrations/pipeline/000010_unmanaged_library.sql migrations/embed_test.go internal/pipeline/domain internal/pipeline/application/unmanaged_metadata* internal/pipeline/adapters/media/metaflac* internal/pipeline/persistence/unmanaged.go tests/integration/pipeline
rtk git commit -m "feat(unmanaged): add metadata and layout plans"
```

### Task 9: Atomic unmanaged import and Navidrome verification

**Files:**
- Create: `internal/pipeline/application/unmanaged.go`
- Create: `internal/pipeline/application/unmanaged_test.go`
- Create: `internal/pipeline/adapters/filesystem/move_noreplace.go`
- Create: `internal/pipeline/adapters/filesystem/move_noreplace_test.go`
- Modify: `internal/pipeline/application/workflow.go`
- Modify: `internal/pipeline/application/workflow_test.go`
- Modify: `internal/pipeline/application/recovery.go`
- Modify: `internal/pipeline/persistence/recovery.go`
- Modify: `cmd/media-pipeline/main.go`
- Create: `tests/integration/pipeline/unmanaged_import_test.go`
- Modify: `tests/integration/pipeline/recovery_test.go`

**Interfaces:**
- Consumes: Task 8 `UnmanagedPlan`/intent repository, Task 2 Navidrome client, existing work root, checksums, and leases.
- Produces: `UnmanagedImportService.Import`, `Reconcile`, and workflow completion at `UNMANAGED_IMPORTED`.

- [ ] **Step 1: Write failing import, collision, and restart tests**

Use real directories to prove:

```go
result, err := importer.Import(ctx, candidateID, plan)
if err != nil { t.Fatal(err) }
if result.FinalPath != filepath.Join(unmanagedRoot, "Kaleb J", "OFF GUARD (2024)") { t.Fatalf("path=%s", result.FinalPath) }
```

Inject failures before mutation, after intent, after file re-layout, after final rename, during scan, and after successful visibility before state update. Recreate the service after each failure and assert one final directory, no overwrite, matching checksums, and eventual `UNMANAGED_IMPORTED`. Pre-create the target with a file and assert review state with no changed target bytes.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adapters/filesystem ./internal/pipeline/application ./tests/integration/pipeline -run 'UnmanagedImport|Recovery|NoReplace' -count=1`

Expected: FAIL because atomic no-replace import is absent.

- [ ] **Step 3: Implement intent-first import and reconciliation**

Define:

```go
type UnmanagedImportService struct {
	Store UnmanagedStore
	Metadata UnmanagedMetadataService
	Navidrome NavidromeLibrary
	WorkRoot, LibraryRoot string
	MoveNoReplace func(string, string) error
}
func (s UnmanagedImportService) Import(ctx context.Context, candidateID string, approved domain.SubmissionDecision) (domain.UnmanagedRelease, error)
func (s UnmanagedImportService) Reconcile(ctx context.Context, candidateID string) (bool, error)
```

Write the idempotent intent before mutation. Preflight every source entry; accepted entries are planned FLAC, basename-matched `.lrc`, `.elrc`, or `.ttml` lyrics, and the selected artwork. Any other entry transitions to `UNMANAGED_REVIEW` before changing a file. Re-layout the claimed work directory using sibling temporary names, record every consumed source entry in the manifest, and keep only planned files in the final release. Create the artist parent, then use Linux `renameat2(RENAME_NOREPLACE)` for the album directory; map `EXDEV` to the existing cross-device error. Trigger an Unmanaged scan, wait, and verify exact album artist/title/track count in the Unmanaged library. Only then transition to terminal state and clean empty staging directories. Recovery inspects intent, work, final path, manifest, and Navidrome before deciding the next idempotent action.

Wire `ControlledWorkflow` so an approved unmanaged decision goes from release matching to review/ready, ready to importing, and importing to reconcile/imported. Managed behavior must remain unchanged.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./internal/pipeline/adapters/filesystem ./internal/pipeline/application ./tests/integration/pipeline -run 'UnmanagedImport|Recovery|NoReplace|Workflow' -count=1`

Expected: PASS for every injected restart boundary and destination collision.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/application/unmanaged* internal/pipeline/application/workflow* internal/pipeline/application/recovery.go internal/pipeline/persistence/recovery.go internal/pipeline/adapters/filesystem/move_noreplace* cmd/media-pipeline/main.go tests/integration/pipeline
rtk git commit -m "feat(unmanaged): import releases atomically"
```

### Task 10: Shared Lidarr catalog preparation

**Files:**
- Create: `internal/pipeline/adapters/lidarr/catalog.go`
- Create: `internal/pipeline/adapters/lidarr/catalog_test.go`
- Create: `internal/pipeline/application/catalog.go`
- Create: `internal/pipeline/application/catalog_test.go`
- Modify: `internal/pipeline/adapters/lidarr/client.go`
- Modify: `tests/integration/pipeline/lidarr_config_test.go`
- Modify: `tests/integration/pipeline/import_test.go`

**Interfaces:**
- Consumes: exact `domain.CanonicalRelease`, existing Lidarr API key/client, and managed root `/data/library`.
- Produces: `LidarrCatalogService.EnsureRelease` returning concrete Lidarr artist, album, and album-release IDs.

- [ ] **Step 1: Write failing present/absent catalog contract tests**

Use `httptest.Server` fixtures for:

```text
GET  /api/v1/rootfolder
GET  /api/v1/qualityprofile
GET  /api/v1/metadataprofile
GET  /api/v1/artist/lookup?term=lidarr:{artist-mbid}
GET  /api/v1/artist
POST /api/v1/artist
POST /api/v1/command                 name=RefreshArtist
GET  /api/v1/album?artistId={id}
PUT  /api/v1/album/{id}
GET  /api/v1/album/{id}
```

Assert an existing exact release causes no POST, an absent artist is added with IDs returned by the fixture rather than literals, only the target album is monitored, and no request body contains `ArtistSearch`, `AlbumSearch`, `searchForMissingAlbums:true`, or `/data/library-unmanaged`.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adapters/lidarr ./internal/pipeline/application ./tests/integration/pipeline -run 'Catalog|LidarrConfig|Import' -count=1`

Expected: FAIL because catalog preparation does not exist.

- [ ] **Step 3: Implement idempotent catalog preparation**

Define:

```go
type CatalogResult struct { ArtistID, AlbumID, AlbumReleaseID int }
type LidarrCatalog interface { EnsureRelease(context.Context, domain.CanonicalRelease) (CatalogResult, error) }
type LidarrCatalogService struct { Catalog LidarrCatalog }
func (s LidarrCatalogService) EnsureRelease(ctx context.Context, release domain.CanonicalRelease) (CatalogResult, error)
```

Select the root whose path is exactly `/data/library`. Use profile IDs returned by the artist lookup resource when populated; otherwise use the API-reported default profile, and fail clearly if no usable profile exists. Find or add the primary MusicBrainz album artist with monitoring disabled and `searchForMissingAlbums=false`. Poll bounded refresh completion, locate the exact release by `foreignReleaseId`, update only its album to monitored, and return the internal release ID. Treat 429/5xx/transport errors as retryable through the existing client. Never delete a catalog entry during rollback.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./internal/pipeline/adapters/lidarr ./internal/pipeline/application ./tests/integration/pipeline -run 'Catalog|LidarrConfig|Import' -count=1`

Expected: PASS for present artist, absent artist, absent album refresh, and idempotent rerun.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/adapters/lidarr internal/pipeline/application/catalog* tests/integration/pipeline/lidarr_config_test.go tests/integration/pipeline/import_test.go
rtk git commit -m "feat(lidarr): prepare missing catalog releases"
```

### Task 11: Initial managed import for artists absent from Lidarr

**Files:**
- Modify: `internal/pipeline/application/workflow.go`
- Modify: `internal/pipeline/application/workflow_test.go`
- Modify: `internal/pipeline/application/import.go`
- Modify: `cmd/media-pipeline/main.go`
- Create: `tests/integration/pipeline/catalog_import_test.go`
- Modify: `tests/integration/pipeline/import_test.go`

**Interfaces:**
- Consumes: Task 10 `LidarrCatalogService`, existing `ImportService`, and Task 6 exact preview decision.
- Produces: managed Submit flow that prepares Lidarr before the controlled Manual Import.

- [ ] **Step 1: Write a failing end-to-end workflow test**

Create an exact MusicBrainz release whose artist and album are absent from the fake Lidarr. Drive the candidate from `RELEASE_MATCHING` to `IMPORTED` and record calls. Assert order:

```text
MusicBrainz exact match
Lidarr EnsureRelease
metadata/enrichment mutation
move to approved
Manual Import prepare
Manual Import submit once
reconcile final files
```

Inject a catalog refresh failure and assert the candidate remains retryable before approved staging and before any Manual Import command.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/application ./tests/integration/pipeline -run 'CatalogImport|Workflow|Import' -count=1`

Expected: FAIL because workflow calls Manual Import without ensuring the catalog.

- [ ] **Step 3: Wire catalog preparation into managed workflow**

Add `Catalog LidarrCatalogService` to `ControlledWorkflow`. In the managed path call `EnsureRelease` after exact identity is durable and before `ImportService.Submit` can move the work tree. Verify the returned `AlbumReleaseID` agrees with `ManualImporter.Prepare`; a mismatch is a hard identity error. Retry catalog failures without creating a second artist or command. Keep unmanaged decisions entirely outside this catalog call.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./internal/pipeline/application ./tests/integration/pipeline -run 'CatalogImport|Workflow|Import' -count=1`

Expected: PASS; absent Lidarr catalog works and existing managed imports remain green.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/application/workflow* internal/pipeline/application/import.go cmd/media-pipeline/main.go tests/integration/pipeline
rtk git commit -m "feat(pipeline): prepare Lidarr before managed import"
```

### Task 12: Durable manual batch checks

**Files:**
- Create: `migrations/pipeline/000011_migration_batches.sql`
- Create: `internal/pipeline/domain/migration.go`
- Create: `internal/pipeline/domain/migration_test.go`
- Create: `internal/pipeline/application/migration_check.go`
- Create: `internal/pipeline/application/migration_check_test.go`
- Create: `internal/pipeline/application/migration_runtime.go`
- Create: `internal/pipeline/application/migration_runtime_test.go`
- Create: `internal/pipeline/persistence/migrations.go`
- Modify: `migrations/embed_test.go`
- Modify: `internal/pipeline/application/runtime.go`
- Modify: `cmd/media-pipeline/main.go`
- Create: `tests/integration/pipeline/migration_check_test.go`
- Modify: `tests/integration/pipeline/persistence_test.go`

**Interfaces:**
- Consumes: imported unmanaged release manifests/metadata, Task 6 `IdentityService`, existing leases, and the configured MusicBrainz rate interval.
- Produces: `MigrationCheckService.CreateBatch`, `CheckItem`, `ResolveSelection`, and a button-triggered `MigrationRuntime`.

- [ ] **Step 1: Write failing batch, filter, and read-only tests**

Create four unmanaged releases and controlled outcomes: no match, ambiguous, exact, and retryable error. Assert one batch has four independent item states and survives service recreation. Assert:

```go
ids, err := service.ResolveSelection(ctx, Selection{SelectAll: true, Filter: UnmanagedFilter{Query: "Kaleb", Status: "IMPORTED"}})
if err != nil || !slices.Equal(ids, []string{"release-1", "release-3"}) { t.Fatalf("ids=%v err=%v", ids, err) }
```

Snapshot Lidarr request count, unmanaged tree fingerprints, and FLAC hashes before and after `CheckItem`; every value must remain identical. No timer or scheduler may create a batch.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./migrations ./internal/pipeline/domain ./internal/pipeline/application ./tests/integration/pipeline -run 'MigrationCheck|Batch|Selection|Persistence' -count=1`

Expected: FAIL because migration state and runtime do not exist.

- [ ] **Step 3: Implement durable check batches**

Migration `000011` creates `migration_batches`, `migration_items`, and append-only `migration_item_errors`, with item identity, original unmanaged candidate, state/revision, `resume_state`, approved/exact release MBID, request/response evidence, idempotency key, timestamps, and uniqueness per batch/release. Define:

```go
type MigrationState string
const (
	CheckPending MigrationState = "CHECK_PENDING"
	Checking MigrationState = "CHECKING"
	NoMatch MigrationState = "NO_MATCH"
	Ambiguous MigrationState = "AMBIGUOUS"
	ExactMatch MigrationState = "EXACT_MATCH"
	Confirmed MigrationState = "CONFIRMED"
	LidarrCatalogReady MigrationState = "LIDARR_CATALOG_READY"
	ImportSubmitted MigrationState = "IMPORT_SUBMITTED"
	Reconciling MigrationState = "RECONCILING"
	Migrated MigrationState = "MIGRATED"
	FailedRetryable MigrationState = "FAILED_RETRYABLE"
)
```

Allow `CHECK_PENDING -> CHECKING`, `CHECKING -> NO_MATCH|AMBIGUOUS|EXACT_MATCH|FAILED_RETRYABLE`, `FAILED_RETRYABLE -> resume_state`, `EXACT_MATCH -> CONFIRMED`, `CONFIRMED -> LIDARR_CATALOG_READY|FAILED_RETRYABLE`, `LIDARR_CATALOG_READY -> IMPORT_SUBMITTED|FAILED_RETRYABLE`, `IMPORT_SUBMITTED -> RECONCILING`, and `RECONCILING -> MIGRATED|FAILED_RETRYABLE`. Store the state that failed in `resume_state`, append every failure, and reject every other transition.

`CreateBatch` accepts explicit release IDs or a server-resolved filter-aware selection, writes `CHECK_PENDING` items plus an audit event, and enqueues only because an authenticated operator pressed the button. `MigrationRuntime` uses `Concurrency.MigrationCheck` workers but shares the MusicBrainz client's serialized rate gate. Each worker leases one item, reuses `IdentityService`, records immutable evidence, and transitions independently. Startup requeues only pending/checking/retryable items that belong to an explicit unfinished batch; it never creates new checks. A page refresh reads durable state.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./migrations ./internal/pipeline/domain ./internal/pipeline/application ./tests/integration/pipeline -run 'MigrationCheck|Batch|Selection|Persistence' -count=1`

Expected: PASS; mixed outcomes persist and the read-only invariants hold.

- [ ] **Step 5: Commit**

```bash
rtk git add migrations/pipeline/000011_migration_batches.sql migrations/embed_test.go internal/pipeline/domain/migration* internal/pipeline/application/migration_check* internal/pipeline/application/migration_runtime* internal/pipeline/persistence/migrations.go internal/pipeline/application/runtime.go cmd/media-pipeline/main.go tests/integration/pipeline
rtk git commit -m "feat(migration): add manual batch catalog checks"
```

### Task 13: Confirmed migration through Lidarr Manual Import

**Files:**
- Create: `internal/pipeline/application/migration.go`
- Create: `internal/pipeline/application/migration_test.go`
- Modify: `internal/pipeline/application/migration_runtime.go`
- Modify: `internal/pipeline/persistence/migrations.go`
- Modify: `internal/pipeline/adapters/lidarr/manual_import.go`
- Modify: `internal/pipeline/adapters/lidarr/verify.go`
- Create: `internal/pipeline/application/migration_mutation.go`
- Create: `internal/pipeline/application/migration_mutation_test.go`
- Modify: `internal/pipeline/application/recovery.go`
- Modify: `cmd/media-pipeline/main.go`
- Create: `tests/integration/pipeline/migration_test.go`
- Modify: `tests/integration/pipeline/import_test.go`
- Modify: `tests/integration/pipeline/recovery_test.go`

**Interfaces:**
- Consumes: exact Task 12 items, Task 10 catalog service, current `ManualImporter`/`LibraryVerifier`, Task 2 Navidrome client, approved/unmanaged roots, and existing leases.
- Produces: `MigrationService.ConfirmSelected`, `Process`, and `Reconcile` with at-most-once Manual Import.

- [ ] **Step 1: Write failing confirmation, rollback, lost-ack, and partial-import tests**

Cover these exact cases:

```text
NO_MATCH/AMBIGUOUS cannot be confirmed
EXACT_MATCH confirmation creates one immutable intent per item
identity drift before catalog leaves unmanaged release untouched
pre-submit mutation failure restores original unmanaged tags and embedded art
failure before Manual Import returns the exact manifest to unmanaged root
lost HTTP acknowledgement sends one ManualImport command and enters RECONCILING
partial managed files remain RECONCILING without a second unmanaged copy
complete import becomes visible in Managed and absent from Unmanaged
```

Count `POST /api/v1/command` bodies named `ManualImport`; every intent must produce at most one.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/application ./tests/integration/pipeline -run 'Migration|LostAcknowledgement|PartialImport|Recovery' -count=1`

Expected: FAIL because confirmed migration is absent.

- [ ] **Step 3: Implement confirmation and restart-safe migration**

Define:

```go
type MigrationService struct {
	Store MigrationStore
	Identity IdentityService
	Catalog LidarrCatalogService
	Importer ImportPreparer
	Verifier ImportFinalVerifier
	Navidrome NavidromeLibrary
	UnmanagedRoot, ApprovedRoot string
	LeaseDuration time.Duration
}
func (s MigrationService) ConfirmSelected(ctx context.Context, items []ConfirmedSelection, actor string) error
func (s MigrationService) Process(ctx context.Context, itemID string) error
func (s MigrationService) Reconcile(ctx context.Context, itemID string) (bool, error)
```

`ConfirmedSelection` carries item ID, expected revision, and approved release MBID. Confirmation accepts only `EXACT_MATCH` with the same MBID and records actor/audit. Processing leases the item, verifies the unmanaged fingerprint, re-fetches MusicBrainz, re-runs the exact match, ensures Lidarr catalog, then records `LIDARR_CATALOG_READY`. Move the whole release with no-replace into an item-specific approved path. Apply the canonical MusicBrainz tag plan there with embedded pictures preserved, record the complete pre-mutation tag sets and checksums, verify audio MD5 plus `flac -t`, then call existing `ManualImporter.Prepare`. Persist request hash/body before submission, serialize through the existing import concurrency of 1, submit once, and immediately mark `IMPORT_SUBMITTED`. A timeout after request write goes directly to reconciliation and never calls Submit again.

Before submission, a failure first reapplies each stored unmanaged tag set, verifies the original picture count, audio MD5, and semantic metadata, rebuilds the original manifest, and then returns the exact directory to its original unmanaged path only when no destination exists. If restoration cannot be proven, keep the item failed in approved staging for operator recovery. After submission, query command/history/queue/album/track-file evidence through `LibraryVerifier`; never simulate rollback by deleting managed files. On complete verification, scan both Navidrome libraries, require exact presence in Managed and absence from Unmanaged, then mark `MIGRATED`. Retain prior paths and all evidence.

- [ ] **Step 4: Run focused tests**

Run: `rtk go test ./internal/pipeline/application ./tests/integration/pipeline -run 'Migration|LostAcknowledgement|PartialImport|Recovery' -count=1`

Expected: PASS, including one Manual Import under lost acknowledgement and safe restart at every state.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/application/migration* internal/pipeline/application/recovery.go internal/pipeline/persistence/migrations.go internal/pipeline/adapters/lidarr cmd/media-pipeline/main.go tests/integration/pipeline
rtk git commit -m "feat(migration): move confirmed releases into Lidarr"
```

### Task 14: Unmanaged list, batch progress, and migration confirmation UI

**Files:**
- Create: `internal/pipeline/adminui/views/unmanaged.templ`
- Create: `internal/pipeline/adminui/views/migration_detail.templ`
- Create: `internal/pipeline/adminui/handlers/migrations.go`
- Create: `internal/pipeline/adminui/handlers/migrations_test.go`
- Modify: `internal/pipeline/application/admin.go`
- Modify: `internal/pipeline/persistence/admin.go`
- Modify: `internal/pipeline/adminui/views/models.go`
- Modify: `internal/pipeline/adminui/views/layout.templ`
- Modify: `internal/pipeline/adminui/handlers/routes.go`
- Modify: `internal/pipeline/adminui/assets/css/app.css`
- Modify: `scripts/verify-ui-source.sh`
- Modify: `tests/integration/pipeline/adminui_test.go`

**Interfaces:**
- Consumes: Task 12 batch service/read model and Task 13 confirmation service.
- Produces: `/unmanaged`, `/migration-batches/{id}`, `Check now`, `Check selected`, filter-aware `Select all`, and separate confirmed migration forms.

- [ ] **Step 1: Write failing handler and rendering tests**

Test:

```text
GET  /unmanaged?q=kaleb&status=UNMANAGED_IMPORTED
POST /unmanaged/check
GET  /migration-batches/{batchID}
POST /migration-batches/{batchID}/confirm
POST /migration-items/{itemID}/retry
```

Assert CSRF, authenticated actor, expected item revisions, and explicit confirmation checkbox. Render one mixed batch and require all labels: `No match`, `Ambiguous`, `Exact candidate`, `Error`, `Checking`, and `Migrated`. Ensure the first check form cannot contain migration confirmation fields, and the confirmation form cannot select no-match or ambiguous rows.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `rtk go test ./internal/pipeline/adminui/... ./tests/integration/pipeline -run 'UnmanagedUI|MigrationUI|AdminUI' -count=1 && rtk scripts/verify-ui-source.sh`

Expected: FAIL because the pages and handlers do not exist.

- [ ] **Step 3: Implement server-rendered batch controls**

Add read models:

```go
type UnmanagedSummary struct { CandidateID, AlbumArtist, Album, Year, State string; Revision uint64; UpdatedAt time.Time }
type MigrationItemSummary struct { ID, ReleaseID, AlbumArtist, Album, State, CandidateMBID, Error string; Revision uint64 }
type MigrationBatchDetail struct { ID, Actor, State string; Items []MigrationItemSummary; CreatedAt, UpdatedAt time.Time }
```

The Unmanaged page submits either explicit `release_id` values or `select_all=filtered` plus the current query/status. `Check now` is the same endpoint with one explicit ID. Redirect immediately to the durable batch page and use small HTMX polling fragments while any item is pending/checking; stop polling when all items settle. Display evidence and field differences for exact/ambiguous candidates. Put `Confirm selected migrations` in a separate form with item IDs, revisions, approved MBIDs, a required confirmation checkbox, and no unreviewed migrate-all control. Keep focus, labels, table captions, reduced-motion behavior, and responsive layout consistent with the existing UI.

- [ ] **Step 4: Generate views and run UI verification**

Run: `rtk templ generate && rtk go test ./internal/pipeline/adminui/... ./tests/integration/pipeline -run 'UnmanagedUI|MigrationUI|AdminUI' -count=1 && rtk scripts/verify-ui-source.sh && rtk git diff --exit-code -- internal/pipeline/adminui/views`

Expected: PASS; generated sources are committed, local-only, and current.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/pipeline/adminui internal/pipeline/application/admin.go internal/pipeline/persistence/admin.go scripts/verify-ui-source.sh tests/integration/pipeline/adminui_test.go
rtk git commit -m "feat(ui): manage unmanaged release migrations"
```

### Task 15: Backup, restore, setup, recovery, and operator documentation

**Files:**
- Modify: `scripts/backup/backup.sh`
- Modify: `internal/platform/restore/restore.go`
- Modify: `tests/integration/operations/backup_policy_test.go`
- Modify: `tests/integration/operations/restore_test.go`
- Modify: `tests/integration/operations/management_command_test.go`
- Modify: `scripts/manage/setup.sh`
- Modify: `scripts/manage/smoke.sh`
- Modify: `README.md`
- Modify: `docs/runbooks/install.md`
- Modify: `docs/runbooks/backup.md`
- Modify: `docs/runbooks/restore.md`
- Modify: `docs/runbooks/incidents.md`
- Modify: `docs/runbooks/clients.md`
- Modify: `docs/runbooks/acceptance-evidence.md`
- Modify: `scripts/check-runbooks.sh`

**Interfaces:**
- Consumes: existing `./denyra setup`, update/rollback, Restic backup, deterministic restore verifier, and startup recovery.
- Produces: one-command creation/reconciliation, backup of unmanaged/upload/migration state, and explicit recovery evidence.

- [ ] **Step 1: Write failing operations and restore tests**

Extend fixtures with:

```text
source/library-unmanaged/Kaleb J/OFF GUARD (2024)/01 - Untukmu.flac
source/incoming/uploading/session-1/01.flac.partial
pipeline migration batch/item rows in nonterminal states
```

Assert backup arguments include `/source/data/library-unmanaged`, `/source/data/incoming`, `/source/data/processing`, and state; restore hashes the unmanaged file and validates both pipeline migration sequences. Assert setup remains one command, creates both roots, invokes reconciler, and has no prompt for a compiler, API key beyond existing generated secrets, library creation, or database migration.

- [ ] **Step 2: Run focused operations tests and confirm failure**

Run: `rtk go test ./internal/platform/restore ./tests/integration/operations -run 'Backup|Restore|Management|Setup' -count=1 && rtk scripts/check-runbooks.sh`

Expected: FAIL because unmanaged content and instructions are not covered.

- [ ] **Step 3: Extend backup/restore and concise runbooks**

Add `library-unmanaged` to Restic input and restore `sourceDirectories`; keep incomplete upload/artwork files under already-backed-up incoming/processing roots. Restore verification must require managed library, unmanaged library, state, incoming, processing, and quarantine to be same-device and checksum-valid. Startup recovery reports and requeues nonterminal upload finalizations, unmanaged intents, check items, and confirmed migrations without starting new catalog checks.

Document only this operator flow:

```text
./denyra setup
Open http://HOST:8090/incoming
Drop/select an album folder or upload it through SFTP
Review metadata/artwork, then Submit
Use Unmanaged -> Check selected -> review -> Confirm selected migrations
./denyra backup
```

State that no host-side Go, Node, Python, templ, ffmpeg, flac, or compiler installation is required; released containers carry runtime media tools. Explain Navidrome's Managed/Unmanaged selector and Feishin music-folder selection. Keep troubleshooting limited to actionable upload, MusicBrainz, Lidarr, Navidrome scan, collision, and recovery checks.

- [ ] **Step 4: Run operations verification**

Run: `rtk go test ./internal/platform/restore ./tests/integration/operations -run 'Backup|Restore|Management|Setup' -count=1 && rtk scripts/check-runbooks.sh`

Expected: PASS; restore detects changed unmanaged or incomplete-upload files.

- [ ] **Step 5: Commit**

```bash
rtk git add scripts/backup scripts/manage internal/platform/restore tests/integration/operations README.md docs/runbooks scripts/check-runbooks.sh
rtk git commit -m "feat(ops): back up unmanaged ingestion state"
```

### Task 16: Deterministic acceptance suite, live compatibility smoke, and browser proof

**Files:**
- Modify: `cmd/denyra-acceptance-fixture/main.go`
- Modify: `cmd/denyra-acceptance-fixture/main_test.go`
- Modify: `tests/acceptance/harness/fixtures.go`
- Modify: `tests/acceptance/harness/faults.go`
- Modify: `tests/acceptance/harness/compose.go`
- Create: `tests/acceptance/unmanaged_ingestion_test.go`
- Create: `tests/integration/pipeline/live_compatibility_test.go`
- Modify: `deploy/compose.acceptance.yaml`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Modify: `docs/runbooks/acceptance-evidence.md`

**Interfaces:**
- Consumes: the entire feature, generated real FLAC fixture script, released Lidarr/Navidrome containers, and controlled MusicBrainz/Spotify fixture responses.
- Produces: deterministic CI evidence, opt-in live schema checks, and local Chrome MCP validation of the actual UI.

- [ ] **Step 1: Write failing acceptance scenarios**

Add named tests for every required outcome:

```go
func TestBrowserUploadResumeAndUnmanagedImport(t *testing.T)
func TestSFTPAndBrowserConvergeAtSubmission(t *testing.T)
func TestBatchCheckMixedResultsIsReadOnly(t *testing.T)
func TestConfirmedMigrationAddsMissingCatalogWithoutSearch(t *testing.T)
func TestMigrationLostAcknowledgementSubmitsOnce(t *testing.T)
func TestMigrationPartialImportReconcilesWithoutDuplicate(t *testing.T)
func TestMigrationRestartsAtEveryDurableState(t *testing.T)
func TestUnmanagedBackupRestore(t *testing.T)
```

The fixture must generate real FLAC, embedded artwork, sidecar artwork/lyrics, ambiguous/no-match/exact/error MusicBrainz responses, missing Lidarr artist/album responses, and Navidrome library visibility. Record request counts and filesystem manifests.

- [ ] **Step 2: Run acceptance tests and confirm failure**

Run: `rtk go test ./tests/acceptance -run 'BrowserUpload|SFTPAndBrowser|BatchCheck|ConfirmedMigration|MigrationLost|MigrationPartial|MigrationRestarts|UnmanagedBackup' -count=1`

Expected: FAIL until fixture endpoints and complete wiring support the scenarios.

- [ ] **Step 3: Complete deterministic fixtures and opt-in compatibility smoke**

Extend the fixture server with exact current schemas used by the adapters. CI must not contact public MusicBrainz or Spotify. Add `TestLiveCompatibility` guarded by `DENYRA_LIVE_COMPATIBILITY=1`; it performs read-only MusicBrainz search/lookup, logs into the running Navidrome test instance to list libraries/get scan status, and reads Lidarr root/profile/artist-lookup schemas without adding, updating, searching, importing, or deleting anything. Add `make acceptance` for deterministic Compose tests and `make live-compatibility` for the opt-in smoke. CI runs deterministic acceptance only.

- [ ] **Step 4: Run full automated verification**

Run:

```bash
rtk templ generate
rtk scripts/verify-ui-source.sh
rtk make verify
rtk go test ./tests/acceptance -count=1
rtk env DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=local go test ./tests/integration -run 'ServiceImage|LidarrPluginInstall' -count=1
rtk git diff --check
```

Expected: all commands exit 0. `go test -race ./...` must find `ffmpeg`, `ffprobe`, `flac`, and `metaflac` through the existing CI dependency step.

- [ ] **Step 5: Exercise the real local UI with Chrome MCP**

Start through the supported command: `rtk ./denyra setup`. Use Chrome MCP on the Admin UI to validate this exact sequence against a copy of `/home/waxarsatia/Music/Kaleb J/OFF GUARD/`:

```text
login
open Incoming
select/drop the OFF GUARD folder
interrupt one file request and press Retry
verify preview metadata and embedded cover
Submit as Unmanaged
verify Unmanaged list and Navidrome Unmanaged visibility/artwork
select the release and press Check selected
verify durable result after refresh
if an exact fixture/live-safe candidate exists, confirm it and verify Managed visibility
```

Do not mutate the original local folder; upload browser file handles or a temporary copy. Capture visible screenshots and request evidence for upload progress, preview, batch result, confirmation boundary, and Navidrome result. If live MusicBrainz has no exact OFF GUARD record, keep the real release unmanaged and run confirmed migration only against the deterministic exact fixture.

- [ ] **Step 6: Commit final acceptance evidence**

```bash
rtk git add cmd/denyra-acceptance-fixture tests/acceptance tests/integration/pipeline/live_compatibility_test.go deploy/compose.acceptance.yaml .github/workflows/ci.yml Makefile docs/runbooks/acceptance-evidence.md
rtk git commit -m "test: verify unmanaged ingestion and migration"
```

## Final verification gate

- [ ] Run `rtk git status --short` and confirm only intended tracked changes exist.
- [ ] Run `rtk templ generate && rtk git diff --exit-code -- internal/pipeline/adminui/views`.
- [ ] Run `rtk scripts/verify-ui-source.sh && rtk scripts/check-runbooks.sh`.
- [ ] Run `rtk make verify`.
- [ ] Run deterministic acceptance: `rtk go test ./tests/acceptance -count=1`.
- [ ] Run image smoke when Docker is available: `rtk env DENYRA_TEST_IMAGE_SMOKE=1 DENYRA_IMAGE_TAG=local go test ./tests/integration -run 'ServiceImage|LidarrPluginInstall' -count=1`.
- [ ] Inspect `rtk git log --oneline --decorate -18` and confirm every task has a focused commit.
- [ ] Push only after all gates pass: `rtk git push origin main`.
