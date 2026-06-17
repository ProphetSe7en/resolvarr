FROM golang:1.26-alpine AS builder

ARG VERSION=0.6.54-dev

RUN apk add --no-cache git

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY ui/ ./ui/
RUN go build -ldflags="-s -w -X main.Version=${VERSION}" -o resolvarr .

# DV-tools stage — bake ffmpeg + dovi_tool into the final image so DV
# detail tagging works zero-config. Apk-install ffmpeg in a throwaway
# stage, then ldd-extract just the binary + its closure (~36 MB) into
# /staging — saves us the full apk dependency tree (~130 MB) which we
# don't use. dovi_tool is a single static musl binary from upstream.
#
# Why this stage instead of the previous runtime install via
# ENABLE_DV_TOOLS env var:
#   - Zero config — every install gets DV tagging out of the box
#   - No first-start delay (~10-15s of apk + wget at container boot)
#   - No network dependency at startup (Alpine repos / GitHub release)
#   - Removes a per-tester support surface ("did you set the env var?")
#
# Image size cost: ~+115 MB vs the previous ~19 MB base (final ~133 MB).
# Measured higher than initial estimate (~36 MB): ldd's closure for
# ffmpeg pulls in 116 .so files, including many transitive deps that
# are needed at link time even if we never call those codepaths.
# Saving more would require compiling ffmpeg with --disable-everything
# and explicit --enable-* flags — heavier maintenance burden for
# marginal benefit. Final size is still well under mainstream Arr
# helper containers (linuxserver/radarr ~190 MB, hotio/sonarr ~250 MB).
FROM alpine:3.23 AS dv-tools

ARG DOVI_VERSION=2.1.2

RUN set -o pipefail && \
    apk add --no-cache ffmpeg wget && \
    mkdir -p /staging/usr/bin /staging/usr/lib && \
    cp /usr/bin/ffmpeg /staging/usr/bin/ && \
    # ldd lists the .so closure; keep only absolute paths and strip
    # the linker line. -L follows symlinks so we copy the real file.
    ldd /usr/bin/ffmpeg | awk '/=>/ {print $3} /^[[:space:]]*\// {print $1}' \
        | grep '^/' | sort -u \
        | xargs -I{} cp -L {} /staging/usr/lib/ && \
    # dovi_tool — pick arch by uname for amd64 / arm64 multi-arch support
    if [ "$(uname -m)" = "x86_64" ]; then DOVI_ARCH="x86_64-unknown-linux-musl"; \
    else DOVI_ARCH="aarch64-unknown-linux-musl"; fi && \
    wget -qO- "https://github.com/quietvoid/dovi_tool/releases/download/${DOVI_VERSION}/dovi_tool-${DOVI_VERSION}-${DOVI_ARCH}.tar.gz" \
        | tar -xz -C /staging/usr/bin/ && \
    chmod +x /staging/usr/bin/dovi_tool && \
    # Smoke-test the staged binaries. If the linker can't resolve all
    # libs the build fails here, not silently at runtime. No pipes so
    # SIGPIPE can't trip pipefail; output redirected to /dev/null —
    # we only care about exit codes.
    /staging/usr/bin/dovi_tool --version >/dev/null && \
    LD_LIBRARY_PATH=/staging/usr/lib /staging/usr/bin/ffmpeg -version >/dev/null

FROM ghcr.io/prophetse7en/container-base:alpine-3.21

ARG VERSION=0.6.54-dev
LABEL org.opencontainers.image.version=${VERSION} \
      org.opencontainers.image.title="resolvarr" \
      org.opencontainers.image.description="Helper container for Radarr and Sonarr — release-group tagging, recovery, multi-instance sync, and scheduled scans" \
      org.opencontainers.image.source="https://github.com/prophetse7en/resolvarr" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /build/resolvarr /usr/local/bin/resolvarr
COPY --from=dv-tools /staging/ /
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME /config

ENV PUID=99
ENV PGID=100
ENV PORT=6075
EXPOSE 6075

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:6075/api/health || exit 1

CMD ["/entrypoint.sh"]
