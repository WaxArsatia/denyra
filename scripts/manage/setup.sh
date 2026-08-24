#!/bin/sh

denyra_setup_copy_secret() {
  denyra_setup_secret_name=$1
  denyra_setup_secret_target=$DENYRA_SECRETS_DIR/$denyra_setup_secret_name
  denyra_setup_legacy_secret=$repo_root/deploy/secrets/$denyra_setup_secret_name
  if [ ! -s "$denyra_setup_secret_target" ] && [ -r "$denyra_setup_legacy_secret" ] && [ -s "$denyra_setup_legacy_secret" ]; then
    denyra_atomic_file "$denyra_setup_secret_target" 0600 < "$denyra_setup_legacy_secret"
  fi
}

denyra_setup_migrate_secret() {
  denyra_setup_legacy_name=$1
  denyra_setup_current_name=$2
  denyra_setup_legacy_path=$DENYRA_SECRETS_DIR/$denyra_setup_legacy_name
  denyra_setup_current_path=$DENYRA_SECRETS_DIR/$denyra_setup_current_name
  if [ ! -s "$denyra_setup_current_path" ] && [ -s "$denyra_setup_legacy_path" ]; then
    denyra_atomic_file "$denyra_setup_current_path" 0600 < "$denyra_setup_legacy_path"
  fi
}

denyra_setup_input_secret() {
  denyra_setup_input_name=$1
  denyra_setup_input_value=$2
  denyra_setup_input_target=$DENYRA_SECRETS_DIR/$denyra_setup_input_name
  [ -s "$denyra_setup_input_target" ] && return 0
  [ -n "$denyra_setup_input_value" ] || denyra_die "$denyra_setup_input_name cannot be empty"
  printf '%s' "$denyra_setup_input_value" | denyra_atomic_file "$denyra_setup_input_target" 0600
}

denyra_setup_prompt_secret() {
  denyra_setup_prompt=$1
  [ -t 0 ] || denyra_die "Soulseek credentials are required; set DENYRA_SOULSEEK_USERNAME and DENYRA_SOULSEEK_PASSWORD"
  printf '%s: ' "$denyra_setup_prompt" >&2
  stty -echo
  IFS= read -r denyra_setup_prompt_value
  stty echo
  printf '\n' >&2
  printf '%s' "$denyra_setup_prompt_value"
}

denyra_setup_copy_config() {
  denyra_setup_config_name=$1
  denyra_setup_config_source=$repo_root/deploy/config/$denyra_setup_config_name
  denyra_setup_config_target=$DENYRA_CONFIG_DIR/$denyra_setup_config_name
  [ -e "$denyra_setup_config_source" ] || return 0
  [ -e "$denyra_setup_config_target" ] && return 0
  denyra_atomic_file "$denyra_setup_config_target" 0640 < "$denyra_setup_config_source"
}

denyra_setup_lidarr_api_key() {
  denyra_setup_lidarr_config=$DENYRA_DATA_ROOT/state/lidarr/config.xml
  denyra_setup_wait_seconds=${DENYRA_WAIT_SECONDS:-180}
  denyra_setup_waited=0
  while [ ! -s "$denyra_setup_lidarr_config" ] && [ "$denyra_setup_waited" -lt "$denyra_setup_wait_seconds" ]; do
    sleep 1
    denyra_setup_waited=$((denyra_setup_waited + 1))
  done
  [ -s "$denyra_setup_lidarr_config" ] || denyra_die "Lidarr config.xml was not created within ${denyra_setup_wait_seconds}s"

  denyra_setup_lidarr_keys=$(sed -n 's:.*<ApiKey>\([^<]*\)</ApiKey>.*:\1:p' "$denyra_setup_lidarr_config")
  denyra_setup_lidarr_key_count=$(printf '%s\n' "$denyra_setup_lidarr_keys" | sed '/^$/d' | wc -l)
  [ "$denyra_setup_lidarr_key_count" -eq 1 ] || denyra_die "Lidarr config.xml must contain exactly one API key"
  denyra_setup_lidarr_key=$denyra_setup_lidarr_keys
  printf '%s\n' "$denyra_setup_lidarr_key" | LC_ALL=C grep -Eq '^[!-~]{16,}$' || denyra_die "Lidarr API key is invalid"

  denyra_setup_lidarr_secret=$DENYRA_SECRETS_DIR/lidarr_api_key
  if [ -s "$denyra_setup_lidarr_secret" ]; then
    denyra_setup_existing_lidarr_key=$(tr -d '\r\n' < "$denyra_setup_lidarr_secret")
    [ "$denyra_setup_existing_lidarr_key" = "$denyra_setup_lidarr_key" ] || denyra_die "existing Lidarr API key does not match persistent Lidarr state"
    return 0
  fi
  printf '%s' "$denyra_setup_lidarr_key" | denyra_atomic_file "$denyra_setup_lidarr_secret" 0600
}

