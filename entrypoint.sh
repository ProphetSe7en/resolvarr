#!/bin/sh
# entrypoint.sh — Set ownership and start resolvarr.
#
# Optional dependency install: when ENABLE_DV_TOOLS=true is set on
# the container (Unraid template variable; -e flag in plain Docker),
# ffmpeg + dovi_tool are installed before privilege-drop. Idempotent —
# already-installed tools are detected via `command -v` and the
# install steps are skipped.
#
# Why entrypoint and not the Dockerfile: opt-in keeps the base image
# lean for users who don't use DV detail (~30 MB ffmpeg + ~10 MB
# dovi_tool saved). Why entrypoint and not a runtime install button:
# apk needs root, the Go process drops to PUID/PGID before any
# request hits a handler — entrypoint is the only window where root
# is available. First start with the var set takes ~30s longer
# while it downloads; subsequent starts are instant (idempotent).

PUID=${PUID:-99}
PGID=${PGID:-100}

if [ "${ENABLE_DV_TOOLS:-false}" = "true" ]; then
    if ! command -v ffmpeg >/dev/null 2>&1; then
        echo "[entrypoint] ENABLE_DV_TOOLS=true → installing ffmpeg via apk"
        apk add --no-cache ffmpeg \
            && echo "[entrypoint] ffmpeg installed" \
            || echo "[entrypoint] WARNING: apk add ffmpeg failed — DV detail will not run"
    fi
    if ! command -v dovi_tool >/dev/null 2>&1; then
        DOVI_VERSION="2.1.2"
        case "$(uname -m)" in
            x86_64)  DOVI_ARCH="x86_64-unknown-linux-musl" ;;
            aarch64) DOVI_ARCH="aarch64-unknown-linux-musl" ;;
            *)       DOVI_ARCH="" ;;
        esac
        if [ -n "$DOVI_ARCH" ]; then
            echo "[entrypoint] ENABLE_DV_TOOLS=true → installing dovi_tool ${DOVI_VERSION} (${DOVI_ARCH})"
            url="https://github.com/quietvoid/dovi_tool/releases/download/${DOVI_VERSION}/dovi_tool-${DOVI_VERSION}-${DOVI_ARCH}.tar.gz"
            if wget -qO- "$url" | tar -xz -C /usr/local/bin/ 2>/dev/null; then
                chmod +x /usr/local/bin/dovi_tool 2>/dev/null
                echo "[entrypoint] dovi_tool installed"
            else
                echo "[entrypoint] WARNING: dovi_tool download/extract failed — DV detail will not run"
            fi
        else
            echo "[entrypoint] WARNING: dovi_tool unsupported arch $(uname -m) — DV detail will not run"
        fi
    fi
fi

if [ -d /config ]; then
    chown -R "$PUID:$PGID" /config
fi

exec su-exec "$PUID:$PGID" resolvarr
