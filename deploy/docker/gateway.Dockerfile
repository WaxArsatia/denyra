# syntax=docker/dockerfile:1
ARG SPOTIFLAC_COMMIT=0306da57dd855175549af119d95539e51ffbd801
ARG SPOTIFLAC_SOURCE_SHA256=66301166b55020ec5ad46082f5c7acbe4811112aed996cd36b1fd8220e1b3754
ARG SPOTIFLAC_EXTENSION_COMMIT=6a4227aec696cd98d6fa9d25d92b1a38f9ae1a07
ARG TIDAL_WEB_SHA256=d346f3e5fdb6f349d8f6ede1310d1961862936f64b3dabbbb4fba868cea31a9a
ARG QOBUZ_WEB_SHA256=3b5fab92608ada9eefe94dcc11a11bef79204804dee3ced2d86809df0d5306cd
ARG DEEZER_SHA256=f6a2505da67c25d4ddfc5f233fdc14e127031f7e15a39fc9d2d0b82fc4b1fd60

FROM golang:1.27 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/acquisition-gateway ./cmd/acquisition-gateway

FROM python:3.12-slim AS spotiflac-builder
ARG SPOTIFLAC_COMMIT
ARG SPOTIFLAC_SOURCE_SHA256
RUN apt-get update && apt-get install -y --no-install-recommends binutils ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN curl -fsSL \
      "https://codeload.github.com/BartolomeoRusso9/SpotiFLAC-Module-Version/tar.gz/${SPOTIFLAC_COMMIT}" \
      -o /tmp/spotiflac-source.tar.gz \
    && printf '%s  %s\n' "$SPOTIFLAC_SOURCE_SHA256" /tmp/spotiflac-source.tar.gz | sha256sum -c - \
    && tar -xzf /tmp/spotiflac-source.tar.gz --strip-components=1 \
    && rm /tmp/spotiflac-source.tar.gz \
    && python -m pip install --no-cache-dir pyinstaller==6.14.1 . \
    && pyinstaller --clean --noconfirm --onefile --name spotiflac \
         --add-data=SpotiFLAC/frontend:SpotiFLAC/frontend \
         --add-data=SpotiFLAC/extensions/_bridge.js:SpotiFLAC/extensions \
         --console launcher.py \
    && mkdir -p /out \
    && pyi-archive_viewer -l dist/spotiflac | tee /out/build-contents.txt \
    && grep -Fq 'SpotiFLAC/extensions/_bridge.js' /out/build-contents.txt \
    && cp dist/spotiflac /out/spotiflac \
    && chmod 0555 /out/spotiflac

FROM golang:1.27 AS extension-installer
ARG DENYRA_RELEASE_REFRESH=manual
ARG SPOTIFLAC_EXTENSION_COMMIT
ARG TIDAL_WEB_SHA256
ARG QOBUZ_WEB_SHA256
ARG DEEZER_SHA256
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY scripts/extract-sflx ./scripts/extract-sflx
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && mkdir -p /opt/spotiflac/artifacts /opt/spotiflac/runtime-home/.spotiflac/extensions \
    && install_extension() { \
         provider="$1"; expected_version="$2"; expected_sha="$3"; \
         artifact="/opt/spotiflac/artifacts/$provider.sflx"; \
         destination="/opt/spotiflac/runtime-home/.spotiflac/extensions/$provider"; \
         curl -fsSL \
           "https://raw.githubusercontent.com/spotiflacapp/SpotiFLAC-Extension/${SPOTIFLAC_EXTENSION_COMMIT}/extensions/$provider.sflx" \
           -o "$artifact"; \
         printf '%s  %s\n' "$expected_sha" "$artifact" | sha256sum -c -; \
         go run ./scripts/extract-sflx -source "$artifact" -destination "$destination"; \
         jq -e --arg provider "$provider" --arg version "$expected_version" \
           '.name == $provider and .version == $version and (.type | index("download_provider") != null) and (.minAppVersion | type == "string" and length > 0) and (.requiredRuntimeFeatures | type == "array" and length > 0)' \
           "$destination/manifest.json" >/dev/null; \
       }; \
       install_extension tidal-web 1.2.0 "$TIDAL_WEB_SHA256"; \
       install_extension qobuz-web 1.2.2 "$QOBUZ_WEB_SHA256"; \
       install_extension deezer 1.3.0 "$DEEZER_SHA256"; \
       chmod -R a-w /opt/spotiflac/artifacts /opt/spotiflac/runtime-home/.spotiflac/extensions

FROM node:24-trixie-slim
ARG DENYRA_GIT_COMMIT=unknown
ARG SPOTIFLAC_COMMIT
ARG SPOTIFLAC_EXTENSION_COMMIT
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg flac \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/acquisition-gateway /app/acquisition-gateway
COPY --from=spotiflac-builder /out/spotiflac /opt/spotiflac/spotiflac
COPY --from=spotiflac-builder /out/build-contents.txt /opt/spotiflac/build-contents.txt
COPY --from=extension-installer /opt/spotiflac/artifacts /opt/spotiflac/artifacts
COPY --from=extension-installer /opt/spotiflac/runtime-home /opt/spotiflac/runtime-home
RUN mkdir -p /opt/spotiflac/runtime-home/.cache/spotiflac /opt/spotiflac/runtime-home/.spotiflac/signed_sessions \
    && chown -R 1000:1000 /opt/spotiflac/runtime-home/.cache /opt/spotiflac/runtime-home/.spotiflac/signed_sessions
ENV DENYRA_GIT_COMMIT=$DENYRA_GIT_COMMIT \
    HOME=/opt/spotiflac/runtime-home \
    SPOTIFLAC_DISABLE_AUTO_INSTALL=1 \
    SPOTIFLAC_DISABLE_AUTO_UPDATE=1 \
    SPOTIFLAC_CACHE_DIR=/opt/spotiflac/runtime-home/.cache/spotiflac \
    SPOTIFLAC_REGISTRIES=" "
LABEL org.opencontainers.image.title="Denyra Acquisition Gateway"
LABEL org.opencontainers.image.spotiflac.commit=$SPOTIFLAC_COMMIT
LABEL org.opencontainers.image.spotiflac-extension.commit=$SPOTIFLAC_EXTENSION_COMMIT
LABEL io.denyra.project="denyra"
USER 1000:1000
ENTRYPOINT ["/app/acquisition-gateway"]
