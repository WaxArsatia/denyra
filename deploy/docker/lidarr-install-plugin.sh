#!/bin/sh
set -eu
legacy=/config/plugins/Lidarr.Plugin.Slskd
target=/config/plugins/allquiet-hub/Lidarr.Plugin.Slskd
if [ -d "$legacy" ]; then
	chmod -R u+w "$legacy"
	rm -rf -- "$legacy"
fi
mkdir -p "$target"
cp -a /defaults/denyra-plugins/Lidarr.Plugin.Slskd/. "$target/"
chmod -R a-w "$target"
