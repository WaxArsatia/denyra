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
  if [ -f "$DENYRA_CONFIG_DIR/denyra.env" ]; then
    if [ -z "${DENYRA_PROJECT_NAME:-}" ]; then
      denyra_project_count=$(sed -n '/^DENYRA_PROJECT_NAME=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)
      [ "$denyra_project_count" -le 1 ] || denyra_die "denyra.env has an invalid project name"
      [ "$denyra_project_count" -eq 0 ] || DENYRA_PROJECT_NAME=$(sed -n 's/^DENYRA_PROJECT_NAME=//p' "$DENYRA_CONFIG_DIR/denyra.env")
    fi
    if [ -z "${DENYRA_COMPOSE_OVERRIDE:-}" ]; then
      denyra_override_count=$(sed -n '/^DENYRA_COMPOSE_OVERRIDE=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)
      [ "$denyra_override_count" -le 1 ] || denyra_die "denyra.env has an invalid compose override"
      [ "$denyra_override_count" -eq 0 ] || DENYRA_COMPOSE_OVERRIDE=$(sed -n 's/^DENYRA_COMPOSE_OVERRIDE=//p' "$DENYRA_CONFIG_DIR/denyra.env")
    fi
  fi
  DENYRA_DATA_ROOT=${DENYRA_DATA_ROOT:-$DENYRA_HOME/data}
  case "$DENYRA_DATA_ROOT" in
    /*) ;;
    *) denyra_die "DENYRA_DATA_ROOT must be an absolute path" ;;
  esac
  [ "$DENYRA_DATA_ROOT" != / ] || denyra_die "DENYRA_DATA_ROOT cannot be /"

  DENYRA_PROJECT_NAME=${DENYRA_PROJECT_NAME:-denyra}
  case "$DENYRA_PROJECT_NAME" in ''|*[!A-Za-z0-9_.-]*) denyra_die "DENYRA_PROJECT_NAME is invalid" ;; esac
  if [ -n "${DENYRA_COMPOSE_OVERRIDE:-}" ]; then
    case "$DENYRA_COMPOSE_OVERRIDE" in /*) ;; *) denyra_die "DENYRA_COMPOSE_OVERRIDE must be an absolute path" ;; esac
  fi

  DENYRA_UPDATES_DIR=$DENYRA_HOME/updates
  export DENYRA_HOME DENYRA_CONFIG_DIR DENYRA_SECRETS_DIR DENYRA_DATA_ROOT DENYRA_UPDATES_DIR DENYRA_PROJECT_NAME DENYRA_COMPOSE_OVERRIDE
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
  if ! cat > "$denyra_atomic_temporary"; then
    rm -f "$denyra_atomic_temporary"
    return 1
  fi
  if ! chmod "$denyra_atomic_mode" "$denyra_atomic_temporary" || ! mv "$denyra_atomic_temporary" "$denyra_atomic_target"; then
    rm -f "$denyra_atomic_temporary"
    return 1
  fi
}

denyra_secret() {
  denyra_secret_name=$1
  denyra_secret_bytes=${2:-32}
  denyra_secret_target=$DENYRA_SECRETS_DIR/$denyra_secret_name
  [ -s "$denyra_secret_target" ] && return 0
  od -An -N"$denyra_secret_bytes" -tx1 /dev/urandom | tr -d ' \n' | denyra_atomic_file "$denyra_secret_target" 0600
}

denyra_compose() {
  denyra_project_name=${DENYRA_PROJECT_NAME:-denyra}
  if [ -n "${DENYRA_COMPOSE_OVERRIDE:-}" ]; then
    case "$DENYRA_COMPOSE_OVERRIDE" in
      /*) ;;
      *) denyra_die "DENYRA_COMPOSE_OVERRIDE must be an absolute path" ;;
    esac
    docker compose --project-name "$denyra_project_name" --env-file "$DENYRA_CONFIG_DIR/denyra.env" -f "$repo_root/deploy/compose.yaml" -f "$DENYRA_COMPOSE_OVERRIDE" "$@"
    return
  fi
  docker compose --project-name "$denyra_project_name" --env-file "$DENYRA_CONFIG_DIR/denyra.env" -f "$repo_root/deploy/compose.yaml" "$@"
}

denyra_compose_snapshot() {
  denyra_compose_snapshot_path=$1
  shift
  docker compose --project-name "${DENYRA_PROJECT_NAME:-denyra}" \
    -f "$denyra_compose_snapshot_path/prior-compose.yaml" \
    -f "$denyra_compose_snapshot_path/prior-images.yaml" "$@"
}

denyra_start_dependencies() {
  denyra_compose up -d --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}" lidarr slskd sftpgo navidrome
}

denyra_start_all() {
  denyra_start_dependencies
  denyra_compose up -d --remove-orphans --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}"
}

denyra_set_release_env() {
  denyra_release_commit=$1
  denyra_release_tag=$2
  denyra_release_refresh=$3
  denyra_release_temporary=$DENYRA_CONFIG_DIR/denyra.env.tmp.$$
  if ! awk -v commit="$denyra_release_commit" -v tag="$denyra_release_tag" -v refresh="$denyra_release_refresh" '
    BEGIN { commit_seen=0; tag_seen=0; refresh_seen=0 }
    /^DENYRA_GIT_COMMIT=/ { print "DENYRA_GIT_COMMIT=" commit; commit_seen++; next }
    /^DENYRA_IMAGE_TAG=/ { print "DENYRA_IMAGE_TAG=" tag; tag_seen++; next }
    /^DENYRA_RELEASE_REFRESH=/ { print "DENYRA_RELEASE_REFRESH=" refresh; refresh_seen++; next }
    { print }
    END {
      if (commit_seen != 1 || tag_seen != 1 || refresh_seen > 1) exit 42
      if (refresh_seen == 0) print "DENYRA_RELEASE_REFRESH=" refresh
    }
  ' "$DENYRA_CONFIG_DIR/denyra.env" > "$denyra_release_temporary"; then
    rm -f "$denyra_release_temporary"
    return 1
  fi
  if ! chmod 0640 "$denyra_release_temporary" || ! mv "$denyra_release_temporary" "$DENYRA_CONFIG_DIR/denyra.env"; then
    rm -f "$denyra_release_temporary"
    return 1
  fi
}

denyra_unavailable() {
  denyra_die "command unavailable in this checkout"
}
