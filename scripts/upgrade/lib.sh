#!/bin/sh
set -eu

denyra_upgrade_require_file() {
  name=$1
  value=$(printenv "$name" || true)
  [ -n "$value" ] && [ -f "$value" ] || { echo "$name must name an existing file" >&2; exit 1; }
}

denyra_upgrade_require_directory() {
  name=$1
  value=$(printenv "$name" || true)
  [ -n "$value" ] && [ -d "$value" ] || { echo "$name must name an existing directory" >&2; exit 1; }
}

denyra_upgrade_lock_hash() { sha256sum "$1" | awk '{print $1}'; }
