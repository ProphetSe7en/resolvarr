# Changelog

> ⚠️ **`:dev` is a moving target.** Between dev builds, some changes are not always backwards-compatible with previous versions — your existing rules get auto-converted on first start, but the shape and the controls in the wizard can change. If you're running `:dev`, plan for the occasional adjustment. The first stable `:latest` will be locked down with normal upgrade discipline.

## v0.6.54-dev — Recover no longer attaches a replaced release's group (2026-06-17)

### Fixed

- **Recover (Library scan) no longer suggests a release group from an old, replaced download.** When a movie file was imported manually or put in place outside Radarr, Recover could attach the release group of a previous release that had already been deleted and replaced. It now recovers the group only from the download that actually produced the file currently on disk, and reports "could not verify" when there is nothing reliable to recover from. Radarr movies only in this release; the same fix for Sonarr is coming.

## v0.6.53-dev — Grab-rename "Bad naming" cleanup + correct release group from foreign names (2026-06-16)

### Added

- **"Bad naming" cleanup for qBittorrent grab-rename rules.** Turn it on and the rule fixes release names Radarr mis-reads before import: a non-Latin bracket in front of the title, the same year twice in a row, and a leftover file extension in the torrent name. The notification shows what was cleaned.
- **Grab-rename now recognises the iTunes (iT) source** and preserves it when the torrent name is missing it.

### Fixed

- **Release group no longer ends up as foreign text.** When a release name leads with non-Latin characters, Radarr can read those as the release group and drop the real one. Recover and grab-rename now take the real group from the name instead.

## v0.6.52-dev — Open scheduled runs from History, see what Plex sync skipped (2026-06-14)

### Added

- **Plex label sync now shows what it could not match, and why.** A sync reports a count of items it skipped because they could not be paired with a Sonarr or Radarr entry. Now the run details list each skipped item (title, where it is, and the reason, for example "no shared ID, title, or path"), and the notification names the first few. So "2 unmatched" is no longer a mystery. This is captured on new runs from now on.

### Fixed

- **Clicking a scheduled run in History now opens its result.** Rows that came from a schedule were not opening when clicked, so you could see that a run happened but not what it did. They now open the full per-phase result, the same as a manual scan.

## v0.6.51-dev — Scheduled runs in History, Plex sync notifications, tagging fixes (2026-06-14)

### Added

- **Scheduled rule runs now show up in History.** Open Tag Library, then History, and runs that fired on a schedule appear in the same list as scans you start by hand, marked "Scheduled" with the rule name and result. Click any of them to open the full result, exactly like a manual scan. Before this, a scheduled run only logged to its own rule card, so the History list looked empty even when your rules were running on schedule.
- **Plex label sync now sends a notification.** When a sync adds or removes Plex labels, you get a notification with a per-label count (for example "FEL: +60, 33 in sync"), in the same style as your other notifications. This works both for syncs that run on a schedule and syncs triggered by a webhook.

### Fixed

- **Selecting every value in a rule or scan no longer wipes your tags.** Picking all resolutions (or all audio formats, all codecs, and so on) used to be read as "tag nothing", which, with "Remove orphaned tags" on, removed every tag in that group. Selecting all now correctly means "tag all". Saved rules left in this state are repaired automatically on the next run.
- **Resolution tags now follow the resolution your Arr reports.** Files like 1440x1080 were sometimes tagged 720p. Resolution now comes from the resolution Sonarr or Radarr already detected from the file, so unusual or anamorphic sizes get the right tag.
- **Turning a tag group off now leaves its tags alone.** With "Remove orphaned tags" on, switching a group off (for example Codec) used to strip all of that group's tags. A group that is off is now left untouched. To clear a group on purpose, keep it on and select no values.

## v0.6.50-dev — qBit tag notifications and app-coloured notifications (2026-06-12)

### Added

- **Tagging a torrent Season or Episode in qBittorrent now sends a notification.** When resolvarr applies the Season-pack or single-Episode tag to a torrent in qBittorrent, you get a notification (Discord, Gotify, and the rest) in the same style as your other resolvarr notifications. This covers torrents added straight to qBittorrent, such as cross-seed, as well as normal grabs. It uses your notification agent's "On grab" event, so turn that on for the agent if you want these.

### Changed

- **Notifications are now coloured by app.** Sonarr notifications use Sonarr blue and Radarr notifications use Radarr gold, so you can tell at a glance which app a notification is about. Delete events stay red.

## v0.6.49-dev — Base image and dependency refresh (2026-06-09)

### Changed

- Updated the container base system, the bundled ffmpeg, and the build toolchain to current versions, so the image ships with up-to-date security patches. Release-group tagging and Dolby Vision detection work exactly as before.

## v0.6.48-dev — Plex label sync tags every selected library (2026-06-08)

**Fixed: a webhook rule now applies the label to every selected Plex library, not just the first.** If a rule synced to more than one Plex library, only the first library that held the item got the label and the rest were skipped. Now each selected library that holds the item is tagged.

**The result and notification name the libraries.** The sync result rows and the webhook notification now show which Plex library each label landed in, so you can confirm every selected library was tagged.

## v0.6.47-dev — Webhook rule remembers its Plex instance + app icon (2026-06-07)

**Fixed: re-opening a webhook rule now shows the Plex instance you picked for Plex label sync.** Your choice was always saved, but the instance picker came up blank on re-edit, which made it look lost (and re-saving from the blank picker could drop it). It now shows the saved instance, the same as the other instance pickers.

**Added: an app icon.** The container now ships a resolvarr icon, so Unraid (and anything that reads the container image) shows the logo instead of a blank square.

## v0.6.46-dev — Clearer DV detail note + Plex match hardening (2026-06-06)

**Webhook rules: removed a confusing note about Tag DV Details and Tag Video.** The old note implied you had to enable Tag Video to use Tag DV Details. You don't. Tag DV Details is its own function and tags the Dolby Vision profile, layer and CM version on its own. Tag Video is separate and only adds the plain `dv` tag, so enable it as well only if you also want that base tag.

**Plex label sync: the folder-name fallback no longer guesses on duplicates.** When a match falls back to the on-disk folder name, two library items that happen to share the same folder name are now left unmatched instead of risking the wrong one getting the label. Items matched by database ID, by an ID in the folder name, or by the full folder path are unaffected.

## v0.6.45-dev — Plex label sync matches reliably + per-Arr qBittorrent default (2026-06-06)

