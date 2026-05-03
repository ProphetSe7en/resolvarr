#!/bin/sh
# entrypoint.sh — Set ownership and start resolvarr.
#
# Optional dependency install: when ENABLE_DV_TOOLS=true is set on
# the container (Unraid template variable; -e flag in plain Docker),
# ffmpeg + dovi_tool are installed before privilege-drop. Idempotent —
# already-installed tools are detected via `command -v` and the
# install steps are skipped. Status is always logged to stdout so a
# `docker logs <container>` after startup tells you exactly what
# happened — no debug toggle needed.
#
# Why entrypoint and not the Dockerfile: opt-in keeps the base image
# lean for users who don't use DV detail. Why entrypoint and not a
# runtime install button: apk + wget need root, the Go process drops
# to PUID/PGID before any request hits a handler — entrypoint is the
# only window where root is available. First start with the var set
# takes ~10-15s longer while binaries download; subsequent starts are
# instant (idempotent).
#
# ffmpeg is pulled as a static binary from the BtbN ffmpeg-builds
# project (https://github.com/BtbN/FFmpeg-Builds). We only need ffmpeg
# to demux HEVC out of MKV/MP4 so dovi_tool can read it; Alpine's
# `ffmpeg` apk package pulls in ~111 transitive deps (libplacebo,
# vulkan, libpulse, dvd/blu-ray libs, AV1/VP9/Theora encoders, etc.)
# — none of which we use. The static build is one binary, ~40 MB
# extracted, vs ~129 MB for the apk path.

PUID=${PUID:-99}
PGID=${PGID:-100}

if [ "${ENABLE_DV_TOOLS:-false}" = "true" ]; then
    echo "[entrypoint] ENABLE_DV_TOOLS=true — preparing Dolby Vision tools"

    # ----- ffmpeg -----
    # We tried BtbN's static builds first (smaller footprint, fewer
    # transitive deps) but they're glibc-linked and Alpine is musl —
    # the binary downloads fine but won't execute (missing
    # /lib64/ld-linux-x86-64.so.2). Alpine's apk-built ffmpeg is musl-
    # native and Just Works, so we use that. The cost is the kitchen-
    # sink dependency tree (~111 packages: libplacebo, vulkan, libpulse,
    # AV1/VP9/Theora encoders, etc) — none of which we touch, but
    # Alpine's package model only ships ffmpeg as a single bundle.
    if command -v ffmpeg >/dev/null 2>&1; then
        echo "[entrypoint] ffmpeg already present at $(command -v ffmpeg) — skipping install"
    else
        echo "[entrypoint] installing ffmpeg via apk (musl-native; ~129 MB with deps)"
        if apk add --no-cache ffmpeg >/dev/null 2>&1; then
            echo "[entrypoint] ffmpeg installed → $(ffmpeg -version 2>/dev/null | head -1)"
        else
            echo "[entrypoint] WARNING: apk add ffmpeg failed — DV detail will not run"
        fi
    fi

    # ----- dovi_tool -----
    if command -v dovi_tool >/dev/null 2>&1; then
        echo "[entrypoint] dovi_tool already present at $(command -v dovi_tool) — skipping install"
    else
        DOVI_VERSION="2.1.2"
        case "$(uname -m)" in
            x86_64)  DOVI_ARCH="x86_64-unknown-linux-musl" ;;
            aarch64) DOVI_ARCH="aarch64-unknown-linux-musl" ;;
            *)       DOVI_ARCH="" ;;
        esac
        if [ -z "$DOVI_ARCH" ]; then
            echo "[entrypoint] WARNING: dovi_tool unsupported arch $(uname -m) — DV detail will not run"
        else
            echo "[entrypoint] downloading dovi_tool ${DOVI_VERSION} (${DOVI_ARCH}, ~10 MB)"
            url="https://github.com/quietvoid/dovi_tool/releases/download/${DOVI_VERSION}/dovi_tool-${DOVI_VERSION}-${DOVI_ARCH}.tar.gz"
            if wget -qO- "$url" | tar -xz -C /usr/local/bin/ 2>/dev/null; then
                chmod +x /usr/local/bin/dovi_tool 2>/dev/null
                echo "[entrypoint] dovi_tool installed"
            else
                echo "[entrypoint] WARNING: dovi_tool download/extract failed — DV detail will not run"
            fi
        fi
    fi
else
    echo "[entrypoint] ENABLE_DV_TOOLS not set — Dolby Vision detail tagging is disabled."
    echo "[entrypoint]   Set ENABLE_DV_TOOLS=true on the container to install ffmpeg + dovi_tool on next start."
    echo "[entrypoint]   In Unraid: edit the resolvarr container, set ENABLE_DV_TOOLS to 'true', click Apply."
fi

if [ -d /config ]; then
    chown -R "$PUID:$PGID" /config
fi

exec su-exec "$PUID:$PGID" resolvarr
