# syntax=docker/dockerfile:1
FROM deluan/navidrome:latest
USER root
ARG DENYRA_RELEASE_REFRESH=manual
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && mkdir -p /plugins \
    && wget -q -O /plugins/nd-lyrics.ndp https://github.com/J0R6IT0/navidrome-lyrics-plugin/releases/latest/download/nd-lyrics.ndp \
    && chmod 0444 /plugins/nd-lyrics.ndp
LABEL org.opencontainers.image.title="Denyra Navidrome" \
      io.denyra.project="denyra"
USER 1000:1000
