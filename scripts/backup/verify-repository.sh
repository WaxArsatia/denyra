#!/bin/sh
set -eu
: "${DENYRA_HOME:?set DENYRA_HOME}"
: "${DENYRA_RESTIC_REPOSITORY_PATH:?set DENYRA_RESTIC_REPOSITORY_PATH}"
[ -d "$DENYRA_HOME" ] && [ -d "$DENYRA_RESTIC_REPOSITORY_PATH" ] || { echo "deployment home and repository directories must exist" >&2; exit 1; }
home_real=$(CDPATH= cd -- "$DENYRA_HOME" && pwd -P)
repository_real=$(CDPATH= cd -- "$DENYRA_RESTIC_REPOSITORY_PATH" && pwd -P)
case "$repository_real/" in
  "$home_real/"*) echo "Restic repository must be outside DENYRA_HOME" >&2; exit 1 ;;
esac
