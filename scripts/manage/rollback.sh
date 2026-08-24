#!/bin/sh

. "$repo_root/scripts/manage/snapshot.sh"
. "$repo_root/scripts/manage/smoke.sh"

denyra_rollback_image() {
  denyra_rollback_image_service=$1
  denyra_rollback_image_file=$2
  denyra_rollback_image_value=$(awk -v service="$denyra_rollback_image_service" '
    $0 == "  " service ":" { wanted=1; next }
    wanted && /^    image: sha256:[0-9a-f]+$/ { print substr($0, 12); found++; wanted=0; next }
    wanted { exit 41 }
    END { if (found != 1) exit 42 }
  ' "$denyra_rollback_image_file") || denyra_die "prior image record invalid: $denyra_rollback_image_service"
  denyra_rollback_digest=${denyra_rollback_image_value#sha256:}
  [ "${#denyra_rollback_digest}" -eq 64 ] || denyra_die "prior image record invalid: $denyra_rollback_image_service"
  case "$denyra_rollback_digest" in *[!0-9a-f]*) denyra_die "prior image record invalid: $denyra_rollback_image_service" ;; esac
  if ! docker image inspect "$denyra_rollback_image_value" >/dev/null 2>&1; then
    denyra_die "prior image missing: $denyra_rollback_image_service $denyra_rollback_image_value"
  fi
}

denyra_rollback_to() {
  denyra_rollback_snapshot=$1
  denyra_rollback_reason=$2
  denyra_snapshot_load_metadata "$denyra_rollback_snapshot"
  denyra_rollback_original_status=$denyra_snapshot_status
  denyra_compose stop >/dev/null 2>&1 || true
  case "$denyra_rollback_original_status" in
    prepared) ;;
    snapshotted|successful) denyra_snapshot_restore "$denyra_rollback_snapshot" ;;
    *) denyra_die "snapshot cannot be rolled back from status: $denyra_rollback_original_status" ;;
  esac
  for denyra_rollback_service in acquisition-gateway media-pipeline lidarr slskd sftpgo navidrome
  do
    denyra_rollback_image "$denyra_rollback_service" "$denyra_rollback_snapshot/prior-images.yaml"
  done
  denyra_compose_snapshot "$denyra_rollback_snapshot" up -d --remove-orphans --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}"
  denyra_smoke "$denyra_rollback_snapshot"
  denyra_snapshot_set_status "$denyra_rollback_snapshot" rolled_back
  printf 'Rollback complete: %s\n' "$denyra_rollback_reason"
  if [ "$denyra_rollback_original_status" != prepared ]; then
    printf 'Failed config retained: %s/failed-config\n' "$denyra_rollback_snapshot"
    printf 'Failed state retained: %s/failed-state\n' "$denyra_rollback_snapshot"
  fi
}

denyra_rollback() {
  denyra_rollback_snapshot=$(denyra_snapshot_latest)
  [ -n "$denyra_rollback_snapshot" ] || denyra_die "no successful update snapshot is available"
  denyra_snapshot_load_metadata "$denyra_rollback_snapshot"
  printf 'Current release: %s\nPrior release:   %s\n' "$denyra_snapshot_new_commit" "$denyra_snapshot_old_commit"
  printf 'Rollback will discard service-state writes made after this update. Continue? [y/N] '
  IFS= read -r denyra_rollback_answer
  case "$denyra_rollback_answer" in
    y|Y|yes|YES|Yes) ;;
    *) printf 'rollback cancelled\n'; denyra_unlock; return 0 ;;
  esac
  denyra_rollback_to "$denyra_rollback_snapshot" "manual rollback"
  denyra_unlock
}
