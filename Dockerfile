FROM golang:1.25-alpine AS builder

ARG VERSION=0.3.4-dev

RUN apk add --no-cache git

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY ui/ ./ui/
RUN go build -ldflags="-s -w -X main.Version=${VERSION}" -o resolvarr .

FROM ghcr.io/prophetse7en/container-base:alpine-3.21

ARG VERSION=0.3.4-dev
LABEL org.opencontainers.image.version=${VERSION} \
      org.opencontainers.image.title="resolvarr" \
      org.opencontainers.image.description="Helper container for Radarr and Sonarr — release-group tagging, recovery, multi-instance sync, and scheduled scans" \
      org.opencontainers.image.source="https://github.com/prophetse7en/resolvarr" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /build/resolvarr /usr/local/bin/resolvarr
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
