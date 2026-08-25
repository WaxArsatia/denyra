#!/bin/sh

denyra_cleanup_validate_root() {
  denyra_cleanup_root=$1
  denyra_cleanup_name=$2
  case "$denyra_cleanup_root" in
    /*) ;;
    *) denyra_die "$denyra_cleanup_name must be an absolute path" ;;
  esac
  [ "$denyra_cleanup_root" != / ] || denyra_die "$denyra_cleanup_name cannot be /"
  [ ! -L "$denyra_cleanup_root" ] || denyra_die "$denyra_cleanup_name must not be a symlink"
  [ -d "$denyra_cleanup_root" ] || denyra_die "$denyra_cleanup_name must be an existing directory"
  denyra_cleanup_canonical=$(CDPATH= cd -- "$denyra_cleanup_root" && pwd -P) || denyra_die "cannot resolve $denyra_cleanup_name"
  [ "$denyra_cleanup_canonical" = "$denyra_cleanup_root" ] || denyra_die "$denyra_cleanup_name must be canonical"
}

denyra_cleanup_validate_target() {
  denyra_cleanup_target=$1
  denyra_cleanup_expected=$2
  denyra_cleanup_kind=$3
  [ "$denyra_cleanup_target" = "$denyra_cleanup_expected" ] || denyra_die "cleanup target is outside the fixed legacy set"
  [ ! -L "$denyra_cleanup_target" ] || denyra_die "$denyra_cleanup_target must not be a symlink"
  denyra_cleanup_parent=$(dirname -- "$denyra_cleanup_target")
  denyra_cleanup_parent_canonical=$(CDPATH= cd -- "$denyra_cleanup_parent" && pwd -P) || denyra_die "cannot resolve cleanup target parent"
  [ "$denyra_cleanup_parent_canonical" = "$denyra_cleanup_parent" ] || denyra_die "cleanup target parent must be canonical"
  case "$denyra_cleanup_kind" in
    directory) [ -d "$denyra_cleanup_target" ] || denyra_die "$denyra_cleanup_target must be a directory" ;;
    file) [ -f "$denyra_cleanup_target" ] || denyra_die "$denyra_cleanup_target must be a regular file" ;;
    *) denyra_die "invalid fixed cleanup target kind" ;;
  esac
}

denyra_cleanup_quote() {
  printf "'"
  printf '%s' "$1" | sed "s/'/'\\\\''/g"
  printf "'\n"
}

denyra_cleanup_legacy_lifecycle() {
  denyra_cleanup_validate_root "$DENYRA_HOME" DENYRA_HOME
  denyra_cleanup_validate_root "$DENYRA_DATA_ROOT" DENYRA_DATA_ROOT
  denyra_cleanup_validate_root "$DENYRA_SECRETS_DIR" DENYRA_SECRETS_DIR

  denyra_cleanup_updates=$DENYRA_HOME/updates
  denyra_cleanup_backups=$DENYRA_DATA_ROOT/backups
  denyra_cleanup_restic=$DENYRA_SECRETS_DIR/restic_password
  denyra_cleanup_targets=

  if [ -e "$denyra_cleanup_updates" ] || [ -L "$denyra_cleanup_updates" ]; then
    denyra_cleanup_validate_target "$denyra_cleanup_updates" "$DENYRA_HOME/updates" directory
    denyra_cleanup_targets="$denyra_cleanup_targets updates"
  fi
  if [ -e "$denyra_cleanup_backups" ] || [ -L "$denyra_cleanup_backups" ]; then
    denyra_cleanup_validate_target "$denyra_cleanup_backups" "$DENYRA_DATA_ROOT/backups" directory
    denyra_cleanup_targets="$denyra_cleanup_targets backups"
  fi
  if [ -e "$denyra_cleanup_restic" ] || [ -L "$denyra_cleanup_restic" ]; then
    denyra_cleanup_validate_target "$denyra_cleanup_restic" "$DENYRA_SECRETS_DIR/restic_password" file
    denyra_cleanup_targets="$denyra_cleanup_targets restic_password"
  fi

  if [ -z "$denyra_cleanup_targets" ]; then
    printf 'No local legacy lifecycle artifacts found.\n'
    return 0
  fi

  printf 'Legacy Denyra artifacts selected for permanent deletion:\n'
  for denyra_cleanup_item in $denyra_cleanup_targets; do
    case "$denyra_cleanup_item" in
      updates) denyra_cleanup_quote "$denyra_cleanup_updates" ;;
      backups) denyra_cleanup_quote "$denyra_cleanup_backups" ;;
      restic_password) denyra_cleanup_quote "$denyra_cleanup_restic" ;;
    esac
  done
  printf 'Type DELETE to permanently remove only these legacy Denyra artifacts:\n'
  denyra_cleanup_confirmation=
  IFS= read -r denyra_cleanup_confirmation || true
  if [ "$denyra_cleanup_confirmation" != DELETE ]; then
    printf 'cleanup cancelled\n'
    return 0
  fi

  for denyra_cleanup_item in $denyra_cleanup_targets; do
    case "$denyra_cleanup_item" in
      updates)
        rm -rf -- "$denyra_cleanup_updates" || denyra_die "cleanup failed at $denyra_cleanup_updates; later targets remain"
        printf 'Removed %s\n' "$denyra_cleanup_updates"
        ;;
      backups)
        rm -rf -- "$denyra_cleanup_backups" || denyra_die "cleanup failed at $denyra_cleanup_backups; later targets remain"
        printf 'Removed %s\n' "$denyra_cleanup_backups"
        ;;
      restic_password)
        rm -f -- "$denyra_cleanup_restic" || denyra_die "cleanup failed at $denyra_cleanup_restic"
        printf 'Removed %s\n' "$denyra_cleanup_restic"
        ;;
    esac
  done
}

denyra_cleanup() {
  [ "$#" -eq 1 ] && [ "$1" = legacy-lifecycle ] || denyra_die "cleanup requires exactly: legacy-lifecycle"
  denyra_cleanup_legacy_lifecycle
}
