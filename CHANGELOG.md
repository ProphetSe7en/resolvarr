# Changelog

## v0.3.5-dev — Dolby Vision tools baked in + script parity (2026-05-04)

This batch closes the gap to the upstream TRaSH bash script (`Radarr-DV-HDR-Tagarr/dv-hdr_tagarr.sh`) — same per-file output, plus everything resolvarr already does (web UI, multi-instance sync, cache, scheduling, notifications).

### What you get

- **Dolby Vision tools ship baked into the image.** No more `ENABLE_DV_TOOLS=true` env var. No more 10-15 second install delay on first start. No more "Tools required" notice if you forget to set the var. DV detail tagging works zero-config the moment you turn it on under Settings. Image grows from ~19 MB to ~140 MB to carry `ffmpeg` + `dovi_tool` — well under typical Arr-helper containers (linuxserver/radarr ~190 MB).

- **No-DV tag.** When Radarr's mediaInfo says a file has Dolby Vision but the actual stream has no RPU (corrupt file, transcode-in-progress, mediaInfo lying), DV detail now writes a `no-dv` tag so you can write custom formats that distinguish "claimed DV" from "confirmed DV". Mirrors the upstream TRaSH bash script's `no-dv` tag. Toggle via the per-value list in Settings → DV detail.

- **Cache is now bulletproof against file replacements + tool upgrades.** v0.3.4 cached by movie file ID + size. v0.3.5 adds modification time + the dovi_tool version that produced the result. So:
  - Replace a file in-place outside Radarr (rare but possible) — old size, new mtime → cache invalidates → fresh extraction.
  - Upgrade dovi_tool in a future image — every cached entry's version mismatches → cache invalidates → fresh extraction.
  - Default behaviour: trust the cache. Skip-cache checkbox stays available for paranoid cases.

