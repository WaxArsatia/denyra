#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"

: "${DENYRA_DATA_ROOT:?set DENYRA_DATA_ROOT}"
: "${DENYRA_RESTIC_REPOSITORY_PATH:?set DENYRA_RESTIC_REPOSITORY_PATH}"
: "${DENYRA_INTERNAL_BEARER_FILE:?set DENYRA_INTERNAL_BEARER_FILE}"
: "${DENYRA_RESTIC_PASSWORD_FILE:?set DENYRA_RESTIC_PASSWORD_FILE}"
denyra_require_file "$DENYRA_INTERNAL_BEARER_FILE"
denyra_require_file "$DENYRA_RESTIC_PASSWORD_FILE"
"$script_dir/verify-repository.sh"

DENYRA_BACKUP_ID=${DENYRA_BACKUP_ID:-$(date -u +%Y%m%dT%H%M%SZ)}
export DENYRA_BACKUP_ID
workspace="$DENYRA_DATA_ROOT/backups/$DENYRA_BACKUP_ID"
mkdir -m 0700 -p "$workspace"
compose=${DENYRA_COMPOSE_FILE:-deploy/compose.yaml}
gateway=${DENYRA_GATEWAY_URL:-http://172.30.0.2:8081}
pipeline=${DENYRA_PIPELINE_URL:-http://172.30.0.3:8081}
success=false

cleanup() {
  docker compose -f "$compose" start lidarr navidrome sftpgo slskd >/dev/null 2>&1 || true
  denyra_api "$gateway/internal/maintenance" "$(denyra_json false backup-complete)" >/dev/null 2>&1 || true
  denyra_api "$pipeline/internal/maintenance" "$(denyra_json false backup-complete)" >/dev/null 2>&1 || true
  if [ "$success" = true ]; then rm -r "$workspace"; else echo "backup failed; evidence retained at $workspace" >&2; fi
}
trap cleanup EXIT HUP INT TERM

denyra_wait_safe "$gateway/internal/maintenance" "$workspace/gateway-maintenance.json"
denyra_wait_safe "$pipeline/internal/maintenance" "$workspace/pipeline-maintenance.json"
docker compose -f "$compose" stop lidarr navidrome sftpgo slskd
denyra_api "$gateway/internal/maintenance/backup" "{\"target\":\"/data/backups/$DENYRA_BACKUP_ID/gateway.db\"}" > "$workspace/gateway-backup.json"
denyra_api "$pipeline/internal/maintenance/backup" "{\"target\":\"/data/backups/$DENYRA_BACKUP_ID/pipeline.db\"}" > "$workspace/pipeline-backup.json"

cp dependencies.lock.json "$workspace/dependencies.lock.json"
cp deploy/images.lock.json "$workspace/images.lock.json"
cp deploy/docker/generated/gateway-build-provenance.json "$workspace/gateway-build-provenance.json"
cp deploy/docker/generated/pipeline-build-provenance.json "$workspace/pipeline-build-provenance.json"
cp deploy/config/gateway.toml "$workspace/gateway.toml"
cp deploy/config/pipeline.toml "$workspace/pipeline.toml"
denyra_restore_tool \
  -v "$DENYRA_DATA_ROOT:/source:ro" -v "$workspace:/workspace" \
  media-pipeline create --source /source --workspace /workspace --backup-id "$DENYRA_BACKUP_ID"

denyra_restic backup /source/library /source/state /source/incoming /source/processing /source/quarantine /workspace/"$DENYRA_BACKUP_ID" \
  --exclude /source/downloads --exclude /source/state/gateway/denyra.db --exclude '/source/state/gateway/denyra.db-*' \
  --exclude /source/state/pipeline/denyra.db --exclude '/source/state/pipeline/denyra.db-*'
denyra_restic check --read-data-subset="${DENYRA_RESTIC_CHECK_SUBSET:-5%}"
denyra_restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 12 --prune
denyra_restic snapshots --latest 1 > "$workspace/restic-snapshot.txt"
success=true
