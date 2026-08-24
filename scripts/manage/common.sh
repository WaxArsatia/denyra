#!/bin/sh

denyra_die() {
  printf 'denyra: %s\n' "$*" >&2
  exit 1
}

denyra_usage() {
  cat <<'EOF'
usage: ./denyra COMMAND [ARGS...]

Commands:
  setup       Create or reconcile the deployment
  start       Start all services
  stop        Stop all services
  restart     Restart all services
  status      Show service status
  logs        Show recent service logs
  update      Update images and restart safely
  rollback    Restore the previous release
  credentials Show generated credentials
  backup      Create a backup
EOF
}

denyra_context() {
  if [ -z "${DENYRA_HOME:-}" ] && [ -f "$repo_root/.denyra-home" ]; then
    IFS= read -r DENYRA_HOME < "$repo_root/.denyra-home"
  fi
  DENYRA_HOME=${DENYRA_HOME:-/srv/denyra}
  case "$DENYRA_HOME" in
    /*) ;;
    *) denyra_die "DENYRA_HOME must be an absolute path" ;;
  esac
  case "$DENYRA_HOME" in
    /|*'$'*|*'~'*) denyra_die "DENYRA_HOME is unsafe" ;;
  esac

  DENYRA_CONFIG_DIR=$DENYRA_HOME/config
  DENYRA_SECRETS_DIR=$DENYRA_HOME/secrets
  if [ -z "${DENYRA_DATA_ROOT:-}" ] && [ -f "$DENYRA_CONFIG_DIR/denyra.env" ]; then
    data_root_count=$(sed -n '/^DENYRA_DATA_ROOT=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)
    [ "$data_root_count" -eq 1 ] || denyra_die "denyra.env has an invalid data root"
    DENYRA_DATA_ROOT=$(sed -n 's/^DENYRA_DATA_ROOT=//p' "$DENYRA_CONFIG_DIR/denyra.env")
  fi
  DENYRA_DATA_ROOT=${DENYRA_DATA_ROOT:-$DENYRA_HOME/data}
  case "$DENYRA_DATA_ROOT" in
    /*) ;;
    *) denyra_die "DENYRA_DATA_ROOT must be an absolute path" ;;
  esac
  [ "$DENYRA_DATA_ROOT" != / ] || denyra_die "DENYRA_DATA_ROOT cannot be /"

  DENYRA_UPDATES_DIR=$DENYRA_HOME/updates
  export DENYRA_HOME DENYRA_CONFIG_DIR DENYRA_SECRETS_DIR DENYRA_DATA_ROOT DENYRA_UPDATES_DIR
}

denyra_lock() {
  mkdir "$DENYRA_HOME/.operation-lock" 2>/dev/null || denyra_die "another Denyra operation is running"
  trap 'denyra_unlock' EXIT HUP INT TERM
}

denyra_unlock() {
  rmdir "$DENYRA_HOME/.operation-lock" 2>/dev/null || true
  trap - EXIT HUP INT TERM
}

denyra_atomic_file() {
  denyra_atomic_target=$1
  denyra_atomic_mode=$2
  denyra_atomic_temporary=$denyra_atomic_target.tmp.$$
  umask 077
  trap 'rm -f "$denyra_atomic_temporary"' HUP INT TERM
  cat > "$denyra_atomic_temporary"
  chmod "$denyra_atomic_mode" "$denyra_atomic_temporary"
  mv "$denyra_atomic_temporary" "$denyra_atomic_target"
  trap - HUP INT TERM
}

denyra_secret() {
  denyra_secret_name=$1
  denyra_secret_bytes=${2:-32}
  denyra_secret_target=$DENYRA_SECRETS_DIR/$denyra_secret_name
  [ -s "$denyra_secret_target" ] && return 0
  od -An -N"$denyra_secret_bytes" -tx1 /dev/urandom | tr -d ' \n' | denyra_atomic_file "$denyra_secret_target" 0600
}

denyra_compose() {
  docker compose --project-name denyra --env-file "$DENYRA_CONFIG_DIR/denyra.env" -f "$repo_root/deploy/compose.yaml" "$@"
}

denyra_unavailable() {
  denyra_die "command unavailable in this checkout"
}
