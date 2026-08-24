#!/bin/sh
set -eu

load_secret() {
  secret_target=$1
  secret_source=$2
  [ -r "$secret_source" ] || { echo "required secret is unreadable: $secret_source" >&2; exit 1; }
  secret_value=$(tr -d '\r\n' < "$secret_source")
  [ -n "$secret_value" ] || { echo "required secret is empty: $secret_source" >&2; exit 1; }
  export "$secret_target=$secret_value"
}

load_secret SLSKD_SLSK_USERNAME "${SLSKD_SLSK_USERNAME_FILE:-/run/secrets/soulseek_username}"
load_secret SLSKD_SLSK_PASSWORD "${SLSKD_SLSK_PASSWORD_FILE:-/run/secrets/soulseek_password}"
load_secret SLSKD_API_KEY "${SLSKD_API_KEY_FILE:-/run/secrets/slskd_api_key}"
SLSKD_API_KEY="role=ReadWrite;$SLSKD_API_KEY"
export SLSKD_API_KEY
SLSKD_USERNAME=admin
export SLSKD_USERNAME
load_secret SLSKD_PASSWORD "${SLSKD_PASSWORD_FILE:-/run/secrets/slskd_web_password}"

[ "$#" -gt 0 ] || { echo "slskd secret entrypoint requires a command" >&2; exit 1; }
exec "$@"
