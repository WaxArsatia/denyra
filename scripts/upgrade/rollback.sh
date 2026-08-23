#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
cd "$repo_root"

denyra_upgrade_require_directory DENYRA_UPGRADE_EVIDENCE_DIR
denyra_upgrade_require_directory DENYRA_VERIFIED_BACKUP_DIR
: "${DENYRA_DATA_ROOT:?set DENYRA_DATA_ROOT}"
[ -f "$DENYRA_UPGRADE_EVIDENCE_DIR/prior-compose.yaml" ] || { echo "prior-compose.yaml is missing" >&2; exit 1; }
for database in gateway.db pipeline.db; do
  [ -f "$DENYRA_VERIFIED_BACKUP_DIR/$database" ] || { echo "prior $database is missing" >&2; exit 1; }
done

mode=$(docker compose -f deploy/compose.yaml run --rm --no-deps \
  --entrypoint /app/denyra-restore-check \
  -v "$DENYRA_DATA_ROOT/state:/current:ro" -v "$DENYRA_VERIFIED_BACKUP_DIR:/prior:ro" \
  media-pipeline schema-compatible \
  --current-gateway /current/gateway/denyra.db --current-pipeline /current/pipeline/denyra.db \
  --prior-gateway /prior/gateway.db --prior-pipeline /prior/pipeline.db)
case "$mode" in
  BINARY_ONLY)
    docker compose -f "$DENYRA_UPGRADE_EVIDENCE_DIR/prior-compose.yaml" up -d --remove-orphans --wait
    ;;
  RESTORE_DATABASE_TREE)
    echo "RESTORE_DATABASE_TREE: binary rollback is unsafe; follow docs/runbooks/restore.md with the prior snapshot" >&2
    exit 3
    ;;
  *) echo "rollback compatibility check returned an invalid result" >&2; exit 1;;
esac
