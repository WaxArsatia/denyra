#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

docker version >/dev/null
docker info >/dev/null
docker compose version >/dev/null
docker compose -f "$repo_root/deploy/compose.yaml" config --quiet

echo "Docker and Compose deployment interface verified"
