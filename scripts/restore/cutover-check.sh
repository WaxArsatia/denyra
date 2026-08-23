#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"

for variable in DENYRA_RESTORE_SNAPSHOT DENYRA_RESTORE_TARGET; do denyra_restore_require "$variable"; done
export DENYRA_RESTORE_REPORT_NAME=cutover-verification.json
export DENYRA_CUTOVER_REPORT_NAME=cutover-check.md
"$script_dir/verify.sh"
echo "No mounts were changed. Perform cutover manually after reviewing cutover-check.md."
