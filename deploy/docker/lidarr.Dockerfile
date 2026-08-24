# syntax=docker/dockerfile:1
FROM debian:stable-slim AS plugin
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/*
ARG DENYRA_RELEASE_REFRESH=manual
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && curl -fsSL https://github.com/allquiet-hub/Lidarr.Plugin.Slskd/releases/latest/download/Lidarr.Plugin.Slskd.net8.0.zip -o /tmp/plugin.zip \
    && mkdir /plugin \
    && unzip -q /tmp/plugin.zip -d /plugin

FROM lscr.io/linuxserver/lidarr:nightly
COPY --from=plugin /plugin /defaults/denyra-plugins/Lidarr.Plugin.Slskd
COPY deploy/docker/lidarr-install-plugin.sh /custom-cont-init.d/20-denyra-plugin
LABEL org.opencontainers.image.title="Denyra Lidarr"
