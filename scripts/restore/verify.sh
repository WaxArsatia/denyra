#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"

for variable in DENYRA_RESTORE_SNAPSHOT DENYRA_RESTORE_TARGET; do denyra_restore_require "$variable"; done
denyra_restore_validate_target "$DENYRA_RESTORE_TARGET" existing
manifest=$(find "$DENYRA_RESTORE_TARGET/workspace" -mindepth 2 -maxdepth 2 -type f -name manifest.json)
[ -n "$manifest" ] && [ "$(printf '%s\n' "$manifest" | wc -l)" -eq 1 ] || { echo "exactly one manifest is required" >&2; exit 1; }
backup_workspace=$(dirname -- "$manifest")
rm_report=${DENYRA_RESTORE_REPORT_NAME:-restore-report.json}
cutover_report=${DENYRA_CUTOVER_REPORT_NAME:-cutover-report.md}
[ ! -e "$backup_workspace/$rm_report" ] && [ ! -e "$backup_workspace/$cutover_report" ] || { echo "restore reports already exist" >&2; exit 1; }

denyra_restore_compose_tool -v "$DENYRA_RESTORE_TARGET:/restore" media-pipeline verify \
  --root /restore --snapshot "$DENYRA_RESTORE_SNAPSHOT" \
  --uid "${DENYRA_MEDIA_UID:-1000}" --gid "${DENYRA_MEDIA_GID:-1000}" \
  --report "/restore/workspace/$(basename "$backup_workspace")/$rm_report" \
  --cutover-report "/restore/workspace/$(basename "$backup_workspace")/$cutover_report"
echo "verification report: $backup_workspace/$rm_report"
echo "cutover report: $backup_workspace/$cutover_report"
