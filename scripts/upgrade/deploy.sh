#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
cd "$repo_root"

denyra_upgrade_require_directory DENYRA_UPGRADE_EVIDENCE_DIR
denyra_upgrade_require_directory DENYRA_VERIFIED_BACKUP_DIR
denyra_upgrade_require_directory DENYRA_UPGRADE_RESTORE_TARGET
[ -f "$DENYRA_UPGRADE_EVIDENCE_DIR/verified-update.env" ] || { echo "verified update evidence is missing" >&2; exit 1; }
[ -f "$DENYRA_VERIFIED_BACKUP_DIR/manifest.json" ] || { echo "verified backup manifest is missing" >&2; exit 1; }
restore_report=$(find "$DENYRA_UPGRADE_RESTORE_TARGET/workspace" -mindepth 2 -maxdepth 2 -name restore-report.json -type f)
[ -n "$restore_report" ] && [ "$(printf '%s\n' "$restore_report" | wc -l)" -eq 1 ] || { echo "exactly one restore-report.json is required" >&2; exit 1; }
python3 - "$restore_report" <<'PY'
import json
import sys

report = json.load(open(sys.argv[1], encoding="utf-8"))
if report["checksum_failures"] != 0 or not report["same_device"]:
    raise SystemExit("restore report is not verified")
if min(report["database_versions"].values()) <= 0:
    raise SystemExit("restored databases did not pass migration checks")
PY
. "$DENYRA_UPGRADE_EVIDENCE_DIR/verified-update.env"
[ "$dependencies_lock_sha256" = "$(denyra_upgrade_lock_hash dependencies.lock.json)" ] || { echo "verified dependency lock changed" >&2; exit 1; }
[ "$images_lock_sha256" = "$(denyra_upgrade_lock_hash deploy/images.lock.json)" ] || { echo "verified image lock changed" >&2; exit 1; }

restore_workspace=$(dirname -- "$restore_report")
[ ! -e "$restore_workspace/upgrade-migration-smoke.json" ] || { echo "upgrade migration smoke report already exists" >&2; exit 1; }
docker compose -f deploy/compose.yaml run --rm --no-deps \
  --entrypoint /app/denyra-restore-check -v "$DENYRA_UPGRADE_RESTORE_TARGET:/restore" \
  media-pipeline migration-smoke --root /restore/source \
  --report "/restore/workspace/$(basename "$restore_workspace")/upgrade-migration-smoke.json"

gofmt -w $(find cmd internal migrations tests scripts -name '*.go' -type f)
go vet ./...
go test -race ./...
docker compose -f deploy/compose.yaml config --quiet
docker compose -f deploy/compose.yaml up -d --remove-orphans --wait
docker compose -f deploy/compose.yaml ps --format json > "$DENYRA_UPGRADE_EVIDENCE_DIR/deployed-services.json"
go test ./tests/contract ./tests/acceptance
printf 'deployed_at=%s\ndependencies_lock_sha256=%s\nimages_lock_sha256=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$dependencies_lock_sha256" "$images_lock_sha256" > "$DENYRA_UPGRADE_EVIDENCE_DIR/deployed.env"
