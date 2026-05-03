#!/bin/sh
# entrypoint.sh — Set ownership and start resolvarr.
#
# Dolby Vision tools (ffmpeg + dovi_tool) are now baked into the image
# via the Dockerfile dv-tools stage. They live at /usr/bin/ffmpeg and
# /usr/local/bin/dovi_tool with the .so closure under /usr/lib. No
# install logic here — DV detail tagging Just Works.
#
# Migration note: testers who previously had ENABLE_DV_TOOLS=true set
# on the container template can leave the env var alone (it's now
# ignored) or remove it from their container config. Legacy install
# remnants under /config/tools/ are also ignored — the image-bundled
# binaries take precedence via $PATH lookup. Users who want to clean
# up can `rm -rf /config/tools` after switching to the baked-in image.

PUID=${PUID:-99}
PGID=${PGID:-100}

# Heads-up message for users who still have the env var set, so they
# know it's no longer needed. Harmless to leave; harmless to delete.
if [ -n "${ENABLE_DV_TOOLS:-}" ]; then
    echo "[entrypoint] Note: ENABLE_DV_TOOLS is no longer needed — DV tools are baked into the image. You can remove this variable from your container template (it's ignored either way)."
fi

# Heads-up for legacy /config/tools/ install remnants.
if [ -d /config/tools ] && [ -f /config/tools/dovi_tool ]; then
    echo "[entrypoint] Note: legacy /config/tools/ install detected. The image now ships DV tools natively — you can safely 'rm -rf /config/tools' (the legacy binaries are used until you do)."
fi

if [ -d /config ]; then
    chown -R "$PUID:$PGID" /config
fi

exec su-exec "$PUID:$PGID" resolvarr
