# syntax=docker/dockerfile:1.18
FROM docker.io/library/debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258 AS go-builder
COPY deploy/docker/debian.sources /etc/apt/sources.list.d/debian.sources
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates=20250419 gcc=4:14.2.0-1 libc6-dev=2.41-12+deb13u3 \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:675c26c449cbb18fc24b74650de1eabbae6e16f64326fd85a283fb3b58280685 \
    https://go.dev/dl/go1.27.0.linux-amd64.tar.gz /tmp/go.tar.gz
RUN tar -C /usr/local -xzf /tmp/go.tar.gz && rm /tmp/go.tar.gz
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local CGO_ENABLED=1
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags='-s -w' -o /out/acquisition-gateway ./cmd/acquisition-gateway

FROM docker.io/library/debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258 AS provider-runtime
COPY deploy/docker/debian.sources /etc/apt/sources.list.d/debian.sources
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates=20250419 xz-utils=5.8.1-1+deb13u1 \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647 \
    https://nodejs.org/dist/v24.19.0/node-v24.19.0-linux-x64.tar.xz /tmp/node.tar.xz
RUN mkdir -p /opt/node && tar -C /opt/node --strip-components=1 -xJf /tmp/node.tar.xz && rm /tmp/node.tar.xz
ADD --checksum=sha256:c008b5b59999f6f740d3f8e0290ce5fe18220dcd736aa903469e5b0ac062334a \
    https://github.com/BartolomeoRusso9/SpotiFLAC-Module-Version/releases/download/v3.0.8/SpotiFLAC-Linux-x86_64 /opt/spotiflac/spotiflac
ADD --checksum=sha256:0d59043bab8229b5fd5664bc144aee25bfd3e6d031832cdce48b9d9ccef5ed22 \
    https://raw.githubusercontent.com/spotiflacapp/SpotiFLAC-Extension/8fc37551ead10683d7ab54cb4155dc5cca4948e6/extensions/tidal-web.sflx /opt/spotiflac/extensions/tidal-web.sflx
ADD --checksum=sha256:9e6d14dc37623eed9ac6326c321b17fd802c36e907476f3068f7fcbe14d79f93 \
    https://raw.githubusercontent.com/spotiflacapp/SpotiFLAC-Extension/8fc37551ead10683d7ab54cb4155dc5cca4948e6/extensions/qobuz-web.sflx /opt/spotiflac/extensions/qobuz-web.sflx
ADD --checksum=sha256:dfead5b50889d2855b4409c6796421ccb35ffd3cac1e002498924e9a7c5446b3 \
    https://raw.githubusercontent.com/spotiflacapp/SpotiFLAC-Extension/8fc37551ead10683d7ab54cb4155dc5cca4948e6/extensions/deezer.sflx /opt/spotiflac/extensions/deezer.sflx
RUN chmod 0555 /opt/spotiflac/spotiflac && chmod 0444 /opt/spotiflac/extensions/*.sflx

FROM docker.io/library/debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258
COPY deploy/docker/debian.sources /etc/apt/sources.list.d/debian.sources
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates=20250419 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 denyra && useradd --uid 10001 --gid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin denyra
COPY --from=go-builder /out/acquisition-gateway /app/acquisition-gateway
COPY --from=provider-runtime /opt/node /opt/node
COPY --from=provider-runtime /opt/spotiflac /opt/spotiflac
COPY --chmod=0444 deploy/docker/generated/gateway-build-provenance.json /app/build-provenance.json
ENV PATH=/opt/node/bin:$PATH \
    SPOTIFLAC_DISABLE_AUTO_INSTALL=1 \
    SPOTIFLAC_DISABLE_AUTO_UPDATE=1 \
    SPOTIFLAC_EXTENSION_DIR=/opt/spotiflac/extensions
LABEL org.opencontainers.image.title="Denyra Acquisition Gateway" org.opencontainers.image.version="3.0.8-provider-set"
USER 10001:10001
ENTRYPOINT ["/app/acquisition-gateway"]
