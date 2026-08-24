# syntax=docker/dockerfile:1
FROM golang:1.27 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/media-pipeline ./cmd/media-pipeline \
    && CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/denyra-restore-check ./cmd/denyra-restore-check \
    && CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/denyra-acceptance-fixture ./cmd/denyra-acceptance-fixture

FROM python:3.14-slim
ARG DENYRA_RELEASE_REFRESH=manual
RUN test -n "$DENYRA_RELEASE_REFRESH" \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg flac \
    && python -m pip install --no-cache-dir 'beets>=2,<3' \
    && python -m pip check \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/media-pipeline /app/media-pipeline
COPY --from=go-builder /out/denyra-restore-check /app/denyra-restore-check
COPY --from=go-builder /out/denyra-acceptance-fixture /app/denyra-acceptance-fixture
ARG DENYRA_GIT_COMMIT=unknown
ENV DENYRA_GIT_COMMIT=$DENYRA_GIT_COMMIT \
    PYTHONNOUSERSITE=1 \
    HOME=/tmp \
    XDG_CONFIG_HOME=/tmp/.config
LABEL org.opencontainers.image.title="Denyra Media Pipeline"
USER 1000:1000
ENTRYPOINT ["/app/media-pipeline"]
