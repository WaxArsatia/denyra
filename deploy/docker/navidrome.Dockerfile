# syntax=docker/dockerfile:1.18
FROM docker.io/deluan/navidrome:0.63.2@sha256:38246ebb80d6f7e2724eecab4acafa7b14ec66ae800b2454aa6da4c19f80a9ce
USER root
ADD --checksum=sha256:a9196e5b4e2c2eb2aaccb9f35c9faf6f488fe9081ff5685b1556901686c7540f \
    https://github.com/J0R6IT0/navidrome-lyrics-plugin/releases/download/v7.2.0/nd-lyrics.ndp /plugins/nd-lyrics.ndp
RUN chmod 0444 /plugins/nd-lyrics.ndp
LABEL org.opencontainers.image.title="Denyra Navidrome" org.opencontainers.image.version="0.63.2-lyrics-7.2.0"
USER 1000:1000
