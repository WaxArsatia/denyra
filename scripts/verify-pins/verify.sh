#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
offline=false
if [ "${1:-}" = "--offline" ]; then
	offline=true
elif [ "$#" -ne 0 ]; then
	echo "usage: $0 [--offline]" >&2
	exit 2
fi

cd "$repo_root"
go test ./internal/platform/deplock

expected_fixture="a563a1cd91c19ceddecfd1812a3eeb3186b4015c0862a3bfdee28cb58bf5f7bc"
actual_fixture=$(sha256sum scripts/verify-pins/fixtures/payload.txt | awk '{print $1}')
if [ "$actual_fixture" != "$expected_fixture" ]; then
	echo "offline verifier fixture checksum mismatch" >&2
	exit 1
fi

if [ "$offline" = false ]; then
	echo "online artifact retrieval is intentionally separate; use a credential-safe build cache" >&2
fi
