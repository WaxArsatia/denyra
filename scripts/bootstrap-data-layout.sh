#!/bin/sh
set -eu

usage() {
	echo "usage: $0 --target /data --uid NUMERIC_UID --gid NUMERIC_GID" >&2
	exit 2
}

target=
owner_uid=
owner_gid=
while [ "$#" -gt 0 ]; do
	case "$1" in
		--target) [ "$#" -ge 2 ] || usage; target=$2; shift 2 ;;
		--uid) [ "$#" -ge 2 ] || usage; owner_uid=$2; shift 2 ;;
		--gid) [ "$#" -ge 2 ] || usage; owner_gid=$2; shift 2 ;;
		*) usage ;;
	esac
done

[ "$target" = "/data" ] || { echo "target must be exactly /data" >&2; exit 1; }
case "$owner_uid:$owner_gid" in
	*[!0-9:]*|:*|*:) echo "uid and gid must be explicit numeric values" >&2; exit 1 ;;
esac
[ ! -L "$target" ] || { echo "/data must not be a symlink" >&2; exit 1; }

for path in \
	/data/downloads/slskd \
	/data/downloads/spotiflac \
	/data/downloads/other \
	/data/incoming/manual \
	/data/processing/work \
	/data/processing/approved \
	/data/quarantine \
	/data/library \
	/data/state/gateway \
	/data/state/pipeline \
	/data/state/lidarr \
	/data/state/slskd \
	/data/state/sftpgo \
	/data/state/navidrome \
	/data/cache/navidrome \
	/data/backups
do
	mkdir -p -- "$path"
	chown "$owner_uid:$owner_gid" -- "$path"
	chmod 0750 -- "$path"
done