**Plex label sync now matches your shows and movies far more reliably.** It reads the database IDs (TVDB, TMDB, IMDb) directly from Plex and matches on those, with the folder on disk as a final fallback. Before, it leaned on the title and year, so an item whose Plex name or year differed from Radarr/Sonarr could be missed. Now a show or movie still gets its label even when Plex and Radarr/Sonarr hold different IDs, only one of them, or none in common. That last case is common for foreign or obscure titles, where Plex matched with the TMDB agent and Sonarr is TVDB-based, or where Plex and your library mount the same files at different paths. If a synced tag's count looked lower than expected before, it should line up now.

**Lock a default qBittorrent instance to each Radarr/Sonarr.** In an instance's settings you can pick the qBittorrent it normally uses. The tools that need one (Stuck downloads, the qBittorrent category and season/episode tools) then pre-select it for that instance, so you stop choosing the same one every time.

**Startup log on disk for troubleshooting.** Resolvarr now writes its startup and key events to /config/logs/resolvarr.log. If the container ever fails to start or misbehaves, that file holds the details even when the container's own log comes back empty, which makes a problem far easier to pin down.

## v0.6.44-dev — Find and clear stuck downloads (2026-06-06)

**New "Stuck downloads" tool (Library scan, Radarr + Sonarr).** Finds downloads that finished but never imported, usually because a better release of the same thing imported first and left the first one sitting in the queue. For each it shows the score it had at grab, the score it gets at import, and the score of the file you already have, plus the plain-language reason it's stuck.

Downloads that are redundant (you already have an equal or better file) can be moved to another qBittorrent category in one click, so your own cleanup handles them. Ones that still need attention (nothing imported yet, or the stuck one is actually better) are listed separately and left alone; you can still move them if you've decided they'll never import. Nothing is ever deleted. Pick the qBittorrent instance and target category once in the Run dialog; the category is pre-filled from your download client.

## v0.6.43-dev — Sonarr wording fixes (2026-06-06)

**Fixed: two spots said "Radarr" on a Sonarr instance.** The Recover result panel's "Trigger rename after fix" checkbox and the Preview-vs-Apply help text both named Radarr regardless of the active instance. They now read correctly for Sonarr.

## v0.6.42-dev — Grab Rename for season packs + two Sonarr Recover fixes (2026-06-06)

**Grab Rename can now rename the files inside a season pack (Sonarr).** Sonarr scores a season pack per file at import, by each file's own name, so renaming only the torrent name didn't help when the files inside were scene-named (for example `web` instead of `WEB-DL`, or missing `NF`) and the import scored low or got stuck. A new "Rename files inside the torrent (needed for season packs)" option on the Sonarr Grab Rename step renames each episode file to match the release title, so each file scores the same at import as it did at grab. "Torrent name only" stays the default and is fine for single-episode grabs.

**Fixed: Recover's Exclude now takes effect right away.** Excluding a series, season, or movie in a Recover result did save the exclusion for the next scan, but the row stayed on screen and still counted toward Apply, so it looked like nothing happened. Excluded items now drop out of the result immediately (into "Show excluded") and are left out of Apply.

**Fixed: Recover no longer puts the wrong release group on a manually-imported episode (Sonarr).** If you replaced an episode with your own file via Sonarr's Manual Import, Recover could attribute the previous download's release group to the new file. It now recognises a manually-imported file (no download attached) and reports it as "failed verify" instead of guessing a group. Normal downloads and season packs are unaffected.

## v0.6.41-dev — Recover handles Sonarr season packs (2026-06-05)

**Fixed: Recover wrongly reported "failed verify" on Sonarr season packs.** When a whole season was downloaded as one pack, every episode after the first showed "failed verify" and never got its release group restored, even though the group was sitting right there in the download history. Recover now follows the pack's grab and import across the whole series, so all episodes in the pack recover their release group. Same behaviour in a one-off scan, a Quick fix-all, and a scheduled run. Webhook recover was already correct. Files with no grab or import in history (nothing to recover from) still report honestly and are left untouched.

**Clearer wording on Sonarr.** A few labels and messages were written for Radarr and showed the wrong word on a Sonarr instance, for example a Quick fix-all review that said "nothing is written to Radarr" and a recover summary that counted "movies". They now say Sonarr, series, or episode files as appropriate.

**Large Sonarr scans no longer time out as easily.** Recover and Audio/Video tagging walk every series, so on a big or error-heavy library a run could hit a "context deadline exceeded" error partway through. The time budget is raised from 60 to 180 seconds so those runs finish.

## v0.6.40-dev — qBit Category Fix no longer slows imports (2026-06-04)

**qBit Category Fix no longer holds up Sonarr/Radarr imports.** It used to run inline while Sonarr/Radarr waited for resolvarr to answer the import notification, and on a season pack (one event per episode) those waits stacked up and made imports crawl. It now runs in the background a short while after import (default 30 seconds, tunable 5 to 600 on the qBit Category Fix step), so the import notification returns instantly. The delay also gives the Arr time to do its own category swap first, so the fix usually finds nothing left to do. It still records its result in History and sends its notification when it finishes.

## v0.6.39-dev — qBit S/E tags sub-tab is Sonarr-only (2026-06-04)

**Fixed: the qBit S/E tags sub-tab showed under Library scan for Radarr.** qBit Season/Episode tagging is Sonarr-only (Radarr has no per-episode model), but the new sub-tab from v0.6.38-dev appeared on Radarr too. It now shows only when Library scan is set to a Sonarr instance. Quick fix-all and Schedule were already correct.

## v0.6.38-dev — qBit S/E tagging beyond webhooks: one-off, scheduled, and Quick fix-all (2026-06-04)

**qBit Season/Episode tagging now works everywhere, not just on webhooks.** Tagging your qBittorrent torrents by what the release looks like (an episode, a season pack, or neither) used to be a webhook-only function plus a backlog button tucked away on the webhook rule card. It is now a first-class action you can run three more ways (Sonarr only):

- **One-off sweep.** Library scan has a new **qBit S/E tags** sub-tab: pick your qBittorrent instance, choose which torrents get an Episode / Season / Unmatched tag, optionally limit to a category, then Preview and Apply. Great for tagging an existing backlog in one pass.
- **On a schedule.** Add qBit S/E tagging to a scheduled rule so it sweeps the backlog on a cron, for example nightly.
- **In Quick fix-all.** Add it as a step in a combined Quick fix-all run alongside your other tagging phases.

All four paths (webhook, one-off, schedule, Quick fix-all) make the exact same tag decision for the same release name, so behaviour is consistent wherever you run it. The webhook function and the webhook rule card's Backlog scan button are unchanged.

