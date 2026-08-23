# syntax=docker/dockerfile:1.18
FROM docker.io/library/debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258 AS plugin
COPY deploy/docker/debian.sources /etc/apt/sources.list.d/debian.sources
RUN apt-get update && apt-get install -y --no-install-recommends unzip=6.0-29+deb13u1 ca-certificates=20250419 \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:5766c6563f7ed36911068c899778e0f935698c6866c85dffafb2f8a32a5fc0d8 \
    https://github.com/allquiet-hub/Lidarr.Plugin.Slskd/releases/download/v1.1.3.0/Lidarr.Plugin.Slskd.net8.0.zip /tmp/plugin.zip
RUN mkdir /plugin && unzip -q /tmp/plugin.zip -d /plugin

FROM lscr.io/linuxserver/lidarr:nightly@sha256:0b84fcf40449e800da92eccbf4a421dd39908a5e1e2a25b6e3e5b5dcc9697e95
COPY --from=plugin /plugin /defaults/denyra-plugins/Lidarr.Plugin.Slskd
COPY deploy/docker/lidarr-install-plugin.sh /custom-cont-init.d/20-denyra-plugin
LABEL org.opencontainers.image.title="Denyra Lidarr" org.opencontainers.image.version="nightly-slskd-1.1.3.0"