- **Path resolution moved before cache lookup.** Side effect: scans complete much faster on libraries where many files have path-mapping issues — they fail-fast instead of going through the full pipeline. (Earlier today's slow scans turned out to be cold kernel-cache after Force Update; subsequent scans are back to ~50ms per file.)

- **DV detail tab cleanups.** "Opt-in" badge gone — tools are always there. "Tools required" big banner replaced with a small "Tools ready" indicator showing the installed `ffmpeg` + `dovi_tool` versions. If you ever see "Tools unreachable" in red, that's a bug — please report.

### What stays the same

- Audio / Video / DV detail are still **off by default** per rule. Turn each one on from Settings (per-instance config) or per-rule (in the wizard).
- Cache `Clear` button + per-scan `Skip cache` checkbox + per-rule `Skip DV cache` (in wizard) all unchanged.
- Existing v1 caches (from v0.3.4) discard cleanly on first start with v0.3.5 — next DV scan re-extracts. ~10-30 seconds for typical libraries.

### Heads-up

- **Existing testers' container env:** if you previously set `ENABLE_DV_TOOLS=true` on your container template, the variable is now ignored. You can remove it from your container config; harmless either way (entrypoint logs a one-line note if it sees the variable still set).
- **Legacy `/config/tools/`:** if you ever used the old runtime install button (removed pre-v0.3.0), leftover binaries under `/config/tools/` will be used in preference to the baked-in versions. To switch to the image-bundled binaries: `rm -rf /config/tools` (entrypoint logs a note if it sees the directory).
- **First scan after Force Update may feel slower** while the kernel page-cache warms up around the new baked-in libraries. Subsequent scans are back to normal speed.

### Internal

- Multi-stage Dockerfile with `dv-tools` build stage: `apk add ffmpeg` → `ldd`-extract closure → `wget dovi_tool` → smoke-test → `COPY` to final image. Adds ~35s to CI build time.
- `set -o pipefail` on the dv-tools `RUN` for fail-fast on download corruption.
- Cache `cacheFileVersion` 1 → 2; load-time discard on mismatch (no migration needed).
- `EmitNoDvTag(cfg)` helper in the engine, parallel to `EmitDvDetailTags`.
- Defensive re-entry guard on `runQuickFixChain` and `runDvDetailScan` — refuses to fire if a previous run is still in progress (the disabled-button binding already prevents this at the UI layer; this is belt-and-braces).
- Path-failure detection in DV scan handler refactored from substring-matching the user-facing error string to a typed bool flag.

---

## v0.3.4-dev — Dolby Vision cache management (2026-05-03)

> **Heads-up about the version jump:** v0.3.2 and v0.3.3 weren't published to a clean state — v0.3.1's content (then v0.3.1 again with the Activity→History rename) accidentally landed twice. v0.3.4 is the next clean monotone version. No content lost.

### What you get

- **DV detail cache panel** on Library scan → DV detail. See how many files are cached, how big the cache file is, and when the most recent extraction happened — at a glance.

- **Clear DV cache** button. Wipes the cache so the next DV scan re-extracts every file from scratch. Useful when you upgrade `dovi_tool` and want fresh extraction with the new version. The confirm modal shows the exact entry count and size before you commit.

- **Skip cache checkbox** in the DV detail Run controls. One-shot bypass — the next scan ignores the cache entirely (no read, no write). Default off (cache active). Resets to off after Apply so a destructive write doesn't silently inherit a Preview's bypass.

- **Saved rules can pin "Skip DV cache"** via a checkbox in the wizard's DV step. A rule with this set always re-extracts on every fire, no matter what's in the cache. Useful for occasional refresh-extraction rules; less useful as a daily-cron default.

- **Your DV tags in Radarr stay untouched** when you Clear cache or use Skip — only the cached extraction results are wiped or bypassed. Tags get re-applied on the next scan if files still match your bucket config.

### Honest framing

Per-file extraction is faster than this codebase used to claim. On modern remux sources, `ffmpeg` + `dovi_tool` finds the RPU SEI in the first GOP and exits in tens of milliseconds. The cache mostly saves cumulative fork/exec + I/O overhead across hundreds of files — turns "minutes" into "milliseconds" on rescan, not "hours" into "seconds". Still worth having at library scale, but the per-file speed difference is small.

### Why you'd still want it

- After a `dovi_tool` upgrade — a new version may detect different layer/CM-version semantics on the same file, but the cache will keep returning the old result. Use Clear (one-time) or tick Skip cache for a single fresh scan.
- After re-encoding a file that kept the same Radarr movie file ID (rare — the size key normally catches this).
- When debugging "why isn't this file showing the right DV detail" — clear the cache and re-run a scan to see fresh extraction output.

### Internal

- Two pre-existing dvdetect test failures (`TestStatus_NoBinaries` + `TestStatus_HappyPathViaVersionFn`) updated to match current behaviour — `Tools.Status` returns empty paths when binaries don't resolve, not legacy-path strings.

---

## v0.3.1-dev — early-access feedback batch (2026-05-03)

First round of polish based on feedback from the early testers.

### What's better

- **Activity tab renamed to History.** It's purely a scan-history viewer now (saved rules and their per-run history live on Run mode where you create them) — "History" reads more honestly. As a bonus the page heading on Run mode that still said "Activity" got fixed too — it's "Saved rules" now.

- **Scan history actually sticks around now.** Saved scans survived day-to-day, but disappeared every time you Force-Updated the container. Your audit log got reset to empty too. Fixed — both now persist properly across restarts and updates.

- **History tab works as a real history-browser.** Clicking a row used to throw you across to whichever sub-tab the scan originally ran on. Now the result opens as a modal right on History, so you can browse old runs without losing your place. Cleanup is still routed to its own tab (no drill-in modal yet).

- **Dolby Vision setup is much friendlier.**
  - When a DV scan reports "Unreachable" files, you now get an inline banner explaining the cause (almost always a missing or wrong Path Mapping) with a direct pointer to where to fix it.
  - The DV help-panel walks you through the two ways paths can line up: same in-container path as Radarr (no Path Mapping needed) or different paths (one row to add).
  - Same explanation now in the README + Unraid template description for the Media volume so you see it before you even start.

- **Audio/Video/DV tag pickers got Select all/none buttons.** Faster than ticking 12 codecs one by one.

- **Logout button** in the top-right of the UI. Was a glaring omission.

- **Atmos detection.** Some files with Atmos audio weren't being tagged because Radarr's MediaInfo didn't expose it. Resolvarr now also checks the filename for Atmos tokens as a fallback.

- **Light-mode polish.** A couple of pockets where dark-theme colours had leaked into light-mode are fixed.

- **Dev version visible in the UI.** Helps testers report exactly which build they're running.

- **Add-instance dialog.** Placeholders now switch to Radarr/Sonarr-appropriate hints when you pick the type.

- **Dolby Vision tools install fix.** The previous build tried to install ffmpeg as a static binary — it downloaded fine but failed to run on Alpine (wrong libc). Reverted to Alpine's musl-native ffmpeg package. The `ENABLE_DV_TOOLS` toggle is also now a proper dropdown in the Unraid template instead of a free-text field.

### Heads-up

- This is still `:dev`. Force-Update at your own pace, breaking changes between dev builds are still on the table.
- Light theme is shipped but the dark theme is more polished — feedback on either welcome.

---

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