## v0.6.37-dev — Pause/resume fix, clearer Resume icon, better season detection (2026-06-04)

**Fixed: pausing a rule failed for some webhook rules.** Pausing or resuming a webhook rule that used qBit Category Fix or Plex label sync returned a "criteria required" error and didn't toggle. It now pauses and resumes cleanly. Affects Radarr and Sonarr rules alike.

**Clearer Resume icon.** The pause/resume button's Resume state used the same filled play triangle as the Run-now and Backlog-scan buttons, so a paused rule showed two near-identical icons side by side. Resume is now a play-in-circle, distinct from the "run now" triangle, on both scheduled-run and webhook rule cards.

**Better season vs episode detection in qBit S/E tagging.** Releases that carry a year alongside season tokens (common on non-English season packs) could be mis-tagged as an episode by the daily-show date check. Season packs like these are now correctly tagged as Season, while genuine daily-show dates still tag as Episode.

## v0.6.36-dev — Apply matches preview, Review tab, resolution and memory fixes (2026-06-04)

This build brings back the work from the v0.6.17 to v0.6.20 builds, one piece at a time on the clean baseline, including the fixes promised in v0.6.21-dev, plus a couple of editor improvements.

**Apply now does exactly what the preview showed.** When you run a preview and then click Apply on the result, resolvarr applies it using the same settings the preview ran with. Before, Apply could fall back to your global settings, so a preview that found, say, 128 tags to add could apply 0. Preview and Apply now always match. This covers Quick fix-all, scheduled runs, the per-action Audio / Video / DV applies, and Plex label sync.

**Review tab when editing a rule.** Editing a webhook or scheduled rule now has a Review tab, the same at-a-glance summary of everything the rule does that you get on the last step of the create wizard. Jump to any tab to change a setting, then Save.

**Clearer notifications.** Tag notifications now use plain headers, Audio and Video instead of Sound and Picture, and Dolby Vision detail values are spelled out (Profile 8, MEL, FEL, CM v2.0, CM v4.0) instead of raw codes.

**HDR releases also get a `10bit` tag.** When a file is HDR (HDR10, HDR10+, Dolby Vision, HLG or PQ), resolvarr now also tags it `10bit`, even when the bit-depth field isn't filled in.

**Fixed: letterboxed 4K tagged one tier too low.** A 2.40:1 cinematic release reports a cropped height (a 4K cut can read as 1600 lines), which made resolvarr tag it 1080p or even 720p instead of 2160p. It now reads the width and tags the correct tier.

**Fixed: webhook rule editor forgetting your choices.** A webhook rule now remembers your "Source of release groups" pick (Active groups / Discover / filter only) and your "add new Discover groups and enable them" choice when you reopen it to edit.

**Scheduled-run results are fully clickable.** In a scheduled or Quick fix-all result, the Plex sync, missing-episodes and TBA-refresh phases now have a View details drill-in like the tagging phases, instead of just a summary line.

Also: the option to delete unused tags has been removed from the webhook rule editor. It is a library-wide action that doesn't fit a per-event rule, so use it from Quick fix-all or a Schedule instead.

## v0.6.21-dev — Reset to v0.6.16-dev baseline (2026-06-03)

This `:dev` build rolls the code back to v0.6.16-dev. The v0.6.17 to v0.6.20-dev builds had inconsistencies in audio and video tag emission, so the cleanest path forward is to reset to the last stable dev build and reintroduce changes one at a time. Force Update if you were on v0.6.17, v0.6.18, v0.6.19 or v0.6.20-dev.

Coming back in upcoming dev releases:

- Cinematic-crop fix (2.40:1 releases tagged one tier below the canonical resolution, e.g. Hokum 1080p ending up as 720p)
- Webhook-rule toggles for "Use Discover for new groups" and "Delete release-group tags that no longer match" persisting across edit
- Notification label polish (Sound to Audio, Picture to Video, Dolby Vision detail values humanised)

## v0.6.16-dev — TBA refresh, Activity search, Plex runs in History (2026-05-29)

**New: TBA refresh (Sonarr).** When Sonarr imports an episode right at release, the title is often still "TBA" and gets baked into the filename. Later, once the real title is known, Sonarr updates the episode but leaves the file named "TBA" on disk. The new **TBA refresh** tool (Library scan, Sonarr only) finds those files and triggers Sonarr to rename them to the real title.

- **Run it from the page.** Library scan, TBA refresh: pick which series to include (continuing / ended / specials), click Find TBA files. You get a list grouped by series and season showing the current name and the name Sonarr would rename it to, with per-file checkboxes. Rename the ones you pick.
- **Put it on a schedule or Quick fix-all.** Add a TBA refresh step to a Schedule (great as a nightly job) or to Quick fix-all. On a scheduled / Quick-fix run it renames every TBA file it finds.

**Each wizard phase now has its own page.** Missing episodes and TBA refresh used to tuck their settings onto the Review step. They now get their own page in the Create rule / Quick fix-all wizard, just like Tag Audio / Tag Video / DV detail, and the Review step shows a clean read-only summary of each.

**Find things in Recent Activity.** The Recent activity tab now has a search box. Type a movie or series title to filter the event list to matching items.

**Plex label sync runs show up in History.** A one-off Plex label sync run now appears in the History tab alongside your other scans. Click it to re-open the result (counts + per-label table).

## v0.6.15-dev — internal cleanup (2026-05-28)

Housekeeping only, no user-facing changes. Removed the unused standalone Plex-label-rule storage and its API endpoints, now that Plex label sync is configured directly on each run, schedule, or webhook (v0.6.14-dev).

## v0.6.14-dev — Plex label sync (2026-05-28)

