#!/bin/sh

denyra_backup() {
  [ "$#" -eq 0 ] || denyra_die "backup takes no arguments"
  [ -n "${DENYRA_RESTIC_REPOSITORY_PATH:-}" ] || denyra_die "set DENYRA_RESTIC_REPOSITORY_PATH to a directory outside DENYRA_HOME"
  DENYRA_INTERNAL_BEARER_FILE=$DENYRA_SECRETS_DIR/internal_bearer
  DENYRA_RESTIC_PASSWORD_FILE=$DENYRA_SECRETS_DIR/restic_password
  DENYRA_COMPOSE_FILE=$repo_root/deploy/compose.yaml
  DENYRA_REPO_ROOT=$repo_root
  export DENYRA_INTERNAL_BEARER_FILE DENYRA_RESTIC_PASSWORD_FILE DENYRA_RESTIC_REPOSITORY_PATH
  export DENYRA_COMPOSE_FILE DENYRA_REPO_ROOT
  "$repo_root/scripts/backup/backup.sh"
  denyra_unlock
}
