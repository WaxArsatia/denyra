#!/bin/sh
set -eu

lock=
output=
service=
while [ "$#" -gt 0 ]; do
	case "$1" in
		--lock) lock=$2; shift 2 ;;
		--output) output=$2; shift 2 ;;
		--service) service=$2; shift 2 ;;
		*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[ -n "$lock" ] && [ -n "$output" ] && [ -n "$service" ] || { echo "--lock, --output, and --service are required" >&2; exit 2; }

python3 - "$lock" "$output" "$service" <<'PY'
import hashlib
import json
import os
import sys
import tempfile

lock_path, output_path, service = sys.argv[1:]
with open(lock_path, "rb") as source:
    raw = source.read()
lock = json.loads(raw)
document = {
    "service": service,
    "lock_sha256": hashlib.sha256(raw).hexdigest(),
    "images": sorted(lock["images"], key=lambda item: item["id"]),
    "artifacts": sorted(lock["artifacts"], key=lambda item: item["id"]),
    "registries": sorted(lock["registries"], key=lambda item: item["id"]),
    "components": sorted(lock["components"], key=lambda item: item["id"]),
}
parent = os.path.dirname(os.path.abspath(output_path))
os.makedirs(parent, exist_ok=True)
fd, temporary = tempfile.mkstemp(prefix=".build-provenance-", dir=parent)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as destination:
        json.dump(document, destination, sort_keys=True, separators=(",", ":"))
        destination.write("\n")
        destination.flush()
        os.fsync(destination.fileno())
    os.replace(temporary, output_path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