New feature: sync your Radarr and Sonarr tags onto matching Plex movies and series, as Plex **labels** or **collections**. Pick which tags travel, and resolvarr keeps Plex in step with your Arr tags. Inspired by [DAPS' `labelarr` module](https://github.com/Drazzilb08/daps/blob/dev/modules/labelarr.py), rebuilt here in Go and wired into the ways you already run things in resolvarr.

**What it does:**

- **Pick the tags from your real tag list.** Choose your Radarr or Sonarr instance and resolvarr fetches the tags you actually have, so there's no guessing the spelling. Tick the ones you want on Plex.
- **Labels, collections, or both.** Labels are light, filterable tags in Plex. Collections are grouped views in Plex Web (what Kometa users tend to use). Choose either or both.
- **Pretty names on Plex.** Radarr forces tag names to lowercase-with-dashes (`atmos`, `dolby-vision`). Next to each tag there's a "Plex label" field, so you can show `Atmos` or `Dolby Vision` on Plex while keeping the Arr tag as-is.
- **Your manual Plex labels are safe.** Only the tags you pick are managed. Anything you've labelled in Plex yourself stays put.
- **Tolerant matching.** Movies and series are matched by TMDB, TVDB and IMDB IDs, falling back to title and year, so the right item gets tagged.

**Four ways to run it:**

- **Once, on demand.** Library scan, Plex label sync: pick your Plex server, libraries and tags, then run a preview (shows what would change, touches nothing) or apply.
- **On a schedule.** Add a "Sync to Plex" step to a Schedule and Plex stays in step on a timer, including tags you change by hand.
- **In Quick fix-all.** Tick "Sync to Plex" and it runs last, after the tagging, so Plex reflects the final tags.
- **Live on import.** Turn on "Sync to Plex" in a webhook rule and Plex updates within seconds of a download, no waiting for a schedule.

Notifications cover it too: Discord, Gotify, NTFY, Pushover and Apprise show a "Synced to Plex" line with per-label counts.

**Better tag search.** In every tag picker you can now type several names separated by spaces (for example `fel mel`) and see them all at once, instead of searching one at a time.

## v0.6.10-dev — Instance picker bug fix + dead code removal (2026-05-25)

### Fixed

- **Instance dropdowns now actually use the instance shown in the picker.** A render-ordering bug let dropdowns visually display the correct instance while the underlying state held a different one — pressing Run fired the scan against the wrong instance until you manually re-selected from the dropdown. Affected page-level Library scan + Recover instance pickers, the Tag inventory picker, the Compare-across-instances picker, the Recent Activity instance picker, the Webhook setup wizard, the rule-editor sync-target picker, and the per-action wizard instance selector (Run Tag Audio / Video / DV Details / Recover).

### Internal cleanup

- Removed unused legacy code that handled runtime downloads of `dovi_tool` + `ffmpeg`. Those binaries have shipped baked into the image since `v0.3.5` — the old download path was unreachable but still in the codebase. Pruned along with its tests (-750 lines). DV detail tagging continues to work exactly as before.

- Internal-documentation cleanup pass across source comments. No functional change.

## v0.6.9-dev — Security baseline + defensive infrastructure (2026-05-25)

A focused security pass — most of this is invisible from the UI but matters when something goes wrong. Safe upgrade from `v0.6.8-dev`; no config changes required.

### What you get

- **Credential masking on rule-listing endpoint.** Per-rule webhook Token + Secret now masked on `GET /api/webhook-rules`. Plain values still reachable via the dedicated `/webhook` endpoint where you copy them into Sonarr/Radarr.

- **Brute-force protection on login.** Rate-limit hardened against bcrypt-DoS via an explicit 72-byte input cap before the password compare.

- **Passphrase-friendly password rule.** Passwords 16+ characters skip the class-diversity requirement, so `correcthorsebatterystaple` is accepted; under 16 still needs 2 of {upper, lower, digit, symbol}.

- **CI gains automated security scans.** `go test -race`, `govulncheck`, and `gosec` now run on every push and PR.

- **Dependabot.** Weekly PRs for Go modules, GitHub Actions, and Dockerfile base-image updates.

- **Alpine.js CDN integrity.** Pinned to `3.15.12` with a SHA-384 SRI hash — the browser refuses to execute a tampered bundle.

## v0.6.8-dev — Internal comment cleanup (2026-05-25)

Internal-only housekeeping pass on code comments. No functional change vs `v0.6.7-dev` — your config, notifications, and webhooks behave exactly the same. Safe upgrade.

## v0.6.7-dev — Notification layout redesign + TRUSTED_PROXIES accepts CIDR (2026-05-25)

### Discord notifications now show every detail, labelled

Notifications used to be sparse — you saw the movie title and the tag values, but not where the change landed, what triggered it, or which file was imported. Now every embed has a proper structure:

- **Tagged in** — which Arr instance handled the change (and the mirror instance when sync-to-secondary fired). Shows up on every event, not just Tag-RG fires.
- **Event** — Grab / Import / File deleted / Episode deleted / Upgraded.
- **Filename** — the actual imported file (`The.Substance.2024.2160p.WEB-DL.DV.HDR-FLUX.mkv`).
- **Rule** — which webhook rule produced this notification. Moved out of the footer into its own row.
- **Timestamp** in the embed corner — Discord renders it locale-aware ("Today at 14:32" / "Yesterday at 09:15" / dated for older).

**Grab Rename gets clearer wording:** Renamed in → Release Group Recovered → Tokens Recovered (Director's Cut / IMAX / TrueHD restored by the rename) → Torrent Name (before) → Restored to Release Name (after). The two filenames stack vertically with the same left edge so you can scan up/down to compare.

**File-delete events** say "Cleaned in" instead of "Tagged in" so the wording stays accurate (tags were removed, not added).

Footer is now just `Resolvarr {version} by ProphetSe7en` + the timestamp on the right.

### TRUSTED_PROXIES now accepts CIDR ranges

Set `TRUSTED_PROXIES=172.19.0.0/24` to trust any container on a Docker bridge — useful when your reverse proxy (Traefik / Caddy / nginx-proxy-manager / SWAG) sits on a bridge with dynamic container IPs. Literal IPs still work the same.

### Why this matters

- **Notifications** — earlier embeds were missing the "what file / where / what triggered it" context, and grab-rename events even rendered without any detail fields. Now matches what the bash `tagarr_import.sh` script always had.
- **TRUSTED_PROXIES** — `TRUSTED_NETWORKS` already accepted CIDR, but `TRUSTED_PROXIES` rejected it with `invalid entry in trusted_proxies: "172.19.0.0/24"`. Inconsistent, and forced you to list every reverse-proxy container IP individually (which changes on every Docker recreate).

## v0.6.6-dev — Activity Table redesign + webhook secret management (2026-05-17)

### What you get

- **Recent Activity is a readable table now.** No more JSON dumps. Five columns: Time / Type / Item (title + subtitle + quality summary) / Outcome / Expand. Each row shows you what happened in plain language — series + episode name, quality + audio + codec + release group + file size, and which rule did what. Click to expand for the full card view with file path, scene name, source trail, and per-rule outcome details. Raw JSON is still available, just hidden behind a collapsible button so it doesn't drown the rest.

- **Three filter dimensions on Recent Activity.**
  - **Type** (chip row): All / Grab / Import / Delete / etc.
  - **Outcome**: All / Made changes / No change / No rule matched / Errors only
  - **Content shape**: 🎬 Episode / 📚 Season pack / 🎞 Movie / ⓘ System — app-type-aware (Radarr shows Movie + System, Sonarr shows Episode + Season pack + System)
  - Counter: "N of M events" so you can see how aggressive your filtering is

- **Webhook setup gets a "Show details" button.** Reveals the URL + Secret on the existing instance card without needing to regenerate the token. Secret is masked by default (`abcd••••••••wxyz` — first 4 + last 4 chars) so you can confirm a paste matches without exposing the whole value. Includes a 7-step plain-language setup checklist for the Sonarr/Radarr Connect form.

- **Generate / Rotate Secret button** on the setup card. If your instance has no Secret yet (legacy migration), click **Generate** → fresh Secret without invalidating the URL Sonarr/Radarr already has. If you want to rotate after a suspected leak, click **Rotate** → same flow with a confirmation warning that you'll need to re-paste into Sonarr/Radarr's Password field.

- **Auto-pick the instance picker when only one exists for the active app-type.** No more clicking through "— pick an instance —" on every visit to the Recent activity tab if you only have one Sonarr or one Radarr.

### Bug fixes

- **Season-pack Grab events now read as "S07 (22 episodes)" instead of the misleading "S07E01 + 21 more".** Same-season detection: when every episode in `episodes[]` shares one season number, it's a pack — use the season form. Mixed-season (rare) keeps the old fallback.

- **Per-episode Import events no longer misclassified as season packs.** Sonarr quirk: per-file Imports list ALL of the pack's 22 episodes in `episodes[]` even though only one file is being imported. The Content-shape filter now checks `episodeFiles` count first — exactly one file = single Episode, regardless of what the episodes metadata says.

- **Per-episode Import subtitle parses the file's actual episode.** Was "S07E01 + 21 more" on every per-episode import within a pack; now reads "S07E01" by parsing the SnnEmm token from `episodeFile.sceneName` / `relativePath`. Falls back to the season-pack form for genuine multi-episode events with no files yet.

- **Content-shape filter chips now only show shapes that match the app-type.** Was showing Episode + Season pack while on Radarr (Sonarr-only concepts) and Movie while on Sonarr.

- **Auto-pick honours the active app-type.** Was picking the first instance globally instead of the first instance of the selected Radarr/Sonarr pill, which could land you on the wrong type.

## v0.6.5-dev — Notifications + custom tag labels + qBit URL override (2026-05-16)

### What you get

- **Discord / Gotify / NTFY / Pushover / Apprise notifications when a rule fires.** Add a notification agent under **Settings → Notifications**, pick which event-classes it cares about (On Grab / On Import / On File Delete + schedule events) and which actions show up in the embed (Tag release group, Auto-tag audio, qBit S/E tag, etc.) — each agent only renders what you ticked. Then turn on **Notify on fire** on the rule itself. You can have one Discord channel for tag changes, another for torrent renames, etc.

- **Custom tag labels per bucket.** Don't like `dvprofile8`? Click "Customise labels" on the Audio / Video / DV step in any rule and rename it to `profile8`, or rename `2160p` → `uhd`, or `truehd` → `premium`. Cleanup follows your current labels — tags with the old name stay in Radarr until you remove them manually in Tag inventory.

- **qBit webhook URL override.** When qBit and resolvarr aren't on the same Docker network (typical when qBit runs in an isolated network), qBit's curl back to resolvarr fails silently. The qBit webhook modal now has a **Resolvarr URL** input — set it to your LAN IP or the proxynet container IP (e.g. `http://172.19.0.35:6075`) and Configure writes that into qBit's autorun. Curl preview updates live so you see exactly what gets written.

- **qBit webhook list-row state badge.** The Webhooks page now shows a green **configured** badge next to qBit instances where the webhook is already set up, and the button reads **Edit webhook** instead of generic "Webhook". Instances without setup show **Set up webhook**.

- **Tags now land in qBit within ~300ms of the add, not after the aggregation window.** Previously the per-rule aggregation window gated both tag-apply AND history-write, so users with the default 60s window saw a long lag before their tag appeared. Tags now apply immediately on receive; the window only batches history-rows + notifications.

### Bug fixes

- **qBit autorun calls were rejected with HTTP 403.** Cross-seed catch-up never worked because resolvarr's CSRF + auth middlewares didn't exempt `/api/qbit/torrent-added/` (Sonarr/Radarr Connect's path was already exempt). qBit's curl, despite a valid per-instance secret, got 403 every time. Now exempt — same server-to-server auth model as the Sonarr/Radarr Connect path. If you saw cross-seed adds in qBit's log but no tags applied, this was it.

- **Schedule and rule overlays now validate auto-tag snapshots.** Previously a buggy UI or hand-crafted payload could persist invalid auto-tag config via the per-schedule / per-rule path, bypassing the validators used by the global PUT handlers. The new custom-labels feature made this freshly exploitable, so both overlay paths now run the same checks.

- **File-delete mirror-only case no longer goes silent.** When a rule with Strip-on-delete had nothing to strip on the primary instance but the secondary mirror did, the delete event produced no history embed. Now it correctly surfaces the mirror cleanup.

### Quality-of-life

- **qBit-add receive is logged.** Every webhook call from qBit to resolvarr writes one line to the container log: `qbit-add 202 instance=… hash=… category=… name=… queued=N matched=true/false` — makes it trivial to confirm whether qBit's call reached resolvarr and what the rule did with it.

- **Default aggregation window dropped from 60s to 2s.** Cross-seed bursts still batch (they complete in <1s), but users with the default no longer wait a minute for the history row.

- **Distinct History entries for repeat-fires.** When qBit fires its autorun multiple times for the same torrent (pause/resume, etc.), each row now appends the short hash so they don't look identical. Multi-event windows where every event shares name + hash collapse to `<name> · <hash> (×N)` instead of N copies.

## v0.6.4-dev — Webhook setup centralised + UI polish (2026-05-14)

### What you get

- **All webhook setup is now on the Webhooks page.** qBit webhook moved here from Settings → qBittorrent. One page for both Sonarr/Radarr and qBit webhook setup.
- **Configure button shows which Arr.** Says "+ Configure Sonarr webhook" or "+ Configure Radarr webhook" based on the active app-type pill at the top of the page.
- **All popups now match the rest of the UI.** The browser-default confirm boxes are gone — replaced with styled modals. Destructive actions get a red Confirm button so you pause before clicking.

### Bug fixes

- **Discover "Add all" no longer aborts when a group is already in your Active list.** It now skips duplicates and continues, with a toast like "Added 5 groups (2 already in Active list — skipped)".
- **"Configure in qBit" button stays clickable when the upfront preferences-read fails.** Previously the button was greyed out so users couldn't see what was actually wrong — now clicking it surfaces the qBit-side error so you can debug or pick the manual paste fallback.
- **qBit autorun command is now a single line.** Previously emitted with backslash line continuations which can be parsed inconsistently by some shells. Single-line removes the ambiguity. (Click Configure in qBit again to refresh your existing line.)
- **Wizard progress strip on Sonarr now shows the real step names.** Grab Rename / qBit S/E tag / qBit category fix used to all show "Review" because the labels were missing.

## v0.6.3-dev — Per-rule webhook URLs (2026-05-14)

### What you get

- **Each rule can have its own webhook URL.** Click the 🔗 URL button on a rule card → Generate URL → paste it as a new Webhook in Sonarr/Radarr → Settings → Connect. Only that one rule fires from that URL. Useful when you want two rules on the same Sonarr/Radarr to react to different Connect events independently.

- **Rotate, replace or turn off the URL** any time from the same modal, without affecting other rules.

- **Strict mode** rejects events that don't include the matching Secret. Turn it on after you've verified a Test event arrives.

- **Heads-up if you forget to paste.** The modal warns when a URL is generated but no events have arrived yet — usually means you forgot to add the webhook in Sonarr/Radarr Connect.

### Bug fixes

- **qBit Category Fix rule remembers its picks on edit.** Opening Edit used to clear the qBit instance and Download client dropdowns — clicking Save without checking would re-point the rule. Both stick now.

- **Discover preference remembered on edit.** The "When Discover finds a new release group" choice (leave disabled / add and enable) showed as nothing-picked on Edit. Now restores.

## v0.6.2-dev — Catch cross-seed adds + clearer rule wording (2026-05-14)

### What you get

- **resolvarr can now tag cross-seed adds (and any other qBit-side adds) that don't go through Sonarr Connect.** Until now the qBit Episode/Season tagging only fired when Sonarr told resolvarr about a grab. Cross-seed adds 5-10 duplicate torrents per release directly into qBit, never touching Sonarr — so those torrents never got tagged. Now qBit itself can call resolvarr per torrent added, and the same Episode/Season tagging runs on every torrent regardless of where it came from. Useful so cross-seed can be told "skip already-tagged Episodes" while still searching for Seasons.

- **One-click setup per qBit instance.** Each qBit instance on the Settings page now has a **Webhook** button. Open it and click **Configure in qBit** — resolvarr writes the right command into qBit's "Run external program on torrent added" setting for you. If you already have another script there (cross-seed-notify.sh, qbit-manage), a popup asks whether to add ours alongside (recommended) or replace it (with a backup so Reset can restore the original). Manual paste is also offered for anyone whose qBit can't be reached for writes.

- **Burst-aggregation so the history doesn't drown.** Cross-seed often adds 8-10 torrents in a few seconds. Without aggregation that would produce 8-10 separate History entries. By default, resolvarr collects all qBit-add events for the same rule within a 60-second window and rolls them up into ONE history entry showing the per-tag breakdown (e.g. "tagged 8 — Episode: 5, Season: 3"). The window length is configurable per rule (1–3600 seconds) so you can make it shorter if you'd rather see each torrent separately.

- **Sonarr Connect events still fire instantly.** The qBit-add path is separate. Normal Sonarr grabs still get tagged immediately, just like before — the new path is purely a safety net for torrents Sonarr doesn't know about. If both paths fire on the same torrent (rare race), qBit's "addTags" call is harmless on an already-tagged torrent so there's no double-tag damage.

### Bug fixes

- **Edit on a rule no longer silently re-points it at the wrong Sonarr/Radarr.** Opening Edit on an existing rule, the instance dropdown sometimes failed to show the rule's actual saved instance — it'd fall back to whichever instance was first in the list. Click Save without checking and the rule got re-pointed at the wrong Sonarr/Radarr. Fixed across all four dropdowns where this happened (Arr instance picker in webhook + schedule modes, qBit picker in Grab Rename + qBit S/E sections).

- **QFA results say "series" on Sonarr runs instead of "movies".** Tag Audio / Tag Video result panel had "movies" hardcoded in every filter label and tooltip even when the run was against a Sonarr instance. Now the wording switches based on instance type.

- **History shows "Import" instead of "Download" (matches what Sonarr/Radarr's UI calls it).** The Connect payload uses "Download" internally for the event Sonarr/Radarr's settings page calls "On Import". The History modal and the Recent activity event-type pills now translate this (plus all other event names) to match the labels in your Arr's Connect screen.

### Help text — clearer wording

- **The wizard now makes it obvious that ONE rule does many things.** Tester feedback: the "How it works" panels read as if each function (Tag Audio, Tag Video, Recover, etc.) needed its own rule, even though the UI clearly shows a checkbox list where you tick many at once. New wording leads with "Most setups only need ONE rule per Sonarr/Radarr — tick all the actions you want and you're done." Multiple rules are now framed as an exception for when you want different settings on the same Sonarr/Radarr (e.g. strict filters for 4K, looser filters for SD).

- **qBit Grab Rename description rewritten to explain what it's actually for.** The old description sold it as "fixes awkward release patterns" — undersold both what it covers and why anyone would care. New description leads with the actual reason: there's a rare grab-loop where Sonarr/Radarr keeps re-grabbing the same release because qBit's name is missing info the release title has, which makes the import-time score lower than the grab-time score. Renaming at grab time so both scores line up breaks the loop. Then lists all seven configurable trigger categories (release group, edition labels, source labels, audio labels, scene-stripped names, custom patterns, "always rename").

- **Plain-language pass on the rule help.** Replaced jargon like "fire on incoming events", "Connect event", "classifying releases", "per-rule snapshots" with clearer phrasings. No behaviour change — just easier to read.

## v0.6.1-dev — Webhook history modal redesign + UX polish (2026-05-14)

### What you get

- **Webhook rule History is much easier to read.** Each fire is now a collapsible card showing event, item title, and a quick "X changes" / "1 error" / "no changes" badge in the header. Click anywhere on the header to expand and see what each function did, with a green ▲ for changes, gray ✓ for no-change, orange ⚠ for skipped, and red ✕ for errors.

- **Grab Rename gets a from→to diff.** When a rename fires, the History row shows the old name and new name as side-by-side word lists — removed words are red and struck through, added words are green and bold. You can see at a glance exactly what the rename changed.

- **Trigger reasons are in plain English.** "missing-release-group (parser rejected: multi-token)" used to be the language. Now it says "Release group missing — filename does not end with a single release-group tag like -TOLS (text after the last hyphen had spaces or dots, so it looked like part of the title — not a group)". Same translation across all six parser-reject reasons (no-hyphen, empty, multi-token, codec, split-fragment, resolution).

- **Errors render in red, not green.** Adapter-emitted error strings ("instance vanished", "qBit unreachable", "DV tools not configured", and 22 more) used to render as a green ▲ "change" in the History modal. Now they show as red ✕ error rows. Single fix in the dispatcher, applies everywhere.

- **Webhook rule editing.** You can now edit existing webhook rules — same full Add-rule wizard, pre-filled with current values. Previously rules could only be created and deleted.

- **Webhook rule cards visually match Saved Rules cards.** Same color-coded edge bar, same chip layout, same action button row. One mental model across both pages.

- **qBit Category Fix is no longer alarming when Arr did its job.** When the category swap was already done by Sonarr/Radarr by the time we checked, the message now says "skipped (category already 'tv-sonarr' — Arr completed the swap)" instead of the old "import may have failed" wording. Layer 2 is also more forgiving on minor Arr-history lag — wording explains exactly what happened.

## v0.6.0-dev — Per-bucket strip-on-delete + Missing Episodes + Webhook end-to-end (2026-05-13)

### What you get

- **Strip-on-delete is now per-bucket.** The old single "Strip managed tags on file delete" checkbox is replaced by three checkboxes — one on the Audio step, one on the Video step, one on the DV-detail step. You can now strip only the buckets you want (e.g. audio only) when Radarr/Sonarr deletes a file.
- **Release-group tags drop off automatically on file delete.** If a rule has "Tag quality releases" on and a file gets deleted, the release-group tag (or filter-only tag) is removed from the item — no extra checkbox needed. If the rule also mirrors to a secondary instance, the tag falls off there too.
- **Missing Episodes can run in Quick fix-all and as a saved rule.** Pick "Find missing episodes" on the Basics step of QFA or Create Rule (Sonarr only). Configure threshold / buffer / Tag / Search choice on the Review step — all in one place. Saved rules fire on cron the same way Tag and Recover do.
- **Webhook rules now process imports correctly.** First-round testing found that import events from Sonarr/Radarr were silently failing — every function on every rule errored out. Fixed. Tag, Recover, Sync, Audio/Video/DV-tagging, qBit category fix all run on real imports now.
- **Webhook activity refreshes itself.** Both the per-rule History modal and the Recent activity sub-tab pick up new events automatically every 10 seconds. The ↻ Reload button still works if you want an instant refresh, but you no longer need to click it to see what happened.

### Bug fixes since the initial v0.6.0-dev push

- **Grab Rename no longer fires for filenames it should ignore.** Scene-named torrent files with dots throughout (e.g. `Movie.2024.[...].H.265-APEX`) used to trigger an unnecessary rename because the release-group reader misread `.265-APEX` as a file extension. Fixed — the reader now only strips real video extensions (`.mkv`, `.mp4`, etc.) and leaves the rest alone.
- **Missing Episodes can be saved as a scheduled rule.** Previously the feature only ran via Quick fix-all (one-shot). Now you can save it as a recurring rule — pick "Find missing episodes" on the Basics step of Create Rule (Sonarr), configure Tag / Search choice on the Review step, and it fires on cron.
- **Require-signature help text is in plain language.** Old text said "reject events without the matching Secret in Authorization: Basic header" which assumed you knew HTTP jargon. New text explains what the Secret is, why it matters, and when to turn the toggle on.
- **Sonarr Tag Library — sidebar label and section header now match what's offered.** Both used to say "Tag quality releases", but Sonarr only supports Recover today (Tag-RG-style scans come in a later release). Both now say "Recover release groups" on Sonarr; Radarr is unchanged.
- **qBit category fix gives Sonarr/Radarr a head start.** Before, resolvarr would change the qBit category instantly after every import — sometimes beating Sonarr/Radarr to their own category change. Now it waits 10 seconds first, only stepping in if the category is still stuck. Result: resolvarr only fixes the broken cases, not the slow ones.
- **Grab-rename History shows what changed.** Before: just the destination name. After: from-name AND to-name AND the reason the rename fired. Lets you understand why a rename happened directly from the History entry instead of guessing.

### Upgrade notes

- **Your existing rules are migrated automatically on first start.** Rules that had the old toggle on get the three new per-bucket checkboxes ticked — same behaviour, just split. Open the rule afterwards if you want to untick one of the buckets.

---

## v0.5.0-dev — Webhook security, new functions, Missing-episodes scanner (2026-05-11)

### What you get

- **Webhook rules now run on every Connect event from Sonarr/Radarr.** Pick the functions you want (tag the imported file, recover missing release-group, mirror tags to a second instance, rename the qBit torrent, tag with episode/season, fix stuck qBit categories) — they fire in real time as files import, upgrade, or get deleted. The full Library-scan engine — same release-group matcher, same filters, same audio/video/DV detectors — runs in single-file mode on each event. No more waiting for the next scheduled scan to catch up.

- **Webhook signing — your webhook URL is no longer the only key.** Each instance gets a unique Secret on top of the URL token. Paste it into Sonarr/Radarr's Webhook **password** field and flip on "Require signature" — resolvarr will reject any event that doesn't have the matching Secret. Legacy mode (no Secret) still works for upgrade-friendliness; flip the toggle on when you're ready to lock down. Rate-limited audit log (5-min window) so a leaked URL can't flood the activity view.

- **qBit Category Fix — auto-correct stuck post-import categories.** Sonarr/Radarr's "change category after import" sometimes silently fails (qBit busy, API timeout, version-specific bug) and leaves your torrent stuck with the pre-import category. Resolvarr now listens for import events, double-verifies the import actually completed (event payload + Arr's own history with retry-backoff for the race window), and finishes the category swap. Category names auto-load from your Sonarr/Radarr Download Client config — no typing required.

- **qBit Episode/Season tagging redesigned to match the popular community auto-tagger.** Three rules: Episode (matches S01E05, multi-episode, daily-show date patterns like 2024.10.15), Season (matches S01, Season 1), Unmatched (catch-all for movies, music, anything non-TV). Customisable tag names per rule. First-match-wins ordering. Drop your auto-tagger Python script — resolvarr does the same job with a cleaner UI + better diagnostics.

- **Retro-tag your existing qBit library.** "Backlog scan" button on every qBit episode/season-tag rule walks all your torrents and shows what would be tagged. Per-row checkboxes + bulk apply. Solves the "I just configured this rule but my existing 5000 torrents have no tags" problem.

- **Tag Library: find missing episodes in fully-aired seasons** (Sonarr-only). New "Missing episodes" sub-tab walks your monitored series, finds seasons that have fully aired but you're missing one or two episodes in the middle. Configurable threshold (default 70% — won't pester you about brand-new series you haven't downloaded yet) and a 24-hour airing buffer (gives indexers time to register new releases). One-click search the missing episodes via Sonarr's normal indexer flow, or tag the series so you can filter them in Sonarr's UI. Auto-cleans the tag when the series becomes complete.

- **Per-rule fire history — see exactly what happened.** Click "History" on any webhook rule to see the last 7 fires with: status (ok/partial/error), event type, item title, release name (from the indexer), file path (after Radarr's rename), and a per-function summary string like *"tagReleaseGroups: applied 'flux'; syncToSecondary: mirrored to Radarr (Remux); fileDeleteClean: skipped (not a Delete event)"*. Lets you diagnose "why didn't my movie get tagged?" without digging through container logs.

- **`Tag release groups` renamed to `Tag quality releases`.** Since it now covers per-group tagging, Discover auto-add, AND filter-only mode (one shared tag for everything passing the filter), the old name was misleading. All UI surfaces use the new name.

- **Mirror-to-secondary is its own dedicated control** (separate from the function checkboxes). Reads naturally: tick Tag quality releases → an "Also mirror to <secondary Radarr>" toggle appears below. Works for both per-group tags AND filter-only tag (`lossless-web` by default).

### Smaller polish

- Function descriptions across the rule editor are now driven by one canonical source — no more drift between the schedule-mode help and the webhook-mode checkboxes.
- Webhook setup tab has a "How it works" panel that covers URL setup vs Rules vs Secret vs Require-signature.
- Help panels everywhere clarify the per-file (webhook) vs library-wide (scan) scope of the orphan-cleanup toggles. Plain-language explanations of what every option does.
- NON-IMAX titles no longer false-trigger the IMAX rename rule.
- Step 3b (qBit Grab Rename) + Step 3c (qBit S/E) UIs are fully wired in the rule editor.
- Sonarr-side rule editor steps hide options that don't apply (e.g. movie-version triggers only show on Radarr instances).
- Browser back/forward navigation works across all tabs.

### Heads-up if you used v0.4.0-dev

- Webhook rules saved on v0.4.0-dev still work as-is. New fields (Secret, RequireSignature, qBit Category Fix, etc.) default to off/empty.
- Categories typed by hand on v0.4.0-dev (none — feature is new) — no action needed.
- "Tag release groups" → "Tag quality releases" is a UI rename only; nothing about behaviour changed.

### Tested but not soak-tested

This is a substantial release. Every feature has unit tests + the new webhook functions went through multi-agent code review before commit, but they haven't been exercised in real-world flow yet — try them, file what breaks. The Configure webhook → Add rule walkthrough is the next thing we'll sit down with.

---

## v0.4.0-dev — Filter-only tag mode + Apply-now polish (2026-05-09)

### What you get

- **New "Use filter only" mode for Tag release groups.** Pick this on
  the Run Tag wizard / Quick fix-all / Create rule and resolvarr tags
  every movie passing your quality + audio filter — release group
  ignored. One tag for everything that meets your bar, no per-group
  bookkeeping. Default tag name `lossless-web` reflects the out-of-
  the-box filter (MA/Play WEB-DL + lossless audio); rename it if you
  customise the filter. Best when you don't care which group made the
  file — only that it meets your quality threshold. New high-quality
  groups get tagged automatically without any config changes.

- **Apply-now buttons on Audio, Video, and DV detail result panels.**
  After running a preview, scroll up OR down and click Apply now —
  same confirm modal as the wizard's Apply, no need to re-walk the
  wizard. The button sits at both the top and bottom of long result
  lists so you don't have to scroll all the way to apply.

- **Apply-now respects target=both.** If you ran the wizard against
  both instances, Apply re-fires once per instance — covers everything
  the preview showed. The variant switcher in the result modal stays
  read-only (just for reading the result); Apply hits whatever the
  wizard's target choice was.

- **Run-mode result panels stay scoped.** Run Quick fix-all on Radarr,
  flip to Sonarr in the picker, the Radarr result hides. Flip back,
  it's still there. No more cross-contamination of result panels
  between Arr types.

- **Tag Library help-panel rewrite.** Removed false claims about
  shared-tag mode and "scoring custom formats based on tag" — Custom
  Formats score releases (downloads), not movies. Replaced with the
  actual disk-savings use case (tag your WEB-DL Radarr → mirror to
  Remux Radarr → cleanup tool reclaims 3-6 TB on overlapping titles)
  written tool-agnostic so you can pick whichever cleanup tool you
  trust.

- **Recover apply-from-preview works correctly.** Open Run Recover,
  see the would-fix list, tick what to apply, click Apply selected
  fixes. No more "I marked the rows but nothing happened" — the
  preview path now applies against the same instance the preview ran
  on, surfaces errors visibly, and the auto-selection of would-fix
  rows actually populates so the Apply button isn't disabled by
  default.

### Why these matter

The "shared tag" pattern previously documented for tagarr scripts
(multiple groups → same tag like `premium`) silently flapped on every
alternate run because each group's decision is independent — `flux`
group says "tag", `sic` group says "remove" (no match), and Apply does
both. Filter-only mode is the architecturally clean replacement: one
rule, one tag, evaluated once per movie. No flapping, and broader
coverage (catches every passing movie regardless of group).

### Behind the scenes

- New `runTagFilterOnly` engine path mirrors the existing per-group
  path's sync-to-secondary + orphan-cleanup machinery. Single tag
  decision per movie, no group iteration.
- Tag-name conflicts (filter-only's tag matches an existing Active
  group) are blocked at API time with a clear error pointing at the
  conflicting group, plus a symmetric reverse check when you add a
  group whose tag matches an existing filter-only schedule.
- Multi-agent code review pass before push: 4 blockers + 8 concerns
  landed across schedule validation, defensive guards, modal apply
  states, and variant-switcher consistency.

### Upgrade notes

Existing rules without `tagSource` set keep using the per-group path
(legacy behaviour, no change). Schedules saved with garbage TagSource
get clamped to "" at next config Load.

---

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
  per-event tagging features ship in subsequent releases.

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
  parked for a later release.

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
  fire, or wizard with localStorage memory.
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
