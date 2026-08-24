# Install Denyra

## Host preparation

Use a Linux amd64 host with Docker Engine, Docker Compose v2, Buildx, Git, and enough space for the FLAC library plus temporary release batches. Deployment images and application runtimes are pinned in `dependencies.lock.json`. The host's local Go version is not a deployment identity.

Choose one numeric media UID and GID. The examples use `1000:1000`; replace both values consistently if those IDs are already assigned.

```sh
sudo groupadd --gid 1000 denyra-media
sudo useradd --uid 1000 --gid 1000 --no-create-home --shell /usr/sbin/nologin denyra-media
export DENYRA_MEDIA_UID=1000
export DENYRA_MEDIA_GID=1000
export DENYRA_DATA_ROOT=/data
```

Create the shared filesystem. Every listed path must be on the filesystem that contains `/data`, otherwise the pipeline cannot guarantee atomic moves.

```sh
sudo install -d -m 0750 -o 1000 -g 1000 \
  /data/downloads/slskd /data/downloads/spotiflac /data/downloads/other \
  /data/incoming/manual /data/processing/work /data/processing/approved \
  /data/quarantine /data/library /data/backups \
  /data/state/gateway /data/state/pipeline /data/state/lidarr \
  /data/state/slskd /data/state/sftpgo /data/state/navidrome /data/cache/navidrome
```

## Secrets

Read `deploy/secrets/README.md`, create every listed file under `deploy/secrets`, set ownership for the service UID, and use mode `0400`. Generate `internal_bearer`, `audit_key`, and `restic_password` with a CSPRNG. `bootstrap_admin` must contain the first media-pipeline admin password and must be at least eight characters. It is consumed only when the user table is empty. After the first password change, replace its contents with an empty file so Compose can still mount the declared secret without retaining the bootstrap credential.

The SFTPGo admin is created through SFTPGo's first-run WebAdmin flow on port `8080`. It is separate from the media-pipeline admin and from Navidrome music users. Do not reuse passwords between them.

## Verify and build

Run every command from the repository root:

```sh
scripts/verify-pins/verify.sh --offline
make generate-provenance
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
gofmt -w $(find cmd internal migrations tests scripts -name '*.go' -type f)
go vet ./...
go test -race ./...
BUILDX_NO_DEFAULT_ATTESTATIONS=1 docker buildx bake -f deploy/docker/docker-bake.hcl --load
docker compose -f deploy/compose.yaml config --quiet
```

Compose uses full `repository:tag@sha256:digest` references on `linux/amd64`. Never replace one with a floating tag. Default BuildKit attestations are disabled because their generated metadata changes the manifest-list digest across otherwise identical builds; Denyra embeds its verified immutable provenance document in each custom image instead. When a custom image is rebuilt, update `deploy/images.lock.json` and the matching Compose reference before deployment.

## Start and configure

```sh
docker compose -f deploy/compose.yaml up -d --wait
docker compose -f deploy/compose.yaml ps
```

Complete these setup steps:

1. Configure Lidarr before enabling imports:

   - Under **Settings > Download Clients**, disable **Completed Download Handling**.
   - Under **Settings > Media Management**, enable **Rename Tracks** and **Import Extra Files**. Set **Extra File Extensions** to `lrc,elrc,ttml`.
   - Set **Standard Track Format** to `{Album Title} ({Release Year})/{Artist Name} - {Album Title} - {track:00} - {Track Title}`.
   - Set **Multi Disc Track Format** to `{Album Title} ({Release Year})/{Medium Format} {medium:00}/{Artist Name} - {Album Title} - {track:00} - {Track Title}`.
   - Under **Settings > Metadata**, enable **Kodi (XBMC) / Emby**, **Artist Images**, and **Album Images**. The NFO metadata options may remain disabled.
   - Confirm that `/data/processing/approved` is the only download/import path visible to Lidarr and `/data/library` is its final library path.

   The media pipeline checks this contract before moving an approved candidate or recording an import intent. Configuration drift blocks the import with an actionable error. Denyra does not change Lidarr settings automatically.
2. Configure the baked Lidarr.Plugin.Slskd integration to reach `slskd` on the acquisition network. Lidarr owns AlbumSearch and the primary queue.
3. Add the following slskd webhook configuration, then restart slskd:

   ```yaml
   integrations:
     webhooks:
       denyra_completion:
         on:
           - DownloadFileComplete
         call:
           url: http://acquisition-gateway:8082/events/slskd
           headers:
             - name: User-Agent
               value: slskd/0.26.0
         timeout: 5000
         retry:
           attempts: 3
   ```

   Port `8082` is reachable only through `denyra-acquisition` and must not be published on the host. The event is only a wake-up signal. Denyra claims nothing until Lidarr.Plugin.Slskd reports the correlated download ID as a completed, healthy batch at `/data/downloads/slskd/lidarr/<download-id>`.
4. Sign in to SFTPGo WebAdmin on port `8080`, create its first admin, then create upload users restricted to `/data/incoming/manual`.
5. Create Navidrome music users on port `4533`. Navidrome owns playback authentication and has `/music` mounted read-only.
6. Sign in to the Denyra Admin UI on port `8090` with the one-time bootstrap account, change its password, then empty the bootstrap secret file in the active secret directory.

If the server already contains music, complete a backup before changing the naming formats. Use Lidarr's Preview Rename on one album first, confirm that each lyric sidecar keeps the same basename as its FLAC file, then organize the remaining artists from Lidarr. The import guard protects new imports; it does not reorganize an existing library.

Check local service state:

```sh
docker compose -f deploy/compose.yaml ps
docker compose -f deploy/compose.yaml logs --since 10m acquisition-gateway media-pipeline
curl --fail http://172.30.0.2:8081/health/ready
curl --fail http://172.30.0.3:8081/health/ready
```

External acquisition outages appear as degraded dependencies and do not fail readiness. Complete the first backup and restore drill before relying on the deployment.
