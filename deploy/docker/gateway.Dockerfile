# syntax=docker/dockerfile:1
FROM golang:1.27 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/acquisition-gateway ./cmd/acquisition-gateway

FROM golang:1.27 AS extension-installer
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
ARG DENYRA_RELEASE_REFRESH=manual
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY scripts/extract-sflx ./scripts/extract-sflx
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && mkdir -p /opt/spotiflac/artifacts /opt/spotiflac/runtime-home/.spotiflac/extensions \
    && curl -fsSL https://github.com/BartolomeoRusso9/SpotiFLAC-Module-Version/releases/latest/download/SpotiFLAC-Linux-x86_64 -o /opt/spotiflac/spotiflac \
    && for provider in tidal-web qobuz-web deezer; do \
         curl -fsSL "https://raw.githubusercontent.com/spotiflacapp/SpotiFLAC-Extension/main/extensions/$provider.sflx" -o "/opt/spotiflac/artifacts/$provider.sflx"; \
         go run ./scripts/extract-sflx -source "/opt/spotiflac/artifacts/$provider.sflx" -destination "/opt/spotiflac/runtime-home/.spotiflac/extensions/$provider"; \
       done \
    && chmod 0555 /opt/spotiflac/spotiflac \
    && chmod -R a-w /opt/spotiflac/artifacts /opt/spotiflac/runtime-home/.spotiflac/extensions

FROM node:24-trixie-slim
ARG DENYRA_GIT_COMMIT=unknown
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/acquisition-gateway /app/acquisition-gateway
COPY --from=extension-installer /opt/spotiflac /opt/spotiflac
RUN mkdir -p /opt/spotiflac/runtime-home/.cache/spotiflac /opt/spotiflac/runtime-home/.spotiflac/signed_sessions \
    && chown -R 1000:1000 /opt/spotiflac/runtime-home/.cache /opt/spotiflac/runtime-home/.spotiflac/signed_sessions
ENV DENYRA_GIT_COMMIT=$DENYRA_GIT_COMMIT \
    HOME=/opt/spotiflac/runtime-home \
    SPOTIFLAC_DISABLE_AUTO_INSTALL=1 \
    SPOTIFLAC_DISABLE_AUTO_UPDATE=1 \
    SPOTIFLAC_CACHE_DIR=/opt/spotiflac/runtime-home/.cache/spotiflac \
    SPOTIFLAC_REGISTRIES=" "
LABEL org.opencontainers.image.title="Denyra Acquisition Gateway"
LABEL io.denyra.project="denyra"
USER 1000:1000
ENTRYPOINT ["/app/acquisition-gateway"]
