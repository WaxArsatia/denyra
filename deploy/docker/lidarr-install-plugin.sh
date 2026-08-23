#!/bin/sh
set -eu
target=/config/plugins/Lidarr.Plugin.Slskd
mkdir -p "$target"
cp -a /defaults/denyra-plugins/Lidarr.Plugin.Slskd/. "$target/"
chmod -R a-w "$target"
