#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"

for variable in DENYRA_RESTORE_SNAPSHOT DENYRA_RESTIC_REPOSITORY_PATH DENYRA_RESTIC_PASSWORD_FILE DENYRA_RESTORE_TARGET; do
  denyra_restore_require "$variable"
done
[ -f "$DENYRA_RESTIC_PASSWORD_FILE" ] || { echo "Restic password file is missing" >&2; exit 1; }
[ -d "$DENYRA_RESTIC_REPOSITORY_PATH" ] || { echo "Restic repository is missing" >&2; exit 1; }
denyra_restore_validate_target "$DENYRA_RESTORE_TARGET"

denyra_restore_restic restic check --read-data-subset="${DENYRA_RESTIC_CHECK_SUBSET:-5%}"
denyra_restore_restic -v "$DENYRA_RESTORE_TARGET:/restore" restic restore "$DENYRA_RESTORE_SNAPSHOT" \
  --target /restore --verify --overwrite never

manifest_count=$(find "$DENYRA_RESTORE_TARGET/workspace" -mindepth 2 -maxdepth 2 -type f -name manifest.json | wc -l)
[ "$manifest_count" -eq 1 ] || { echo "restored tree must contain exactly one backup manifest" >&2; exit 1; }
manifest=$(find "$DENYRA_RESTORE_TARGET/workspace" -mindepth 2 -maxdepth 2 -type f -name manifest.json)
backup_workspace=$(dirname -- "$manifest")
mkdir -p "$DENYRA_RESTORE_TARGET/source/state/gateway" "$DENYRA_RESTORE_TARGET/source/state/pipeline"
install -m 0600 "$backup_workspace/gateway.db" "$DENYRA_RESTORE_TARGET/source/state/gateway/denyra.db"
install -m 0600 "$backup_workspace/pipeline.db" "$DENYRA_RESTORE_TARGET/source/state/pipeline/denyra.db"

"$script_dir/verify.sh"
echo "restore verified at $DENYRA_RESTORE_TARGET; cutover remains manual"
