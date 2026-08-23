#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/lib.sh"
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
cd "$repo_root"

denyra_upgrade_require_file DENYRA_UPGRADE_BASE_LOCK
denyra_upgrade_require_file DENYRA_UPGRADE_APPROVAL_FILE
: "${DENYRA_UPGRADE_EVIDENCE_DIR:?set DENYRA_UPGRADE_EVIDENCE_DIR}"
mkdir -m 0700 -p "$DENYRA_UPGRADE_EVIDENCE_DIR"
[ -z "$(find "$DENYRA_UPGRADE_EVIDENCE_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ] || { echo "upgrade evidence directory must be empty" >&2; exit 1; }

current_hash=$(denyra_upgrade_lock_hash dependencies.lock.json)
base_hash=$(denyra_upgrade_lock_hash "$DENYRA_UPGRADE_BASE_LOCK")
[ "$current_hash" != "$base_hash" ] || { echo "dependency lock did not change" >&2; exit 1; }
python3 - "$DENYRA_UPGRADE_APPROVAL_FILE" "$current_hash" <<'PY'
import datetime
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    approval = json.load(source)
if set(approval) != {"lock_sha256", "reviewer", "reviewed_at"}:
    raise SystemExit("approval evidence has unexpected fields")
if approval["lock_sha256"] != sys.argv[2] or not approval["reviewer"].strip():
    raise SystemExit("approval evidence does not match the reviewed lock")
datetime.datetime.fromisoformat(approval["reviewed_at"].replace("Z", "+00:00"))
PY

cp "$DENYRA_UPGRADE_BASE_LOCK" "$DENYRA_UPGRADE_EVIDENCE_DIR/prior-dependencies.lock.json"
cp deploy/images.lock.json "$DENYRA_UPGRADE_EVIDENCE_DIR/prior-images.lock.json"
cp deploy/compose.yaml "$DENYRA_UPGRADE_EVIDENCE_DIR/prior-compose.yaml"
cp "$DENYRA_UPGRADE_APPROVAL_FILE" "$DENYRA_UPGRADE_EVIDENCE_DIR/approval.json"

scripts/verify-pins/verify.sh --offline
python3 - deploy/python/requirements.lock <<'PY'
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
packages = re.split(r"\n(?=[A-Za-z0-9_.-]+==)", text)
for package in packages:
    lines = [line for line in package.splitlines() if line and not line.lstrip().startswith("#")]
    if lines and "==" in lines[0] and "--hash=sha256:" not in package:
        raise SystemExit(f"unhashed Python dependency: {lines[0]}")
PY

generated_before=$(mktemp)
generated_after=$(mktemp)
trap 'rm -f "$generated_before" "$generated_after"' EXIT HUP INT TERM
find internal/pipeline/adminui -name '*_templ.go' -type f -print0 | sort -z | xargs -0 sha256sum > "$generated_before"
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
find internal/pipeline/adminui -name '*_templ.go' -type f -print0 | sort -z | xargs -0 sha256sum > "$generated_after"
cmp "$generated_before" "$generated_after"
gofmt -w $(find cmd internal migrations tests scripts -name '*.go' -type f)
go vet ./...
go test -race ./...

grep -q "default = \"$current_hash\"" deploy/docker/docker-bake.hcl || { echo "docker-bake lock label was not updated" >&2; exit 1; }
scripts/verify-pins/build-provenance.sh --lock dependencies.lock.json --service gateway --output deploy/docker/generated/gateway-build-provenance.json
scripts/verify-pins/build-provenance.sh --lock dependencies.lock.json --service pipeline --output deploy/docker/generated/pipeline-build-provenance.json
docker buildx bake -f deploy/docker/docker-bake.hcl gateway pipeline lidarr navidrome --load

gateway_id=$(docker image inspect denyra/acquisition-gateway:local --format '{{.Id}}')
pipeline_id=$(docker image inspect denyra/media-pipeline:local --format '{{.Id}}')
lidarr_id=$(docker image inspect denyra/lidarr:local --format '{{.Id}}')
navidrome_id=$(docker image inspect denyra/navidrome:local --format '{{.Id}}')
python3 - deploy/images.lock.json deploy/compose.yaml "$current_hash" "$gateway_id" "$pipeline_id" "$lidarr_id" "$navidrome_id" <<'PY'
import json
import re
import sys

lock_path, compose_path, dependency_hash, gateway, pipeline, lidarr, navidrome = sys.argv[1:]
identities = {
    "acquisition-gateway": f"docker.io/denyra/acquisition-gateway:local@{gateway}",
    "media-pipeline": f"docker.io/denyra/media-pipeline:local@{pipeline}",
    "lidarr-derived": f"docker.io/denyra/lidarr:local@{lidarr}",
    "navidrome-derived": f"docker.io/denyra/navidrome:local@{navidrome}",
}
with open(lock_path, encoding="utf-8") as source:
    lock = json.load(source)
lock["dependencies_lock_sha256"] = dependency_hash
for image in lock["images"]:
    image["reference"] = identities[image["id"]]
with open(lock_path, "w", encoding="utf-8") as destination:
    json.dump(lock, destination, indent=2)
    destination.write("\n")
compose = open(compose_path, encoding="utf-8").read()
service_ids = {"acquisition-gateway": "acquisition-gateway", "media-pipeline": "media-pipeline", "lidarr": "lidarr-derived", "navidrome": "navidrome-derived"}
for service, identity in service_ids.items():
    pattern = rf"(?m)^(  {re.escape(service)}:\n    image: ).*$"
    compose, count = re.subn(pattern, rf"\g<1>{identities[identity]}", compose)
    if count != 1:
        raise SystemExit(f"could not update Compose image for {service}")
open(compose_path, "w", encoding="utf-8").write(compose)
PY

docker compose -f deploy/compose.yaml config --quiet
TEST_DOCKER_IMAGES=1 go test ./tests/integration -run 'Compose|ServiceImage'
cp deploy/images.lock.json "$DENYRA_UPGRADE_EVIDENCE_DIR/candidate-images.lock.json"
cp deploy/compose.yaml "$DENYRA_UPGRADE_EVIDENCE_DIR/candidate-compose.yaml"
printf 'dependencies_lock_sha256=%s\nimages_lock_sha256=%s\n' "$current_hash" "$(denyra_upgrade_lock_hash deploy/images.lock.json)" > "$DENYRA_UPGRADE_EVIDENCE_DIR/verified-update.env"
