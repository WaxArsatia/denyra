#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
python3 - "$repo_root/dependencies.lock.json" <<'PY'
import json
import subprocess
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    lock = json.load(source)

for image in lock["images"]:
    reference = image["reference"]
    result = subprocess.run(
        ["docker", "buildx", "imagetools", "inspect", reference, "--format", "{{json .}}"],
        check=True,
        capture_output=True,
        text=True,
    )
    metadata = json.loads(result.stdout)
    manifest = json.dumps(metadata, sort_keys=True)
    if image["digest"] not in manifest:
        raise SystemExit(f"manifest digest mismatch for {image['id']}")
    if image["platform"] != "linux/amd64":
        raise SystemExit(f"unsupported platform for {image['id']}")
PY
