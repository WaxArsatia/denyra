#!/bin/sh
set -eu

denyra_require_file() { [ -f "$1" ] || { echo "required file missing: $1" >&2; exit 1; }; }
denyra_json() { printf '{"enabled":%s,"reason":"%s"}' "$1" "$2"; }
denyra_api() {
  endpoint=$1
  payload=$2
  curl --fail --silent --show-error --max-time "${DENYRA_BACKUP_API_TIMEOUT_SECONDS:-30}" \
    -H "Authorization: Bearer $(tr -d '\r\n' < "$DENYRA_INTERNAL_BEARER_FILE")" \
    -H 'Content-Type: application/json' -H "X-Request-ID: backup-$DENYRA_BACKUP_ID" \
    --data "$payload" "$endpoint"
}
denyra_device() { stat -c %d "$1"; }
denyra_restic() {
  docker compose -f "${DENYRA_COMPOSE_FILE:-deploy/compose.yaml}" --profile backup run --rm restic "$@"
}

denyra_wait_safe() {
  endpoint=$1
  output=$2
  attempts=${DENYRA_BACKUP_DRAIN_ATTEMPTS:-60}
  interval=${DENYRA_BACKUP_DRAIN_INTERVAL_SECONDS:-2}
  count=0
  while [ "$count" -lt "$attempts" ]; do
    denyra_api "$endpoint" "$(denyra_json true deterministic-backup)" > "$output"
    if grep -q '"safe":true' "$output"; then
      return 0
    fi
    count=$((count + 1))
    sleep "$interval"
  done
  echo "maintenance did not drain before deadline: $endpoint" >&2
  return 1
}
