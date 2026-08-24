#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"

: "${DENYRA_DATA_ROOT:?set DENYRA_DATA_ROOT}"
: "${DENYRA_CONFIG_DIR:?set DENYRA_CONFIG_DIR}"
: "${DENYRA_COMPOSE_FILE:?set DENYRA_COMPOSE_FILE}"
: "${DENYRA_REPO_ROOT:?set DENYRA_REPO_ROOT}"
: "${DENYRA_RESTIC_REPOSITORY_PATH:?set DENYRA_RESTIC_REPOSITORY_PATH}"
: "${DENYRA_INTERNAL_BEARER_FILE:?set DENYRA_INTERNAL_BEARER_FILE}"
: "${DENYRA_RESTIC_PASSWORD_FILE:?set DENYRA_RESTIC_PASSWORD_FILE}"
denyra_require_file "$DENYRA_INTERNAL_BEARER_FILE"
denyra_require_file "$DENYRA_RESTIC_PASSWORD_FILE"
"$script_dir/verify-repository.sh"

DENYRA_BACKUP_ID=${DENYRA_BACKUP_ID:-$(date -u +%Y%m%dT%H%M%SZ)}
case "$DENYRA_BACKUP_ID" in ''|*[!A-Za-z0-9._-]*) echo "invalid backup ID" >&2; exit 1 ;; esac
export DENYRA_BACKUP_ID
workspace="$DENYRA_DATA_ROOT/backups/$DENYRA_BACKUP_ID"
mkdir -m 0700 -p "$workspace"
gateway=${DENYRA_GATEWAY_URL:-http://acquisition-gateway:8081}
pipeline=${DENYRA_PIPELINE_URL:-http://media-pipeline:8081}
success=false

cleanup() {
  denyra_backup_compose start lidarr navidrome sftpgo slskd >/dev/null 2>&1 || true
  denyra_api "$gateway/internal/maintenance" "$(denyra_json false backup-complete)" >/dev/null 2>&1 || true
  denyra_api "$pipeline/internal/maintenance" "$(denyra_json false backup-complete)" >/dev/null 2>&1 || true
  if [ "$success" = true ]; then rm -r -- "$workspace"; else echo "backup failed; evidence retained at $workspace" >&2; fi
}
trap cleanup EXIT HUP INT TERM

denyra_wait_safe "$gateway/internal/maintenance" "$workspace/gateway-maintenance.json"
denyra_wait_safe "$pipeline/internal/maintenance" "$workspace/pipeline-maintenance.json"
denyra_backup_compose stop lidarr navidrome sftpgo slskd
denyra_api "$gateway/internal/maintenance/backup" "{\"target\":\"/data/backups/$DENYRA_BACKUP_ID/gateway.db\"}" > "$workspace/gateway-backup.json"
denyra_api "$pipeline/internal/maintenance/backup" "{\"target\":\"/data/backups/$DENYRA_BACKUP_ID/pipeline.db\"}" > "$workspace/pipeline-backup.json"

mkdir -m 0700 "$workspace/config"
cp -a -- "$DENYRA_CONFIG_DIR/." "$workspace/config/"
git_commit=$(git -C "$DENYRA_REPO_ROOT" rev-parse HEAD)
denyra_restore_tool \
  -v "$DENYRA_DATA_ROOT:/data-source:ro" -v "$workspace:/workspace" \
  media-pipeline create --source /data-source --workspace /workspace \
  --backup-id "$DENYRA_BACKUP_ID" --git-commit "$git_commit"

denyra_restic backup /source/config /source/secrets /source/data/library /source/data/library-unmanaged /source/data/state \
  /source/data/incoming /source/data/processing /source/data/quarantine /workspace/$DENYRA_BACKUP_ID \
  --exclude /source/credentials.txt --exclude /source/data/downloads --exclude /source/data/cache \
  --exclude /source/updates --exclude /source/data/backups \
  --exclude /source/data/state/gateway/denyra.db --exclude '/source/data/state/gateway/denyra.db-*' \
  --exclude /source/data/state/pipeline/denyra.db --exclude '/source/data/state/pipeline/denyra.db-*'
denyra_restic check --read-data-subset="${DENYRA_RESTIC_CHECK_SUBSET:-5%}"
denyra_restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 12 --prune
denyra_restic snapshots --latest 1 > "$workspace/restic-snapshot.txt"
success=true
