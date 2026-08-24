#!/bin/sh
set -eu

usage() {
	echo "usage: $0 --target ABSOLUTE_PATH --uid NUMERIC_UID --gid NUMERIC_GID" >&2
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

case "$target" in
	/*) ;;
	*) echo "target must be an absolute path" >&2; exit 1 ;;
esac
[ "$target" != "/" ] || { echo "target must not be /" >&2; exit 1; }
case "$target" in
	*//*|*/./*|*/../*|*/.|*/..|*/) echo "target must already be canonical" >&2; exit 1 ;;
esac
case "$owner_uid:$owner_gid" in
	*[!0-9:]*|:*|*:) echo "uid and gid must be explicit numeric values" >&2; exit 1 ;;
esac
[ ! -L "$target" ] || { echo "target must not be a symlink" >&2; exit 1; }

for path in \
	downloads/slskd \
	downloads/slskd/incomplete \
	downloads/spotiflac \
	downloads/other \
	incoming/manual \
	incoming/uploading \
	processing/work \
	processing/approved \
	quarantine \
	library \
	library-unmanaged \
	state/gateway \
	state/pipeline \
	state/lidarr \
	state/slskd \
	state/sftpgo \
	state/navidrome \
	cache/navidrome \
	backups
do
	path=$target/$path
	mkdir -p -- "$path"
	chown "$owner_uid:$owner_gid" -- "$path"
	chmod 0750 -- "$path"
done
