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
    go build -trimpath -ldflags='-s -w' -o /out/media-pipeline ./cmd/media-pipeline

FROM docker.io/library/debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258 AS python-builder
COPY deploy/docker/debian.sources /etc/apt/sources.list.d/debian.sources
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates=20250419 gcc=4:14.2.0-1 make=4.4.1-2 libc6-dev=2.41-12+deb13u3 \
    zlib1g-dev=1:1.3.dfsg+really1.3.1-1+b1 libssl-dev=3.5.6-1~deb13u2 \
    libbz2-dev=1.0.8-6 libreadline-dev=8.2-6 libffi-dev=3.4.8-2 \
    liblzma-dev=5.8.1-1+deb13u1 libsqlite3-dev=3.46.1-7+deb13u1 \
    tk-dev=8.6.16 uuid-dev=2.41.5-0+deb13u1 xz-utils=5.8.1-1+deb13u1 \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:3b48dac8fb59f62eaa67ac83c1eb12bda1b7a08406dd286e252c11a66be27f81 \
    https://www.python.org/ftp/python/3.14.7/Python-3.14.7.tar.xz /tmp/python.tar.xz
RUN mkdir /tmp/python && tar -C /tmp/python --strip-components=1 -xJf /tmp/python.tar.xz \
    && cd /tmp/python && ./configure --prefix=/opt/python --with-ensurepip=install \
    && make -j2 && make install
COPY deploy/python/requirements.lock /tmp/requirements.lock
RUN /opt/python/bin/python3 -m pip download --require-hashes --dest /tmp/wheelhouse -r /tmp/requirements.lock \
    && /opt/python/bin/python3 -m pip install --no-index --find-links=/tmp/wheelhouse --require-hashes -r /tmp/requirements.lock \
    && /opt/python/bin/python3 -m pip check

FROM docker.io/library/debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258
COPY deploy/docker/debian.sources /etc/apt/sources.list.d/debian.sources
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates=20250419 ffmpeg=7:7.1.5-0+deb13u1 flac=1.5.0+ds-2 \
    libsqlite3-0=3.46.1-7+deb13u1 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10002 denyra && useradd --uid 10002 --gid 10002 --home-dir /nonexistent --shell /usr/sbin/nologin denyra
COPY --from=go-builder /out/media-pipeline /app/media-pipeline
COPY --from=python-builder /opt/python /opt/python
COPY --chmod=0444 deploy/docker/generated/pipeline-build-provenance.json /app/build-provenance.json
ENV PATH=/opt/python/bin:$PATH PYTHONNOUSERSITE=1 PIP_NO_INDEX=1 HOME=/tmp XDG_CONFIG_HOME=/tmp/.config
LABEL org.opencontainers.image.title="Denyra Media Pipeline" org.opencontainers.image.version="baseline"
USER 10002:10002
ENTRYPOINT ["/app/media-pipeline"]
