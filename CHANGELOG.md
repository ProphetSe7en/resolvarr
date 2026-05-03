# Changelog

## v0.3.0-dev — first public early-access build (2026-05-03)

First release published to GHCR as `:dev`. Published while we collect feedback from a small group of early testers — occasional breaking changes between dev builds are expected. A first stable `:latest` will land after a soak period.

Resolvarr is the next step for the [tagarr](https://github.com/prophetse7en/tagarr) bash toolkit, packaged as a Go container with a web UI on port 6075.

### What you get

- **Tag library** — scan all your movies (Radarr) or series (Sonarr) and tag each one with its release-group name, after the filters you set up. Preview before applying — nothing changes in Radarr/Sonarr until you click Apply. Tags are auto-created the first time a group earns one, so there's no manual setup of the tag list itself.

- **Recover missing release groups** — for movies/series where Radarr/Sonarr lost the release group during import, resolvarr walks the grab history to find which group originally provided the file and writes it back. Results split into clear buckets (would-fix / fixed / fix-failed / flagged / no-release-group / no-history / failed-verify) with per-row exclude checkboxes so you can skip individual items.

- **Discover release groups** — surface groups in your library that pass your filters but aren't yet in your Active list. Each group expands to show sample movies/episodes so you can decide whether to add it. Optional audit mode includes already-known groups for re-validation.

- **Cleanup unused tags** — remove release-group tags that no longer match anything in your config. Quality-profile tags, manually-added tags, and tags from other tools are never touched.

- **Multi-instance sync** — mirror tag decisions from your primary Radarr to a secondary one (e.g. a 4K instance) by matching on movie ID. Optional orphan cleanup keeps the secondary in sync when you remove tags on the primary. Sync can be skipped per-rule when you want one-off scans without touching the secondary.

- **Audio tags** — codec (TrueHD, DTS-HD MA, DDP, etc.) and channel layout (5-1, 7-1, mono, stereo) read from MediaInfo. Pick which audio types you want as tags and how the labels look.

- **Video tags** — codec (h264, h265, AV1) and HDR variant (HDR10, HDR10+, HLG, DV) read from MediaInfo. Same per-type controls as Audio.

- **Dolby Vision detail** *(opt-in via `ENABLE_DV_TOOLS=true`)* — extract Dolby Vision specifics (profile P5/P7/P8, layer FEL/MEL, CM version CM2/CM4) directly from the file using dovi_tool. Useful when you want quality profiles to prefer specific DV variants. A live progress banner shows the current file and how many are left during scans, with a Cancel button that works mid-scan from any tab. Requires read access to your media library and a Path Mapping configured under Settings → Instances.

- **Quick fix-all wizard** — chain several actions in one run. Tag, Recover, Cleanup, Audio/Video/DV scans, sync to secondary — pick any combination. Each chain leaves a result panel you can drill into per phase. Apply-after-preview re-fires the chain in apply mode without re-walking the wizard.

- **Schedules** — save any combination of actions and filters as a reusable rule. Schedule it on a cron expression or save as Manual-only and trigger on demand. Each rule keeps its own snapshot of filters, groups, and extra-tag config — so changing your global defaults later doesn't perturb already-saved schedules. Per-rule history with the last seven runs visible in the Activity tab.

- **Tag inventory drill-down** — browse every tag in your Radarr/Sonarr, click any tag to see exactly which movies/series carry it (with file context: release group, scene name, relative path highlighted). Compare two tags side-by-side with a Venn-style diff (in-both / only-A / only-B) for sanity-checking decisions or finding overlap between groups.

- **Scan history** — every scan saves its decisions to disk for later review under the Activity tab. Filter by action type, click a row to bring back the result panel for inspection. The Apply button is automatically disabled when viewing a saved snapshot — run a fresh Preview before applying. Old saves prune automatically.

- **Multi-agent notifications** — Discord, Gotify, NTFY, Pushover, Apprise. Multiple agents per type, per-event routing, per-agent test button. Discord embeds carry a coloured sidebar matching event severity and auto-chunk long detail messages.

- **Login required by default** — Forms (username/password), HTTP Basic, or None (with a safety prompt before disabling). Trusted Networks list lets devices on your LAN skip the login page. Brute-force protection blocks repeated failed login attempts.

- **Multi-instance** — connect any number of Radarr and Sonarr instances. Per-instance feature visibility — Radarr-only features (Dolby Vision detail, TMDb-based sync) don't show for Sonarr instances and vice-versa.
