#!/bin/sh

denyra_snapshot_validate_commit() {
  denyra_snapshot_commit=$1
  [ "${#denyra_snapshot_commit}" -eq 40 ] || denyra_die "commit must be 40 lowercase hexadecimal characters"
  case "$denyra_snapshot_commit" in *[!0-9a-f]*) denyra_die "commit must be 40 lowercase hexadecimal characters" ;; esac
}

denyra_snapshot_validate_updates_root() {
  [ -d "$DENYRA_UPDATES_DIR" ] || denyra_die "updates directory is missing"
  [ ! -L "$DENYRA_UPDATES_DIR" ] || denyra_die "updates directory must not be a symlink"
  denyra_snapshot_updates_real=$(CDPATH= cd -- "$DENYRA_UPDATES_DIR" && pwd -P)
  [ "$denyra_snapshot_updates_real" = "$DENYRA_UPDATES_DIR" ] || denyra_die "updates directory must be canonical"
}

denyra_snapshot_validate_path() {
  denyra_snapshot_path=$1
  denyra_snapshot_validate_updates_root
  [ -d "$denyra_snapshot_path" ] || denyra_die "snapshot directory is missing"
  [ ! -L "$denyra_snapshot_path" ] || denyra_die "snapshot must not be a symlink"
  denyra_snapshot_parent=$(CDPATH= cd -- "$(dirname -- "$denyra_snapshot_path")" && pwd -P)
  [ "$denyra_snapshot_parent" = "$denyra_snapshot_updates_real" ] || denyra_die "snapshot is outside updates directory"
}

denyra_snapshot_prepare() {
  denyra_snapshot_old_commit=$1
  denyra_snapshot_validate_commit "$denyra_snapshot_old_commit"
  mkdir -p "$DENYRA_UPDATES_DIR"
  chmod 0700 "$DENYRA_UPDATES_DIR"
  denyra_snapshot_validate_updates_root
  denyra_snapshot_pending=$DENYRA_UPDATES_DIR/.pending-$denyra_snapshot_old_commit-$$
  [ ! -e "$denyra_snapshot_pending" ] || denyra_die "pending snapshot already exists"
  mkdir "$denyra_snapshot_pending"
  chmod 0700 "$denyra_snapshot_pending"

  denyra_snapshot_compose_temporary=$denyra_snapshot_pending/prior-compose.yaml.tmp
  if ! denyra_compose config > "$denyra_snapshot_compose_temporary"; then
    rm -rf -- "$denyra_snapshot_pending"
    denyra_die "could not render prior compose model"
  fi
  chmod 0600 "$denyra_snapshot_compose_temporary"
  mv "$denyra_snapshot_compose_temporary" "$denyra_snapshot_pending/prior-compose.yaml"
  denyra_snapshot_images_temporary=$denyra_snapshot_pending/prior-images.yaml.tmp
  printf 'services:\n' > "$denyra_snapshot_images_temporary"
  chmod 0600 "$denyra_snapshot_images_temporary"
  for denyra_snapshot_service in acquisition-gateway media-pipeline lidarr slskd sftpgo navidrome
  do
    denyra_snapshot_container=$(denyra_compose ps -q "$denyra_snapshot_service")
    if [ -z "$denyra_snapshot_container" ]; then
      rm -rf -- "$denyra_snapshot_pending"
      denyra_die "prior image container missing: $denyra_snapshot_service"
    fi
    denyra_snapshot_image=$(docker inspect --format '{{.Image}}' "$denyra_snapshot_container")
    denyra_snapshot_digest=${denyra_snapshot_image#sha256:}
    if [ "$denyra_snapshot_digest" = "$denyra_snapshot_image" ] || [ "${#denyra_snapshot_digest}" -ne 64 ]; then
      rm -rf -- "$denyra_snapshot_pending"
      denyra_die "prior image identity invalid: $denyra_snapshot_service $denyra_snapshot_image"
    fi
    case "$denyra_snapshot_digest" in
      *[!0-9a-f]*)
        rm -rf -- "$denyra_snapshot_pending"
        denyra_die "prior image identity invalid: $denyra_snapshot_service $denyra_snapshot_image"
        ;;
    esac
    printf '  %s:\n    image: %s\n' "$denyra_snapshot_service" "$denyra_snapshot_image" >> "$denyra_snapshot_images_temporary"
  done
  mv "$denyra_snapshot_images_temporary" "$denyra_snapshot_pending/prior-images.yaml"
  printf '%s\n' "$denyra_snapshot_pending"
}