denyra_setup_reconcile() {
  denyra_setup_reconcile_attempt=1
  denyra_setup_reconcile_attempts=${DENYRA_RECONCILE_ATTEMPTS:-5}
  denyra_setup_reconcile_interval=${DENYRA_RECONCILE_RETRY_SECONDS:-3}
  while [ "$denyra_setup_reconcile_attempt" -le "$denyra_setup_reconcile_attempts" ]; do
    if denyra_compose --profile setup run --rm reconciler; then
      return 0
    fi
    [ "$denyra_setup_reconcile_attempt" -lt "$denyra_setup_reconcile_attempts" ] || return 1
    printf 'denyra: reconciliation attempt %s/%s failed; retrying\n' "$denyra_setup_reconcile_attempt" "$denyra_setup_reconcile_attempts" >&2
    sleep "$denyra_setup_reconcile_interval"
    denyra_setup_reconcile_attempt=$((denyra_setup_reconcile_attempt + 1))
  done
}

denyra_setup_start_all() {
  denyra_setup_start_attempt=1
  denyra_setup_start_attempts=${DENYRA_START_ATTEMPTS:-3}
  denyra_setup_start_interval=${DENYRA_START_RETRY_SECONDS:-3}
  while [ "$denyra_setup_start_attempt" -le "$denyra_setup_start_attempts" ]; do
    if denyra_compose up -d --remove-orphans --wait; then
      return 0
    fi
    [ "$denyra_setup_start_attempt" -lt "$denyra_setup_start_attempts" ] || return 1
    printf 'denyra: final start attempt %s/%s failed; retrying\n' "$denyra_setup_start_attempt" "$denyra_setup_start_attempts" >&2
    sleep "$denyra_setup_start_interval"
    denyra_setup_start_attempt=$((denyra_setup_start_attempt + 1))
  done
}

denyra_setup_verify_layout() {
  for denyra_setup_path in \
    library library-unmanaged incoming/manual incoming/uploading \
    processing/work processing/approved quarantine state/gateway state/pipeline
  do
    [ -d "$DENYRA_DATA_ROOT/$denyra_setup_path" ] || denyra_die "data layout is incomplete: $denyra_setup_path"
  done
}

