#!/bin/bash
set -euo pipefail

load_secret() {
    local variable_name="$1"
    local secret_path="$2"
    local secret_value=""

    if [[ ! -r "$secret_path" ]]; then
        printf 'required slskd secret is not readable: %s\n' "$secret_path" >&2
        exit 1
    fi
    IFS= read -r secret_value < "$secret_path" || [[ -n "$secret_value" ]]
    if [[ -z "$secret_value" ]]; then
        printf 'required slskd secret is empty: %s\n' "$secret_path" >&2
        exit 1
    fi
    export "$variable_name=$secret_value"
}

load_secret SLSKD_SLSK_USERNAME "${SLSKD_SLSK_USERNAME_FILE:-/run/secrets/soulseek_username}"
load_secret SLSKD_SLSK_PASSWORD "${SLSKD_SLSK_PASSWORD_FILE:-/run/secrets/soulseek_password}"

if [[ "$#" -eq 0 ]]; then
    printf 'slskd secret entrypoint requires a command\n' >&2
    exit 1
fi

exec "$@"
