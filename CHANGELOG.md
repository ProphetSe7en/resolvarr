# Changelog

## v0.3.10-dev — qBittorrent foundation + live status + secret hiding (2026-05-07)

### What you get

- **Settings → qBittorrent is live.** Add qBittorrent instances by
  name. Direct URLs work, reverse-proxied URLs work, and qui
  client-proxy URLs work too — for qui just leave Username +
  Password blank, the URL token is the auth. Test Connection
  before saving. Optional TLS-skip toggle for self-signed setups,
  with a clear warning when it's risky to use.

- **Live connection status on every row.** Sonarr / Radarr + qBit
  pills now reflect the actual state, auto-refreshing in the
  background every minute. No more "Connected" pill that's stale
  from a test you ran three days ago. Manual Test button still
  there when you want to force a check.

- **URLs hidden after saving.** The configured-instance rows
  (Sonarr / Radarr, qBittorrent, webhooks) no longer show URLs
  on every page view. You already chose the name, and the
  buttons cover everything you need to do. Open Edit if you
  want to see or change the URL.

- **qui tokens displayed safely after save.** The token in qui
  proxy URLs is your password — qui never shows it again after
  you create it. After save we display it as `http://qui:7476/proxy/602f...c33e`
  so you can confirm you're looking at the right one without
  the full secret being on screen.

- **Webhook Delete button.** Removes a configured webhook entirely
  (was only Regenerate + Logging-toggle before). The URL stops
  working immediately, and the row goes back to "not configured".
  Recent activity log is kept — use Clear log on the Activity
  tab if you want both gone.

- **"Last received" timestamp accurate everywhere.** Used to say
  "Never received an event" on every webhook row except the one
  you'd opened in the Activity dropdown — even when events had
  arrived. Fixed.

- **Copy buttons work on plain HTTP setups.** Browsers block
  modern clipboard access unless you're on HTTPS or localhost.
  Most people run resolvarr on a LAN HTTP URL where Copy was
  silently failing. Falls back automatically now — webhook URL,
  wizard URL, and API key copies all work.

## v0.3.9-dev — Webhook foundation + wizard-everywhere + filter honesty (2026-05-07)

### What you get

- **Webhooks tab is live (logging-only mode).** Configure a webhook
  per Sonarr / Radarr instance via the new wizard, paste the URL
  into your Arr's Settings → Connect, and every event you fire
  lands in the Recent activity panel — full decoded JSON, click
  to expand. Lets you verify Connect setup end-to-end before any
  per-event tagging features ship in subsequent sessions.

- **Tag Audio / Tag Video / Tag DV Details now have their own
  Run wizards.** Click "Run Tag Audio" (or Video / DV Details)
  on the sub-tab and a wizard opens with instance + run-mode +
  per-bucket allow-list + Review steps. Same shape as Quick fix-all
  but locked to that one action. Settings configured on the
  sub-tab page still act as defaults the wizard pre-fills from.

- **Run Recover** + **Find unused tags** likewise open small
  wizards — pick instance + (preview/apply for Recover) + Run.
  Replaces the "header instance picker + click button" flow which
  could disagree with what the wizard would show.

- **Lossless / lossy audio label is honest now.** Before: a movie
  with `DDP5.1` audio that passed the Quality filter (with Audio
  filter off) showed up labelled "Lossless audio" — wrong. Now:
  it shows "EAC3/DD+ (lossy)", "AAC (lossy)", "AC3 (lossy)", or
  "No lossless audio" depending on what's actually in the file.
  Quality column similarly: rows that pass with no MA/Play prefix
  read "AMZN (not MA/Play)" / "Netflix (not MA/Play)" / "Plain
  WEB-DL (no MA/Play prefix)" / "No WEB-DL source" instead of
  the placeholder "Unknown".

- **Header "Instance" dropdown removed.** Every wizard has its own
  instance picker on Step 1 — single source of truth, no more
  header-says-X but wizard-shows-Y confusion. App-type pill
  (Sonarr / Radarr) stays since it controls page-level visibility.

