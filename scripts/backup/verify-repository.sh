#!/bin/sh
set -eu
: "${DENYRA_DATA_ROOT:?set DENYRA_DATA_ROOT}"
: "${DENYRA_RESTIC_REPOSITORY_PATH:?set DENYRA_RESTIC_REPOSITORY_PATH}"
[ -d "$DENYRA_DATA_ROOT" ] && [ -d "$DENYRA_RESTIC_REPOSITORY_PATH" ] || { echo "data and repository directories must exist" >&2; exit 1; }
data_device=$(stat -c %d "$DENYRA_DATA_ROOT")
repository_device=$(stat -c %d "$DENYRA_RESTIC_REPOSITORY_PATH")
[ "$data_device" != "$repository_device" ] || { echo "Restic repository must be on a different filesystem device" >&2; exit 1; }
