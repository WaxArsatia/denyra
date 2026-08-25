#!/bin/sh

. "$repo_root/scripts/manage/smoke.sh"

denyra_update_exit() {
  denyra_update_exit_status=$1
  trap - EXIT HUP INT TERM
  if [ "$denyra_update_exit_status" -ne 0 ] && [ "${denyra_update_healthy:-false}" != true ]; then
    printf 'denyra: update failed\n' >&2
    printf 'phase=%s\naffected=%s\ndeployed_commit=%s\n' \
      "${denyra_update_phase:-validate}" "${denyra_update_affected:-deployment}" "${denyra_update_deployed:-unknown}" >&2
    printf 'logs=./denyra logs %s\nretry=./denyra update\n' \
      "${denyra_update_log_service:-acquisition-gateway}" >&2
  fi
  denyra_unlock
  exit "$denyra_update_exit_status"
}

denyra_update_release_values() {
  denyra_update_commit_count=$(sed -n '/^DENYRA_GIT_COMMIT=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)
  denyra_update_tag_count=$(sed -n '/^DENYRA_IMAGE_TAG=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)
  [ "$denyra_update_commit_count" -eq 1 ] || denyra_die "active deployment commit is missing; rerun setup"
  [ "$denyra_update_tag_count" -eq 1 ] || denyra_die "active deployment image tag is missing; rerun setup"
  denyra_update_deployed=$(sed -n 's/^DENYRA_GIT_COMMIT=//p' "$DENYRA_CONFIG_DIR/denyra.env")
  denyra_update_deployed_tag=$(sed -n 's/^DENYRA_IMAGE_TAG=//p' "$DENYRA_CONFIG_DIR/denyra.env")
  denyra_validate_commit "$denyra_update_deployed"
  [ -n "$denyra_update_deployed_tag" ] || denyra_die "active deployment image tag is empty; rerun setup"
}

denyra_update_stack_healthy() {
  denyra_update_health=$(denyra_compose ps --format json 2>/dev/null) || return 1
  [ -n "$denyra_update_health" ] || return 1
  denyra_smoke >/dev/null 2>&1
}

denyra_update_quiet_crash_loops() {
  denyra_update_restart_limit=${DENYRA_RESTART_LIMIT:-3}
  for denyra_update_service in acquisition-gateway media-pipeline lidarr slskd sftpgo navidrome; do
    denyra_update_container=$(denyra_compose ps -q "$denyra_update_service" 2>/dev/null) || continue
    [ -n "$denyra_update_container" ] || continue
    denyra_update_restarts=$(docker inspect --format '{{.RestartCount}}' "$denyra_update_container" 2>/dev/null) || continue
    case "$denyra_update_restarts" in ''|*[!0-9]*) continue ;; esac
    if [ "$denyra_update_restarts" -ge "$denyra_update_restart_limit" ]; then
      printf 'denyra: stopping crash-looping service %s after %s restarts\n' "$denyra_update_service" "$denyra_update_restarts" >&2
      denyra_compose stop "$denyra_update_service" >/dev/null 2>&1 || true
    fi
  done
}

denyra_cleanup_images() {
  denyra_cleanup_running=
  for denyra_cleanup_container in $(docker ps --quiet 2>/dev/null); do
    denyra_cleanup_image=$(docker inspect --format '{{.Image}}' "$denyra_cleanup_container" 2>/dev/null) || continue
    denyra_cleanup_running="$denyra_cleanup_running $denyra_cleanup_image "
  done
  for denyra_cleanup_image in $(docker image ls --no-trunc --filter label=io.denyra.project=denyra --quiet 2>/dev/null); do
    case "$denyra_cleanup_running" in
      *" $denyra_cleanup_image "*) continue ;;
    esac
    if ! docker image rm "$denyra_cleanup_image" >/dev/null; then
      printf 'denyra: warning: retained image %s because Docker reports it in use\n' "$denyra_cleanup_image" >&2
    fi
  done
}

denyra_update() {
  [ "$#" -eq 0 ] || denyra_die "update takes no arguments"
  denyra_update_started=$(date +%s)
  denyra_update_phase=validate
  denyra_update_affected=source-and-release
  denyra_update_log_service=acquisition-gateway
  denyra_update_deployed=unknown
  denyra_update_healthy=false
  trap 'denyra_update_exit $?' EXIT
  trap 'exit 130' HUP INT TERM

  denyra_update_release_values
  denyra_update_previous=$denyra_update_deployed
  git diff --quiet --ignore-submodules -- || denyra_die "tracked source files have local changes"
  git diff --cached --quiet --ignore-submodules -- || denyra_die "tracked source files have staged changes"
  denyra_update_branch=$(git symbolic-ref --quiet --short HEAD) || denyra_die "updates require a checked-out branch"

  denyra_update_phase=fetch
  denyra_update_affected=source
  git fetch origin "$denyra_update_branch"
  git merge --ff-only "origin/$denyra_update_branch"
  denyra_update_selected=$(git rev-parse HEAD)
  denyra_validate_commit "$denyra_update_selected"

  denyra_update_phase=render
  denyra_update_affected=compose-model
  DENYRA_IMAGE_TAG=$(printf '%s' "$denyra_update_selected" | cut -c1-12)
  DENYRA_GIT_COMMIT=$denyra_update_selected
  DENYRA_RELEASE_REFRESH=update-$(date -u +%Y%m%dT%H%M%SZ)
  export DENYRA_IMAGE_TAG DENYRA_GIT_COMMIT DENYRA_RELEASE_REFRESH
  denyra_compose config --quiet

  if [ "$denyra_update_selected" = "$denyra_update_deployed" ] && denyra_update_stack_healthy; then
    denyra_update_healthy=true
    trap - EXIT HUP INT TERM
    denyra_unlock
    printf 'already current\n'
    return 0
  fi

  denyra_update_phase=pull
  denyra_update_affected=external-images
	denyra_compose pull --policy always slskd sftpgo

  denyra_update_phase=build
  denyra_update_affected=denyra-images
  denyra_compose build --pull

  denyra_update_phase=activate
  denyra_update_affected=release-environment
  denyra_update_tag=$(printf '%s' "$denyra_update_selected" | cut -c1-12)
  denyra_set_release_env "$denyra_update_selected" "$denyra_update_tag" "$DENYRA_RELEASE_REFRESH"
  denyra_update_deployed=$denyra_update_selected

  denyra_update_phase=recreate
  denyra_update_affected=compose-project
  if ! denyra_compose up -d --remove-orphans --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}"; then
    denyra_update_quiet_crash_loops
    return 1
  fi

  denyra_update_phase=smoke
  denyra_update_affected=all-services
  denyra_smoke

  denyra_update_phase=cleanup
  denyra_update_affected=unreferenced-denyra-images
  denyra_cleanup_images

  denyra_update_healthy=true
  trap - EXIT HUP INT TERM
  denyra_unlock
  denyra_update_elapsed=$(($(date +%s) - denyra_update_started))
  printf 'Updated %s -> %s in %ss\n' "$denyra_update_previous" "$denyra_update_selected" "$denyra_update_elapsed"
}