- **Per-action wizards remember last instance.** Tag Audio / Video /
  DV Details / Recover wizards remember which instance you fired
  last time and pre-select it. Cross-tab memory via localStorage.
  Other wizards (Tag Release Groups, Discover, QFA, Webhook)
  still seed from current state — extending memory to those is
  parked for next session.

### Bug fixes

- Webhook receiver no longer poisons its on-disk log when an
  unparseable event arrives (json.RawMessage validation would
  fail every subsequent persist; now wraps invalid bytes into a
  valid JSON envelope before storing).
- `handleRenameTag` lowercases + per-app-type validates the new
  label server-side. Frontend already sanitises keystrokes; this
  is defence-in-depth so a curl client can't trip the case-
  sensitive FindByLabel race in upstream Arr code.
- AAC2.0 / DDP5.1 / EAC3.5.1 channel-suffixed lossy variants now
  match the lossy-detection regex (was missing the trailing-digit
  forms — bash inherits the same gap but never surfaced it
  because bash always runs with audio filter enabled).
- Several smaller traps fixed during a backend + frontend code
  review: ring-buffer backing array now reallocates on FIFO
  eviction (was leaking dead WebhookEvents over long uptimes),
  list() deep-copies Raw bytes (defence against future code
  mutating in place), constant-time token compare on webhook
  receive, rotate-without-body preserves loggingEnabled, all
  webhookEvents/webhookConfigs writes use spread-assign (Alpine
  v3 reactivity trap on object-key add).

### Coming next

- **Wizard-everywhere finalisation.** Decide where bucket configs
  live — sub-tab page (current), wizard with save-to-globals on
  fire, or wizard with localStorage memory. Three documented
  paths in `dev/analysis/wizard-everywhere-followup.md`.
- **qBit auto-tagging via Sonarr Grab webhook + backlog scan.**
  Two services on the M-Webhook foundation: realtime tag-on-grab
  and one-off backlog catch-up.
- **Apply now button on standalone Audio / Video / DV result
  panels** with frozen preview-time settings.

---

## v0.3.8-dev — Run Discover gets its own wizard (2026-05-06)

### What you get

- **Run Discover now opens a short wizard** instead of firing
  immediately. Three steps:
  1. **Choices** — pick run mode (Preview vs Apply), the
     add-behavior for new groups (enabled or disabled), and
     whether to include groups already on your Active list
     (audit mode).
  2. **Filter** — same Quality + Audio toggles as the Tag wizard,
     with the same one-must-be-on gate. Saves on each click.
  3. **Review** — confirm and run.

- **Preview vs Apply** for Discover, mirroring the Tag wizard:
  - **Preview** (default) — show every candidate in the result
    modal so you tick which to add. Same flow you had before.
  - **Apply** — auto-add every candidate with the chosen
    add-behavior. No manual review. Use when you trust your
    filter and just want every match in.

The audit-mode toggle that used to sit on the actions card moved
into the wizard's Choices step — tidies up the page and keeps
all Discover options in one place.

### Why this changed

Filters sub-tab was dropped in v0.3.7-dev (folded into the Tag
wizard). That left the standalone Run Discover button with no
visible filter UI on the Tag Release Groups page — clicking it
would silently use whatever filter state happened to be saved.
The wizard fixes that by walking you through filter selection
before the scan fires.

---

## v0.3.7-dev — Library scan restructure + wizards on Step 1 (2026-05-05)

### What you get

- **Library scan is simpler.** Tag library + Release Groups + Recover
  folded into one **Tag Release Groups** sub-tab. The standalone Tag
  library card is replaced by a guided 4-step **Tag release groups**
  wizard (Choices → Filter → Active groups → Review) that walks you
  through the picks. The Filters sub-tab is gone — filter config lives
  inside the wizard now, where it matters. Audio / Video / DV detail
  sub-tabs are renamed **Tag Audio** / **Tag Video** /
  **Tag DV Details** for naming consistency.

- **Pick the instance inside the wizard.** Click "Tag release groups"
  without pre-selecting an instance — the wizard's first step has
  the picker. Single-instance setups skip it (no choice to make).
  Matches how Quick fix-all already worked.

