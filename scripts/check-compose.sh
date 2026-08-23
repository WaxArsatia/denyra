#!/bin/sh
set -eu

expected_engine=29.6.2
expected_compose=v2.40.3
expected_buildx=0.36.1

engine=$(docker version --format '{{.Server.Version}}')
compose=$(docker compose version --short)
buildx=$(docker buildx version | awk '{print $2}')
buildx=${buildx#v}

status=0
if [ "$engine" != "$expected_engine" ]; then
	echo "Docker Engine $engine does not match deployment pin $expected_engine" >&2
	status=1
fi
if [ "$compose" != "$expected_compose" ]; then
	echo "Docker Compose $compose does not match deployment pin $expected_compose" >&2
	echo "approved Compose-v2 compatibility exception: local Compose v5 may render config, but cannot satisfy the deployment gate" >&2
	status=1
fi
if [ "$buildx" != "$expected_buildx" ]; then
	echo "Docker Buildx $buildx does not match deployment pin v$expected_buildx" >&2
	status=1
fi

exit "$status"
