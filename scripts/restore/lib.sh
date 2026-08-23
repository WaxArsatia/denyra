#!/bin/sh
set -eu

denyra_restore_require() {
  name=$1
  value=$(printenv "$name" || true)
  [ -n "$value" ] || { echo "set $name" >&2; exit 1; }
  case "$value" in *'$'*|'~'*) echo "$name contains an unresolved path expression" >&2; exit 1;; esac
}

denyra_restore_validate_target() {
  target=$1
  content_policy=${2:-empty}
  case "$target" in /*) ;; *) echo "restore target must be absolute" >&2; exit 1;; esac
  clean=$(realpath -m -- "$target")
  [ "$clean" = "$target" ] || { echo "restore target must already be canonical" >&2; exit 1; }
  [ "$clean" != / ] || { echo "restore target cannot be /" >&2; exit 1; }
  live=$(realpath -m -- "${DENYRA_DATA_ROOT:-/data}")
  [ "$clean" != "$live" ] || { echo "restore target cannot be the live data tree" >&2; exit 1; }
  workspace=$(realpath -m -- "$script_dir/../..")
  [ "$clean" != "$workspace" ] || { echo "restore target cannot be the workspace root" >&2; exit 1; }
  user_home=$(getent passwd "$(id -u)" | cut -d: -f6)
  [ -z "$user_home" ] || [ "$clean" != "$(realpath -m -- "$user_home")" ] || { echo "restore target cannot be the user home" >&2; exit 1; }
  [ ! -L "$clean" ] || { echo "restore target cannot be a symlink" >&2; exit 1; }
  parent=$(dirname -- "$clean")
  [ -d "$parent" ] && [ ! -L "$parent" ] || { echo "restore parent must be an existing real directory" >&2; exit 1; }
  [ "$(realpath -e -- "$parent")" = "$parent" ] || { echo "restore parent contains a symlink" >&2; exit 1; }
  if [ -e "$clean" ]; then
    [ -d "$clean" ] || { echo "restore target must be a directory" >&2; exit 1; }
    if [ "$content_policy" = empty ]; then
      [ -z "$(find "$clean" -mindepth 1 -maxdepth 1 -print -quit)" ] || { echo "restore target must be empty" >&2; exit 1; }
    fi
  else
    mkdir -m 0700 -- "$clean"
  fi
}

denyra_restore_compose_tool() {
  docker compose -f "${DENYRA_COMPOSE_FILE:-deploy/compose.yaml}" run --rm --no-deps \
    --entrypoint /app/denyra-restore-check "$@"
}

denyra_restore_restic() {
  docker compose -f "${DENYRA_COMPOSE_FILE:-deploy/compose.yaml}" --profile backup run --rm "$@"
}