- **Run mode is on Step 1 in both wizards now, defaulting to
  Preview.** Pick Preview / Apply up-front, then configure the
  rest — no more digging for the run-mode radio after building
  the chain. Promote a preview to apply via the result panel's
  Apply now button.

- **Tag inventory — Select unused.** New button picks every tag
  with 0 movies/series AND no references in Lists, Custom Formats,
  Notifications, etc. Pairs with Delete selected so you can sweep
  orphans without hitting the per-tag block banner.

- **Tag inventory — pre-delete reference check.** Deleting a tag
  used by a List or Custom Format now warns you BEFORE you submit
  ("Used by 2 Lists, 1 Custom Format on Radarr") instead of returning
  a cryptic Arr error after the fact.

- **Tag rename — per-app-type input rules.** Characters that the
  target Arr won't accept get blocked as you type. Radarr is strict
  (a-z, 0-9, hyphens only — uppercase auto-lowercased); Sonarr
  accepts spaces / unicode / punctuation but lowercases on save.
  No more silent rename failures from invalid characters.

- **Result panel filter chips look like filters now.** The
  Add / Remove / Keep buttons on the Tag / Audio / Video / DV
  result panels were styled like action buttons but actually filter
  the result list — pill-shaped now with a "Show movies where tag
  will be:" prefix and ± / = symbols, so the function is obvious
  at a glance.

- **Recover (standalone) honest summary.** No more misleading
  "419 movies needing recovery" when most of them are actually
  unfixable; the summary now reads "Found X with empty or
  Unknown release group" plus a breakdown of how many of those
  are recoverable, flagged, or unfixable.

### Bug fixes

- Apply-mode Run scan no longer silently bails when there's no
  prior preview — Apply runs against a fresh scan as it should.
- Quick fix-all "no active groups" toast no longer fires when
  Discover is enabled in the chain (Discover seeds the active list
  at runtime, the gate now respects that).
- Legacy testers persisting old sub-tab names (`tag` / `recover` /
  `filters`) migrate forward to the new "Tag Release Groups" tab
  instead of landing on a blank page after Force Update.
- Tag rename now sends pre-lowercased to the Arr (matches what
  Radarr/Sonarr store anyway and sidesteps a dedup race in
  upstream code).
- Several smaller fixes from a three-agent code review pass —
  Alpine reactivity traps, wizard step deadlock when source
  flipped, mixed-payload guards in the Recover-exclude API.

### Coming next

- **Apply now button on standalone Audio / Video / DV result
  panels** (planned next batch). After a Preview scan you'll be
  able to commit the changes with one click using the exact
  settings that produced the preview.
- **Import / webhook integration.** Bringing in the `tagarr_import`
  flow so resolvarr can tag files automatically as they land in
  Radarr/Sonarr, not just on library scan.

---

## v0.3.6-dev — Sonarr support (2026-05-04)

### What you get

- **Sonarr is now supported.** Recover, Audio tags and Video tags all
  work the same way as Radarr — Library scan, Quick fix-all, schedules,
  the whole flow. Tags land on the series itself (Sonarr's tag model
  only allows series-level tags, not per episode); the result panel
  expands each series into its seasons + per-episode mediaInfo so you
  can see exactly which episodes contributed which tags.

- **Show excluded** lets you skip movies / series / seasons you don't
  want resolvarr to keep checking on Recover scans. Click Exclude on
  any result row, and they'll be filtered out of every future scan
  until you click Include again from the Show excluded chip.

- **Plain-language help across the app.** Help panels rewritten to
  explain features in everyday language — fewer technical terms,
  more "what this does and why you'd want it". Sonarr-aware where
  the mechanics differ from Radarr.

- **Footer** showing the running version + project credit, matching
  the rest of the container family.

### Bug fixes

Various bug fixes and stability improvements.

### Coming next

- **Import / webhook integration.** Bringing in the `tagarr_import`
  flow so resolvarr can tag files automatically as they land in
  Radarr/Sonarr, not just on library scan.

---

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
