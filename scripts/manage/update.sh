#!/bin/sh

. "$repo_root/scripts/manage/snapshot.sh"
. "$repo_root/scripts/manage/rollback.sh"
. "$repo_root/scripts/manage/smoke.sh"

denyra_update_remove_snapshot() {
  [ -n "${denyra_update_snapshot:-}" ] || return 0
  [ -d "$denyra_update_snapshot" ] || return 0
  denyra_snapshot_validate_path "$denyra_update_snapshot"
  rm -rf -- "$denyra_update_snapshot"
}

denyra_update_exit() {
  denyra_update_exit_status=$1
  trap - EXIT HUP INT TERM
  if [ "${denyra_update_cutover_started:-false}" = true ] && [ "${denyra_update_healthy:-false}" != true ]; then
    if ! denyra_rollback_to "$denyra_update_snapshot" "interrupted or unhealthy update"; then
      printf 'denyra: automatic rollback failed; snapshot retained at %s\n' "$denyra_update_snapshot" >&2
    fi
  elif [ "${denyra_update_healthy:-false}" != true ]; then
    denyra_update_remove_snapshot
  fi
  denyra_unlock
  exit "$denyra_update_exit_status"
}

denyra_update_active_commit() {
  denyra_update_commit_count=$(sed -n '/^DENYRA_GIT_COMMIT=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)
  [ "$denyra_update_commit_count" -eq 1 ] || denyra_die "active deployment commit is missing; rerun setup"
  denyra_update_active=$(sed -n 's/^DENYRA_GIT_COMMIT=//p' "$DENYRA_CONFIG_DIR/denyra.env")
  denyra_snapshot_validate_commit "$denyra_update_active"
}

denyra_update() {
  [ "$#" -eq 0 ] || denyra_die "update takes no arguments"
  denyra_update_started=$(date +%s)
  denyra_update_cutover_started=false
  denyra_update_healthy=false
  denyra_update_snapshot=
  trap 'denyra_update_exit $?' EXIT
  trap 'exit 130' HUP INT TERM

  git diff --quiet --ignore-submodules -- || denyra_die "tracked source files have local changes"
  git diff --cached --quiet --ignore-submodules -- || denyra_die "tracked source files have staged changes"
  denyra_update_branch=$(git symbolic-ref --quiet --short HEAD) || denyra_die "updates require a checked-out branch"
  denyra_update_active_commit
  denyra_update_pending=$(denyra_snapshot_prepare "$denyra_update_active")
  denyra_update_snapshot=$denyra_update_pending
  git fetch origin "$denyra_update_branch"
  git merge --ff-only "origin/$denyra_update_branch"
  denyra_update_new=$(git rev-parse HEAD)
  denyra_snapshot_validate_commit "$denyra_update_new"
  if [ "$denyra_update_new" = "$denyra_update_active" ]; then
    denyra_update_remove_snapshot
    denyra_update_snapshot=
    trap - EXIT HUP INT TERM
    denyra_unlock
    printf 'already current\n'
    return 0
  fi
  denyra_update_snapshot=$(denyra_snapshot_name "$denyra_update_pending" "$denyra_update_new")
  denyra_update_tag=$(printf '%s' "$denyra_update_new" | cut -c1-12)
  DENYRA_IMAGE_TAG=$denyra_update_tag
  DENYRA_GIT_COMMIT=$denyra_update_new
  DENYRA_RELEASE_REFRESH=update-$(date -u +%Y%m%dT%H%M%SZ)
  export DENYRA_IMAGE_TAG DENYRA_GIT_COMMIT DENYRA_RELEASE_REFRESH

  denyra_compose pull --policy always slskd sftpgo restic
  denyra_compose build --pull

  denyra_update_cutover_started=true
  denyra_compose stop
  denyra_snapshot_capture "$denyra_update_snapshot"
  denyra_set_release_env "$denyra_update_new" "$denyra_update_tag" "$DENYRA_RELEASE_REFRESH"
  denyra_start_all
  denyra_smoke

  denyra_update_healthy=true
  denyra_snapshot_set_status "$denyra_update_snapshot" successful
  trap - EXIT HUP INT TERM
  denyra_snapshot_retain_two
  denyra_unlock
  denyra_update_elapsed=$(($(date +%s) - denyra_update_started))
  printf 'Updated %s -> %s in %ss\n' "$denyra_update_active" "$denyra_update_new" "$denyra_update_elapsed"
}