denyra_setup() {
  git --version >/dev/null 2>&1 || denyra_die "Git is required"
  docker version >/dev/null 2>&1 || denyra_die "Docker Engine is required"
  docker compose version >/dev/null 2>&1 || denyra_die "Docker Compose v2 is required"

  denyra_setup_uid=$(id -u)
  denyra_setup_gid=$(id -g)
  if ! mkdir -p "$DENYRA_HOME" 2>/dev/null; then
    command -v sudo >/dev/null 2>&1 || denyra_die "cannot create $DENYRA_HOME; sudo is unavailable"
    sudo install -d -m 0750 -o "$denyra_setup_uid" -g "$denyra_setup_gid" "$DENYRA_HOME"
  fi
  chmod 0750 "$DENYRA_HOME"
  mkdir -p "$DENYRA_CONFIG_DIR" "$DENYRA_SECRETS_DIR" "$DENYRA_DATA_ROOT" "$DENYRA_UPDATES_DIR"
  chmod 0750 "$DENYRA_CONFIG_DIR" "$DENYRA_DATA_ROOT" "$DENYRA_UPDATES_DIR"
  chmod 0700 "$DENYRA_SECRETS_DIR"
  denyra_lock

  denyra_setup_migrate_secret denyra_admin_password bootstrap_admin
  denyra_setup_migrate_secret navidrome_admin_password navidrome_admin
  denyra_setup_migrate_secret sftpgo_admin_password sftpgo_admin
  denyra_setup_migrate_secret sftpgo_upload_password sftpgo_upload

  for denyra_setup_generated_secret in \
    internal_bearer audit_key bootstrap_admin navidrome_admin \
    sftpgo_admin sftpgo_upload slskd_api_key slskd_web_password restic_password
  do
    denyra_setup_copy_secret "$denyra_setup_generated_secret"
    denyra_secret "$denyra_setup_generated_secret" 32
  done

  denyra_setup_copy_secret soulseek_username
  denyra_setup_copy_secret soulseek_password
  if [ ! -s "$DENYRA_SECRETS_DIR/soulseek_username" ]; then
    denyra_setup_soulseek_username=${DENYRA_SOULSEEK_USERNAME:-}
    [ -n "$denyra_setup_soulseek_username" ] || denyra_setup_soulseek_username=$(denyra_setup_prompt_secret "Soulseek username")
    denyra_setup_input_secret soulseek_username "$denyra_setup_soulseek_username"
  fi
  if [ ! -s "$DENYRA_SECRETS_DIR/soulseek_password" ]; then
    denyra_setup_soulseek_password=${DENYRA_SOULSEEK_PASSWORD:-}
    [ -n "$denyra_setup_soulseek_password" ] || denyra_setup_soulseek_password=$(denyra_setup_prompt_secret "Soulseek password")
    denyra_setup_input_secret soulseek_password "$denyra_setup_soulseek_password"
  fi

  "$repo_root/scripts/bootstrap-data-layout.sh" --target "$DENYRA_DATA_ROOT" --uid "$denyra_setup_uid" --gid "$denyra_setup_gid"
  denyra_setup_verify_layout

  for denyra_setup_config in gateway.toml pipeline.toml navidrome.toml navidrome-lyrics.toml slskd.yml
  do
    denyra_setup_copy_config "$denyra_setup_config"
  done

  denyra_setup_short_commit=$(git rev-parse --short=12 HEAD)
  denyra_setup_full_commit=$(git rev-parse HEAD)
  {
    printf 'DENYRA_HOME=%s\n' "$DENYRA_HOME"
    printf 'DENYRA_CONFIG_DIR=%s\n' "$DENYRA_CONFIG_DIR"
    printf 'DENYRA_SECRETS_DIR=%s\n' "$DENYRA_SECRETS_DIR"
    printf 'DENYRA_DATA_ROOT=%s\n' "$DENYRA_DATA_ROOT"
    printf 'DENYRA_PROJECT_NAME=%s\n' "$DENYRA_PROJECT_NAME"
    printf 'DENYRA_COMPOSE_OVERRIDE=%s\n' "${DENYRA_COMPOSE_OVERRIDE:-}"
    printf 'DENYRA_MEDIA_UID=%s\n' "$denyra_setup_uid"
    printf 'DENYRA_MEDIA_GID=%s\n' "$denyra_setup_gid"
    printf 'DENYRA_IMAGE_TAG=%s\n' "$denyra_setup_short_commit"
    printf 'DENYRA_GIT_COMMIT=%s\n' "$denyra_setup_full_commit"
  } | denyra_atomic_file "$DENYRA_CONFIG_DIR/denyra.env" 0640

  {
    printf 'Denyra | http://localhost:8090 | admin | %s/bootstrap_admin\n' "$DENYRA_SECRETS_DIR"
    printf 'Navidrome | http://localhost:4533 | admin | %s/navidrome_admin\n' "$DENYRA_SECRETS_DIR"
    printf 'SFTPGo | http://localhost:8080 | admin | %s/sftpgo_admin\n' "$DENYRA_SECRETS_DIR"
    printf 'SFTP | localhost:2022 | upload | %s/sftpgo_upload\n' "$DENYRA_SECRETS_DIR"
    printf 'slskd | internal only | admin | %s/slskd_web_password\n' "$DENYRA_SECRETS_DIR"
    printf 'Run ./denyra credentials to display current values.\n'
  } | denyra_atomic_file "$DENYRA_HOME/credentials.txt" 0600

  printf '%s\n' "$DENYRA_HOME" | denyra_atomic_file "$repo_root/.denyra-home" 0600

  DENYRA_RELEASE_REFRESH=setup-$(date -u +%Y%m%dT%H%M%SZ)
  export DENYRA_RELEASE_REFRESH
  denyra_compose build --pull
  denyra_compose up -d --wait --wait-timeout "${DENYRA_WAIT_SECONDS:-180}" lidarr slskd sftpgo navidrome
  denyra_setup_lidarr_api_key
  denyra_setup_reconcile
  denyra_setup_start_all
  . "$repo_root/scripts/manage/smoke.sh"
  denyra_smoke
  denyra_unlock
}