denyra_snapshot_name() {
  denyra_snapshot_pending=$1
  denyra_snapshot_new_commit=$2
  denyra_snapshot_validate_commit "$denyra_snapshot_new_commit"
  denyra_snapshot_validate_path "$denyra_snapshot_pending"
  denyra_snapshot_pending_name=$(basename -- "$denyra_snapshot_pending")
  case "$denyra_snapshot_pending_name" in
    .pending-????????????????????????????????????????-*) ;;
    *) denyra_die "invalid pending snapshot name" ;;
  esac
  denyra_snapshot_old_commit=${denyra_snapshot_pending_name#.pending-}
  denyra_snapshot_old_commit=${denyra_snapshot_old_commit%%-*}
  denyra_snapshot_validate_commit "$denyra_snapshot_old_commit"
  denyra_snapshot_stamp=$(date -u +%Y%m%dT%H%M%SZ)
  denyra_snapshot_final=$DENYRA_UPDATES_DIR/$denyra_snapshot_stamp-$denyra_snapshot_old_commit-to-$denyra_snapshot_new_commit
  [ ! -e "$denyra_snapshot_final" ] || denyra_die "snapshot target already exists"
  mv "$denyra_snapshot_pending" "$denyra_snapshot_final"
  denyra_snapshot_write_metadata "$denyra_snapshot_final" "$denyra_snapshot_old_commit" "$denyra_snapshot_new_commit" prepared
  printf '%s\n' "$denyra_snapshot_final"
}

denyra_snapshot_write_metadata() {
  denyra_snapshot_target=$1
  denyra_snapshot_old=$2
  denyra_snapshot_new=$3
  denyra_snapshot_status_value=$4
  denyra_snapshot_created_value=${5:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
  denyra_snapshot_validate_commit "$denyra_snapshot_old"
  denyra_snapshot_validate_commit "$denyra_snapshot_new"
  case "$denyra_snapshot_status_value" in prepared|snapshotted|successful|rolled_back) ;; *) denyra_die "invalid snapshot status" ;; esac
  {
    printf 'snapshot_schema=1\n'
    printf 'old_commit=%s\n' "$denyra_snapshot_old"
    printf 'new_commit=%s\n' "$denyra_snapshot_new"
    printf 'created_at=%s\n' "$denyra_snapshot_created_value"
    printf 'status=%s\n' "$denyra_snapshot_status_value"
  } | denyra_atomic_file "$denyra_snapshot_target/metadata.env" 0600
}

denyra_snapshot_load_metadata() {
  denyra_snapshot_metadata_path=$1
  denyra_snapshot_validate_path "$denyra_snapshot_metadata_path"
  denyra_snapshot_schema=
  denyra_snapshot_old_commit=
  denyra_snapshot_new_commit=
  denyra_snapshot_created_at=
  denyra_snapshot_status=
  while IFS='=' read -r denyra_snapshot_key denyra_snapshot_value
  do
    case "$denyra_snapshot_key" in
      snapshot_schema) [ -z "$denyra_snapshot_schema" ] || denyra_die "duplicate snapshot metadata"; denyra_snapshot_schema=$denyra_snapshot_value ;;
      old_commit) [ -z "$denyra_snapshot_old_commit" ] || denyra_die "duplicate snapshot metadata"; denyra_snapshot_old_commit=$denyra_snapshot_value ;;
      new_commit) [ -z "$denyra_snapshot_new_commit" ] || denyra_die "duplicate snapshot metadata"; denyra_snapshot_new_commit=$denyra_snapshot_value ;;
      created_at) [ -z "$denyra_snapshot_created_at" ] || denyra_die "duplicate snapshot metadata"; denyra_snapshot_created_at=$denyra_snapshot_value ;;
      status) [ -z "$denyra_snapshot_status" ] || denyra_die "duplicate snapshot metadata"; denyra_snapshot_status=$denyra_snapshot_value ;;
      *) denyra_die "unknown snapshot metadata key" ;;
    esac
  done < "$denyra_snapshot_metadata_path/metadata.env"
  [ "$denyra_snapshot_schema" = 1 ] || denyra_die "unsupported snapshot schema"
  denyra_snapshot_validate_commit "$denyra_snapshot_old_commit"
  denyra_snapshot_validate_commit "$denyra_snapshot_new_commit"
  case "$denyra_snapshot_created_at" in ????-??-??T??:??:??Z) ;; *) denyra_die "invalid snapshot timestamp" ;; esac
  case "$denyra_snapshot_status" in prepared|snapshotted|successful|rolled_back) ;; *) denyra_die "invalid snapshot status" ;; esac
}

