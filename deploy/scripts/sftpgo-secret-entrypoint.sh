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

SFTPGO_DATA_PROVIDER__CREATE_DEFAULT_ADMIN=true
SFTPGO_DEFAULT_ADMIN_USERNAME=admin
export SFTPGO_DATA_PROVIDER__CREATE_DEFAULT_ADMIN SFTPGO_DEFAULT_ADMIN_USERNAME
load_secret SFTPGO_DEFAULT_ADMIN_PASSWORD "${SFTPGO_DEFAULT_ADMIN_PASSWORD_FILE:-/run/secrets/sftpgo_admin}"

exec "${SFTPGO_ENTRYPOINT:-/entrypoint.sh}" "$@"
