# Resolvarr

![GitHub Release](https://img.shields.io/github/v/release/prophetse7en/resolvarr?label=latest) ![GitHub last commit](https://img.shields.io/github/last-commit/prophetse7en/resolvarr/main?label=last%20commit)

A helper container for Radarr and Sonarr. Tags releases by their group. Recovers release groups Radarr/Sonarr lost during import. Discovers new groups in your library. Mirrors tag decisions between instances. Runs all of it on a schedule, through a web UI on port 6075.

Resolvarr is the next step for the [tagarr](https://github.com/prophetse7en/tagarr) bash toolkit. Same matching and recovery logic, now wrapped in a web UI. Scheduling, drill-down inspection, multi-agent notifications, and a wizard for chaining actions all live in the UI. The bash scripts continue to work, and you can run both side by side.

Free, open source, and self-hosted.

> ⚠️ **Status: early access (`:dev` only).** Active development. Expect occasional bugs and UI quirks. **Wizard controls and the rule shape can shift between dev builds and are not always backwards-compatible.** Existing rules auto-convert on first start, so you don't lose work. A first stable `:latest` ships after a soak period, and Community Apps listing follows that. **Please open an [issue](https://github.com/prophetse7en/resolvarr/issues) if you hit anything.** That's the whole point of this stage.

## Features

### Tag library
Tag movies (Radarr) or series (Sonarr) with the release-group name when a release passes your active filters. Preview every decision before applying. Nothing changes in Radarr/Sonarr until you click Apply. Tags are auto-created the first time a group earns one, so there's no manual setup of the tag list itself.

### Discover release groups
Walk the entire library and surface release groups that pass your filters but aren't yet in your Active list. Each group expands to show sample movies and episodes, so you can decide whether to add it. An optional audit mode also includes already-known groups, so you can re-validate decisions over time.

### Recover missing release groups
When Radarr/Sonarr loses a release group during import, resolvarr walks the grab history to find which group originally provided the file and writes it back. Results are split into clear buckets (would-fix, fixed, fix-failed, flagged, no-release-group, no-history, failed-verify) with per-row exclude checkboxes, so you can skip individual items before applying.

### Cleanup unused tags
Removes release-group tags from Radarr/Sonarr that no longer match anything in your config. Safety boundary: only tags derived from your configured release groups are eligible. Quality-profile tags, manually-added tags, and tags from other tools are never touched.

### Multi-instance sync
Mirrors tag decisions from a primary Radarr to a secondary one (for example, a 4K instance) by matching on TmdbID. Each instance keeps its own tag set; the sync just makes sure they agree. An optional orphan-cleanup pass removes tags from secondary movies the primary no longer tags. Sync can be skipped per-rule when you want one-off scans without touching the secondary.

### Audio tags
Tags movies and series with their audio configuration: codec (TrueHD, DTS-HD MA, DDP, and so on) and channel layout (`5-1`, `7-1`, `mono`, `stereo`). Values come from Radarr/Sonarr's mediaInfo data. A per-bucket toggle, optional tag prefix, and per-value allow-list let you opt in to only the tags you care about.

### Video tags
Tags movies and series with their video format: codec (h264, h265, AV1) and HDR variant (HDR10, HDR10+, HLG, DV). Values come from Radarr/Sonarr's mediaInfo data. Same per-bucket and per-value controls as Audio tags.

### Dolby Vision detail
Adds tags for the Dolby Vision profile (P5, P7, P8), layer (FEL, MEL), and CM version (CM2, CM4). dovi_tool and ffmpeg are baked into the image, so DV tagging works out of the box with no extra setup. Quality profiles can then prefer specific DV variants. A live progress banner shows the current file and processed/total during DV scans, with a Cancel button that works mid-scan and from any tab in the UI.

Unlike the other tag types, which read pre-extracted MediaInfo data via the Radarr/Sonarr API, DV detail needs **read access to the actual media files**, because dovi_tool inspects the file's first frame directly. That means the container must have your media library mounted. You also need to configure **Path mappings** under Settings → Instances, so resolvarr can translate the file paths Radarr/Sonarr report into paths the container can open. Read-only is sufficient. Resolvarr never writes to your media files.

### Quick fix-all wizard
Run several actions back-to-back in one configurable chain: Tag library, Recover, Cleanup, Audio / Video / DV scans, sync to secondary, in any combination. Each chain leaves a result panel you can drill into per phase. Re-fire the same chain in apply mode without re-walking the wizard, using the Apply button on the result panel.

### Schedules
Save any combination of actions, filters, release groups, and extra-tag rules as a reusable rule. Schedule it on a cron expression, or save as Manual-only and trigger on demand. Each rule keeps its own snapshot of filters, groups, and extra-tag config, so changing your global defaults doesn't perturb already-saved schedules. Per-rule history keeps the last 7 runs, viewable in the Activity tab.

### Tag inventory drill-down
Browse every tag known to your Radarr/Sonarr. Click any tag to see exactly which movies or series carry it, with file context (release group, scene name, relative path highlighted in each row). Compare two tags side-by-side with a Venn-style diff (in-both, only-A, only-B). Useful for sanity-checking decisions or finding overlap between groups.

### Scan history
Every scan dumps its decisions to a JSON file under `/config/logs/`. Browse them in the Activity tab, filter by action type, and click a row to hydrate its result panel for review. The Apply button is automatically disabled when viewing a historical snapshot, so you always run a fresh Preview before applying. Old dumps prune automatically once retention limits are hit.

### Multi-agent notifications
Configure one or more notification agents (Discord, Gotify, NTFY, Pushover, Apprise) and pick which events fire to each. Per-agent test buttons. Discord embeds carry a coloured sidebar matching event severity, and long detail messages auto-chunk so nothing is lost.

### Plex label sync
Push selected Radarr/Sonarr tags onto matching Plex movies and series as **Labels** or **Collections** (or both). Whitelist-strict by design, so manual Plex labels outside the whitelist are never touched. A per-tag display override lets you render Radarr's `atmos` as Plex's `Atmos`. Real-time per-item sync on webhook import and delete events, plus on-demand Run with Preview or Apply mode. Inspired by [DAPS labelarr](https://github.com/Drazzilb08/daps/blob/dev/modules/labelarr.py). Same core idea, rewritten in Go with per-event triggers, dual Label / Collection target, and inline webhook integration.

### Multi-instance
Connect any number of Radarr and Sonarr instances. Per-instance feature visibility: Radarr-only features (DV detail, TMDb-based sync) don't show for Sonarr instances and vice-versa, so each picker presents only what's relevant for the selected instance type.

### Authentication
Login required by default. Three modes:

- **Forms** (default). Standard username/password form with a session cookie.
- **Basic.** HTTP Basic Auth, for when an upstream reverse proxy handles login.
- **None.** Disables auth entirely. Requires password confirmation to enable. Blast radius is catastrophic.

A Trusted Networks list lets devices on your LAN skip the login page (Radarr-parity defaults: all RFC1918, link-local, and loopback). Brute-force protection on `/setup`, `/login`, and password-change endpoints (5 attempts per IP per minute, then HTTP 429 with `Retry-After`). API key for scripts and dashboards (Homepage, Uptime Kuma, and so on).

## Quick Start

Pick the install path that matches your setup:

- **Plain Docker.** Copy the `docker run` command below.
- **Docker Compose.** Copy the YAML in [Docker Compose](#docker-compose).
- **Unraid.** See the [Unraid](#unraid) section for a one-line `curl` that drops the template onto your boot disk, so you can add the container from the Docker tab as usual.

### 1. Run with Docker

```bash
docker run -d \
  --name resolvarr \
  --restart unless-stopped \
  -p 6075:6075 \
  -v /path/to/config:/config \
  -v /path/to/media:/data/media:ro \
  -e PUID=99 -e PGID=100 -e TZ=Europe/Oslo \
  ghcr.io/prophetse7en/resolvarr:dev
```

The `/data/media` mount is only needed if you plan to use Dolby Vision detail tagging. The other tag types don't need file access. The container path defaults to `/data/media` to match the standard TRaSH-Guides / Servarr layout. If your Radarr/Sonarr already mount the share at `/data/media`, no Path Mapping is needed inside resolvarr.

Open the Web UI at `http://your-host:6075`.

### 2. Initial setup

1. Open `http://your-host:6075`. You'll be redirected to `/setup` on first run to create an admin account.
2. After login, go to **Settings** and add your Radarr/Sonarr instance (URL + API key).
3. Switch to **Library scan**, then configure your **Release Groups** and **Filters** under the relevant sub-tabs.
4. Click **Run** in **Preview** mode to see what would happen, then **Apply** when you're satisfied.

## Docker

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TZ` | No | `UTC` | Container timezone. |
| `PUID` | No | `99` | User ID for file ownership. |
| `PGID` | No | `100` | Group ID for file ownership. |
| `PORT` | No | `6075` | Web UI port inside the container. |
| `CONFIG_DIR` | No | `/config` | Persistent config root. If changed, mount the same path as a volume. |
| `TRUSTED_NETWORKS` | No | *(empty, uses Radarr-parity defaults)* | Lock **Trusted Networks** at the host level. Comma-separated IPs and CIDRs. When set, the UI field is disabled and can only be changed by editing the template and restarting. |
| `TRUSTED_PROXIES` | No | *(empty)* | Lock **Trusted Proxies** at the host level. Same UI-disabled behavior as `TRUSTED_NETWORKS`. Only needed when resolvarr sits behind a reverse proxy that terminates TLS. |

### Volumes

| Container Path | Purpose |
|---------------|---------|
| `/config` | Configuration, scan history logs, schedule data. |
| `/data/media` (default; matches TRaSH/Servarr layout) | **Only required for Dolby Vision detail.** Mount your media library so resolvarr can read files for dovi_tool. Read-only (`:ro`) is sufficient. The default container path `/data/media` matches the standard TRaSH-Guides / Servarr layout. If your Radarr/Sonarr also mount the share at `/data/media` (the typical case), no Path Mapping is needed inside resolvarr. If your Radarr/Sonarr uses a different in-container path, either change the container path here to match, or keep `/data/media` and add a Path Mapping under Settings → Instances inside the web UI to translate Radarr's path prefix into `/data/media`. Skip this volume entirely if you're not using DV detail tagging. |

### Ports

| Port | Purpose |
|------|---------|
| `6075` | Web UI and API. |

### Docker Compose

Save the YAML below as `docker-compose.yml`, then run `docker compose up -d` from the same directory. The relative `./resolvarr-config` path creates a folder next to the compose file the first time the container starts.

```yaml
services:
  resolvarr:
    image: ghcr.io/prophetse7en/resolvarr:dev
    container_name: resolvarr
    restart: unless-stopped
    ports:
      - "6075:6075"
    environment:
      - TZ=Europe/Oslo
      - PUID=99
      - PGID=100
    volumes:
      - ./resolvarr-config:/config
      # Only needed for Dolby Vision detail tagging. Read-only is fine.
      # Default container path is /data/media (matches the TRaSH/Servarr
      # layout). If your Radarr/Sonarr uses a different in-container
      # path, either change /data/media here to match, or add a Path
      # Mapping under Settings → Instances inside the web UI.
      # - /path/to/media:/data/media:ro
```

**Updating.** `docker compose pull && docker compose up -d` pulls the newest `:dev` image and recreates the container. Your config folder persists across updates.

### Building from source

```bash
git clone https://github.com/prophetse7en/resolvarr.git
cd resolvarr
docker build -t resolvarr .
docker run -d --name resolvarr -p 6075:6075 \
  -v ./config:/config resolvarr
```

### Healthcheck

The container includes a built-in healthcheck that verifies the web UI is responsive. Docker (and platforms like Unraid and Portainer) will show the container as healthy when the API responds.

### Unraid

While we're in early access, resolvarr is **not** yet listed in Community Apps. Install the template manually:

1. Open the Unraid terminal (or SSH in as `root`) and run this one-liner. It downloads our template and saves it to the user-templates folder on the persistent boot disk (`/boot/...`), so it survives reboots:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/prophetse7en/resolvarr/main/unraid-template.xml \
     -o /boot/config/plugins/dockerMan/templates-user/my-resolvarr.xml
   ```

2. In the Unraid web UI, go to the **Docker** tab, then **Add Container**, open the **Template** dropdown at the top of the form, and pick **resolvarr** from the list.
3. The form fills in with the defaults from the template: port `6075`, config path `/mnt/user/appdata/resolvarr`, and an optional `/data/media` mount (defaults to `/mnt/user/data/media`) for Dolby Vision detail. Most users can leave everything as-is. Click **Apply** to create and start the container.
4. Open the WebUI link Unraid shows for the container (or `http://your-unraid-ip:6075`) to land on the first-run setup wizard.

To update the template later (when we ship a new version with new env vars or paths), just re-run the same `curl` command. It overwrites the file with the latest version. Existing container settings are preserved; only the template definition changes. To pick up a new image, click the resolvarr container icon in the Docker tab and select **Force Update**.

Listing in Community Apps will follow once `:latest` is published and a soak period has confirmed stability.

## Coexistence with tagarr scripts

Resolvarr does what `tagarr.sh`, `tagarr_recover.sh`, and the discovery flag together do, plus features the scripts don't have: web UI, multi-agent notifications, scheduling, scan history, drill-down inventory, audio/video/DV auto-tags, multi-instance sync UI, and Dolby Vision detail.

The [tagarr bash scripts](https://github.com/prophetse7en/tagarr) continue to be maintained for users who prefer cron-driven, file-based workflows. The two don't share state but target the same tag patterns, so you can run both side by side or migrate from one to the other gradually.

## Acknowledgments

Resolvarr is built on top of:

- **[Radarr](https://radarr.video/) and [Sonarr](https://sonarr.tv/).** Every read and write goes through their REST API. Tested against Sonarr 4+ and Radarr 5+ (Radarr 6 supported). The audio, video, and HDR tag values come from the MediaInfo data Radarr/Sonarr already extract from your files.
- **[MediaInfo](https://mediaarea.net/en/MediaInfo).** Source of the codec, audio, channel-layout, and HDR information surfaced through Radarr/Sonarr.
- **[dovi_tool](https://github.com/quietvoid/dovi_tool).** Baked into the image. Extracts Dolby Vision RPU details (profile, layer, CM version) from the file. The tagging concept for codec, audio, and Dolby Vision detail is inspired by similar community tagging tools.
- **[tagarr (bash)](https://github.com/prophetse7en/tagarr).** Origin of the matching, recovery, and discovery logic. The Go engine in resolvarr is a direct port of `tagarr.sh` and `tagarr_recover.sh` with byte-for-byte parity on tag decisions.
- **[DAPS](https://github.com/Drazzilb08/daps).** The Plex label sync feature is inspired by DAPS' `labelarr` module. Resolvarr's implementation is an independent rewrite in Go with the same core idea (Arr tags → Plex labels), plus real-time per-event triggers, dual Label and Collection targets, per-tag display overrides, and inline webhook integration.

## Disclaimer

While resolvarr is tested before each release, it modifies Radarr/Sonarr metadata (tags, release groups), and a successful tag write can trigger Radarr/Sonarr's own renaming workflows depending on your settings. Always run in **Preview** mode first, and review the result panel before clicking **Apply**. Keep backups of your Radarr/Sonarr databases.

The authors are not responsible for any unintended changes to your media automation setup. **Use at your own risk.**

## Support

- **GitHub.** [prophetse7en/resolvarr/issues](https://github.com/prophetse7en/resolvarr/issues) for bug reports and feature requests.

## Development

Resolvarr is developed with active AI assistance (Claude, Anthropic) under human direction. Architectural decisions, code review, testing against real Radarr/Sonarr instances, and releases are done by ProphetSe7en. Issues and pull requests go through human review.

## License

[MIT](LICENSE)