denyra_snapshot_set_status() {
  denyra_snapshot_status_path=$1
  denyra_snapshot_status_new=$2
  denyra_snapshot_load_metadata "$denyra_snapshot_status_path"
  denyra_snapshot_write_metadata "$denyra_snapshot_status_path" "$denyra_snapshot_old_commit" "$denyra_snapshot_new_commit" "$denyra_snapshot_status_new" "$denyra_snapshot_created_at"
}

denyra_snapshot_capture() {
  denyra_snapshot_capture_path=$1
  denyra_snapshot_load_metadata "$denyra_snapshot_capture_path"
  [ "$denyra_snapshot_status" = prepared ] || denyra_die "snapshot is not prepared"
  [ "$DENYRA_CONFIG_DIR" = "$DENYRA_HOME/config" ] || denyra_die "config root is outside deployment home"
  [ -d "$DENYRA_CONFIG_DIR" ] || denyra_die "config root is missing"
  [ ! -L "$DENYRA_CONFIG_DIR" ] || denyra_die "config root must not be a symlink"
  [ -d "$DENYRA_DATA_ROOT/state" ] || denyra_die "state root is missing"
  [ ! -L "$DENYRA_DATA_ROOT/state" ] || denyra_die "state root must not be a symlink"
  for denyra_snapshot_destination in "$denyra_snapshot_capture_path/config" "$denyra_snapshot_capture_path/state"
  do
    [ ! -e "$denyra_snapshot_destination" ] || denyra_die "snapshot destination is not empty"
    mkdir "$denyra_snapshot_destination"
  done
  cp -a -- "$DENYRA_CONFIG_DIR/." "$denyra_snapshot_capture_path/config/"
  cp -a -- "$DENYRA_DATA_ROOT/state/." "$denyra_snapshot_capture_path/state/"
  denyra_snapshot_set_status "$denyra_snapshot_capture_path" snapshotted
}

denyra_snapshot_restore() {
  denyra_snapshot_restore_path=$1
  denyra_snapshot_load_metadata "$denyra_snapshot_restore_path"
  [ -d "$denyra_snapshot_restore_path/config" ] && [ -d "$denyra_snapshot_restore_path/state" ] || denyra_die "snapshot state is incomplete"
  [ ! -e "$denyra_snapshot_restore_path/failed-config" ] || denyra_die "failed config tree already exists"
  [ ! -e "$denyra_snapshot_restore_path/failed-state" ] || denyra_die "failed state tree already exists"
  mv "$DENYRA_CONFIG_DIR" "$denyra_snapshot_restore_path/failed-config"
  mv "$DENYRA_DATA_ROOT/state" "$denyra_snapshot_restore_path/failed-state"
  mkdir "$DENYRA_CONFIG_DIR" "$DENYRA_DATA_ROOT/state"
  cp -a -- "$denyra_snapshot_restore_path/config/." "$DENYRA_CONFIG_DIR/"
  cp -a -- "$denyra_snapshot_restore_path/state/." "$DENYRA_DATA_ROOT/state/"
}

denyra_snapshot_latest() {
  denyra_snapshot_validate_updates_root
  find "$DENYRA_UPDATES_DIR" -mindepth 1 -maxdepth 1 -type d ! -name '.pending-*' -printf '%f\n' | sort -r | while IFS= read -r denyra_snapshot_entry
  do
    denyra_snapshot_candidate=$DENYRA_UPDATES_DIR/$denyra_snapshot_entry
    denyra_snapshot_load_metadata "$denyra_snapshot_candidate"
    if [ "$denyra_snapshot_status" = successful ]; then
      printf '%s\n' "$denyra_snapshot_candidate"
      break
    fi
  done
}

denyra_snapshot_retain_two() {
  denyra_snapshot_validate_updates_root
  denyra_snapshot_count=0
  find "$DENYRA_UPDATES_DIR" -mindepth 1 -maxdepth 1 -type d ! -name '.pending-*' -printf '%f\n' | sort -r | while IFS= read -r denyra_snapshot_entry
  do
    denyra_snapshot_count=$((denyra_snapshot_count + 1))
    [ "$denyra_snapshot_count" -le 2 ] && continue
    denyra_snapshot_remove=$DENYRA_UPDATES_DIR/$denyra_snapshot_entry
    denyra_snapshot_validate_path "$denyra_snapshot_remove"
    if find "$denyra_snapshot_remove" -type d ! -writable -print -quit | grep -q .; then
      printf 'denyra: snapshot contains directories not writable by the operator: %s\n' "$denyra_snapshot_remove" >&2
      return 1
    fi
    rm -rf -- "$denyra_snapshot_remove"
  done
}
