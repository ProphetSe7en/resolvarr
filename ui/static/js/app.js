// app.js — resolvarr Alpine.js app. Single Alpine `app()` factory returns a
// state-bag + methods object that x-data binds to. Past the structure-
// baseline §8.3 1500-line warn threshold; split into feature modules
// (js/scan.js, js/tag-inventory.js, etc.) when it crosses 3000 or grows
// a clear feature seam.
//
// Browser global helpers used inside this file (fetch, document, etc.)
// don't need imports — single-file, no module bundler.

function app() {
  return {
    // state
    version: 'dev',
    // Server-side context for time formatting. Loaded from /api/version
    // on init. Defaults are safe-but-neutral; real values arrive after
    // the first fetch. timezone is an IANA name (e.g. "Europe/Oslo")
    // and locale is a BCP 47 tag (e.g. "nb-NO" or "en-GB"). Both flow
    // into formatDate / scheduleNextRun / scheduleNextFires so every
    // timestamp matches the container's host context, not the
    // browser's whim — fixes the case where a Norwegian admin on an
    // en-US browser would see MM/DD/YYYY + AM/PM.
    serverTimezone: '',
    serverLocale: 'en-GB',
    // User-overridable display preference for time format.
    //   ''/'auto' — locale decides (Norwegian TZ → 24h, US TZ → 12h)
    //   '24h'     — force 24-hour clock regardless of locale
    //   '12h'     — force 12-hour AM/PM clock regardless of locale
    // Loaded from /api/config; saved to /api/config/display alongside
    // uiScale.
    timeFormat: 'auto',
    currentPage: 'settings',
    section: 'instances',
    // Hash-routing guard. pushNav writes location.hash → browser
    // fires hashchange → restoreFromHash runs → state setters fire
    // pushNav again. Set to true around restore to break the loop.
    // See restoreFromHash + pushNav for the contract.
    _navSkipPush: false,
    webhookSection: 'setup',  // 'setup' | 'activity'. Per-event-type sub-tabs (Grab / Import / Delete) dropped 2026-05-07 — they suggested global per-event settings, but real architecture is per-instance via wizard.
    // Webhook subsystem state. webhookConfigs is populated by
    // loadWebhookSetupPage on Webhooks-tab open + after wizard finish:
    // { [instanceId]: { token, url, loggingEnabled } }. Empty for
    // instances that haven't been configured yet.
    webhookConfigs: {},
    // Per-instance recent events cache. Loaded lazily when the user
    // picks an instance on the Recent activity sub-tab. Newest-first
    // arrays of WebhookEvent objects.
    webhookEvents: {},
    webhookEventsLoading: false,
    webhookActivityInstanceId: '',
    webhookEventFilter: 'all',     // 'all' | <eventType> — chip filter on activity panel
    webhookActivitySearch: '',     // case-insensitive substring on event title + subtitle
    webhookOutcomeFilter: 'all',   // 'all' | 'changed' | 'no-change' | 'no-rule' | 'errors' — outcome dropdown
    // Content-shape multi-select. Empty array = no filter (show all).
    // Non-empty = only shapes in the array. Possible values:
    //   'episode'      single episode (episodes.length === 1)
    //   'season-pack'  multi-episode grab or "S04E05 + N more" subtitle
    //   'movie'        Radarr event (body.movie present)
    //   'system'       no movie + no series (Test / Health / Manual /
    //                  Application update / qBit-add catch-all)
    webhookContentShapeFilter: [],
    webhookEventExpanded: {},      // { [eventId]: true } — expand-to-see-JSON toggle
    webhookDetailsExpanded: {},    // { [instanceId]: true } — Setup-card details panel (URL + Secret)
    webhookSecretRevealed: {},     // { [instanceId]: true } — show secret in plaintext vs masked
    // Webhook configuration wizard state. Two-step flow matching the
    // QFA / Tag / Discover wizards' Step-1-carries-everything pattern:
    //   Step 1 (Choices): Arr type pick (Sonarr/Radarr radio) +
    //     instance pick (filtered to chosen type) + function picks
    //     (today: only Enable logging — others are placeholders).
    //   Step 2 (Summary): generated/existing webhook URL + the list
    //     of Connect events to enable in Sonarr/Radarr based on the
    //     function picks.
    webhookWizard: {
      open: false,
      step: 0,
      appType: 'radarr',
      instanceId: '',
      fnLogging: true,
      busy: false,
      generatedUrl: '',         // populated after Step 1 advance
      generatedSecret: '',      // shared-secret-as-Basic-auth-password (Phase 2 Slice A)
      generatedLoggingEnabled: false,
      requireSignature: false,  // strict-mode toggle on Summary step (Phase 2 Slice B)
    },
    scanSection: 'run',           // 'run' | 'groups' | 'filters' | 'recover' | 'audio' | 'video' | 'dvdetail' | 'history'
    // Library scan App-type pill — same pattern as Tag inventory. Picks
    // which Arr type the page operates on; instance dropdown is filtered
    // to that type, sub-tabs hide the ones that don't apply (Sonarr
    // currently supports Recover only — Tag library / Discover / Audio /
    // Video / DV detail are Radarr-only until each is ported).
    scanAppType: 'radarr',

    // Per-sub-tab help-panel toggles. Default closed; click the info icon
    // at the top of a sub-tab to expand a longer "How it works" panel.
    // Not persisted — fresh page load = closed again. Keys: run / groups /
    // filters / extra / tags (the standalone Tag inventory page).
    helpOpen: { run: false, groups: false, filters: false, recover: false, sonarrRecover: false, audio: false, video: false, dvdetail: false, missingEpisodes: false, tags: false, history: false,
      // Webhooks page-level help.
      webhooks: false,
      // Wizard-step help panels — same toggle pattern as the Library
      // scan fanes. Each wizard step renders its own collapsible
      // "How it works" panel so the inline form copy can stay short.
      ruleBasics: false, ruleRG: false, ruleFilters: false, ruleAudio: false, ruleVideo: false, ruleDvDetail: false, ruleMissingEpisodes: false, ruleGrabRename: false, ruleQbitSe: false, ruleQbitCategoryFix: false, rulePlexLabelSync: false, ruleSchedule: false },

    // Single source of truth for short function descriptions. Used by:
    // - Rule editor Basics step (schedule + webhook modes) to render
    //   the function checkboxes / help-list with one canonical
    //   wording per function instead of three slight variations.
    // - QFA wizard step descriptions (when porting future).
    // - Tag Library section-desc (when porting future).
    //
    // Per entry:
    //   id            — option key on editingRule.options.fn<Cap>
    //   optionFlag    — the editingRule.options key the checkbox writes to
    //   label         — UI heading for the row
    //   summary       — Radarr / generic plain-language description
    //   summarySonarr — optional Sonarr-specific variant (falls back to summary)
    //   triggers      — webhook events this function listens for
    //                   (function id may pass through ruleEditorIsWebhook
    //                    contexts where they're shown as a callout)
    //   appliesTo     — 'radarr' | 'sonarr' | 'both'
    //   webhookOnly   — true when the function only makes sense from a
    //                   Connect-event trigger (Grab Rename, qBit S/E,
    //                   Strip-on-delete). Hidden in schedule-mode rules.
    //   scheduleOnly  — true when the function only makes sense as a
    //                   chained scheduled action (Cleanup unused tags).
    //                   Hidden in webhook-mode rules.
    FUNCTION_INFO: {
      discover: {
        id: 'discover',
        optionFlag: 'fnDiscover',
        label: 'Discover new release-groups',
        summary: 'Walks your library and surfaces release groups that pass your filter but aren\'t on your Active list yet — only filter-qualifying groups are reported, not every group seen. New groups get added on the Release Groups step (Add + leave disabled / Add + enable). Useful for first-time setup or keeping the list current as new groups appear in your imports.',
        summaryWebhook: 'Checks the release group of the imported file and adds it to your Active list if it isn\'t there yet — but only when the group passes your filter. Keeps the Active list growing automatically as new filter-qualifying groups appear in your imports.',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'radarr',
      },
      recover: {
        id: 'recover',
        optionFlag: 'fnRecover',
        label: 'Recover missing release groups',
        summary: 'Indexers usually know the release group, but if the torrent filename doesn\'t include it the release-group field ends up empty or "Unknown" after import. This step looks at the original grab history — where the indexer\'s release group is preserved — and writes it back to the file.',
        summarySonarr: 'Indexers usually know the release group, but if the torrent filename doesn\'t include it the release-group field ends up empty or "Unknown" after import. This step looks at Sonarr\'s grab history per series and writes the indexer\'s release group back to the affected episode files.',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'both',
      },
      tagReleaseGroups: {
        id: 'tagReleaseGroups',
        optionFlag: 'fnTagReleaseGroups',
        label: 'Tag quality releases',
        summary: 'Walks your library and tags movies by release group. Three modes: match against your Active list (filter is a per-group quality gate), use Discover to find and add new groups passing the filter then tag, or skip groups entirely and tag every movie passing the filter with one shared tag (filter-only mode).',
        summaryWebhook: 'Tags the imported movie based on its release group. Three modes mirror Library scan: match against your Active list (filter is a per-group quality gate), pair with Discover to auto-add new filter-qualifying groups before tagging, or skip groups entirely and tag every movie passing the filter with one shared tag (filter-only mode).',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'radarr',
      },
      tagAudio: {
        id: 'tagAudio',
        optionFlag: 'fnTagAudio',
        label: 'Tag Audio',
        summary: 'Adds tags showing what kind of audio the file has (TrueHD, Atmos, 5.1, 7.1, etc). Useful for spotting movies missing Atmos and for grouping in Plex/Jellyfin.',
        summarySonarr: 'Adds tags to each series showing what kind of audio its episodes have (TrueHD, Atmos, 5.1, 7.1, etc). Useful for spotting series that need an audio upgrade and for Plex/Jellyfin grouping.',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'both',
      },
      tagVideo: {
        id: 'tagVideo',
        optionFlag: 'fnTagVideo',
        label: 'Tag Video',
        summary: 'Adds tags showing what kind of video the file has (1080p, 4K, h265, HDR10, Dolby Vision). Same idea — easy upgrade triage and Plex shelving.',
        summarySonarr: 'Adds tags to each series showing what kind of video its episodes have (1080p, 4K, h265, HDR10, Dolby Vision). Same idea — easy upgrade triage and Plex shelving.',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'both',
      },
      tagDvDetail: {
        id: 'tagDvDetail',
        optionFlag: 'fnTagDvDetail',
        label: 'Tag DV Details',
        summary: 'Reads inside Dolby Vision files to figure out which kind of DV they are (profile 5 / 7 / 8, FEL / MEL, CM2 / CM4). The first run is slower because it has to read each file; later runs are fast.',
        summaryWebhook: 'Reads inside the imported Dolby Vision file to figure out which kind of DV it is (profile 5 / 7 / 8, FEL / MEL, CM2 / CM4) and tags it accordingly.',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'radarr',
      },
      plexLabelSync: {
        id: 'plexLabelSync',
        optionFlag: 'fnPlexLabelSync',
        label: 'Sync to Plex',
        summary: 'After the other tag steps have written their changes to Radarr/Sonarr, propagate the whitelist tags out to Plex — as labels, collections, or both — for the imported item. Configure the Plex side directly on this rule (Plex instance, libraries, whitelist tags, target mode) in the Plex label sync wizard step. Per-event, single-item scope — no full library scan.',
        summaryWebhook: 'After the other tag steps run on this rule, propagate the whitelist tags out to Plex — as labels, collections, or both — for the imported item. Use the Plex label sync wizard step to pick the target Plex instance, libraries, and which tag-names should travel. Per-event, single-item scope.',
        triggers: ['On File Import', 'On File Upgrade', 'On File Delete'],
        appliesTo: 'both',
        // Webhook-only function. Scheduled rules use the dedicated
        // Plex label-sync sub-tab + its own scheduler reference
        // (Approach A — not yet wired). Excluded from the schedule
        // checkbox list to avoid confusing the user with a duplicate
        // entry point.
        webhookOnly: true,
      },
      cleanup: {
        id: 'cleanup',
        optionFlag: 'fnCleanup',
        label: 'Delete tags that no longer match any movie',
        summary: 'Cleanup pass after Tag quality releases. If a tag ends up on zero movies, this removes it from the tag list. Only touches tags from your Active list — never manual / custom-format / quality-profile tags.',
        appliesTo: 'radarr',
        scheduleOnly: true,
      },
      syncToSecondary: {
        id: 'syncToSecondary',
        optionFlag: 'fnSyncToSecondary',
        label: 'Mirror release-group tags to secondary',
        summary: 'If you have a second Radarr (e.g. a 4K/remux instance), this copies the Tag quality releases decisions from primary over so both stay in sync. Audio/Video/DV tags aren\'t mirrored — set them to run on the secondary as their own scan in the next step.',
        triggers: [],
        appliesTo: 'radarr',
        // Rendered as its own form-group below the function checkbox
        // list (mirrors the schedule-mode block at line 5949-5983) so
        // the secondary-instance picker can live next to the toggle.
        // Excluded from the lean checkbox list by default; help-panel
        // still surfaces it via functionInfoList({ includeSeparate: true }).
        separateControl: true,
      },
      // fileDeleteClean was a single rule-level checkbox that stripped
      // every file-property tag (audio/video/DV) plus the release-group
      // tag on file-delete events. As of v0.6.0-dev that all-or-nothing
      // function is gone — replaced by:
      //   - per-bucket "Strip <bucket> tags on file delete" checkboxes
      //     in the Audio / Video / DV detail wizard steps (granular
      //     opt-in per bucket)
      //   - automatic Tag-RG strip-on-delete that fires whenever a rule
      //     has Tag-RG enabled (no separate user opt-in needed; the
      //     primary's qualification is the single source of truth)
      // The legacy function is auto-migrated on first Load post-update
      // (per-bucket flags set true on all three buckets; legacy function
      // dropped from Functions). Existing rules keep doing what they did.
      grabRename: {
        id: 'grabRename',
        optionFlag: 'fnGrabRename',
        label: 'qBit Grab Rename',
        summary: 'Fixes a rare grab-loop where Sonarr/Radarr keeps re-grabbing the same release. The loop happens when the qBittorrent torrent name is missing info the release title has — the import-time score then comes out lower than the grab-time score, so the Arr decides the file isn\'t good enough and grabs again. This action renames the qBit torrent to match the release title at grab time so both scores line up. You pick which kinds of missing info to fix per rule: release group at the end of the name, edition labels (Remastered, Director\'s Cut), source tokens (WEB-DL, BluRay, REMUX), audio tokens (Atmos, DD+, TrueHD), names that look stripped of their scene group, plus your own custom patterns. Includes a "rename every time" option if you just want everything to land with the full release title.',
        triggers: ['On Grab'],
        appliesTo: 'both',
        webhookOnly: true,
        requiresQbit: true,
      },
      qbitSeTag: {
        id: 'qbitSeTag',
        optionFlag: 'fnQbitSeTag',
        label: 'qBit episode / season tag',
        summary: 'Tags the qBit torrent with the season + episode it\'s downloading (S01, S01E05, S01E05E06). Useful when you have a long-running backlog and want to filter qBit by season.',
        triggers: ['On Grab'],
        appliesTo: 'sonarr',
        webhookOnly: true,
        requiresQbit: true,
      },
      qbitCategoryFix: {
        id: 'qbitCategoryFix',
        optionFlag: 'fnQbitCategoryFix',
        label: 'Fix stuck qBit category after import',
        summary: 'Sonarr/Radarr is supposed to change a torrent\'s qBit category after import (e.g. from "qbit-movies" to "qbit-movies-imp"). Sometimes that update silently fails and the torrent gets stuck on the pre-import category. This function listens for import events, verifies the import really happened (Arr history check), and corrects the category if needed.',
        summaryWebhook: 'On every import: check the torrent\'s qBit category, verify the import actually completed via Arr\'s history, and swap to the post-import category if it\'s still stuck on pre-import.',
        triggers: ['On File Import', 'On File Upgrade'],
        appliesTo: 'both',
        webhookOnly: true,
        requiresQbit: true,
      },
    },
    scanInstanceId: '',           // primary instance for a run; picked per-run, not persistent
    // Tag is always-on for the Run scan button on the Tag library card
    // (the checkbox was removed 2026-04-29 — clicking Run scan implies tag).
    // Discover and Recover stay false-by-default; they have their own
    // dedicated entry points (Discover lives on Release Groups → Find new
    // groups, Recover has its own card with Find missing groups button)
    // and Quick fix-all flips them on internally per its own toggles.
    scanModes: { tag: true, discover: false, recover: false },
    scanMode: 'preview',          // 'preview' | 'apply' (tag-mode only)
    scanLoading: false,
    scanError: '',                // error message from last run, cleared on next run
    scanResults: { tag: null, discover: null, audioTags: null, videoTags: null, dvDetail: null },   // per-mode result objects populated only when that mode ran. Recover always lands on this.recoverResults regardless of trigger.
    scanFilter: 'add',            // 'add' | 'remove' | 'keep' | 'nofile' | 'missing' — default 'add' so the user lands on actionable rows
    scanInstanceFilter: 'both',   // 'both' | 'primary' | 'secondary' — when sync is enabled, narrows counts + rows to one side
    scanGroupExpanded: {},        // tag-mode: { [groupId]: true } for which group rows are expanded to list movies (one level — same as Discover)
    // Per-movie row expand inside a tag-result group's drill-in. Mirrors
    // the Discover / Tag inventory pattern: collapsed shows title + verdict
    // text accents + match-location hint; expanded shows Q/A chips, reason,
    // and the full file-context grid with token highlights.
    scanRowExpanded: {},
    scanIncludeKnown: false,      // discover-mode toggle — include already-configured groups (audit/--discover-clean parity)
    scanSyncToSecondary: false,   // tag-mode toggle — mirror primary decisions to the secondary radarr instance via TmdbID (M3e)
    // Cleanup-unused-tags toggle for the standalone Tag library card.
    // Same effect as the wizard's CleanupUnusedTags option: after the
    // tag pass, every managed (release-group) tag whose final usage
    // count drops to 0 is deleted from Radarr. Off by default —
    // destructive, opt-in. Affects primary + secondary symmetrically
    // when sync is on (per scan_tag.go:520-543).
    scanCleanupUnusedTags: false,
    // Filter-only tag-mode state. "" / "active" / "discover" mean the
    // legacy per-group code path; "filter-only" routes the request to
    // runTagFilterOnly on the backend with scanFilterOnlyTag as the
    // single tag emitted for every movie passing the quality + audio
    // filter. Set via the wizard's filter-only branch; cleared on a
    // standalone Run that doesn't go through the wizard.
    scanTagSource: '',
    scanFilterOnlyTag: '',
    // Mini-wizard state for the new "Tag quality releases" launcher on
    // the Tag quality releases sub-tab. 4 steps:
    //   Choices → Filter → Active groups → Review.
    // The wizard is a thin orchestrator — final Run hands off to the
    // existing runLibraryScan path after copying picks into the
    // standard scanModes / scanMode / scanSync* state. No new
    // backend endpoint; just a guided front-end for the same scan.
    tagRgWizard: {
      open: false,
      step: 0,
      source: 'active',           // 'active' | 'discover' | 'filter-only'
      discoverAdd: 'enabled',     // 'disabled' | 'enabled' (only when source==='discover')
      filterOnlyTag: 'lossless-web', // only when source==='filter-only' — matches the OOTB MA/Play WEB-DL + lossless filter; user can rename
      syncToSecondary: false,
      cleanupUnusedTags: false,
      runMode: 'preview',         // 'preview' | 'apply'
      busy: false,
    },
    // Discover-only mini-wizard. Opened by the standalone "Run Discover"
    // button on the Tag quality releases actions card. Three steps:
    //   Choices → Filter → Review.
    // Same shape as tagRgWizard but no run-mode (Discover doesn't have
    // preview/apply — it always lists candidates for the user to tick),
    // no Active-groups step (we're finding NEW groups, not touching the
    // existing list), and no sync (Discover doesn't write tags). The
    // Choices step covers the two real options: audit mode (include
    // groups already in Active in the result, for verification) and
    // add-behavior (when the user later clicks Add Selected on the
    // result modal, should the new groups land enabled or disabled).
    discoverWizard: {
      open: false,
      step: 0,
      runMode: 'preview',         // 'preview' | 'apply' — Preview: show candidates, user picks via Add Selected. Apply: auto-add all candidates with chosen addBehavior.
      addBehavior: 'enabled',     // 'enabled' | 'disabled' — drives _tagRgDiscoverEnableOnAdd at run time
      includeKnown: false,        // mirrors scanIncludeKnown — hydrated on open, written back on run
      busy: false,
    },
    scanDiscoverSelected: {},     // discover-mode: { [search]: true } for "Add Selected" (search is keyed in original case from response)
    scanDiscoverExpanded: {},     // discover-mode: { [search]: true } for which group rows are expanded to show samples
    // Per-sample row expand inside a group's drill-in. Composite key
    // <search>:<movieId> because movie IDs can repeat across groups
    // (the same movie can match release-group strings differing only
    // in case or surrounding chars). Mirrors the Tag inventory pattern
    // so both views feel the same when the user drills two levels deep.
    discoverSampleExpanded: {},
    scanDiscoverAdding: false,    // discover-mode: loading state for "Add Selected" / "+ Add" submission
    scanDiscoverBannerDismissed: false,  // user dismissed the Run-mode "Discover ran" banner; reset on next scan
    // Discover detail modal auto-pops on scanResults.discover (no flag).
    // Manual Library-scan sync target. Empty = let the backend
    // auto-pick (single-secondary case). Auto-defaulted in pickDefaultScanFilter
    // when there's exactly one candidate.
    scanSyncTargetId: '',
    // Skip the orphan-cleanup pass when mirroring tags. Default ON
    // (legacy scanSyncSkipOrphanCleanup field removed — sync's
    // orphan-cleanup pass is part of bash-parity sync semantics
    // and not user-toggleable, per tagarr.conf "Orphaned tags in
    // secondary ... are cleaned up automatically" + tagarr.sh's
    // unconditional orphan walk when ENABLE_SYNC_TO_SECONDARY=true.)
    // Cleanup section (Release Groups sub-tab). Standalone tag-cleanup
    // entry point — runs against the chosen instance, surfaces managed tags
    // with 0 movies, lets user delete per-row or in bulk.
    cleanupLoading: false,
    cleanupError: '',
    cleanupResults: null,         // { instance, totals: {items, tagsToDelete: [{label, tagId, count}]} }
    cleanupSelected: {},          // { [label]: true } for bulk-delete
    cleanupDeleting: false,       // loading state for Delete one / Delete selected

    // Recover section (Run mode sub-tab) — M3c. Surfaces movies whose
    // releaseGroup is empty/Unknown, fetches grab history per movie,
    // classifies into six buckets via engine.recover. recoverApplySelected
    // gates per-row inclusion in the apply pass — defaults all-on after a
    // preview run, user can untoggle rows that look wrong.
    recoverLoading: false,
    recoverError: '',
    recoverResults: null,        // { instance, totals: {...}, recover: [{id, title, status, recoveredGroup, ...}] }
    recoverFilter: 'all',        // 'all' | status — narrow the row list
    recoverExpanded: {},         // { [itemId]: true } for episode/movie drill-down
    recoverSeriesExpanded: {},   // Sonarr only — { [seriesId]: true } for series cards
    recoverSeasonExpanded: {},   // Sonarr only — { [`${seriesId}-${seasonNumber}`]: true }
    recoverApplySelected: {},    // { [itemId]: true } — which would-fix rows to apply
    recoverRename: true,         // mirrors bash RENAME=true default
    recoverApplying: false,      // loading state for apply
    // Recover exclusions — per-instance "skip these in next scan"
    // list. User flags faulty / unfixable items via the Exclude
    // buttons in the result modal. Loaded from /api/recover/exclusions
    // on demand (when the result modal opens or the show-excluded
    // panel toggles on). Shape mirrors the GET response — three flat
    // arrays the markup reads directly.
    recoverExclusions: { instanceId: '', movies: [], series: [], seasons: [] },
    recoverExclusionsLoading: false,
    showScanApplyConfirm: false,  // confirm modal before applying a preview's decisions

    // Missing-episodes scanner (Tag Library → Sonarr → Missing episodes).
    // Sonarr-only feature; the sidebar entry hides on Radarr. State is
    // session-scoped — config not persisted to globals.
    //  - missingEpisodesConfig drives the form inputs; thresholdPercent
    //    is whole-number 0-100 (sent to the backend as /100 for the
    //    engine's 0.0-1.0 contract).
    //  - missingEpisodesPreview holds the last scan response.
    //  - missingEpisodesSelected maps episodeID → bool for the per-row
    //    selection state (used by the bulk Search button + Select all/none).
    //  - missingEpisodesApplying gates the per-row + bulk buttons during
    //    an in-flight Sonarr command POST.
    missingEpisodesConfig: {
      thresholdPercent: 70,
      bufferHours: 24,
      includeContinuing: true,
      includeEnded: true,
      // B2: specials (season 0) skipped by default — typically ad-hoc
      // content the user doesn't curate via Sonarr search. Tick the
      // toggle in the UI to flag them.
      includeSpecials: false,
      tagName: 'missing-episodes',
    },
    missingEpisodesPreview: null,
    missingEpisodesSelected: {},
    missingEpisodesLoading: false,
    missingEpisodesApplying: false,
    missingEpisodesError: '',

    // TBA refresh (Sonarr-only). Same filter toggles as Missing
    // Episodes. tbaRefreshSelected maps episodeFileId → bool for the
    // per-row checkboxes.
    tbaRefreshConfig: {
      includeContinuing: true,
      includeEnded: true,
      includeSpecials: false,
    },
    tbaRefreshPreview: null,
    tbaRefreshSelected: {},
    tbaRefreshLoading: false,
    tbaRefreshApplying: false,
    tbaRefreshError: '',

    instances: [],
    // qBittorrent instances — user-managed list. Populated by
    // loadQbitInstances on Settings → qBit visit + after each
    // create/update/delete. Used today for the standalone CRUD
    // page; future sessions add WebhookConfig.QbitInstanceID
    // pairing + backlog scan picker.
    qbitInstances: [],
    qbitInstanceModal: {
      open: false,
      id: '',
      name: '',
      url: '',
      username: '',
      password: '',
      trustedCerts: false,
      busy: false,
      testing: false,
      testResult: '',
      testOk: false,
    },
    // Per-instance live status — flat-shape parity with Arr's
    // instStatus/instError so the row pill template renders the same
    // way ("Connected" / "Failed — <err>" / "Testing" / "Not tested").
    // Auto-refreshed via refreshAllQbitStatus() on init + every 60s.
    qbitStatus: {},
    qbitError: {},
    deleteQbitTarget: null,

    // Plex instances — user-managed list used by the Plex label-sync
    // feature. Populated by loadPlexInstances on Settings → Plex visit
    // and after every CRUD action. Each instance carries a cached
    // Libraries list refreshed on demand via the "Fetch libraries"
    // button (no auto-refresh — Plex's section list is stable enough
    // that surprise-refreshes would just burn API calls).
    plexInstances: [],
    plexInstanceModal: {
      open: false,
      id: '',
      name: '',
      url: '',
      token: '',
      trustedCerts: false,
      busy: false,
      testing: false,
      testResult: '',
      testOk: false,
    },
    // Per-instance live status — parallel to qbitStatus / qbitError.
    // refreshAllPlexStatus() sweeps on Settings → Plex visit + every
    // 60s thereafter so the row pill stays current.
    plexStatus: {},
    plexError: {},
    plexLibrariesBusy: {},
    deletePlexTarget: null,
    deletePlexBusy: false,

    // Plex label-sync one-off run form state. Backs the "Configure and
    // run Plex label sync" form on the Library scan → Plex sync sub-tab
    // (no saved rule — the form POSTs to /api/plex-sync/run). The
    // webhook + schedule wizards use their own config objects.
    plexLabelRuleModal: {
      open: false,
      id: '',
      name: '',
      enabled: true,
      instanceId: '',
      appType: '',
      // labels is the array of whitelisted tag-names — populated by
      // ticking checkboxes in the Arr-tag picker. The chip strip
      // above the picker is read-only display + × to drop entries.
      labels: [],
      // labelDisplay maps Arr tag-name (key in Labels) → Plex-side
      // display string. Empty / missing entries fall back to the Arr
      // tag verbatim. Lets users render "Atmos" / "FEL" / "Dolby
      // Vision" on Plex even when Radarr's strict lowercase-kebab tag
      // validator only accepts the lower-case form. UI shows an
      // inline input next to each ticked checkbox; validator on the
      // backend drops orphan + identity + empty entries at save-time.
      labelDisplay: {},
      plexInstanceId: '',
      libraryKeys: [],
      runMode: 'apply',
      // targetTypes — array of Plex metadata targets. Multi-select:
      // can contain "label" and/or "collection". Engine runs one
      // diff + apply pass per target type. Default ["label"] for
      // new rules.
      targetTypes: ['label'],
      busy: false,
    },

    // Plex label-sync run modal — drives the "Run now" flow. Three
    // states the same modal cycles through:
    //   1. confirm — user picks Preview / Apply
    //   2. busy    — request in flight; spinner + disable buttons
    //   3. result  — render the PlexLabelRuleRun returned by the
    //                backend (summary + counts + per-label detail)
    plexLabelRunModal: {
      open: false,
      stage: 'confirm', // 'confirm' | 'busy' | 'result'
      rule: null,       // the rule being run (full object, for header display)
      runMode: 'apply', // user's pick — defaults to the rule's runMode on open
      result: null,     // PlexLabelRuleRun returned by /run endpoint
      error: '',        // top-level error (network / 4xx / 5xx)
      detailsFilter: '', // result stage: filter the per-item details list to one label (empty = show all)
    },
    // Live tag list from the rule's picked Arr instance — populated by
    // plexLabelRuleLoadAvailableTags() when the modal opens or the Arr
    // dropdown changes. Drives the "Pick from your tags" checkbox
    // section in the rule modal so the user doesn't have to remember
    // exact tag-names from Radarr/Sonarr.
    plexLabelRuleAvailableTags: [],
    plexLabelRuleTagsLoading: false,
    plexLabelRuleTagsError: '',
    // Case-insensitive substring filter on the tag checkbox list.
    // Lets users with many tags narrow down without scrolling.
    // Empty = show everything. Reset every time the modal opens.
    plexLabelRuleTagFilter: '',

    // ---- Wizard "Sync to Plex" step state (isolated from the one-off
    // form above so the verified one-off flow is never touched). Bound
    // to editingRule.plexSync; tag list comes from editingRule.instanceId.
    wizardPlexAvailableTags: [],
    wizardPlexTagsLoading: false,
    wizardPlexTagsError: '',
    wizardPlexTagFilter: '',

    // M-qBit-add Slice 5 — per-instance webhook hook modal state.
    qbitWebhookOpen: false,
    qbitWebhookInstance: null,
    qbitWebhookData: null,
    qbitWebhookLoading: false,
    qbitWebhookActionInFlight: false,
    qbitWebhookShowCurl: false,
    qbitWebhookTestResult: null,
    qbitWebhookConflictOpen: false,
    qbitWebhookConflictMode: 'append',
    // Editable override for the URL qBit calls back on. Hydrated from
    // qbitWebhookData.webhookCallbackUrl when the modal opens; empty
    // string means "use r.Host detection" (the default qbitWebhookData
    // .defaultWebhookUrl). Sent to backend on Configure with
    // hasOverride=true so a cleared field genuinely clears the
    // persisted override.
    qbitWebhookOverrideInput: '',
    deleteQbitBusy: false,
    // (legacy single-Discord state was here — replaced by multi-agent)
    // Notification agents (multi-provider). Each entry is one Discord/
    // Gotify/NTFY/Pushover/Apprise config with its own credentials and
    // event toggles. Loaded from /api/notifications/agents on settings
    // section open; mutations go through the dedicated CRUD endpoints
    // so masked-credential round-trips work correctly per the
    // notification-agents-pattern.md spec.
    agents: [],
    agentsLoading: false,
    agentsLoadError: '',
    agentBusyId: null,            // agent id with an in-flight enable-toggle
    // Per-agent test state for the inline status indicator next to the
    // Test button. Map: { agentId: { testing: bool, results: [...] } }.
    // Populated by testSavedAgent; cleared on next test or page reload.
    agentTestStatus: {},
    showAgentModal: false,
    agentModal: {
      id: '',
      name: '',
      type: 'discord',
      enabled: true,
      events: { onScheduleSuccess: true, onScheduleFailure: true },
      // Functions whitelist — see agents.Agent.Functions backend
      // contract. Empty array = "all functions" (default; matches
      // pre-pivot behaviour). Non-empty = whitelist; THIS agent only
      // renders embed sections for these function constants.
      functions: [],
      config: {},
      busy: false,
      testing: false,
      testPassed: false,
      testResult: '',
      error: '',
    },
    // Display catalog for the agent-edit Functions section. IDs match
    // the WebhookFunction constants in internal/core/webhook_rules.go
    // (`tagReleaseGroups`, `tagAudio`, etc.) — the backend validator
    // rejects anything else. Labels are plain-language; order matches
    // the user-scan path on a Download event (Tag-Q-R headline →
    // Auto-tags → auxiliary actions → qBit-side).
    agentFunctionOptions: [
      { id: 'tagReleaseGroups', label: 'Tag release groups (quality tag)' },
      { id: 'tagAudio',         label: 'Auto-tag: audio (mediaInfo)' },
      { id: 'tagVideo',         label: 'Auto-tag: video (resolution/codec/HDR)' },
      { id: 'tagDvDetail',      label: 'Auto-tag: Dolby Vision detail' },
      { id: 'discover',         label: 'Discover new release groups' },
      { id: 'recover',          label: 'Recover missing release group' },
      { id: 'syncToSecondary',  label: 'Mirror to secondary instance' },
      { id: 'fileDeleteClean',  label: 'Strip managed tags on file delete' },
      { id: 'grabRename',       label: 'Rename torrent in qBittorrent' },
      { id: 'qbitSeTag',        label: 'Tag Episode/Season in qBittorrent (Sonarr)' },
      { id: 'qbitCategoryFix',  label: 'Fix qBittorrent category after import' },
    ],
    deleteAgentTarget: null,
    deleteAgentBusy: false,
    uiScale: '1.1',
    theme: (function() {
      // Wrapped — Safari Private Mode + sandboxed iframes throw on access.
      try { return localStorage.getItem('resolvarr-theme') || 'system'; }
      catch (e) { return 'system'; }
    })(),

    // groups state
    groupsSection: 'active',
    groupTogglingId: null,        // group id currently being mode-toggled (list-inline action)
    deleteGroupTarget: null,      // group pending confirm-delete; null = modal closed
    deleteGroupBusy: false,       // delete request in flight
    groups: [],
    groupsLoadError: '',
    showGroupModal: false,
    groupForm: { id: '', search: '', tag: '', display: '', mode: 'filtered' },
    groupFormError: '',
    groupFormBusy: false,
    // Tag-usage data surfaced as columns in the Active groups table.
    // Map shape: { primary: { 'rg-flux': {id: 7, count: 12}, ... }, secondary: ... }.
    // Keys are group.tag (lowercased label). The tag id is kept alongside
    // count so the count-click drill-down knows which tag to fetch items
    // for via /api/instances/{id}/tag-items?ids=<tagId>. Re-fetched when
    // the sub-tab instance picker changes or after a successful
    // add/edit/delete that could shift counts (lazy invalidation).
    tagsByLabel: { primary: {}, secondary: {} },
    tagCountsLoading: { primary: false, secondary: false },

    // Group-items drill-down modal — opens when the user clicks an
    // In-primary or In-secondary count. groupItemsTarget carries which
    // group + which slot was clicked so we can show "FLUX in radarr4k"
    // in the title. Items are lazy-fetched on open and reset on close.
    showGroupItemsModal: false,
    groupItemsTarget: null,    // { group, slot, instance, tagId, count }
    groupItemsList: [],
    groupItemsLoading: false,
    groupItemsError: '',

    // filters state
    filters: {
      quality: true, maWebDL: true, playWebDL: true,
      audio: true, trueHD: true, trueHDAtmos: true,
      dtsX: true, dtsHDMA: true,
    },

    // Extra tags state (M4). Per-bucket enabled + optional prefix; Sonarr
    // aggregation is persisted server-side but not exposed in UI yet
    // (Radarr-only container). Default prefix is empty — bare-value tags
    // matching TRaSH bash convention (`hdr10`, `1080p`, `truehd`). Users
    // can set a prefix per bucket for namespace (e.g. `media-`).
    // loadConfig() pulls real values on every config refresh via the
    // merge() helper (mutating field-by-field preserves Alpine's
    // x-model reactivity on nested properties — replacing the bucket
    // object wholesale would silently break the toggles).
    // ---- Audio tags (M4 audio split) ---------------------------------
    // Audio-stream auto-tags from mediaInfo. Codec / channels / atmos
    // share one bucket + one prefix because they're conceptually
    // "everything you'd say about the audio stream". Per-section
    // RemoveOrphanedTags applies only to audio labels.
    audioTags: {
      audio: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '', labels: {} },
      removeOrphanedTags: false,
    },
    // Audio vocab — three sub-categories. UI renders separate checkbox
    // groups for clarity but they all share the single Audio bucket
    // toggle + prefix. Loaded from /api/audio-tags response so engine
    // stays the source of truth; defaults here cover the first paint
    // before the API responds.
    audioVocab: {
      codecs:   ['truehd', 'dts-x', 'dts-hd-ma', 'dts-hd-hra', 'dts-es', 'dts', 'eac3', 'ac3', 'aac', 'flac', 'pcm', 'opus'],
      channels: ['7-1', '5-1', '4-0', '2-0', 'mono'],
      flags:    ['atmos'],
    },
    audioTagsRunMode: 'preview',
    showAudioTagsApplyConfirm: false,

    // ---- Video tags (M4 video split) ---------------------------------
    // Video-stream auto-tags from mediaInfo: resolution / codec / HDR.
    // Three buckets each with own toggle + prefix because users
    // commonly want different namespacing or selective emission per
    // category. Base "dv" tag emits from HDR bucket here; DV detail
    // (mel/fel/etc) lives separately in cfg.dvDetail.
    videoTags: {
      resolution: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '', labels: {} },
      codec:      { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '', labels: {} },
      hdr:        { enabled: false, prefix: '', sonarrAggregation: 'strict',        allowedValues: [], selectMode: '', labels: {} },
      removeOrphanedTags: false,
    },
    videoVocab: {
      resolution: ['2160p', '1440p', '1080p', '720p', '480p', 'sd'],
      codec:      ['h265', 'h264', 'av1', '10bit', 'mpeg4', 'mpeg2', 'vc1'],
      hdr:        ['sdr', 'pq', 'hdr10', 'hdr10plus', 'dv'],
    },
    videoTagsRunMode: 'preview',
    showVideoTagsApplyConfirm: false,

    // ---- M4b Dolby Vision detail ------------------------------------
    // Distinct from Extra tags because the underlying flow is slow
    // (ffmpeg + dovi_tool RPU extraction, ~1-3s per file) and gated
    // on opt-in tools install. Lives in its own sub-tab + result slot
    // + run-mode + state. The base "dv" tag still belongs to Extra
    // tags' HDR bucket — this only adds the detail layer
    // (mel/fel/dvprofile8/cm2/cm4).
    dvDetail: { enabled: false, prefix: '', allowedValues: [], selectMode: '', labels: {}, removeOrphanedTags: false },
    // Vocabulary fetched from /api/dv-detail (engine.DvDetailVocabulary
    // is the source of truth — frontend doesn't hardcode the list so
    // a future engine vocab change ships to the UI on the next config
    // GET, no separate frontend release needed).
    dvDetailVocab: [],
    dvDetailRunMode: 'preview',
    // Per-scan cache bypass — checkbox in DV detail Run controls.
    // When true, this scan's request goes out with bypassDvCache=true
    // and the scan handler skips both Cache.Get and Cache.Put. Resets
    // to false in confirmDvDetailApply() so an Apply doesn't silently
    // inherit a Preview's "skip cache" — but stays sticky between
    // consecutive Previews so the user doesn't have to re-tick if they
    // run multiple. Saved rules carry their own bypass via wizard.
    dvBypassCache: false,
    // Tools state — populated by /api/tools/dv/status (resolves
    // ffmpeg + dovi_tool against $PATH; legacy /config/tools/ checked
    // first as fallback). Empty until first poll. As of v0.3.5 tools
    // ship baked into the image (Dockerfile dv-tools stage), so the
    // status should always be installed=true; the "Tools unreachable"
    // UI branch is defensive against a broken image build.
    dvTools: { installed: false },
    // DV scan progress — populated by startDvScanPoll while a scan is
    // running. {running:true, total, processed, extracted, cacheHits,
    // failed, currentTitle}. Null when no scan is running. Drives the
    // progress bar + Cancel button on the DV detail tab.
    dvScanProgress: null,
    _dvScanPollHandle: null,

    // Adhoc-scan history viewer (under the History tab). Populated by
    // /api/scan/history; rule + schedule runs live in their own per-card
    // history modal on the Run mode tab and are excluded from this list.
    scanHistory: [],
    scanHistoryLoading: false,
    scanHistoryFilter: 'all',
    scanHistoryTypes: [
      { action: 'tag',       label: 'Tag quality releases' },
      { action: 'discover',  label: 'Discover' },
      { action: 'recover',   label: 'Recover' },
      { action: 'cleanup',   label: 'Cleanup' },
      { action: 'audiotags', label: 'Tag Audio' },
      { action: 'videotags', label: 'Tag Video' },
      { action: 'dvdetail',  label: 'Tag DV Details' },
      { action: 'plexsync',  label: 'Sync to Plex' },
    ],
    // Apply-now confirmation modal flag for DV detail.
    showDvDetailApplyConfirm: false,
    // DV cache panel state (Library scan → DV detail tab). dvCacheStats
    // mirrors the GET /api/dv-cache/stats response shape: {entryCount,
    // fileSizeBytes, oldestCachedAt, newestCachedAt}. Refreshed on tab
    // landing + after a successful clear. clearingDvCache gates the
    // button to prevent double-clicks during the DELETE round-trip.
    dvCacheStats: null,
    clearingDvCache: false,
    showClearDvCacheConfirm: false,
    // Per-tag drill-down expansion state — shared across audio/video/dv
    // result panels. Keyed by tag label so multiple rows can be open
    // in parallel. Renamed from extraTagRowExpanded; same semantics.
    autoTagRowExpanded: {},

    // logging settings — persisted via /api/config/logging.
    // loggingDebug toggles per-Arr-API-call detail in /config/logs/runs.log.
    // loggingKeepDays (1–90, default 14) drives the rotation prune.
    // loggingPath is read-only — server-resolved file location for display.
    loggingDebug: false,
    loggingKeepDays: 14,
    loggingPath: '',
    loggingError: '',

    // tags state
    // tagsAppType drives the App-type pill picker on Tag inventory. The
    // instance dropdown is filtered to only show instances of this type,
    // and switching type auto-picks the first matching instance (or clears
    // the selection if none exists). Persisted separately from the chosen
    // instance so the user's last app-type sticks even when instances come
    // and go.
    tagsAppType: 'radarr',
    tagsInstanceId: '',
    tags: [],
    tagsLoading: false,
    tagsLoadError: '',
    tagsSelected: new Set(),
    tagsBusy: false,
    tagsSort: 'usage',   // 'label' | 'usage'
    tagsSortDir: 'desc', // 'asc' | 'desc'
    // Per-tag drill-down state. Each is a {tagId: ...} map so multiple tags
    // can be expanded simultaneously without trampling each other. Items are
    // lazy-loaded per tag — clicking expand fetches once and caches the
    // result; collapsing keeps the cache so a re-expand is instant.
    tagExpanded: {},     // {tagId: true}
    tagItems: {},        // {tagId: [{id, title, year}, ...]}
    tagItemsLoading: {}, // {tagId: true} while fetching
    tagItemsError: {},   // {tagId: 'msg'} on failure

    // Compare-tags panel — driven by the existing tag-list checkboxes.
    // The "Compare selected" toolbar button is enabled only when exactly
    // two tags are selected; clicking it toggles the result panel.
    // Selection changes (toggling a third tag, deselecting one, switching
    // instances) auto-close the panel so results never go stale.
    compareOpen: false,
    compareLoading: false,
    compareError: '',
    // compareResults shape stays the same for both same-instance and
    // cross-instance compares so the rendering layer doesn't branch:
    //   { a: {label, instanceName, items}, b: {label, instanceName, items},
    //     both: [], onlyA: [], onlyB: [], crossInstance: bool, joinKey: 'tmdbId'|'tvdbId'|'id' }
    compareResults: null,
    compareExpanded: { both: false, onlyA: false, onlyB: false },
    // compareCrossInstanceTarget — when set to an instance ID, the
    // Compare button switches to cross-instance mode: the single
    // selected tag is matched by name on the target instance, and
    // results are joined on tmdbId (Radarr) or tvdbId (Sonarr).
    // Empty → same-instance flow (the original 2-tag pair behaviour).
    compareCrossInstanceTarget: '',

    // rename tag modal
    showRenameModal: false,
    renameTarget: { id: 0, label: '', usageCount: 0 },
    renameNewLabel: '',
    renameKeepOldDefinition: false,
    renamePreview: [],          // [{id, title}] of items affected
    renamePreviewLoading: false,
    renameError: '',
    renameBusy: false,

    // tag search — DSL-driven query of items by tag combinations.
    // Cache: server returns the full {tags, items} payload on first query;
    // subsequent queries evaluate locally without another round-trip.
    // Cache is keyed by tagsInstanceId so app-type / instance switches
    // invalidate cleanly. Reload button on the search bar busts the cache.
    tagSearchQuery: '',
    tagSearchCache: {},          // { instanceId: { tags: [...], items: [...] } }
    tagSearchCacheVersion: 0,    // bumped on every cache write — drives memo invalidation
    tagSearchLoading: false,
    tagSearchError: '',
    tagSearchParseError: '',     // populated when DSL parse fails
    tagSearchExpanded: {},       // { itemId: bool } for per-row drill-in (search results)
    tagItemExpanded: {},         // { itemId: bool } for per-row drill-in (tag inventory drill-down — same shape, different view)
    // Memoisation for tagSearchResults — the function is hot-pathed by the
    // template (called inside x-for chip loops). Without memo, every keystroke
    // triggers N×M full-library scans where M is chips-per-row. With memo,
    // identical (query, instanceId, cacheVersion) tuples reuse the result.
    _tagSearchResultsKey: '',
    _tagSearchResultsValue: null,

    // batch rename modal — applies a prefix / suffix / find-replace template
    // across every selected tag. Sequential calls to /tags/rename, one per
    // affected row. Backend handler already covers the merge case.
    showBatchRenameModal: false,
    batchRenameTargets: [],     // [{id, label, usageCount}]
    batchRenameMode: 'suffix',  // 'prefix' | 'suffix' | 'replace'
    batchRenamePrefix: '',
    batchRenameSuffix: '',
    batchRenameFind: '',
    batchRenameReplace: '',
    batchRenameKeepOldDefinition: false,
    batchRenameBusy: false,
    batchRenameError: '',
    batchRenameProgress: '',

    // delete tag modal (single or bulk)
    showDeleteModal: false,
    deleteTargets: [],           // [{id, label, usageCount}]
    deleteKeepDefinition: false,
    deletePreviewGroups: [],     // [{tagId, label, items: [{id, title}]}]
    deletePreviewLoading: false,
    deleteError: '',
    deleteProgress: '',
    deleteBusy: false,

    // schedule state (M3d) — saved cron-driven workflows fired by the
    // backend Scheduler. Modal is preset-driven: pick "Daily at a chosen
    // time" + 03:00 → cron string composed via applySchedulePreset(); a
    // small client-side cron evaluator drives the Next-5-fires preview.
    schedulesLoading: false,
    schedules: [],
    // Webhook rules — saved on /api/webhook-rules CRUD + the
    // unified rule editor's webhook-mode save flow. Loaded eagerly
    // when the Webhooks page mounts; refreshed after every save +
    // delete + toggle-enabled.
    webhookRules: [],
    // Webhook rule history modal — opened from the per-rule History
    // button on the Setup tab. Reads off the rule's already-loaded
    // History[] (no separate API call); Refresh button re-runs
    // loadWebhookRules() to pick up fires that arrived while the
    // modal was open.
    webhookRuleHistoryOpen: false,
    webhookRuleHistoryRule: null,
    webhookRuleHistoryRefreshing: false,

    // M-per-rule-webhook Slice 5 — per-rule webhook URL modal state.
    perRuleWebhookOpen: false,
    perRuleWebhookRule: null,        // the rule object (kept for name display)
    perRuleWebhookData: null,        // server response { token, secret, requireSignature, url }
    perRuleWebhookLoading: false,
    perRuleWebhookActionInFlight: false,
    perRuleWebhookShowCurl: false,
    perRuleWebhookConfirmDisable: false, // true when user clicks Disable → confirm step
    // Expanded state for the collapsible per-run cards in the rule
    // history modal. Keyed by `run.startedAt + ':' + idx` (same shape
    // as the x-for :key). Reset on modal close so re-opens default to
    // all-collapsed.
    webhookRuleHistoryExpanded: {},
    // qBit S/E backlog scan modal — opened from the per-rule "Backlog
    // scan" button on rules carrying the qbitSeTag function. The modal
    // walks three phases: initial (Run preview) → preview loaded
    // (per-row checkboxes + Apply) → apply complete (results summary).
    // State is reset every open; preview + apply responses persist
    // across phase 2/3 so the user can flip back to the preview list
    // if they want.
    qbitSeBacklogOpen: false,
    qbitSeBacklogRule: null,
    qbitSeBacklogCategoryFilter: '',
    qbitSeBacklogLoading: false,         // preview pass in flight
    qbitSeBacklogApplying: false,        // apply pass in flight
    qbitSeBacklogPreview: null,          // last preview response
    qbitSeBacklogApplyResult: null,      // last apply response (null until apply runs)
    qbitSeBacklogSelected: {},           // {hash: bool} — per-row checkbox state
    qbitSeBacklogFilter: 'taggable',     // 'all' | 'taggable' | 'alreadyOk' | 'skipped'
    qbitSeBacklogError: '',              // top-level error banner shown above the table
    // Per-Arr-type filter on the Webhooks page. Mirrors scanAppType
    // / tagsAppType — pills at the page top let the user flip
    // context, instance card list filters down, app-type-irrelevant
    // controls hide.
    webhookAppType: 'radarr',
    schedulesError: '',
    scheduleBusyId: null,         // schedule id with run-now / delete in flight
    deleteScheduleTarget: null,   // schedule pending confirm-delete; null = modal closed
    // Background poll for cron-fired runs. While the user is on the
    // Scan -> Run sub-tab we re-fetch /api/schedules every ~30s so a
    // schedule that fires while the page is open updates the table
    // without a manual Reload click. lastSeenScheduleRuns maps
    // schedule.id -> last seen history.startedAt, used to detect
    // "this run is new since the previous poll" so we can toast
    // exactly once per fire (no spam on every poll cycle).
    schedulePollHandle: null,
    lastSeenScheduleRuns: {},
    // History modal — opened from the per-row clock icon. Shows the
    // last N (5 in-memory cap, see scheduler.go maxInMemoryHistory)
    // runs as a clickable list. selectedHistoryRunIdx is the index
    // currently expanded into the detail panel; null = list view only.
    historyTarget: null,
    selectedHistoryRunIdx: null,
    // historyResultLoading + historyResultError surface state for the
    // "View per-movie details" button on the history modal. The button
    // fetches the persisted scan response, hydrates scanResults.tag (or
    // recoverResults / scanResults.discover, depending on the schedule's
    // mode), closes the history modal, and shows a "Historical run:"
    // banner above the result so the user knows it's not live data.
    historyResultLoading: false,
    historyResultError: '',
    // historicalRunInfo, when non-null, is what triggers the "Historical
    // run" banner above the Tag/Recover result blocks. Cleared by
    // dismissTagResults / dismissRecoverResults / clearScanResultsForInstanceChange
    // / any fresh scan, so the banner can never persist past results
    // it doesn't apply to.
    historicalRunInfo: null,         // { kind: 'tag'|'recover'|'discover'|'audiotags'|'videotags'|'dvdetail'|'cleanup', source: 'schedule'|'adhoc', scheduleName?, startedAt }
    schedulePresets: [
      { id: 'hourly',      label: 'Every hour' },
      { id: 'every-6h',    label: 'Every 6 hours' },
      { id: 'every-12h',   label: 'Every 12 hours' },
      { id: 'daily',       label: 'Daily at a chosen time' },
      { id: 'twice-daily', label: 'Twice daily (00:00 + 12:00)' },
      { id: 'weekly',      label: 'Weekly on a chosen day/time' },
      { id: 'monthly',     label: 'Monthly on a chosen day/time' },
      { id: 'custom',      label: 'Custom cron expression' },
    ],
    // Self-contained schedule rules ("rule editor"). The modal serves
    // two flows from one shape: Wizard (Create — linear Steps 1..N) and
    // Tabbed (Edit — jump freely between sections). Both share the same
    // editingRule shape and the same Save path.
    //
    // editingRule is a deep copy of the in-progress ScheduledJob; it
    // carries its own FilterConfig snapshot, ExtraTagsConfig snapshot,
    // and a subset-by-ID into the global cfg.ReleaseGroups list. The
    // Library scan UI keeps using globals — this modal never reads or
    // writes them. That's the whole point of the rule model: changing
    // a global filter doesn't perturb already-saved schedules.
    ruleEditor: {
      open: false,
      isCreate: false,        // true → wizard mode, false → tabbed-edit
      isQuickFix: false,      // true → wizard with no name/cron/save (one-shot dispatcher)
      step: 0,                // wizard step index 0..N-1
      activeTab: 'basics',    // tabbed-edit active section
      // Rule kind — drives save target + visible steps + Basics step
      // content. 'schedule' is the default (Tag Library / QFA / Create
      // Rule wizards all open with kind='schedule'). 'webhook' is set
      // by the Webhooks page +Add rule entry point — the editor then
      // saves to /api/webhook-rules instead of /api/schedules and
      // shows function-checkboxes on Basics instead of cron+combinedModes.
      kind: 'schedule',
      // Locked Arr-type for this wizard session — set on open() from
      // scanAppType (Create / QFA) or the existing rule's instance type
      // (Edit). Drives the Primary instance dropdown filter and the
      // mode-catalog filter so the wizard can only ever produce a rule
      // matching the type the user picked in the page header.
      appType: 'radarr',
      busy: false,
      error: '',
      cronError: '',
      nextFires: [],
    },
    editingRule: null,
    // Mid-chain abort flag — flipped by cancelRunningChain(), reset
    // at the top of runQuickFixChain. Watched by the loop's
    // isCancelled() so subsequent phases skip after a Cancel click.
    chainCancelRequested: false,
    // Tab metadata. RG/Filters/Extra-tags visibility is computed from
    // the rule's mode + combinedModes — see ruleEditorTabVisible().
    //
    // Order matters: Filters comes BEFORE Release Groups so the user
    // decides "filtered or simple mode" first, then picks which groups
    // apply. Engine semantics make Filters-off-equals-simple a natural
    // rule-level switch (CheckQuality/CheckAudio short-circuit when
    // their master is off), so users can stop worrying about per-group
    // mode within a rule.
    ruleEditorTabs: [
      { id: 'basics',   label: 'Basics' },
      { id: 'filters',  label: 'Filters' },
      { id: 'rg',       label: 'Release Groups' },
      // Audio / Video / DV detail tabs are visible only when the
      // rule's mode (or combined-mode list) includes that section's
      // phases. Each one mirrors its dedicated standalone fane.
      { id: 'audio',    label: 'Audio tags' },
      { id: 'video',    label: 'Video tags' },
      { id: 'dvdetail', label: 'DV detail' },
      // Missing episodes + TBA refresh — Sonarr-only phases, each their
      // own page like Audio/Video/DV. Gated by ruleEditorTabVisible.
      { id: 'missingepisodes', label: 'Missing episodes' },
      { id: 'tbarefresh',      label: 'TBA refresh' },
      // Plex sync — schedule/QFA rules only (gated by ruleEditorTabVisible
      // = !isWebhook && ruleAffectsPlexSync). Edit flow navigates by tabs,
      // so without this the dedicated step is unreachable when editing a
      // saved schedule that includes the phase.
      { id: 'plexsync', label: 'Sync to Plex' },
      // Webhook-only tabs — visible only for webhook rules that have
      // the corresponding function ticked. ruleEditorTabVisible() gates
      // these via ruleAffectsGrabRename / ruleAffectsQbitSe.
      { id: 'grabrename', label: 'qBit Grab Rename' },
      { id: 'qbitse',     label: 'qBit S/E tag' },
      { id: 'qbitcategoryfix', label: 'qBit category fix' },
      { id: 'plexlabelsync', label: 'Plex label sync' },
      // Schedule tab is visible only when the rule fires on a cron
      // (not Manual run only). Hidden in quickfix mode entirely.
      { id: 'schedule', label: 'Schedule' },
    ],
    // Quickfix run results — combined summary of all phases that
    // fired, rendered in a dedicated panel below the schedule list.
    // Stays around until the user dismisses it or fires another run.
    quickFixResults: null,
    // Run-mode result panel — mirrors quickFixResults shape
    // ({startedAt, phases, ok, scheduleName, error}) but populated by
    // schedule Run-now completion + history-detail clicks instead of
    // a wizard run. Renders inline on Run mode so the user stays
    // where they are after firing/inspecting a schedule. Variable
    // name is `activityResults` for legacy reasons (the panel lived
    // on the old Activity tab pre-Step-5 — internal-only, scheduled
    // for rename in a later cleanup pass).
    activityResults: null,
    // Run-now completion poll handle — set when a Run-now click is
    // waiting for the schedule's history to grow. Cleared when the
    // result lands (or when polling times out).
    activityRunPoll: null,
    // Per-phase detail-modal state. Hydrated from quickFixResults
    // when the user clicks a phase row; isolated from the standalone
    // scanResults.* slots so opening a QFA detail doesn't disturb
    // whatever live scan happened to be open. qfaDetail is the
    // "which modal is open" toggle.
    qfaDetail: null,             // 'audio' | 'video' | 'dv' | null
    qfaDetailAudio: null,        // scan_auto_tags response for an audio phase row
    qfaDetailVideo: null,        // scan_auto_tags response for a video phase row
    qfaDetailDv: null,           // scan_dv_detail response for a DV phase row
    // qfaDetailVariants holds per-instance variants when the chain
    // ran the active phase on multiple instances (target='both').
    // Each entry: { instanceId, label, response }. When length > 1
    // the modal renders an instance switcher above the body so the
    // user can flip between primary + secondary results without
    // re-running. Empty when only one instance ran.
    qfaDetailVariants: [],
    qfaDetailVariantIdx: 0,
    qfaDetailDvFilter: 'add',    // DV drill-in action chip (add/remove/keep)
    qfaDetailDvStatusFilter: null, // DV drill-in status chip (cached/extracted/failed/tools-missing/null=all)
    qfaDetailDvTagFilter: null,  // {tag, action} narrowing from a clicked breakdown row; null = no tag filter
    qfaDetailDvStatusHelpOpen: false, // collapse state for "what do these mean?" explainer next to the status chips
    qfaDetailExpanded: {},       // per-row expand state inside the modal
    // Per-tag breakdown collapse — default closed per user request, that
    // table can grow tall on a many-bucket scan and the user usually
    // wants the per-movie list first. Toggled by the breakdown header.
    qfaDetailBreakdownOpen: false,
    // Tag-filter narrows the per-movie list to only items whose decisions
    // include the chosen (bucket, tag) pair. Set by clicking a row in
    // the per-tag breakdown table; cleared via the "Clear" affordance
    // above the per-movie list. Null = no tag filter (show all).
    qfaDetailAutoTagFilter: null,
    qfaDetailAutoFilter: 'add',          // shared audio/video drill-in chip
    // Wizard step order. 'review' is the final-confirmation step; the
    // others map 1:1 onto ruleEditorTabs and inherit the same
    // visibility rules.
    //
    // 'grabrename' + 'qbitse' webhook-only steps land between dvdetail
    // and schedule. Visibility (ruleEditorTabVisible) gates them off
    // for schedule rules entirely, and for webhook rules they only
    // surface when the matching fn flag is ticked on Basics.
    ruleEditorSteps: ['basics', 'filters', 'rg', 'audio', 'video', 'dvdetail', 'missingepisodes', 'tbarefresh', 'plexsync', 'grabrename', 'qbitse', 'qbitcategoryfix', 'plexlabelsync', 'schedule', 'review'],

    // instance UI state
    instStatus: {},
    instVersion: {},
    instError: {},
    showInstModal: false,
    instForm: { id: '', name: '', type: 'radarr', iconVariant: 'standard', url: '', apiKey: '', pathMappings: [] },
    // Path-mappings editor expander — collapsed by default so the
    // common case (aligned mounts; no mappings needed) doesn't see
    // the noise. Click the header to expand.
    instFormPathMappingsOpen: false,
    instFormError: '',
    instFormTesting: false,
    instFormTestResult: '',
    instFormTestedOK: false,  // last Test in modal succeeded with matching url/apiKey
    instFormTestedKey: '',    // url+apiKey combo that was tested — invalidates on edit
    instFormTestedVersion: '', // server version from the successful Test (preserved to save)
    pollHandle: null,

    // Security panel state. securityForm holds the dirty edit copy of
    // auth-policy fields; securityFormDirty flips on first @input/@change
    // and gates the Save button. authStatus comes from /api/auth/status
    // and tells us which fields are env-locked. API key + password-change
    // surfaces are independent of the auth-policy form.
    securityForm: {
      authentication: 'forms',
      authenticationRequired: 'disabled_for_local_addresses',
      trustedNetworks: '',
      trustedProxies: '',
      sessionTtlDays: 30,
    },
    securityFormDirty: false,
    securitySaving: false,
    securitySaveMsg: '',
    securitySaveOk: false,
    authStatus: { trustedNetworksLocked: false, trustedProxiesLocked: false },
    securityApiKey: '',
    securityApiKeyVisible: false,
    securityApiKeyCopied: false,
    securityRegenConfirm: false,
    securityRegenerating: false,
    pwChange: { current: '', next: '', confirm: '' },
    pwChangeSaving: false,
    pwChangeMsg: '',
    pwChangeOk: false,

    // toast
    toasts: [], _toastSeq: 0,

    // ---- Custom confirm dialog (replaces browser window.confirm) ----
    //
    // The browser-native confirm() is jarring against the styled UI
    // and blocks the event loop. Single shared modal driven by this
    // state — call this.confirmDialog({...}) from anywhere; it
    // returns a Promise<boolean> that resolves true on Confirm,
    // false on Cancel / Escape / backdrop-click on a non-destructive
    // dialog.
    confirmModal: {
      open: false,
      title: '',
      message: '',
      confirmText: 'Confirm',
      cancelText: 'Cancel',
      kind: 'default',     // 'default' | 'danger' | 'warning'
      _resolve: null,
    },

    // apiFetch wraps window.fetch to handle two cross-cutting concerns:
    //
    // (1) CSRF — attach X-CSRF-Token header to every same-origin API call.
    //     Required on POST / PUT / DELETE; safely a no-op on GET (backend
    //     middleware skips GETs anyway).
    //
    // (2) 401 → /login redirect — if the backend returns 401 (session
    //     expired, logged out elsewhere), bounce the user to /login so
    //     they can re-auth. Centralised here so every caller doesn't
    //     need `if (r.status === 401) ...`. A never-resolving promise is
    //     returned on redirect so callers don't try to .json() a body
    //     that won't arrive before navigation completes.
    //
    //     Skip the redirect when:
    //       - Already on /login or /setup (avoid loop — those pages probe
    //         /api/auth/status as a normal public endpoint).
    //       - Caller opts out via X-Skip-Login-Redirect header. The only
    //         legitimate use is the disable-auth modal, where 401 means
    //         "confirm_password incorrect" not "session expired".
    //
    // The backend returns text/plain error bodies for CSRF / auth failures
    // (not JSON). apiFetch returns the raw Response exactly like fetch —
    // callers decide parsing — but callers that blindly r.json() on a 4xx
    // will throw. Guard with resp.ok before parsing.
    async apiFetch(url, opts) {
      opts = opts || {};
      const headers = new Headers(opts.headers || {});
      const m = document.cookie.match(/(?:^|; )resolvarr_csrf=([^;]+)/);
      if (m) headers.set('X-CSRF-Token', decodeURIComponent(m[1]));
      const skipLoginRedirect = headers.get('X-Skip-Login-Redirect') === '1';
      // Client-side hint only — strip before sending to server.
      headers.delete('X-Skip-Login-Redirect');
      opts.headers = headers;
      const resp = await fetch(url, opts);
      if (resp.status === 401 && !skipLoginRedirect) {
        const path = window.location.pathname;
        if (path !== '/login' && path !== '/setup') {
          window.location.href = '/login';
          return new Promise(() => {});
        }
      }
      return resp;
    },

    async init() {
      // Apply theme on init and listen for system-pref changes when theme=system.
      // The FOUC-prevention <script> in index.html already set data-theme before
      // first paint; this re-applies it once Alpine state exists so setTheme()
      // works reactively from the Settings UI.
      this.applyTheme();
      try {
        matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
          if (this.theme === 'system') this.applyTheme();
        });
      } catch (e) { /* older browsers without addEventListener on MediaQueryList */ }
      // Cross-tab sync: when another tab changes the theme, mirror it here.
      window.addEventListener('storage', (e) => {
        if (e.key === 'resolvarr-theme' && e.newValue && e.newValue !== this.theme) {
          this.theme = e.newValue;
          this.applyTheme();
        }
      });

      // Restore last-visited page and settings subsection from localStorage so
      // refreshes land where the user left off. Validated against known values
      // to guard against stale keys from older versions.
      const savedPage = localStorage.getItem('resolvarr-page');
      if (['scan','tags','settings'].includes(savedPage)) this.currentPage = savedPage;
      // Migrate stale 'groups' → 'scan' with groups sub-tab active.
      if (savedPage === 'groups') {
        this.currentPage = 'scan';
        this.scanSection = 'groups';
        localStorage.setItem('resolvarr-page', 'scan');
        localStorage.setItem('resolvarr-scan-section', 'groups');
      }
      const savedSection = localStorage.getItem('resolvarr-settings-section');
      if (['instances','notifications','display','about'].includes(savedSection)) this.section = savedSection;
      const savedScanSection = localStorage.getItem('resolvarr-scan-section');
      // M4 split: legacy 'extra' (the old Extra tags fane) folds into
      // 'video' as the sensible default. 'dvdetail' has been a stable
      // section since M4b — accept directly. Audio lives on its own
      // 'audio' fane.
      if (savedScanSection === 'extra') {
        this.scanSection = 'video';
        localStorage.setItem('resolvarr-scan-section', 'video');
      } else if (savedScanSection === 'activity') {
        // Step-5 follow-up rename: 'Activity' tab → 'History'. Migrate
        // existing testers' persisted value so their next page-load
        // lands them on the renamed (same) tab.
        this.scanSection = 'history';
        localStorage.setItem('resolvarr-scan-section', 'history');
      } else if (savedScanSection === 'tag' || savedScanSection === 'recover' || savedScanSection === 'filters') {
        // 2026-05-05 restructure folded standalone Tag library / Recover /
        // Filters sub-tabs into one 'groups' (Tag quality releases) tab.
        // Testers persisting any of those stale ids would land on a
        // hidden section with no nav back. Migrate forward.
        this.scanSection = 'groups';
        localStorage.setItem('resolvarr-scan-section', 'groups');
      } else if (['run','groups','audio','video','dvdetail','history'].includes(savedScanSection)) {
        this.scanSection = savedScanSection;
      }
      const savedGroupsSection = localStorage.getItem('resolvarr-groups-section');
      // 'filters' was briefly persisted as a groups-sidebar item before
      // the 2026-05-05 fold. Same forward-migration target as above —
      // route legacy testers to 'groups' so the page renders.
      if (savedGroupsSection === 'filters') {
        this.scanSection = 'groups';
        this.groupsSection = 'active';
        localStorage.setItem('resolvarr-scan-section', 'groups');
        localStorage.setItem('resolvarr-groups-section', 'active');
      } else if (['active','discovered'].includes(savedGroupsSection)) {
        this.groupsSection = savedGroupsSection;
      }
      const savedTagsInstance = localStorage.getItem('resolvarr-tags-instance');
      if (savedTagsInstance) this.tagsInstanceId = savedTagsInstance;
      const savedTagsAppType = localStorage.getItem('resolvarr-tags-app-type');
      if (savedTagsAppType === 'radarr' || savedTagsAppType === 'sonarr') {
        this.tagsAppType = savedTagsAppType;
      }
      const savedScanAppType = localStorage.getItem('resolvarr-scan-app-type');
      if (savedScanAppType === 'radarr' || savedScanAppType === 'sonarr') {
        this.scanAppType = savedScanAppType;
      }
      const savedWebhookAppType = localStorage.getItem('resolvarr-webhook-app-type');
      if (savedWebhookAppType === 'radarr' || savedWebhookAppType === 'sonarr') {
        this.webhookAppType = savedWebhookAppType;
      }

      // Hash-routing: restore from URL hash (deep-link / refresh) AFTER
      // localStorage so the hash wins when present. Empty hash falls
      // through to localStorage state. Listener wired below for browser
      // back/forward + sibling-tab navigation.
      if (location.hash) {
        this.restoreFromHash(location.hash);
      } else {
        // Seed the hash with current state so the URL matches what's
        // visible from the very first render. Without this the first
        // user click writes hash + adds a history entry; better to
        // anchor the initial entry to the current page.
        const initial = this.buildNavHash();
        if (initial && initial !== '#') history.replaceState(null, '', initial);
      }
      window.addEventListener('hashchange', () => {
        this.restoreFromHash(location.hash);
      });

      await this.loadConfig();
      this.applyUIScale();
      try {
        const r = await this.apiFetch('/api/version');
        const d = await r.json();
        this.version = d.version || 'dev';
        if (d.timezone) this.serverTimezone = d.timezone;
        if (d.locale)   this.serverLocale = d.locale;
      } catch {}
      // If the saved tags-instance no longer exists, drop it.
      if (this.tagsInstanceId && !this.instances.find(i => i.id === this.tagsInstanceId)) {
        this.tagsInstanceId = '';
        localStorage.removeItem('resolvarr-tags-instance');
      }
      // If the saved tags-instance is of a different type than the saved
      // app-type, the app-type wins (user explicitly clicked Radarr/Sonarr
      // last) and the instance gets re-picked from the matching pool.
      const savedInst = this.instances.find(i => i.id === this.tagsInstanceId);
      if (savedInst && savedInst.type !== this.tagsAppType) {
        this.tagsInstanceId = '';
      }
      // Auto-pick the first instance of the active app-type if none chosen.
      if (!this.tagsInstanceId) {
        const first = this.instances.find(i => i.type === this.tagsAppType);
        if (first) {
          this.tagsInstanceId = first.id;
          localStorage.setItem('resolvarr-tags-instance', first.id);
        }
      }
      // Reconcile scanAppType against the live instances list. If the
      // saved app-type has no matching instance (e.g. user deleted the
      // last Radarr after a scanAppType=radarr session), flip to the
      // other type when one is available so the page lands somewhere
      // usable. Same logic protects sonarr-only deployments — initScan
      // below seeds scanInstanceId from scanAvailableInstances() which
      // is filtered by scanAppType.
      if (!this.scanAppTypeAvailable(this.scanAppType)) {
        const other = this.scanAppType === 'radarr' ? 'sonarr' : 'radarr';
        if (this.scanAppTypeAvailable(other)) {
          this.scanAppType = other;
          localStorage.setItem('resolvarr-scan-app-type', other);
        }
      }
      // If we landed on the Tags page, load its data now.
      if (this.currentPage === 'tags' && this.tagsInstanceId) this.loadTags();
      // If we landed on Scan tab, run initial setup. Groups data loads only when the Groups sub-tab is active.
      if (this.currentPage === 'scan') {
        this.initScan();
        if (this.scanSection === 'groups') this.loadGroups();
        // Direct-hash landing on the Plex-sync sub-tab — load the Plex
        // server list so the one-off run form's picker + pre-flight
        // banner render with current data.
        if (this.scanSection === 'plex-sync') {
          this.loadPlexInstances();
        }
      }
      // Load notification agents when landing on Settings → Notifications.
      if (this.currentPage === 'settings' && this.section === 'notifications') {
        this.loadAgents();
      }
      // Same lazy-load for the Security panel.
      if (this.currentPage === 'settings' && this.section === 'security') {
        this.loadSecurityPanel();
      }
      // Same lazy-load for the Plex panel — direct-hash landings on
      // #settings/plex hit here.
      if (this.currentPage === 'settings' && this.section === 'plex') {
        this.loadPlexInstances();
      }
      // Webhooks page mount effects — fired here (post-loadConfig)
      // rather than from restoreFromHash since restoreFromHash runs
      // before instances are populated. Both refresh-on-#webhooks
      // and refresh-with-localStorage-page=webhooks land here with
      // full data context.
      if (this.currentPage === 'webhooks') {
        this.loadWebhookSetupPage();
        this.loadWebhookRules();
        // qBit instances feed the Grab Rename + qBit S/E tag step
        // dropdowns inside the rule editor. Without this load, opening
        // the Webhooks page directly (refresh / bookmark / hash) before
        // visiting Settings → qBit leaves both dropdowns empty and the
        // Next-button gate stuck on "Pick a qBit instance".
        this.loadQbitInstances();
        // Plex instances feed the Plex label sync step's dropdown.
        // Same reasoning as the qBit case above.
        this.loadPlexInstances();
      }
      // Kick off initial status check for all instances, then poll every 60s.
      // Same cadence applies to qBit + Plex so the row pills reflect live
      // state, not the last-clicked Test result.
      this.refreshAllStatus();
      this.refreshAllQbitStatus();
      this.refreshAllPlexStatus();
      this.pollHandle = setInterval(() => {
        this.refreshAllStatus();
        this.refreshAllQbitStatus();
        this.refreshAllPlexStatus();
      }, 60000);
    },

    setCurrentPage(page) {
      this.currentPage = page;
      localStorage.setItem('resolvarr-page', page);
      this.pushNav();
      // Schedules feed both the Run mode rules grid AND the History scan
      // filter chips — load + poll whenever the user is anywhere on the
      // Scan tab. Stop the poll when leaving the Scan tab entirely.
      if (page === 'scan' && (this.scanSection === 'run' || this.scanSection === 'history')) {
        this.loadSchedules();
        this.startSchedulePoll();
      } else {
        this.stopSchedulePoll();
      }
      // Lazy-load webhook configs + recent events the first time the
      // user lands on the Webhooks page. Subsequent visits re-fetch
      // so events captured while the user was elsewhere show up.
      // Leaving the page closes the SSE stream so we don't hold an
      // open EventSource for a tab the user isn't looking at.
      if (page === 'webhooks') {
        this.loadWebhookSetupPage();
        // Webhook rules are listed per-instance on the Setup card;
        // load them alongside the per-instance webhook configs so
        // the rule list renders on first paint.
        this.loadWebhookRules();
        // Mirror init() — qBit instances feed Grab Rename + qBit S/E
        // tag dropdowns. Idempotent (loadQbitInstances just refetches).
        this.loadQbitInstances();
        // Plex instances feed the Plex label sync step's dropdown.
        this.loadPlexInstances();
      } else {
        this.stopWebhookEventStream();
      }
    },

    setSettingsSection(section) {
      this.section = section;
      localStorage.setItem('resolvarr-settings-section', section);
      // Lazy-load notification agents the first time the user lands on
      // the section. Future opens still re-fetch so changes from
      // another tab / Direct API edit are picked up.
      if (section === 'notifications') {
        this.loadAgents();
      }
      if (section === 'security') {
        this.loadSecurityPanel();
      }
      if (section === 'logging') {
        this.loadLogging();
      }
      if (section === 'qbit') {
        this.loadQbitInstances();
      }
      if (section === 'plex') {
        this.loadPlexInstances();
      }
      this.pushNav();
    },

    // ===== Hash routing (back/forward, bookmarks, copyable nav links) =====
    //
    // Hash format:
    //   #scan/<appType>/<scanSection>[/<groupsSection>] — Tag Library
    //   #tags/<tagsAppType>                              — Tag inventory
    //   #lists                                           — Lists (M5 placeholder)
    //   #webhooks                                        — Webhooks
    //   #settings/<section>                              — Settings sub-section
    //
    // setCurrentPage / setScanSection / setSettingsSection / setScanAppType
    // already write to localStorage on every change; pushNav() is just a
    // mirror to location.hash so browser back/forward + right-click "Open
    // in new tab" + bookmarks work. localStorage stays the source of truth
    // for "where was I" on a fresh tab open with no hash; the hash wins
    // when present (handled in init via restoreFromHash).

    buildNavHash() {
      const p = this.currentPage;
      if (p === 'tags')     return '#tags/' + (this.tagsAppType || 'radarr');
      if (p === 'lists')    return '#lists';
      if (p === 'webhooks') return '#webhooks/' + (this.webhookAppType || 'radarr');
      if (p === 'settings') return '#settings/' + (this.section || 'instances');
      // 'scan' (Tag Library) — appType + scan section + optional groups sub.
      const app = this.scanAppType || 'radarr';
      const sec = this.scanSection || 'run';
      let hash = '#scan/' + app + '/' + sec;
      if (sec === 'groups') hash += '/' + (this.groupsSection || 'active');
      return hash;
    },

    // navHref builds the hash a target page/section would produce, without
    // mutating any state. Used by nav anchors so right-click → "Open in new
    // tab" / "Copy link address" work, and the browser shows the URL on
    // hover. opts: { appType, scanSection, groupsSection, settingsSection,
    //                tagsAppType } — each defaults to current state.
    navHref(page, opts = {}) {
      if (page === 'tags')     return '#tags/' + (opts.tagsAppType || this.tagsAppType || 'radarr');
      if (page === 'lists')    return '#lists';
      if (page === 'webhooks') return '#webhooks/' + (opts.webhookAppType || this.webhookAppType || 'radarr');
      if (page === 'settings') return '#settings/' + (opts.settingsSection || this.section || 'instances');
      const app = opts.appType || this.scanAppType || 'radarr';
      const sec = opts.scanSection || this.scanSection || 'run';
      let hash = '#scan/' + app + '/' + sec;
      if (sec === 'groups') hash += '/' + (opts.groupsSection || this.groupsSection || 'active');
      return hash;
    },

    pushNav() {
      if (this._navSkipPush) return;
      const hash = this.buildNavHash();
      // Initial render: location.hash is "" — pushState gives us the
      // canonical hash. Subsequent pushes only fire when the hash
      // actually changes so we don't pile up identical history entries.
      if (location.hash !== hash) {
        history.pushState(null, '', hash);
      }
    },

    restoreFromHash(hash) {
      if (!hash || hash === '#') return false;
      // Loop guard: if the parsed hash already matches current state,
      // skip — pushNav write triggered hashchange triggered restore;
      // bailing here breaks the cycle.
      if (hash === this.buildNavHash()) return true;
      const parts = hash.replace(/^#/, '').split('/').filter(Boolean);
      if (parts.length === 0) return false;
      const page = parts[0];
      const validPages = ['scan', 'tags', 'lists', 'webhooks', 'settings'];
      if (!validPages.includes(page)) return false;
      const validScanSections = ['run', 'groups', 'recover', 'audio', 'video', 'dvdetail', 'history'];
      const validGroupsSections = ['active', 'discovered'];
      const validSettings = ['instances', 'qbit', 'notifications', 'security', 'display', 'logging', 'about'];
      this._navSkipPush = true;
      try {
        if (page === 'scan') {
          this.currentPage = 'scan';
          if (parts[1] === 'radarr' || parts[1] === 'sonarr') this.scanAppType = parts[1];
          if (parts[2] && validScanSections.includes(parts[2])) this.scanSection = parts[2];
          if (this.scanSection === 'groups' && parts[3] && validGroupsSections.includes(parts[3])) {
            this.groupsSection = parts[3];
          }
          // Mirror what setCurrentPage('scan') would have done so the
          // page lands in a coherent state on a fresh tab open with a
          // deep-link hash. Light side-effects only — no extra API
          // hits beyond what setCurrentPage already triggers.
          if (this.scanSection === 'run' || this.scanSection === 'history') {
            this.loadSchedules();
            this.startSchedulePoll();
          }
        } else if (page === 'tags') {
          this.currentPage = 'tags';
          if (parts[1] === 'radarr' || parts[1] === 'sonarr') this.tagsAppType = parts[1];
        } else if (page === 'lists') {
          this.currentPage = 'lists';
        } else if (page === 'webhooks') {
          this.currentPage = 'webhooks';
          if (parts[1] === 'radarr' || parts[1] === 'sonarr') this.webhookAppType = parts[1];
          // Side-effects (loadWebhookSetupPage / loadWebhookRules)
          // deferred to _dispatchPageMountEffects after loadConfig
          // resolves — restoreFromHash fires during init BEFORE
          // this.instances is populated, so iterating it here would
          // wipe webhookConfigs. The post-loadConfig dispatcher
          // re-fires the same side-effects with full data context.
        } else if (page === 'settings') {
          this.currentPage = 'settings';
          if (parts[1] && validSettings.includes(parts[1])) this.section = parts[1];
        }
        // Mirror localStorage so a refresh without the hash also lands
        // back here — both surfaces stay aligned.
        localStorage.setItem('resolvarr-page', this.currentPage);
        if (this.section) localStorage.setItem('resolvarr-settings-section', this.section);
        if (this.scanSection) localStorage.setItem('resolvarr-scan-section', this.scanSection);
        if (this.scanAppType) localStorage.setItem('resolvarr-scan-app-type', this.scanAppType);
        if (this.tagsAppType) localStorage.setItem('resolvarr-tags-app-type', this.tagsAppType);
        if (this.webhookAppType) localStorage.setItem('resolvarr-webhook-app-type', this.webhookAppType);
        if (this.groupsSection) localStorage.setItem('resolvarr-groups-section', this.groupsSection);
        return true;
      } finally {
        this._navSkipPush = false;
      }
    },

    // ===== qBittorrent instances =====
    //
    // Standalone CRUD list. Loaded on Settings → qBit visit, refreshed
    // after each create / update / delete + the on-boot loadConfig
    // (since /api/config also returns the list, masked-passwords).
    // The dedicated /api/qbit-instances endpoint is the source-of-truth
    // for the Settings page; loadConfig's mirror is a fallback for
    // first-render before the dedicated fetch returns.

    async loadQbitInstances() {
      try {
        const r = await this.apiFetch('/api/qbit-instances');
        if (r.ok) {
          const d = await r.json();
          this.qbitInstances = Array.isArray(d) ? d : [];
          // Kick a silent status sweep so freshly-loaded rows don't
          // sit at "Not tested" until the 60s poll catches up. Honors
          // the silent path so a manual click already in flight isn't
          // overwritten.
          this.refreshAllQbitStatus();
        }
      } catch (e) {
        // Silent — page renders empty state until next refresh.
      }
    },

    openQbitInstanceModal(qi) {
      // qi is the existing instance (edit) or undefined (create).
      // Password placeholder for edit shows '••••••••' so the user
      // knows leaving it blank preserves the stored value.
      this.qbitInstanceModal = {
        open: true,
        id: qi ? qi.id : '',
        name: qi ? qi.name : '',
        url: qi ? qi.url : '',
        username: qi ? (qi.username || '') : '',
        password: '', // never pre-populate — masked on edit, blank on create
        trustedCerts: !!(qi && qi.trustedCerts),
        busy: false,
        testing: false,
        testResult: '',
        testOk: false,
      };
    },

    closeQbitInstanceModal() {
      if (this.qbitInstanceModal.busy) return;
      this.qbitInstanceModal.open = false;
    },

    // testQbitInstanceModal hits the inline-creds endpoint that
    // probes against whatever's currently in the modal — lets the
    // user verify creds BEFORE saving. Distinct from
    // testQbitInstance() which probes against the SAVED creds for
    // a row in the list.
    async testQbitInstanceModal() {
      const m = this.qbitInstanceModal;
      if (!m.url) return;
      m.testing = true;
      m.testResult = '';
      try {
        const r = await this.apiFetch('/api/qbit-instances/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: m.id, // empty on create — backend ignores; populated on edit so the masked-password path can pull stored creds
            url: m.url,
            username: m.username,
            password: m.password,
            trustedCerts: m.trustedCerts,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        m.testOk = !!d.ok;
        m.testResult = d.ok ? (d.message || 'Connected') : ('Failed: ' + (d.error || 'unknown'));
        // Bridge into the row-status map when editing a saved instance,
        // so closing the modal doesn't leave the row pill stuck at "Not
        // tested" right after the user verified it works.
        if (m.id) {
          this.qbitStatus[m.id] = d.ok ? 'connected' : 'failed';
          this.qbitError[m.id] = d.ok ? '' : (d.error || '');
        }
      } catch (e) {
        m.testOk = false;
        m.testResult = 'Failed: ' + e.message;
        if (m.id) {
          this.qbitStatus[m.id] = 'failed';
          this.qbitError[m.id] = e.message;
        }
      } finally {
        m.testing = false;
      }
    },

    async saveQbitInstanceModal() {
      const m = this.qbitInstanceModal;
      if (!m.name || !m.url) return;
      m.busy = true;
      try {
        const body = {
          name: m.name,
          url: m.url,
          username: m.username,
          password: m.password,
          trustedCerts: m.trustedCerts,
        };
        const path = m.id ? '/api/qbit-instances/' + m.id : '/api/qbit-instances';
        const method = m.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadQbitInstances();
        this.qbitInstanceModal.open = false;
        this.showToast('qBit instance saved', 'success');
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    // testQbitInstance — same shape as testInstance (Arr): probes the
    // saved-creds endpoint, writes flat status/error into qbitStatus +
    // qbitError. silent=true skips the "testing" pill flash so the
    // 60s background refresh doesn't strobe every row.
    async testQbitInstance(id, silent = false) {
      if (!silent) this.qbitStatus[id] = 'testing';
      this.qbitError[id] = '';
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + id + '/test', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitStatus[id] = d.ok ? 'connected' : 'failed';
        this.qbitError[id] = d.ok ? '' : (d.error || '');
      } catch (e) {
        this.qbitStatus[id] = 'failed';
        this.qbitError[id] = e.message;
      }
    },

    // Mirror refreshAllStatus for qBit. Skip rows already mid-Test so
    // a manual click doesn't get clobbered by the silent background
    // sweep.
    async refreshAllQbitStatus() {
      for (const qi of (this.qbitInstances || [])) {
        if (this.qbitStatus[qi.id] === 'testing') continue;
        this.testQbitInstance(qi.id, true);
      }
    },

    confirmDeleteQbitInstance(qi) {
      this.deleteQbitTarget = qi;
    },

    async deleteQbitInstance() {
      const target = this.deleteQbitTarget;
      if (!target) return;
      this.deleteQbitBusy = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + target.id, {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadQbitInstances();
        this.deleteQbitTarget = null;
        this.showToast('qBit instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteQbitBusy = false;
      }
    },

    // ---- Plex instances (label-sync Settings tab) ----------------
    // Same CRUD shape as the qBit handlers above; just operate on a
    // different endpoint family. Token preservation mirrors qBit's
    // password preservation — empty / masked input on edit keeps the
    // stored value.

    async loadPlexInstances() {
      try {
        const r = await this.apiFetch('/api/plex-instances');
        if (r.ok) {
          const d = await r.json();
          this.plexInstances = Array.isArray(d) ? d : [];
          this.refreshAllPlexStatus();
        }
      } catch (e) {
        // Silent — page renders empty state until next refresh.
      }
    },

    openPlexInstanceModal(pi) {
      this.plexInstanceModal = {
        open: true,
        id: pi ? pi.id : '',
        name: pi ? pi.name : '',
        url: pi ? pi.url : '',
        token: '', // never pre-populate — masked on edit, blank on create
        trustedCerts: !!(pi && pi.trustedCerts),
        busy: false,
        testing: false,
        testResult: '',
        testOk: false,
      };
    },

    closePlexInstanceModal() {
      if (this.plexInstanceModal.busy) return;
      this.plexInstanceModal.open = false;
    },

    // testPlexInstanceModal — inline-creds probe used before save so
    // the user can verify URL + token without committing. Distinct
    // from testPlexInstance() which probes saved creds for a row.
    async testPlexInstanceModal() {
      const m = this.plexInstanceModal;
      if (!m.url) return;
      m.testing = true;
      m.testResult = '';
      try {
        const r = await this.apiFetch('/api/plex-instances/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: m.id, // empty on create — backend ignores; populated on edit so masked-token path can pull stored creds
            url: m.url,
            token: m.token,
            trustedCerts: m.trustedCerts,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        m.testOk = !!d.ok;
        m.testResult = d.ok ? (d.message || 'Connected') : ('Failed: ' + (d.error || 'unknown'));
        // Bridge into the row-status map so closing the modal doesn't
        // leave the row pill stuck at "Not tested".
        if (m.id) {
          this.plexStatus[m.id] = d.ok ? 'connected' : 'failed';
          this.plexError[m.id] = d.ok ? '' : (d.error || '');
        }
      } catch (e) {
        m.testOk = false;
        m.testResult = 'Failed: ' + e.message;
        if (m.id) {
          this.plexStatus[m.id] = 'failed';
          this.plexError[m.id] = e.message;
        }
      } finally {
        m.testing = false;
      }
    },

    async savePlexInstanceModal() {
      const m = this.plexInstanceModal;
      if (!m.name || !m.url) return;
      m.busy = true;
      try {
        const body = {
          name: m.name,
          url: m.url,
          token: m.token,
          trustedCerts: m.trustedCerts,
        };
        const path = m.id ? '/api/plex-instances/' + m.id : '/api/plex-instances';
        const method = m.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadPlexInstances();
        this.plexInstanceModal.open = false;
        this.showToast('Plex instance saved', 'success');
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    async testPlexInstance(id, silent = false) {
      if (!silent) this.plexStatus[id] = 'testing';
      this.plexError[id] = '';
      try {
        const r = await this.apiFetch('/api/plex-instances/' + id + '/test', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.plexStatus[id] = d.ok ? 'connected' : 'failed';
        this.plexError[id] = d.ok ? '' : (d.error || '');
      } catch (e) {
        this.plexStatus[id] = 'failed';
        this.plexError[id] = e.message;
      }
    },

    async refreshAllPlexStatus() {
      for (const pi of (this.plexInstances || [])) {
        if (this.plexStatus[pi.id] === 'testing') continue;
        this.testPlexInstance(pi.id, true);
      }
    },

    confirmDeletePlexInstance(pi) {
      this.deletePlexTarget = pi;
    },

    async deletePlexInstance() {
      const target = this.deletePlexTarget;
      if (!target) return;
      this.deletePlexBusy = true;
      try {
        const r = await this.apiFetch('/api/plex-instances/' + target.id, {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadPlexInstances();
        this.deletePlexTarget = null;
        this.showToast('Plex instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deletePlexBusy = false;
      }
    },

    // fetchPlexLibraries hits the per-instance refresh endpoint, which
    // calls Plex's /library/sections and persists the result back to
    // PlexInstance.Libraries. On success the in-memory list is reloaded
    // so the row's library-count pill updates without a manual refresh.
    async fetchPlexLibraries(id) {
      this.plexLibrariesBusy[id] = true;
      try {
        const r = await this.apiFetch('/api/plex-instances/' + id + '/fetch-libraries', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok || d.ok === false) {
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        await this.loadPlexInstances();
        const count = (d.libraries || []).length;
        this.showToast(count + ' libr' + (count === 1 ? 'y' : 'ies') + ' fetched', 'success');
      } catch (e) {
        this.showToast('Fetch libraries failed: ' + e.message, 'error');
      } finally {
        this.plexLibrariesBusy[id] = false;
      }
    },

    // ---- Plex label-sync rules (Library scan → Plex sync sub-tab) -----
    // plexLabelRuleAvailableInstances filters the Add/Edit modal's
    // Arr-instance dropdown to instances matching the page-level
    // scanAppType. Defensive default: when scanAppType is unset
    // (rare) returns the full list.
    plexLabelRuleAvailableInstances() {
      const want = this.scanAppType;
      if (!want) return this.instances || [];
      return (this.instances || []).filter(i => i.type === want);
    },

    openPlexLabelRuleModal(rule) {
      const editing = !!rule;
      // Pull AppType from the linked instance so the library picker
      // can filter immediately, without waiting for the user to
      // re-pick the instance dropdown.
      let appType = '';
      if (editing) {
        appType = rule.appType || '';
      }
      this.plexLabelRuleModal = {
        open: true,
        id: editing ? rule.id : '',
        name: editing ? rule.name : '',
        enabled: editing ? !!rule.enabled : true,
        instanceId: editing ? rule.instanceId : '',
        appType: appType,
        labels: editing ? [...(rule.labels || [])] : [],
        labelDisplay: editing && rule.labelDisplay ? { ...rule.labelDisplay } : {},
        plexInstanceId: editing && rule.targets && rule.targets[0] ? rule.targets[0].plexInstanceId : '',
        libraryKeys: editing && rule.targets && rule.targets[0] ? [...(rule.targets[0].libraryKeys || [])] : [],
        runMode: editing && rule.runMode ? rule.runMode : 'apply',
        // Empty array treated as ["label"] by the backend for
        // backward compatibility — surfaced explicitly here so the
        // UI checkboxes show the selected state on edit.
        targetTypes: (editing && Array.isArray(rule.targetTypes) && rule.targetTypes.length > 0)
          ? [...rule.targetTypes]
          : ['label'],
        busy: false,
      };
      // Pre-fetch the picked Arr's tag list so the checkbox picker has
      // data immediately. Edit flow has instanceId from the start;
      // Add flow fires the fetch later via onInstanceChange.
      this.plexLabelRuleAvailableTags = [];
      this.plexLabelRuleTagsError = '';
      this.plexLabelRuleTagFilter = '';
      if (this.plexLabelRuleModal.instanceId) {
        this.plexLabelRuleLoadAvailableTags();
      }
    },

    // plexLabelRuleFilteredAvailableTags applies the search filter to
    // the available-tags list. Multi-term OR: whitespace splits the
    // query into terms, a tag matches when it contains ANY term — so
    // "fel mel" surfaces both `fel` and `mel` in one search instead of
    // forcing a search-select-clear-search loop. Empty filter returns
    // the full list. Used by the one-off form AND the webhook step.
    plexLabelRuleFilteredAvailableTags() {
      return this.filterTagsByTerms(this.plexLabelRuleAvailableTags, this.plexLabelRuleTagFilter);
    },

    // filterTagsByTerms — shared multi-term OR filter for the Plex
    // tag-pickers (one-off form, webhook step, wizard step). Splits the
    // query on whitespace; a tag matches when its label contains ANY
    // term. "fel mel" → both `fel` and `mel`. Empty query = full list.
    filterTagsByTerms(list, query) {
      const terms = (query || '').trim().toLowerCase().split(/\s+/).filter(Boolean);
      if (terms.length === 0) return list;
      return (list || []).filter(t => {
        const lbl = (t.label || '').toLowerCase();
        return terms.some(term => lbl.includes(term));
      });
    },

    // plexLabelRuleLoadAvailableTags fetches the live tag list from the
    // currently-picked Arr instance (uses the same /api/instances/{id}/tags
    // endpoint that Tag inventory / Compare use). Drops on AppType
    // change so the picker always reflects the current Arr's tag set.
    async plexLabelRuleLoadAvailableTags() {
      const m = this.plexLabelRuleModal;
      if (!m.instanceId) {
        this.plexLabelRuleAvailableTags = [];
        return;
      }
      this.plexLabelRuleTagsLoading = true;
      this.plexLabelRuleTagsError = '';
      try {
        const r = await this.apiFetch('/api/instances/' + m.instanceId + '/tags');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const list = await r.json();
        // Normalise + sort alphabetically so the checkbox order is
        // predictable across page-reloads.
        const out = (Array.isArray(list) ? list : [])
          .map(t => ({ label: (t.label || '').trim(), usageCount: t.usageCount || 0 }))
          .filter(t => t.label);
        out.sort((a, b) => a.label.localeCompare(b.label));
        this.plexLabelRuleAvailableTags = out;
      } catch (e) {
        this.plexLabelRuleTagsError = e.message;
        this.plexLabelRuleAvailableTags = [];
      } finally {
        this.plexLabelRuleTagsLoading = false;
      }
    },

    // onPlexLabelRuleInstanceChange — fires when the user picks a
    // different Arr instance from the dropdown. Refreshes AppType +
    // tag list; resets library selection (since the new instance's
    // type may differ and library-keys would no longer match).
    onPlexLabelRuleInstanceChange() {
      const m = this.plexLabelRuleModal;
      const inst = (this.instances || []).find(i => i.id === m.instanceId);
      m.appType = inst ? inst.type : '';
      // Library keys are appType-gated; force re-pick on Arr change.
      m.libraryKeys = [];
      this.plexLabelRuleLoadAvailableTags();
    },

    // plexLabelRuleHasLabel — case-insensitive membership check for the
    // checkbox-row's :checked binding. Lets the user see at a glance
    // which available tags are already part of the rule's whitelist.
    plexLabelRuleHasLabel(label) {
      return (this.plexLabelRuleModal.labels || []).some(l => l.toLowerCase() === label.toLowerCase());
    },

    // plexLabelRuleToggleAvailableTag — checkbox click handler. Adds
    // or removes the tag from the rule's labels list, preserving the
    // exact case as stored on the Arr. Drops the matching
    // labelDisplay entry on untick so an unchecked tag doesn't leave
    // a stale override hanging around (backend would drop it on save
    // anyway, but keeping in-modal state tidy avoids confusion if
    // the user re-ticks the tag and expects a fresh slate).
    plexLabelRuleToggleAvailableTag(label) {
      const m = this.plexLabelRuleModal;
      const idx = m.labels.findIndex(l => l.toLowerCase() === label.toLowerCase());
      if (idx === -1) {
        m.labels.push(label);
      } else {
        const removed = m.labels[idx];
        m.labels.splice(idx, 1);
        if (m.labelDisplay && removed in m.labelDisplay) {
          delete m.labelDisplay[removed];
        }
      }
    },

    // plexLabelRuleSetDisplay — write a per-tag override into the
    // labelDisplay map. Empty value deletes the entry so JSON
    // round-trips cleanly (the backend treats empty + missing as
    // equivalent, but we keep client state tidy).
    plexLabelRuleSetDisplay(arrTag, value) {
      const m = this.plexLabelRuleModal;
      if (!m.labelDisplay) m.labelDisplay = {};
      const trimmed = (value || '').trim();
      if (!trimmed || trimmed === arrTag) {
        delete m.labelDisplay[arrTag];
      } else {
        m.labelDisplay[arrTag] = trimmed;
      }
    },

    // plexLabelRuleEffectiveLabel — what the engine will write to Plex
    // for a given Arr tag. Drives the chip-strip badge + the rule-
    // card "Manages:" line so the user can see at a glance what the
    // Plex side will look like.
    plexLabelRuleEffectiveLabel(arrTag, displayMap) {
      const m = displayMap || this.plexLabelRuleModal.labelDisplay || {};
      const v = m[arrTag];
      if (v && v.trim() && v.trim() !== arrTag) return v.trim();
      return arrTag;
    },

    // plexLabelRuleIsCustomLabel — returns true when a label on the
    // rule is NOT in the current Arr's tag list. Either the user typed
    // it manually (preempt-config for a tag they'll create later), or
    // the Arr tag was deleted after the rule was saved. Either way,
    // it's surfaced in the UI with a "not in Arr" hint so the user
    // knows the rule won't match anything until the tag exists.
    plexLabelRuleIsCustomLabel(label) {
      if (this.plexLabelRuleTagsLoading) return false;
      if (!this.plexLabelRuleAvailableTags || this.plexLabelRuleAvailableTags.length === 0) return false;
      return !this.plexLabelRuleAvailableTags.some(t => t.label.toLowerCase() === label.toLowerCase());
    },

    closePlexLabelRuleModal() {
      if (this.plexLabelRuleModal.busy) return;
      this.plexLabelRuleModal.open = false;
    },

    // togglePlexLabelRuleLibraryKey — multi-select checkbox handler.
    // Keeps libraryKeys as a clean string array (no dupes).
    togglePlexLabelRuleLibraryKey(key) {
      const m = this.plexLabelRuleModal;
      const idx = m.libraryKeys.indexOf(key);
      if (idx === -1) {
        m.libraryKeys.push(key);
      } else {
        m.libraryKeys.splice(idx, 1);
      }
    },

    // plexLabelRuleSelectedPlexLibraries — list of libraries on the
    // currently-picked Plex instance, filtered by Arr type (Radarr →
    // movie libs, Sonarr → show libs). Returns the FULL list (no type
    // filter) when AppType isn't picked yet so the user can see what
    // would appear once they finish the form.
    plexLabelRuleSelectedPlexLibraries() {
      const m = this.plexLabelRuleModal;
      if (!m.plexInstanceId) return [];
      const pi = (this.plexInstances || []).find(p => p.id === m.plexInstanceId);
      if (!pi) return [];
      return pi.libraries || [];
    },

    // plexLabelRuleVisibleLibraryCount — count of libraries matching
    // the rule's app-type (drives the "no movie libs available"
    // empty-state message inside the picker).
    plexLabelRuleVisibleLibraryCount() {
      const libs = this.plexLabelRuleSelectedPlexLibraries();
      const want = this.plexLabelRuleWantedLibraryType();
      if (!want) return libs.length;
      return libs.filter(l => l.type === want).length;
    },

    plexLabelRuleWantedLibraryType() {
      // AppType comes from the linked Arr instance. Derive on the fly
      // when the modal hasn't pre-populated it yet (Add-flow before the
      // user picks the Arr).
      const m = this.plexLabelRuleModal;
      let appType = m.appType;
      if (!appType && m.instanceId) {
        const inst = (this.instances || []).find(i => i.id === m.instanceId);
        if (inst) appType = inst.type;
      }
      if (appType === 'radarr') return 'movie';
      if (appType === 'sonarr') return 'show';
      return '';
    },

    plexLabelRuleNoLibrariesMessage() {
      const want = this.plexLabelRuleWantedLibraryType();
      if (!want) return 'Pick an Arr instance above so the library picker can filter to the matching type.';
      if (want === 'movie') return 'No movie libraries cached on this Plex instance. Add a Movies library in Plex, then click Fetch libraries on the Plex row in Settings.';
      return 'No show libraries cached on this Plex instance. Add a TV Shows library in Plex, then click Fetch libraries on the Plex row in Settings.';
    },

    plexLabelRuleLibraryHint() {
      const want = this.plexLabelRuleWantedLibraryType();
      if (!want) return 'Pick the Plex libraries this rule will sync labels into. Multi-select supported.';
      if (want === 'movie') return 'Only movie libraries shown — Radarr instances can only manage labels on movie libraries.';
      return 'Only show libraries shown — Sonarr instances can only manage labels on show libraries.';
    },

    // plexLabelRuleModalValid — UI-side gate for the Save button. Mirrors
    // the backend validator's core checks (server still re-validates).
    plexLabelRuleModalValid() {
      const m = this.plexLabelRuleModal;
      // No name requirement — this is a one-off run form, not a saved
      // rule. Persistence lives on Schedule / Webhook only.
      if (!m.instanceId) return false;
      if (!m.labels || m.labels.length === 0) return false;
      if (!m.plexInstanceId) return false;
      if (!m.libraryKeys || m.libraryKeys.length === 0) return false;
      if (m.runMode !== 'apply' && m.runMode !== 'preview') return false;
      if (!m.targetTypes || m.targetTypes.length === 0) return false;
      return true;
    },

    // plexLabelRuleToggleTargetType — checkbox click handler for the
    // Plex-target picker. Toggles "label" / "collection" on the
    // targetTypes array; preserves order so the UI's read-back of
    // selected state is stable.
    plexLabelRuleToggleTargetType(t) {
      const m = this.plexLabelRuleModal;
      const idx = m.targetTypes.indexOf(t);
      if (idx === -1) {
        m.targetTypes.push(t);
      } else {
        m.targetTypes.splice(idx, 1);
      }
    },

    plexLabelRuleHasTargetType(t) {
      return (this.plexLabelRuleModal.targetTypes || []).includes(t);
    },

    // describePlexLabelRuleTargets — one-line summary for the rule card.
    // "Radarr (Main) → Main Plex / Movies + Movies 4K"
    describePlexLabelRuleTargets(rule) {
      const inst = (this.instances || []).find(i => i.id === rule.instanceId);
      const arrLabel = inst ? inst.name : '(unknown Arr)';
      if (!rule.targets || rule.targets.length === 0) return arrLabel + ' → (no target)';
      const tgt = rule.targets[0];
      const plex = (this.plexInstances || []).find(p => p.id === tgt.plexInstanceId);
      const plexLabel = plex ? plex.name : '(unknown Plex)';
      // Map library keys to titles via the cached library list on the
      // Plex instance. Falls back to the key if the cache hasn't been
      // refreshed.
      const libs = plex ? (plex.libraries || []) : [];
      const libNames = (tgt.libraryKeys || []).map(k => {
        const l = libs.find(x => x.key === k);
        return l ? l.title : k;
      });
      return arrLabel + ' → ' + plexLabel + ' / ' + libNames.join(' + ');
    },

    // ---- One-off Plex label sync run ------------------------------
    // Builds the inline PlexLabelSyncConfig from the form state and
    // POSTs it to /api/plex-sync/run — no saved rule, nothing
    // persisted (matches every other Tag Library sub-tab). On success
    // the form modal closes and the result is shown in the run modal's
    // result stage via a synthetic rule (the run-modal markup is
    // wrapped in x-if="rule").
    async runOneOffPlexSync() {
      const m = this.plexLabelRuleModal;
      if (!this.plexLabelRuleModalValid()) return;
      m.busy = true;
      try {
        // Per-tag display overrides — drop empty / identity / orphan
        // entries client-side (backend re-cleans, but a tidy body
        // mirrors what runs).
        const labelDisplay = {};
        for (const k of Object.keys(m.labelDisplay || {})) {
          const v = (m.labelDisplay[k] || '').trim();
          if (!v || v === k) continue;
          if (!m.labels.some(l => l === k)) continue;
          labelDisplay[k] = v;
        }
        const body = {
          arrInstanceId: m.instanceId,
          runMode: m.runMode || 'apply',
          plexLabelSync: {
            plexInstanceId: m.plexInstanceId,
            libraryKeys: m.libraryKeys,
            labels: m.labels,
            labelDisplay: labelDisplay,
            targetTypes: (m.targetTypes && m.targetTypes.length > 0) ? m.targetTypes : ['label'],
          },
        };
        const r = await this.apiFetch('/api/plex-sync/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Close the form, surface the result in the run modal. The run
        // modal's markup is wrapped in x-if="rule", so we hand it a
        // synthetic rule shaped just enough for the header +
        // describePlexLabelRuleTargets() (name + instanceId + first
        // target). No persistence — this object lives only for the
        // result view.
        const syntheticRule = {
          name: 'Plex label sync',
          instanceId: m.instanceId,
          targetTypes: body.plexLabelSync.targetTypes,
          labelDisplay: body.plexLabelSync.labelDisplay,
          targets: [{
            plexInstanceId: m.plexInstanceId,
            libraryKeys: m.libraryKeys,
          }],
        };
        m.open = false;
        this.plexLabelRunModal = {
          open: true,
          stage: 'result',
          rule: syntheticRule,
          runMode: d.runMode || (m.runMode || 'apply'),
          result: d,
          error: '',
          detailsFilter: '',
        };
      } catch (e) {
        this.showToast('Plex sync failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    // The run modal is opened directly in the result stage by
    // runOneOffPlexSync + the QFA result drill-in (viewPhaseDetails).
    closePlexLabelRunModal() {
      this.plexLabelRunModal.open = false;
    },

    // Helpers for the result modal — keep the template tidy by
    // hoisting the count math out of x-text expressions.
    plexLabelRunAddedTotal(result) {
      if (!result || !result.added) return 0;
      return Object.values(result.added).reduce((a, b) => a + b, 0);
    },

    plexLabelRunRemovedTotal(result) {
      if (!result || !result.removed) return 0;
      return Object.values(result.removed).reduce((a, b) => a + b, 0);
    },

    plexLabelRunInSyncTotal(result) {
      if (!result || !result.inSync) return 0;
      return Object.values(result.inSync).reduce((a, b) => a + b, 0);
    },

    // plexLabelRunLabelSummary — flattens Added / Removed / InSync
    // maps into one sorted per-label table for the result modal. Each
    // entry: { label, added, removed, inSync }. Includes labels that
    // appear in ANY of the three maps so the user sees all four
    // numbers per label even when one bucket is zero.
    plexLabelRunLabelSummary(result) {
      if (!result) return [];
      const labels = new Set();
      for (const k of Object.keys(result.added || {})) labels.add(k);
      for (const k of Object.keys(result.removed || {})) labels.add(k);
      for (const k of Object.keys(result.inSync || {})) labels.add(k);
      const out = [];
      for (const lbl of labels) {
        out.push({
          label: lbl,
          added: (result.added && result.added[lbl]) || 0,
          removed: (result.removed && result.removed[lbl]) || 0,
          inSync: (result.inSync && result.inSync[lbl]) || 0,
        });
      }
      out.sort((a, b) => a.label.localeCompare(b.label));
      return out;
    },

    // plexLabelRunFilteredPerLabel — applies the detailsFilter (label
    // name) to the per-item change list so the user can drill into a
    // single label at a time.
    plexLabelRunFilteredPerLabel(result) {
      if (!result || !result.perLabel) return [];
      const filter = (this.plexLabelRunModal.detailsFilter || '').trim().toLowerCase();
      if (!filter) return result.perLabel;
      return result.perLabel.filter(c => (c.label || '').toLowerCase() === filter);
    },

    // Status pill colour mapping — same legend the engine returns:
    //   "ok"      every library + every item synced cleanly
    //   "partial" something went wrong on a subset (per-item errors)
    //   "error"   couldn't fire at all (Plex unreachable, missing
    //             instance, etc.)
    plexLabelRunStatusStyle(status) {
      if (status === 'ok')      return 'background:var(--alpha-green);color:var(--accent-green)';
      if (status === 'partial') return 'background:var(--alpha-orange,rgba(255,165,0,0.15));color:var(--accent-orange,#ff9800)';
      if (status === 'error')   return 'background:var(--accent-red-bg);color:var(--accent-red)';
      return 'background:var(--bg-muted);color:var(--text-muted)';
    },

    // ---- qBit webhook hook (M-qBit-add Slice 5) -------------------
    //
    // Per-instance modal showing the curl ready to paste into qBit's
    // "Run external program on torrent added" field, plus auto-
    // configure / rotate / test / reset helpers backed by the Slice 4
    // endpoints. State indicator at the top distinguishes:
    //   - Not configured        — qBit autorun field is empty
    //   - Configured by us      — our path prefix is in the field
    //   - Third-party content   — non-empty + not ours (Configure
    //                             prompts for Append vs Replace)
    //   - qBit unreachable      — fetchError surfaced; manual paste
    //                             still viable

    openQbitWebhookModal(qi) {
      this.qbitWebhookInstance = qi;
      this.qbitWebhookData = null;
      this.qbitWebhookShowCurl = false;
      this.qbitWebhookTestResult = null;
      this.qbitWebhookConflictMode = 'append';
      this.qbitWebhookConflictOpen = false;
      this.qbitWebhookOverrideInput = '';
      this.qbitWebhookOpen = true;
      this.loadQbitWebhookConfig();
    },

    closeQbitWebhookModal() {
      if (this.qbitWebhookActionInFlight) return;
      this.qbitWebhookOpen = false;
      this.qbitWebhookInstance = null;
      this.qbitWebhookData = null;
      this.qbitWebhookShowCurl = false;
      this.qbitWebhookTestResult = null;
      this.qbitWebhookConflictOpen = false;
      this.qbitWebhookOverrideInput = '';
    },

    async loadQbitWebhookConfig() {
      if (!this.qbitWebhookInstance) return;
      this.qbitWebhookLoading = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitWebhookData = d;
        // Hydrate the editable override from the persisted value.
        // Empty = use the browser-detected default.
        this.qbitWebhookOverrideInput = d.webhookCallbackUrl || '';
      } catch (e) {
        this.showToast('Load webhook config failed: ' + e.message, 'error');
        this.qbitWebhookData = null;
      } finally {
        this.qbitWebhookLoading = false;
      }
    },

    // qbitWebhookOverrideInvalid — client-side mirror of the backend
    // validateQbitWebhookCallbackURL contract. Empty is valid (clears
    // override). Non-empty must parse, be http(s), have a host, and
    // carry no path/query/fragment.
    qbitWebhookOverrideInvalid() {
      const v = (this.qbitWebhookOverrideInput || '').trim();
      if (v === '') return false;
      try {
        const u = new URL(v);
        if (u.protocol !== 'http:' && u.protocol !== 'https:') return true;
        if (!u.host) return true;
        if (u.pathname !== '/' && u.pathname !== '') return true;
        if (u.search || u.hash) return true;
      } catch (_) {
        return true;
      }
      return false;
    },

    // qbitWebhookResolvedURL returns the URL qBit will actually call
    // back on given the current override input + the backend-supplied
    // default. Used by the curl-preview + the hint text.
    qbitWebhookResolvedURL() {
      const d = this.qbitWebhookData;
      if (!d) return '';
      const v = (this.qbitWebhookOverrideInput || '').trim().replace(/\/+$/, '');
      if (v === '' || this.qbitWebhookOverrideInvalid()) {
        return d.defaultWebhookUrl || d.webhookUrl || '';
      }
      const id = (this.qbitWebhookInstance || {}).id || '';
      return v + '/api/qbit/torrent-added/' + id;
    },

    // qbitWebhookCurlPreview rebuilds the curl client-side from the
    // current override + the secret loaded from the backend. Mirrors
    // buildQbitCurlCommand format so what the user sees is exactly
    // what gets written into qBit's autorun.
    qbitWebhookCurlPreview() {
      const d = this.qbitWebhookData;
      if (!d) return '';
      const url = this.qbitWebhookResolvedURL();
      const secret = d.secret || '';
      // Quoting matches Go's %q semantics (the backend's renderer).
      const q = (s) => '"' + String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
      return 'curl -fsS -X POST ' + q(url) + ' -H ' + q('X-API-Key: ' + secret) +
             ' --data-urlencode "infoHash=%I" --data-urlencode "name=%N" --data-urlencode "category=%L"';
    },

    // qbitWebhookStateLabel derives a human-friendly status string +
    // colour from the loaded config + qBit fetch state. Used in the
    // modal header.
    qbitWebhookStateLabel() {
      const d = this.qbitWebhookData;
      if (!d) return { text: 'Loading…', color: 'var(--text-muted)' };
      const st = d.qbitState || {};
      if (st.fetchError) {
        return { text: 'qBit unreachable — manual paste only', color: 'var(--accent-orange)' };
      }
      if (st.configuredByUs) {
        return { text: 'Configured (resolvarr is wired into qBit)', color: 'var(--accent-green)' };
      }
      if (st.thirdPartyContent) {
        return { text: 'qBit autorun has third-party content — Configure will prompt', color: 'var(--accent-orange)' };
      }
      return { text: 'Not configured (qBit autorun is empty)', color: 'var(--text-secondary)' };
    },

    // Configure click — if qBit has third-party content, opens the
    // conflict resolution sub-flow. Otherwise fires append directly
    // (semantics for empty + already-ours are the same regardless
    // of mode, so 'append' is safe).
    onQbitConfigureClick() {
      const st = (this.qbitWebhookData || {}).qbitState || {};
      if (st.thirdPartyContent && !st.fetchError) {
        this.qbitWebhookConflictMode = 'append';
        this.qbitWebhookConflictOpen = true;
        return;
      }
      this.doConfigureQbitWebhook('append');
    },

    async doConfigureQbitWebhook(mode) {
      if (!this.qbitWebhookInstance) return;
      if (this.qbitWebhookOverrideInvalid()) {
        this.showToast('Fix the Resolvarr URL — must be http:// or https:// with a host, no path', 'error');
        return;
      }
      this.qbitWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/configure', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            mode,
            // Always send hasOverride=true so a blank field genuinely
            // clears the persisted value (vs "field not sent").
            callbackUrlOverride: (this.qbitWebhookOverrideInput || '').trim(),
            hasOverride: true,
          }),
        });
        const d = await r.json();
        if (!r.ok) {
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.qbitWebhookConflictOpen = false;
        this.showToast('qBit autorun configured — hook should fire on next add', 'success');
        await this.loadQbitWebhookConfig();
      } catch (e) {
        // Surface the qBit-side error verbatim (often "qui blocked
        // this") so user knows manual paste is the workaround.
        this.showToast('Configure failed: ' + e.message + ' — try Show command for manual paste', 'error');
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    async doRotateQbitWebhookSecret() {
      if (!this.qbitWebhookInstance) return;
      if (!await this.confirmDialog({
        title:       'Rotate the webhook secret?',
        message:     'The old secret stops working immediately. If qBit was auto-configured, the new secret is pushed there too — but if that push fails (qui block / network), you will need to manually update qBit\'s autorun field with the new curl.',
        confirmText: 'Rotate',
        kind:        'warning',
      })) return;
      this.qbitWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        if (d.qbitOutOfSync) {
          this.showToast('Secret rotated locally, but qBit update failed: ' + (d.qbitUpdateError || 'unknown') + ' — re-run Configure to fix qBit', 'error');
        } else {
          this.showToast('Secret rotated', 'success');
        }
        await this.loadQbitWebhookConfig();
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    async doTestQbitWebhook() {
      if (!this.qbitWebhookInstance) return;
      this.qbitWebhookActionInFlight = true;
      this.qbitWebhookTestResult = null;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/test', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitWebhookTestResult = { ok: true, message: 'Receiver is wired correctly. (This does NOT verify qBit can reach resolvarr — only an actual qBit add proves end-to-end.)' };
      } catch (e) {
        this.qbitWebhookTestResult = { ok: false, message: 'Test failed: ' + e.message };
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    async doResetQbitWebhook() {
      if (!this.qbitWebhookInstance) return;
      const hadBackup = !!((this.qbitWebhookData || {}).qbitState && this.qbitWebhookInstance.previousAutorunBackup);
      const msg = hadBackup
        ? 'Resolvarr will restore the value qBit had before you clicked Configure, then forget the backup. The hook stops firing.'
        : 'Resolvarr will clear qBit\'s autorun field and disable it (no previous value to restore). The hook stops firing.';
      if (!await this.confirmDialog({
        title:       'Reset qBit autorun?',
        message:     msg,
        confirmText: 'Reset',
        kind:        'warning',
      })) return;
      this.qbitWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/reset', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast(d.hadPreviousBackup ? 'qBit autorun restored to previous value' : 'qBit autorun cleared', 'success');
        await this.loadQbitWebhookConfig();
      } catch (e) {
        this.showToast('Reset failed: ' + e.message, 'error');
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    // qbitWebhookConflictPreview computes the resulting qBit autorun
    // field for the conflict resolution sub-modal. Same join semantics
    // the backend uses ("; " separator on append).
    qbitWebhookConflictPreview() {
      const d = this.qbitWebhookData;
      if (!d || !d.qbitState) return '';
      const current = d.qbitState.currentProgram || '';
      // Use the live-computed preview so the conflict view reflects
      // the user's current Resolvarr URL override — not the stale
      // backend-rendered curl from page-load. Falls back to the
      // backend value if for any reason the preview returns empty.
      const ours = this.qbitWebhookCurlPreview() || d.curlCommand || '';
      if (this.qbitWebhookConflictMode === 'replace') return ours;
      // append (default)
      if (!current) return ours;
      return current + '; ' + ours;
    },

    async copyQbitWebhookSecret() {
      const d = this.qbitWebhookData;
      if (!d) return;
      const ok = await this.copyToClipboard(d.secret || '');
      this.showToast(ok ? 'Secret copied' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    async copyQbitWebhookCurl() {
      const d = this.qbitWebhookData;
      if (!d) return;
      // Use the live-computed preview so the copied curl reflects the
      // user's current override input — not the value persisted at
      // last load.
      const curl = this.qbitWebhookCurlPreview() || d.curlCommand || '';
      const ok = await this.copyToClipboard(curl);
      this.showToast(ok ? 'Curl command copied' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    async loadLogging() {
      try {
        const r = await this.apiFetch('/api/config/logging');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.loggingDebug = !!d.debug;
        this.loggingKeepDays = d.keepDays || 14;
        this.loggingPath = d.logPath || '';
        this.loggingError = '';
      } catch (e) {
        this.loggingError = 'Could not load logging settings: ' + e.message;
      }
    },

    async saveLogging() {
      const keepDays = parseInt(this.loggingKeepDays, 10);
      if (isNaN(keepDays) || keepDays < 1 || keepDays > 90) {
        this.loggingError = 'Keep-days must be between 1 and 90';
        return;
      }
      try {
        const r = await this.apiFetch('/api/config/logging', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ debug: !!this.loggingDebug, keepDays }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.loggingError = '';
        this.showToast('Logging settings saved', 'success');
      } catch (e) {
        this.loggingError = 'Save failed: ' + e.message;
      }
    },

    setScanSection(section) {
      // Migrate legacy section IDs that were folded into 'groups'
      // during the 2026-05-05 restructure (Tag library + Release
      // Groups + Sonarr Recover all live on one tab now). Old
      // localStorage values + any code path that still passes 'tag'
      // / 'recover' lands cleanly on the unified tab instead of a
      // dead sub-tab.
      if (section === 'tag' || section === 'recover') section = 'groups';
      this.scanSection = section;
      localStorage.setItem('resolvarr-scan-section', section);
      // Schedules feed the Run mode rules grid AND the History tab — keep
      // the poll alive whenever the user is on either of those sub-tabs.
      // Scan history only lives on the History tab, so its initial fetch
      // is gated separately.
      if (section === 'run' || section === 'history') {
        this.loadSchedules();
        this.startSchedulePoll();
      } else {
        this.stopSchedulePoll();
      }
      if (section === 'history') {
        this.loadScanHistory();
      }
      if (section === 'dvdetail') {
        // Refresh cache stats on tab landing — covers schedule fires +
        // QFA chains that populated entries while the user was on
        // another tab. loadConfig fires this once on app boot, but
        // background scans can happen any time after.
        this.loadDvCacheStats();
      }
      if (section === 'plex-sync') {
        // Plex server list drives the one-off run form's picker + the
        // pre-flight banner (shown when no Plex server is configured).
        this.loadPlexInstances();
      }
      this.pushNav();
    },

    // ---- Scan history (adhoc dumps) ----

    async loadScanHistory() {
      this.scanHistoryLoading = true;
      try {
        const r = await this.apiFetch('/api/scan/history');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.scanHistory = await r.json() || [];
      } catch (e) {
        this.showToast('Could not load scan history: ' + e.message, 'error');
      } finally {
        this.scanHistoryLoading = false;
      }
    },

    // Per-app-type filter for Run-mode rules + History-tab scans.
    // Schedules and scan history are stored globally but each view
    // scopes to the currently-selected instance type, mirroring the
    // per-instance-type sub-tab visibility model. Sonarr users never
    // see Radarr rules and vice versa.
    schedulesForCurrentApp() {
      const list = this.schedules || [];
      if (!list.length) return list;
      return list.filter(sj => {
        const inst = this.instances.find(i => i.id === sj.instanceId);
        return inst && inst.type === this.scanAppType;
      });
    },

    scanHistoryForCurrentApp() {
      const list = this.scanHistory || [];
      if (!list.length) return list;
      return list.filter(r => {
        // instanceType lands in the dump preview as of the per-app-type
        // restructure. Older dumps without the field fall through to an
        // ID lookup; if neither resolves, drop the row from the
        // current-app view (better than showing cross-type noise).
        if (r.instanceType) return r.instanceType === this.scanAppType;
        if (r.instanceId) {
          const inst = this.instances.find(i => i.id === r.instanceId);
          return inst && inst.type === this.scanAppType;
        }
        return false;
      });
    },

    scanHistoryCountByAction(action) {
      return this.scanHistoryForCurrentApp().filter(r => r.action === action).length;
    },

    scanHistoryFiltered() {
      const scoped = this.scanHistoryForCurrentApp();
      if (!this.scanHistoryFilter || this.scanHistoryFilter === 'all') {
        return scoped;
      }
      return scoped.filter(r => r.action === this.scanHistoryFilter);
    },

    // Returns the user-facing label for the Historical-run banner.
    // Schedule-fired hydration carries the schedule's name; adhoc
    // dumps just show the action + date — no fake "schedule X" wording.
    historicalRunLabel() {
      const h = this.historicalRunInfo;
      if (!h) return '';
      const when = this.formatDate(h.startedAt);
      if (h.source === 'schedule' && h.scheduleName) {
        return 'Replay of "' + h.scheduleName + '" from ' + when;
      }
      const label = this.scanHistoryActionLabel(h.kind || '') || h.kind || 'scan';
      return 'Saved ' + label + ' scan from ' + when;
    },

    // True when the currently-shown historicalRunInfo matches the action
    // the caller is about to fire. Apply buttons use this to disable
    // themselves so the user can't promote a historical preview to a
    // live apply (the change-counts shown in the apply-confirm modal
    // would be from the historical run, not the current Radarr state).
    isHistoricalForAction(action) {
      const h = this.historicalRunInfo;
      return !!(h && h.kind === action);
    },

    scanHistoryActionLabel(action) {
      const t = this.scanHistoryTypes.find(x => x.action === action);
      return t ? t.label : action;
    },

    // Click a row → fetch the dump + auto-pop the matching per-phase
    // detail modal in-place. No tab redirect: the modal is a top-level
    // overlay that renders over whichever sub-tab the user is on, so
    // the History tab stays a pure browser. Each phase has its own
    // modal triggered by its own state slot:
    //   tag/discover/audio/video/dv → viewPhaseDetails dispatcher
    //   recover                     → recoverResults
    //   cleanup                     → cleanupResults
    // historicalRunInfo is set after dispatch so the snapshot banner
    // + Apply-gating activate inside the modal regardless of phase.
    async openScanHistory(row) {
      try {
        const r = await this.apiFetch('/api/scan/history/' + encodeURIComponent(row.file));
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const data = await r.json();
        // Close any other open modal first so the new one doesn't
        // stack behind it. Map row.action to the closeAllResultModals
        // except-key (discover/audio/video/dv have shorter names there).
        const exceptMap = {
          tag: 'tag', discover: 'discover', recover: 'recover', cleanup: 'cleanup',
          audiotags: 'audio', videotags: 'video', dvdetail: 'dv',
        };
        this.closeAllResultModals(exceptMap[row.action] || null);
        // Each action hydrates the right state slot so its modal
        // auto-pops. Tag/Discover/Audio/Video/DV route via
        // viewPhaseDetails which owns the state-set + filter defaults
        // (so we don't duplicate them here). Recover + Cleanup have
        // dedicated modals that read recoverResults / cleanupResults
        // directly — set the slot and return.
        switch (row.action) {
          case 'tag':       this.viewPhaseDetails({ phase: 'tag',       response: data }); break;
          case 'discover':  this.viewPhaseDetails({ phase: 'discover',  response: data }); break;
          case 'audiotags': this.viewPhaseDetails({ phase: 'audiotags', response: data }); break;
          case 'videotags': this.viewPhaseDetails({ phase: 'videotags', response: data }); break;
          case 'dvdetail':  this.viewPhaseDetails({ phase: 'dvdetail',  response: data }); break;
          case 'recover':   this.recoverResults = data; break;
          case 'cleanup':   this.cleanupResults = data; break;
          case 'plexsync':
            // Dump shape: { mode, instance, totals, run }. The run is a
            // PlexLabelRuleRun — route it through the same modal the
            // one-off + QFA drill-in use.
            this.viewPhaseDetails({ phase: 'plexsync', response: (data && data.run) || {}, instanceId: data && data.instance && data.instance.id });
            break;
          default: this.showToast('Unknown action: ' + row.action, 'error'); return;
        }
        // kind enables the Historical-run banner if the user later
        // navigates to the originating sub-tab. The modal itself is
        // independent — clicking from the History tab opens it overlaid.
        this.historicalRunInfo = {
          kind: row.action,
          source: 'adhoc',
          startedAt: row.timestamp,
        };
        this.showToast('Loaded ' + this.scanHistoryActionLabel(row.action) + ' from ' + this.formatDate(row.timestamp), 'success');
      } catch (e) {
        this.showToast('Could not open scan: ' + e.message, 'error');
      }
    },

    // Called when the user clicks into the Scan tab. Seeds scanInstanceId to
    // the first Radarr instance if it's still empty. Does not load anything
    // else — Groups and Filters load on demand when their sub-tab is clicked.
    initScan() {
      // Seed scanInstanceId from the active app-type pool, not hardcoded
      // Radarr — sonarr-only deployments need to land on a Sonarr instance
      // when the saved scanAppType=sonarr. Reuses scanAvailableInstances()
      // so the same filtering applies as the dropdown.
      if (!this.scanInstanceId) {
        const first = this.scanAvailableInstances()[0];
        if (first) this.scanInstanceId = first.id;
      }
      // Initial schedules pull whenever Scan tab opens on Run mode (rules
      // grid lives there) or History (scan-history filter chips). Without
      // this gate, a fresh page-load that lands on Run mode shows an empty
      // rules grid until the user clicks History → Run.
      if (this.scanSection === 'run' || this.scanSection === 'history') {
        this.loadSchedules();
        this.startSchedulePoll();
      }
    },

    // Runs a scan against the chosen instance. Mode='preview' is purely
    // read-only — the backend returns decisions without calling
    // EditorApplyTags. Mode='apply' does the preview pass AND commits the
    // add/remove batches, with lazy tag-label creation.
    // Helper: at least one mode toggled on (gate on Run scan button).
    anyScanModeEnabled() {
      return !!(this.scanModes.tag || this.scanModes.discover || this.scanModes.recover);
    },

    // closeAllResultModals(except) — close every result-modal slot except
    // the one passed in. Call before opening a new modal (Run scan
    // handlers, viewPhaseDetails, openScanHistory) so two modals can't
    // co-exist visually. Without this, running a fresh Audio scan on top
    // of an open Tag preview would leave the Tag modal stacked behind.
    //
    //   except = 'tag' | 'discover' | 'recover' | 'cleanup' | 'audio' | 'video' | 'dv' | null
    //
    // null = close everything (used from clearScanResultsForInstanceChange).
    closeAllResultModals(except) {
      if (except !== 'tag') {
        this.scanResults.tag = null;
        this.scanGroupExpanded = {};
        this.scanRowExpanded = {};
      }
      if (except !== 'discover') {
        this.scanResults.discover = null;
        this.scanDiscoverSelected = {};
        this.scanDiscoverExpanded = {};
      }
      if (except !== 'recover') {
        this.recoverResults = null;
        this.recoverApplySelected = {};
        this.recoverExpanded = {};
        this.recoverSeriesExpanded = {};
        this.recoverSeasonExpanded = {};
      }
      if (except !== 'cleanup') {
        this.cleanupResults = null;
        this.cleanupSelected = {};
      }
      if (except !== 'audio' && except !== 'video' && except !== 'dv') {
        this.qfaDetail = null;
        this.qfaDetailAudio = null;
        this.qfaDetailVideo = null;
        this.qfaDetailDv = null;
        this.qfaDetailExpanded = {};
        this.scanResults.audioTags = null;
        this.scanResults.videoTags = null;
        this.scanResults.dvDetail = null;
      }
      // Variant switcher pills (qfaDetailVariants) live alongside
      // every result modal that opens via viewPhaseDetails — when
      // ANY of those modals is being replaced, the variant set is
      // stale by definition. The next opener (chain runner / single-
      // instance scan) repopulates from its fresh response. Without
      // this clear, opening an audio-history row after a chain Both
      // run would render the OLD pills with the chain's instance
      // labels. Recover dismiss + standalone Recover already clear
      // these on their own paths; this catches every other surface.
      if (except !== 'tag' && except !== 'recover' && except !== 'audio' && except !== 'video' && except !== 'dv') {
        this.qfaDetailVariants = [];
        this.qfaDetailVariantIdx = 0;
      } else if (except === 'audio' || except === 'video' || except === 'dv') {
        // Within the auto-tag modal family, swapping between audio /
        // video / dv views means the variants from the previous view
        // are stale (different phase). Clear unless the new modal
        // re-populates immediately (which the standard flow does).
        // Chain-driven re-opens replace the variants array wholesale
        // anyway, so the clear-then-repopulate is idempotent.
      }
      // historicalRunInfo lives across modals — clear only when the new
      // trigger doesn't itself set it (the caller sets it after, if relevant).
      this.historicalRunInfo = null;
    },

    // Clear every result/error/expanded/selected state tied to a scan against
    // a specific instance. Called on instance-switch so the user never looks
    // at totals or banners from a previous instance while the picker shows
    // a different one. The scan-tab and release-groups-tab cleanup section
    // both share scanInstanceId, so we clear both sides regardless of which
    // sub-tab triggered the change.
    // Clear a single results set on user request (× dismiss button on each
    // result card). Re-running a scan auto-clears via the orchestrator;
    // these are for "I've read the result, hide it now" — leaves the rest
    // of the page state untouched (instance picker, mode, sync toggle, etc.).
    dismissTagResults() {
      this.scanResults.tag = null;
      this.scanGroupExpanded = {};
      this.scanRowExpanded = {};
      this.scanFilter = 'add';
      this.scanInstanceFilter = 'both';
      this.scanError = '';
      // Clear the historical-run banner only when it was bound to tag
      // results. Discover/recover banners on other slots stay if they
      // still have data behind them.
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'tag') {
        this.historicalRunInfo = null;
      }
    },
    dismissRecoverResults() {
      this.recoverResults = null;
      this.recoverApplySelected = {};
      this.recoverExpanded = {};
      this.recoverSeriesExpanded = {};
      this.recoverSeasonExpanded = {};
      this.recoverFilter = 'all';
      this.recoverError = '';
      // Reset cached exclusion list — fresh open against any instance
      // re-fetches via loadRecoverExclusions so stale data from a
      // previous instance can't flash before the GET resolves.
      this.recoverExclusions = { instanceId: '', movies: [], series: [], seasons: [] };
      // Variant switcher state lives in qfaDetailVariants but also serves
      // the Recover modal when target='both' fired two passes. Clear it
      // here so a future single-instance Recover doesn't render a stale
      // switcher with primary+secondary pills from the previous run.
      this.qfaDetailVariants = [];
      this.qfaDetailVariantIdx = 0;
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'recover') {
        this.historicalRunInfo = null;
      }
    },

    // ---- Recover exclusions ----
    //
    // Per-instance "skip these in next scan" list. User flags faulty
    // movies / series / seasons via the Exclude buttons in the
    // result panel. Backend filters them out of the next Recover
    // scan entirely — saves API calls + result-panel space. The
    // "Show excluded" panel surfaces what's currently excluded with
    // an Include-again button per row.

    // Load the exclusion list for the instance the current result
    // belongs to. Called when the result modal opens (so the
    // per-row Exclude buttons can know what's already excluded) and
    // on every mutation so the UI stays in sync without a page reload.
    async loadRecoverExclusions(instanceId) {
      if (!instanceId) {
        this.recoverExclusions = { instanceId: '', movies: [], series: [], seasons: [] };
        return;
      }
      this.recoverExclusionsLoading = true;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instanceId));
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
      } catch (e) {
        // Non-fatal — just keep the empty default. Failure here means
        // the Show-excluded panel won't show pre-existing entries
        // until the user reloads, but exclusion writes still work.
        console.warn('[recover] load exclusions failed:', e);
        this.recoverExclusions = { instanceId, movies: [], series: [], seasons: [] };
      } finally {
        this.recoverExclusionsLoading = false;
      }
    },

    // Three boolean helpers — the wire shape carries title-enriched
    // entries ({id, title, year, ...}) so we walk and match on .id.
    // Linear scans — exclusion lists are typically a handful of entries.
    isMovieExcluded(movieId) {
      for (const m of (this.recoverExclusions.movies || [])) {
        if (m.id === movieId) return true;
      }
      return false;
    },
    isSeriesExcluded(seriesId) {
      for (const s of (this.recoverExclusions.series || [])) {
        if (s.id === seriesId) return true;
      }
      return false;
    },
    isSeasonExcluded(seriesId, seasonNumber) {
      // Whole-series excluded = season counts as excluded too.
      if (this.isSeriesExcluded(seriesId)) return true;
      for (const s of (this.recoverExclusions.seasons || [])) {
        if (s.seriesId === seriesId && s.seasonNumber === seasonNumber) return true;
      }
      return false;
    },

    // Add-to-exclusions wrappers. Each takes the relevant identity,
    // POSTs to the API, and refreshes local state. Toast on success
    // so the user knows the click registered (the result-panel UI
    // already filters the row, but for the Show-excluded section
    // toggle the visual feedback is less obvious).
    async excludeMovie(movieId, title) {
      if (!this.recoverResults || !this.recoverResults.instance) return;
      const instId = this.recoverResults.instance.id;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instId), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ movies: [movieId] }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
        this.showToast('Excluded "' + title + '" — won\'t scan it next time.', 'success');
      } catch (e) {
        this.showToast('Exclude failed: ' + e.message, 'error');
      }
    },
    async excludeSeries(seriesId, title) {
      if (!this.recoverResults || !this.recoverResults.instance) return;
      const instId = this.recoverResults.instance.id;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instId), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ series: [seriesId] }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
        this.showToast('Excluded "' + title + '" — series will be skipped in future scans.', 'success');
      } catch (e) {
        this.showToast('Exclude failed: ' + e.message, 'error');
      }
    },
    async excludeSeason(seriesId, seasonNumber, seriesTitle) {
      if (!this.recoverResults || !this.recoverResults.instance) return;
      const instId = this.recoverResults.instance.id;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instId), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ seasons: [{ seriesId, seasonNumber }] }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
        const lbl = seasonNumber === 0 ? 'Specials' : ('Season ' + seasonNumber);
        this.showToast('Excluded ' + lbl + ' of "' + seriesTitle + '" — will be skipped in future scans.', 'success');
      } catch (e) {
        this.showToast('Exclude failed: ' + e.message, 'error');
      }
    },

    // Remove-from-exclusions wrappers. Same shape as the add ones.
    async includeMovie(movieId, title) {
      if (!this.recoverResults || !this.recoverResults.instance) return;
      const instId = this.recoverResults.instance.id;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instId), {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ movies: [movieId] }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
        this.showToast('Included "' + (title || 'movie') + '" again — back in next scan.', 'success');
      } catch (e) {
        this.showToast('Include failed: ' + e.message, 'error');
      }
    },
    async includeSeries(seriesId, title) {
      if (!this.recoverResults || !this.recoverResults.instance) return;
      const instId = this.recoverResults.instance.id;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instId), {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ series: [seriesId] }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
        this.showToast('Included "' + (title || 'series') + '" again — back in next scan.', 'success');
      } catch (e) {
        this.showToast('Include failed: ' + e.message, 'error');
      }
    },
    async includeSeason(seriesId, seasonNumber, seriesTitle) {
      if (!this.recoverResults || !this.recoverResults.instance) return;
      const instId = this.recoverResults.instance.id;
      try {
        const resp = await this.apiFetch('/api/recover/exclusions/' + encodeURIComponent(instId), {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ seasons: [{ seriesId, seasonNumber }] }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        this.recoverExclusions = await resp.json();
        const lbl = seasonNumber === 0 ? 'Specials' : ('Season ' + seasonNumber);
        this.showToast('Included ' + lbl + ' of "' + (seriesTitle || 'series') + '" again — back in next scan.', 'success');
      } catch (e) {
        this.showToast('Include failed: ' + e.message, 'error');
      }
    },

    // recoverExclusionCount drives the "Show excluded (N)" pill.
    // Counts the three buckets summed.
    recoverExclusionCount() {
      const e = this.recoverExclusions || {};
      return (e.movies || []).length + (e.series || []).length + (e.seasons || []).length;
    },

    dismissCleanupResults() {
      this.cleanupResults = null;
      this.cleanupSelected = {};
      this.cleanupError = '';
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'cleanup') {
        this.historicalRunInfo = null;
      }
    },
    dismissDiscoverResults() {
      this.scanResults.discover = null;
      this.scanDiscoverSelected = {};
      this.scanDiscoverExpanded = {};
      this.scanDiscoverBannerDismissed = false;
      // Belt-and-suspenders: clear the in-flight Add flag too. If the user
      // dismissed mid-add, the orphaned POST drops on the floor when its
      // response lands; the flag would otherwise stick and disable the
      // re-opened modal's Add buttons.
      this.scanDiscoverAdding = false;
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'discover') {
        this.historicalRunInfo = null;
      }
    },

    clearScanResultsForInstanceChange() {
      this.scanResults = { tag: null, discover: null, audioTags: null, videoTags: null, dvDetail: null };
      this.scanError = '';
      this.historicalRunInfo = null;
      this.scanGroupExpanded = {};
      this.scanRowExpanded = {};
      this.scanDiscoverSelected = {};
      this.scanDiscoverExpanded = {};
      this.scanDiscoverBannerDismissed = false;
      this.scanFilter = 'add';
      this.scanInstanceFilter = 'both';
      this.cleanupResults = null;
      this.cleanupSelected = {};
      this.cleanupError = '';
      this.recoverResults = null;
      this.recoverError = '';
      this.recoverApplySelected = {};
      this.recoverExpanded = {};
      this.recoverSeriesExpanded = {};
      this.recoverSeasonExpanded = {};
      this.recoverFilter = 'all';
      // Belt-and-suspenders — if a check or apply is in flight when the
      // user switches instances, clear the in-flight flags too so the
      // Find / Apply buttons don't stay stuck disabled. Mid-flight HTTP
      // request is orphaned but its result drops on the floor since
      // recoverResults is now null.
      this.recoverLoading = false;
      this.recoverApplying = false;

      // Missing-episodes preview is keyed to a specific Sonarr instance —
      // its episodeIDs / seriesIDs only mean anything against the
      // instance that produced them. Switching instances and then
      // hitting Search/Tag would otherwise pollute the new instance
      // with orphan tag-applications or accidentally hit overlapping IDs.
      // Same in-flight reset rule as recover: an orphan POST drops on the
      // floor because missingEpisodesPreview is now null.
      this.missingEpisodesPreview = null;
      this.missingEpisodesSelected = {};
      this.missingEpisodesError = '';
      this.missingEpisodesLoading = false;
      this.missingEpisodesApplying = false;
    },
    async runLibraryScan() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      if (!this.anyScanModeEnabled()) return;
      this.scanError = '';
      // Close every result modal so a previous run's stack doesn't
      // show through behind the new result. Tag/Discover are the
      // phases this orchestrator dispatches; both will re-open via
      // viewPhaseDetails after their fetch completes.
      this.closeAllResultModals(null);
      this.scanResults = { tag: null, discover: null, audioTags: null, videoTags: null, dvDetail: null };
      this.scanDiscoverBannerDismissed = false;
      this.scanLoading = true;
      try {
        const bothEnabled = this.scanModes.tag && this.scanModes.discover;

        // Phase 1 — Discover first so any new groups can be folded into
        // the tag pass.
        if (this.scanModes.discover) {
          await this.runDiscoverInternal();
          if (this.scanError) return;
        }

        // Phase 2 — auto-add discovered candidates when combined+apply.
        if (bothEnabled && this.scanMode === 'apply' && this.scanResults.discover) {
          const discovered = this.scanResults.discover.discovered || [];
          if (discovered.length > 0) {
            await this.addDiscoveredSearches(discovered.map(d => d.search));
            if (this.scanError) return;
            // Refresh the local groups state so other UI surfaces (Release
            // Groups sub-tab) reflect the new entries. Backend reads cfg
            // fresh per scan, so the tag pass below picks up the new
            // groups regardless.
            await this.loadGroups();
          }
        }

        // Phase 3 — Tag (picks up any groups added in phase 2).
        if (this.scanModes.tag) {
          await this.runTagInternal();
          if (this.scanError) return;
        }

        // Phase 4 — Recover lands with M3c.
      } finally {
        this.scanLoading = false;
      }
    },

    // ===== Tag quality releases mini-wizard =====
    //
    // Replaces the legacy Tag library run-card with a guided flow
    // that walks the user through Use-active-vs-Use-Discover, filter
    // (mandatory), active-list review, and Preview-vs-Apply. Final
    // Run hands off to the existing runLibraryScan after copying
    // wizard picks into the matching Alpine state — same scan, same
    // backend, just better UX.

    openTagRgWizard() {
      // Wizard is openable without a pre-picked instance — the user
      // can pick (or change) the target inside the Choices step,
      // matching the QFA flow. We just need at least one instance
      // of the current app-type to exist; the launcher button
      // already gates on that, so this is defence in depth for
      // programmatic callers.
      const pool = this.scanAvailableInstances();
      if (pool.length === 0) {
        const t = this.scanAppType === 'sonarr' ? 'Sonarr' : 'Radarr';
        this.showToast('Add a ' + t + ' instance in Settings → Instances first', 'error');
        return;
      }
      // Seed precedence: last-used remembered → current scanInstanceId
      // (when in pool) → first-of-type. Wizard's instance picker on
      // Choices lets the user override.
      const remembered = this.recallWizardInstance('tag-rg', pool);
      if (remembered) {
        this.scanInstanceId = remembered;
      } else if (!this.scanInstanceId || !pool.some(i => i.id === this.scanInstanceId)) {
        this.scanInstanceId = pool[0].id;
      }
      // Hydrate from current global state so a user who's already
      // tweaked filters / sync / cleanup on the standalone surfaces
      // sees their picks reflected in the wizard. They can still
      // change anything per-run.
      this.tagRgWizard = {
        open: true,
        step: 0,
        source: 'active',
        discoverAdd: 'enabled',
        filterOnlyTag: 'lossless-web',
        syncToSecondary: !!this.scanSyncToSecondary,
        cleanupUnusedTags: !!this.scanCleanupUnusedTags,
        runMode: 'preview',
        busy: false,
      };
    },

    closeTagRgWizard() {
      if (this.tagRgWizard.busy) return;
      this.tagRgWizard.open = false;
      // Clear the wizard-only Discover-enable-on-add override so the
      // next standalone "+ Add" / "Add Selected" click on the
      // Discover sub-tab doesn't inherit our prior pick. Without this
      // reset a user who ran the wizard with "Add + leave disabled"
      // would silently get enabled:false on every subsequent
      // standalone Discover add. addDiscoveredSearches treats
      // undefined as "use the legacy explicit-pick = enabled
      // contract" — exactly what we want.
      delete this._tagRgDiscoverEnableOnAdd;
    },

    // Steps the wizard renders. Active-groups step skipped on Use
    // Discover (the list will be augmented by Discover at run-time)
    // and on Use filter only (no group list applies — every passing
    // movie gets the single filter-only tag).
    tagRgWizardVisibleSteps() {
      const steps = ['Choices', 'Filter'];
      if (this.tagRgWizard.source === 'active') steps.push('Active groups');
      steps.push('Review');
      return steps;
    },

    // Filter-only collision check — true when the user-typed tag
    // matches any existing Active-group's Tag for this instance type.
    // Backend rejects the request with 409 in this state; the inline
    // warning + Run-button gate let the user fix it before clicking.
    // Case-insensitive to mirror the backend's strings.EqualFold.
    tagRgWizardFilterOnlyCollides() {
      const t = (this.tagRgWizard.filterOnlyTag || '').toLowerCase().trim();
      if (!t) return false;
      const appType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      return (this.groups || []).some(g => g.type === appType && g.tag && g.tag.toLowerCase() === t);
    },
    // Same collision check for the rule editor (QFA / Create Rule
    // wizards) — reads filterOnlyTag off editingRule.options instead
    // of the standalone tag-rg wizard. App-type comes from the rule's
    // primary instance.
    ruleEditorFilterOnlyCollides() {
      if (!this.editingRule || !this.editingRule.options) return false;
      const t = (this.editingRule.options.filterOnlyTag || '').toLowerCase().trim();
      if (!t) return false;
      const appType = this.ruleEditorInstanceType() || 'radarr';
      return (this.groups || []).some(g => g.type === appType && g.tag && g.tag.toLowerCase() === t);
    },

    // Clamp the current step to the new visibleSteps length. Called
    // from any state mutation that could shrink the list (today only
    // the source radios on Step 1 — Active mode adds the Active
    // groups step, Discover removes it). Without this, switching
    // source while past the affected step lands the wizard on an
    // index with no template match: every Step body keys off
    // tagRgWizardVisibleSteps()[step], so undefined → no render and
    // the footer's Next/Run buttons mis-key against a stale length.
    tagRgWizardClampStep() {
      const max = this.tagRgWizardVisibleSteps().length - 1;
      if (this.tagRgWizard.step > max) this.tagRgWizard.step = max;
    },

    tagRgWizardCanAdvance() {
      const cur = this.tagRgWizardVisibleSteps()[this.tagRgWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have a target instance picked. Auto-seeded
        // on open, but defending against an instance being deleted
        // mid-wizard or a programmatic clear.
        if (!this.scanInstanceId) return false;
        // Hard gate: source==='active' with 0 active groups would
        // produce a no-op scan — refuse advance until user switches
        // to Use Discover or closes wizard to add groups manually.
        // The inline orange banner explains the state + offers a
        // one-click switch.
        if (this.tagRgWizard.source === 'active' && this.groupsFilteredByInstanceType().length === 0) {
          return false;
        }
        // Hard gate: filter-only requires a non-empty, non-colliding
        // tag name. Empty would 400 at the backend; colliding would
        // 409. The inline collision warning shows the state inline;
        // an empty input shows the placeholder + the user just hasn't
        // filled it yet.
        if (this.tagRgWizard.source === 'filter-only') {
          const t = (this.tagRgWizard.filterOnlyTag || '').trim();
          if (!t) return false;
          if (this.tagRgWizardFilterOnlyCollides()) return false;
        }
        return true;
      }
      if (cur === 'Filter') {
        // Gate: at least one master must be on. The whole
        // restructure assumes filter is mandatory — this is the
        // enforcement.
        return !!(this.filters && (this.filters.quality || this.filters.audio));
      }
      if (cur === 'Active groups') {
        // Soft gate: warn if 0 enabled but allow continue (user might
        // want to see the Review step anyway). Run-button on Review
        // catches the empty case with a toast.
        return true;
      }
      return true;
    },

    tagRgWizardNext() {
      if (!this.tagRgWizardCanAdvance()) return;
      const max = this.tagRgWizardVisibleSteps().length - 1;
      if (this.tagRgWizard.step < max) this.tagRgWizard.step++;
    },

    tagRgWizardPrev() {
      if (this.tagRgWizard.step > 0) this.tagRgWizard.step--;
    },

    // Helpers for the secondary-instance picker. Reuses existing
    // single-secondary-auto-pick semantics from the standalone Tag
    // library card.
    tagRgWizardSecondaryAvailable() {
      const t = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      return (this.instances || []).filter(i => i.type === t && i.id !== this.scanInstanceId).length > 0;
    },
    tagRgWizardSecondaryName() {
      const t = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      const sec = (this.instances || []).find(i => i.type === t && i.id !== this.scanInstanceId);
      return sec ? sec.name : '';
    },

    // Run hands off to runLibraryScan after seeding state from
    // wizard picks. Same underlying chain — Discover-then-Tag (when
    // source==='discover') or Tag-only (when source==='active').
    async runTagRgWizard() {
      if (!this.scanInstanceId) {
        this.tagRgWizard.busy = false;
        this.showToast('Pick an instance first', 'error');
        return;
      }
      // Active-list emptiness check — for Use active mode only.
      if (this.tagRgWizard.source === 'active') {
        const enabled = this.groupsFilteredByInstanceType().filter(g => g.enabled).length;
        if (enabled === 0) {
          this.showToast('No active release groups enabled. Pick at least one or switch to Use Discover.', 'error');
          return;
        }
      }
      // Filter-only validation — defense in depth. Empty and
      // collision states are also blocked at the Choices step's
      // Next-button, but a programmatic call could bypass that.
      if (this.tagRgWizard.source === 'filter-only') {
        const t = (this.tagRgWizard.filterOnlyTag || '').trim();
        if (!t) {
          this.showToast('Enter a tag name for filter-only mode.', 'error');
          return;
        }
        if (this.tagRgWizardFilterOnlyCollides()) {
          this.showToast('Tag name collides with an Active group rule. Pick a different name.', 'error');
          return;
        }
      }
      // Filter gate — defense in depth. The wizard already blocks
      // advance from the Filter step, but a programmatic call could
      // bypass that.
      if (!this.filters || !(this.filters.quality || this.filters.audio)) {
        this.showToast('Enable at least one filter before tagging.', 'error');
        return;
      }
      // Remember the picked instance for next time the wizard opens.
      this.rememberWizardInstance('tag-rg', this.scanInstanceId);

      // Seed the standard scan state from wizard picks. runLibraryScan
      // reads these.
      this.scanModes = {
        tag: true,
        discover: this.tagRgWizard.source === 'discover',
        recover: false,
      };
      this.scanMode = this.tagRgWizard.runMode;
      this.scanSyncToSecondary = !!this.tagRgWizard.syncToSecondary;
      // Cleanup is only valid with Use active per the safety rail.
      this.scanCleanupUnusedTags = this.tagRgWizard.source === 'active'
        ? !!this.tagRgWizard.cleanupUnusedTags
        : false;
      // Filter-only pass-through. runTagInternal reads these to add
      // tagSource + filterOnlyTag to the /api/scan/run body. For
      // active / discover modes leave scanTagSource empty so the
      // backend's per-group runTag fires (legacy default).
      if (this.tagRgWizard.source === 'filter-only') {
        this.scanTagSource = 'filter-only';
        this.scanFilterOnlyTag = (this.tagRgWizard.filterOnlyTag || '').trim();
      } else {
        this.scanTagSource = '';
        this.scanFilterOnlyTag = '';
      }
      // Discover add-behavior (only meaningful when source==='discover').
      // runLibraryScan reads scanMode + scanModes; the discover-add
      // semantics are applied via the existing addDiscoveredSearches
      // path which auto-adds + enables. The "leave disabled" branch
      // requires the addDiscoveredSearches helper to honour an
      // explicit Enabled flag — it does, via cfg.ReleaseGroups
      // append where Enabled is set per the request body. We stash
      // the choice on a class-level flag the runner reads.
      this._tagRgDiscoverEnableOnAdd = this.tagRgWizard.discoverAdd === 'enabled';

      this.tagRgWizard.busy = true;
      try {
        await this.runLibraryScan();
      } finally {
        this.tagRgWizard.busy = false;
        // Close wizard after run completes (whether success or error).
        // Result modal pops via runLibraryScan's normal viewPhaseDetails
        // route, so user lands on the result panel directly.
        this.tagRgWizard.open = false;
        // Drop the wizard-only override so subsequent standalone
        // Discover adds don't inherit our pick. See closeTagRgWizard
        // for the full rationale.
        delete this._tagRgDiscoverEnableOnAdd;
      }
    },

    // ===== Discover-only mini-wizard =====
    //
    // Opened by the "Run Discover" button on the Tag quality releases
    // actions card. Walks the user through audit-mode + add-behavior
    // + filter selection before firing the existing runDiscover()
    // handler. Replaces the previous one-click button that fired
    // immediately against current globals (no way to confirm the
    // filter the scan would use).

    openDiscoverWizard() {
      const pool = this.scanAvailableInstances();
      if (pool.length === 0) {
        const t = this.scanAppType === 'sonarr' ? 'Sonarr' : 'Radarr';
        this.showToast('Add a ' + t + ' instance in Settings → Instances first', 'error');
        return;
      }
      // Seed precedence: last-used remembered → current scanInstanceId
      // (when in pool) → first-of-type.
      const remembered = this.recallWizardInstance('discover', pool);
      if (remembered) {
        this.scanInstanceId = remembered;
      } else if (!this.scanInstanceId || !pool.some(i => i.id === this.scanInstanceId)) {
        this.scanInstanceId = pool[0].id;
      }
      this.discoverWizard = {
        open: true,
        step: 0,
        runMode: 'preview',
        addBehavior: 'enabled',
        includeKnown: !!this.scanIncludeKnown,
        busy: false,
      };
    },

    closeDiscoverWizard() {
      if (this.discoverWizard.busy) return;
      this.discoverWizard.open = false;
    },

    discoverWizardVisibleSteps() {
      return ['Choices', 'Filter', 'Review'];
    },

    discoverWizardCanAdvance() {
      const cur = this.discoverWizardVisibleSteps()[this.discoverWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have a target instance. Auto-seeded on open
        // but defending against mid-wizard instance deletion.
        return !!this.scanInstanceId;
      }
      if (cur === 'Filter') {
        // Same gate as the Tag wizard — at least one filter must be on.
        // Discover with no filter would surface every release group
        // in the library (huge, useless result).
        return !!(this.filters && (this.filters.quality || this.filters.audio));
      }
      return true;
    },

    discoverWizardNext() {
      if (!this.discoverWizardCanAdvance()) return;
      const max = this.discoverWizardVisibleSteps().length - 1;
      if (this.discoverWizard.step < max) this.discoverWizard.step++;
    },

    discoverWizardPrev() {
      if (this.discoverWizard.step > 0) this.discoverWizard.step--;
    },

    // Run hands off to runDiscover() after seeding the two globals
    // it reads — scanIncludeKnown for audit mode, _tagRgDiscoverEnableOnAdd
    // for the per-row Add Selected behavior. The flag is named "tagRg"
    // historically but it's the shared "did the user pick enabled-on-add"
    // bit; both wizards write it. Result modal pops automatically when
    // scanResults.discover lands; user ticks candidates + Add Selected
    // applies the chosen enable behavior.
    async runDiscoverWizard() {
      if (!this.scanInstanceId) {
        this.discoverWizard.busy = false;
        this.showToast('Pick an instance first', 'error');
        return;
      }
      if (!this.filters || !(this.filters.quality || this.filters.audio)) {
        this.showToast('Enable at least one filter before running Discover.', 'error');
        return;
      }
      // Remember the picked instance for next time the wizard opens.
      this.rememberWizardInstance('discover', this.scanInstanceId);
      this.scanIncludeKnown = !!this.discoverWizard.includeKnown;
      this._tagRgDiscoverEnableOnAdd = this.discoverWizard.addBehavior === 'enabled';
      this.discoverWizard.busy = true;
      try {
        await this.runDiscover();
        // Apply mode — auto-add every discovered candidate with the
        // chosen add-behavior. Skips the manual Add Selected step.
        // Preview mode (default) just leaves the result modal open
        // for the user to tick which to add.
        if (this.discoverWizard.runMode === 'apply' &&
            this.scanResults && this.scanResults.discover) {
          const found = this.scanResults.discover.discovered || [];
          if (found.length > 0) {
            const searches = found.map(d => d.search);
            await this.addDiscoveredSearches(searches);
            // Dismiss the result modal — user lands back on the
            // Tag quality releases page with a toast + the updated
            // Active list. Otherwise the modal would still show
            // the auto-added candidates as if pending review.
            this.scanResults.discover = null;
          } else {
            this.showToast('Discover found no candidates.', 'info');
          }
        }
      } finally {
        this.discoverWizard.busy = false;
        this.discoverWizard.open = false;
        // The Add-Selected override stays alive ON PURPOSE in preview
        // mode — the user just opened the result modal and is about
        // to click Add Selected. Cleared on dismissDiscoverResults
        // / next wizard open. In apply mode addDiscoveredSearches
        // already consumed it; safe to leave dangling.
      }
    },

    // Tag-mode internal — fires POST /api/scan/run with action=tag and
    // stores the result in scanResults.tag. No top-level loading flag
    // toggle (the orchestrator handles that). Sets scanError on failure.
    // syncToInstanceId is set only when the user enabled "Also sync
    // decisions to <secondary>" AND there's an eligible secondary
    // (different radarr instance). Backend ignores empty value.
    async runTagInternal(opts = {}) {
      try {
        // forceMode lets a caller (e.g. confirmScanApply, Quick fix-all)
        // override the user-bound scanMode for a single call without
        // mutating the radio-group state. Avoids the visible flicker of
        // flipping `this.scanMode = 'apply'` then back to 'preview'.
        const requestMode = opts.forceMode || this.scanMode;
        const body = {
          instanceId: this.scanInstanceId,
          action: 'tag',
          mode: requestMode,
        };
        if (this.scanSyncToSecondary) {
          // Prefer the explicit picker selection; fall back to first
          // other-of-same-type when the user has only one candidate
          // (legacy auto-pick) or hasn't touched the picker yet.
          let target = this.scanSyncTargetId;
          if (!target || !this.instances.find(i => i.id === target && i.type === 'radarr' && i.id !== this.scanInstanceId)) {
            const sec = this.instances.find(i => i.type === 'radarr' && i.id !== this.scanInstanceId);
            if (sec) target = sec.id;
          }
          if (target) body.syncToInstanceId = target;
        }
        // cleanupUnusedTags fires when the user has the standalone
        // toggle on, OR when an explicit forceMode caller passes it
        // (e.g. apply-after-preview re-fire). Either path lands the
        // same flag on the request.
        if (this.scanCleanupUnusedTags || opts.cleanupUnusedTags) {
          body.cleanupUnusedTags = true;
        }
        // Filter-only mode pass-through. Backend's runTagFilterOnly
        // takes over when tagSource === "filter-only"; the per-group
        // runTag handler ignores both fields when tagSource is empty
        // or "active". scanFilterOnlyTag carries the user-typed tag
        // (default "lossless-web" — see scan_types.go).
        if (this.scanTagSource) {
          body.tagSource = this.scanTagSource;
          if (this.scanTagSource === 'filter-only') {
            body.filterOnlyTag = this.scanFilterOnlyTag;
            // Cleanup-tail is a no-op in filter-only mode by design
            // (single-rule, single-tag) — strip the flag if a stale
            // value snuck in via opts.cleanupUnusedTags.
            delete body.cleanupUnusedTags;
          }
        }
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.scanError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.scanResults.tag = await resp.json();
        // Reset modal-internal expand/filter state so the new result
        // doesn't inherit stale group/row keys from the previous scan.
        // (viewPhaseDetails does the same when QFA chain or History
        // routes through it; runTagInternal is the standalone path.)
        this.scanGroupExpanded = {};
        this.scanRowExpanded = {};
        this.scanInstanceFilter = 'both';
        // Pick the most useful default filter based on the response. In
        // an apply run with 0 added / 0 removed (typical when running
        // QFA against an already-synced library) the old default of
        // 'add' landed on an empty list and made the result look broken.
        // pickDefaultScanFilter resolves to whichever bucket has the
        // most items, with a small bias toward action-buckets so a
        // preview with pending changes still lands on Match.
        this.scanFilter = this.pickDefaultScanFilter();
        if (requestMode === 'apply' && this.scanResults.tag.applied) {
          const a = this.scanResults.tag.applied;
          let msg = 'Applied: ' + a.itemsAdded + ' added, ' + a.itemsRemoved + ' removed';
          if (a.tagsDeleted && a.tagsDeleted.length > 0) {
            msg += ', ' + a.tagsDeleted.length + ' tag' + (a.tagsDeleted.length === 1 ? '' : 's') + ' deleted';
          }
          this.showToast(msg, 'success');
        }
      } catch (e) {
        this.scanError = e.message || 'Tag scan failed';
      }
    },

    // Promote an existing preview to apply. The user ran Preview first, saw
    // the decisions, and wants to commit them. We re-run with mode='apply'
    // against the same instance so the backend recomputes (not cached — the
    // library might have moved since preview) and applies the result.
    openScanApplyConfirm() {
      // Include secondary deltas in the gate — sync-only runs (primary fully
      // in sync, but secondary needs add/remove) must still be applyable.
      // Match the disabled-check on the button itself for consistency.
      if (!this.scanResults.tag) return;
      const t = this.scanResults.tag.totals;
      const total = (t.toAdd || 0) + (t.toRemove || 0) + (t.secondaryToAdd || 0) + (t.secondaryToRemove || 0);
      if (total === 0) return;
      this.showScanApplyConfirm = true;
    },

    async confirmScanApply() {
      this.showScanApplyConfirm = false;
      // Function-boundary re-entry guard — the modal Apply button
      // already :disabled-gates on scanLoading, but a programmatic
      // call (keyboard shortcut, browser back/forward, Alpine error
      // recovery cycles) could otherwise bypass that and double-fire
      // a 5+ second apply against the same instance.
      if (this.scanLoading) return;
      // Defense in depth — same reasoning as runRecoverApply.
      if (this.isHistoricalForAction('tag')) {
        this.showToast('Run a fresh Tag preview before applying — current panel is a snapshot.', 'error');
        return;
      }
      // Promote-to-apply runs against the live instance — clear any
      // historical-run banner so the result that lands isn't labelled
      // "Historical run:" when it's actually fresh-from-Radarr.
      this.historicalRunInfo = null;
      // Run apply via forceMode so the user-bound scanMode radio doesn't
      // briefly flip to 'apply' and back. The Tag library card's radio
      // stays on Preview throughout — explicit user choice is required to
      // make Apply the default.
      this.scanLoading = true;
      try {
        await this.runTagInternal({ forceMode: 'apply' });
      } finally {
        this.scanLoading = false;
      }
    },

    // Standalone Discover entrypoint — used by the "Run Discover" button on
    // the Release Groups sub-tab. Manages scanLoading itself (the orchestrator
    // wraps that around its own chain) and clears stale results so the user
    // sees a fresh run, not the previous one fading in/out under the spinner.
    async runDiscover() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      this.scanLoading = true;
      this.scanError = '';
      this.scanResults.discover = null;
      this.scanDiscoverSelected = {};
      this.scanDiscoverExpanded = {};
      this.scanDiscoverBannerDismissed = false;
      // Fresh discover replaces any historical-run banner whose kind
      // was discover (no consumer today, but the bookkeeping keeps the
      // historical-run state honest if/when a discover banner lands).
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'discover') {
        this.historicalRunInfo = null;
      }
      try {
        await this.runDiscoverInternal();
        // Surface results in a modal so they don't push the Active
        // groups list off the screen — the candidate review is a
        // one-time action, not persistent state. Only opens on a
        // standalone Run Discover (not on the Quick fix-all chain
        // which has its own combined-result panel on Run mode).
        // scanResults.discover being non-null is enough to auto-pop
        // the Discover detail modal — no separate flag needed.
      } finally {
        this.scanLoading = false;
      }
    },

    // Discover internal — same shape as runTagInternal. Stores result
    // in scanResults.discover. The orchestrator (runLibraryScan / Quick
    // fix-all) handles the top-level loading flag.
    //
    // includeKnown defaults to the user's audit-mode toggle (set on the
    // Release Groups → Find new groups panel) when called standalone, but
    // callers can override via opts. Quick fix-all overrides to false —
    // a chained run that auto-adds discovered candidates must NEVER run
    // in audit mode (would drown the chain in 409 duplicate-name errors
    // when re-adding groups already in config).
    async runDiscoverInternal(opts = {}) {
      try {
        const includeKnown = (opts && Object.prototype.hasOwnProperty.call(opts, 'includeKnown'))
          ? !!opts.includeKnown
          : !!this.scanIncludeKnown;
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'discover',
            includeKnown: includeKnown,
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.scanError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.scanResults.discover = await resp.json();
      } catch (e) {
        this.scanError = e.message || 'Discover failed';
      }
    },

    // Discover-mode selection helpers. scanDiscoverSelected and
    // scanDiscoverExpanded are {search: true} maps — Alpine reactivity
    // needs a fresh object on every mutation, so reassign instead of
    // mutating in place.
    toggleDiscoverSelected(search) {
      const next = { ...this.scanDiscoverSelected };
      if (next[search]) delete next[search];
      else next[search] = true;
      this.scanDiscoverSelected = next;
    },
    toggleDiscoverExpanded(search) {
      const next = { ...this.scanDiscoverExpanded };
      if (next[search]) delete next[search];
      else next[search] = true;
      this.scanDiscoverExpanded = next;
    },
    toggleDiscoverSampleRow(search, movieId) {
      const key = search + ':' + movieId;
      const next = { ...this.discoverSampleExpanded };
      if (next[key]) delete next[key];
      else next[key] = true;
      this.discoverSampleExpanded = next;
    },
    selectAllDiscovered() {
      if (!this.scanResults.discover || !this.scanResults.discover.discovered) return;
      const next = {};
      for (const d of this.scanResults.discover.discovered) next[d.search] = true;
      this.scanDiscoverSelected = next;
    },
    deselectAllDiscovered() {
      this.scanDiscoverSelected = {};
    },
    discoveredSelectedCount() {
      return Object.keys(this.scanDiscoverSelected).length;
    },

    // addDiscoveredSearches is the shared add-flow for both per-row
    // "+ Add" and bulk "Add Selected". Each search POSTs to /api/groups
    // with auto-generated tag/display fields. We hit the existing
    // endpoint per group rather than batching — keeps the backend's
    // Add-Group validation in one place. If any group fails (e.g.
    // tag-name collision), we surface the error and stop; remaining
    // searches stay queued in the selection map so the user can resolve
    // and retry without re-checking.
    async addDiscoveredSearches(searches) {
      if (!searches || searches.length === 0) return;
      if (!this.scanResults.discover || !this.scanResults.discover.instance) {
        this.showToast('No active instance for the discover result', 'error');
        return;
      }
      // Defense in depth — Add buttons are :disabled when viewing a
      // snapshot, but refuse here too in case anything bypasses them.
      if (this.isHistoricalForAction('discover')) {
        this.showToast('Run a fresh Discover before adding — current panel is a snapshot.', 'error');
        return;
      }
      const instType = this.scanResults.discover.instance.type;
      this.scanDiscoverAdding = true;
      let added = 0;
      let skipped = 0;       // already-existing groups — non-fatal
      let failed = '';       // first non-skip failure stops the loop
      const succeeded = [];  // includes skipped — both prune from visible list
      try {
        const bySearch = {};
        for (const d of this.scanResults.discover.discovered) bySearch[d.search] = d;
        for (const search of searches) {
          const d = bySearch[search];
          if (!d) continue;
          // Auto-generated fields. Tag label = lowercase search, sanitized
          // for Radarr's [a-z0-9_-] tag-label rules. No "rg-" prefix —
          // user prefers the bare release-group name as the tag, matching
          // how they tag manually. Display = original-case search. Mode =
          // filtered (matches bash discovery default — discovered groups
          // are always filter-mode candidates).
          const tagLabel = search.toLowerCase().replace(/[^a-z0-9_-]/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '');
          // enabled defaults true (legacy "Add Selected" semantics —
          // user explicitly picked these). Mini-wizard's Use Discover
          // mode passes _tagRgDiscoverEnableOnAdd to override; when
          // false, new groups land on Active list with Enabled off so
          // the Tag pass right after this call skips them. The wizard
          // banner explains this trade-off.
          const enabledOnAdd = (this._tagRgDiscoverEnableOnAdd === false) ? false : true;
          const payload = {
            search,
            tag: tagLabel,
            display: search,
            type: instType,
            mode: 'filtered',
            enabled: enabledOnAdd,
          };
          const resp = await this.apiFetch('/api/groups', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          });
          if (!resp.ok) {
            const body = await resp.text();
            let msg = body;
            try { msg = JSON.parse(body).error || body; } catch {}
            // 409 with "tag name already exists" is the "already in
            // your Active list" case — non-fatal. Skip + continue
            // so a bulk Add-all doesn't abort just because one of
            // the discovered groups overlaps with what you already
            // have. Other 409 reasons (filter-only schedule
            // collision) and any non-409 stay fatal.
            const isAlreadyExists = resp.status === 409 && /already exists|already used/i.test(msg || '');
            if (isAlreadyExists) {
              skipped++;
              succeeded.push(search); // prune from visible list — it IS in Active now
              if (this.scanDiscoverSelected[search]) {
                const sel = { ...this.scanDiscoverSelected };
                delete sel[search];
                this.scanDiscoverSelected = sel;
              }
              continue;
            }
            failed = `${search}: ${msg || ('HTTP ' + resp.status)}`;
            break;
          }
          // Remove from selection so retries after partial failure don't
          // re-attempt this one.
          if (this.scanDiscoverSelected[search]) {
            const sel = { ...this.scanDiscoverSelected };
            delete sel[search];
            this.scanDiscoverSelected = sel;
          }
          succeeded.push(search);
          added++;
        }
        if (succeeded.length > 0) {
          // Prune the just-added (and just-skipped) entries from the
          // visible discover list so the user doesn't see a "+ Add"
          // button next to a group already in Active. Match by search
          // string — discover keys discovered groups by raw RG (case
          // preserved from the first sample).
          if (this.scanResults.discover && Array.isArray(this.scanResults.discover.discovered)) {
            const succeededSet = new Set(succeeded);
            this.scanResults.discover.discovered = this.scanResults.discover.discovered.filter(d => !succeededSet.has(d.search));
            // Update the count too so the totals strip stays honest.
            if (this.scanResults.discover.totals) {
              this.scanResults.discover.totals.discovered = this.scanResults.discover.discovered.length;
            }
          }
          if (added > 0) await this.loadGroups();
        }
        // Toast — combines added + skipped counts so the user sees
        // the full picture in one message.
        if (added > 0 && skipped > 0) {
          this.showToast(`Added ${added} group${added === 1 ? '' : 's'} (${skipped} already in Active list — skipped)`, 'success');
        } else if (added > 0) {
          this.showToast(`Added ${added} group${added === 1 ? '' : 's'}`, 'success');
        } else if (skipped > 0 && !failed) {
          this.showToast(`All ${skipped} group${skipped === 1 ? ' was' : 's were'} already in Active list — nothing to add`, '');
        }
        if (failed) {
          this.scanError = `Add failed: ${failed}`;
          this.showToast(`Stopped after ${added} added — ${failed}`, 'error');
        }
      } finally {
        this.scanDiscoverAdding = false;
      }
    },

    // Bulk "Add Selected" — submits everything in scanDiscoverSelected.
    async addSelectedDiscovered() {
      await this.addDiscoveredSearches(Object.keys(this.scanDiscoverSelected));
    },

    // Per-row "+ Add" — submits a single discovered group. Uses the same
    // pipeline as the bulk action; the only difference is scope.
    async addOneDiscovered(search) {
      await this.addDiscoveredSearches([search]);
    },

    // ====== Cleanup unused tags (Release Groups sub-tab) ======
    // Standalone cleanup entry point — runs against scanInstanceId, surfaces
    // managed tags with 0 movies, lets user delete per-row or in bulk.
    // Backend is the same /api/scan/run with action=cleanup; preview lists
    // candidates, apply (with optional cleanupLabels filter) deletes them.

    async runCleanupCheck() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      this.closeAllResultModals('cleanup');
      this.cleanupLoading = true;
      this.cleanupError = '';
      this.cleanupResults = null;
      this.cleanupSelected = {};
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'cleanup',
            mode: 'preview',
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.cleanupError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.cleanupResults = await resp.json();
      } catch (e) {
        this.cleanupError = e.message || 'Cleanup check failed';
      } finally {
        this.cleanupLoading = false;
      }
    },

    toggleCleanupSelected(label) {
      const next = { ...this.cleanupSelected };
      if (next[label]) delete next[label];
      else next[label] = true;
      this.cleanupSelected = next;
    },
    selectAllCleanup() {
      if (!this.cleanupResults || !this.cleanupResults.totals.tagsToDelete) return;
      const next = {};
      for (const c of this.cleanupResults.totals.tagsToDelete) next[c.label] = true;
      this.cleanupSelected = next;
    },
    deselectAllCleanup() {
      this.cleanupSelected = {};
    },
    cleanupSelectedCount() {
      return Object.keys(this.cleanupSelected).length;
    },

    // Per-row Delete — deletes a single label. Reuses the same cleanup-apply
    // backend path with cleanupLabels=[label] so the safety-bound logic
    // applies (label must be a valid candidate).
    async deleteOneCleanup(label) {
      await this.applyCleanupDeletes([label]);
    },

    // Bulk Delete-Selected — submits everything in cleanupSelected.
    async deleteSelectedCleanup() {
      await this.applyCleanupDeletes(Object.keys(this.cleanupSelected));
    },

    // applyCleanupDeletes is the shared backend call. After a successful
    // delete, removes the deleted labels from the in-memory cleanup result
    // so the row(s) disappear from the UI without a full re-fetch. Also
    // clears those labels from cleanupSelected.
    async applyCleanupDeletes(labels) {
      if (!labels || labels.length === 0) return;
      // Defense in depth — same reasoning as runRecoverApply / Discover.
      if (this.isHistoricalForAction('cleanup')) {
        this.showToast('Run a fresh Cleanup check before deleting — current panel is a snapshot.', 'error');
        return;
      }
      this.cleanupDeleting = true;
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'cleanup',
            mode: 'apply',
            cleanupLabels: labels,
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.showToast('Delete failed: ' + msg, 'error');
          return;
        }
        const data = await resp.json();
        const deleted = (data.applied && data.applied.tagsDeleted) || [];
        if (deleted.length === 0) {
          this.showToast('Nothing to delete', 'error');
          return;
        }
        const deletedSet = new Set(deleted);
        // Prune in-memory result + selection so UI updates immediately.
        if (this.cleanupResults && this.cleanupResults.totals && Array.isArray(this.cleanupResults.totals.tagsToDelete)) {
          this.cleanupResults.totals.tagsToDelete = this.cleanupResults.totals.tagsToDelete.filter(c => !deletedSet.has(c.label));
        }
        const nextSel = { ...this.cleanupSelected };
        for (const l of deleted) delete nextSel[l];
        this.cleanupSelected = nextSel;
        this.showToast('Deleted ' + deleted.length + ' tag' + (deleted.length === 1 ? '' : 's'), 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + (e.message || 'unknown'), 'error');
      } finally {
        this.cleanupDeleting = false;
      }
    },

    // Cleanup-only path used by Quick fix-all when 'cleanup' is on but 'tag'
    // is off. Runs a fresh cleanup-apply against current Radarr state — no
    // tag-pass deltas to factor in. Shows a toast with the count.
    async runCleanupApplyAll() {
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'cleanup',
            mode: 'apply',
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.scanError = msg || ('HTTP ' + resp.status);
          return;
        }
        const data = await resp.json();
        const deleted = (data.applied && data.applied.tagsDeleted) || [];
        this.showToast('Cleanup: ' + deleted.length + ' tag' + (deleted.length === 1 ? '' : 's') + ' deleted', 'success');
      } catch (e) {
        this.scanError = e.message || 'Cleanup failed';
      }
    },

    // ====== Recover (Run mode sub-tab) ======
    // Bash tagarr_recover.sh parity. Standalone, not part of Quick fix-all.

    async runRecoverCheck() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      // Close any other open modal first; closeAllResultModals(except='recover')
      // also nulls historicalRunInfo so a previous snapshot banner can't carry
      // over into this fresh live run.
      this.closeAllResultModals('recover');
      this.recoverLoading = true;
      this.recoverError = '';
      this.recoverResults = null;
      this.recoverApplySelected = {};
      this.recoverExpanded = {};
      this.recoverSeriesExpanded = {};
      this.recoverSeasonExpanded = {};
      this.recoverFilter = 'all';
      // Standalone Run Recover targets a single instance — drop any
      // wizard-driven variant set so the switcher doesn't render
      // stale primary/secondary pills from an earlier Both run.
      this.qfaDetailVariants = [];
      this.qfaDetailVariantIdx = 0;
      // Fresh recover replaces any historical-run banner that was tied
      // to an earlier replay.
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'recover') {
        this.historicalRunInfo = null;
      }
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'recover',
            mode: 'preview',
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.recoverError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.recoverResults = await resp.json();
        // Load the per-instance exclusion list so the result panel can
        // show the Show-excluded count + per-row Include-again buttons.
        if (this.recoverResults.instance && this.recoverResults.instance.id) {
          this.loadRecoverExclusions(this.recoverResults.instance.id);
        }
        // Auto-default would-fix rows to selected so a user who just clicks
        // Apply gets every recoverable item — they untoggle the ones they
        // don't trust. Matches bash's "fix all" semantics with the
        // container-side per-row override.
        const sel = {};
        for (const it of (this.recoverResults.recover || [])) {
          if (it.status === 'would-fix') sel[it.id] = true;
        }
        this.recoverApplySelected = sel;
        // Default filter to whichever bucket has the most action so the
        // user lands on something useful — would-fix beats flagged beats all.
        const t = this.recoverResults.totals;
        if (t.recoverWouldFix) this.recoverFilter = 'would-fix';
        else if (t.recoverFlagged) this.recoverFilter = 'flagged';
        else this.recoverFilter = 'all';
      } catch (e) {
        this.recoverError = e.message || 'Recover check failed';
      } finally {
        this.recoverLoading = false;
      }
    },

    async runRecoverApply() {
      if (!this.recoverResults) return;
      // Function-boundary re-entry guard — same rationale as
      // confirmScanApply.
      if (this.recoverApplying) return;
      // Defense in depth — the partial's Apply button is :disabled when
      // viewing a snapshot, but a programmatic call (keyboard shortcut,
      // future code path, replayed event) could otherwise bypass that
      // and write apply mutations against the snapshot's selection set.
      // Refuse at the function boundary too.
      if (this.isHistoricalForAction('recover')) {
        this.showToast('Run a fresh Recover before applying — current panel is a snapshot.', 'error');
        return;
      }
      const ids = Object.keys(this.recoverApplySelected).filter(k => !!this.recoverApplySelected[k]).map(k => parseInt(k, 10));
      if (ids.length === 0) return;
      // Apply against the SAME instance the preview ran on, not the
      // Library-scan-global selector. The result panel can be opened by
      // the standalone Run Recover button, the wizard chain, or a
      // History replay — only the wizard path is guaranteed to have a
      // matching scanInstanceId. Reading the id off the response object
      // makes apply correct regardless of trigger.
      const applyInstanceId = (this.recoverResults.instance && this.recoverResults.instance.id) || this.scanInstanceId;
      this.recoverApplying = true;
      this.recoverError = '';
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: applyInstanceId,
            action: 'recover',
            mode: 'apply',
            recoverRename: this.recoverRename,
            recoverApplyItems: ids,
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.recoverError = msg || ('HTTP ' + resp.status);
          // Toast too — the inline error banner is hidden behind the
          // modal overlay; without a toast the user sees nothing happen.
          this.showToast('Recover apply failed: ' + this.recoverError, 'error');
          return;
        }
        // Replace the result wholesale — apply-mode response includes the
        // full updated state (fixed rows, fix-failed errors, rename status).
        this.recoverResults = await resp.json();
        // Selection retention rules:
        //  - fixed rows: drop from selection (already done, retry pointless)
        //  - fix-failed rows: KEEP in selection so the user can re-Apply
        //    after fixing the underlying issue (PUT error / fetch error)
        //    without manually re-checking each row
        //  - would-fix rows that the user excluded earlier: keep their
        //    selection state intact
        const remaining = {};
        for (const it of (this.recoverResults.recover || [])) {
          if (this.recoverApplySelected[it.id] && (it.status === 'would-fix' || it.status === 'fix-failed')) {
            remaining[it.id] = true;
          }
        }
        this.recoverApplySelected = remaining;
        const t = this.recoverResults.totals;
        const fixed = t.recoverFixed || 0;
        const failed = t.recoverFixFailed || 0;
        if (failed > 0) {
          this.showToast('Recovered ' + fixed + ', ' + failed + ' failed', 'error');
        } else {
          this.showToast('Recovered ' + fixed + ' release group' + (fixed === 1 ? '' : 's'), 'success');
        }
        // Re-default the filter chip post-apply to whichever bucket needs
        // attention most. fix-failed wins (user needs to retry / investigate),
        // then would-fix (still pending), then fixed (confirm result), then all.
        if (failed > 0) this.recoverFilter = 'fix-failed';
        else if (t.recoverWouldFix > 0) this.recoverFilter = 'would-fix';
        else if (fixed > 0) this.recoverFilter = 'fixed';
        else this.recoverFilter = 'all';
      } catch (e) {
        this.recoverError = e.message || 'Recover apply failed';
        this.showToast('Recover apply failed: ' + this.recoverError, 'error');
      } finally {
        this.recoverApplying = false;
      }
    },

    toggleRecoverExpanded(id) {
      const next = { ...this.recoverExpanded };
      if (next[id]) delete next[id];
      else next[id] = true;
      this.recoverExpanded = next;
    },
    toggleRecoverSeriesExpanded(seriesId) {
      const next = { ...this.recoverSeriesExpanded };
      if (next[seriesId]) delete next[seriesId];
      else next[seriesId] = true;
      this.recoverSeriesExpanded = next;
    },
    toggleRecoverSeasonExpanded(seriesId, seasonNumber) {
      const key = seriesId + '-' + seasonNumber;
      const next = { ...this.recoverSeasonExpanded };
      if (next[key]) delete next[key];
      else next[key] = true;
      this.recoverSeasonExpanded = next;
    },
    recoverSeasonExpandedKey(seriesId, seasonNumber) {
      return !!this.recoverSeasonExpanded[seriesId + '-' + seasonNumber];
    },

    // recoverSonarrGroupedItems builds Series → Season → Episodes from the
    // flat filteredRecoverItems list. Used by the Sonarr-only result tree
    // (Radarr keeps the flat layout — there's no series/season hierarchy
    // to fold movies into). Status-counts roll up at each level so series
    // and season cards can render at-a-glance pills like "Would fix: 12 ·
    // Flagged: 3" without re-scanning the children.
    //
    // Filtering through filteredRecoverItems means a chip-narrowed view
    // (e.g. recoverFilter='fix-failed') hides series/seasons with no
    // matching episodes — totals on cards then reflect what's actually
    // shown, not the full population.
    recoverSonarrGroupedItems() {
      const list = this.filteredRecoverItems();
      const seriesMap = new Map();
      for (const it of list) {
        if (!it.seriesId) continue;
        let series = seriesMap.get(it.seriesId);
        if (!series) {
          series = {
            seriesId:    it.seriesId,
            seriesTitle: it.seriesTitle || (it.title || '').split(' — ')[0],
            year:        it.year,
            tvdbId:      it.tvdbId,
            seasons:     new Map(),
            statusCounts: {},
            total: 0,
          };
          seriesMap.set(it.seriesId, series);
        }
        // Distinguish "Specials" (Sonarr's real season 0) from "Unknown
        // season" (shape issue: seasonNumber missing/null/undefined). Both
        // are bucket-of-last-resort but mean different things — collapsing
        // them under one label loses signal. seasonKey uses string sentinel
        // 'unknown' so it can't collide with any real numeric season; the
        // sort below treats it as last.
        const hasSeason = typeof it.seasonNumber === 'number';
        const seasonKey = hasSeason ? it.seasonNumber : 'unknown';
        let season = series.seasons.get(seasonKey);
        if (!season) {
          season = {
            seasonNumber: hasSeason ? it.seasonNumber : null,
            episodes: [],
            statusCounts: {},
          };
          series.seasons.set(seasonKey, season);
        }
        season.episodes.push(it);
        season.statusCounts[it.status] = (season.statusCounts[it.status] || 0) + 1;
        series.statusCounts[it.status] = (series.statusCounts[it.status] || 0) + 1;
        series.total += 1;
      }
      return Array.from(seriesMap.values())
        .map(s => ({
          ...s,
          seasons: Array.from(s.seasons.values())
            .sort((a, b) => {
              // "Unknown" (null seasonNumber) always sorts last; otherwise
              // ascending numeric. Sonarr's Specials (season 0) ends up
              // first naturally.
              if (a.seasonNumber === null && b.seasonNumber === null) return 0;
              if (a.seasonNumber === null) return 1;
              if (b.seasonNumber === null) return -1;
              return a.seasonNumber - b.seasonNumber;
            })
            .map(sn => ({
              ...sn,
              episodes: sn.episodes.slice().sort((a, b) => {
                const la = this.episodeLabelFromItem(a);
                const lb = this.episodeLabelFromItem(b);
                return la.localeCompare(lb);
              }),
            })),
        }))
        .sort((a, b) => (a.seriesTitle || '').toLowerCase().localeCompare((b.seriesTitle || '').toLowerCase()));
    },
    // episodeLabelFromItem extracts the "S01E05" part out of the row's
    // composite title ("Series — S01E05") so the per-episode row inside a
    // season card doesn't repeat the series name. Falls back to a regex
    // pull from relativePath, then to season-only "S01" if everything
    // else fails.
    episodeLabelFromItem(it) {
      if (!it) return '';
      if (it.seriesTitle && it.title) {
        const sep = it.seriesTitle + ' — ';
        if (it.title.startsWith(sep)) return it.title.substring(sep.length);
      }
      const m = (it.relativePath || it.title || '').match(/S\d+E\d+(?:[E-]\d+)*/i);
      if (m) return m[0].toUpperCase();
      if (typeof it.seasonNumber === 'number') return 'S' + String(it.seasonNumber).padStart(2, '0');
      return it.title || '';
    },

    toggleRecoverApply(id) {
      const next = { ...this.recoverApplySelected };
      if (next[id]) delete next[id];
      else next[id] = true;
      this.recoverApplySelected = next;
    },

    // Applyable rows = would-fix (pending) + fix-failed (eligible for retry).
    // Used by the apply-controls strip wording, Select-all behavior, and
    // the disabled-state gates on those buttons.
    recoverApplyableCount() {
      if (!this.recoverResults) return 0;
      return (this.recoverResults.recover || [])
        .filter(it => it.status === 'would-fix' || it.status === 'fix-failed')
        .length;
    },

    recoverApplySelectedCount() {
      return Object.keys(this.recoverApplySelected).filter(k => !!this.recoverApplySelected[k]).length;
    },

    // Show "(incl. N retry)" hint when some of the selected rows are
    // fix-failed retries — disambiguates the count when the user is
    // selectively re-applying after a partial-failure run.
    recoverFixFailedSelectedCount() {
      if (!this.recoverResults) return 0;
      const idSet = new Set(
        (this.recoverResults.recover || [])
          .filter(it => it.status === 'fix-failed')
          .map(it => it.id)
      );
      return Object.keys(this.recoverApplySelected)
        .filter(k => !!this.recoverApplySelected[k] && idSet.has(parseInt(k, 10)))
        .length;
    },

    recoverSelectAllApply() {
      const next = {};
      for (const it of (this.recoverResults?.recover || [])) {
        if (it.status === 'would-fix' || it.status === 'fix-failed') next[it.id] = true;
      }
      this.recoverApplySelected = next;
    },

    recoverDeselectAllApply() {
      this.recoverApplySelected = {};
    },

    // filteredRecoverItems narrows the per-row list by the chip filter.
    // Each status maps to its own chip (UI agent review 2026-04-27 split
    // the previous "would-fix / fixed" merged chip — they have different
    // colors and meanings, so merging broke the chip↔badge color story).
    filteredRecoverItems() {
      if (!this.recoverResults) return [];
      // Excluded chip is its own render branch (handled in the partial
      // via recoverExcludedDisplay()) — return [] here so the regular
      // chip render path doesn't double-show anything.
      if (this.recoverFilter === 'excluded') return [];
      const list = this.recoverResults.recover || [];
      if (this.recoverFilter === 'all') return list;
      return list.filter(it => it.status === this.recoverFilter);
    },

    // recoverExcludedDisplay returns the excluded items in render-
    // ready shape — same fields the regular result rows expect so
    // the partial can reuse the existing card markup. Each entry
    // carries a kind ('movie' | 'series' | 'season') + identity +
    // title (best-effort from the API enrichment, with "Movie #ID"
    // / "Series #ID" fallback when Arr was unreachable when GET
    // fired).
    //
    // Sorted by title (case-insensitive) so the list is stable
    // across opens. Empty array when nothing is excluded — partial
    // shows the standard empty-filter message in that case.
    recoverExcludedDisplay() {
      const e = this.recoverExclusions || {};
      const out = [];
      for (const m of (e.movies || [])) {
        out.push({
          kind: 'movie',
          id: m.id,
          title: m.title || ('Movie #' + m.id),
          year: m.year || 0,
        });
      }
      for (const s of (e.series || [])) {
        out.push({
          kind: 'series',
          id: s.id,
          seriesId: s.id,
          title: s.title || ('Series #' + s.id),
          year: s.year || 0,
          tvdbId: s.tvdbId || 0,
        });
      }
      for (const s of (e.seasons || [])) {
        const lbl = s.seasonNumber === 0 ? 'Specials' : ('Season ' + s.seasonNumber);
        const baseTitle = s.seriesTitle || ('Series #' + s.seriesId);
        out.push({
          kind: 'season',
          id: 'season-' + s.seriesId + ':' + s.seasonNumber,
          seriesId: s.seriesId,
          seasonNumber: s.seasonNumber,
          seriesTitle: baseTitle,
          title: baseTitle + ' — ' + lbl,
          year: s.year || 0,
        });
      }
      out.sort((a, b) => a.title.localeCompare(b.title));
      return out;
    },

    // recoverStatusLabel + recoverStatusStyle render the per-row badge.
    // Kept as JS helpers (not a CSS class) so the badge color tracks the
    // status string verbatim — easier to add new buckets later.
    recoverStatusLabel(status) {
      switch (status) {
        case 'would-fix':     return 'Would fix';
        case 'fixed':         return 'Fixed';
        case 'fix-failed':    return 'Fix failed';
        case 'flagged':       return 'Flagged';
        case 'no-history':    return 'No history';
        case 'no-rls-group':  return 'No-RlsGroup';
        case 'failed-verify': return 'Failed verify';
        default:              return status || '?';
      }
    },
    // Per-row status badges. Reuse the existing .btn .btn-sm class so they
    // pick up the EXACT same visual tokens as the filter chips above (same
    // border, same padding, same font, same hover-resistant base look).
    // The wrapper template applies a font-size + padding override to scale
    // them down for in-row use, plus pointer-events:none + cursor:default
    // so they don't read as interactive.
    //
    // Per-status mapping — each row badge color matches its filter chip's
    // left-border accent, so a quick glance from "all No-RlsGroup chip"
    // to a row badge tells the same color story:
    //   fixed         → btn-primary (filled green)         — bash "fixed"
    //   would-fix     → btn-blue    (filled blue)          — pending fix
    //   fix-failed    → btn-danger  (filled red)           — apply error
    //   flagged       → btn-warn    (filled amber)         — manual review
    //   no-rls-group  → btn-purple  (filled purple)        — limitation
    //   failed-verify → btn-teal    (filled teal)          — limitation
    //   no-history    → '' (neutral grey, no accent)       — pure no-data
    //   fallback      → '' neutral
    recoverStatusBtnClass(status) {
      switch (status) {
        case 'fixed':         return 'btn-primary';
        case 'would-fix':     return 'btn-blue';
        case 'fix-failed':    return 'btn-danger';
        case 'flagged':       return 'btn-warn';
        case 'no-rls-group':  return 'btn-purple';
        case 'failed-verify': return 'btn-teal';
        default:              return ''; // no-history + fallback: neutral grey
      }
    },

    // recoverFilenameRejectLabel maps the engine's rejection-reason enum
    // to a plain-language explanation surfaced in the drill-down. Helps
    // users understand "why didn't the engine just read the group from
    // the filename?" without learning the engine's internal vocabulary.
    recoverFilenameRejectLabel(reason) {
      switch (reason) {
        case 'no-hyphen':
          return 'no hyphen in filename — no group separator to split on';
        case 'empty':
          return 'filename ends with a hyphen but nothing after it';
        case 'multi-token':
          return 'text after the last hyphen has dots or spaces (looks like a codec/audio fragment, not a clean group name)';
        case 'codec':
          return 'filename ends with a codec (h265/x264/etc.), not a group name';
        case 'split-fragment':
          return 'filename ends with "DL" or "HD" — leftover from WEB-DL / DTS-HD splits';
        case 'resolution':
          return 'filename ends with a resolution (1080p/2160p), not a group name';
        default:
          return reason;
      }
    },

    // ===== Missing episodes (Tag Library → Sonarr → Missing episodes) =====
    //
    // Three backend endpoints feed this surface:
    //   - POST /api/scan/missing-episodes/preview  — run the scan
    //   - POST /api/scan/missing-episodes/search   — trigger Sonarr search
    //   - POST /api/scan/missing-episodes/tag      — apply / auto-cleanup tag
    //
    // The Search button goes straight to Sonarr's EpisodeSearch command
    // (Sonarr queues + throttles internally). The Tag button writes a
    // single configurable tag (default "missing-episodes") to every
    // series with gaps, with auto-cleanup (removeFromOthers: true) so a
    // re-scan after a series fills in retires the tag automatically.

    async runMissingEpisodesScan() {
      if (!this.scanInstanceId) {
        this.showToast('Pick a Sonarr instance first', 'error');
        return;
      }
      // C1: both filters disabled = no series will be scanned. Guard
      // here so the user gets a clear toast instead of a backend 400 they
      // can't see. The Run button is also :disabled in this state.
      if (!this.missingEpisodesConfig.includeContinuing && !this.missingEpisodesConfig.includeEnded) {
        this.showToast('Enable Continuing or Ended series first', 'error');
        return;
      }
      this.missingEpisodesLoading = true;
      this.missingEpisodesError = '';
      try {
        // C2/C3: bufferHours sent as a number with the explicit value the
        // user typed (0 is a valid "any aired episode" sentinel). The
        // backend uses *int to tell "not supplied" from "explicit 0",
        // but the JSON wire format is just a number — we always send one.
        const bufferHoursRaw = this.missingEpisodesConfig.bufferHours;
        const bufferHours = (bufferHoursRaw === undefined || bufferHoursRaw === null || bufferHoursRaw === '')
          ? 24
          : Number(bufferHoursRaw);
        const res = await this.apiFetch('/api/scan/missing-episodes/preview', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            threshold: (this.missingEpisodesConfig.thresholdPercent || 70) / 100,
            bufferHours: bufferHours,
            includeContinuing: !!this.missingEpisodesConfig.includeContinuing,
            includeEnded: !!this.missingEpisodesConfig.includeEnded,
            includeSpecials: !!this.missingEpisodesConfig.includeSpecials,
          }),
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        this.missingEpisodesPreview = data;
        // Pre-select all missing episodes — the typical action after a
        // scan is "search for everything that was found". The user can
        // toggle off rows before hitting the bulk button.
        const sel = {};
        for (const s of (data.series || [])) {
          for (const season of (s.seasons || [])) {
            for (const ep of (season.missingEpisodes || [])) {
              sel[ep.episodeID] = true;
            }
          }
        }
        this.missingEpisodesSelected = sel;
        const tone = data.seriesWithGaps > 0 ? 'info' : 'success';
        const msg = data.seriesWithGaps > 0
          ? 'Scan complete: ' + data.totalMissingEpisodes + ' missing episodes across ' + data.seriesWithGaps + ' series'
          : 'Scan complete: all ' + data.seriesScanned + ' series are complete';
        this.showToast(msg, tone);
      } catch (e) {
        this.missingEpisodesError = String((e && e.message) || e);
      } finally {
        this.missingEpisodesLoading = false;
      }
    },

    missingEpisodesSelectAll() {
      const sel = {};
      for (const s of ((this.missingEpisodesPreview && this.missingEpisodesPreview.series) || [])) {
        for (const season of (s.seasons || [])) {
          for (const ep of (season.missingEpisodes || [])) {
            sel[ep.episodeID] = true;
          }
        }
      }
      this.missingEpisodesSelected = sel;
    },
    missingEpisodesSelectNone() { this.missingEpisodesSelected = {}; },
    missingEpisodesSelectedCount() {
      const sel = this.missingEpisodesSelected || {};
      let n = 0;
      for (const k of Object.keys(sel)) if (sel[k]) n++;
      return n;
    },

    async missingEpisodesSearchSelected() {
      const sel = this.missingEpisodesSelected || {};
      const ids = Object.keys(sel).filter(k => sel[k]).map(k => Number(k));
      if (ids.length === 0) return;
      if (!await this.confirmDialog({
        title:       'Trigger Sonarr search?',
        message:     'Trigger Sonarr search for ' + ids.length + ' episodes? Sonarr will queue + throttle the search calls itself.',
        confirmText: 'Trigger search',
      })) return;
      await this._missingEpisodesSearch(ids);
    },
    async missingEpisodesSearchOne(episodeID) {
      await this._missingEpisodesSearch([episodeID]);
    },
    async _missingEpisodesSearch(episodeIDs) {
      this.missingEpisodesApplying = true;
      try {
        const res = await this.apiFetch('/api/scan/missing-episodes/search', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: this.scanInstanceId, episodeIds: episodeIDs }),
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        this.showToast('Sonarr search triggered for ' + (data.triggered || episodeIDs.length) + ' episode(s)', 'success');
      } catch (e) {
        this.showToast('Search failed: ' + ((e && e.message) || e), 'error');
      } finally {
        this.missingEpisodesApplying = false;
      }
    },

    async missingEpisodesTagSeries() {
      const series = ((this.missingEpisodesPreview && this.missingEpisodesPreview.series) || []);
      const seriesIDs = series.map(s => s.seriesID);
      if (seriesIDs.length === 0) {
        this.showToast('Run a scan first — no series to tag', 'error');
        return;
      }
      const tagName = (this.missingEpisodesConfig.tagName || 'missing-episodes').trim();
      if (!tagName) {
        this.showToast('Tag name cannot be empty', 'error');
        return;
      }
      if (!await this.confirmDialog({
        title:       'Tag ' + seriesIDs.length + ' series?',
        message:     'Tag ' + seriesIDs.length + ' series with "' + tagName + '". Series that currently carry this tag but are no longer flagged will have it removed automatically.',
        confirmText: 'Tag',
      })) return;
      this.missingEpisodesApplying = true;
      try {
        const res = await this.apiFetch('/api/scan/missing-episodes/tag', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            tagName: tagName,
            seriesIds: seriesIDs,
            removeFromOthers: true,
          }),
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        this.showToast('Tagged ' + (data.applied || 0) + ' series, removed from ' + (data.removed || 0), 'success');
      } catch (e) {
        this.showToast('Tag failed: ' + ((e && e.message) || e), 'error');
      } finally {
        this.missingEpisodesApplying = false;
      }
    },

    // ---- TBA refresh (Sonarr-only) -------------------------------
    async runTbaRefreshScan() {
      if (!this.scanInstanceId) {
        this.showToast('Pick a Sonarr instance first', 'error');
        return;
      }
      this.tbaRefreshLoading = true;
      this.tbaRefreshError = '';
      try {
        const res = await this.apiFetch('/api/scan/tba-refresh/preview', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            includeContinuing: !!this.tbaRefreshConfig.includeContinuing,
            includeEnded: !!this.tbaRefreshConfig.includeEnded,
            includeSpecials: !!this.tbaRefreshConfig.includeSpecials,
          }),
        });
        if (!res.ok) throw new Error(await res.text());
        this.tbaRefreshPreview = await res.json();
        // Pre-select every found file — same default as Missing Episodes.
        const sel = {};
        for (const ser of (this.tbaRefreshPreview.series || [])) {
          for (const f of (ser.files || [])) sel[f.episodeFileId] = true;
        }
        this.tbaRefreshSelected = sel;
      } catch (e) {
        this.tbaRefreshError = (e && e.message) || String(e);
        this.tbaRefreshPreview = null;
      } finally {
        this.tbaRefreshLoading = false;
      }
    },
    tbaRefreshSelectAll() {
      const sel = {};
      for (const ser of ((this.tbaRefreshPreview && this.tbaRefreshPreview.series) || [])) {
        for (const f of (ser.files || [])) sel[f.episodeFileId] = true;
      }
      this.tbaRefreshSelected = sel;
    },
    tbaRefreshSelectNone() { this.tbaRefreshSelected = {}; },
    tbaRefreshSelectedCount() {
      return Object.values(this.tbaRefreshSelected || {}).filter(Boolean).length;
    },
    // Group a series' flat file list into [{season, files}] for the
    // series → season → file rendering (and the same shape the Discord
    // notification groups by). Files arrive season-sorted from the API.
    tbaSeasonGroups(series) {
      const groups = [];
      let cur = null;
      for (const f of ((series && series.files) || [])) {
        if (!cur || cur.season !== f.seasonNumber) {
          cur = { season: f.seasonNumber, files: [] };
          groups.push(cur);
        }
        cur.files.push(f);
      }
      return groups;
    },
    // SxxExx label; collapses multi-episode files to S03E07E08.
    tbaEpLabel(file) {
      const s = 'S' + String(file.seasonNumber).padStart(2, '0');
      const eps = (file.episodeNumbers || []);
      if (eps.length === 0) return s;
      return s + eps.map(e => 'E' + String(e).padStart(2, '0')).join('');
    },
    async applyTbaRefresh() {
      const groups = [];
      for (const ser of ((this.tbaRefreshPreview && this.tbaRefreshPreview.series) || [])) {
        const fileIds = (ser.files || [])
          .filter(f => this.tbaRefreshSelected[f.episodeFileId])
          .map(f => f.episodeFileId);
        if (fileIds.length > 0) groups.push({ seriesId: ser.seriesId, fileIds });
      }
      if (groups.length === 0) {
        this.showToast('Select at least one file to rename', 'error');
        return;
      }
      const total = groups.reduce((n, g) => n + g.fileIds.length, 0);
      if (!await this.confirmDialog({
        title:       'Rename ' + total + ' file' + (total === 1 ? '' : 's') + '?',
        message:     'Trigger Sonarr to rename ' + total + ' file' + (total === 1 ? '' : 's') + ' across ' + groups.length + ' series. Sonarr renames per its configured naming pattern; this is queued and runs in the background.',
        confirmText: 'Rename',
      })) return;
      this.tbaRefreshApplying = true;
      try {
        const res = await this.apiFetch('/api/scan/tba-refresh/apply', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: this.scanInstanceId, groups }),
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        const failed = (data.errors || []).length;
        let msg = 'Queued ' + (data.queued || 0) + ' rename' + ((data.queued === 1) ? '' : 's') + ' across ' + (data.seriesCount || 0) + ' series';
        if (failed > 0) msg += ' — ' + failed + ' series failed';
        this.showToast(msg, failed > 0 ? 'error' : 'success');
        // Clear — Sonarr renames async; re-run Preview to confirm.
        this.tbaRefreshPreview = null;
        this.tbaRefreshSelected = {};
      } catch (e) {
        this.showToast('Rename failed: ' + ((e && e.message) || e), 'error');
      } finally {
        this.tbaRefreshApplying = false;
      }
    },

    // formatDate renders an ISO8601 timestamp in the CONTAINER'S host
    // context. Three controls feed in:
    //   - serverTimezone (from $TZ on init) — the moment is shown in
    //     the container's local time, not the browser's
    //   - serverLocale (from $LANG, or derived from $TZ when LANG is
    //     unset) — drives date order (DD/MM, MM/DD, YYYY-MM-DD)
    //   - timeFormat (user-set in Settings → Display) — auto lets the
    //     locale pick 12h vs 24h; "24h" or "12h" forces it
    // So an Oslo-TZ admin sees "28.04.2026, 17:30:00" by default; an
    // en-US-TZ admin sees "4/28/2026, 5:30:00 PM"; either can flip the
    // setting if they want the other format. Falls back to raw string
    // if Date parsing fails.
    formatDate(iso) {
      if (!iso) return '';
      try {
        const d = new Date(iso);
        if (isNaN(d.getTime())) return iso;
        return d.toLocaleString(this.serverLocale || 'en-GB', this.dateFormatOptions());
      } catch (e) {
        return iso;
      }
    },

    // ===== Webhook subsystem (M-Webhook foundation, logging-only today) =====

    // Loads the per-instance webhook configs for every Arr instance.
    // Called when the Webhooks page mounts + after wizard completion
    // / token rotation. Failures are logged but don't block the UI —
    // an instance whose GET fails just shows "not configured" and
    // works the next time the page reloads.
    //
    // Fetches in parallel via Promise.all — serialised awaits stack
    // latency on slow networks (5 instances × 50ms = 250ms wall).
    async loadWebhookSetupPage() {
      const insts = this.instances || [];
      // Defensive: if the user picked an instance for the activity tab
      // and that instance was deleted under Settings → Instances, the
      // dropdown selected-id stays dangling. Reset before re-render.
      if (this.webhookActivityInstanceId &&
          !insts.find(i => i.id === this.webhookActivityInstanceId)) {
        this.webhookActivityInstanceId = '';
      }
      const results = await Promise.all(insts.map(async inst => {
        try {
          const r = await this.apiFetch('/api/instances/' + inst.id + '/webhook');
          if (r.ok) {
            const d = await r.json();
            return [inst.id, d];
          }
        } catch (e) {
          // Tolerate per-instance failure — leave the entry unset
          // and let the UI render "not configured" for that one.
        }
        return null;
      }));
      const next = {};
      for (const r of results) {
        if (r) next[r[0]] = r[1];
      }
      // Whole-object replacement so Alpine v3's reactivity proxy
      // sees the change on EVERY key — nested-key writes don't
      // always trigger re-render when the key wasn't tracked yet
      // (the same trap that bit the Extra-tags toggle on M4).
      this.webhookConfigs = next;
      // Prefetch event lists for every CONFIGURED instance so the
      // Setup-tab "Last received" label reflects reality. Only the
      // ones with a token are worth fetching (untokenized rows can't
      // have received anything). Runs in parallel — slowest one
      // doesn't block the others.
      for (const inst of insts) {
        const cfg = next[inst.id];
        if (cfg && cfg.token) this.prefetchWebhookEventsForLabel(inst.id);
      }
      // Auto-pick the first instance OF THE ACTIVE app-type for the
      // Recent activity sub-tab if the user hasn't picked one yet —
      // saves a click on first visit. Honours the webhookAppType pill
      // so a user on the Sonarr pill auto-lands on Sonarr-1 even when
      // Radarr-1 is first in the global instance list.
      if (!this.webhookActivityInstanceId && insts.length > 0) {
        const t = this.webhookAppType || 'radarr';
        const first = insts.find(i => i.type === t) || insts[0];
        this.webhookActivityInstanceId = first.id;
        this.loadWebhookEvents(first.id);
      }
    },

    // Fetches recent events for one instance and caches them. Writes
    // to webhookEvents via spread-assign so Alpine reliably re-renders
    // when adding a new instanceId key (nested-key writes don't always
    // trigger updates in Alpine v3 — same trap class as Extra-tags).
    //
    // Also (re)starts the SSE stream for the picked instance so future
    // events arrive in real-time without further GET calls. Stream
    // disconnect happens when the user picks another instance, leaves
    // the Webhooks page, or closes the tab.
    async loadWebhookEvents(instanceId) {
      if (!instanceId) {
        this.stopWebhookEventStream();
        return;
      }
      this.webhookEventsLoading = true;
      try {
        let events = [];
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events');
        if (r.ok) {
          const d = await r.json();
          events = Array.isArray(d) ? d : [];
        }
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: events };
      } catch (e) {
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: [] };
      } finally {
        this.webhookEventsLoading = false;
      }
      this.startWebhookEventStream(instanceId);
    },

    // Lightweight events-only fetch (no SSE, no loading flag) used by
    // the Setup tab to populate webhookLastReceivedLabel for every
    // configured instance. Without this the label says "Never received"
    // for any instance the user hasn't opened in the Activity dropdown
    // yet, even when the server-side ring buffer has events. Failures
    // are silent — the label just stays at "Never" until the next
    // refresh.
    async prefetchWebhookEventsForLabel(instanceId) {
      if (!instanceId) return;
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events');
        if (!r.ok) return;
        const d = await r.json();
        const events = Array.isArray(d) ? d : [];
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: events };
      } catch (e) {
        // silent — label falls back to "Never received"
      }
    },

    // SSE-driven live updates for the Recent activity panel.
    // Replaces a polling alternative — the receiver knows when an
    // event arrives, so it pushes; browser doesn't have to ask.
    //
    // Owner of `_webhookEventSource` is this Alpine component. Only
    // one stream is alive at a time; switching instances closes the
    // old one. Browser's EventSource auto-reconnects on transient
    // network drop; we don't add a manual reconnect loop.
    startWebhookEventStream(instanceId) {
      this.stopWebhookEventStream();
      if (!instanceId) return;
      if (typeof EventSource === 'undefined') {
        // Old browser — no SSE support. Recent activity still
        // works via the GET endpoint + the ↻ button. No-op here.
        return;
      }
      try {
        const url = '/api/instances/' + instanceId + '/webhook/events/stream';
        const es = new EventSource(url);
        es.addEventListener('webhook', (msgEvent) => {
          this._handleWebhookSseEvent(instanceId, msgEvent.data);
        });
        // No reconnect spam — EventSource handles transient drops
        // itself. We only log the FIRST onerror so dev consoles
        // aren't full of "EventSource failed" reconnect noise.
        es.onerror = () => {
          // Browser will auto-reconnect; no action needed.
        };
        this._webhookEventSource = es;
        this._webhookEventSourceInstanceId = instanceId;
      } catch (e) {
        // Mostly defensive — `new EventSource` only throws on
        // a malformed URL, which we control. Swallow + leave the
        // panel in poll-via-↻ mode.
      }
      // Start the 10-second polling safety-net regardless of SSE
      // outcome — proxies sometimes silently buffer SSE responses, or
      // EventSource fails to reconnect after long idle. Polling makes
      // sure the panel reflects new events even when push is broken.
      this.startWebhookEventPolling(instanceId);
    },

    stopWebhookEventStream() {
      if (this._webhookEventSource) {
        try { this._webhookEventSource.close(); } catch (e) { /* ignore */ }
      }
      this._webhookEventSource = null;
      this._webhookEventSourceInstanceId = null;
      // Stop polling fallback too — paired lifecycle with the SSE stream.
      if (this._webhookEventPollHandle) {
        clearInterval(this._webhookEventPollHandle);
        this._webhookEventPollHandle = null;
      }
    },

    // startWebhookEventPolling is a safety-net fallback that runs
    // alongside the SSE stream. SSE is the primary push channel, but
    // it's fragile in real deployments — reverse proxies (SWAG /
    // nginx) sometimes buffer event-stream responses, or drop the
    // connection silently after the heartbeat window. Without a poll,
    // the user has to click ↻ to see new events.
    //
    // 10-second poll is cheap (the GET endpoint returns a JSON list
    // capped at 100 entries) and small enough that it doesn't fight
    // SSE — when SSE works, the poll just re-fetches the same list
    // we already have. The Activity panel re-renders against the new
    // list (Alpine spread-assign on webhookEvents).
    //
    // Stops cleanly via stopWebhookEventStream — paired lifecycle.
    startWebhookEventPolling(instanceId) {
      if (this._webhookEventPollHandle) {
        clearInterval(this._webhookEventPollHandle);
      }
      if (!instanceId) return;
      this._webhookEventPollHandle = setInterval(async () => {
        // Stale-tab guard: if the user navigated away the instance
        // dropdown will have changed; skip in that case.
        if (instanceId !== this._webhookEventSourceInstanceId) return;
        try {
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events', {
            headers: { 'X-Skip-Login-Redirect': '1' },
          });
          if (r.ok) {
            const d = await r.json();
            if (Array.isArray(d)) {
              this.webhookEvents = { ...this.webhookEvents, [instanceId]: d };
            }
          }
        } catch (e) {
          // Network blip — next tick will retry. No user-facing error.
        }
        // Piggyback: refresh the rules list too. Each rule's History
        // field carries its per-fire summaries — without this call,
        // the per-rule History modal stays stale until the user
        // manually clicks Reload. Cheap GET (small JSON, capped at
        // a handful of rules per instance).
        try {
          await this.loadWebhookRules();
        } catch (e) {
          // loadWebhookRules already swallows errors silently.
        }
      }, 10000); // 10s
    },

    // Handler for one SSE 'webhook' event payload. Prepends the new
    // event to the in-memory list (newest-first). Trims to the
    // server-side cap (100) so the display matches the persisted
    // ring on disk. Spread-assign on webhookEvents to dodge the
    // Alpine v3 nested-key reactivity trap that caught us before.
    _handleWebhookSseEvent(instanceId, payload) {
      // Stale-stream guard: if the user already switched instances
      // between event-arrival and this handler firing, drop the
      // event. The new stream will deliver future events.
      if (instanceId !== this._webhookEventSourceInstanceId) return;
      let ev;
      try { ev = JSON.parse(payload); }
      catch (e) { return; }
      if (!ev || typeof ev !== 'object') return;
      // Defence-in-depth: server stream is per-instance, so
      // ev.instanceId should always equal instanceId. Drop
      // mismatches rather than silently land an event on the
      // wrong instance's panel.
      if (ev.instanceId && ev.instanceId !== instanceId) return;
      const cur = this.webhookEvents[instanceId] || [];
      const next = [ev].concat(cur).slice(0, 100);
      this.webhookEvents = { ...this.webhookEvents, [instanceId]: next };
    },

    // "Last: 2 min ago" / "Never received" status pill text. Pulls
    // from the per-instance event cache, falls back to "Never" when
    // the cache is empty (server-side ring would be empty too in
    // that case).
    webhookLastReceivedLabel(instanceId) {
      const events = this.webhookEvents[instanceId];
      if (!events || events.length === 0) return 'Never received an event';
      const newest = events[0]; // events are newest-first
      if (!newest || !newest.receivedAt) return '';
      try {
        const d = new Date(newest.receivedAt);
        const ageMs = Date.now() - d.getTime();
        if (ageMs < 60 * 1000) return 'Last: just now';
        if (ageMs < 60 * 60 * 1000) return 'Last: ' + Math.floor(ageMs / 60000) + ' min ago';
        if (ageMs < 24 * 60 * 60 * 1000) return 'Last: ' + Math.floor(ageMs / 3600000) + ' h ago';
        return 'Last: ' + Math.floor(ageMs / 86400000) + ' d ago';
      } catch (e) {
        return '';
      }
    },

    // Copy text to clipboard with a legacy fallback. The modern
    // navigator.clipboard API only works in secure contexts (HTTPS
    // or localhost) — most users run resolvarr on a LAN HTTP URL,
    // which fails the SecureContext check. The textarea +
    // execCommand('copy') path still works there. Returns true on
    // success.
    async copyToClipboard(text) {
      if (!text) return false;
      if (navigator.clipboard && window.isSecureContext) {
        try {
          await navigator.clipboard.writeText(text);
          return true;
        } catch (e) {
          // fall through to legacy
        }
      }
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      // Off-screen but in the layout so focus + select work in every
      // browser. position:fixed avoids triggering page scroll on
      // .focus() in iOS Safari.
      ta.style.position = 'fixed';
      ta.style.top = '0';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      let ok = false;
      try {
        ok = document.execCommand('copy');
      } catch (e) {
        ok = false;
      }
      document.body.removeChild(ta);
      return ok;
    },

    async copyWebhookUrl(instanceId) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.url) return;
      const ok = await this.copyToClipboard(cfg.url);
      this.showToast(
        ok ? 'Webhook URL copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Confirm + execute token rotation. New URL means the user has
    // to update Sonarr/Radarr's Connect entry; warn explicitly so
    // they don't lose the previous URL accidentally.
    async confirmRegenerateWebhookToken(instanceId, name) {
      const cfg = this.webhookConfigs[instanceId];
      const wasConfigured = !!(cfg && cfg.token);
      const arrName = (this.instances.find(i => i.id === instanceId) || {}).type === 'sonarr' ? 'Sonarr' : 'Radarr';
      // Rotation also rotates the Secret (Phase 2 Slice A) — surface
      // that in the confirm message so users with Require-signature on
      // know they'll need to re-paste the new Secret into Arr's
      // Connect password field too.
      const msg = wasConfigured
        ? 'The current URL will stop working immediately. You\'ll need to paste BOTH the new URL and the new Secret into ' + arrName + ' → Settings → Connect → your Webhook (Secret goes in the password field).'
        : 'A unique webhook URL + Secret will be generated for ' + name + '.';
      const title = wasConfigured
        ? 'Generate a new webhook URL AND new Secret for "' + name + '"?'
        : 'Generate webhook URL for "' + name + '"?';
      if (!await this.confirmDialog({
        title:       title,
        message:     msg,
        confirmText: wasConfigured ? 'Rotate' : 'Generate',
        kind:        wasConfigured ? 'warning' : 'default',
      })) return;
      this.regenerateWebhookToken(instanceId);
    },

    async regenerateWebhookToken(instanceId) {
      try {
        // No body — preserve LoggingEnabled. Backend reads it as
        // *bool and now distinguishes "field omitted" (preserve)
        // from "field present and false" (explicit disable). Old
        // behaviour silently flipped logging off on rotate.
        //
        // Backend also preserves RequireSignature across rotation,
        // so users in strict mode stay in strict mode — but their
        // Sonarr/Radarr config still has the OLD Secret in the
        // password field, so events will start 401-ing until they
        // re-paste. Toast surfaces this; the Setup-tab card's
        // Require-signature toggle is still visible for an emergency
        // downgrade to grace mode if the user wants events flowing
        // while they go update Arr's config.
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: d };
        if (d.requireSignature) {
          this.showToast(
            'New URL + Secret generated. Require-signature is ON — re-paste the new Secret into your Webhook password field in Sonarr/Radarr or events will be rejected.',
            'success',
          );
        } else {
          this.showToast('New webhook URL + Secret generated', 'success');
        }
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      }
    },

    // Confirm + delete a configured webhook. Clears the token so the
    // receiver path returns 404 for the previous URL, reverts the
    // row to "not configured" + drops the persisted Connect entry's
    // ability to reach us. Recent activity events are NOT wiped —
    // they're keyed by instance ID, not token, and the user has a
    // separate Clear log button if they want both.
    async confirmDeleteWebhook(instanceId, name) {
      const arr = (this.instances.find(i => i.id === instanceId) || {}).type === 'sonarr' ? 'Sonarr' : 'Radarr';
      const msg = 'The current URL will stop working immediately. To reconfigure later you\'ll need to:\n' +
        '  1. Generate a new webhook here.\n' +
        '  2. Update the URL in ' + arr + ' → Settings → Connect, or remove the Connect entry there.';
      if (!await this.confirmDialog({
        title:       'Remove the webhook for "' + name + '"?',
        message:     msg,
        confirmText: 'Remove',
        kind:        'danger',
      })) return;
      this.deleteWebhook(instanceId);
    },

    async deleteWebhook(instanceId) {
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook', {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Reflect the cleared state locally — empty the entry rather
        // than dropping the key, so x-text on the row sees a defined
        // (but empty) config and renders the "not configured" branch.
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { token: '', url: '', loggingEnabled: false },
        };
        this.showToast('Webhook removed', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

    // Toggle the LoggingEnabled flag for an existing webhook config.
    // Optimistic update — the checkbox flips immediately, server
    // confirms in the background. On failure we revert. Writes use
    // spread-assign on the parent object so Alpine reliably re-renders.
    async toggleWebhookLogging(instanceId, enabled) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.token) return;
      const prev = cfg.loggingEnabled;
      this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, loggingEnabled: enabled } };
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/logging', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ loggingEnabled: enabled }),
        });
        if (!r.ok) {
          const d = await r.json();
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        // Revert
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, loggingEnabled: prev } };
        this.showToast('Logging toggle failed: ' + e.message, 'error');
      }
    },

    // Confirm + clear the per-instance log. Idempotent on the
    // server but the confirm step matches the rest of the
    // destructive-action UX (delete tag, delete instance, etc.).
    async confirmClearWebhookEvents(instanceId) {
      if (!instanceId) return;
      const events = this.webhookEvents[instanceId] || [];
      if (events.length === 0) return;
      const inst = this.instances.find(i => i.id === instanceId);
      const name = inst ? inst.name : 'this instance';
      if (!await this.confirmDialog({
        title:       'Clear logged events?',
        message:     'Clear ' + events.length + ' logged event(s) for "' + name + '". The events are also removed from the on-disk log file.',
        confirmText: 'Clear',
        kind:        'danger',
      })) return;
      this.clearWebhookEventsApi(instanceId);
    },

    async clearWebhookEventsApi(instanceId) {
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events', {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json();
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: [] };
        this.showToast('Log cleared', 'success');
      } catch (e) {
        this.showToast('Clear failed: ' + e.message, 'error');
      }
    },

    // connectEventLabel maps the raw Connect event-type strings
    // (what the payload calls them) to the labels Sonarr/Radarr show
    // in their own UI — so the user sees the same word here that
    // they ticked in the Arr's Connect settings. Most notable: the
    // payload says "Download" for what the Arr UI calls "Import".
    //
    // Unknown event types pass through verbatim so future Arr
    // additions don't crash the UI. Also handles resolvarr's own
    // custom event types (qbit:torrentAdded).
    connectEventLabel(rawType) {
      if (!rawType) return '(unknown event)';
      switch (rawType) {
        case 'Grab':                return 'Grab';
        case 'Download':            return 'Import';
        case 'Upgrade':             return 'Upgrade';
        case 'Rename':              return 'Rename';
        case 'MovieAdded':          return 'Movie added';
        case 'MovieDelete':         return 'Movie deleted';
        case 'MovieFileDelete':     return 'Movie file deleted';
        case 'SeriesAdd':           return 'Series added';
        case 'SeriesDelete':        return 'Series deleted';
        case 'EpisodeFileDelete':   return 'Episode file deleted';
        case 'HealthIssue':         return 'Health issue';
        case 'HealthRestored':      return 'Health restored';
        case 'ApplicationUpdate':   return 'Application update';
        case 'ManualInteractionRequired': return 'Manual interaction required';
        case 'Test':                return 'Test';
        case 'qbit:torrentAdded':   return 'qBit torrent added';
        default:                    return rawType;
      }
    },

    // Build [label, rawType, count] triples from the per-instance event
    // cache, sorted by count desc. Drives the filter chip row on the
    // Recent activity sub-tab. Label is the user-facing version (so
    // "Download" appears as "Import" in chips); rawType is what the
    // filter compares against to keep the filter logic correct even
    // when labels collide between Arr versions.
    webhookEventTypeCounts(instanceId) {
      const events = this.webhookEvents[instanceId] || [];
      const counts = new Map();
      for (const ev of events) {
        const t = ev.eventType || '(unknown)';
        counts.set(t, (counts.get(t) || 0) + 1);
      }
      return Array.from(counts.entries()).sort((a, b) => b[1] - a[1]);
    },

    webhookEventsFiltered() {
      const events = this.webhookEvents[this.webhookActivityInstanceId] || [];
      let out = events;
      const q = (this.webhookActivitySearch || '').trim().toLowerCase();
      if (q) {
        out = out.filter(ev =>
          ((ev.title || '').toLowerCase().includes(q)) ||
          ((ev.subtitle || '').toLowerCase().includes(q)));
      }
      if (this.webhookEventFilter !== 'all') {
        out = out.filter(ev => ev.eventType === this.webhookEventFilter);
      }
      if (this.webhookOutcomeFilter && this.webhookOutcomeFilter !== 'all') {
        out = out.filter(ev => this.eventOutcomeFilterMatch(ev));
      }
      if ((this.webhookContentShapeFilter || []).length > 0) {
        out = out.filter(ev => this.eventContentShapeFilterMatch(ev));
      }
      return out;
    },

    // Toggle the expand-to-see-JSON state on a single event card.
    // Spread-assign so first-touch keys land cleanly on Alpine v3's
    // proxy (object-key writes are the same trap class that bit
    // webhookEvents / webhookConfigs).
    toggleWebhookEventExpanded(eventId) {
      const cur = !!this.webhookEventExpanded[eventId];
      this.webhookEventExpanded = { ...this.webhookEventExpanded, [eventId]: !cur };
    },

    // Pretty-print the raw event JSON for the expand-to-see view.
    // Falls back to showing the raw string when JSON.parse fails
    // (the receiver stamps unparseable bodies through with
    // eventType="(unparseable)"; pre-pretty-printing them as
    // raw text is the right behaviour).
    formatWebhookRaw(raw) {
      if (!raw) return '';
      // raw is a JSON value (object/array/etc.) decoded by Alpine
      // from the JSON response — stringify it pretty.
      try {
        return JSON.stringify(raw, null, 2);
      } catch (e) {
        return String(raw);
      }
    },

    // ===== Webhook setup details (URL + Secret) =====
    //
    // Toggles inline display of a configured webhook's URL + Secret on
    // the Setup-tab instance card. Lets the user copy the Secret after
    // initial wizard config without having to regenerate the token
    // (which would invalidate the URL Sonarr/Radarr currently has).

    toggleWebhookDetails(instanceId) {
      const cur = !!this.webhookDetailsExpanded[instanceId];
      this.webhookDetailsExpanded = { ...this.webhookDetailsExpanded, [instanceId]: !cur };
      // Auto-mask secret again when collapsing
      if (cur) {
        this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: false };
      }
    },
    toggleWebhookSecretReveal(instanceId) {
      const cur = !!this.webhookSecretRevealed[instanceId];
      this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: !cur };
    },
    webhookSecretDisplay(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const secret = cfg.secret || '';
      if (!secret) return '(not yet generated)';
      if (this.webhookSecretRevealed[instanceId]) return secret;
      // Masked: keep first 4 + last 4 chars so user can verify the
      // value matches what they pasted into Sonarr/Radarr without
      // exposing the whole secret in over-shoulder screenshots.
      if (secret.length <= 12) return '•'.repeat(secret.length);
      return secret.slice(0, 4) + '•'.repeat(Math.max(0, secret.length - 8)) + secret.slice(-4);
    },
    // rotateWebhookSecret asks the backend for a fresh Secret while
    // keeping the existing Token (URL stays valid in Sonarr/Radarr).
    // Use when the instance has no Secret (legacy migration), or when
    // the user explicitly wants to rotate after a suspected leak.
    //
    // No confirm modal for the "missing Secret" case (zero-impact —
    // there was nothing to invalidate); shows a confirm for explicit
    // rotation when a Secret already exists because Sonarr/Radarr
    // will need its password re-pasted.
    async rotateWebhookSecret(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const hadSecret = !!cfg.secret;
      if (hadSecret) {
        const ok = await this.confirmDialog({
          title:       'Rotate webhook Secret?',
          message:     "A new Secret will be generated. The URL in Sonarr/Radarr stays the same, but you must paste the new Secret into the Connect → Password field — until you do, events with the old Secret will be rejected if Require signature is on.",
          confirmText: 'Rotate',
          kind:        'warning',
        });
        if (!ok) return;
      }
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Patch the local cache so the details panel updates without
        // a full reload.
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, secret: d.secret, url: d.url } };
        // Auto-reveal so user can immediately copy it.
        this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: true };
        this.showToast(hadSecret ? 'Secret rotated — paste the new one into Sonarr/Radarr' : 'Secret generated — paste it into Sonarr/Radarr → Connect → Password', 'success');
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      }
    },

    async copyWebhookSecret(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const secret = cfg.secret || '';
      if (!secret) {
        this.showToast('No secret to copy', 'error');
        return;
      }
      const ok = await this.copyToClipboard(secret);
      this.showToast(ok ? 'Secret copied' : 'Copy failed — reveal + select manually', ok ? 'success' : 'error');
    },

    // ===== Recent Activity table renderers (Phase A3) =====
    //
    // Per-event-type helpers pull the relevant fields from raw payload
    // and produce display strings for the table. Backend's eventTitle/
    // eventSubtitle are coarse — these renderers go deeper and produce
    // a quality summary + outcome chips. Pure functions on the event
    // shape; safe to call in x-text bindings (no side effects).
    eventIcon(eventType) {
      switch (eventType) {
        case 'Grab':                return '🎯';
        case 'Download':            return '📥';
        case 'MovieFileDelete':
        case 'EpisodeFileDelete':   return '🗑';
        case 'Rename':              return '✏️';
        case 'qbit:torrentAdded':   return '📦';
        case 'HealthIssue':
        case 'HealthRestored':      return '🏥';
        case 'ApplicationUpdate':   return '⬆️';
        case 'ManualInteractionRequired': return '⚠️';
        case 'Test':                return '✨';
        default:                    return 'ⓘ';
      }
    },

    // eventQualitySummary builds the right-side compact summary like
    // "1080p WEB-DL · EAC3 5.1 · h264 · FLUX · 3.04 GiB".
    // Resilient against missing fields (older payloads, partial data).
    eventQualitySummary(ev) {
      const body = ev.raw || {};
      // For Download events, episodeFiles/movieFile has the canonical
      // info. For Grab events, release.releaseTitle is the source of
      // truth (mediainfo not yet known). For Delete, the old file's
      // quality is in episodeFile/movieFile.
      let parts = [];
      let file = null;
      if (Array.isArray(body.episodeFiles) && body.episodeFiles.length) {
        file = body.episodeFiles[0];
      } else if (body.episodeFile) {
        file = body.episodeFile;
      } else if (body.movieFile) {
        file = body.movieFile;
      }
      if (file) {
        if (file.quality) parts.push(file.quality);
        const mi = file.mediaInfo || {};
        if (mi.audioCodec) {
          const ch = mi.audioChannels ? ' ' + mi.audioChannels : '';
          parts.push(mi.audioCodec + ch);
        }
        if (mi.videoCodec) parts.push(mi.videoCodec);
        if (mi.videoDynamicRangeType) parts.push(mi.videoDynamicRangeType);
        if (file.releaseGroup) parts.push(file.releaseGroup);
        if (file.size) parts.push(this.formatBytes(file.size));
      } else if (body.release && body.release.releaseTitle) {
        // Grab event — no file yet. Just show the release title.
        return body.release.releaseTitle;
      }
      return parts.join(' · ');
    },

    // eventIndexerLine returns the indexer + downloader source-trail
    // string ("BHD → qBit-tv") shown on the expanded card. Empty when
    // both fields are missing.
    eventIndexerLine(ev) {
      const body = ev.raw || {};
      const indexer = (body.release && body.release.indexer) || '';
      const dlClient = body.downloadClient || '';
      if (indexer && dlClient) return indexer + ' → ' + dlClient;
      return indexer || dlClient || '';
    },

    // eventSceneName returns episodeFile.sceneName or movieFile.sceneName
    // for the expand-card display. Useful when comparing grab-name vs
    // import-name drift.
    eventSceneName(ev) {
      const body = ev.raw || {};
      if (Array.isArray(body.episodeFiles) && body.episodeFiles.length) {
        return body.episodeFiles[0].sceneName || '';
      }
      if (body.episodeFile) return body.episodeFile.sceneName || '';
      if (body.movieFile) return body.movieFile.sceneName || '';
      return '';
    },

    // eventFilePath returns the imported file's full path. Empty for
    // events that don't have a file (Grab, Test, Health).
    eventFilePath(ev) {
      const body = ev.raw || {};
      if (Array.isArray(body.episodeFiles) && body.episodeFiles.length) {
        return body.episodeFiles[0].path || '';
      }
      if (body.episodeFile) return body.episodeFile.path || '';
      if (body.movieFile) return body.movieFile.path || '';
      return '';
    },

    // formatBytes converts a byte count into a human-readable string
    // (KiB/MiB/GiB). Standard binary IEC prefixes.
    formatBytes(bytes) {
      if (!bytes || bytes < 1024) return (bytes || 0) + ' B';
      const units = ['KiB', 'MiB', 'GiB', 'TiB'];
      let v = bytes / 1024;
      let i = 0;
      while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
      return v.toFixed(2) + ' ' + units[i];
    },

    // eventOutcomeChips returns the per-rule outcome chips to render in
    // the Outcome column. Empty array → "no rule matched" placeholder
    // in the template.
    eventOutcomeChips(ev) {
      const outcomes = ev.outcomes || [];
      return outcomes.map(o => ({
        ruleId:   o.ruleId,
        ruleName: o.ruleName || '(unnamed rule)',
        status:   o.status || 'ok',
        changed:  !!o.changed,
        summary:  o.summary || '',
        symbol:   o.status === 'error'   ? '✗'
                : o.status === 'partial' ? '~'
                : o.changed              ? '✓'
                : '·',
        color:    o.status === 'error'   ? 'var(--accent-red)'
                : o.status === 'partial' ? 'var(--accent-orange)'
                : o.changed              ? 'var(--accent-green)'
                : 'var(--text-muted-secondary)',
      }));
    },

    // eventOutcomeFilterMatch returns true when the event's outcomes
    // satisfy the current outcome filter. Drives webhookEventsFiltered
    // when the outcome dropdown isn't "all".
    eventOutcomeFilterMatch(ev) {
      const outcomes = ev.outcomes || [];
      const f = this.webhookOutcomeFilter;
      if (!f || f === 'all') return true;
      if (f === 'no-rule')  return outcomes.length === 0;
      if (f === 'changed')  return outcomes.some(o => o.changed && o.status !== 'error');
      if (f === 'no-change') return outcomes.length > 0 && outcomes.every(o => !o.changed && o.status !== 'error');
      if (f === 'errors')   return outcomes.some(o => o.status === 'error' || o.status === 'partial');
      return true;
    },

    // eventContentShape classifies an event by what kind of content it
    // describes. Sonarr-quirk-aware:
    //
    //   - Grab events report ALL episodes the release covers in
    //     episodes[]. A season-pack grab has episodes.length=24 + no
    //     episodeFiles. → 'season-pack'.
    //
    //   - Download (Import) events fire ONCE PER EPISODE FILE. But for
    //     files imported AS PART OF a season pack, Sonarr lists ALL of
    //     the pack's episodes in episodes[] (not just the one in this
    //     file). The signal that distinguishes per-episode-from-pack
    //     from true multi-episode is episodeFiles[] — exactly one entry
    //     means the event is about that single file. So
    //     "episodes.length=24 + episodeFiles.length=1" = single episode
    //     import that happens to be from a pack, NOT a season-pack
    //     event in the user's sense.
    //
    //   - True multi-episode events are exceedingly rare. They'd be
    //     either a Grab (no files yet) or a special "multi-ep file"
    //     (e.g. an S01E01E02 mux) — both result in episodeFiles being
    //     empty OR a single file holding multiple episode IDs.
    //
    // Detection order:
    //   movie field         → 'movie'
    //   no episodes context → 'system'
    //   has 1+ episodeFiles → 'episode'   (single file, regardless of
    //                                       episodes[] count)
    //   episodes.length > 1 → 'season-pack' (Grab without files)
    //   else                → 'episode'
    eventContentShape(ev) {
      const body = ev.raw || {};
      if (body.movie) return 'movie';
      const epCount = Array.isArray(body.episodes) ? body.episodes.length : 0;
      if (epCount === 0 && !body.series) return 'system';
      const fileCount = Array.isArray(body.episodeFiles) ? body.episodeFiles.length : 0;
      if (fileCount >= 1) return 'episode';
      if (epCount > 1) return 'season-pack';
      return 'episode';
    },

    // eventContentShapeFilterMatch returns true when the event's shape
    // is in the active filter set, or when no shape filter is active.
    eventContentShapeFilterMatch(ev) {
      const f = this.webhookContentShapeFilter || [];
      if (f.length === 0) return true;
      return f.includes(this.eventContentShape(ev));
    },

    // Toggle a content-shape in the filter set. Click cycles a shape
    // in/out independently — multi-select semantics.
    toggleWebhookContentShape(shape) {
      const cur = this.webhookContentShapeFilter || [];
      const i = cur.indexOf(shape);
      if (i >= 0) {
        this.webhookContentShapeFilter = cur.filter(s => s !== shape);
      } else {
        this.webhookContentShapeFilter = [...cur, shape];
      }
    },

    // Returns the content-shape chips that make sense for the active
    // webhookAppType. Radarr can't produce episode / season-pack
    // events; Sonarr can't produce movie events. System (Test/Health
    // /Manual/qBit-add) applies regardless.
    activeWebhookContentShapes() {
      const all = [
        {id: 'episode',     label: '🎬 Episode',      help: 'Single-episode events (1 entry in episodes[])',     appType: 'sonarr'},
        {id: 'season-pack', label: '📚 Season pack',  help: 'Multi-episode grab — covers a full season',          appType: 'sonarr'},
        {id: 'movie',       label: '🎞 Movie',        help: 'Radarr events (movie field present)',                appType: 'radarr'},
        {id: 'system',      label: 'ⓘ System',         help: 'Test / Health / Manual / qBit-add / other',          appType: 'any'},
      ];
      const t = this.webhookAppType || 'radarr';
      return all.filter(s => s.appType === 'any' || s.appType === t);
    },

    // Auto-select the activity-instance picker when exactly one
    // instance exists for the active app-type. Saves a click when
    // the user has just one Sonarr OR one Radarr — common single-
    // host setups. Triggered on app-type pill change AND on initial
    // load. Doesn't clobber an existing selection.
    autoPickWebhookActivityInstance() {
      const candidates = this.webhookInstancesForAppType();
      if (candidates.length === 1) {
        // Only force when current selection isn't already among the
        // candidates — if user switched app-type pill and the
        // previous instance was a different type, replace it.
        const stillValid = candidates.some(i => i.id === this.webhookActivityInstanceId);
        if (!stillValid) {
          this.webhookActivityInstanceId = candidates[0].id;
          this.loadWebhookEvents(this.webhookActivityInstanceId);
        }
      } else if (candidates.length === 0) {
        this.webhookActivityInstanceId = '';
      }
    },

    // ===== Webhook configuration wizard =====
    //
    // Two-step (Choices → Summary), Arr-type-first layout matching
    // the QFA / Tag / Discover wizards. Today's only function pick
    // is "Enable logging"; later releases add per-function checkboxes
    // (release-group tag on import, DV detail on import, qBit S/E
    // tag on grab, etc.) that drive the Step-2 summary's "Connect
    // events to enable" list.

    openWebhookWizard() {
      // Seed Arr-type from the page-level pill (webhookAppType).
      // User picks the type out front via the pills, so the wizard
      // mirrors that choice rather than re-deriving from instance
      // pool. Wizard's internal Arr-type radios still let the user
      // flip mid-configuration, mostly for users who haven't seen
      // the pill yet.
      const insts = this.instances || [];
      let appType = this.webhookAppType || 'radarr';
      // Defence: if the picked app-type has no instances (shouldn't
      // happen since the pill is :disabled in that state, but
      // keyboard navigation could still trigger), fall back to
      // whichever type does have one.
      const hasPicked = insts.some(i => i.type === appType);
      if (!hasPicked) {
        const hasRadarr = insts.some(i => i.type === 'radarr');
        const hasSonarr = insts.some(i => i.type === 'sonarr');
        if (hasRadarr) appType = 'radarr';
        else if (hasSonarr) appType = 'sonarr';
      }
      // Seed instance: last-used remembered (when still in pool of
      // the picked appType — note: remembered may be from a different
      // type, in which case we ignore it) → first-of-type.
      const remembered = this.recallWizardInstance('webhook', insts.filter(i => i.type === appType));
      const firstOfType = insts.find(i => i.type === appType);
      const seedId = remembered || (firstOfType ? firstOfType.id : '');
      this.webhookWizard = {
        open: true,
        step: 0,
        appType,
        instanceId: seedId,
        fnLogging: true,
        busy: false,
        generatedUrl: '',
        generatedSecret: '',
        generatedLoggingEnabled: false,
        requireSignature: false,
      };
    },

    closeWebhookWizard() {
      if (this.webhookWizard.busy) return;
      this.webhookWizard.open = false;
    },

    webhookWizardVisibleSteps() {
      return ['Choices', 'Summary'];
    },

    // Reactive instance list for the Step 1 picker — filtered to
    // the chosen Arr type, sorted by name.
    webhookWizardInstancesForType() {
      return (this.instances || [])
        .filter(i => i.type === this.webhookWizard.appType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // When the user flips the Arr-type radio, re-seed instanceId
    // to the first instance of the new type (or empty when none).
    webhookWizardSetAppType(type) {
      this.webhookWizard.appType = type;
      const list = this.webhookWizardInstancesForType();
      this.webhookWizard.instanceId = list.length > 0 ? list[0].id : '';
    },

    webhookWizardCanAdvance() {
      const cur = this.webhookWizardVisibleSteps()[this.webhookWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have an instance picked + at least one
        // function ticked (today: logging is the only function so
        // this collapses to "fnLogging must be true").
        if (!this.webhookWizard.instanceId) return false;
        if (!this.webhookWizard.fnLogging) return false;
        return true;
      }
      return true;
    },

    webhookWizardPrev() {
      if (this.webhookWizard.step > 0) this.webhookWizard.step--;
    },

    // Advance from Choices → Summary. This is where we actually
    // mutate config: persist the function picks (Logging) and,
    // if the instance has no token yet, generate one. Existing
    // tokens are preserved so a user re-running the wizard for an
    // already-configured instance doesn't accidentally rotate.
    async webhookWizardNext() {
      if (!this.webhookWizardCanAdvance()) return;
      const cur = this.webhookWizardVisibleSteps()[this.webhookWizard.step];
      if (cur === 'Choices') {
        await this.webhookWizardCommit();
        return;
      }
      // No further forward steps today; Summary is terminal (footer
      // shows Close instead of Next).
    },

    async webhookWizardCommit() {
      const { instanceId, fnLogging } = this.webhookWizard;
      // Remember the picked instance for next time the wizard opens.
      if (instanceId) this.rememberWizardInstance('webhook', instanceId);
      this.webhookWizard.busy = true;
      try {
        // If instance already has a token, just update the logging
        // flag. Otherwise rotate to generate one. Both calls return
        // a JSON body containing { token, url, loggingEnabled }.
        const existing = this.webhookConfigs[instanceId];
        let result;
        if (existing && existing.token) {
          // Toggle logging only — token preserved. Then re-fetch the
          // full config so URL stays computed against the current
          // request host.
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/logging', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ loggingEnabled: !!fnLogging }),
          });
          if (!r.ok) {
            const d = await r.json();
            throw new Error(d.error || 'HTTP ' + r.status);
          }
          // Re-fetch to get the URL (the logging endpoint doesn't
          // return it). On failure surface the error rather than
          // silently advancing with the cached URL — that URL was
          // computed against a previous request's host header and
          // could be stale if the user moved the container behind
          // a different reverse proxy.
          const g = await this.apiFetch('/api/instances/' + instanceId + '/webhook');
          if (!g.ok) {
            const d = await g.json().catch(() => ({}));
            throw new Error(d.error || 'fetch URL: HTTP ' + g.status);
          }
          result = await g.json();
        } else {
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ loggingEnabled: !!fnLogging }),
          });
          const d = await r.json();
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          result = d;
        }
        // Mirror into the page-level cache via spread-assign so the
        // Setup sub-tab sees the new state without a manual refresh
        // and Alpine reliably re-renders even on first-write to a
        // previously-untracked instance key.
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: result };
        this.webhookWizard.generatedUrl = result.url || '';
        // Phase 2 Slice A: rotate response includes the new Secret +
        // preserved RequireSignature flag. handleWebhookSetLogging's
        // response doesn't carry these, but we re-fetched via GET
        // /webhook above when the token already existed, so the
        // result has them in both branches.
        this.webhookWizard.generatedSecret = result.secret || '';
        this.webhookWizard.requireSignature = !!result.requireSignature;
        this.webhookWizard.generatedLoggingEnabled = !!result.loggingEnabled;
        this.webhookWizard.step = 1;
      } catch (e) {
        this.showToast('Webhook setup failed: ' + e.message, 'error');
      } finally {
        this.webhookWizard.busy = false;
      }
    },

    // Connect-events-to-enable matrix for the Summary step. Today
    // logging is everything-mode — Sonarr/Radarr should toggle ALL
    // event types so resolvarr can capture them. When functions
    // land, this list will whittle to only the events the picked
    // functions need.
    webhookWizardArrEvents() {
      const isSonarr = this.webhookWizard.appType === 'sonarr';
      // Matches Sonarr's / Radarr's actual checkbox labels in their
      // Connect → Webhook config form so users can match them
      // 1:1 in the Summary step.
      if (isSonarr) {
        return [
          'On Test (verify connectivity)',
          'On Grab',
          'On Import (Download)',
          'On Upgrade (Download — upgrade)',
          'On Episode File Delete',
          'On Series Add',
          'On Series Delete',
          'On Health Issue (optional)',
          'On Health Restored (optional)',
          'On Application Update (optional)',
        ];
      }
      return [
        'On Test (verify connectivity)',
        'On Grab',
        'On Import',
        'On Upgrade',
        'On Rename',
        'On Movie Added',
        'On Movie Delete',
        'On Movie File Delete',
        'On Health Issue (optional)',
        'On Health Restored (optional)',
        'On Application Update (optional)',
      ];
    },

    // Copy the wizard's generated URL.
    async copyWebhookWizardUrl() {
      if (!this.webhookWizard.generatedUrl) return;
      const ok = await this.copyToClipboard(this.webhookWizard.generatedUrl);
      this.showToast(
        ok ? 'URL copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Copy the wizard's generated Secret. Same UX pattern as
    // copyWebhookWizardUrl — toast on success/failure. The Secret is
    // the shared password the user pastes into Sonarr/Radarr → Connect
    // → Webhook → password. Phase 2 Slice A.
    async copyWebhookWizardSecret() {
      if (!this.webhookWizard.generatedSecret) return;
      const ok = await this.copyToClipboard(this.webhookWizard.generatedSecret);
      this.showToast(
        ok ? 'Secret copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Toggle the Require-signature flag from within the wizard's
    // Summary step. Optimistic update with revert-on-failure, same
    // pattern as toggleWebhookLogging. Backend validator rejects
    // {enabled:true} with 400 when stored Secret is empty — we
    // shouldn't hit that here because the wizard reached Summary by
    // generating both Token + Secret, but we surface the error
    // through the toast just in case. Phase 2 Slice B.
    async toggleWebhookWizardRequireSignature(enabled) {
      const instanceId = this.webhookWizard.instanceId;
      if (!instanceId) return;
      const prev = this.webhookWizard.requireSignature;
      this.webhookWizard.requireSignature = enabled;
      try {
        const r = await this.apiFetch(
          '/api/instances/' + instanceId + '/webhook/require-signature',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled }),
          },
        );
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Mirror the new flag into the page-level cache so the
        // Setup-tab card pill stays in sync without a refresh.
        const cur = this.webhookConfigs[instanceId] || {};
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { ...cur, requireSignature: enabled },
        };
        this.showToast(
          enabled
            ? 'Require signature on — events without the Secret will be rejected'
            : 'Require signature off — events without the Secret pass with a warning',
          'success',
        );
      } catch (e) {
        // Revert optimistic update.
        this.webhookWizard.requireSignature = prev;
        this.showToast('Require-signature toggle failed: ' + e.message, 'error');
      }
    },

    // Per-instance Require-signature toggle from the Setup tab's
    // instance card. Same backend endpoint; this just lives outside
    // the wizard so users can flip strict mode anytime. Phase 2
    // Slice B.
    async toggleWebhookRequireSignature(instanceId, enabled) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.token) return;
      const prev = !!cfg.requireSignature;
      this.webhookConfigs = {
        ...this.webhookConfigs,
        [instanceId]: { ...cfg, requireSignature: enabled },
      };
      try {
        const r = await this.apiFetch(
          '/api/instances/' + instanceId + '/webhook/require-signature',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled }),
          },
        );
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        // Revert.
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { ...cfg, requireSignature: prev },
        };
        this.showToast('Require-signature toggle failed: ' + e.message, 'error');
      }
    },

    // formatBytes renders a byte count in B / KB / MB / GB with one
    // decimal place. Used for the DV cache file-size display. KiB-style
    // 1024-base for "what the OS reports" matches du/ls expectations.
    formatBytes(n) {
      if (n == null || isNaN(n)) return '0 B';
      if (n < 1024) return n + ' B';
      if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
      if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
      return (n / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    },

    // dateFormatOptions builds the Intl.DateTimeFormat options object
    // shared by formatDate + scheduleNextFires + scheduleNextRun so
    // every timestamp in the UI honours the same Settings → Display
    // preference. Only sets hour12 when the user explicitly picks one
    // — passing undefined hour12 + a locale lets Intl pick the
    // locale-native default, which is what the "auto" choice means.
    dateFormatOptions() {
      const opts = {};
      if (this.serverTimezone) opts.timeZone = this.serverTimezone;
      if (this.timeFormat === '24h') opts.hour12 = false;
      else if (this.timeFormat === '12h') opts.hour12 = true;
      return opts;
    },

    // highlightMatchTokens wraps tokens that drove the filter pass in
    // CSS-class spans so the user can see at a glance WHY a movie
    // qualified. Three classes (palette + style live in components.css):
    //   - .tok-rg      release group (blue, matched as `-<RG>` or bare)
    //   - .tok-quality quality-filter tokens (MA WEB-DL, Play WEB-DL)
    //   - .tok-audio   audio-filter tokens (TrueHD, Atmos, DTS-X, DTS-HD MA)
    //
    // Order matters: longer patterns first so DTS-HD MA isn't partially
    // matched by DTS alone. Word boundaries via \b avoid coloring tokens
    // mid-word (e.g. "DTSomething" wouldn't match \bDTS\b).
    //
    // We HTML-escape the input first so user-controlled scene/path strings
    // can't inject markup; the wrapping <span> tags are added on top of
    // the escaped text. Search term is escaped for regex too — release
    // groups like `126811` are safe but `[GROUP]` would otherwise blow up.
    //
    // Note: the spans use class= rather than inline style so the design
    // tokens (palette, dotted-underline vs filled-bg, etc.) live in CSS.
    // One stylesheet update changes every panel that calls this helper.
    highlightMatchTokens(text, releaseGroup, opts) {
      if (!text) return '';
      const options = opts || {};
      // Escape HTML
      let html = String(text)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');

      const wrap = (re, cls) => {
        html = html.replace(re, (m) => `<span class="${cls}">${m}</span>`);
      };

      // Quality + audio tokens are filter-decision context — only useful
      // where the user is checking whether a release passes the engine's
      // filter set (scan results, discover samples). Compare-tags and the
      // tag-inventory drill-down don't care about filter passage; they're
      // listing tag membership. opts.releaseGroupOnly suppresses these
      // wraps so those views stay clean (just the blue tag-label highlight).
      if (!options.releaseGroupOnly) {
        // Audio tokens — longest first so DTS-HD MA isn't pre-eaten.
        wrap(/\bDTS[._\-\s]?HD[._\-\s]?MA\b/gi, 'tok-audio');
        wrap(/\bDTS[._\-\s]?X\b/gi, 'tok-audio');
        wrap(/\bAtmos\b/gi, 'tok-audio');
        wrap(/\bTrueHD\b/gi, 'tok-audio');

        // Quality tokens — bash check_quality_match uses these regexes:
        //   MA WEB-DL  : \bma(\]?\s*\[?|[._-])web([-.]?dl)?
        //   Play WEB-DL: \bplay(\]?\s*\[?|[._-])web([-.]?dl)?
        wrap(/\bMA([._\-\s]|\][\s_]?\[?)WEB(?:[._\-]?DL)?\b/gi, 'tok-quality');
        wrap(/\bPlay([._\-\s]|\][\s_]?\[?)WEB(?:[._\-]?DL)?\b/gi, 'tok-quality');
      }

      // Release group — match `-<RG>` (the typical "filename-FLUX" form)
      // AND bare `<RG>` (the case where the value IS the release group,
      // e.g. the releaseGroup: field on its own). Optional dash prefix
      // + word boundaries keeps it from matching mid-token. Escape
      // regex metacharacters in the group name to be safe.
      //
      // releaseGroup may be a string (single RG, the typical case) or an
      // array (used by compare-tags where two tag labels both want
      // highlighting in the same file-context block).
      const rgs = Array.isArray(releaseGroup) ? releaseGroup : (releaseGroup ? [releaseGroup] : []);
      for (const rg of rgs) {
        if (!rg) continue;
        const rgEsc = rg.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        wrap(new RegExp('-?\\b' + rgEsc + '\\b', 'gi'), 'tok-rg');
      }

      return html;
    },

    // Returns the subset of scanResult.items that match the current filter
    // chip. 'all' returns everything; the action filters return items that
    // contain at least one decision with the matching action. 'nofile'
    // returns items with no decisions (which indicates the backend skipped
    // them because MovieFile was nil).
    filteredScanItems() {
      // Legacy filter — kept for the no-file flow + filter-counts. The
      // group-first display uses decisionsByGroup() instead, which only
      // covers items that have decisions (no-file items by definition
      // don't appear under any group).
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter === 'all') return r.items;
      if (this.scanFilter === 'nofile') return r.items.filter(i => !i.decisions || i.decisions.length === 0);
      return r.items.filter(item => (item.decisions || []).some(d => d.action === this.scanFilter));
    },

    // No-file items shown only when filter is "nofile" — they don't fit
    // any release-group bucket since they have no decisions.
    noFileScanItems() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter !== 'nofile') return [];
      return r.items.filter(i => !i.decisions || i.decisions.length === 0);
    },

    // Missing-in-secondary items (sync mode only). One row per primary
    // movie that has no matching TmdbID in secondary — `secondaryAction`
    // is identical across all groups for the same item (the movie is
    // either present in secondary or not), so we dedupe by movie ID.
    missingScanItems() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter !== 'missing') return [];
      const seen = new Set();
      const out = [];
      for (const item of r.items) {
        if (!item.decisions) continue;
        const isMissing = item.decisions.some(d => d.secondaryAction === 'missing');
        if (isMissing && !seen.has(item.id)) {
          seen.add(item.id);
          out.push(item);
        }
      }
      return out;
    },

    // Counts per action across the tag result. Respects scanInstanceFilter:
    //   'primary'   → only primary action counted
    //   'secondary' → only secondary action counted (secondary has no nofile concept)
    //   'both'      → counts EITHER side that matches (so a movie matching primary
    //                 'add' AND secondary 'add' is counted twice — that's
    //                 intentional, both are real actions to apply).
    // Also respects orphan-pass-only contributions to secondary remove counts.
    // Switches the visible-instance filter and re-picks the status chip
    // if the new filter would orphan it (e.g. scanFilter='missing' is
    // a secondary-only concept, scanFilter='nofile' is a primary-only
    // concept — when scanInstanceFilter flips, the chip can become
    // incompatible). Re-pick keeps the user on a valid chip without
    // forcing them to click again.
    setScanInstanceFilter(next) {
      this.scanInstanceFilter = next;
      const f = this.scanFilter;
      // Hidden-chip cases per index.html @x-show conditions:
      //   No file: hidden when filter=secondary
      //   Missing: hidden when filter=primary
      if ((f === 'nofile' && next === 'secondary') || (f === 'missing' && next === 'primary')) {
        this.scanFilter = this.pickDefaultScanFilter();
      }
    },

    // Pick the most useful default filter chip based on the response.
    // Action buckets (add/remove) win when they have anything, since the
    // user typically came to act on changes. When all action buckets are
    // empty (common: QFA against already-synced library, returned 0/0),
    // fall back to whichever non-action bucket has the most items so the
    // user lands on data instead of an empty filter. Honors the active
    // scanInstanceFilter so the counts reflect what's currently visible.
    pickDefaultScanFilter() {
      const c = this.scanFilterCounts();
      if (c.add > 0) return 'add';
      if (c.remove > 0) return 'remove';
      // No actionable change. Pick the largest of keep / nofile / missing.
      const r = this.scanResults.tag;
      const missing = (r && r.totals && r.totals.secondaryMissing) || 0;
      const candidates = [
        { name: 'keep',    count: c.keep },
        { name: 'nofile',  count: c.nofile },
        { name: 'missing', count: missing },
      ];
      candidates.sort((a, b) => b.count - a.count);
      return candidates[0].count > 0 ? candidates[0].name : 'add';
    },

    scanFilterCounts() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return { add: 0, remove: 0, keep: 0, nofile: 0 };
      const fil = this.scanInstanceFilter;
      let add = 0, remove = 0, keep = 0, nofile = 0;
      for (const item of r.items) {
        if (!item.decisions || item.decisions.length === 0) {
          if (fil !== 'secondary') nofile++;
          continue;
        }
        for (const d of item.decisions) {
          if (fil === 'primary' || fil === 'both') {
            if (d.action === 'add') add++;
            else if (d.action === 'remove') remove++;
            else if (d.action === 'keep') keep++;
          }
          if (fil === 'secondary' || fil === 'both') {
            if (d.secondaryAction === 'add') add++;
            else if (d.secondaryAction === 'remove') remove++;
            else if (d.secondaryAction === 'keep') keep++;
          }
        }
      }
      return { add, remove, keep, nofile };
    },

    // Reorganize per-movie items into per-group buckets. Each group row
    // collapses N decisions across the library into one expandable entry,
    // matching Discover's UI pattern. Filtered to actions matching the
    // current scanFilter (or all actions when scanFilter is "all").
    //
    // Each bucket carries:
    //   group: { id, tag, display }
    //   items: [{ ...item fields, action, matched, matchLocation,
    //             quality, qualityDetail, audio, audioDetail, reason }]
    //   totals: { add, remove, keep, skip }  (full per-group totals,
    //                                          unfiltered — for the chip)
    //
    // Sort: groups by tag-label alphabetical. Within a group, items by
    // movie title.
    decisionsByGroup() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      const filter = this.scanFilter;
      const instFil = this.scanInstanceFilter;
      // No-file rows handled in noFileScanItems(), missing handled separately.
      if (filter === 'nofile' || filter === 'missing') return [];
      const buckets = new Map();
      for (const item of r.items) {
        if (!item.decisions || item.decisions.length === 0) continue;
        for (const d of item.decisions) {
          let b = buckets.get(d.groupId);
          if (!b) {
            b = {
              group: { id: d.groupId, tag: d.groupTag, display: d.groupDisplay },
              items: [],
              totals: { add: 0, remove: 0, keep: 0, skip: 0 },
            };
            buckets.set(d.groupId, b);
          }
          b.totals[d.action] = (b.totals[d.action] || 0) + 1;
          // Decide whether this (item, decision) matches the chosen filter,
          // taking instance-filter into account. For 'both', either side
          // matching qualifies the row for inclusion.
          let matchesFilter = false;
          if (instFil === 'primary' || instFil === 'both') {
            if (d.action === filter) matchesFilter = true;
          }
          if (!matchesFilter && (instFil === 'secondary' || instFil === 'both')) {
            if (d.secondaryAction === filter) matchesFilter = true;
          }
          if (matchesFilter) {
            b.items.push({
              ...item,
              action: d.action,
              matched: d.matched,
              matchLocation: d.matchLocation,
              quality: d.quality,
              qualityDetail: d.qualityDetail,
              audio: d.audio,
              audioDetail: d.audioDetail,
              reason: d.reason,
              secondaryAction: d.secondaryAction,
              secondaryHasTag: d.secondaryHasTag,
            });
          }
        }
      }
      // Drop groups with no items after the filter — keeps the screen
      // tight when the user picks "Add" and most groups have only Keeps.
      const out = [];
      for (const b of buckets.values()) {
        if (b.items.length === 0) continue;
        b.items.sort((a, c) => a.title.localeCompare(c.title, undefined, { sensitivity: 'base' }));
        out.push(b);
      }
      out.sort((a, b) => a.group.tag.localeCompare(b.group.tag, undefined, { sensitivity: 'base' }));
      return out;
    },

    toggleScanRowExpanded(itemId) {
      const next = { ...this.scanRowExpanded };
      if (next[itemId]) delete next[itemId];
      else next[itemId] = true;
      this.scanRowExpanded = next;
    },

    toggleScanGroupExpanded(groupId) {
      const next = { ...this.scanGroupExpanded };
      if (next[groupId]) delete next[groupId];
      else next[groupId] = true;
      this.scanGroupExpanded = next;
    },

    iconUrl(type, variant) {
      if (type === 'radarr') return variant === '4k' ? '/icons/radarr4kNew.png' : '/icons/radarrNew.png';
      return variant === '4k' ? '/icons/sonarr4k.png' : '/icons/sonarr.png';
    },

    async refreshAllStatus() {
      for (const inst of this.instances) {
        // Don't overwrite a 'testing' that the user just triggered
        if (this.instStatus[inst.id] === 'testing') continue;
        this.testInstance(inst, true);
      }
    },

    async loadConfig() {
      try {
        const r = await this.apiFetch('/api/config');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const cfg = await r.json();
        this.instances = cfg.instances || [];
        // Reconcile picker state — if the previously-chosen cross-instance
        // compare target was deleted (or had its type changed) since the
        // last config load, clear it so the user doesn't see stale UI or
        // hit "Instance not found" on the next Compare click. Same pattern
        // already applied to tagsInstanceId in init() at app.js:627.
        if (this.compareCrossInstanceTarget) {
          const t = this.currentInstanceType();
          const stillThere = this.instances.find(
            i => i.id === this.compareCrossInstanceTarget && i.type === t && i.id !== this.tagsInstanceId
          );
          if (!stillThere) this.compareCrossInstanceTarget = '';
        }
        this.discord = cfg.discord || { enabled: false, webhookUrl: '' };
        this.uiScale = (cfg.display && cfg.display.uiScale) || '1.1';
        this.timeFormat = (cfg.display && cfg.display.timeFormat) || 'auto';
        this.groups = cfg.releaseGroups || [];
        // FilterSet has per-Arr-type structure (cfg.filters.radarr +
        // cfg.filters.sonarr). The Filters sub-tab only renders one set
        // today — UI-side mirroring of per-type filters lands with the
        // M-Sonarr per-episode walker. Until then we read/write the
        // Radarr block, which matches the active scan type for every
        // shipped feature (Radarr-only handlers).
        const f = (cfg.filters && cfg.filters.radarr) || {};
        this.filters = {
          quality: f.Quality !== false,
          maWebDL: f.MAWebDL !== false,
          playWebDL: f.PlayWebDL !== false,
          audio: f.Audio !== false,
          trueHD: f.TrueHD !== false,
          trueHDAtmos: f.TrueHDAtmos !== false,
          dtsX: f.DTSX !== false,
          dtsHDMA: f.DTSHDMA !== false,
        };
        // Snapshot the Sonarr block so we can round-trip it through
        // saveFilters without the user's checkbox toggles wiping the
        // Sonarr-side defaults that Load() backfilled.
        this._savedSonarrFilters = (cfg.filters && cfg.filters.sonarr) || null;
        // Audio + Video tags load from their own endpoints (each ships
        // {config, ...vocab} together so the UI can render the closed-
        // vocab checkbox matrix without hardcoding values).
        await this.loadAudioTags();
        await this.loadVideoTags();
        await this.loadDvDetail();
        // Best-effort tools status — ignore errors so a 503 (DV not wired
        // in dev mode) doesn't trip the toast banner.
        try { await this.loadDvToolsStatus(); } catch (_) {}
        try { await this.loadDvCacheStats(); } catch (_) {}
      } catch (e) {
        this.showToast('Load failed: ' + e.message, 'error');
      }
    },

    // --- DV detail (M4b) ---
    async loadDvDetail() {
      try {
        // GET — CSRF doesn't gate it but use apiFetch for consistency.
        const r = await this.apiFetch('/api/dv-detail');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config) {
          this.dvDetail.enabled = !!data.config.enabled;
          this.dvDetail.prefix = data.config.prefix || '';
          this.dvDetail.allowedValues = Array.isArray(data.config.allowedValues) ? data.config.allowedValues : [];
          this.dvDetail.selectMode = data.config.selectMode || '';
          this.dvDetail.labels = (data.config.labels && typeof data.config.labels === 'object') ? { ...data.config.labels } : {};
          this.dvDetail.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.vocabulary)) {
          this.dvDetailVocab = data.vocabulary;
        }
      } catch (e) {
        // Silent — config-load is best-effort. saveDvDetail will surface
        // network errors if the user actually tries to write.
      }
    },

    async saveDvDetail() {
      // Same pattern as saveExtraTags: PUT the full object (server treats
      // it as wholesale replacement). Validation errors come back as 400
      // with a clear message — surface as a toast.
      // apiFetch (not raw fetch) — CSRF middleware rejects PUT without
      // the X-CSRF-Token header, same trap that bit the DV-tools handlers.
      try {
        const r = await this.apiFetch('/api/dv-detail', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            enabled: !!this.dvDetail.enabled,
            prefix: this.dvDetail.prefix || '',
            allowedValues: this.dvDetail.allowedValues,
            selectMode: this.dvDetail.selectMode || '',
            labels: this.dvDetail.labels || {},
            removeOrphanedTags: !!this.dvDetail.removeOrphanedTags,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          this.showToast('DV detail save failed: ' + (err.error || r.status), 'error');
          return;
        }
      } catch (e) {
        this.showToast('DV detail save failed: ' + e.message, 'error');
      }
    },

    // toggleDvDetailValue uses the same two-mode semantics as the audio
    // and video bucket toggles — see audioTagValueChecked for the full
    // SelectMode rationale. Default mode treats empty=all; first per-
    // value click switches to "select" mode using the full vocab as
    // baseline so the unchecked one disappears as expected.
    toggleDvDetailValue(value) {
      const fullVocab = [...this.dvDetailVocab];
      if (this.dvDetail.selectMode !== 'select') {
        if (!this.dvDetail.allowedValues || this.dvDetail.allowedValues.length === 0) {
          this.dvDetail.allowedValues = [...fullVocab];
        }
        this.dvDetail.selectMode = 'select';
      }
      const av = this.dvDetail.allowedValues || [];
      const idx = av.indexOf(value);
      if (idx >= 0) av.splice(idx, 1);
      else          av.push(value);
      const order = (v) => this.dvDetailVocab.indexOf(v);
      av.sort((a, b) => order(a) - order(b));
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        this.dvDetail.selectMode = '';
        this.dvDetail.allowedValues = [];
      } else {
        this.dvDetail.allowedValues = av;
      }
      this.saveDvDetail();
    },
    selectAllDvDetailValues() {
      this.dvDetail.selectMode = '';
      this.dvDetail.allowedValues = [];
      this.saveDvDetail();
    },
    selectNoneDvDetailValues() {
      this.dvDetail.selectMode = 'select';
      this.dvDetail.allowedValues = [];
      this.saveDvDetail();
    },

    isDvDetailValueAllowed(value) {
      const av = this.dvDetail.allowedValues || [];
      if (this.dvDetail.selectMode !== 'select' && av.length === 0) return true;
      return av.includes(value);
    },

    async loadDvToolsStatus() {
      try {
        // GET — CSRF doesn't gate it but use apiFetch for consistency
        // with the rest of the codebase + any future auth wiring.
        const r = await this.apiFetch('/api/tools/dv/status');
        if (r.status === 503) {
          // AttachDV not called server-side — dev/test build, not a
          // production deployment. Banner stays in default
          // {installed:false} state which shows the [Install] CTA;
          // the user clicking Install will get a 503 from the install
          // endpoint with a clear "DV tools not configured" message.
          // Console.warn so a dev running locally sees the wiring gap.
          // eslint-disable-next-line no-console
          console.warn('DV tools status endpoint returned 503 — AttachDV likely not wired in main; install button will fail with 503 too.');
          return;
        }
        if (!r.ok) return;
        this.dvTools = await r.json();
      } catch (e) {
        // Silent on poll failure; banner just stays as-is.
      }
    },

    // installDvTools / uninstallDvTools removed — DV tools (ffmpeg +
    // dovi_tool) ship baked into the image as of v0.3.5 via the
    // Dockerfile dv-tools stage. No env var, no install step.
    // loadDvToolsStatus stays as a defensive health check for the
    // "Tools unreachable" UI branch (only fires if the image build
    // is somehow broken — should never happen in normal CI).

    // --- DV cache panel (Library scan → DV detail tab) ---
    // GET /api/dv-cache/stats. Server returns zero-valued struct when
    // DvCache is nil (defensive — shouldn't happen in normal deploys
    // but covered). UI renders "No files cached yet" copy in that case.
    async loadDvCacheStats() {
      try {
        const r = await this.apiFetch('/api/dv-cache/stats');
        if (!r.ok) return;
        this.dvCacheStats = await r.json();
      } catch (_) {
        // Silent — panel just shows the empty-state copy.
      }
    },
    // Open the confirm modal. Stats are already loaded; if not, we
    // still open the modal so the user can see "0 cached files" and
    // the Clear button is naturally disabled.
    openClearDvCacheConfirm() {
      this.showClearDvCacheConfirm = true;
    },
    // Fire the DELETE. Server wipes in-memory + persists empty file
    // and returns the post-clear stats so we don't need a follow-up
    // GET. Toast on success/failure.
    async confirmClearDvCache() {
      if (this.clearingDvCache) return;
      this.clearingDvCache = true;
      try {
        const r = await this.apiFetch('/api/dv-cache', { method: 'DELETE' });
        if (!r.ok) {
          let msg = 'HTTP ' + r.status;
          try { msg = (await r.json()).error || msg; } catch (_) {}
          throw new Error(msg);
        }
        this.dvCacheStats = await r.json();
        this.showToast('DV cache cleared — next scan will re-extract from scratch', 'success');
        this.showClearDvCacheConfirm = false;
      } catch (e) {
        this.showToast('Clear DV cache failed: ' + e.message, 'error');
      } finally {
        this.clearingDvCache = false;
      }
    },

    async runDvDetailScan(mode, bypassOverride, instanceIdOverride = '') {
      // instanceIdOverride: same target=both fan-out as runAudioTagsScan.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) return;
      // Defensive re-entry guard — same pattern as runQuickFixChain.
      // The Run-button gates on scanLoading at the UI layer; this
      // is the function-level safety net.
      if (this.scanLoading) return;
      // bypassOverride lets confirmDvDetailApply pass the snapshot it
      // captured before resetting dvBypassCache. Falsy → fall back to
      // the live state (Preview path).
      const bypassDvCache = bypassOverride !== undefined ? !!bypassOverride : !!this.dvBypassCache;
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('dv');
        this.scanResults.dvDetail = null;
        this.scanError = '';
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'dvdetail') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      // Reset progress + start polling. The scan is slow enough
      // (~1-3s per file × N files) that the user needs both a
      // current-file label and a way to cancel. Poll runs every 1.2s
      // — frequent enough to feel live, slow enough that polling
      // never bottlenecks anything.
      this.dvScanProgress = { running: true, total: 0, processed: 0, currentTitle: '' };
      this.startDvScanPoll();
      try {
        const r = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: targetInstanceId,
            action: 'dvdetail',
            mode,
            bypassDvCache,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          // Set scanError too — confirmDvDetailApply's target=both
          // fan-out reads it to short-circuit the loop after a failed
          // pass. Without this the loop silently continues to the
          // secondary instance and fires a false success toast. Audio
          // and Video already do this; this matches their pattern.
          this.scanError = err.error || ('HTTP ' + r.status);
          this.showToast('DV detail scan failed: ' + (err.error || r.status), 'error');
          return;
        }
        this.scanResults.dvDetail = await r.json();
        const totals = (this.scanResults.dvDetail && this.scanResults.dvDetail.totals) || {};
        const summary = (totals.dvCandidates || 0) + ' candidates · ' +
                        (totals.toAdd || 0) + ' add · ' +
                        (totals.toRemove || 0) + ' remove · ' +
                        (totals.toKeep || 0) + ' keep';
        this.showToast((mode === 'apply' ? 'DV detail applied — ' : 'DV detail preview ready — ') + summary, 'success');
        this.viewPhaseDetails({ phase: 'dvdetail', response: this.scanResults.dvDetail });
      } catch (e) {
        this.scanError = e.message || 'DV detail scan failed';
        this.showToast('DV detail scan failed: ' + e.message, 'error');
      } finally {
        this.scanLoading = false;
        this.stopDvScanPoll();
        this.dvScanProgress = null;
        // Refresh the History tab's scan-list if the user is sitting
        // on it so the just-dumped scan appears without manual reload.
        // No-op when they're elsewhere; the loadScanHistory tick on
        // History-tab landing will pick it up.
        if (this.scanSection === 'history') this.loadScanHistory();
        // Refresh the cache panel — a cache-active scan populates new
        // entries; a bypass scan changes nothing on disk but loadDvCacheStats
        // is cheap enough to run regardless.
        try { await this.loadDvCacheStats(); } catch (_) {}
      }
    },

    // ---- DV scan progress poll + cancel ----

    startDvScanPoll() {
      this.stopDvScanPoll();
      const tick = async () => {
        try {
          const r = await this.apiFetch('/api/scan/dvdetail/progress');
          if (!r.ok) return;
          const d = await r.json();
          if (d && d.running) {
            this.dvScanProgress = d;
          }
        } catch (_) {
          // Silent — next tick will retry.
        }
      };
      tick(); // immediate first poll so the UI doesn't sit blank
      // 400ms poll — fast enough that the progress bar visibly
      // increments per file on remux sources (per-file extraction is
      // tens of ms, so a 1200ms poll showed 20-file hops which looked
      // broken). Cost: ~25 GET/sec during a scan that already takes
      // ~10 seconds for a 200-file library — negligible.
      this._dvScanPollHandle = setInterval(tick, 400);
    },

    stopDvScanPoll() {
      if (this._dvScanPollHandle) {
        clearInterval(this._dvScanPollHandle);
        this._dvScanPollHandle = null;
      }
    },

    async cancelDvScan() {
      try {
        await this.apiFetch('/api/scan/dvdetail/cancel', { method: 'POST' });
        this.showToast('Cancelling DV scan — partial result will land when current file finishes', 'info');
      } catch (e) {
        this.showToast('Cancel failed: ' + e.message, 'error');
      }
    },

    // Sum of pending tag-changes from the most recent DV detail Preview.
    // Used by the Apply confirm modal so the user sees the actual diff
    // size before committing. Returns 0 when no preview has run yet —
    // the modal then shows a generic warning rather than "0 changes".
    dvDetailPendingChangeCount() {
      const r = this.scanResults && this.scanResults.dvDetail;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },

    openDvDetailApplyConfirm() {
      // Don't gate on count — even with 0 known changes, Apply against
      // a fresh cache walks the library and may discover changes the
      // user hasn't previewed. Modal copy adapts via the count check.
      this.showDvDetailApplyConfirm = true;
    },

    async confirmDvDetailApply() {
      this.showDvDetailApplyConfirm = false;
      // Re-entry guard — see confirmScanApply for rationale.
      // Especially important for DV: target=both fan-out + slow
      // ffmpeg+dovi_tool means a double-fire would queue two long
      // scans against the same instance and snapshot dvBypassCache
      // in inconsistent states.
      if (this.scanLoading) return;
      // Reset Skip cache before Apply so a destructive write doesn't
      // silently inherit a Preview's bypass setting. Snapshot first
      // so the actual run uses whatever the user had ticked.
      const bypass = !!this.dvBypassCache;
      this.dvBypassCache = false;
      // Same target=both fan-out as confirmAudioTagsApply. See its
      // header comment for the design rationale. Both runs honor the
      // same bypassDvCache snapshot so a fresh-cache pass is fresh
      // for both instances.
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        const totals = { added: 0, removed: 0 };
        for (const v of variants) {
          await this.runDvDetailScan('apply', bypass, v.instanceId);
          if (this.scanError) break;
          // Refresh variant response — see confirmAudioTagsApply.
          if (this.scanResults.dvDetail) {
            v.response = this.scanResults.dvDetail;
          }
          const a = this.scanResults.dvDetail && this.scanResults.dvDetail.applied;
          if (a) {
            totals.added += a.itemsAdded || 0;
            totals.removed += a.itemsRemoved || 0;
          }
        }
        if (!this.scanError) {
          this.showToast('DV detail applied across ' + variants.length + ' instances: ' + totals.added + ' added, ' + totals.removed + ' removed', 'success');
        }
      } else {
        await this.runDvDetailScan('apply', bypass);
      }
    },

    // --- Groups ---
    setGroupsSection(section) {
      this.groupsSection = section;
      localStorage.setItem('resolvarr-groups-section', section);
      this.pushNav();
    },

    async loadGroups() {
      this.groupsLoadError = '';
      try {
        const r = await this.apiFetch('/api/groups');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.groups = (await r.json()) || [];
      } catch (e) {
        this.groupsLoadError = e.message;
      }
      // Refresh the In-primary / In-secondary counts whenever the source
      // group list changes (post-load, post-save, post-delete).
      this.loadGroupTagCounts();
    },

    // Returns the currently-selected scan-instance object, or null.
    activeInstance() {
      if (!this.scanInstanceId) return null;
      return this.instances.find(i => i.id === this.scanInstanceId) || null;
    },

    // Library scan App-type pill helpers — same shape as the Tag
    // inventory equivalents (tagsAvailableInstances etc.).
    scanAvailableInstances() {
      return this.instances
        .filter(i => i.type === this.scanAppType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },
    scanAppTypeAvailable(type) {
      return this.instances.some(i => i.type === type);
    },
    // Sub-tab visibility per app-type. Sonarr currently supports only
    // Recover (lives on Run mode) + History. Tag library / Discover /
    // Sub-tab visibility per active app-type.
    //
    // Restructure 2026-05-05: Tag library + Release Groups + Recover
    // (Sonarr) all merged into one "Tag quality releases" sub-tab
    // (internal id 'groups'). Standalone 'tag' and 'recover' sub-tabs
    // are unreachable from sidebar — kept in markup as legacy x-show
    // gates so any existing localStorage value or deep-linked state
    // doesn't 404, but new clicks always route through 'groups'.
    //
    // Sonarr coverage today inside "Tag quality releases": Recover
    // works; Tag run + Discover are stubbed with M-Sonarr badges.
    // Filters + DV detail remain Radarr-only.
    scanSectionVisible(section) {
      // Standalone tag + recover + filters sub-tabs were folded into
      // 'groups' / wizardent during the 2026-05-05 restructure. Hide
      // their sidebar entries on every app type. Filter persistence
      // moves to the wizard; Cleanup moves under Active groups.
      if (section === 'tag' || section === 'recover' || section === 'filters') return false;
      // Plex label sync rules bind to one specific Arr instance — so the
      // sub-tab respects the page-level app-type pill (showing the user
      // only rules + instances of the picked type). Consistent with how
      // every other sub-tab (Tag library / Audio / Video / DV) is gated
      // by scanAppType.
      if (section === 'plex-sync') return true;
      if (this.scanAppType === 'sonarr') {
        // Sonarr's "Tag quality releases" tab carries the Recover action
        // + stubbed Tag/Discover for naming consistency with Radarr.
        // Filters + DV detail are Radarr-only. Missing episodes is
        // Sonarr-only (Radarr doesn't have the per-episode model).
        return section === 'run'    || section === 'groups' ||
               section === 'audio'  || section === 'video'  ||
               section === 'missing-episodes' ||
               section === 'tba-refresh' ||
               section === 'history';
      }
      // Radarr: every visible section EXCEPT the Sonarr-only ones.
      if (section === 'missing-episodes' || section === 'tba-refresh') return false;
      return true;
    },
    setScanAppType(type) {
      if (type !== 'radarr' && type !== 'sonarr') return;
      if (!this.scanAppTypeAvailable(type)) return;
      if (this.scanAppType === type) return;
      this.scanAppType = type;
      localStorage.setItem('resolvarr-scan-app-type', type);
      // Rebind scanInstanceId to the first instance of the new type, or
      // clear it. Switching to a section that's not visible for the new
      // type also gets nudged back to 'run' so the user lands somewhere.
      const first = this.scanAvailableInstances()[0];
      this.scanInstanceId = first ? first.id : '';
      if (this.scanInstanceId) {
        // Reuse the existing onChange handler so groups + cached state
        // refresh consistently with a manual instance pick.
        this.onGroupsInstanceChange();
      }
      if (!this.scanSectionVisible(this.scanSection)) {
        this.setScanSection('run');
      }
      this.pushNav();
    },

    // Returns 'radarr' | 'sonarr' for the active instance, defaulting to
    // 'radarr' so the Active-groups table has a sensible filter while no
    // instance is picked. Drives both the type-filter on the table and the
    // header label ("Showing N Radarr groups").
    activeInstanceType() {
      const inst = this.activeInstance();
      return inst ? inst.type : 'radarr';
    },

    // Find a sibling instance of the same type as the active one, used to
    // populate the "In secondary" column. Returns null when only one
    // instance of this type exists; the column is hidden in that case.
    secondaryInstanceForGroups() {
      const inst = this.activeInstance();
      if (!inst) return null;
      return this.instances.find(i => i.type === inst.type && i.id !== inst.id) || null;
    },

    // Filters the groups list by the active instance's type. Used by both
    // the table render and the count badge in the picker header.
    groupsFilteredByInstanceType() {
      const t = this.activeInstanceType();
      return (this.groups || []).filter(g => (g.type || 'radarr') === t);
    },

    // Look up a group's usage count in either the primary or secondary
    // instance. Returns 0 when the tag doesn't exist on that instance yet
    // (created lazily on first apply).
    groupTagCount(g, which) {
      const map = this.tagsByLabel[which] || {};
      const entry = map[(g.tag || '').toLowerCase()];
      return entry ? entry.count : 0;
    },

    // Look up the tag id for a group on either side. Returns null when
    // the tag hasn't been created on that instance yet (count 0 case).
    groupTagId(g, which) {
      const map = this.tagsByLabel[which] || {};
      const entry = map[(g.tag || '').toLowerCase()];
      return entry ? entry.id : null;
    },

    // Fetches /api/instances/{id}/tags for the active instance (and its
    // same-type sibling, if any), collapsing the response to a
    // label→{id,count} lookup keyed by lowercased label. Errors are
    // swallowed silently — the count column gracefully falls back to 0
    // when a fetch fails. The Tags tab will surface the underlying error
    // if it's a real auth/conn issue. Loading flags drive the "…"
    // placeholder in the table cells.
    async loadGroupTagCounts() {
      const primary = this.activeInstance();
      const secondary = this.secondaryInstanceForGroups();
      // Set loading flags BEFORE clearing the maps so there's no
      // microtask window where Alpine sees both tagsByLabel={} AND
      // loading=false → cells flash to "0" before the "…" placeholder
      // takes over. Order matters: synchronous mutations land in this
      // order in the same render tick.
      if (primary) this.tagCountsLoading.primary = true;
      if (secondary) this.tagCountsLoading.secondary = true;
      this.tagsByLabel = { primary: {}, secondary: {} };
      const fetchFor = async (inst, slot) => {
        if (!inst) return;
        try {
          const r = await this.apiFetch('/api/instances/' + inst.id + '/tags');
          if (!r.ok) return;
          const list = await r.json();
          const map = {};
          for (const t of (list || [])) {
            map[(t.label || '').toLowerCase()] = { id: t.id, count: t.usageCount || 0 };
          }
          this.tagsByLabel[slot] = map;
        } catch (e) {
          // Silent — count column shows 0; Tags tab surfaces real errors.
        } finally {
          this.tagCountsLoading[slot] = false;
        }
      };
      await Promise.all([
        fetchFor(primary, 'primary'),
        fetchFor(secondary, 'secondary'),
      ]);
    },

    // Triggered from the sub-tab picker — re-load counts for the new
    // instance pair. Discover/Cleanup blocks below share scanInstanceId,
    // so they pick up the change automatically; we still clear any stale
    // results that were tied to the old instance to avoid confusion.
    onGroupsInstanceChange() {
      this.clearScanResultsForInstanceChange();
      this.loadGroupTagCounts();
    },

    // Opens the drill-down modal listing every movie/series tagged with
    // this group's tag on the chosen instance. Same data source as the
    // Tag-inventory drill-down (/api/instances/{id}/tag-items), but
    // surfaced from a single click on the count cell. No-ops on count==0
    // (the cell handler short-circuits before getting here).
    async openGroupItems(g, slot) {
      const inst = slot === 'secondary' ? this.secondaryInstanceForGroups() : this.activeInstance();
      const tagId = this.groupTagId(g, slot);
      const count = this.groupTagCount(g, slot);
      if (!inst || !tagId || count === 0) return;
      this.groupItemsTarget = { group: g, slot, instance: inst, tagId, count };
      this.groupItemsList = [];
      this.groupItemsError = '';
      this.groupItemsLoading = true;
      this.showGroupItemsModal = true;
      try {
        const r = await this.apiFetch('/api/instances/' + inst.id + '/tag-items?ids=' + tagId);
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          throw new Error(msg || 'HTTP ' + r.status);
        }
        const data = await r.json();
        const grp = (data || []).find(x => x.tagId === tagId);
        this.groupItemsList = (grp && grp.items) || [];
      } catch (e) {
        this.groupItemsError = e.message || 'Failed to load items';
      } finally {
        this.groupItemsLoading = false;
      }
    },

    closeGroupItems() {
      this.showGroupItemsModal = false;
      this.groupItemsTarget = null;
      this.groupItemsList = [];
      this.groupItemsError = '';
    },

    openGroupModal(g) {
      if (g) {
        this.groupForm = { id: g.id, search: g.search, tag: g.tag, display: g.display, mode: g.mode };
      } else {
        this.groupForm = { id: '', search: '', tag: '', display: '', mode: 'filtered' };
      }
      this.groupFormError = '';
      this.groupFormBusy = false;
      this.showGroupModal = true;
    },

    closeGroupModal() {
      this.showGroupModal = false;
    },

    async saveGroup() {
      this.groupFormError = '';
      // Client-side validation mirrors server-side checks so users see
      // fast feedback before the round-trip.
      const search = (this.groupForm.search || '').trim();
      const tag = (this.groupForm.tag || '').trim().toLowerCase();
      const display = (this.groupForm.display || '').trim();
      if (!search) { this.groupFormError = 'Search string is required.'; return; }
      if (!tag) { this.groupFormError = 'Tag name is required.'; return; }
      if (!/^[a-z0-9][a-z0-9_-]*$/.test(tag)) {
        this.groupFormError = 'Tag name must be lowercase letters, digits, underscores, or dashes.';
        return;
      }
      if (!display) { this.groupFormError = 'Display name is required.'; return; }
      if (this.groupForm.mode !== 'filtered' && this.groupForm.mode !== 'simple') {
        this.groupFormError = 'Pick a mode.';
        return;
      }

      this.groupFormBusy = true;
      try {
        const payload = { search, tag, display, mode: this.groupForm.mode };
        const url = this.groupForm.id ? '/api/groups/' + this.groupForm.id : '/api/groups';
        const method = this.groupForm.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(url, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          throw new Error(err.error || 'HTTP ' + r.status);
        }
        await this.loadGroups();
        this.showGroupModal = false;
        this.showToast(this.groupForm.id ? 'Group updated' : 'Group added', 'success');
      } catch (e) {
        this.groupFormError = e.message;
      } finally {
        this.groupFormBusy = false;
      }
    },

    // deleteGroup opens our own confirm modal rather than window.confirm().
    // The native popup is jarring against the rest of the styled UI and
    // also blocks the event loop, which can race with background polling.
    deleteGroup(g) {
      this.deleteGroupTarget = g;
    },

    async confirmDeleteGroup() {
      const g = this.deleteGroupTarget;
      if (!g) return;
      this.deleteGroupBusy = true;
      try {
        const r = await this.apiFetch('/api/groups/' + g.id, { method: 'DELETE' });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        await this.loadGroups();
        // Surface the actual identity in the toast so the user sees
        // exactly what disappeared — useful when removing several in
        // a row, where a generic "Group removed" gives no confirmation
        // they hit the right one. Show the search-string + tag-label
        // pair (display-name varies per group).
        const label = g.tag ? ` → ${g.tag}` : '';
        this.showToast(`Group removed: ${g.search}${label}`, 'success');
        this.deleteGroupTarget = null;
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteGroupBusy = false;
      }
    },

    // setGroupMode sets a group's mode from the segmented control in the
    // list — no need to open the Edit modal for a one-field change. Caller
    // passes the target mode (segmented buttons are disabled when already
    // active, so we don't guard here). Server is the source of truth; we
    // re-load from the list endpoint on success rather than assuming the
    // PUT echo is authoritative.
    async setGroupMode(g, nextMode) {
      if (this.groupTogglingId) return; // one at a time
      if (g.mode === nextMode) return;
      this.groupTogglingId = g.id;
      try {
        const r = await this.apiFetch('/api/groups/' + g.id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            search: g.search,
            tag: g.tag,
            display: g.display,
            mode: nextMode,
            type: g.type,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          throw new Error(err.error || 'HTTP ' + r.status);
        }
        await this.loadGroups();
        this.showToast(g.display + ' → ' + (nextMode === 'filtered' ? 'Filtered' : 'Simple'), 'success');
      } catch (e) {
        this.showToast('Mode change failed: ' + e.message, 'error');
      } finally {
        this.groupTogglingId = null;
      }
    },

    // toggleGroupEnabled flips a group's enabled flag from the list toggle
    // switch. A disabled group stays in the config — all settings are
    // preserved — but every scan mode skips it. Equivalent to commenting
    // out the `#` row in the bash RELEASE_GROUPS array. Uses the Enabled
    // pointer on the backend request so we don't have to resend every
    // other field (Edit modal still does that).
    async toggleGroupEnabled(g) {
      if (this.groupTogglingId) return;
      this.groupTogglingId = g.id;
      const nextEnabled = !g.enabled;
      try {
        const r = await this.apiFetch('/api/groups/' + g.id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            search: g.search,
            tag: g.tag,
            display: g.display,
            mode: g.mode,
            type: g.type,
            enabled: nextEnabled,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          throw new Error(err.error || 'HTTP ' + r.status);
        }
        await this.loadGroups();
        this.showToast(g.display + ' ' + (nextEnabled ? 'enabled' : 'disabled'), 'success');
      } catch (e) {
        this.showToast('Enable toggle failed: ' + e.message, 'error');
      } finally {
        this.groupTogglingId = null;
      }
    },

    // --- Filters ---
    async saveFilters() {
      try {
        // Backend expects per-Arr-type FilterSet shape:
        // {radarr: {Quality, ...}, sonarr: {...}}. The UI only edits
        // the Radarr block today; we round-trip whatever Sonarr block
        // the server returned at load (or default-on if none) so a
        // save-from-UI never wipes the Sonarr-side defaults that
        // Load() backfilled.
        const radarrBlock = {
          Quality: this.filters.quality,
          MAWebDL: this.filters.maWebDL,
          PlayWebDL: this.filters.playWebDL,
          Audio: this.filters.audio,
          TrueHD: this.filters.trueHD,
          TrueHDAtmos: this.filters.trueHDAtmos,
          DTSX: this.filters.dtsX,
          DTSHDMA: this.filters.dtsHDMA,
        };
        const sonarrBlock = this._savedSonarrFilters || {
          Quality: true, MAWebDL: true, PlayWebDL: true, Audio: true,
          TrueHD: true, TrueHDAtmos: true, DTSX: true, DTSHDMA: true,
        };
        const r = await this.apiFetch('/api/filters', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ radarr: radarrBlock, sonarr: sonarrBlock }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },

    // --- Extra tags (M4) ---
    // anyExtraTagsBucketEnabled gates the Run scan button and the Quick
    // fix-all "Apply extra tags" checkbox. A run with no buckets enabled
    // ---- Audio + Video tags (M4 split) ------------------------------
    // Two parallel sets of helpers — the buckets they operate on differ
    // (Audio = single bucket; Video = three buckets) but the per-value
    // allow-list semantics are identical. Each side has its own scan
    // entrypoint so results land on the originating sub-tab.
    //
    // Empty allowedValues = "all allowed" (engine convention). The
    // UI's checkbox flow auto-disables a bucket if the user un-checks
    // every value (correct way to "tag nothing"); re-enabling starts
    // fresh with all-allowed.

    autoTagPrefixRule: /^[a-z0-9-]*$/,
    // bucketLabelRule mirrors Radarr's strict tag-label regex used by
    // the backend's validateLabelsMap. NON-empty + a-z 0-9 hyphen only.
    bucketLabelRule: /^[a-z0-9-]+$/,

    // Bucket label override helpers ------------------------------------
    // Sparse map on the bucket — keyed by canonical engine value
    // (e.g. "dvprofile8", "truehd", "2160p"), valued by user-chosen
    // replacement. Empty/missing entry means "use engine default".
    //
    // The bucket Prefix still applies on top — override replaces the
    // value portion only. To drop the prefix for an override, leave
    // the bucket Prefix empty (separate per-bucket setting).
    bucketLabelValue(bucket, value) {
      if (!bucket || !bucket.labels) return '';
      const v = bucket.labels[value];
      return (typeof v === 'string') ? v : '';
    },
    setBucketLabel(bucket, value, label, save) {
      if (!bucket) return;
      if (!bucket.labels || typeof bucket.labels !== 'object') bucket.labels = {};
      const trimmed = (label || '').trim();
      if (trimmed === '') {
        delete bucket.labels[value];
      } else {
        bucket.labels[value] = trimmed;
      }
      if (typeof save === 'function') save();
    },
    bucketLabelValid(bucket, value) {
      const v = this.bucketLabelValue(bucket, value);
      if (v === '') return true; // empty = use default, valid
      return this.bucketLabelRule.test(v);
    },
    // Returns true when any label in the bucket collides with another
    // (two keys → same override value). Used to flag the customise-
    // labels section header so users see the error before save.
    bucketHasLabelCollision(bucket) {
      if (!bucket || !bucket.labels) return false;
      const seen = {};
      for (const [k, v] of Object.entries(bucket.labels)) {
        if (!v) continue;
        if (seen[v]) return true;
        seen[v] = k;
      }
      return false;
    },
    // Returns the number of currently-configured overrides — drives
    // the "(N customised)" badge on the section header.
    bucketLabelCount(bucket) {
      if (!bucket || !bucket.labels) return 0;
      return Object.values(bucket.labels).filter(v => v && v.trim() !== '').length;
    },
    // ruleEditorLabelError scans every bucket on the currently-edited
    // rule snapshot for invalid override characters or intra-bucket
    // label collisions. Returns the first user-facing error string or
    // empty when everything's clean. Called from the schedule + webhook
    // save handlers so users see "Fix … on the Audio step" inline
    // instead of round-tripping to a backend 400 toast.
    ruleEditorLabelError() {
      const r = this.editingRule;
      if (!r) return '';
      const buckets = [];
      if (r.audioTags && r.audioTags.audio) buckets.push({ b: r.audioTags.audio, vocab: this.audioFullVocab(),       step: 'Audio' });
      if (r.videoTags) {
        if (r.videoTags.resolution) buckets.push({ b: r.videoTags.resolution, vocab: this.videoVocab.resolution, step: 'Video → Resolution' });
        if (r.videoTags.codec)      buckets.push({ b: r.videoTags.codec,      vocab: this.videoVocab.codec,      step: 'Video → Codec' });
        if (r.videoTags.hdr)        buckets.push({ b: r.videoTags.hdr,        vocab: this.videoVocab.hdr,        step: 'Video → HDR' });
      }
      if (r.dvDetail) buckets.push({ b: r.dvDetail, vocab: this.dvDetailVocab || [], step: 'DV detail' });
      for (const { b, vocab, step } of buckets) {
        for (const v of vocab) {
          if (!this.bucketLabelValid(b, v)) {
            return 'Fix invalid label override on the ' + step + ' step (allowed: a-z, 0-9, hyphen)';
          }
        }
        if (this.bucketHasLabelCollision(b)) {
          return 'Two label overrides on the ' + step + ' step map to the same value — pick distinct labels';
        }
      }
      return '';
    },

    // Audio --------------------------------------------------------
    anyAudioTagsBucketEnabled() {
      return !!(this.audioTags && this.audioTags.audio && this.audioTags.audio.enabled);
    },
    audioTagPrefixInvalid() {
      const p = (this.audioTags.audio && this.audioTags.audio.prefix) || '';
      return !this.autoTagPrefixRule.test(p);
    },
    // Two-mode semantics (SelectMode):
    //   "" / "all" (default) → empty allowedValues means "all allowed".
    //                          Tick state: empty list shows everything checked.
    //   "select"             → exact list. Empty means "tag nothing".
    //                          Tick state: explicit per-value checks.
    // Matches engine.BucketConfig.allowed() — see Go side for the truth-source.
    audioTagValueChecked(value) {
      const b = this.audioTags.audio;
      const av = b.allowedValues;
      if (b.selectMode !== 'select' && (!av || av.length === 0)) return true;
      return !!av && av.includes(value);
    },
    toggleAudioTagValue(value, fullVocab) {
      const bucket = this.audioTags.audio;
      // First per-value click in legacy "all-allowed" mode flips the bucket
      // into explicit-select mode using fullVocab as the starting set, then
      // toggles the clicked value off. Subsequent clicks are pure add/remove.
      if (bucket.selectMode !== 'select') {
        if (!bucket.allowedValues || bucket.allowedValues.length === 0) {
          bucket.allowedValues = [...fullVocab];
        }
        bucket.selectMode = 'select';
      }
      let av = bucket.allowedValues || [];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      // If user re-checked back to the full set, normalise to "all" mode so
      // future vocab additions automatically apply.
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        bucket.selectMode = '';
        bucket.allowedValues = [];
      } else {
        bucket.allowedValues = av;
      }
      this.saveAudioTags();
    },
    selectAllAudioValues() {
      const bucket = this.audioTags.audio;
      bucket.selectMode = '';
      bucket.allowedValues = [];
      this.saveAudioTags();
    },
    selectNoneAudioValues() {
      const bucket = this.audioTags.audio;
      bucket.selectMode = 'select';
      bucket.allowedValues = [];
      this.saveAudioTags();
    },
    // Combined audio vocabulary across the three sub-categories — used
    // by toggleAudioTagValue when it needs the full canonical list to
    // expand "empty = all" into an explicit slice.
    audioFullVocab() {
      return [...this.audioVocab.codecs, ...this.audioVocab.channels, ...this.audioVocab.flags];
    },

    async loadAudioTags() {
      try {
        const r = await this.apiFetch('/api/audio-tags');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config && data.config.audio) {
          const src = data.config.audio;
          const dst = this.audioTags.audio;
          dst.enabled = !!src.enabled;
          dst.prefix = src.prefix || '';
          dst.sonarrAggregation = src.sonarrAggregation || 'all-occurring';
          dst.allowedValues = Array.isArray(src.allowedValues) ? src.allowedValues : [];
          dst.selectMode = src.selectMode || '';
          dst.labels = (src.labels && typeof src.labels === 'object') ? { ...src.labels } : {};
          this.audioTags.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.audioCodecs))   this.audioVocab.codecs   = data.audioCodecs;
        if (Array.isArray(data && data.audioChannels)) this.audioVocab.channels = data.audioChannels;
        if (Array.isArray(data && data.audioFlags))    this.audioVocab.flags    = data.audioFlags;
      } catch (_) {
        // Silent — config-load is best-effort.
      }
    },

    // persistRuleSnapshotsToGlobals writes the per-action wizard's
    // bucket-config snapshot back to globals via the same endpoints
    // the (now-removed) sub-tab pages used. Called from
    // runQuickFixChain after a successful per-action run so the
    // wizard's tweaks become the new default for next open.
    //
    // No-op for actions without a bucket config (recover) and for
    // anything outside audiotags / videotags / dvdetail.
    //
    // Failures are soft: the chain already succeeded, so a save
    // failure is annoying but not catastrophic. Surface as a toast,
    // leave the run results intact. User can fire the wizard again
    // to re-attempt.
    async persistRuleSnapshotsToGlobals(rule, action) {
      if (!rule) return;
      try {
        if (action === 'audiotags' && rule.audioTags) {
          const a = rule.audioTags;
          const r = await this.apiFetch('/api/audio-tags', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              audio: a.audio,
              removeOrphanedTags: !!a.removeOrphanedTags,
            }),
          });
          if (!r.ok) {
            const b = await r.json().catch(() => ({}));
            throw new Error(b.error || 'HTTP ' + r.status);
          }
          // Mirror into local cache so the sub-tab page (still
          // visible if user navigates there) reflects the new
          // state without a reload.
          this.audioTags = JSON.parse(JSON.stringify(a));
        } else if (action === 'videotags' && rule.videoTags) {
          const v = rule.videoTags;
          const r = await this.apiFetch('/api/video-tags', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              resolution: v.resolution,
              codec:      v.codec,
              hdr:        v.hdr,
              removeOrphanedTags: !!v.removeOrphanedTags,
            }),
          });
          if (!r.ok) {
            const b = await r.json().catch(() => ({}));
            throw new Error(b.error || 'HTTP ' + r.status);
          }
          this.videoTags = JSON.parse(JSON.stringify(v));
        } else if (action === 'dvdetail' && rule.dvDetail) {
          const d = rule.dvDetail;
          const r = await this.apiFetch('/api/dv-detail', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              enabled: !!d.enabled,
              prefix: d.prefix || '',
              allowedValues: d.allowedValues,
              selectMode: d.selectMode || '',
              removeOrphanedTags: !!d.removeOrphanedTags,
            }),
          });
          if (!r.ok) {
            const b = await r.json().catch(() => ({}));
            throw new Error(b.error || 'HTTP ' + r.status);
          }
          this.dvDetail = JSON.parse(JSON.stringify(d));
        }
        // 'recover' has no bucket config — silent no-op is correct.
      } catch (e) {
        // Soft failure — the run itself succeeded; just couldn't
        // persist as new default. Surface but don't undo.
        this.showToast('Could not save as new default: ' + e.message, 'error');
      }
    },

    async saveAudioTags() {
      if (this.audioTagPrefixInvalid()) {
        this.showToast('Audio prefix has invalid characters — Radarr only allows a-z, 0-9, and -', 'error');
        return;
      }
      try {
        const payload = {
          audio: this.audioTags.audio,
          removeOrphanedTags: !!this.audioTags.removeOrphanedTags,
        };
        const r = await this.apiFetch('/api/audio-tags', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },

    async runAudioTagsScan(mode = 'preview', instanceIdOverride = '') {
      // instanceIdOverride lets Apply-now target a specific instance
      // (used by confirmAudioTagsApply when target=both produced two
      // variants — each variant's instance gets its own apply pass).
      // Empty string falls back to the page-level scanInstanceId for
      // legacy single-instance flows.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) { this.showToast('Pick an instance first', 'error'); return; }
      if (!this.anyAudioTagsBucketEnabled()) { this.showToast('Enable Audio bucket first', 'error'); return; }
      // Modal-close + state-reset only on the first pass when the
      // caller didn't pass an override. Apply-now-against-variants
      // does multiple successive runs and must NOT close the modal
      // between them or reset historical state.
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('audio');
        this.autoTagRowExpanded = {};
        this.scanResults.audioTags = null;
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'audiotags') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      this.scanError = '';
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: targetInstanceId, action: 'audiotags', mode }),
        });
        if (!resp.ok) {
          const err = await resp.json().catch(() => ({}));
          this.scanError = err.error || ('HTTP ' + resp.status);
          return;
        }
        this.scanResults.audioTags = await resp.json();
        if (mode === 'apply' && this.scanResults.audioTags.applied) {
          const a = this.scanResults.audioTags.applied;
          this.showToast('Audio tags applied: ' + a.itemsAdded + ' added, ' + a.itemsRemoved + ' removed', 'success');
        }
        // Open detail modal automatically — same popup pattern as the
        // other phases. History row click + QFA chain phase click also
        // route through viewPhaseDetails, so all surfaces converge.
        this.viewPhaseDetails({ phase: 'audiotags', response: this.scanResults.audioTags });
      } catch (e) {
        this.scanError = e.message || 'Audio-tags scan failed';
      } finally {
        this.scanLoading = false;
      }
    },

    audioTagsPendingChangeCount() {
      const r = this.scanResults && this.scanResults.audioTags;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },
    // Apply-now tooltip helper. Renders a context-aware hint per
    // action, for buttons in the result panels' top + bottom strips.
    // Three states:
    //   1. Historical snapshot   → "Run a fresh preview" warning
    //   2. target=both was used  → "Writes to <pri> + <sec>" clarifier
    //   3. Single instance       → empty (no tooltip needed)
    // Action arg: 'tag' | 'audiotags' | 'videotags' | 'dvdetail' | 'recover'.
    // Recover is intentionally scope-narrowing: each instance has its
    // own per-row selection set (vs. audio/video/dv's "every decision
    // applies"), so apply hits only the currently-viewed variant.
    applyNowTooltip(action) {
      if (this.isHistoricalForAction(action)) {
        const labels = { tag: 'Tag', audiotags: 'Audio', videotags: 'Video', dvdetail: 'DV', recover: 'Recover' };
        return 'Run a fresh ' + (labels[action] || 'scan') + ' preview before applying — this is a saved snapshot, not live data.';
      }
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        if (action === 'recover') {
          // Recover's per-row selection is variant-specific — applies
          // only to the instance whose result is currently displayed.
          // Switch variant to apply on the other one. This differs
          // from audio/video/dv which fan-out across every variant.
          return 'Applies to the currently-viewed instance only. Switch variant above to apply on the other one — Recover selections are per-instance.';
        }
        const names = variants.map(v => v.label || (v.instanceId === this.scanInstanceId ? 'Primary' : 'Secondary')).join(' + ');
        return 'Writes to ' + names + '.';
      }
      return '';
    },
    // Same helper but for the in-modal info banner. Empty on
    // single-instance runs so the banner doesn't render.
    applyNowMultiInstanceLabel() {
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length <= 1) return '';
      const names = variants.map(v => v.label || (v.instanceId === this.scanInstanceId ? 'Primary' : 'Secondary')).join(' + ');
      return 'Writes to ' + names + '.';
    },
    openAudioTagsApplyConfirm() {
      // Don't gate on count — when the user picks Apply mode without
      // running Preview first, the count is 0 (no scanResults yet)
      // but they explicitly chose Apply: they want to scan + write in
      // one step. Modal copy adapts via the count check. Same pattern
      // openDvDetailApplyConfirm uses.
      this.showAudioTagsApplyConfirm = true;
    },
    async confirmAudioTagsApply() {
      this.showAudioTagsApplyConfirm = false;
      // Re-entry guard — see confirmScanApply for rationale.
      if (this.scanLoading) return;
      // When the wizard ran with target=both the preview produced two
      // variants (one per instance). Apply must hit BOTH — that's what
      // the user picked. Variant switcher is for reading the result,
      // not for narrowing apply scope. If the user wanted apply to one
      // instance only, they'd have picked primary or secondary in
      // step 1. Single-variant case falls through to legacy single-
      // instance flow against scanInstanceId.
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        const totals = { added: 0, removed: 0 };
        for (const v of variants) {
          await this.runAudioTagsScan('apply', v.instanceId);
          if (this.scanError) break;
          // Refresh the variant entry's response so the pill switcher
          // shows post-apply state (mode chip flips Preview → Applied,
          // counts reflect what was written) instead of the stale
          // preview response. Without this, clicking a pill after
          // fan-out re-displays the original preview which is
          // confusing and looks like the apply didn't take.
          if (this.scanResults.audioTags) {
            v.response = this.scanResults.audioTags;
          }
          const a = this.scanResults.audioTags && this.scanResults.audioTags.applied;
          if (a) {
            totals.added += a.itemsAdded || 0;
            totals.removed += a.itemsRemoved || 0;
          }
        }
        if (!this.scanError) {
          this.showToast('Audio tags applied across ' + variants.length + ' instances: ' + totals.added + ' added, ' + totals.removed + ' removed', 'success');
        }
      } else {
        await this.runAudioTagsScan('apply');
      }
    },
    audioTagMoviesFor(tag, action) {
      const r = this.scanResults && this.scanResults.audioTags;
      if (!r || !r.items) return [];
      const out = [];
      for (const item of r.items) {
        if (!item.autoDecisions) continue;
        for (const d of item.autoDecisions) {
          if (d.tag === tag && d.action === action) { out.push(item); break; }
        }
      }
      return out;
    },

    // Video --------------------------------------------------------
    anyVideoTagsBucketEnabled() {
      const v = this.videoTags;
      return !!(v.resolution.enabled || v.codec.enabled || v.hdr.enabled);
    },
    videoTagPrefixInvalid(bucketKey) {
      const p = (this.videoTags[bucketKey] && this.videoTags[bucketKey].prefix) || '';
      return !this.autoTagPrefixRule.test(p);
    },
    anyVideoTagPrefixInvalid() {
      return ['resolution', 'codec', 'hdr'].some(b => this.videoTagPrefixInvalid(b));
    },
    // Same two-mode select semantics as audioTagValueChecked — see
    // that helper's header comment for the full SelectMode rationale.
    videoTagValueChecked(bucketKey, value) {
      const b = this.videoTags[bucketKey];
      if (!b) return true;
      const av = b.allowedValues;
      if (b.selectMode !== 'select' && (!av || av.length === 0)) return true;
      return !!av && av.includes(value);
    },
    toggleVideoTagValue(bucketKey, value, fullVocab) {
      const bucket = this.videoTags[bucketKey];
      if (bucket.selectMode !== 'select') {
        if (!bucket.allowedValues || bucket.allowedValues.length === 0) {
          bucket.allowedValues = [...fullVocab];
        }
        bucket.selectMode = 'select';
      }
      let av = bucket.allowedValues || [];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        bucket.selectMode = '';
        bucket.allowedValues = [];
      } else {
        bucket.allowedValues = av;
      }
      this.saveVideoTags();
    },
    selectAllVideoValues(bucketKey) {
      const bucket = this.videoTags[bucketKey];
      bucket.selectMode = '';
      bucket.allowedValues = [];
      this.saveVideoTags();
    },
    selectNoneVideoValues(bucketKey) {
      const bucket = this.videoTags[bucketKey];
      bucket.selectMode = 'select';
      bucket.allowedValues = [];
      this.saveVideoTags();
    },

    async loadVideoTags() {
      try {
        const r = await this.apiFetch('/api/video-tags');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config) {
          const merge = (dst, src) => {
            if (!src) return;
            dst.enabled = !!src.enabled;
            dst.prefix = src.prefix || '';
            dst.sonarrAggregation = src.sonarrAggregation || dst.sonarrAggregation;
            dst.allowedValues = Array.isArray(src.allowedValues) ? src.allowedValues : [];
            dst.selectMode = src.selectMode || '';
            dst.labels = (src.labels && typeof src.labels === 'object') ? { ...src.labels } : {};
          };
          merge(this.videoTags.resolution, data.config.resolution);
          merge(this.videoTags.codec,      data.config.codec);
          merge(this.videoTags.hdr,        data.config.hdr);
          this.videoTags.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.resolution)) this.videoVocab.resolution = data.resolution;
        if (Array.isArray(data && data.codec))      this.videoVocab.codec      = data.codec;
        if (Array.isArray(data && data.hdr))        this.videoVocab.hdr        = data.hdr;
      } catch (_) {}
    },

    async saveVideoTags() {
      const invalid = ['resolution', 'codec', 'hdr'].filter(b => this.videoTagPrefixInvalid(b));
      if (invalid.length > 0) {
        this.showToast(invalid[0] + ' prefix has invalid characters — Radarr only allows a-z, 0-9, and -', 'error');
        return;
      }
      try {
        const payload = {
          resolution: this.videoTags.resolution,
          codec:      this.videoTags.codec,
          hdr:        this.videoTags.hdr,
          removeOrphanedTags: !!this.videoTags.removeOrphanedTags,
        };
        const r = await this.apiFetch('/api/video-tags', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },

    async runVideoTagsScan(mode = 'preview', instanceIdOverride = '') {
      // instanceIdOverride: same target=both fan-out as runAudioTagsScan.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) { this.showToast('Pick an instance first', 'error'); return; }
      if (!this.anyVideoTagsBucketEnabled()) { this.showToast('Enable at least one Video bucket first', 'error'); return; }
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('video');
        this.autoTagRowExpanded = {};
        this.scanResults.videoTags = null;
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'videotags') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      this.scanError = '';
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: targetInstanceId, action: 'videotags', mode }),
        });
        if (!resp.ok) {
          const err = await resp.json().catch(() => ({}));
          this.scanError = err.error || ('HTTP ' + resp.status);
          return;
        }
        this.scanResults.videoTags = await resp.json();
        if (mode === 'apply' && this.scanResults.videoTags.applied) {
          const a = this.scanResults.videoTags.applied;
          this.showToast('Video tags applied: ' + a.itemsAdded + ' added, ' + a.itemsRemoved + ' removed', 'success');
        }
        this.viewPhaseDetails({ phase: 'videotags', response: this.scanResults.videoTags });
      } catch (e) {
        this.scanError = e.message || 'Video-tags scan failed';
      } finally {
        this.scanLoading = false;
      }
    },

    videoTagsPendingChangeCount() {
      const r = this.scanResults && this.scanResults.videoTags;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },
    openVideoTagsApplyConfirm() {
      // Don't gate on count — same reasoning as openAudioTagsApplyConfirm.
      // Modal copy adapts to the no-preview case via the count check.
      this.showVideoTagsApplyConfirm = true;
    },
    async confirmVideoTagsApply() {
      this.showVideoTagsApplyConfirm = false;
      // Re-entry guard — see confirmScanApply for rationale.
      if (this.scanLoading) return;
      // Same target=both fan-out as confirmAudioTagsApply. See its
      // header comment for the design rationale.
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        const totals = { added: 0, removed: 0 };
        for (const v of variants) {
          await this.runVideoTagsScan('apply', v.instanceId);
          if (this.scanError) break;
          // Refresh variant response to apply state — see
          // confirmAudioTagsApply for rationale.
          if (this.scanResults.videoTags) {
            v.response = this.scanResults.videoTags;
          }
          const a = this.scanResults.videoTags && this.scanResults.videoTags.applied;
          if (a) {
            totals.added += a.itemsAdded || 0;
            totals.removed += a.itemsRemoved || 0;
          }
        }
        if (!this.scanError) {
          this.showToast('Video tags applied across ' + variants.length + ' instances: ' + totals.added + ' added, ' + totals.removed + ' removed', 'success');
        }
      } else {
        await this.runVideoTagsScan('apply');
      }
    },
    videoTagMoviesFor(tag, action) {
      const r = this.scanResults && this.scanResults.videoTags;
      if (!r || !r.items) return [];
      const out = [];
      for (const item of r.items) {
        if (!item.autoDecisions) continue;
        for (const d of item.autoDecisions) {
          if (d.tag === tag && d.action === action) { out.push(item); break; }
        }
      }
      return out;
    },

    // Shared per-row drill-down toggle (audio / video / dv all use the
    // same autoTagRowExpanded state).
    toggleAutoTagRow(tag) {
      this.autoTagRowExpanded = { ...this.autoTagRowExpanded, [tag]: !this.autoTagRowExpanded[tag] };
    },

    // ---- Rule-editor per-value allow-list helpers --------------------
    // Mirror of the global toggleAudioTagValue / toggleVideoTagValue /
    // toggleDvDetailValue but bound to editingRule.* instead of global
    // state. Don't call save* — the rule-editor's Save button persists
    // everything atomically.

    ruleAudioTagValueChecked(value) {
      if (!this.editingRule || !this.editingRule.audioTags) return false;
      const av = this.editingRule.audioTags.audio && this.editingRule.audioTags.audio.allowedValues;
      if (!av || av.length === 0) return true;
      return av.includes(value);
    },
    ruleToggleAudioTagValue(value, fullVocab) {
      if (!this.editingRule || !this.editingRule.audioTags) return;
      const bucket = this.editingRule.audioTags.audio;
      let av = bucket.allowedValues || [];
      if (av.length === 0) av = [...fullVocab];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      if (av.length === 0) {
        bucket.enabled = false;
        bucket.allowedValues = [];
        this.showToast('Audio bucket disabled — no values were left allowed', 'info');
        return;
      }
      const normalised = av.length === fullVocab.length && fullVocab.every(v => av.includes(v));
      bucket.allowedValues = normalised ? [] : av;
    },

    ruleVideoTagValueChecked(bucketKey, value) {
      if (!this.editingRule || !this.editingRule.videoTags) return false;
      const av = this.editingRule.videoTags[bucketKey] && this.editingRule.videoTags[bucketKey].allowedValues;
      if (!av || av.length === 0) return true;
      return av.includes(value);
    },
    ruleToggleVideoTagValue(bucketKey, value, fullVocab) {
      if (!this.editingRule || !this.editingRule.videoTags) return;
      const bucket = this.editingRule.videoTags[bucketKey];
      let av = bucket.allowedValues || [];
      if (av.length === 0) av = [...fullVocab];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      if (av.length === 0) {
        bucket.enabled = false;
        bucket.allowedValues = [];
        this.showToast(bucketKey + ' bucket disabled — no values were left allowed', 'info');
        return;
      }
      const normalised = av.length === fullVocab.length && fullVocab.every(v => av.includes(v));
      bucket.allowedValues = normalised ? [] : av;
    },

    ruleDvDetailValueChecked(value) {
      if (!this.editingRule || !this.editingRule.dvDetail) return false;
      const av = this.editingRule.dvDetail.allowedValues;
      if (!av || av.length === 0) return true;
      return av.includes(value);
    },
    ruleToggleDvDetailValue(value) {
      if (!this.editingRule || !this.editingRule.dvDetail) return;
      const dd = this.editingRule.dvDetail;
      const av = dd.allowedValues || [];
      const idx = av.indexOf(value);
      if (idx >= 0) av.splice(idx, 1);
      else          av.push(value);
      const order = (v) => this.dvDetailVocab.indexOf(v);
      av.sort((a, b) => order(a) - order(b));
      dd.allowedValues = av;
    },

    // ---- Review-step helpers (Quick fix-all + schedule wizard) ----
    // The Review step is the user's last chance to catch a wrong
    // toggle before committing — so it spells out everything the
    // run will do, not just the headline. Each helper returns a
    // readable string; templates render them inline.

    // Run-mode label as it'll behave on dispatch.
    reviewRunModeLabel() {
      if (!this.editingRule) return '';
      const m = this.editingRule.options && this.editingRule.options.runMode;
      if (m === 'preview') return 'Preview (read-only — nothing is written to Radarr)';
      return 'Apply (writes tag changes to Radarr)';
    },

    // Resolve the secondary instance that sync / auto-tags-on-secondary
    // would target. Mirrors backend resolveSyncTarget logic: explicit
    // SyncToInstanceID wins; otherwise pick first other-of-same-type.
    // Returns the instance object or null when sync is disabled / no
    // valid target exists.
    reviewSecondaryInstance() {
      const r = this.editingRule;
      if (!r || !r.options || !r.options.syncToSecondary) return null;
      if (r.options.syncToInstanceId) {
        return this.instances.find(i => i.id === r.options.syncToInstanceId) || null;
      }
      const primary = this.instances.find(i => i.id === r.instanceId);
      if (!primary) return null;
      return this.instances.find(i => i.type === primary.type && i.id !== r.instanceId) || null;
    },

    // reviewTargetLabel — render a per-bucket target ('primary' |
    // 'secondary' | 'both') for the Review step using the resolved
    // instance names. Falls back to "primary only" when the rule has
    // a single instance (in which case the picker isn't shown anyway,
    // but a stale target value should still render gracefully).
    reviewTargetLabel(target) {
      const t = target || 'primary';
      const r = this.editingRule;
      if (!r) return t;
      const primary = (this.instances || []).find(i => i.id === r.instanceId) || {};
      const secondary = this.ruleSecondaryInstance() || {};
      const pName = primary.name || 'primary';
      const sName = secondary.name || 'secondary';
      if (t === 'primary')   return pName + ' only';
      if (t === 'secondary') return sName + ' only';
      if (t === 'both')      return 'both — ' + pName + ' then ' + sName;
      return t;
    },

    // ruleHasSecondary — true when the rule's primary instance has at
    // least one same-type sibling (so a secondary target is available).
    // Drives visibility of the per-bucket target pickers (audio /
    // video / DV) on each step. With one instance the picker is hidden;
    // bucket runs against primary implicitly.
    ruleHasSecondary() {
      const r = this.editingRule;
      if (!r) return false;
      const primary = (this.instances || []).find(i => i.id === r.instanceId);
      if (!primary) return false;
      return (this.instances || []).some(i => i.id !== r.instanceId && i.type === primary.type);
    },

    // ruleSecondaryInstance — the resolved secondary instance for the
    // current rule. Prefers the explicit syncToInstanceId pick; falls
    // back to first other-of-same-type. Distinct from
    // reviewSecondaryInstance which only resolves when tag-sync is on
    // — the per-bucket target pickers care about secondary regardless
    // of whether tag-sync is configured (auto-tags can run on secondary
    // without tag mirroring).
    ruleSecondaryInstance() {
      const r = this.editingRule;
      if (!r) return {};
      const primary = (this.instances || []).find(i => i.id === r.instanceId);
      if (!primary) return {};
      if (r.options && r.options.syncToInstanceId) {
        const explicit = (this.instances || []).find(i => i.id === r.options.syncToInstanceId);
        if (explicit) return explicit;
      }
      return (this.instances || []).find(i => i.id !== r.instanceId && i.type === primary.type) || {};
    },

    // Active filter list — returns an array of human labels for every
    // enabled filter on the rule's per-rule snapshot. Empty array
    // when all are off (which means "no quality/audio gating — every
    // matched release group passes").
    reviewActiveFilters() {
      if (!this.editingRule || !this.editingRule.filters) return [];
      const f = this.editingRule.filters;
      const out = [];
      if (f.Quality)     out.push('Quality');
      if (f.MAWebDL)     out.push('MA WebDL');
      if (f.PlayWebDL)   out.push('Play WebDL');
      if (f.Audio)       out.push('Audio');
      if (f.TrueHD)      out.push('TrueHD');
      if (f.TrueHDAtmos) out.push('TrueHD Atmos');
      if (f.DTSX)        out.push('DTS:X');
      if (f.DTSHDMA)     out.push('DTS-HD MA');
      return out;
    },

    // Per-bucket summary helper used by the Audio / Video / DV
    // sections of Review. Returns one of:
    //   "Disabled"
    //   "Enabled — bare values, all allowed"
    //   "Enabled — prefix `audio-`, 4 of 12 values"
    // Plex label sync review helpers — render an inline Plex-sync
    // config as four readable lines for the Review step. cfg defaults
    // to the webhook field (editingRule.plexLabelSync); the schedule /
    // QFA review block passes editingRule.plexSync. Defensive against
    // partially-filled state — returns "(none picked)" not a crash.
    _reviewPlexCfg(cfg) {
      if (cfg) return cfg;
      return this.editingRule && this.editingRule.plexLabelSync;
    },

    reviewPlexInstanceName(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      if (!cfg || !cfg.plexInstanceId) return '(none picked)';
      const pi = (this.plexInstances || []).find(p => p.id === cfg.plexInstanceId);
      return pi ? pi.name : cfg.plexInstanceId;
    },

    reviewPlexLibraryTitles(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      if (!cfg || !cfg.libraryKeys || cfg.libraryKeys.length === 0) return '(none picked)';
      const pi = (this.plexInstances || []).find(p => p.id === cfg.plexInstanceId);
      const libs = pi ? (pi.libraries || []) : [];
      const titles = cfg.libraryKeys.map(k => {
        const l = libs.find(x => x.key === k);
        return l ? l.title : k;
      });
      return titles.join(', ');
    },

    reviewPlexTagsSummary(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      if (!cfg || !cfg.labels || cfg.labels.length === 0) return '(none picked)';
      const display = cfg.labelDisplay || {};
      const parts = cfg.labels.map(lbl => {
        const override = (display[lbl] || '').trim();
        return override && override !== lbl ? `${lbl} → ${override}` : lbl;
      });
      return parts.join(', ');
    },

    reviewPlexTargetTypesLabel(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      const types = (cfg && cfg.targetTypes && cfg.targetTypes.length > 0) ? cfg.targetTypes : ['label'];
      const hasLabel = types.includes('label');
      const hasCollection = types.includes('collection');
      if (hasLabel && hasCollection) return 'Plex labels and collections';
      if (hasCollection) return 'Plex collections only';
      return 'Plex labels';
    },

    reviewBucketSummary(bucket, vocabSize) {
      if (!bucket || !bucket.enabled) return 'Disabled';
      const parts = ['Enabled'];
      const prefix = bucket.prefix && bucket.prefix.trim();
      parts.push(prefix ? 'prefix `' + prefix + '`' : 'bare values');
      const av = bucket.allowedValues || [];
      if (av.length === 0) {
        parts.push('all values allowed');
      } else if (vocabSize) {
        parts.push(av.length + ' of ' + vocabSize + ' values');
      } else {
        parts.push(av.length + ' value' + (av.length === 1 ? '' : 's') + ' selected');
      }
      return parts.join(' — ').replace(' — ', ' · ');
    },

    // Mode-label for human-readable rendering. Combined-mode also
    // returns the chain order joined by arrows.
    reviewModeLabel() {
      if (!this.editingRule) return '';
      const m = this.editingRule.mode;
      const map = {
        tag: 'Tag quality releases',
        discover: 'Discover release groups',
        recover: 'Recover missing release groups',
        audiotags: 'Tag Audio',
        videotags: 'Tag Video',
        dvdetail: 'Tag DV Details',
        combined: 'Combined chain',
      };
      const label = map[m] || m;
      if (m !== 'combined') return label;
      const cm = (this.editingRule.options.combinedModes || []).map(x => map[x] || x);
      return label + (cm.length ? ' — ' + cm.join(' → ') : ' — (no phases picked)');
    },

    // Schedule label for non-quickfix rules.
    reviewScheduleLabel() {
      if (!this.editingRule) return '';
      // manualOnly is the authoritative UI flag — cron carries a default
      // placeholder during the wizard session (so the picker has
      // something to show if the user toggles manualOnly off again).
      // Save-time persists cron='' for manual rules; both paths land
      // on the same answer here.
      if (this.editingRule.manualOnly) {
        return 'Manual run only (Run-now button on the Run mode card)';
      }
      const cron = (this.editingRule.cron || '').trim();
      return cron === '' ? 'Manual run only (Run-now button on the Run mode card)' : cron;
    },

    // Tracks which auto-tag sections will actually emit on this run
    // — used by the Review template to decide which subsections to
    // render. Same gating as ruleAffectsAudio / ruleAffectsVideo.
    ruleAutoTagsSummary() {
      if (!this.editingRule) return '(none)';
      const parts = [];
      const a = this.editingRule.audioTags;
      if (a && a.audio && a.audio.enabled) parts.push('audio');
      const v = this.editingRule.videoTags;
      if (v) {
        ['resolution', 'codec', 'hdr'].forEach(k => {
          if (v[k] && v[k].enabled) parts.push(k);
        });
      }
      const dd = this.editingRule.dvDetail;
      if (dd && dd.enabled) parts.push('dv-detail');
      return parts.length === 0 ? '(none)' : parts.join(', ');
    },

    // Toast queue. Each toast gets its own timer so a fast burst (e.g. multiple
    // group adds in a row) shows every message stacked top-down instead of the
    // first one being clobbered by the next.
    showToast(msg, type = '') {
      const id = ++this._toastSeq;
      this.toasts = [...this.toasts, { id, msg, type }];
      // 8 s default — long enough that a user looking elsewhere when
      // the toast pops up still gets to read it. Click the × on the
      // toast to dismiss early; the markup wires dismissToast(id) to
      // every toast regardless of type so the user is never trapped
      // waiting for a wall of text to expire.
      setTimeout(() => {
        this.toasts = this.toasts.filter(t => t.id !== id);
      }, 8000);
    },

    // confirmDialog — Promise<boolean> wrapper around the shared
    // confirm-modal. Drop-in replacement for window.confirm():
    //
    //   if (!await this.confirmDialog({ title: 'Delete?', message: '...' })) return;
    //
    // Options: { title, message, confirmText, cancelText, kind }.
    // 'kind' is 'default' | 'danger' | 'warning' — drives the
    // Confirm-button colour. Default cancelText is 'Cancel'.
    //
    // Returns the previous Promise's resolution as false if a
    // second confirmDialog call comes in while one is open
    // (defensive — shouldn't happen with proper UI gating).
    confirmDialog(opts) {
      // Resolve any in-flight prompt as Cancel before opening a new
      // one to avoid orphaned promises.
      if (this.confirmModal.open && this.confirmModal._resolve) {
        try { this.confirmModal._resolve(false); } catch (_) { /* noop */ }
      }
      return new Promise((resolve) => {
        this.confirmModal = {
          open:        true,
          title:       opts && opts.title ? String(opts.title) : 'Are you sure?',
          message:     opts && opts.message ? String(opts.message) : '',
          confirmText: opts && opts.confirmText ? String(opts.confirmText) : 'Confirm',
          cancelText:  opts && opts.cancelText ? String(opts.cancelText) : 'Cancel',
          kind:        opts && opts.kind ? String(opts.kind) : 'default',
          _resolve:    resolve,
        };
      });
    },

    // Helpers wired by the confirm-modal template.
    _resolveConfirmDialog(value) {
      const r = this.confirmModal._resolve;
      this.confirmModal = { open: false, title: '', message: '', confirmText: 'Confirm', cancelText: 'Cancel', kind: 'default', _resolve: null };
      if (r) r(value);
    },
    confirmDialogOk()     { this._resolveConfirmDialog(true); },
    confirmDialogCancel() { this._resolveConfirmDialog(false); },

    // Manual dismiss — wired to the × button on every toast row.
    // Filters by id so dismissing one toast doesn't disturb others
    // that happen to be showing in the stack.
    dismissToast(id) {
      this.toasts = this.toasts.filter(t => t.id !== id);
    },

    // --- Instances ---
    openInstanceModal(inst) {
      if (inst) {
        this.instForm = {
          id: inst.id, name: inst.name, type: inst.type,
          iconVariant: inst.iconVariant || 'standard',
          url: inst.url, apiKey: inst.apiKey,
          // Deep-copy pathMappings so editor mutations don't bleed
          // into the live instances array before save. Empty/missing
          // → empty array so the editor renders cleanly.
          pathMappings: Array.isArray(inst.pathMappings)
            ? inst.pathMappings.map(m => ({ from: m.from || '', to: m.to || '' }))
            : [],
        };
      } else {
        this.instForm = {
          id: '', name: '', type: 'radarr', iconVariant: 'standard',
          url: '', apiKey: '', pathMappings: [],
        };
      }
      this.instFormError = '';
      this.instFormTestResult = '';
      this.instFormTestedOK = false;
      this.instFormTestedKey = '';
      this.instFormTestedVersion = '';
      this.showInstModal = true;
    },

    addInstancePathMapping() {
      this.instForm.pathMappings = [...(this.instForm.pathMappings || []), { from: '', to: '' }];
      this.instFormPathMappingsOpen = true;
    },
    removeInstancePathMapping(idx) {
      this.instForm.pathMappings = (this.instForm.pathMappings || []).filter((_, i) => i !== idx);
    },

    async saveInstance() {
      this.instFormError = '';
      // Filter out empty rows from the path-mapping editor before save
      // — saving a half-filled mapping would silently fail-open at
      // translation time. Both sides must be non-empty to count.
      const mappings = (this.instForm.pathMappings || [])
        .map(m => ({ from: (m.from || '').trim(), to: (m.to || '').trim() }))
        .filter(m => m.from !== '' && m.to !== '');
      const body = {
        name: this.instForm.name, type: this.instForm.type,
        iconVariant: this.instForm.iconVariant,
        url: this.instForm.url, apiKey: this.instForm.apiKey,
        pathMappings: mappings,
      };
      // Preserve Test result across save: if url+apiKey match the tested combo,
      // reuse the cached version from the Test call so status shows 'connected' immediately.
      const curKey = this.instForm.url + '|' + this.instForm.apiKey;
      const preserveVersion = (this.instFormTestedOK && curKey === this.instFormTestedKey)
        ? this.instFormTestedVersion : null;
      try {
        const url = this.instForm.id ? `/api/instances/${this.instForm.id}` : '/api/instances';
        const method = this.instForm.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        if (!r.ok) { const d = await r.json(); throw new Error(d.error || 'HTTP ' + r.status); }
        const saved = this.instForm.id ? null : await r.json();
        this.showInstModal = false;
        await this.loadConfig();
        // Carry over the Test result if it still applies
        if (preserveVersion != null) {
          const id = this.instForm.id || saved?.id;
          if (id) {
            this.instStatus[id] = 'connected';
            this.instVersion[id] = preserveVersion;
          }
        }
        this.refreshAllStatus();
        this.showToast(this.instForm.id ? 'Instance updated' : 'Instance added', 'success');
      } catch (e) {
        this.instFormError = e.message;
      }
    },

    async deleteInstance(inst) {
      if (!await this.confirmDialog({
        title:       `Delete "${inst.name}"?`,
        message:     'Removes the Sonarr/Radarr connection from resolvarr. Webhooks, rules, and schedules tied to this instance will lose their link.',
        confirmText: 'Delete',
        kind:        'danger',
      })) return;
      try {
        const r = await this.apiFetch(`/api/instances/${inst.id}`, { method: 'DELETE' });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        await this.loadConfig();
        this.showToast('Instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

    async testInstance(inst, silent = false) {
      if (!silent) this.instStatus[inst.id] = 'testing';
      this.instError[inst.id] = '';
      try {
        const r = await this.apiFetch(`/api/instances/${inst.id}/test`, { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.instStatus[inst.id] = 'connected';
        this.instVersion[inst.id] = d.version;
      } catch (e) {
        this.instStatus[inst.id] = 'failed';
        this.instError[inst.id] = e.message;
      }
    },

    async testInstanceForm() {
      this.instFormTesting = true;
      this.instFormTestResult = '';
      this.instFormTestedOK = false;
      this.instFormTestedVersion = '';
      try {
        const body = {
          name: this.instForm.name || 'test', type: this.instForm.type,
          iconVariant: this.instForm.iconVariant,
          url: this.instForm.url, apiKey: this.instForm.apiKey,
        };
        const r = await this.apiFetch('/api/instances/test', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.instFormTestResult = 'OK — v' + d.version;
        this.instFormTestedOK = true;
        this.instFormTestedKey = this.instForm.url + '|' + this.instForm.apiKey;
        this.instFormTestedVersion = d.version;
      } catch (e) {
        this.instFormTestResult = 'Failed: ' + e.message;
      } finally {
        this.instFormTesting = false;
      }
    },

    // ============ Security panel ============

    // Hydrate the security form from the live config + auth-status
    // endpoint. authStatus surfaces trustedNetworksLocked /
    // trustedProxiesLocked so the inputs disable when env vars override.
    async loadSecurityPanel() {
      this.securitySaveMsg = '';
      try {
        const r = await this.apiFetch('/api/auth/status');
        if (r.ok) {
          const d = await r.json();
          // Backend uses snake_case for these keys (auth_handlers.go).
          this.authStatus = {
            trustedNetworksLocked: !!(d.trusted_networks_locked || d.trustedNetworksLocked),
            trustedProxiesLocked:  !!(d.trusted_proxies_locked  || d.trustedProxiesLocked),
          };
        }
      } catch {}
      // Hydrate form from current config (already loaded into this.config
      // by loadConfig on init).
      const c = this.config || {};
      this.securityForm = {
        authentication:         c.authentication || 'forms',
        authenticationRequired: c.authenticationRequired || 'disabled_for_local_addresses',
        trustedNetworks:        c.trustedNetworks || '',
        trustedProxies:         c.trustedProxies || '',
        sessionTtlDays:         c.sessionTtlDays || 30,
      };
      this.securityFormDirty = false;
      // Lazy-fetch the API key — server returns it in plaintext (auth-
      // gated endpoint), we mask it client-side until "Show" is clicked.
      this.fetchSecurityApiKey();
    },

    async fetchSecurityApiKey() {
      try {
        const r = await this.apiFetch('/api/auth/api-key');
        if (!r.ok) return;
        const d = await r.json();
        // Backend returns api_key (snake_case in auth_handlers.go).
        this.securityApiKey = d.api_key || d.apiKey || '';
      } catch {}
    },

    async copySecurityApiKey() {
      if (!this.securityApiKey) return;
      const ok = await this.copyToClipboard(this.securityApiKey);
      if (ok) {
        this.securityApiKeyCopied = true;
        setTimeout(() => { this.securityApiKeyCopied = false; }, 2000);
      } else {
        this.showToast('Copy failed — your browser blocked clipboard access', 'error');
      }
    },

    async regenerateApiKey() {
      this.securityRegenerating = true;
      try {
        const r = await this.apiFetch('/api/auth/regenerate-api-key', { method: 'POST' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        const d = await r.json();
        this.securityApiKey = d.api_key || d.apiKey || '';
        this.securityApiKeyVisible = true;
        this.securityRegenConfirm = false;
        this.showToast('API key regenerated — old key invalid immediately', 'success');
      } catch (e) {
        this.showToast('Regenerate failed: ' + e.message, 'error');
      } finally {
        this.securityRegenerating = false;
      }
    },

    async saveSecurityConfig() {
      this.securitySaving = true;
      this.securitySaveMsg = '';
      try {
        const r = await this.apiFetch('/api/config/auth', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.securityForm),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Mirror saved values back into the live config object so other
        // surfaces (the loadConfig cache) stay consistent.
        Object.assign(this.config || {}, this.securityForm);
        this.securityFormDirty = false;
        this.securitySaveOk = true;
        this.securitySaveMsg = 'Saved.';
        setTimeout(() => { this.securitySaveMsg = ''; }, 4000);
      } catch (e) {
        this.securitySaveOk = false;
        this.securitySaveMsg = e.message || 'Save failed';
      } finally {
        this.securitySaving = false;
      }
    },

    async changePassword() {
      // Client-side belt-and-braces — server validates too.
      if (this.pwChange.next !== this.pwChange.confirm) {
        this.pwChangeOk = false;
        this.pwChangeMsg = 'New password and confirmation do not match';
        return;
      }
      this.pwChangeSaving = true;
      this.pwChangeMsg = '';
      try {
        const r = await this.apiFetch('/api/auth/change-password', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          // Backend uses snake_case (auth_handlers.go:347-349) — must
          // send current_password / new_password / new_password_confirm
          // exactly so the JSON decoder picks them up.
          body: JSON.stringify({
            current_password:     this.pwChange.current,
            new_password:         this.pwChange.next,
            new_password_confirm: this.pwChange.confirm,
          }),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.pwChangeOk = true;
        this.pwChangeMsg = 'Password changed. Other sessions signed out.';
        this.pwChange = { current: '', next: '', confirm: '' };
        setTimeout(() => { this.pwChangeMsg = ''; }, 6000);
      } catch (e) {
        this.pwChangeOk = false;
        this.pwChangeMsg = e.message || 'Change failed';
      } finally {
        this.pwChangeSaving = false;
      }
    },

    // POST /logout — invalidates this browser's session cookie and
    // redirects to /login. Other sessions stay active. Best-effort:
    // even if the POST fails (network blip), we still nuke the cookie
    // client-side via redirect so the user isn't stuck on a stale page.
    async logout() {
      try {
        await this.apiFetch('/logout', { method: 'POST' });
      } catch (_) {
        // Ignore — we redirect regardless.
      }
      window.location.href = '/login';
    },

    // ============ Notification agents (multi-provider) ============

    // Load the agents list from the server, masking credentials applied
    // server-side. Called on settings section open, after a CRUD action,
    // and after the modal closes.
    async loadAgents() {
      this.agentsLoading = true;
      this.agentsLoadError = '';
      try {
        const r = await this.apiFetch('/api/notifications/agents');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.agents = (await r.json()) || [];
      } catch (e) {
        this.agentsLoadError = e.message;
      } finally {
        this.agentsLoading = false;
      }
    },

    // Provider icon path. Most types ship an SVG; Gotify and Apprise
    // ship a PNG bitmap (no clean SVG available upstream).
    agentIconSrc(type) {
      if (type === 'gotify' || type === 'apprise') {
        return `/icons/${type}.png`;
      }
      return `/icons/${type}.svg`;
    },

    // Open the modal — empty form for Add, prefilled for Edit. Editing
    // sets agentModal.testPassed=true so trivial edits (renaming, event-
    // toggle) save without re-testing; touching any credential input
    // resets it to false via the @input handler.
    openAgentModal(a) {
      // Event defaults: schedule events on (legacy default), webhook
      // events off. Existing agent's Events object is round-tripped
      // wholesale via Object.assign so future flag additions don't
      // get silently zeroed on save.
      const eventDefaults = {
        onScheduleSuccess: true, onScheduleFailure: true,
        onImport: false, onGrab: false, onFileDelete: false,
      };
      if (a) {
        this.agentModal = {
          id: a.id,
          name: a.name || '',
          type: a.type,
          enabled: !!a.enabled,
          events: Object.assign({}, eventDefaults, a.events || {}),
          functions: Array.isArray(a.functions) ? a.functions.slice() : [],
          config: Object.assign({}, a.config || {}),
          busy: false,
          testing: false,
          testPassed: true, // existing agent — was tested before save
          testResult: '',
          error: '',
        };
      } else {
        this.agentModal = {
          id: '',
          name: '',
          type: 'discord',
          enabled: true,
          events: Object.assign({}, eventDefaults),
          functions: [],
          config: {},
          busy: false,
          testing: false,
          testPassed: false,
          testResult: '',
          error: '',
        };
      }
      this.showAgentModal = true;
    },

    // Functions checkbox handler — adds/removes the function ID from
    // the agentModal.functions array. Empty array (= no filter) is
    // the default; non-empty acts as a whitelist.
    toggleAgentFunction(id, checked) {
      if (checked) {
        if (!this.agentModal.functions.includes(id)) {
          this.agentModal.functions.push(id);
        }
      } else {
        this.agentModal.functions = this.agentModal.functions.filter(f => f !== id);
      }
    },

    closeAgentModal() {
      if (this.agentModal.busy) return;
      this.showAgentModal = false;
    },

    onAgentTypeChange() {
      // Switching type wipes any provider-specific creds the user typed
      // for the old type — they don't apply to the new one. Reset
      // testPassed too since fresh config needs verification.
      this.agentModal.config = {};
      this.agentModal.testPassed = false;
      this.agentModal.testResult = '';
      this.agentModal.error = '';
    },

    // Returns true when the modal has enough fields populated to even
    // attempt a Test. Discord needs a webhook URL; Gotify needs URL +
    // token; etc. Empty fields → button greyed out.
    agentTypeFilled() {
      const c = this.agentModal.config || {};
      switch (this.agentModal.type) {
        case 'discord':  return !!(c.discordWebhook || '').trim();
        case 'gotify':   return !!(c.gotifyUrl || '').trim() && !!(c.gotifyToken || '').trim();
        case 'ntfy':     return !!(c.ntfyUrl || '').trim() && !!(c.ntfyTopic || '').trim();
        case 'pushover': return !!(c.pushoverUserKey || '').trim() && !!(c.pushoverAppToken || '').trim();
        case 'apprise':  return !!(c.appriseUrl || '').trim() && (c.appriseUrls || []).some(u => (u || '').trim() !== '');
        default:         return false;
      }
    },

    // Save is enabled when (a) the form has a name, (b) credentials are
    // populated for the active type, and (c) a successful test has run
    // since the last credential edit (testPassed). Edit mode arrives
    // with testPassed=true so renames and event-toggle changes save
    // without re-testing; credential input resets it.
    canSaveAgent() {
      if (!(this.agentModal.name || '').trim()) return false;
      if (!this.agentTypeFilled()) return false;
      return this.agentModal.testPassed;
    },

    // Build the body sent to /api/notifications/agents{,/test}.
    // Events object is round-tripped fully — openAgentModal hydrates
    // ALL event keys from the existing agent (including webhook events
    // not yet exposed in the modal: onImport / onGrab / onUpgrade /
    // onFileDelete). This Object.assign preserves them so a save round-
    // trip doesn't wipe webhook flags set by future UI surfaces.
    agentRequestBody() {
      return {
        id:        this.agentModal.id,
        name:      (this.agentModal.name || '').trim(),
        type:      this.agentModal.type,
        enabled:   !!this.agentModal.enabled,
        events:    Object.assign({}, this.agentModal.events),
        functions: (this.agentModal.functions || []).slice(),
        config:    Object.assign({}, this.agentModal.config),
      };
    },

    // Inline-test using the modal's current values without saving.
    // POST /api/notifications/agents/test. Server receives the in-form
    // credentials (or the masked placeholder + ID for an edit, which
    // it preserves to the stored value).
    async testInlineAgent() {
      this.agentModal.testing = true;
      this.agentModal.error = '';
      this.agentModal.testResult = '';
      this.agentModal.testPassed = false;
      try {
        const r = await this.apiFetch('/api/notifications/agents/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.agentRequestBody()),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        const results = (d.results || []);
        const failed = results.filter(x => x.status !== 'ok');
        if (results.length === 0) {
          // Provider returned no channels — treat as failure so a buggy
          // provider can't slip past the Save guard with an empty pass.
          this.agentModal.testPassed = false;
          this.agentModal.testResult = 'Test returned no results.';
        } else if (failed.length === 0) {
          this.agentModal.testPassed = true;
          this.agentModal.testResult = results.length === 1
            ? 'Sent successfully.'
            : `${results.length} channel(s) verified.`;
        } else {
          this.agentModal.testPassed = false;
          this.agentModal.testResult = failed.map(f => (f.label ? f.label + ': ' : '') + (f.error || 'failed')).join(' · ');
        }
      } catch (e) {
        this.agentModal.testResult = e.message || 'Test failed';
      } finally {
        this.agentModal.testing = false;
      }
    },

    // Save creates (POST) when no ID, updates (PUT) when present.
    // After success, refreshes the list and closes the modal.
    async saveAgent() {
      this.agentModal.busy = true;
      this.agentModal.error = '';
      try {
        const url = this.agentModal.id
          ? '/api/notifications/agents/' + encodeURIComponent(this.agentModal.id)
          : '/api/notifications/agents';
        const method = this.agentModal.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(url, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.agentRequestBody()),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadAgents();
        this.showToast(this.agentModal.id ? 'Agent updated' : 'Agent added', 'success');
        this.showAgentModal = false;
      } catch (e) {
        this.agentModal.error = e.message || 'Save failed';
      } finally {
        this.agentModal.busy = false;
      }
    },

    // Toggle the Enabled flag inline from the agents-list row. Sends a
    // PUT with the existing config (server preserves masked creds).
    async toggleAgentEnabled(a) {
      if (this.agentBusyId) return;
      this.agentBusyId = a.id;
      try {
        const r = await this.apiFetch('/api/notifications/agents/' + encodeURIComponent(a.id), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: a.id, name: a.name, type: a.type,
            enabled: !a.enabled,
            events: a.events || {},
            config: a.config || {},
          }),
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadAgents();
      } catch (e) {
        this.showToast('Toggle failed: ' + e.message, 'error');
      } finally {
        this.agentBusyId = null;
      }
    },

    // Test a saved agent inline from the list. Hits POST /agents/{id}/test
    // which uses the stored credentials (no need to re-send). Result lands
    // in agentTestStatus[agent.id] for the inline status indicator next
    // to the Test button — green ✓ / red ✗ / per-channel breakdown.
    async testSavedAgent(a) {
      this.agentTestStatus = Object.assign({}, this.agentTestStatus, {
        [a.id]: { testing: true, results: [] },
      });
      try {
        const r = await this.apiFetch('/api/notifications/agents/' + encodeURIComponent(a.id) + '/test', { method: 'POST' });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.agentTestStatus = Object.assign({}, this.agentTestStatus, {
          [a.id]: { testing: false, results: d.results || [] },
        });
      } catch (e) {
        this.agentTestStatus = Object.assign({}, this.agentTestStatus, {
          [a.id]: { testing: false, results: [{ label: 'test', status: 'error', error: e.message || 'failed' }] },
        });
      }
    },

    openDeleteAgent(a) {
      this.deleteAgentTarget = a;
    },

    async confirmDeleteAgent() {
      if (!this.deleteAgentTarget) return;
      this.deleteAgentBusy = true;
      const deletedId = this.deleteAgentTarget.id;
      try {
        const r = await this.apiFetch('/api/notifications/agents/' + encodeURIComponent(deletedId), { method: 'DELETE' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Drop the per-row test status entry so it doesn't linger
        // forever in agentTestStatus after the agent is gone.
        if (this.agentTestStatus[deletedId]) {
          const next = Object.assign({}, this.agentTestStatus);
          delete next[deletedId];
          this.agentTestStatus = next;
        }
        await this.loadAgents();
        this.showToast('Agent deleted', 'success');
        this.deleteAgentTarget = null;
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteAgentBusy = false;
      }
    },

    // --- Tags ---
    currentInstanceType() {
      const inst = this.instances.find(i => i.id === this.tagsInstanceId);
      return inst ? inst.type : '';
    },

    // Filtered instance list for the Tag inventory dropdown — only the
    // active app-type. Sorted by name for stable order.
    tagsAvailableInstances() {
      return this.instances
        .filter(i => i.type === this.tagsAppType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // True if at least one instance of the given app-type is configured.
    // Drives the disabled+tooltip state on the App-type pills.
    tagsAppTypeAvailable(type) {
      return this.instances.some(i => i.type === type);
    },

    // Switch the active app-type. Persists to localStorage, re-picks the
    // first matching instance (or clears if none), and reloads tags.
    setTagsAppType(type) {
      if (type !== 'radarr' && type !== 'sonarr') return;
      if (!this.tagsAppTypeAvailable(type)) return;
      if (this.tagsAppType === type) return;
      this.tagsAppType = type;
      localStorage.setItem('resolvarr-tags-app-type', type);
      // Reset selection state — different instance pool means stale selections.
      this.tagsSelected = new Set();
      this.compareOpen = false;
      this.compareResults = null;
      this.compareCrossInstanceTarget = '';
      // Pick first matching instance, or clear if no instances of this type.
      const first = this.tagsAvailableInstances()[0];
      if (first) {
        this.tagsInstanceId = first.id;
        localStorage.setItem('resolvarr-tags-instance', first.id);
        this.loadTags();
      } else {
        this.tagsInstanceId = '';
        localStorage.removeItem('resolvarr-tags-instance');
        this.tags = [];
      }
      // App-type switch invalidates the search cache (tags + items belong
      // to the previous instance) and clears the search field.
      this.resetTagSearchState({ clearCache: true });
      this.pushNav();
    },

    // Resets the tag-search UI to its empty-query state. Used both on
    // app-type switch (cache invalid) and on plain instance-dropdown
    // switch within the same app-type (cache for the new instance may
    // not exist yet, but query/expanded state from the previous instance
    // shouldn't carry over). Pass clearCache:true to also wipe the
    // payload cache and bump the version counter.
    resetTagSearchState(opts = {}) {
      this.tagSearchQuery = '';
      this.tagSearchExpanded = {};
      this.tagSearchError = '';
      this.tagSearchParseError = '';
      if (opts.clearCache) {
        this.tagSearchCache = {};
        this.tagSearchCacheVersion++;
      }
      this._tagSearchResultsKey = '';
      this._tagSearchResultsValue = null;
    },

    // ---- Tag search (Mode B — query items by tag combinations) ----

    // Map of bucket-name → set of tag-name strings, derived from the
    // currently-loaded ExtraTags / AudioTags / VideoTags / DvDetail config.
    // Used by the bucket: macro in the DSL.
    tagSearchBuckets() {
      const out = {};
      const expand = (vocab, prefix) => {
        const set = new Set();
        for (const v of (vocab || [])) {
          set.add(v);
          if (prefix) set.add(prefix + v);
        }
        return set;
      };
      // Audio sub-buckets share one prefix; expose individual sub-buckets
      // and a combined 'audio' alias for convenience.
      const audioPrefix = (this.audioTags && this.audioTags.audio && this.audioTags.audio.prefix) || '';
      const audioCodec    = expand(this.audioVocab && this.audioVocab.codecs,   audioPrefix);
      const audioChannels = expand(this.audioVocab && this.audioVocab.channels, audioPrefix);
      const audioFlags    = expand(this.audioVocab && this.audioVocab.flags,    audioPrefix);
      out['audio-codec']    = audioCodec;
      out['audio-channels'] = audioChannels;
      out['audio-flags']    = audioFlags;
      out['audio'] = new Set([...audioCodec, ...audioChannels, ...audioFlags]);
      // Video sub-buckets — each has its own prefix in config.
      const vt = this.videoTags || {};
      out['resolution'] = expand(this.videoVocab && this.videoVocab.resolution, vt.resolution && vt.resolution.prefix);
      out['codec']      = expand(this.videoVocab && this.videoVocab.codec,      vt.codec      && vt.codec.prefix);
      out['hdr']        = expand(this.videoVocab && this.videoVocab.hdr,        vt.hdr        && vt.hdr.prefix);
      // DV detail.
      const dvPrefix = (this.dvDetail && this.dvDetail.prefix) || '';
      out['dv-detail'] = expand(this.dvDetailVocab, dvPrefix);
      return out;
    },

    // Tokenise a query into a flat token stream. Tokens:
    //   {kind: 'and' | 'or' | 'not' | 'lparen' | 'rparen'}
    //   {kind: 'term', value: 'tagname', wildcard: bool, bucket: 'name'?}
    // Implicit AND is inserted later during the parse phase.
    tagSearchTokenise(input) {
      const tokens = [];
      const re = /\s*([()]|[^\s()]+)/g;
      let m;
      while ((m = re.exec(input)) !== null) {
        const raw = m[1];
        if (raw === '(') { tokens.push({ kind: 'lparen' }); continue; }
        if (raw === ')') { tokens.push({ kind: 'rparen' }); continue; }
        const lower = raw.toLowerCase();
        if (lower === 'and') { tokens.push({ kind: 'and' }); continue; }
        if (lower === 'or')  { tokens.push({ kind: 'or'  }); continue; }
        if (lower === 'not') { tokens.push({ kind: 'not' }); continue; }
        if (raw.startsWith('-') && raw.length > 1) {
          tokens.push({ kind: 'not' });
          tokens.push(this.tagSearchTermToken(raw.slice(1)));
          continue;
        }
        tokens.push(this.tagSearchTermToken(raw));
      }
      return tokens;
    },

    tagSearchTermToken(raw) {
      const lower = raw.toLowerCase();
      if (lower.startsWith('bucket:')) {
        return { kind: 'term', bucket: lower.slice('bucket:'.length), value: '', wildcard: false };
      }
      if (lower.endsWith('*')) {
        return { kind: 'term', value: lower.slice(0, -1), wildcard: true };
      }
      return { kind: 'term', value: lower, wildcard: false };
    },

    // Parse the token stream into an AST. Recursive-descent with implicit
    // AND. AST nodes: {op:'and'|'or', left, right} | {op:'not', child} |
    // {op:'term', value, wildcard, bucket?}. Throws on syntax errors.
    // Validates bucket: macros against the live bucket-name set so users
    // get an immediate error instead of a silent zero-match.
    tagSearchParse(input, validBuckets) {
      const tokens = this.tagSearchTokenise(input);
      if (tokens.length === 0) return null;
      // Reject empty-prefix wildcards ("*" alone) and bucket: with no name —
      // both produce match-everything-or-nothing surprises.
      for (const t of tokens) {
        if (t.kind !== 'term') continue;
        if (t.bucket !== undefined) {
          if (!t.bucket) throw new Error("'bucket:' needs a name (e.g. bucket:dv-detail)");
          if (validBuckets && !validBuckets.has(t.bucket)) {
            const list = validBuckets ? [...validBuckets].sort().join(', ') : '';
            throw new Error("unknown bucket '" + t.bucket + "' — valid: " + list);
          }
        } else if (t.wildcard && !t.value) {
          throw new Error("'*' on its own matches every tag — narrow it (e.g. rg-*)");
        }
      }
      let pos = 0;
      const peek = () => tokens[pos];
      const consume = (kind) => {
        if (!peek() || peek().kind !== kind) throw new Error('expected ' + kind);
        return tokens[pos++];
      };
      // expr := orExpr
      // orExpr := andExpr (OR andExpr)*
      // andExpr := unary (AND unary | unary)*    -- implicit AND when no operator
      // unary := NOT unary | atom
      // atom := term | '(' expr ')'
      const parseAtom = () => {
        const t = peek();
        if (!t) throw new Error('unexpected end of input');
        if (t.kind === 'lparen') {
          consume('lparen');
          const inner = parseOr();
          if (!peek() || peek().kind !== 'rparen') throw new Error("missing ')'");
          consume('rparen');
          return inner;
        }
        if (t.kind === 'term') {
          pos++;
          return { op: 'term', value: t.value, wildcard: !!t.wildcard, bucket: t.bucket };
        }
        throw new Error("unexpected token '" + (t.kind) + "'");
      };
      const parseUnary = () => {
        if (peek() && peek().kind === 'not') {
          consume('not');
          return { op: 'not', child: parseUnary() };
        }
        return parseAtom();
      };
      const parseAnd = () => {
        let left = parseUnary();
        while (peek() && peek().kind !== 'or' && peek().kind !== 'rparen') {
          if (peek().kind === 'and') consume('and'); // explicit AND
          // implicit AND: just continue parsing the next unary
          const right = parseUnary();
          left = { op: 'and', left, right };
        }
        return left;
      };
      const parseOr = () => {
        let left = parseAnd();
        while (peek() && peek().kind === 'or') {
          consume('or');
          const right = parseAnd();
          left = { op: 'or', left, right };
        }
        return left;
      };
      const result = parseOr();
      if (pos !== tokens.length) throw new Error("unexpected trailing input");
      return result;
    },

    // Evaluate the AST against an item. itemTagLabels is a Set of
    // lowercase tag-label strings the item carries. allTagLabels is the
    // full inventory (for wildcard expansion); buckets is the bucket map.
    tagSearchEval(node, itemTagLabels, allTagLabels, buckets) {
      if (!node) return true;
      if (node.op === 'and') {
        return this.tagSearchEval(node.left, itemTagLabels, allTagLabels, buckets)
            && this.tagSearchEval(node.right, itemTagLabels, allTagLabels, buckets);
      }
      if (node.op === 'or') {
        return this.tagSearchEval(node.left, itemTagLabels, allTagLabels, buckets)
            || this.tagSearchEval(node.right, itemTagLabels, allTagLabels, buckets);
      }
      if (node.op === 'not') {
        return !this.tagSearchEval(node.child, itemTagLabels, allTagLabels, buckets);
      }
      if (node.op === 'term') {
        // Bucket macro — match if the item carries any tag from the bucket.
        if (node.bucket) {
          const set = buckets[node.bucket];
          if (!set) return false; // unknown bucket → no match
          for (const tag of itemTagLabels) {
            if (set.has(tag)) return true;
          }
          return false;
        }
        // Wildcard — match if any item-tag starts with the prefix.
        if (node.wildcard) {
          const prefix = node.value;
          for (const tag of itemTagLabels) {
            if (tag.startsWith(prefix)) return true;
          }
          return false;
        }
        // Exact (case-insensitive) match.
        return itemTagLabels.has(node.value);
      }
      return false;
    },

    // Returns an array of label strings the term matches against the
    // current inventory — used to highlight matching tag chips on the
    // result rows. For NOT nodes, returns the negative set's labels too
    // so the user can see WHY a row matched ("doesn't have any of …").
    // Keeps it simple: returns positive matches only, NOT-branches are
    // skipped for highlight purposes.
    tagSearchHighlightSet(node, allTagLabels, buckets) {
      const out = new Set();
      const walk = (n, negated) => {
        if (!n) return;
        if (n.op === 'and' || n.op === 'or') {
          walk(n.left, negated);
          walk(n.right, negated);
          return;
        }
        if (n.op === 'not') {
          walk(n.child, !negated);
          return;
        }
        if (n.op === 'term' && !negated) {
          if (n.bucket) {
            const set = buckets[n.bucket];
            if (set) for (const t of set) out.add(t);
            return;
          }
          if (n.wildcard) {
            for (const t of allTagLabels) if (t.startsWith(n.value)) out.add(t);
            return;
          }
          out.add(n.value);
        }
      };
      walk(node, false);
      return out;
    },

    // Compute results for the current query — returns {items, highlight,
    // matchedTotal, libraryTotal} or null if query is empty.
    // Memoised: the function is invoked from inside x-for chip loops, so
    // a 2000-movie library would otherwise pay a full scan per chip per
    // render. Cache key combines query + instance + cacheVersion so any
    // state change that affects results busts the memo.
    tagSearchResults() {
      const q = this.tagSearchQuery.trim();
      if (!q) return null;
      const cache = this.tagSearchCache[this.tagsInstanceId];
      if (!cache) return null;
      const buckets = this.tagSearchBuckets();
      const validBucketNames = new Set(Object.keys(buckets));
      const key = q + '|' + this.tagsInstanceId + '|' + this.tagSearchCacheVersion;
      if (key === this._tagSearchResultsKey && this._tagSearchResultsValue) {
        return this._tagSearchResultsValue;
      }
      let ast;
      try {
        ast = this.tagSearchParse(q, validBucketNames);
        this.tagSearchParseError = '';
      } catch (e) {
        this.tagSearchParseError = e.message;
        const errOut = { items: [], highlight: new Set(), matchedTotal: 0, libraryTotal: cache.items.length };
        this._tagSearchResultsKey = key;
        this._tagSearchResultsValue = errOut;
        return errOut;
      }
      const idToLabel = new Map();
      const allLabels = new Set();
      for (const t of cache.tags) {
        const lbl = t.label.toLowerCase();
        idToLabel.set(t.id, lbl);
        allLabels.add(lbl);
      }
      const matched = [];
      for (const it of cache.items) {
        const labelSet = new Set();
        for (const tid of it.tags) {
          const lbl = idToLabel.get(tid);
          if (lbl) labelSet.add(lbl);
        }
        if (this.tagSearchEval(ast, labelSet, allLabels, buckets)) {
          matched.push({
            ...it,
            tagLabels: [...labelSet].sort(),
          });
        }
      }
      const result = {
        items: matched,
        highlight: this.tagSearchHighlightSet(ast, allLabels, buckets),
        matchedTotal: matched.length,
        libraryTotal: cache.items.length,
      };
      this._tagSearchResultsKey = key;
      this._tagSearchResultsValue = result;
      return result;
    },

    async loadTagSearchInventory(force = false) {
      // Snapshot the instance id at call time. If the user switches the
      // dropdown before the fetch resolves, the response belongs to the
      // PREVIOUS instance and must not be written to the new instance's
      // cache slot. The snapshot also drives the URL so the request is
      // never built against a stale id mid-flight.
      const inst = this.tagsInstanceId;
      if (!inst) return;
      if (!force && this.tagSearchCache[inst]) return;
      this.tagSearchLoading = true;
      this.tagSearchError = '';
      try {
        const r = await this.apiFetch(`/api/instances/${inst}/items-with-tags`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Stale-response guard: if instance switched during fetch, drop
        // the result on the floor — the new instance's load (if any)
        // will populate the right cache slot on its own.
        if (inst !== this.tagsInstanceId) return;
        this.tagSearchCache[inst] = {
          tags: d.tags || [],
          items: d.items || [],
        };
        this.tagSearchCacheVersion++;
      } catch (e) {
        if (inst === this.tagsInstanceId) {
          this.tagSearchError = 'Could not load library: ' + e.message;
        }
      } finally {
        if (inst === this.tagsInstanceId) {
          this.tagSearchLoading = false;
        }
      }
    },

    // Called when the user types in the search field. Loads inventory
    // lazily on first non-empty query.
    onTagSearchInput() {
      this.tagSearchExpanded = {};
      if (this.tagSearchQuery.trim() && !this.tagSearchCache[this.tagsInstanceId]) {
        this.loadTagSearchInventory();
      }
    },

    clearTagSearch() {
      this.tagSearchQuery = '';
      this.tagSearchParseError = '';
      this.tagSearchExpanded = {};
    },

    toggleTagSearchRow(itemId) {
      this.tagSearchExpanded = { ...this.tagSearchExpanded, [itemId]: !this.tagSearchExpanded[itemId] };
    },

    // Per-row expand toggle for the tag inventory drill-down. Mirrors the
    // search-results pattern so both views feel the same; rows start
    // collapsed so the drill-down doesn't dump every field on screen.
    toggleTagItemRow(itemId) {
      this.tagItemExpanded = { ...this.tagItemExpanded, [itemId]: !this.tagItemExpanded[itemId] };
    },

    currentInstanceTypeLabel() {
      const t = this.currentInstanceType();
      if (!t) return 'the instance';
      return t.charAt(0).toUpperCase() + t.slice(1);
    },

    // itemLabel returns "movie"/"movies" for Radarr, "series"/"series" for Sonarr.
    itemLabel(n) {
      if (this.currentInstanceType() === 'radarr') return n === 1 ? 'movie' : 'movies';
      if (this.currentInstanceType() === 'sonarr') return 'series';
      return n === 1 ? 'item' : 'items';
    },

    usageColumnLabel() {
      if (this.currentInstanceType() === 'radarr') return 'Movies';
      if (this.currentInstanceType() === 'sonarr') return 'Series';
      return 'Used by';
    },

    setSort(col) {
      if (this.tagsSort === col) {
        this.tagsSortDir = this.tagsSortDir === 'asc' ? 'desc' : 'asc';
      } else {
        this.tagsSort = col;
        // Sensible defaults: label A→Z, usage most-first.
        this.tagsSortDir = col === 'label' ? 'asc' : 'desc';
      }
    },

    sortedTags() {
      const dir = this.tagsSortDir === 'asc' ? 1 : -1;
      const out = [...this.tags];
      if (this.tagsSort === 'label') {
        out.sort((a, b) => a.label.localeCompare(b.label) * dir);
      } else {
        out.sort((a, b) => {
          const d = (a.usageCount - b.usageCount) * dir;
          return d !== 0 ? d : a.label.localeCompare(b.label);
        });
      }
      return out;
    },

    deleteTargetsTotalUsage() {
      return this.deleteTargets.reduce((s, t) => s + (t.usageCount || 0), 0);
    },

    async loadTags() {
      if (!this.tagsInstanceId) return;
      this.tagsLoading = true;
      this.tagsLoadError = '';
      this.tagsSelected = new Set();
      // Reset drill-down state — tag IDs may not be the same on a different
      // instance, and a Reload should re-fetch any expanded sections so the
      // items list reflects current state (just-applied tags, etc).
      this.tagExpanded = {};
      this.tagItems = {};
      this.tagItemsLoading = {};
      this.tagItemsError = {};
      // Per-row expanded state must reset too — the next instance may have
      // an item with the same internal ID as one the user expanded here,
      // and Alpine would re-open the wrong row with the new instance's data.
      this.tagItemExpanded = {};
      // Same reasoning for compare — selected tag IDs may not exist on the
      // newly-loaded instance, and stale results would be misleading.
      this.compareOpen = false;
      this.compareResults = null;
      this.compareError = '';
      this.compareExpanded = { both: false, onlyA: false, onlyB: false };
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.tags = d || [];
      } catch (e) {
        this.tagsLoadError = e.message;
        this.tags = [];
      } finally {
        this.tagsLoading = false;
      }
    },

    // Returns instances of the same Arr type as the current Tag inventory
    // instance, excluding it. Drives the cross-instance picker dropdown.
    compareCrossInstanceCandidates() {
      const t = this.currentInstanceType();
      if (!t) return [];
      return this.instances
        .filter(i => i.type === t && i.id !== this.tagsInstanceId)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // Compare button enables when:
    //   - 2 tags selected from current instance and no cross-instance target
    //     (original same-instance flow)
    //   - 1 tag selected and a cross-instance target picked
    //     (cross-instance flow — same-name match on the other instance)
    compareCanRun() {
      if (this.compareLoading) return false;
      if (this.compareCrossInstanceTarget) return this.tagsSelected.size === 1;
      return this.tagsSelected.size === 2;
    },

    compareDisabledTooltip() {
      if (this.compareLoading) return 'Comparing…';
      if (this.compareCrossInstanceTarget) {
        if (this.tagsSelected.size === 0) return 'Pick the tag you want to compare across instances';
        if (this.tagsSelected.size > 1)   return 'Cross-instance compare uses one tag — uncheck the extras';
        return 'Compare this tag against the same name on the other instance';
      }
      if (this.tagsSelected.size === 0) return 'Pick 2 tags to compare, or pick another instance to compare across';
      if (this.tagsSelected.size === 1) return 'Pick a 2nd tag, or pick another instance to compare across';
      if (this.tagsSelected.size > 2)   return 'Same-instance compare uses exactly 2 tags — uncheck the extras';
      return 'Compare the two selected tags';
    },

    // Compare two tags — fetch items for both via tag-items, compute set
    // differences in-browser. The endpoint already supports multi-id query
    // (?ids=A,B) so this is one round-trip. Sorted by title for stable
    // diffs across runs (parity testing leans on this — a moved item would
    // otherwise look like a regression even when the set is identical).
    //
    // The two tag IDs come from the toolbar's `tagsSelected` Set in the
    // order JavaScript chose to iterate — Sets preserve insertion order,
    // so A is whichever the user clicked first. Acceptable: which is "A"
    // vs "B" doesn't change the math, only the column labels.
    async runCompare(idA, idB) {
      if (!idA || !idB || idA === idB) return;
      // Snapshot the inputs so a late-arriving response doesn't overwrite
      // state that the user has since cleared (closeCompare, app-type
      // switch, or instance switch). Same pattern as loadTagSearchInventory.
      const instSnap = this.tagsInstanceId;
      const idASnap = idA, idBSnap = idB;
      const stale = () => this.tagsInstanceId !== instSnap
                       || !this.compareOpen
                       || !this.tagsSelected.has(idASnap)
                       || !this.tagsSelected.has(idBSnap);
      this.compareLoading = true;
      this.compareError = '';
      this.compareResults = null;
      try {
        const r = await this.apiFetch(`/api/instances/${instSnap}/tag-items?ids=${idA},${idB}`);
        if (stale()) return;
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.compareError = msg || ('HTTP ' + r.status);
          return;
        }
        const data = await r.json();
        const a = (data || []).find(g => g.tagId === idA) || { label: '', items: [] };
        const b = (data || []).find(g => g.tagId === idB) || { label: '', items: [] };
        const aIds = new Set(a.items.map(it => it.id));
        const bIds = new Set(b.items.map(it => it.id));
        const both = a.items.filter(it => bIds.has(it.id));
        const onlyA = a.items.filter(it => !bIds.has(it.id));
        const onlyB = b.items.filter(it => !aIds.has(it.id));
        if (stale()) return;
        const sortFn = (x, y) => x.title.localeCompare(y.title);
        const instName = (this.instances.find(i => i.id === instSnap) || {}).name || '';
        this.compareResults = {
          a: { label: a.label, instanceName: instName, items: [...a.items].sort(sortFn) },
          b: { label: b.label, instanceName: instName, items: [...b.items].sort(sortFn) },
          both: both.sort(sortFn),
          onlyA: onlyA.sort(sortFn),
          onlyB: onlyB.sort(sortFn),
          crossInstance: false,
          joinKey: 'id',
        };
        // Auto-expand the categories with content so the user sees something
        // immediately instead of having to click each card.
        this.compareExpanded = {
          both: both.length > 0,
          onlyA: onlyA.length > 0,
          onlyB: onlyB.length > 0,
        };
      } catch (e) {
        if (!stale()) this.compareError = e.message || 'Compare failed';
      } finally {
        if (this.tagsInstanceId === instSnap) this.compareLoading = false;
      }
    },

    // Toggle the drill-down expander on a tag row. First expand triggers a
    // lazy fetch via /api/instances/{id}/tag-items?ids=<tagId>; collapse
    // keeps the cached items so a re-expand is instant.
    async toggleTagExpanded(tagID) {
      const next = { ...this.tagExpanded };
      if (next[tagID]) {
        delete next[tagID];
        this.tagExpanded = next;
        return;
      }
      next[tagID] = true;
      this.tagExpanded = next;

      // Already cached? Skip the fetch.
      if (this.tagItems[tagID]) return;

      this.tagItemsLoading = { ...this.tagItemsLoading, [tagID]: true };
      this.tagItemsError = { ...this.tagItemsError, [tagID]: '' };
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tag-items?ids=${tagID}`);
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.tagItemsError = { ...this.tagItemsError, [tagID]: msg || ('HTTP ' + r.status) };
          return;
        }
        const data = await r.json();
        // Endpoint returns [{tagId, label, items}] — flatten to the matched group.
        const group = (data || []).find(g => g.tagId === tagID);
        this.tagItems = { ...this.tagItems, [tagID]: (group && group.items) || [] };
      } catch (e) {
        this.tagItemsError = { ...this.tagItemsError, [tagID]: e.message || 'unknown' };
      } finally {
        this.tagItemsLoading = { ...this.tagItemsLoading, [tagID]: false };
      }
    },

    toggleOneTag(id, checked) {
      const next = new Set(this.tagsSelected);
      if (checked) next.add(id); else next.delete(id);
      this.tagsSelected = next;
      // Selection changed — close any open compare so results never lag
      // behind the selection. User re-clicks Compare to re-run with the
      // new pair.
      this.closeCompare();
    },

    toggleAllTags(checked) {
      this.tagsSelected = checked ? new Set(this.tags.map(t => t.id)) : new Set();
      this.closeCompare();
    },

    // Tags that are safe to delete without manual triage —
    // 0 movies/series attached AND no non-item references
    // (Lists, Custom Formats, Notifications, Indexers, etc.)
    // Reuses `nonItemUsage` keyed off the API response for
    // each tag, same data the delete-confirm pre-check uses.
    _isUnusedSafelyDeletable(t) {
      if (!t) return false;
      if ((t.usageCount || 0) > 0) return false;
      const u = t.nonItemUsage || {};
      return Object.keys(u).length === 0;
    },

    unusedSafelyDeletableCount() {
      return this.tags.reduce((n, t) => n + (this._isUnusedSafelyDeletable(t) ? 1 : 0), 0);
    },

    // Bulk-select every tag that meets the both-criteria gate
    // above. Replaces the current selection rather than merging
    // into it — the button's clear intent is "give me a clean
    // selection of safe-to-delete orphans". Closes any open
    // compare panel since selection changed.
    selectUnusedTags() {
      const next = new Set();
      for (const t of this.tags) {
        if (this._isUnusedSafelyDeletable(t)) next.add(t.id);
      }
      this.tagsSelected = next;
      this.closeCompare();
    },

    // Compare panel toggle. Dispatches to one of two flows based on the
    // selection + cross-instance picker state. Re-clicking when the panel
    // is already open closes it (toggle behaviour).
    async toggleCompare() {
      if (this.compareOpen) {
        this.closeCompare();
        return;
      }
      if (!this.compareCanRun()) return;
      this.compareOpen = true;
      if (this.compareCrossInstanceTarget) {
        const [tagId] = [...this.tagsSelected];
        await this.runCrossInstanceCompare(tagId, this.compareCrossInstanceTarget);
      } else {
        const [idA, idB] = [...this.tagsSelected];
        await this.runCompare(idA, idB);
      }
    },

    closeCompare() {
      this.compareOpen = false;
      this.compareResults = null;
      this.compareError = '';
      this.compareExpanded = { both: false, onlyA: false, onlyB: false };
    },

    // Cross-instance compare. Resolves the same tag NAME on the target
    // instance, fetches both sides' tag-items, and joins on tmdbId for
    // Radarr or tvdbId for Sonarr (instance-local item IDs are useless
    // across instances). Items missing the join key are excluded from
    // both/onlyA/onlyB and reported in `unjoinable` so users know why a
    // count looks low. Same-name match is the v1 scope; comparing across
    // different tag NAMES is a follow-up.
    async runCrossInstanceCompare(tagAId, instanceBId) {
      // Snapshot the inputs — three sequential awaits below means a wide
      // race window. If the user closes the panel, switches instance,
      // changes the cross-instance target, or clears the tag selection
      // mid-flight, this run's results are discarded on the floor.
      const instSnap = this.tagsInstanceId;
      const targetSnap = instanceBId;
      const tagSnap = tagAId;
      const stale = () => this.tagsInstanceId !== instSnap
                       || this.compareCrossInstanceTarget !== targetSnap
                       || !this.compareOpen
                       || !this.tagsSelected.has(tagSnap);
      this.compareLoading = true;
      this.compareError = '';
      this.compareResults = null;
      try {
        const instA = this.instances.find(i => i.id === instSnap);
        const instB = this.instances.find(i => i.id === instanceBId);
        if (!instA || !instB || instA.id === instB.id) {
          if (!stale()) this.compareError = 'Instance not found';
          return;
        }
        if (instA.type !== instB.type) {
          if (!stale()) this.compareError = 'Cross-Arr-type compare (Radarr vs Sonarr) is not supported — different content types';
          return;
        }
        const joinKey = instA.type === 'sonarr' ? 'tvdbId' : 'tmdbId';

        // Side A — fetch the chosen tag's items on the current instance.
        const tagA = this.tags.find(t => t.id === tagSnap);
        if (!tagA) { if (!stale()) this.compareError = 'Selected tag not found on this instance'; return; }
        // Side A and B's tag-list can run in parallel — neither depends
        // on the other. Side B's tag-items still has to wait for B's
        // tags to identify the matching tag id.
        const [aResp, tagsResp] = await Promise.all([
          this.apiFetch(`/api/instances/${instSnap}/tag-items?ids=${tagSnap}`),
          this.apiFetch(`/api/instances/${targetSnap}/tags`),
        ]);
        if (stale()) return;
        if (!aResp.ok)    { this.compareError = 'Failed to load tag on side A: HTTP ' + aResp.status; return; }
        if (!tagsResp.ok) { this.compareError = 'Failed to load tags on side B: HTTP ' + tagsResp.status; return; }
        const aData = await aResp.json();
        const aGroup = (aData || []).find(g => g.tagId === tagSnap) || { label: tagA.label, items: [] };
        const bTags = await tagsResp.json();
        const tagB = (bTags || []).find(t => t.label.toLowerCase() === tagA.label.toLowerCase());
        let bGroup = { label: tagA.label, items: [] };
        if (tagB) {
          const bResp = await this.apiFetch(`/api/instances/${targetSnap}/tag-items?ids=${tagB.id}`);
          if (stale()) return;
          if (!bResp.ok) { this.compareError = 'Failed to load tag on side B: HTTP ' + bResp.status; return; }
          const bData = await bResp.json();
          bGroup = (bData || []).find(g => g.tagId === tagB.id) || bGroup;
        }

        // Join by tmdbId/tvdbId. Items missing the key are unjoinable —
        // reported separately so users can see why counts may be lower
        // than expected (rare, but happens with stub-only entries).
        const aById = new Map();
        const bById = new Map();
        let aUnjoinable = 0, bUnjoinable = 0;
        for (const it of aGroup.items) {
          const k = it[joinKey];
          if (!k) { aUnjoinable++; continue; }
          aById.set(k, it);
        }
        for (const it of bGroup.items) {
          const k = it[joinKey];
          if (!k) { bUnjoinable++; continue; }
          bById.set(k, it);
        }
        const both = [], onlyA = [], onlyB = [];
        for (const [k, aIt] of aById) {
          if (bById.has(k)) {
            const bIt = bById.get(k);
            // Use side-A item as the row identity; carry side-B reference
            // so the result UI can render both file contexts if useful.
            both.push({ ...aIt, _b: bIt, _joinKey: k });
          } else {
            onlyA.push({ ...aIt, _joinKey: k });
          }
        }
        for (const [k, bIt] of bById) {
          if (!aById.has(k)) onlyB.push({ ...bIt, _joinKey: k });
        }
        if (stale()) return;
        const sortFn = (x, y) => x.title.localeCompare(y.title);
        this.compareResults = {
          a: { label: aGroup.label, instanceName: instA.name, items: [...aGroup.items].sort(sortFn) },
          b: { label: bGroup.label, instanceName: instB.name, items: [...bGroup.items].sort(sortFn) },
          both: both.sort(sortFn),
          onlyA: onlyA.sort(sortFn),
          onlyB: onlyB.sort(sortFn),
          crossInstance: true,
          joinKey,
          tagBExists: !!tagB,
          aUnjoinable, bUnjoinable,
        };
        this.compareExpanded = {
          both: both.length > 0,
          onlyA: onlyA.length > 0,
          onlyB: onlyB.length > 0,
        };
      } catch (e) {
        if (!stale()) this.compareError = e.message || 'Cross-instance compare failed';
      } finally {
        if (this.tagsInstanceId === instSnap && this.compareCrossInstanceTarget === targetSnap) {
          this.compareLoading = false;
        }
      }
    },

    // ===== Tag label validation (per-app-type) =====
    //
    // Source of truth for what each Arr's POST /api/v3/tag will accept:
    //
    // RADARR (TagController.cs in Radarr/Radarr):
    //   .Matches("^[a-z0-9-]+$", RegexOptions.IgnoreCase)
    //   .WithMessage("Allowed characters a-z, 0-9 and -")
    // Then TagService.Add lowercases the label via ToLowerInvariant before
    // insert. So uppercase is accepted by the validator but stored as
    // lowercase. Periods, colons, underscores, spaces, unicode, emoji
    // → 400. No length cap in source.
    //
    // SONARR (TagController.cs in Sonarr/Sonarr):
    //   No validator at all. Accepts anything non-empty. TagService.Add
    //   also lowercases via ToLowerInvariant before insert. Spaces,
    //   periods, unicode, emoji all accepted; just stored lowercase.
    //
    // Implication for our preview: always lowercase before send (matches
    // what the server will store + sidesteps a case-sensitive
    // FindByLabel dedup race in both Arrs). Block Radarr-illegal chars
    // up-front so the user sees the reason, not just a 400.

    tagLabelNormalize(s) {
      return (s || '').trim().toLowerCase();
    },

    // Live keystroke sanitiser bound to the rename inputs.
    //   Radarr: validator is `^[a-z0-9-]+$` IgnoreCase + server-side
    //     ToLowerInvariant. We strip anything outside that set as the
    //     user types — uppercase, spaces, periods, colons, underscores,
    //     unicode, emoji all refuse to land in the input.
    //   Sonarr: validator accepts any non-empty string but TagService
    //     still lowercases via ToLowerInvariant on insert. We lowercase
    //     keystrokes too so the input shows what will actually be
    //     stored — spaces, periods, accented chars all pass through.
    // Both rules give a WYSIWYG preview without trailing "will be
    // saved as X" warnings.
    sanitizeTagInput(value, appType) {
      const v = (value || '').toLowerCase();
      if (appType === 'radarr') {
        return v.replace(/[^a-z0-9-]/g, '');
      }
      return v;
    },

    // Returns { valid, reason, message, normalized } where reason is one
    // of '' | 'empty' | 'radarr-chars' | 'too-long'. message is a
    // user-facing string the modals display next to the input.
    tagLabelValidate(s, appType) {
      const normalized = this.tagLabelNormalize(s);
      if (!normalized) {
        return { valid: false, reason: 'empty', message: 'Tag name cannot be empty.', normalized };
      }
      // Defensive cap — neither Arr enforces one in source code, but
      // SQLite's TEXT column has practical limits and a 200-char tag
      // is not a real use case. Higher than the longest released
      // group (~30) plus margin for prefixes / decorators.
      if (normalized.length > 200) {
        return { valid: false, reason: 'too-long', message: 'Tag name is too long (200-char cap).', normalized };
      }
      if (appType === 'radarr') {
        if (!/^[a-z0-9-]+$/.test(normalized)) {
          // Pull out the offending characters so the message is concrete.
          const bad = [...new Set(normalized.replace(/[a-z0-9-]/g, '').split(''))]
            .map(c => c === ' ' ? '"space"' : '"' + c + '"')
            .join(', ');
          return {
            valid: false,
            reason: 'radarr-chars',
            message: 'Radarr only accepts a-z, 0-9 and hyphens. Remove: ' + bad + '.',
            normalized,
          };
        }
      }
      // Sonarr — anything non-empty after lowercasing.
      return { valid: true, reason: '', message: '', normalized };
    },

    // Single-rename helpers — return validation state for the current
    // input + active instance. The modal binds these for the input
    // hint, preview row, and submit-button gate.
    renameValidation() {
      return this.tagLabelValidate(this.renameNewLabel, this.currentInstanceType());
    },
    renameNormalized() {
      return this.renameValidation().normalized;
    },

    openRenameTag(t) {
      this.renameTarget = { id: t.id, label: t.label, usageCount: t.usageCount };
      this.renameNewLabel = t.label;
      this.renameKeepOldDefinition = false;
      this.renameError = '';
      this.renameBusy = false;
      this.renamePreview = [];
      this.showRenameModal = true;
      this.loadRenamePreview();
    },

    async loadRenamePreview() {
      this.renamePreviewLoading = true;
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tag-items?ids=${this.renameTarget.id}`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.renamePreview = (d && d[0] && d[0].items) ? d[0].items : [];
      } catch (e) {
        this.renamePreview = [];
        this.renameError = 'Preview failed: ' + e.message;
      } finally {
        this.renamePreviewLoading = false;
      }
    },

    // Returns the existing tag that the new label would merge into, or null.
    // Comparison uses the normalized (trim+lowercase) form because that's
    // what the server will store — "MyTag" submitted against existing
    // "mytag" is a merge, not a fresh rename.
    renameMergeTarget() {
      const newLabel = this.renameNormalized();
      if (!newLabel || newLabel === this.renameTarget.label.toLowerCase()) return null;
      return this.tags.find(t => t.label.toLowerCase() === newLabel && t.id !== this.renameTarget.id) || null;
    },

    async submitRename() {
      const v = this.renameValidation();
      if (!v.valid) return;
      // Send the normalized form. Both Arrs lowercase server-side before
      // insert; sending pre-lowercased avoids a case-sensitive
      // FindByLabel dedup race where "MyTag" submitted against existing
      // DB row "mytag" hits an insert + UNIQUE-constraint failure
      // instead of finding the merge candidate.
      const newLabel = v.normalized;
      if (newLabel === this.renameTarget.label.toLowerCase()) {
        this.showRenameModal = false;
        return;
      }
      this.renameBusy = true;
      this.renameError = '';
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags/rename`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            oldId: this.renameTarget.id,
            newLabel,
            keepOldDefinition: this.renameKeepOldDefinition,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showRenameModal = false;
        const label = this.itemLabel(d.movedCount || 0);
        const verb = d.merged ? 'Merged into' : 'Renamed to';
        this.showToast(`${verb} "${newLabel}" (${d.movedCount || 0} ${label} moved)`, 'success');
        await this.loadTags();
      } catch (e) {
        this.renameError = e.message;
      } finally {
        this.renameBusy = false;
      }
    },

    openBatchRename() {
      if (this.tagsSelected.size < 2) return;
      this.batchRenameTargets = this.tags
        .filter(t => this.tagsSelected.has(t.id))
        .map(t => ({ id: t.id, label: t.label, usageCount: t.usageCount }));
      this.batchRenameMode = 'suffix';
      this.batchRenamePrefix = '';
      this.batchRenameSuffix = '';
      this.batchRenameFind = '';
      this.batchRenameReplace = '';
      this.batchRenameKeepOldDefinition = false;
      this.batchRenameBusy = false;
      this.batchRenameError = '';
      this.batchRenameProgress = '';
      this.showBatchRenameModal = true;
    },

    // Apply the active template to one label and return the resulting label.
    // Empty inputs collapse to no-op (returns the original label unchanged).
    batchRenameApplyTemplate(label) {
      if (this.batchRenameMode === 'prefix') {
        const p = this.batchRenamePrefix.trim();
        return p ? p + label : label;
      }
      if (this.batchRenameMode === 'suffix') {
        const s = this.batchRenameSuffix.trim();
        return s ? label + s : label;
      }
      if (this.batchRenameMode === 'replace') {
        const f = this.batchRenameFind;
        if (!f) return label;
        // Plain substring replace-all (no regex) — escape for safety
        const escaped = f.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        return label.replace(new RegExp(escaped, 'g'), this.batchRenameReplace);
      }
      return label;
    },

    // Live preview rows — one per selected tag, with the resulting label and
    // a status that drives the UI (chip + apply-disabled gate). Statuses:
    //   ok                — clean rename, no collision
    //   merge-existing    — new label matches an existing tag NOT in selection (will merge via single-rename handler)
    //   merge-batch       — two selected tags resolve to the same new label (would clobber each other)
    //   unchanged         — template produced the same label (skipped on apply)
    //   invalid           — new label fails the [a-z0-9-]+ rule or is empty
    batchRenamePreview() {
      const rows = [];
      const appType = this.currentInstanceType();
      const selectedIds = new Set(this.batchRenameTargets.map(t => t.id));
      // Tally new labels within the batch to detect dupes. Key by the
      // normalized (lowercase) form because the server will lowercase
      // on write — "Foo" + "foo" produced by the template both end up
      // as "foo" and would collide.
      const newLabelCounts = new Map();
      for (const t of this.batchRenameTargets) {
        const raw = this.batchRenameApplyTemplate(t.label);
        const v = this.tagLabelValidate(raw, appType);
        if (v.valid) {
          newLabelCounts.set(v.normalized, (newLabelCounts.get(v.normalized) || 0) + 1);
        }
      }
      for (const t of this.batchRenameTargets) {
        const raw = this.batchRenameApplyTemplate(t.label);
        const v = this.tagLabelValidate(raw, appType);
        // Display column shows the normalized form (what server
        // stores) — keeps preview honest. Falls back to the raw
        // input for empty/invalid cases so the user sees what
        // they actually typed alongside the error chip.
        const newLabel = v.valid ? v.normalized : raw;
        let status = 'ok';
        let mergeTarget = null;
        let invalidMessage = '';
        if (!v.valid) {
          status = 'invalid';
          invalidMessage = v.message;
        } else if (v.normalized === t.label.toLowerCase()) {
          status = 'unchanged';
        } else if (newLabelCounts.get(v.normalized) > 1) {
          status = 'merge-batch';
        } else {
          // Look for existing tag (outside the selection) that matches.
          const existing = this.tags.find(
            x => x.label.toLowerCase() === v.normalized && !selectedIds.has(x.id)
          );
          if (existing) {
            status = 'merge-existing';
            mergeTarget = existing;
          }
        }
        rows.push({ id: t.id, oldLabel: t.label, newLabel, status, mergeTarget, invalidMessage });
      }
      return rows;
    },

    batchRenameApplyDisabled() {
      const rows = this.batchRenamePreview();
      // Block on any hard error. Unchanged rows are silently skipped on apply.
      if (rows.some(r => r.status === 'invalid' || r.status === 'merge-batch')) return true;
      // Need at least one row that would actually change.
      if (!rows.some(r => r.status === 'ok' || r.status === 'merge-existing')) return true;
      return false;
    },

    batchRenameStatusChip(status) {
      switch (status) {
        case 'ok': return { text: 'Rename', color: 'var(--accent-green)', bg: '#0e3318' };
        case 'merge-existing': return { text: 'Merge into existing', color: 'var(--accent-orange)', bg: '#2a2414' };
        case 'merge-batch': return { text: 'Conflicts within batch', color: 'var(--accent-red)', bg: '#3d0e0a' };
        case 'unchanged': return { text: 'No change — skipped', color: 'var(--text-secondary)', bg: 'var(--bg-card)' };
        case 'invalid': return { text: 'Invalid name', color: 'var(--accent-red)', bg: '#3d0e0a' };
        default: return { text: status, color: 'var(--text-secondary)', bg: 'var(--bg-card)' };
      }
    },

    async submitBatchRename() {
      if (this.batchRenameApplyDisabled()) return;
      const rows = this.batchRenamePreview().filter(r => r.status === 'ok' || r.status === 'merge-existing');
      this.batchRenameBusy = true;
      this.batchRenameError = '';
      let renamed = 0;
      let merged = 0;
      let movedTotal = 0;
      const failures = [];
      for (let i = 0; i < rows.length; i++) {
        const row = rows[i];
        this.batchRenameProgress = `${i + 1} of ${rows.length} — ${row.oldLabel} → ${row.newLabel}`;
        try {
          const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags/rename`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              oldId: row.id,
              newLabel: row.newLabel,
              keepOldDefinition: this.batchRenameKeepOldDefinition,
            }),
          });
          const d = await r.json();
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          if (d.merged) merged++; else renamed++;
          movedTotal += d.movedCount || 0;
        } catch (e) {
          failures.push(`${row.oldLabel}: ${e.message}`);
        }
      }
      this.batchRenameBusy = false;
      this.batchRenameProgress = '';
      if (failures.length > 0) {
        this.batchRenameError = `${failures.length} of ${rows.length} renames failed:\n` + failures.join('\n');
        // Refresh anyway so partial success is reflected.
        await this.loadTags();
        return;
      }
      this.showBatchRenameModal = false;
      const parts = [];
      if (renamed > 0) parts.push(`${renamed} renamed`);
      if (merged > 0) parts.push(`${merged} merged`);
      const itemWord = this.itemLabel(movedTotal);
      this.showToast(`${parts.join(' · ')} (${movedTotal} ${itemWord} moved)`, 'success');
      this.tagsSelected = new Set();
      await this.loadTags();
    },

    openDeleteTag(t) {
      this.deleteTargets = [{ id: t.id, label: t.label, usageCount: t.usageCount, nonItemUsage: t.nonItemUsage || {} }];
      this.deleteKeepDefinition = false;
      this.deleteError = '';
      this.deleteProgress = '';
      this.deleteBusy = false;
      this.deletePreviewGroups = [];
      this.showDeleteModal = true;
      this.loadDeletePreview();
    },

    // deleteBlockedTargets returns the subset of deleteTargets that
    // have non-item references (Lists, Custom Formats, Notifications,
    // etc.). Radarr/Sonarr refuse to delete those tags — surfacing
    // them in the modal lets the user fix before clicking Delete and
    // getting a long cryptic API error.
    deleteBlockedTargets() {
      return (this.deleteTargets || []).filter(t => {
        const u = t.nonItemUsage || {};
        return Object.keys(u).length > 0;
      });
    },
    // Pretty label for the modal: "2 Lists, 1 Custom Format" etc.
    deleteBlockedSummary(target) {
      const u = (target && target.nonItemUsage) || {};
      const parts = [];
      for (const k of Object.keys(u)) {
        parts.push(u[k] + ' ' + k);
      }
      return parts.join(', ');
    },

    openBulkDelete() {
      if (this.tagsSelected.size === 0) return;
      this.deleteTargets = this.tags
        .filter(t => this.tagsSelected.has(t.id))
        .map(t => ({ id: t.id, label: t.label, usageCount: t.usageCount, nonItemUsage: t.nonItemUsage || {} }));
      this.deleteKeepDefinition = false;
      this.deleteError = '';
      this.deleteProgress = '';
      this.deleteBusy = false;
      this.deletePreviewGroups = [];
      this.showDeleteModal = true;
      this.loadDeletePreview();
    },

    async loadDeletePreview() {
      this.deletePreviewLoading = true;
      try {
        const ids = this.deleteTargets.map(t => t.id).join(',');
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tag-items?ids=${ids}`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.deletePreviewGroups = d || [];
      } catch (e) {
        this.deletePreviewGroups = [];
        this.deleteError = 'Preview failed: ' + e.message;
      } finally {
        this.deletePreviewLoading = false;
      }
    },

    // Flatten preview groups to rows: [{tagId, tagLabel, itemId, title}]
    deletePreviewRows() {
      const rows = [];
      for (const g of this.deletePreviewGroups) {
        for (const it of (g.items || [])) {
          rows.push({ tagId: g.tagId, tagLabel: g.label, itemId: it.id, title: it.title });
        }
      }
      return rows;
    },

    async submitDelete() {
      this.deleteBusy = true;
      this.deleteError = '';
      const keep = this.deleteKeepDefinition ? '?keepDefinition=true' : '';
      let done = 0, failed = 0, itemsRemoved = 0;
      for (const t of this.deleteTargets) {
        this.deleteProgress = `Deleting ${t.label}… (${done + failed + 1}/${this.deleteTargets.length})`;
        try {
          const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags/${t.id}${keep}`, { method: 'DELETE' });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          done++;
          itemsRemoved += d.removedFrom || 0;
        } catch (e) {
          failed++;
          this.deleteError = `${t.label}: ${e.message}`;
        }
      }
      this.deleteBusy = false;
      this.deleteProgress = '';
      if (failed === 0) {
        this.showDeleteModal = false;
        const action = this.deleteKeepDefinition ? 'Cleared' : 'Deleted';
        const itemWord = this.itemLabel(itemsRemoved);
        this.showToast(`${action} ${done} tag${done === 1 ? '' : 's'} (${itemsRemoved} ${itemWord} affected)`, 'success');
      }
      await this.loadTags();
    },

    // --- Schedules (M3d) ---

    // Loads /api/schedules into this.schedules. Called on Scan tab init
    // and from the Reload button. Soft-fail: error stays in
    // schedulesError so the table reads as empty without trapping the
    // user.
    async loadSchedules() {
      this.schedulesLoading = true;
      this.schedulesError = '';
      try {
        const r = await this.apiFetch('/api/schedules');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.schedules = (await r.json()) || [];
        this.seedLastSeenRuns();
      } catch (e) {
        this.schedules = [];
        this.schedulesError = 'Load failed: ' + (e.message || 'unknown');
      } finally {
        this.schedulesLoading = false;
      }
    },

    // After a fresh load (initial or manual Reload), seed the last-seen
    // map from current state. Without this, the first poll after a
    // page open would treat every existing history entry as "new" and
    // spam toasts. Only entries newer than what's already in the map
    // are flagged on subsequent polls.
    seedLastSeenRuns() {
      for (const sj of this.schedules) {
        const last = (sj.history || [])[(sj.history || []).length - 1];
        if (last) this.lastSeenScheduleRuns[sj.id] = last.startedAt;
      }
    },

    // Called from the background poll (every ~30s while Scan -> Run is
    // visible) and at any other moment we want to repaint without
    // showing a "loading" spinner. Same fetch path as loadSchedules
    // but quiet: no error toast, no schedulesLoading flag, just a
    // diff-and-toast pass.
    async pollSchedulesForFires() {
      try {
        const r = await this.apiFetch('/api/schedules');
        if (!r.ok) return;
        const fresh = (await r.json()) || [];
        // For every schedule, see if its latest history entry is
        // newer than what we last saw. Toast + remember the new
        // startedAt so the same fire isn't announced twice.
        for (const sj of fresh) {
          const last = (sj.history || [])[(sj.history || []).length - 1];
          if (!last) continue;
          const prev = this.lastSeenScheduleRuns[sj.id];
          if (prev !== last.startedAt) {
            // Skip the very first observation per page-load (handled
            // by seedLastSeenRuns) — only fires NEW since last poll
            // get a toast.
            if (prev !== undefined) {
              const tone = last.status === 'ok' ? 'success'
                         : last.status === 'partial' ? 'error'
                         : last.status === 'error' ? 'error'
                         : 'success';
              const summary = last.summary ? ' — ' + last.summary : '';
              this.showToast(`Schedule "${sj.name}" finished${summary}`, tone);
            }
            this.lastSeenScheduleRuns[sj.id] = last.startedAt;
          }
        }
        this.schedules = fresh;
      } catch {
        // Network blip — try again next tick. No user-facing surface.
      }
    },

    // Starts the 30s poll while the Run sub-tab is visible. Idempotent
    // — safe to call multiple times; existing handle is reused.
    startSchedulePoll() {
      if (this.schedulePollHandle) return;
      this.schedulePollHandle = setInterval(() => this.pollSchedulesForFires(), 30000);
    },

    stopSchedulePoll() {
      if (this.schedulePollHandle) {
        clearInterval(this.schedulePollHandle);
        this.schedulePollHandle = null;
      }
    },


    // ---- Rule editor (self-contained schedule rules) ----
    //
    // Single wizard modal covering three flows: Create (saved
    // schedule rule), Edit (tabbed-edit of an existing rule), and
    // Quickfix (one-shot dispatcher that runs the chain without
    // persisting). All three share the same editingRule shape and
    // section components — flows differ only in which steps render
    // and whether Save persists a rule (Create/Edit) or fires a chain
    // via runQuickFixChain (Quickfix).

    // Default snapshots used when a brand-new rule is being created or
    // when an older row is missing a snapshot field. PascalCase mirrors
    // engine.FilterConfig's wire shape (no JSON tags on the Go side).
    defaultRuleFilters() {
      return {
        Quality: true, MAWebDL: true, PlayWebDL: true,
        Audio: true, TrueHD: true, TrueHDAtmos: true,
        DTSX: true, DTSHDMA: true,
      };
    },
    defaultRuleAudioTags() {
      return {
        audio: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
        removeOrphanedTags: false,
      };
    },
    defaultRuleVideoTags() {
      return {
        resolution: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
        codec:      { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
        hdr:        { enabled: false, prefix: '', sonarrAggregation: 'strict',         allowedValues: [], selectMode: '' },
        removeOrphanedTags: false,
      };
    },
    defaultRuleDvDetail() {
      return { enabled: false, prefix: '', allowedValues: [], removeOrphanedTags: false };
    },
    // Snapshot the live global Filters block (Radarr-side — UI only
    // edits that side today) into the per-rule wire shape.
    snapshotGlobalFilters() {
      return {
        Quality: this.filters.quality, MAWebDL: this.filters.maWebDL, PlayWebDL: this.filters.playWebDL,
        Audio: this.filters.audio, TrueHD: this.filters.trueHD, TrueHDAtmos: this.filters.trueHDAtmos,
        DTSX: this.filters.dtsX, DTSHDMA: this.filters.dtsHDMA,
      };
    },
    // Per-snapshot helpers — each returns an independent deep-clone of
    // the matching global config so the rule editor can mutate freely.
    // removeOrphanedTags is FORCED off in the snapshot regardless of
    // the global setting: rules are user-explicit by definition (you
    // sit in the wizard and tick what you want), and a destructive
    // cleanup that silently inherits from a Library-scan tab toggle
    // most users don't remember setting is exactly the kind of
    // surprise the wizard flow is meant to prevent. The user can
    // still tick the orphan-cleanup checkbox on the audio/video/dv
    // step explicitly if they want it.
    snapshotGlobalAudioTags() {
      const snap = JSON.parse(JSON.stringify(this.audioTags));
      // Destructive opt-ins reset per-rule — globals never carry these
      // forward to a fresh rule snapshot. User opts in explicitly.
      snap.removeOrphanedTags = false;
      snap.stripOnFileDelete = false;
      return snap;
    },
    snapshotGlobalVideoTags() {
      const snap = JSON.parse(JSON.stringify(this.videoTags));
      snap.removeOrphanedTags = false;
      snap.stripOnFileDelete = false;
      return snap;
    },
    snapshotGlobalDvDetail() {
      const snap = JSON.parse(JSON.stringify(this.dvDetail));
      snap.removeOrphanedTags = false;
      snap.stripOnFileDelete = false;
      return snap;
    },
    // Per-rule snapshot of the global Missing-Episodes config. Used by
    // the QFA + Create-Rule wizards when missingepisodes is in
    // combinedModes. The wizard's actionTag / actionSearch live on the
    // snapshot itself so each rule (or QFA chain) picks its own
    // application semantics independently of the standalone tab's
    // last-used setting.
    snapshotGlobalMissingEpisodes() {
      const c = this.missingEpisodesConfig || {};
      return {
        thresholdPercent: c.thresholdPercent ?? 70,
        bufferHours: (c.bufferHours === undefined || c.bufferHours === null || c.bufferHours === '') ? 24 : Number(c.bufferHours),
        includeContinuing: c.includeContinuing !== false,
        includeEnded: c.includeEnded !== false,
        includeSpecials: !!c.includeSpecials,
        tagName: ((c.tagName || 'missing-episodes') + '').trim() || 'missing-episodes',
        // Default: Tag on, Search off. Tag is informative + auto-cleans;
        // Search fires Sonarr indexer calls, more aggressive.
        actionTag: true,
        actionSearch: false,
      };
    },
    // Blank Plex-sync snapshot for the wizard's plexsync step. Unlike
    // the auto-tag snapshots there is NO global Plex-sync config to
    // copy from (one-off / schedule / webhook / qfa each own their
    // inline config), so this is just an empty starting point the user
    // fills in on the Plex sync step.
    snapshotDefaultPlexSync() {
      return {
        plexInstanceId: '',
        libraryKeys: [],
        labels: [],
        labelDisplay: {},
        targetTypes: ['label'],
      };
    },
    // Blank TBA-refresh snapshot for the wizard's tbarefresh phase
    // (Sonarr-only). No global to copy from; sensible default is all
    // monitored series, specials off.
    snapshotDefaultTbaRefresh() {
      return {
        includeContinuing: true,
        includeEnded: true,
        includeSpecials: false,
      };
    },
    // Subset of cfg.ReleaseGroups[].id matching the rule's instance type
    // AND currently Enabled. Used to seed editingRule.releaseGroupIds
    // when the user opens the wizard — gives them a sensible default
    // ("everything I currently track") that they can prune in the RG
    // step.
    snapshotGlobalRGIds(instanceId) {
      const inst = this.instances.find(i => i.id === instanceId);
      if (!inst) return [];
      return this.groups.filter(g => g.type === inst.type && g.enabled).map(g => g.id);
    },

    // ---- Open / close ----
    openCreateRuleWizard() {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Lock the wizard to the Arr-type the user picked in the Library
      // scan header. The instance dropdown + mode catalog both filter
      // through this. If the user is on a tab without an app picker we
      // fall back to whatever scanAppType currently is.
      const wizardAppType = this.scanAppType || 'radarr';
      const poolForType = this.instances.filter(i => i.type === wizardAppType);
      if (poolForType.length === 0) {
        this.showToast('No ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') + ' instance configured — add one in Settings → Instances', 'error');
        return;
      }
      // Seed precedence: last-used remembered (per-Arr-type key so
      // Radarr Create-rule and Sonarr Create-rule remember separately) →
      // current scanInstanceId → first-of-type.
      const memKey = 'create-rule-' + wizardAppType;
      const remembered = this.recallWizardInstance(memKey, poolForType);
      const scanPicked = poolForType.find(i => i.id === this.scanInstanceId);
      const inst = remembered || (scanPicked || poolForType[0]).id;
      // mode='combined' for every rule. Single-mode runs are expressed
      // by ticking one chain step (combinedModes carries the user's
      // pick). Sonarr-context seeds 'recover' since that's the only
      // currently-supported phase; Radarr starts with no boxes ticked
      // (user must pick at least one before Save).
      const seedCombined = wizardAppType === 'sonarr' ? ['recover'] : [];
      this.editingRule = {
        id: '', name: '', mode: 'combined', instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        manualOnly: false,
        enabled: true,
        options: {
          runMode: 'apply',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: seedCombined,
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          // Per-bucket instance target. 'primary' (default), 'secondary',
          // or 'both'. Audio/Video/DV-tags each pick independently. The
          // chain runs primary phases first (discover/recover/tag), then
          // a sub-chain on the primary instance with whichever of
          // audio/video/dv have target=primary or 'both', then a sub-chain
          // on the secondary instance with whichever have target=secondary
          // or 'both'. Token allow-lists are universal — same per-rule
          // settings get applied to whichever instance(s) run.
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      this.ruleEditor = { open: true, isCreate: true, isQuickFix: false, step: 0, activeTab: 'basics', appType: wizardAppType, busy: false, error: '', cronError: '', nextFires: [], fixedAction: '' };
      this.computeRuleEditorNextFires();
    },

    // Quick fix-all entry point. Same wizard, but flagged as one-shot:
    // no Name field, no cron picker, no enabled toggle, Save button
    // becomes "Run now" and dispatches the chain immediately without
    // persisting anything. Pre-fills from current globals (same seed
    // as Create) so the user lands on Review with sensible defaults
    // and can fire-fast by clicking through Next-Next-Next-RunNow.
    openQuickFixWizard() {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Quick fix-all locks to whichever Arr-type the user picked in
      // the Library scan header (scanAppType). Radarr seed includes
      // the full discover→recover→tag head chain; Sonarr seed picks
      // only the phases backend supports today (recover + audio +
      // video — tag/discover land with M-Sonarr Phase 2).
      const wizardAppType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      const poolForType = this.instances.filter(i => i.type === wizardAppType);
      if (poolForType.length === 0) {
        this.showToast(
          'Quick fix-all needs a ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') +
          ' instance — add one in Settings → Instances', 'error');
        return;
      }
      // QFA carries its own state in localStorage (resolvarr-qfa-state-<arrtype>)
      // — independent of globals, independent of per-action wizard
      // defaults. User's last-fired QFA configuration (chain checks,
      // bucket configs, sync targets, instance, run-mode) becomes the
      // pre-fill for next open. Falls back to globals on first open
      // (cleared cache, new install).
      const restored = this._loadQfaState(wizardAppType);
      // Validate restored instance is still in pool. If the user
      // deleted their Radarr and the localStorage points to a dead
      // ID, we'd seed a wizard that can't fire.
      let inst;
      if (restored && restored.instanceId && poolForType.some(i => i.id === restored.instanceId)) {
        inst = restored.instanceId;
      } else {
        const scanPicked = poolForType.find(i => i.id === this.scanInstanceId);
        inst = (scanPicked || poolForType[0]).id;
      }

      // Default to Preview for safety — the wizard is interactive
      // and one-shot, so a Preview-then-Apply flow is the right
      // pattern. The result panel's "Apply now" button promotes
      // a clean preview to apply without re-walking the wizard.
      const defaults = {
        id: '',
        name: 'Quick fix-all',
        mode: 'combined',
        instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        enabled: true,
        options: {
          runMode: 'preview',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: wizardAppType === 'sonarr' ? ['recover', 'audiotags', 'videotags', 'missingepisodes'] : ['discover', 'recover', 'tag'],
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          // Per-bucket instance target. 'primary' (default), 'secondary',
          // or 'both'. Audio/Video/DV-tags each pick independently.
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      // Merge restored state over defaults. Bucket snapshots use
      // recursive per-field merge so a localStorage payload written
      // before a new bucket field landed (e.g. SonarrAggregation in
      // M-Sonarr Phase A) doesn't leak undefined past the backend's
      // overlay validator on next Apply. Top-level fields use
      // shallow-merge — restored wins on every field that's
      // actually present.
      this.editingRule = restored
        ? {
            ...defaults,
            ...restored,
            instanceId: inst, // post-validation pick; restored may have a stale ID
            options: { ...defaults.options, ...(restored.options || {}) },
            filters:   this._mergeBucketSnapshot(restored.filters,   defaults.filters),
            audioTags: this._mergeBucketSnapshot(restored.audioTags, defaults.audioTags),
            videoTags: this._mergeBucketSnapshot(restored.videoTags, defaults.videoTags),
            dvDetail:  this._mergeBucketSnapshot(restored.dvDetail,  defaults.dvDetail),
            missingEpisodes: this._mergeBucketSnapshot(restored.missingEpisodes, defaults.missingEpisodes),
            plexSync: (restored.plexSync && typeof restored.plexSync === 'object') ? restored.plexSync : this.snapshotDefaultPlexSync(),
            tbaRefresh: (restored.tbaRefresh && typeof restored.tbaRefresh === 'object') ? restored.tbaRefresh : this.snapshotDefaultTbaRefresh(),
            releaseGroupIds: Array.isArray(restored.releaseGroupIds)
              ? restored.releaseGroupIds
              : defaults.releaseGroupIds,
          }
        : defaults;
      this.ruleEditor = { open: true, isCreate: true, isQuickFix: true, step: 0, activeTab: 'basics', appType: wizardAppType, busy: false, error: '', cronError: '', nextFires: [], fixedAction: '' };
    },

    // ===== QFA localStorage state =====
    //
    // QFA stores its full editingRule snapshot per Arr-type so the
    // wizard remembers the user's last chain configuration without
    // mutating globals (which per-action wizards own). Key shape:
    // 'resolvarr-qfa-state-radarr' / '-sonarr'. Per-Arr-type because
    // Radarr QFA and Sonarr QFA have different valid chain phases.

    _qfaStateKey(arrType) {
      return 'resolvarr-qfa-state-' + (arrType === 'sonarr' ? 'sonarr' : 'radarr');
    },
    _saveQfaState(arrType, rule) {
      if (!rule) return;
      try {
        // Strip volatile fields — `id` empty, `name` auto-generated.
        // Keep only what should re-hydrate next open. Deep-clone via
        // JSON.stringify-then-parse so a later mutation of rule.options
        // (e.g. an analytics post-run hook that mucks with combinedModes)
        // can't bleed into the persisted snapshot.
        const persist = {
          mode: rule.mode,
          instanceId: rule.instanceId,
          options: rule.options,
          filters: rule.filters,
          audioTags: rule.audioTags,
          videoTags: rule.videoTags,
          dvDetail: rule.dvDetail,
          missingEpisodes: rule.missingEpisodes,
          plexSync: rule.plexSync,
          tbaRefresh: rule.tbaRefresh,
          releaseGroupIds: rule.releaseGroupIds || [],
        };
        localStorage.setItem(this._qfaStateKey(arrType), JSON.stringify(persist));
      } catch (e) { /* ignore — Safari private mode et al */ }
    },

    // Per-field merge for QFA-state hydration. Old localStorage payloads
    // written before a new bucket field landed (e.g. SonarrAggregation
    // added in M-Sonarr Phase A) shouldn't leak `undefined` past the
    // backend's validator on next Apply. Recursively layer restored
    // values over defaults so missing fields fall back to fresh defaults.
    // ===== Per-action wizard: unified instance + "both" picker =====
    //
    // For Tag Audio / Video / DV Details runs, "primary" has no
    // semantic meaning — each instance reads its own mediaInfo
    // independently, there's no TmdbID-mirror like tag-release-groups
    // does. So the Step-1 "Primary instance" picker and the Step-2
    // "Run X on" target picker are redundant pairs encoding the same
    // intent twice.
    //
    // Per-action wizards collapse them into ONE dropdown on Step 1
    // listing instances by name + a "Both instances" option (when
    // 2+ same-type instances exist). State translation:
    //   value = '<inst-id>'  →  instanceId = <inst-id>, target = 'primary'
    //   value = '__both__'   →  instanceId = first-of-type, target = 'both'
    // The chain runner already handles target='both' by running on
    // primary (instanceId) + secondary (auto-derived first-other-of-type),
    // so keeping instanceId as a real ID + target='both' just works.

    _perActionTargetField() {
      const fa = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
      if (fa === 'audiotags') return 'audioTagsTarget';
      if (fa === 'videotags') return 'videoTagsTarget';
      if (fa === 'dvdetail')  return 'dvDetailTarget';
      if (fa === 'recover')   return 'recoverTarget';
      return '';
    },
    // True when the current per-action wizard supports the unified
    // primary/secondary/both picker. Auto-tag phases (audiotags /
    // videotags / dvdetail) walk per-instance media independently —
    // primary vs secondary has no semantic meaning. Recover joined
    // 2026-05-09 because each instance has its own movie/episode files
    // with their own missing-releaseGroup history; "primary only" was
    // an artificial restriction. Used to gate the unified-picker markup
    // vs the legacy primary-instance-only picker on Basics step.
    isPerActionAutoTag() {
      const fa = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
      return fa === 'audiotags' || fa === 'videotags' || fa === 'dvdetail' || fa === 'recover';
    },
    perActionInstanceSelectorValue() {
      if (!this.editingRule || !this.editingRule.options) return '';
      const tf = this._perActionTargetField();
      if (!tf) return this.editingRule.instanceId || '';
      if (this.editingRule.options[tf] === 'both') return '__both__';
      return this.editingRule.instanceId || '';
    },
    setPerActionInstance(value) {
      if (!this.editingRule || !this.editingRule.options) return;
      const tf = this._perActionTargetField();
      if (!tf) {
        this.editingRule.instanceId = value;
        return;
      }
      if (value === '__both__') {
        // Anchor instanceId to the first available instance of the
        // wizard's app type. The chain runner picks the secondary
        // automatically (first other-of-same-type).
        const pool = this.ruleEditorInstancesAvailable();
        if (pool.length > 0) this.editingRule.instanceId = pool[0].id;
        this.editingRule.options[tf] = 'both';
      } else {
        this.editingRule.instanceId = value;
        this.editingRule.options[tf] = 'primary';
      }
    },

    _mergeBucketSnapshot(restored, fallback) {
      if (!restored || typeof restored !== 'object') return fallback;
      if (!fallback || typeof fallback !== 'object') return restored;
      const out = { ...fallback };
      for (const k of Object.keys(restored)) {
        const rv = restored[k];
        const fv = fallback[k];
        if (rv && typeof rv === 'object' && !Array.isArray(rv) &&
            fv && typeof fv === 'object' && !Array.isArray(fv)) {
          out[k] = this._mergeBucketSnapshot(rv, fv);
        } else if (rv !== undefined) {
          out[k] = rv;
        }
      }
      return out;
    },
    _loadQfaState(arrType) {
      try {
        const s = localStorage.getItem(this._qfaStateKey(arrType));
        if (!s) return null;
        const parsed = JSON.parse(s);
        return (parsed && typeof parsed === 'object') ? parsed : null;
      } catch (e) { return null; }
    },

    // ===== Per-action standalone wizards =====
    //
    // openSpecificActionWizard reuses the QFA wizard infrastructure
    // but pre-seeds combinedModes to a single action and sets a
    // fixedAction flag so the Basics step's chain-checkboxes
    // section can hide (the user came here to run ONE thing — not
    // the whole "fix everything" chain). Wizard hydrates from
    // globals like QFA does, lets the user tweak per-run, fires
    // via runQuickFixChain on the Review step's Run button.
    //
    // Action keys: 'audiotags' | 'videotags' | 'dvdetail' | 'recover'.
    // Cleanup is handled by a separate small wizard (cleanup isn't
    // a combinedModes phase today).
    openSpecificActionWizard(action) {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Recover, Audio, Video work for both Arr types. DV detail is
      // Radarr-only today. Audio/Video on Sonarr work via the M-Sonarr
      // path. Discover/Tag remain Sonarr-deferred.
      const wizardAppType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      if (action === 'dvdetail' && wizardAppType !== 'radarr') {
        this.showToast('Tag DV Details is Radarr-only', 'error');
        return;
      }
      const pool = this.instances.filter(i => i.type === wizardAppType);
      // Seed precedence:
      //   1. last-used remembered instance for this action (if still in pool)
      //   2. current scanInstanceId (when matches type — pre-removal of
      //      header dropdown this was the legacy default; kept as a
      //      sensible fallback for users who just clicked from a tab)
      //   3. first-of-type
      const remembered = this.recallWizardInstance(action, pool);
      const seedInst = (remembered && pool.find(i => i.id === remembered))
                    || pool.find(i => i.id === this.scanInstanceId)
                    || pool[0];
      if (!seedInst) {
        this.showToast(
          'Need a ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') +
          ' instance — add one in Settings → Instances', 'error');
        return;
      }
      const inst = seedInst.id;
      const titleByAction = {
        audiotags:       'Tag Audio',
        videotags:       'Tag Video',
        dvdetail:        'Tag DV Details',
        recover:         'Run Recover',
        missingepisodes: 'Find missing episodes',
      };
      this.editingRule = {
        id: '',
        name: titleByAction[action] || 'Run',
        mode: 'combined',
        instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        enabled: true,
        options: {
          runMode: 'preview',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: [action],
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      this.ruleEditor = {
        open: true, isCreate: true, isQuickFix: true,
        step: 0, activeTab: 'basics', appType: wizardAppType,
        busy: false, error: '', cronError: '', nextFires: [],
        // fixedAction tells the Basics step to hide its chain-
        // checkboxes section — the user came here to run a single
        // action, not configure a chain. They can still hit Back
        // from later steps to tweak instance / run-mode.
        fixedAction: action,
      };
    },

    // Convenience entry points the sub-tab Run buttons call.
    openAudioWizard()   { this.openSpecificActionWizard('audiotags'); },
    openVideoWizard()   { this.openSpecificActionWizard('videotags'); },
    openDvWizard()      { this.openSpecificActionWizard('dvdetail'); },
    openRecoverWizard() { this.openSpecificActionWizard('recover'); },

    // ===== Per-wizard last-instance memory =====
    //
    // Each wizard remembers the instance the user picked on its
    // last successful run. Stored per-action so Tag Audio's last
    // pick doesn't bleed into Tag Video's. Bucket configs etc. are
    // intentionally NOT remembered — page-as-defaults handles
    // those, and rules handle "always identical settings" via
    // explicit save. Ad-hoc bucket tweaks are per-run by design.
    //
    // Storage key: 'resolvarr-wizard-instance-<action>'.
    // try/catch on every read+write — Safari private mode + similar
    // can block localStorage.
    _wizardInstanceKey(action) {
      return 'resolvarr-wizard-instance-' + action;
    },
    rememberWizardInstance(action, instanceId) {
      if (!action || !instanceId) return;
      try { localStorage.setItem(this._wizardInstanceKey(action), instanceId); }
      catch (e) { /* ignore — non-fatal */ }
    },
    // Returns the remembered instance ID iff it's still in pool +
    // matches the wizard's required type. Caller falls back to its
    // existing seed logic on null.
    recallWizardInstance(action, pool) {
      if (!action || !Array.isArray(pool) || pool.length === 0) return null;
      let stored = null;
      try { stored = localStorage.getItem(this._wizardInstanceKey(action)); }
      catch (e) { return null; }
      if (!stored) return null;
      return pool.some(i => i.id === stored) ? stored : null;
    },

    // ===== Cleanup unused tags wizard =====
    //
    // Cleanup isn't a combinedModes phase (it's a separate scan
    // action server-side), so it gets its own small wizard rather
    // than reusing the QFA flow. One-step: pick instance + Run.
    // The cleanup scan is preview-style — finds tags with 0 usage,
    // pops the result modal where the user picks which to delete.
    cleanupWizardState: {
      open: false,
      instanceId: '',
      busy: false,
    },
    openCleanupWizard() {
      const pool = (this.instances || []).filter(i => i.type === this.scanAppType);
      if (pool.length === 0) {
        const t = this.scanAppType === 'sonarr' ? 'Sonarr' : 'Radarr';
        this.showToast('Add a ' + t + ' instance in Settings → Instances first', 'error');
        return;
      }
      // Seed precedence: last-used remembered → current scanInstanceId
      // (when in pool) → first-of-type.
      const remembered = this.recallWizardInstance('cleanup', pool);
      const seedId = remembered
        || (pool.find(i => i.id === this.scanInstanceId) || pool[0]).id;
      this.cleanupWizardState = {
        open: true,
        instanceId: seedId,
        busy: false,
      };
    },
    closeCleanupWizard() {
      if (this.cleanupWizardState.busy) return;
      this.cleanupWizardState.open = false;
    },
    async runCleanupWizard() {
      if (!this.cleanupWizardState.instanceId) return;
      // Remember the picked instance for next time the wizard opens.
      this.rememberWizardInstance('cleanup', this.cleanupWizardState.instanceId);
      this.cleanupWizardState.busy = true;
      // Seed scanInstanceId so runCleanupCheck (which reads from
      // it) targets the wizard's pick. Auto-revert on close isn't
      // needed — when cleanup completes, scanInstanceId stays on
      // the user's last pick which is consistent with how Tag
      // and Audio runs already work.
      this.scanInstanceId = this.cleanupWizardState.instanceId;
      try {
        await this.runCleanupCheck();
      } finally {
        this.cleanupWizardState.busy = false;
        this.cleanupWizardState.open = false;
      }
    },
    cleanupWizardInstancesForType() {
      return (this.instances || [])
        .filter(i => i.type === this.scanAppType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // Translates an existing schedule row into the editingRule shape.
    // Cron is decomposed into preset/hour/minute via derivePresetFromCron
    // so the time-of-day pickers can render it back; non-standard cron
    // expressions land in 'custom' so the user can edit raw.
    openEditRuleModal(sj) {
      const copy = JSON.parse(JSON.stringify(sj));
      // Manual-only rules persist with cron="" — derive the UI flag
      // from that and substitute a sane default cron so the pickers
      // have something to show if the user toggles back to scheduled.
      copy.manualOnly = !copy.cron;
      if (copy.manualOnly) copy.cron = '0 3 * * *';
      const preset = derivePresetFromCron(copy.cron);
      const h24 = preset.hour ?? 3;
      copy.preset = preset.id;
      copy.hour = h24;
      copy.minute = preset.minute ?? 0;
      copy.hour12 = hour24To12(h24);
      copy.ampm = h24 >= 12 ? 'PM' : 'AM';
      copy.dow = preset.dow ?? 0;
      copy.dom = preset.dom ?? 1;
      // Backfill missing snapshots from globals — defence against any
      // pre-migration row that slipped through. Post-Step-1 every row
      // already has them populated.
      if (!copy.filters)         copy.filters         = this.snapshotGlobalFilters();
      if (!copy.audioTags)       copy.audioTags       = this.snapshotGlobalAudioTags();
      if (!copy.videoTags)       copy.videoTags       = this.snapshotGlobalVideoTags();
      if (!copy.dvDetail)        copy.dvDetail        = this.snapshotGlobalDvDetail();
      if (!copy.missingEpisodes) copy.missingEpisodes = this.snapshotGlobalMissingEpisodes();
      if (!copy.plexSync)        copy.plexSync        = this.snapshotDefaultPlexSync();
      if (!copy.tbaRefresh)      copy.tbaRefresh      = this.snapshotDefaultTbaRefresh();
      if (!copy.releaseGroupIds) copy.releaseGroupIds = this.snapshotGlobalRGIds(copy.instanceId);
      copy.options = Object.assign({
        runMode: 'apply', cleanupUnusedTags: false, syncToSecondary: false, syncToInstanceId: '',
        includeDiscovery: false, autoActivateDiscovered: false,
        discoverWriteBack: false, discoverScanSecondary: false,
        recoverIncludeSecondary: false, recoverIncludeSonarr: false, recoverSonarrSecondary: false,
        recoverTestItemId: 0, debugTrace: false, bypassDvCache: false,
        audioTagsTarget: 'primary', videoTagsTarget: 'primary', dvDetailTarget: 'primary',
        recoverTarget: 'primary',
        tagSource: '', filterOnlyTag: 'lossless-web',
      }, copy.options || {});
      // Migrate legacy autoTagsRunOnSecondary boolean → per-bucket
      // targets. true → audio + video both targets='both'; false →
      // 'primary'. DV target stays 'primary' (it was always
      // single-instance pre-migration). Drop the legacy key after
      // translation so the saved shape stays clean.
      if (typeof copy.options.autoTagsRunOnSecondary === 'boolean') {
        const t = copy.options.autoTagsRunOnSecondary ? 'both' : 'primary';
        if (copy.options.audioTagsTarget === 'primary') copy.options.audioTagsTarget = t;
        if (copy.options.videoTagsTarget === 'primary') copy.options.videoTagsTarget = t;
        delete copy.options.autoTagsRunOnSecondary;
      }
      // Migrate legacy recoverIncludeSecondary boolean → recoverTarget.
      // true → 'both' (run primary AND secondary); false → 'primary'
      // (default). recoverTarget defaulted to 'primary' above so an
      // older rule without the flag stays primary-only. Keep the legacy
      // key on the saved shape — backend's JobOptions.RecoverIncludeSecondary
      // is still read by the scheduler runner; the new field is purely
      // a wizard/chain-dispatcher concern. Drop only if the user
      // explicitly toggled the new picker (handled inline via
      // setPerActionInstance).
      if (typeof copy.options.recoverIncludeSecondary === 'boolean' &&
          copy.options.recoverTarget === 'primary') {
        copy.options.recoverTarget = copy.options.recoverIncludeSecondary ? 'both' : 'primary';
      }
      // Migrate legacy single-mode rules to combined-mode shape so the
      // wizard's chain-checkbox UI can edit them. mode='tag' → mode=
      // 'combined' + combinedModes=['tag']. Save-time persists the
      // new shape; the chain runner / scheduler-runner already accept
      // both shapes via has() → r.mode === m || combinedModes.includes(m).
      if (copy.mode && copy.mode !== 'combined') {
        const legacyMode = copy.mode;
        copy.mode = 'combined';
        copy.options.combinedModes = copy.options.combinedModes || [];
        if (!copy.options.combinedModes.includes(legacyMode)) {
          copy.options.combinedModes.push(legacyMode);
        }
      }
      this.editingRule = copy;
      // Lock appType to whatever the existing rule's instance is —
      // editing a Radarr rule should never expose Sonarr instances in
      // the dropdown, and vice versa. Cross-type "edit" is effectively
      // a different rule; user must delete + re-create.
      const editInst = (this.instances || []).find(i => i.id === copy.instanceId);
      const editAppType = editInst ? editInst.type : 'radarr';
      this.ruleEditor = { open: true, isCreate: false, isQuickFix: false, step: 0, activeTab: 'basics', appType: editAppType, busy: false, error: '', cronError: '', nextFires: [], fixedAction: '' };
      this.computeRuleEditorNextFires();
    },
    closeRuleEditor() {
      // Closing the wizard mid-run signals "abort the chain". The
      // chain's isCancelled() guard reads this between phases, so
      // the next phase won't fire. The CURRENT phase keeps running
      // unless it's a DV scan — those have a backend cancel endpoint
      // that flips the scan's context (ffmpeg/dovi_tool die within
      // a second). Other phases (tag/audio/video/recover/discover)
      // are short enough that letting them complete is fine.
      if (this.ruleEditor.busy) {
        this.cancelRunningChain();
      }
      this.ruleEditor.open = false;
      this.editingRule = null;
    },
    dismissQuickFixResults() {
      this.quickFixResults = null;
    },

    // Aborts whatever Quick fix-all / rule-edit chain is mid-flight.
    // Sets a flag the chain loop's isCancelled() picks up between
    // phases AND fires the backend's DV cancel endpoint when a DV
    // phase is the current slow one. Idempotent — Cancel button +
    // Esc + wizard close all funnel here.
    cancelRunningChain() {
      this.chainCancelRequested = true;
      if (this.dvScanProgress && this.dvScanProgress.running) {
        this.cancelDvScan();
      }
    },

    // Re-fire the previously-previewed Quick fix-all chain in apply
    // mode. Reuses the rule snapshot stored on the result so the
    // user doesn't have to re-walk the wizard. Discover stays
    // preview-only (it has no apply concept); other phases get
    // mode='apply' via the chain runner's per-phase logic.
    canApplyQuickFixFromPreview() {
      const q = this.quickFixResults;
      return !!(q && q.runMode === 'preview' && q.ruleSnapshot && !this.ruleEditor.busy);
    },
    // Tooltip for the QFA result panel's "⚡ Apply now" button. The
    // chain reads per-phase targets from the saved snapshot — if any
    // phase resolves to secondary (target='secondary' / 'both', or
    // tag-mode syncToSecondary), the apply hits both instances.
    // Otherwise it's primary only. Tells the user up-front rather
    // than letting them assume the variant they're viewing is the
    // only target.
    applyQuickFixTooltip() {
      const q = this.quickFixResults;
      if (!q || !q.ruleSnapshot) return 'Re-fire the same chain in apply mode using these settings.';
      const r = q.ruleSnapshot;
      const opts = r.options || {};
      const has = (m) => r.mode === m || (r.mode === 'combined' && (opts.combinedModes || []).includes(m));
      const targets = [];
      if (has('recover'))   targets.push(opts.recoverTarget   || 'primary');
      if (has('audiotags')) targets.push(opts.audioTagsTarget || 'primary');
      if (has('videotags')) targets.push(opts.videoTagsTarget || 'primary');
      if (has('dvdetail'))  targets.push(opts.dvDetailTarget  || 'primary');
      const tagHitsSecondary = has('tag') && !!opts.syncToSecondary;
      const hitsSecondary = tagHitsSecondary || targets.some(t => t === 'secondary' || t === 'both');
      if (!hitsSecondary) {
        return 'Re-fires the chain in apply mode against this instance only.';
      }
      // Resolve concrete names — primary from rule.instanceId, secondary
      // = first other-of-same-type. Falls back to "Primary"/"Secondary"
      // if either lookup fails (e.g. instance was deleted post-preview).
      const primary = (this.instances || []).find(i => i.id === r.instanceId);
      const primaryName = primary ? primary.name : 'Primary';
      const secondary = primary
        ? (this.instances || []).find(i => i.type === primary.type && i.id !== primary.id)
        : null;
      const secondaryName = secondary ? secondary.name : 'Secondary';
      return 'Re-fires the chain in apply mode. Writes to ' + primaryName + ' + ' + secondaryName + '.';
    },
    async applyQuickFixFromPreview() {
      const q = this.quickFixResults;
      if (!q || !q.ruleSnapshot) return;
      // Deep clone so we don't mutate the result panel's snapshot.
      const rule = JSON.parse(JSON.stringify(q.ruleSnapshot));
      rule.options = rule.options || {};
      rule.options.runMode = 'apply';
      // Empty out previous result so the re-run replaces it cleanly.
      this.quickFixResults = null;
      await this.runQuickFixChain(rule);
    },

    // Switch between primary / secondary variants when the chain
    // ran the active phase on multiple instances (target='both').
    selectQfaDetailVariant(idx) {
      if (!this.qfaDetailVariants || idx < 0 || idx >= this.qfaDetailVariants.length) return;
      this.qfaDetailVariantIdx = idx;
      const v = this.qfaDetailVariants[idx];
      // Replay viewPhaseDetails-equivalent slot writes for the
      // active phase. Re-using the dispatcher would close+reopen
      // the modal; we just want to swap the response in place.
      if (this.qfaDetail === 'audio') {
        this.qfaDetailAudio = v.response;
        this.qfaDetailAutoFilter = this.pickAutoDetailFilter(v.response);
      } else if (this.qfaDetail === 'video') {
        this.qfaDetailVideo = v.response;
        this.qfaDetailAutoFilter = this.pickAutoDetailFilter(v.response);
      } else if (this.qfaDetail === 'dv') {
        this.qfaDetailDv = v.response;
        this.qfaDetailDvStatusFilter = null;
        this.qfaDetailDvTagFilter = null;
      } else if (this.recoverResults) {
        // Recover lives in its own modal (recoverResults slot, not
        // qfaDetail*) but reuses qfaDetailVariants for the switcher.
        // Swapping the response means re-running viewPhaseDetails-
        // recover's setup: hydrate, auto-select would-fix rows, pick
        // a sensible filter chip default, and reload exclusions for
        // the variant's instance.
        this.recoverResults = v.response;
        this.recoverError = '';
        this.recoverExpanded = {};
        this.recoverSeriesExpanded = {};
        this.recoverSeasonExpanded = {};
        const sel = {};
        for (const it of (v.response.recover || [])) {
          if (it.status === 'would-fix') sel[it.id] = true;
        }
        this.recoverApplySelected = sel;
        const t = (v.response.totals || {});
        if (t.recoverWouldFix) this.recoverFilter = 'would-fix';
        else if (t.recoverFlagged) this.recoverFilter = 'flagged';
        else this.recoverFilter = 'all';
        if (v.response.instance && v.response.instance.id) {
          this.loadRecoverExclusions(v.response.instance.id);
        }
      }
      // Clear filters/expansions that were keyed off the prior
      // variant's data so the new variant renders cleanly.
      this.qfaDetailExpanded = {};
      this.qfaDetailAutoTagFilter = null;
    },

    closeQfaDetail() {
      this.qfaDetail = null;
      this.qfaDetailExpanded = {};
      this.qfaDetailAudio = null;
      this.qfaDetailVideo = null;
      this.qfaDetailDv = null;
      // Drop variant memory on close — next open populates fresh.
      this.qfaDetailVariants = [];
      this.qfaDetailVariantIdx = 0;
      this.qfaDetailDvStatusFilter = null;
      this.qfaDetailDvTagFilter = null;
      this.qfaDetailDvStatusHelpOpen = false;
      this.qfaDetailAutoTagFilter = null;
      this.qfaDetailBreakdownOpen = false;
      // Sonarr per-series-season expansion — keyed by (seriesId,
      // seasonNumber). Wipes on modal close so a fresh scan doesn't
      // see leftover expand-state from a prior viewing AND so the map
      // doesn't grow unbounded across sessions.
      this.qfaDetailSeasonExpanded = {};
      // Audio/Video/DV standalone Run scans set scanResults.audioTags etc.
      // BEFORE viewPhaseDetails routes through this modal. Clear those too
      // so the orphan state doesn't linger after the modal closes — the
      // historicalRunInfo banner stays in sync regardless of which path
      // populated the modal (Run scan / History click / QFA chain phase).
      if (this.scanResults) {
        this.scanResults.audioTags = null;
        this.scanResults.videoTags = null;
        this.scanResults.dvDetail = null;
      }
      if (this.historicalRunInfo &&
          (this.historicalRunInfo.kind === 'audiotags' ||
           this.historicalRunInfo.kind === 'videotags' ||
           this.historicalRunInfo.kind === 'dvdetail')) {
        this.historicalRunInfo = null;
      }
    },

    // Click handler for phase rows on either result panel (Quick fix-
    // all + saved-rule run-now both render on Run mode; the History
    // tab opens the same drill-in modal overlaid). Takes the phase
    // object directly — no need to look it up in a collection — so
    // the same handler serves both panels.
    //
    // Opens a dedicated drill-in modal (Tag / Recover / ExtraTags) or
    // the existing Discover modal. Modals keep the user inside their
    // current tab/context; closing returns to the panel underneath.
    viewPhaseDetails(p) {
      if (!p || !p.response) {
        this.showToast('No detail available for this phase', 'error');
        return;
      }
      // Close any other open result modal first so two don't stack.
      // Map phase name to the closeAllResultModals except-key.
      const exceptMap = {
        tag: 'tag', discover: 'discover',
        audiotags: 'audio', videotags: 'video', dvdetail: 'dv',
      };
      this.closeAllResultModals(exceptMap[p.phase] || null);
      this.qfaDetailExpanded = {};
      // Pick a sensible default chip based on what the run produced —
      // mirrors pickDefaultScanFilter for live runs so the modal opens
      // on the chip with content rather than an empty default.
      switch (p.phase) {
        case 'tag': {
          // Tag unified through the top-level Tag detail modal — same
          // partial that the standalone Tag scan + History surfaces
          // use. Hydrate scanResults.tag instead of qfaDetailTag; the
          // modal pops up on scanResults.tag being non-null.
          this.scanResults.tag = p.response;
          this.scanGroupExpanded = {};
          this.scanRowExpanded = {};
          // Hydrate the page-level scan-state that confirmScanApply →
          // runTagInternal reads on Apply-now re-fire. Without this,
          // a chain-driven Tag preview (target=both / filter-only /
          // sync-on) would re-fire with stale page-level state and
          // silently downgrade scope (e.g. drop syncToInstanceId).
          // Source of truth in priority order:
          //   1. quickFixResults.ruleSnapshot (chain context — full
          //      rule state including tagSource + filterOnlyTag +
          //      cleanup + sync settings)
          //   2. p.response.instance.id (whichever instance the
          //      preview actually ran against — covers History replay
          //      and standalone runs).
          // Standalone Tag-RG wizard already seeds these in
          // runTagRgWizard, so steps below idempotently re-affirm.
          const snap = (this.quickFixResults && this.quickFixResults.ruleSnapshot) || null;
          if (snap && snap.instanceId) {
            this.scanInstanceId = snap.instanceId;
            const o = snap.options || {};
            this.scanSyncToSecondary = !!o.syncToSecondary;
            this.scanCleanupUnusedTags = !!o.cleanupUnusedTags;
            this.scanTagSource = o.tagSource || '';
            this.scanFilterOnlyTag = o.filterOnlyTag || '';
          } else if (p.response.instance && p.response.instance.id) {
            this.scanInstanceId = p.response.instance.id;
            // No ruleSnapshot → can't recover sync intent. The
            // confirm modal's secondary count comes from response
            // totals, so if secondary deltas are present we know
            // sync was on. Setting scanSyncToSecondary based on the
            // observed deltas keeps the Apply re-fire honoring it.
            const t = p.response.totals || {};
            this.scanSyncToSecondary = !!(t.secondaryToAdd || t.secondaryToRemove || t.secondaryToKeep || t.secondaryMissing);
          }
          const t = (p.response.totals || {});
          if ((t.toAdd || 0) + (t.secondaryToAdd || 0) > 0) this.scanFilter = 'add';
          else if ((t.toRemove || 0) + (t.secondaryToRemove || 0) > 0) this.scanFilter = 'remove';
          else if ((t.toKeep || 0) + (t.secondaryToKeep || 0) > 0) this.scanFilter = 'keep';
          else this.scanFilter = 'add';
          this.scanInstanceFilter = 'both';
          break;
        }
        case 'recover':
          // Recover unified through the top-level Recover detail modal
          // — same partial that the standalone Run Recover + History
          // surfaces use. Hydrate recoverResults instead of the
          // QFA-modal-only qfaDetailRecover; the modal pops up on
          // recoverResults being non-null.
          this.recoverResults = p.response;
          this.recoverError = '';
          this.recoverExpanded = {};
          this.recoverSeriesExpanded = {};
          this.recoverSeasonExpanded = {};
          // Auto-select would-fix rows + sensible filter default — same
          // semantics as runRecoverCheck so a wizard-driven preview lands
          // ready to Apply with one click. Without this the Apply button
          // sits disabled and the user has to manually re-check every row
          // they already implicitly approved by running the preview.
          {
            const sel = {};
            for (const it of (this.recoverResults.recover || [])) {
              if (it.status === 'would-fix') sel[it.id] = true;
            }
            this.recoverApplySelected = sel;
            const t = this.recoverResults.totals || {};
            if (t.recoverWouldFix) this.recoverFilter = 'would-fix';
            else if (t.recoverFlagged) this.recoverFilter = 'flagged';
            else this.recoverFilter = 'all';
          }
          if (p.response && p.response.instance && p.response.instance.id) {
            this.loadRecoverExclusions(p.response.instance.id);
          }
          break;
        case 'audiotags': {
          this.qfaDetailAudio = p.response;
          this.qfaDetail = 'audio';
          this.qfaDetailAutoFilter = this.pickAutoDetailFilter(p.response);
          break;
        }
        case 'videotags': {
          this.qfaDetailVideo = p.response;
          this.qfaDetail = 'video';
          this.qfaDetailAutoFilter = this.pickAutoDetailFilter(p.response);
          break;
        }
        case 'dvdetail': {
          this.qfaDetailDv = p.response;
          this.qfaDetail = 'dv';
          this.qfaDetailDvStatusFilter = null;
          this.qfaDetailDvTagFilter = null;
          this.qfaDetailDvFilter = this.pickDvDetailFilter(p.response);
          break;
        }
        case 'discover':
          // Discover unified through the top-level Discover detail modal
          // — auto-pops on scanResults.discover being non-null. Same
          // trigger pattern as Recover and Tag.
          this.scanResults.discover = p.response;
          this.scanDiscoverSelected = {};
          this.scanDiscoverExpanded = {};
          break;
        case 'missingepisodes': {
          // Missing-episodes uses the existing standalone-tab UI for
          // drill-down (per-series → seasons → episodes with per-row
          // Search + bulk Tag). Hydrate that state from the chain
          // response + the rule snapshot, then navigate the user to
          // the Missing Episodes sub-tab so they can act on the
          // findings. Apply re-fire happens via the QFA result panel's
          // Apply button (flips runMode='apply'); the standalone tab
          // is for ad-hoc selective Search / Tag after the chain run.
          this.missingEpisodesPreview = p.response;
          this.missingEpisodesError = '';
          const snap = (this.quickFixResults && this.quickFixResults.ruleSnapshot) || null;
          if (snap) {
            this.scanInstanceId = snap.instanceId || this.scanInstanceId;
            if (snap.missingEpisodes) {
              this.missingEpisodesConfig = {
                ...this.missingEpisodesConfig,
                ...JSON.parse(JSON.stringify(snap.missingEpisodes)),
              };
            }
          }
          // Pre-select all missing episodes — same default the
          // standalone Preview button uses, so the user lands on a
          // result ready for Search Selected / Tag series.
          const sel = {};
          for (const s of (p.response.series || [])) {
            for (const season of (s.seasons || [])) {
              for (const ep of (season.missingEpisodes || [])) {
                sel[ep.episodeID] = true;
              }
            }
          }
          this.missingEpisodesSelected = sel;
          this.scanAppType = 'sonarr';
          this.scanSection = 'missing-episodes';
          break;
        }
        case 'plexsync': {
          // Reuse the one-off run modal's result view. The phase row's
          // response is a PlexLabelRuleRun; we hand the run modal a
          // synthetic rule shaped just enough for its header (the
          // result markup is wrapped in x-if="rule"). Plex target
          // config isn't carried on the run, so the context strip may
          // read "(unknown Plex)" — cosmetic; the counts + per-label
          // table all come from the run itself.
          const run = p.response || {};
          this.plexLabelRunModal = {
            open: true,
            stage: 'result',
            rule: {
              name: 'Plex label sync',
              instanceId: p.instanceId || '',
              targetTypes: run.targetTypes || [],
              labelDisplay: {},
              targets: [{ plexInstanceId: '', libraryKeys: [] }],
            },
            runMode: run.runMode || 'apply',
            result: run,
            error: '',
            detailsFilter: '',
          };
          break;
        }
        case 'tbarefresh': {
          // No dedicated TBA modal — hydrate the sub-tab's preview state
          // and jump there, same as missingepisodes does.
          this.tbaRefreshPreview = p.response || null;
          this.scanAppType = 'sonarr';
          this.scanSection = 'tba-refresh';
          break;
        }
        default:
          this.showToast('Unknown phase: ' + p.phase, 'error');
      }
    },
    // Back-compat thin wrapper for the QFA result panel that still
    // calls this method by name with phase/idx args. Looks up the
    // phase row in quickFixResults.phases and forwards to viewPhaseDetails.
    viewQuickFixPhaseDetails(phase, idx) {
      const phases = (this.quickFixResults && this.quickFixResults.phases) || [];
      let p = (typeof idx === 'number' && phases[idx] && phases[idx].phase === phase) ? phases[idx] : null;
      if (!p) p = phases.find(x => x.phase === phase);
      this.viewPhaseDetails(p);
    },

    // ===== QFA Audio / Video drill-in helpers =====
    // Both phases share the same response shape (Items[] with
    // AutoDecisions[] {bucket, tag, action}; Totals.AutoTagRollups
    // []). The drill-in modal renders a per-tag rollup table at the
    // top + a per-movie list filtered by the action chip below it.
    // Splitting state by mode (Audio vs Video) is convenient for the
    // markup; the helpers below take the active response as a param
    // so the same code serves both.

    // pickAutoDetailFilter: open the chip with content rather than
    // an empty default. Mirrors pickDefaultScanFilter for the Tag
    // modal — saves the user a click 99% of the time.
    pickAutoDetailFilter(resp) {
      const t = (resp && resp.totals) || {};
      if ((t.toAdd || 0) > 0)    return 'add';
      if ((t.toRemove || 0) > 0) return 'remove';
      if ((t.toKeep || 0) > 0)   return 'keep';
      return 'add';
    },

    // qfaDetailAutoActive: which auto response is open right now.
    // Returns null when neither is showing.
    qfaDetailAutoActive() {
      if (this.qfaDetail === 'audio') return this.qfaDetailAudio;
      if (this.qfaDetail === 'video') return this.qfaDetailVideo;
      return null;
    },

    // qfaDetailAutoNounPlural / qfaDetailAutoNounSingular pick the
    // right word for the active QFA result based on the instance
    // type. Sonarr Audio/Video tags apply at the series level (per
    // the help text in the rule editor), so "series" is the right
    // word — not "episode". Radarr is "movie" / "movies".
    qfaDetailAutoNounPlural() {
      const r = this.qfaDetailAutoActive();
      if (r && r.instance && r.instance.type === 'sonarr') return 'series';
      return 'movies';
    },
    qfaDetailAutoNounSingular() {
      const r = this.qfaDetailAutoActive();
      if (r && r.instance && r.instance.type === 'sonarr') return 'series';
      return 'movie';
    },
    // Capitalised plural for sentence-start titles + tooltips. Saves
    // .charAt(0).toUpperCase() inlining in every template.
    qfaDetailAutoNounPluralCap() {
      return this.qfaDetailAutoNounPlural() === 'series' ? 'Series' : 'Movies';
    },

    // Counts of items whose at least one decision has a given action
    // (movie-level, not decision-level — same convention as the
    // standalone fane). Used for the chip badges.
    qfaDetailAutoFilterCounts() {
      const r = this.qfaDetailAutoActive();
      const out = { add: 0, remove: 0, keep: 0 };
      if (!r || !Array.isArray(r.items)) return out;
      for (const it of r.items) {
        const decs = it.autoDecisions || [];
        const seen = new Set();
        for (const d of decs) {
          const a = (d.action || '').toLowerCase();
          if (a && !seen.has(a) && (a === 'add' || a === 'remove' || a === 'keep')) {
            out[a]++;
            seen.add(a);
          }
        }
      }
      return out;
    },

    // Movies / series whose AutoDecisions have at least one entry
    // matching the active filter chip. Each item is annotated with
    // the filtered subset of decisions (so the row only shows
    // matching tags, not the full decision list). Optional tag-filter
    // narrows further to a specific (bucket, tag) pair.
    //
    // Series with a non-empty error field (Sonarr fetch failure) are
    // ALWAYS surfaced — they have no decisions to filter on, but
    // they're failures the user needs to see. Without this branch,
    // Sonarr fetch errors disappear silently (the row exists in the
    // response but no chip catches it).
    qfaDetailAutoFilteredItems() {
      const r = this.qfaDetailAutoActive();
      if (!r || !Array.isArray(r.items)) return [];
      const f = (this.qfaDetailAutoFilter || 'add').toLowerCase();
      const tagF = this.qfaDetailAutoTagFilter;
      const out = [];
      for (const it of r.items) {
        if (it.error) {
          out.push({ ...it, decisionsFiltered: [], _errorRow: true });
          continue;
        }
        const decs = it.autoDecisions || [];
        const matched = decs.filter(d => {
          if ((d.action || '').toLowerCase() !== f) return false;
          if (tagF) {
            if ((d.bucket || '').toLowerCase() !== (tagF.bucket || '').toLowerCase()) return false;
            if ((d.tag    || '').toLowerCase() !== (tagF.tag    || '').toLowerCase()) return false;
          }
          return true;
        });
        if (matched.length === 0) continue;
        out.push({ ...it, decisionsFiltered: matched });
      }
      return out;
    },

    // Count of error rows in the active scan response — used to
    // surface a "N series failed to fetch" banner above the chip row
    // when there are any. Sonarr-only in practice (Radarr handler
    // doesn't produce error rows in this shape) but cheap to evaluate
    // on Radarr too.
    qfaDetailAutoErrorCount() {
      const r = this.qfaDetailAutoActive();
      if (!r || !Array.isArray(r.items)) return 0;
      let n = 0;
      for (const it of r.items) if (it.error) n++;
      return n;
    },

    // Click-handler for a per-tag breakdown row. Sets the tag-filter
    // and forces the action-filter chip to match the row's action so
    // the per-movie list immediately shows the matching items. Toggles
    // off when clicking the same row twice (acts as un-filter).
    setAutoTagFilter(action, bucket, tag) {
      const cur = this.qfaDetailAutoTagFilter;
      if (cur && cur.bucket === bucket && cur.tag === tag && this.qfaDetailAutoFilter === action) {
        this.qfaDetailAutoTagFilter = null;
        return;
      }
      this.qfaDetailAutoFilter = action;
      this.qfaDetailAutoTagFilter = { bucket, tag };
    },

    clearAutoTagFilter() {
      this.qfaDetailAutoTagFilter = null;
    },

    // ---- Sonarr per-series episode grouping (M-Sonarr Audio/Video) ----
    //
    // Sonarr audio/video scans return per-series rows whose Episodes[]
    // payload carries one entry per episodefile. The drill-in's expanded
    // view renders these grouped season → episodes (collapsible
    // seasons, flat episodes inside) — same pattern Recover-Sonarr uses
    // (partials/recover-result-panel.html). Pure data-shaping; no I/O.
    //
    // Empty / non-Sonarr items return [] so the markup branch can
    // safely no-op render against any item.

    episodesGroupedBySeason(item) {
      if (!item || !Array.isArray(item.episodes) || item.episodes.length === 0) return [];
      // Episode-number extraction for stable per-season ordering.
      // localeCompare on relativePath sorts S01E10 BEFORE S01E2
      // lexicographically, so we mine the SxxExx token and sort by
      // (season, episode) numeric. Falls back to episodeFileId when
      // the token isn't found (mid-process renames).
      const epOrderKey = (ev) => {
        const m = (ev.relativePath || '').match(/S(\d+)E(\d+)/i);
        if (m) return parseInt(m[1], 10) * 1000 + parseInt(m[2], 10);
        return ev.episodeFileId || 0;
      };
      const buckets = new Map();
      for (const ep of item.episodes) {
        const k = (typeof ep.seasonNumber === 'number') ? ep.seasonNumber : 'unknown';
        if (!buckets.has(k)) buckets.set(k, []);
        buckets.get(k).push(ep);
      }
      const out = [];
      for (const [k, eps] of buckets.entries()) {
        eps.sort((a, b) => epOrderKey(a) - epOrderKey(b));
        out.push({
          seasonNumber: typeof k === 'number' ? k : null,
          episodes: eps,
        });
      }
      out.sort((a, b) => {
        // Specials (season 0) ahead of "Unknown" (null); regular
        // seasons ascending.
        if (a.seasonNumber === null && b.seasonNumber === null) return 0;
        if (a.seasonNumber === null) return 1;
        if (b.seasonNumber === null) return -1;
        return a.seasonNumber - b.seasonNumber;
      });
      return out;
    },

    // Reused state map: { seriesId: { seasonNumber: true } } so each
    // (series, season) pair toggles independently. Lives on root state
    // not item-local because Alpine x-for keys would re-create item
    // objects on every reactivity tick and clobber expanded-state.
    qfaDetailSeasonExpanded: {},

    toggleQfaDetailSeasonExpanded(seriesId, seasonNumber) {
      if (!this.qfaDetailSeasonExpanded[seriesId]) {
        this.qfaDetailSeasonExpanded[seriesId] = {};
      }
      const k = seasonNumber === null ? 'unknown' : seasonNumber;
      this.qfaDetailSeasonExpanded[seriesId][k] = !this.qfaDetailSeasonExpanded[seriesId][k];
    },

    qfaDetailSeasonIsExpanded(seriesId, seasonNumber) {
      const sm = this.qfaDetailSeasonExpanded[seriesId];
      if (!sm) return false;
      const k = seasonNumber === null ? 'unknown' : seasonNumber;
      return !!sm[k];
    },

    // Format "S01E05" or "S01" fallback for an episode row label.
    // ev.relativePath usually carries the full release name; we mine
    // out the first SxxExx token to render a compact label. When the
    // file doesn't yield one (mid-process renames), fall back to
    // "S<season>" so the row still has identity. Mirrors backend
    // sonarrEpisodeLabel().
    qfaEpisodeLabel(ev) {
      if (!ev) return '';
      const m = (ev.relativePath || '').match(/S\d+E\d+(?:[E-]\d+)*/i);
      if (m) return m[0].toUpperCase();
      if (typeof ev.seasonNumber === 'number') {
        return 'S' + String(ev.seasonNumber).padStart(2, '0');
      }
      return '';
    },

    // Compact one-line summary the per-episode row shows next to its
    // S01E05 label. Pulls from the strings the backend pre-computed
    // via SummariseMediaInfo (resolution / videoCodec / hdr / audio /
    // channels). Skips empty pieces so the line stays clean.
    qfaEpisodeMediaLine(ev) {
      if (!ev) return '';
      const parts = [];
      if (ev.resolution) parts.push(ev.resolution);
      if (ev.videoCodec) parts.push(ev.videoCodec);
      // hasTenBit mirrors the engine's is10Bit inference: bitDepth==10 OR
      // HDR rangeType implies 10-bit. Webhook payloads omit videoBitDepth,
      // so reading the raw int would miss the 10bit tag on every HDR
      // webhook fire; the engine-computed flag handles both code paths.
      if (ev.hasTenBit) parts.push('10bit');
      if (ev.hdr && ev.hdr !== 'sdr') parts.push(ev.hdr);
      if (ev.audioCodec) parts.push(ev.audioCodec);
      if (ev.audioChannels) parts.push(ev.audioChannels);
      if (ev.hasAtmos) parts.push('atmos');
      return parts.join(' · ');
    },

    // Friendly header label for a season row.
    qfaSeasonLabel(seasonNumber) {
      if (seasonNumber === null) return 'Unknown season';
      if (seasonNumber === 0) return 'Specials';
      return 'Season ' + seasonNumber;
    },

    // ---- DV detail drill-in helpers ----
    // Same UX shape as the audio/video drill-in (qfaDetailAutoActive +
    // friends) but reads the DV-specific item fields: dvDecisions
    // (action+status+tag, no bucket), dvDetail (profile/cmVersion/layer),
    // dvStatus (cached/extracted/tools-missing/failed/skipped).

    pickDvDetailFilter(r) {
      if (!r || !r.totals) return 'add';
      const t = r.totals;
      if ((t.toAdd || 0) > 0)    return 'add';
      if ((t.toRemove || 0) > 0) return 'remove';
      if ((t.toKeep || 0) > 0)   return 'keep';
      return 'add';
    },

    // Per-status counts for the secondary chip row (cached / extracted /
    // failed). Reads each item's dvStatus once. Items without DV
    // (dvStatus === 'no-dv' or empty) are excluded from these counts —
    // those rows produce no decisions and aren't useful here.
    qfaDetailDvStatusCounts() {
      const r = this.qfaDetailDv;
      const out = { cached: 0, extracted: 0, failed: 0, 'tools-missing': 0, skipped: 0 };
      if (!r || !Array.isArray(r.items)) return out;
      for (const it of r.items) {
        const s = (it.dvStatus || '').toLowerCase();
        if (s && s !== 'no-dv' && out[s] !== undefined) out[s]++;
      }
      return out;
    },

    // Per-action counts (movie-level: a movie counts once per action it
    // contains, even if multiple decisions share the action). Drives
    // the action-chip badges.
    qfaDetailDvFilterCounts() {
      const r = this.qfaDetailDv;
      const out = { add: 0, remove: 0, keep: 0 };
      if (!r || !Array.isArray(r.items)) return out;
      for (const it of r.items) {
        const decs = it.dvDecisions || [];
        const seen = new Set();
        for (const d of decs) {
          const a = (d.action || '').toLowerCase();
          if (a && !seen.has(a) && (a === 'add' || a === 'remove' || a === 'keep')) {
            out[a]++;
            seen.add(a);
          }
        }
      }
      return out;
    },

    // Movies whose dvDecisions match the active action chip; further
    // narrowed by qfaDetailDvStatusFilter (cached/extracted/failed)
    // and qfaDetailDvTagFilter (specific tag from a breakdown click).
    // Each item is annotated with the matching subset so the row shows
    // only the relevant decisions.
    qfaDetailDvFilteredItems() {
      const r = this.qfaDetailDv;
      if (!r || !Array.isArray(r.items)) return [];
      const f = (this.qfaDetailDvFilter || 'add').toLowerCase();
      const sf = this.qfaDetailDvStatusFilter;
      const tagF = this.qfaDetailDvTagFilter;
      const out = [];
      for (const it of r.items) {
        if (sf && (it.dvStatus || '').toLowerCase() !== sf) continue;
        const decs = it.dvDecisions || [];
        const matched = decs.filter(d => {
          if ((d.action || '').toLowerCase() !== f) return false;
          if (tagF && (d.tag || '').toLowerCase() !== (tagF.tag || '').toLowerCase()) return false;
          return true;
        });
        if (matched.length === 0) continue;
        out.push({ ...it, decisionsFiltered: matched });
      }
      return out;
    },

    // Click-handler for a per-tag breakdown row (DV variant). Mirrors
    // setAutoTagFilter — sets action chip + tag filter; clicking the
    // same row again clears.
    setDvTagFilter(action, tag) {
      const cur = this.qfaDetailDvTagFilter;
      if (cur && cur.tag === tag && this.qfaDetailDvFilter === action) {
        this.qfaDetailDvTagFilter = null;
        return;
      }
      this.qfaDetailDvFilter = action;
      this.qfaDetailDvTagFilter = { tag, action };
    },

    clearDvTagFilter() {
      this.qfaDetailDvTagFilter = null;
    },

    // Pretty short-form for the DV detail summary line on a row, e.g.
    // "Profile 8 · MEL · CM v2.9". Reads from the dvDetail blob.
    qfaDvDetailSummary(it) {
      const d = it && (it.dvDetail);
      if (!d) return '';
      const parts = [];
      if (d.profile)   parts.push('Profile ' + d.profile);
      if (d.layer)     parts.push(d.layer.toUpperCase());
      if (d.cmVersion) parts.push('CM v' + (d.cmVersion === 4 ? '4.0' : '2.9'));
      return parts.join(' · ');
    },

    // Plain-English description of a dvStatus value. Used as the
    // tooltip on each status chip + each per-row pill so users don't
    // have to memorise the vocabulary. Keep in sync with the engine's
    // status emit logic in scan_dv_detail.go.
    dvStatusExplain(status) {
      const s = (status || '').toLowerCase();
      switch (s) {
        case 'cached':
          return 'DV detail was already extracted on a previous scan and read from /config/dv-cache.json — no ffmpeg work needed this time.';
        case 'extracted':
          return 'DV detail was freshly extracted from the file via ffmpeg + dovi_tool during this scan, then cached.';
        case 'failed':
          return 'Extraction tried but failed — file unreachable, RPU corrupt, or an ffmpeg/dovi_tool error. See the per-row error in the expanded view.';
        case 'tools-missing':
          return 'ffmpeg or dovi_tool not on PATH. Tools ship baked into the image; if you see this status the image build is broken — check docker logs and report the issue.';
        case 'skipped':
          return 'Not a Dolby Vision file (Radarr mediaInfo says so) — DV detail does not run.';
        case 'no-dv':
          return 'Not a Dolby Vision file — skipped before extraction.';
        default:
          return 'DV detail status: ' + s;
      }
    },

    // dvStatus pill colour. Returns object form (not a string) so
    // Alpine merges with the static pill style instead of replacing
    // it. The string-form trap documented in CLAUDE.md (M4 results-
    // table grid bug) bites the per-row pill markup that pairs a
    // static font-size/padding/border-radius style with this binding.
    qfaDvStatusStyle(status) {
      const s = (status || '').toLowerCase();
      if (s === 'cached')        return { color: 'var(--accent-green)', background: '#0d2f1a' };
      if (s === 'extracted')     return { color: 'var(--accent-sky)', background: '#0c1f33' };
      if (s === 'failed')        return { color: 'var(--accent-red)', background: 'var(--accent-red-bg)' };
      if (s === 'tools-missing') return { color: 'var(--accent-orange)', background: '#3d2f0a' };
      return { color: 'var(--text-secondary)', background: 'var(--bg-card)' };
    },

    // Phase-row label — usually just the phase name, but when audio
    // or video
    // tags ran twice (primary + secondary), show the instance name
    // alongside so the two rows are distinguishable.
    quickFixPhaseLabel(p) {
      if (!p) return '';
      // Audio + Video tags can run twice in one chain (primary +
      // secondary). When that happens, suffix with instance name to
      // distinguish the rows.
      if ((p.phase === 'audiotags' || p.phase === 'videotags') &&
          p.response && p.response.instance && p.response.instance.name) {
        const list = (this.quickFixResults && this.quickFixResults.phases) || [];
        const count = list.filter(x => x.phase === p.phase).length;
        if (count > 1) return p.phase + ' · ' + p.response.instance.name;
      }
      return p.phase;
    },

    // Per-phase summary string for the result panel — pulls a short
    // line from the response based on which action ran.
    //
    // Field names match scan_types.go scanTotals: toAdd / toRemove /
    // toKeep / noFile / discovered / recoverWouldFix / etc. The
    // Discovered LIST lives at p.response.discovered (top-level) —
    // separate from the Discovered COUNT at totals.discovered. Tag/
    // extratags totals also have secondary-* counterparts populated
    // when sync was on; surfaced inline for visibility.
    quickFixPhaseSummary(p) {
      if (!p || !p.response) return '(no response)';
      const t = p.response.totals || {};
      switch (p.phase) {
        case 'tag': {
          const parts = [
            `${t.toAdd || 0} to add`,
            `${t.toRemove || 0} to remove`,
            `${t.toKeep || 0} to keep`,
            `${t.noFile || 0} no file`,
          ];
          if (t.secondaryToAdd || t.secondaryToRemove || t.secondaryMissing) {
            parts.push(`secondary +${t.secondaryToAdd || 0} / -${t.secondaryToRemove || 0} / missing ${t.secondaryMissing || 0}`);
          }
          return parts.join(' · ');
        }
        case 'discover': {
          const list = p.response.discovered || [];
          return `${list.length} new ${list.length === 1 ? 'group' : 'groups'} found`;
        }
        case 'recover':
          return `${t.recoverWouldFix || 0} would fix · ${t.recoverFixed || 0} fixed · ${t.recoverFlagged || 0} flagged · ${t.recoverNoHistory || 0} no history`;
        case 'audiotags':
        case 'videotags': {
          const parts = [
            `${t.toAdd || 0} to add`,
            `${t.toRemove || 0} to remove`,
            `${t.toKeep || 0} to keep`,
          ];
          if (t.missingMediaInfo) parts.push(`${t.missingMediaInfo} missing mediaInfo`);
          return parts.join(' · ');
        }
        case 'dvdetail': {
          const parts = [
            `${t.dvCandidates || 0} DV candidates`,
            `${t.toAdd || 0} to add`,
            `${t.toRemove || 0} to remove`,
          ];
          if (t.dvExtractFailed) parts.push(`${t.dvExtractFailed} failed`);
          return parts.join(' · ');
        }
        case 'missingepisodes': {
          // Response shape comes from missingEpisodesPreviewResponse:
          // { seriesScanned, seriesWithGaps, totalMissingEpisodes }.
          // Apply-step results (tagApplied / searchApplied) live on the
          // phase row itself, not on p.response — render them inline so
          // users see what the chain actually wrote.
          const r = p.response || {};
          const parts = [
            `${r.seriesScanned || 0} scanned`,
            `${r.seriesWithGaps || 0} with gaps`,
            `${r.totalMissingEpisodes || 0} missing episodes`,
          ];
          if (p.tagApplied) parts.push(`tagged ${p.tagApplied.applied || 0}, untagged ${p.tagApplied.removed || 0}`);
          if (p.searchApplied) parts.push(`search triggered for ${p.searchApplied.triggered || 0}`);
          if (p.tagError) parts.push(`tag error: ${p.tagError}`);
          if (p.searchError) parts.push(`search error: ${p.searchError}`);
          return parts.join(' · ');
        }
        case 'plexsync': {
          // p.response is a PlexLabelRuleRun (no .totals). Prefer its
          // one-line summary; fall back to counts from the Added /
          // Removed / InSync maps.
          const run = p.response || {};
          if (run.summary) return run.summary;
          const sum = (m) => m ? Object.values(m).reduce((a, b) => a + b, 0) : 0;
          return `${run.matched || 0} matched · ${sum(run.added)} added · ${sum(run.removed)} removed · ${sum(run.inSync)} in sync`;
        }
        case 'tbarefresh': {
          const pr = p.response || {};
          let s = `${pr.totalFiles || 0} TBA files · ${pr.seriesWithTba || 0} series`;
          if (p.applied) s += ` · queued ${p.applied.queued || 0} renames`;
          return s;
        }
        default:          return '(unknown phase)';
      }
    },

    // ---- Per-instance-type mode availability ----
    // Single source of truth for which modes apply to which Arr type.
    // Sonarr today only supports recover; everything else is Radarr-only
    // until the per-episode-file refactor lands (M-Sonarr). The wizard
    // mode dropdown + Combined chain checkboxes filter through this so
    // users on a Sonarr instance never see Radarr-only options that
    // would 501 at scan time.
    ruleModeCatalog: [
      { value: 'tag',       label: 'Tag quality releases — tag movies whose release group passes your filters',      appliesTo: ['radarr'] },
      { value: 'discover',  label: 'Discover — find new release-groups in your library',                            appliesTo: ['radarr'] },
      { value: 'recover',   label: 'Recover — fill missing release-group fields from grab history',                 appliesTo: ['radarr', 'sonarr'] },
      { value: 'audiotags', label: 'Tag Audio — informative tags from audio mediaInfo (codec / channels / atmos)',  appliesTo: ['radarr', 'sonarr'] },
      { value: 'videotags', label: 'Tag Video — informative tags from video mediaInfo (resolution / codec / HDR)',  appliesTo: ['radarr', 'sonarr'] },
      { value: 'dvdetail',  label: 'Tag DV Details — Dolby Vision profile / CM tags (requires ffmpeg + dovi_tool)', appliesTo: ['radarr'] },
      { value: 'combined',  label: 'Combined — chain several of the above in one run',                              appliesTo: ['radarr', 'sonarr'] },
    ],
    // ruleCombinedSubstepCatalog drives the chain-step checkboxes on
    // Basics. M-Sonarr extension contract: when Sonarr support lands
    // for a phase, add 'sonarr' to the corresponding appliesTo array
    // here AND wire the matching Sonarr scan path in
    // internal/api/scan*.go. The wizard, instance dropdown, tab
    // visibility, and chain runner all key off appliesTo — no other
    // frontend changes needed.
    //
    // Sonarr coverage today: recover + audiotags + videotags. Tag
    // library + discover land with M-Sonarr Phase 2 (per-episode-file
    // walk for the release-group tagging path). DV detail stays
    // Radarr-only — extraction is per-file and series-level
    // aggregation isn't meaningful.
    ruleCombinedSubstepCatalog: [
      { value: 'discover',        label: 'Discover new release-groups',         appliesTo: ['radarr'] },
      { value: 'recover',         label: 'Recover missing release-groups',      appliesTo: ['radarr', 'sonarr'] },
      { value: 'tag',             label: 'Tag quality releases',                  appliesTo: ['radarr'] },
      { value: 'audiotags',       label: 'Tag Audio',                           appliesTo: ['radarr', 'sonarr'] },
      { value: 'videotags',       label: 'Tag Video',                           appliesTo: ['radarr', 'sonarr'] },
      { value: 'dvdetail',        label: 'Tag DV Details',                      appliesTo: ['radarr'], optIn: true },
      { value: 'missingepisodes', label: 'Find missing episodes',               appliesTo: ['sonarr'] },
      { value: 'plexsync',        label: 'Sync to Plex',                        appliesTo: ['radarr', 'sonarr'], optIn: true },
      { value: 'tbarefresh',      label: 'TBA refresh',                         appliesTo: ['sonarr'], optIn: true },
    ],
    ruleEditorInstanceType() {
      // Locked at open-time on ruleEditor.appType (Create/QFA seed from
      // scanAppType, Edit seeds from existing rule's instance.type) so
      // the wizard can't mid-flight cross the Arr-type boundary.
      if (this.ruleEditor && this.ruleEditor.appType) return this.ruleEditor.appType;
      // Fallback only for transitional state where appType isn't set —
      // resolve from the rule's current instance.
      const r = this.editingRule;
      if (!r) return null;
      const inst = (this.instances || []).find(i => i.id === r.instanceId);
      return inst ? inst.type : null;
    },
    ruleEditorInstancesAvailable() {
      const t = this.ruleEditorInstanceType();
      return (this.instances || [])
        .filter(i => !t || i.type === t)
        .sort((a, b) => a.name.localeCompare(b.name));
    },
    ruleModeOptionsForInstance() {
      const t = this.ruleEditorInstanceType();
      if (!t) return this.ruleModeCatalog;
      return this.ruleModeCatalog.filter(o => o.appliesTo.includes(t));
    },
    ruleCombinedSubstepsForInstance() {
      const t = this.ruleEditorInstanceType();
      if (!t) return this.ruleCombinedSubstepCatalog;
      return this.ruleCombinedSubstepCatalog.filter(s => s.appliesTo.includes(t));
    },
    ruleDefaultModeForInstanceType(type) {
      if (type === 'sonarr') return 'recover';
      return 'tag';
    },

    // ---- FUNCTION_INFO accessors ----
    //
    // Render-time helpers for the rule editor (and any future surface
    // that wants the canonical short copy). Filters by current instance
    // type + rule kind (schedule-only / webhook-only) so the same data
    // object can drive both Basics blocks.
    //
    // List order matches the order the entries appear in FUNCTION_INFO
    // — Object.values() preserves insertion order in modern JS engines.
    functionInfoList(opts) {
      const o = opts || {};
      const wantWebhook = !!o.webhook;
      const includeSeparate = !!o.includeSeparate;
      const t = this.ruleEditorInstanceType();
      return Object.values(this.FUNCTION_INFO).filter(fn => {
        // Arr-type gate.
        if (fn.appliesTo !== 'both' && t && fn.appliesTo !== t) return false;
        // Schedule-only (Cleanup) — only on schedule rules.
        if (fn.scheduleOnly && wantWebhook) return false;
        // Webhook-only (file delete / grab rename / qbit S/E) — only
        // on webhook rules.
        if (fn.webhookOnly && !wantWebhook) return false;
        // Separate-control (syncToSecondary) — excluded from lean
        // checkbox lists; help-panel renders includes them via
        // includeSeparate=true so the description still appears.
        if (fn.separateControl && !includeSeparate) return false;
        return true;
      });
    },
    // Picks the right summary based on rule kind + instance type.
    // Webhook context (single-file fire on Connect events) often differs
    // from schedule context (full-library walk) — summaryWebhook /
    // summaryWebhookSonarr override when present.  Falls through to
    // summary / summarySonarr otherwise.
    //
    // Pass { webhook: true } to force webhook resolution; default reads
    // the current ruleEditor kind so library-scan callers (which don't
    // touch ruleEditor) still get the schedule wording.
    functionInfoSummary(fn, opts) {
      if (!fn) return '';
      const o = opts || {};
      const isWebhook = (o.webhook !== undefined) ? !!o.webhook : !!this.ruleEditorIsWebhook?.();
      const t = this.ruleEditorInstanceType();
      if (isWebhook) {
        if (t === 'sonarr' && fn.summaryWebhookSonarr) return fn.summaryWebhookSonarr;
        if (fn.summaryWebhook) return fn.summaryWebhook;
      }
      if (t === 'sonarr' && fn.summarySonarr) return fn.summarySonarr;
      return fn.summary || '';
    },
    functionInfoTriggers(fn) {
      if (!fn) return [];
      const t = this.ruleEditorInstanceType();
      if (t === 'sonarr' && fn.triggersSonarr) return fn.triggersSonarr;
      return fn.triggers || [];
    },
    // Webhook-mode checkbox onChange — writes the value AND runs the
    // same side-effects QFA's combined-mode toggle does, so dependent
    // state stays consistent.  Mirror of ruleEditorCombinedToggle but
    // for the webhook fn* flags.
    //
    // Side-effects today:
    //   - Discover ON  → ensureDiscoverDefaults() (mirror QFA)
    //   - Tag-RG OFF   → clear fnSyncToSecondary (Sync mirrors Tag-RG
    //                    decisions — without Tag-RG it's invalid and
    //                    backend would reject; rule-state stays clean)
    webhookFnToggle(fn, checked) {
      const r = this.editingRule;
      if (!r || !r.options || !fn) return;
      r.options[fn.optionFlag] = checked;
      if (fn.id === 'discover' && checked) {
        this.ensureDiscoverDefaults();
      }
      if (fn.id === 'tagReleaseGroups' && !checked) {
        r.options.fnSyncToSecondary = false;
      }
    },
    // Functions marked requiresQbit (Grab Rename, qBit S/E tag, Category
    // Fix) need at least one qBit instance configured under Settings →
    // qBit before they can run. Without one, the checkbox is disabled
    // with a tooltip so the user can't tick a function that has no chance
    // of working. Once they add a qBit instance the checkbox unlocks
    // automatically (qbitInstances is reactive).
    webhookFnDisabled(fn) {
      if (!fn) return false;
      if (fn.requiresQbit && (this.qbitInstances || []).length === 0) return true;
      return false;
    },
    webhookFnDisabledReason(fn) {
      if (!fn) return '';
      if (fn.requiresQbit && (this.qbitInstances || []).length === 0) {
        return 'Add a qBittorrent instance under Settings → qBit before enabling this function.';
      }
      return '';
    },
    // Soft warning — DV detail layers profile/layer/CM-version tags on
    // top of the base `dv` tag, which Tag Video → HDR emits. Ticking
    // DV detail alone leaves files with profile tags but no base dv
    // tag. Surfaces as a hint under the checkbox list, not a hard
    // block — the user may know what they want.
    webhookDvWithoutVideoWarning() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnTagDvDetail && !o.fnTagVideo;
    },

    // ---- Mode / tab visibility helpers ----
    //
    // Each helper checks BOTH the legacy schedule-mode flag (mode +
    // combinedModes) AND the webhook-mode function flag (options.fnX,
    // set by the Basics step's function checkboxes when kind='webhook').
    // The unified rule editor uses the same RG / Filters / Audio /
    // Video / DV / Recover / Sync steps for both kinds — these helpers
    // are how the wizard decides which steps to show.
    ruleAffectsTag()       {
      const r = this.editingRule; if (!r) return false;
      if (r.mode === 'tag' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('tag'))) return true;
      return !!(r.options && r.options.fnTagReleaseGroups);
    },
    ruleAffectsDiscover()  {
      const r = this.editingRule; if (!r) return false;
      if (r.mode === 'discover' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('discover'))) return true;
      return !!(r.options && r.options.fnDiscover);
    },

    // Tag-phase release-group requirement. Returns true if validation
    // should reject an empty releaseGroupIds list. Bypassed when:
    //   - there is no Tag phase in the rule (Discover-only rules don't
    //     need a pre-selected RG list — discover scans the library
    //     independent of selection); OR
    //   - Discover IS in the chain AND it's set to write-back AND
    //     auto-activate. In that case the chain runner injects the
    //     newly-discovered group IDs into the overlay before the Tag
    //     phase fires, so an initially-empty list resolves at runtime.
    //     This is the canonical "first-time setup with no groups yet"
    //     flow — must not be blocked at the wizard.
    tagPhaseNeedsGroups() {
      if (!this.ruleAffectsTag()) return false;
      // Filter-only tags every movie passing the quality+audio filter
      // (release group ignored). The RG picker is moot for this path,
      // so the save validator must not require it. Without this guard
      // the user gets "Pick at least one Release Group..." even after
      // explicitly choosing Use filter only on the tag-source step.
      const tagSource = (this.editingRule && this.editingRule.options && this.editingRule.options.tagSource) || '';
      if (tagSource === 'filter-only') return false;
      // Discover-in-chain bypasses the requirement entirely.
      // Rationale (2026-05-05): if user picks "Add + leave disabled"
      // Discover still augments the Active list (just with Enabled
      // off). Tag phase then runs against whatever's currently
      // enabled; if that's empty, the result is a no-op (0 movies
      // tagged), not an error. The user explicitly chose Discover —
      // we trust their intent rather than blocking the run because
      // autoActivate is off. Previously this check required BOTH
      // discoverWriteBack AND autoActivateDiscovered, which produced
      // a misleading "enable Discover with Add + enable" error even
      // when Discover was already enabled with Add + disabled.
      if (this.ruleAffectsDiscover()) return false;
      return true;
    },

    // Inline action for the "no active groups + Discover not in rule"
    // banner on the tag-source step. Adds Discover to the rule chain
    // without forcing the user back to the Basics step. Mode flips to
    // 'combined' when needed; tagSource flips to 'discover' so the
    // freshly-enabled chain is the picked source.
    enableDiscoverFromTagSourceBanner() {
      const r = this.editingRule;
      if (!r) return;
      if (r.mode === 'tag') {
        r.mode = 'combined';
        r.options.combinedModes = ['tag', 'discover'];
      } else if (r.mode === 'combined') {
        if (!Array.isArray(r.options.combinedModes)) r.options.combinedModes = [];
        if (!r.options.combinedModes.includes('discover')) r.options.combinedModes.push('discover');
      }
      this.ensureDiscoverDefaults();
      r.options.tagSource = 'discover';
    },
    ruleAffectsRecover()   {
      const r = this.editingRule; if (!r) return false;
      if (r.mode === 'recover' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('recover'))) return true;
      return !!(r.options && r.options.fnRecover);
    },
    // Webhook-only function affects-helpers — schedule rules never
    // set these so they stay false for that path.
    ruleAffectsSyncToSecondaryFn() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnSyncToSecondary;
    },
    // ruleAffectsAutoTags: any of the three auto-tag sub-flows is
    // selected. Used for cross-cutting gates (e.g. the
    // AutoTagsRunOnSecondary checkbox + Review-step summary).
    ruleAffectsAutoTags() {
      return this.ruleAffectsAudio() || this.ruleAffectsVideo() || this.ruleAffectsDvDetail();
    },
    // ruleAffectsAudio: gates the Audio tags wizard step.
    ruleAffectsAudio() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'audiotags') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('audiotags')) return true;
      return !!(r.options && r.options.fnTagAudio);
    },
    // ruleAffectsVideo: gates the Video tags wizard step (resolution
    // / codec / HDR buckets only — DV detail is its own step now).
    ruleAffectsVideo() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'videotags') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('videotags')) return true;
      return !!(r.options && r.options.fnTagVideo);
    },
    // ruleAffectsDvDetail: gates the DV detail wizard step. Lives
    // on its own fane + step because it requires extra tools and
    // most users won't run it.
    ruleAffectsDvDetail() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'dvdetail') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('dvdetail')) return true;
      return !!(r.options && r.options.fnTagDvDetail);
    },
    // ruleAffectsMissingEpisodes: gates the Missing Episodes wizard
    // step + chain phase. Sonarr-only; the catalog entry's appliesTo
    // makes the checkbox invisible on Radarr Basics, but defence-in-
    // depth keeps the helper returning false if a stale combinedModes
    // value lands on a Radarr rule.
    ruleAffectsMissingEpisodes() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'missingepisodes') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('missingepisodes')) return true;
      return false;
    },
    ruleAffectsTbaRefresh() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'tbarefresh') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('tbarefresh')) return true;
      return false;
    },
    // Read-only one-line summaries for the Review step. Config itself
    // lives on the dedicated Missing episodes / TBA refresh steps.
    reviewMissingEpisodesSummary() {
      const m = this.editingRule && this.editingRule.missingEpisodes;
      if (!m) return '';
      const series = [];
      if (m.includeContinuing) series.push('continuing');
      if (m.includeEnded) series.push('ended');
      if (m.includeSpecials) series.push('specials');
      const actions = [];
      if (m.actionTag) actions.push('tag "' + (m.tagName || 'missing-episodes') + '"');
      if (m.actionSearch) actions.push('search');
      const actionStr = actions.length ? actions.join(' + ') : 'preview only';
      return `${m.thresholdPercent ?? 70}% coverage · ${m.bufferHours ?? 24}h buffer · ${series.join('/') || 'no series picked'} · on apply: ${actionStr}`;
    },
    reviewTbaRefreshSummary() {
      const t = this.editingRule && this.editingRule.tbaRefresh;
      if (!t) return '';
      const series = [];
      if (t.includeContinuing) series.push('continuing');
      if (t.includeEnded) series.push('ended');
      if (t.includeSpecials) series.push('specials');
      return `${series.join('/') || 'no series picked'} · renames every TBA file found on apply`;
    },

    // ---- Wizard "Sync to Plex" step ------------------------------
    // All bound to editingRule.plexSync. Mirrors the one-off form's
    // picker logic but on isolated wizardPlex* state so the verified
    // one-off flow is untouched. The Arr instance is the wizard's
    // editingRule.instanceId (locked in the Basics step).
    ruleAffectsPlexSync() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'plexsync') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('plexsync')) return true;
      return false;
    },
    async wizardPlexLoadTags() {
      const r = this.editingRule;
      // The schedule / QFA wizard can open on a page that never loaded
      // the Plex server list (it's loaded per-page, like the webhooks
      // page). Without this the step shows a false "No Plex servers
      // configured" banner. Pull it on step entry if it's empty.
      if (!this.plexInstances || this.plexInstances.length === 0) {
        await this.loadPlexInstances();
      }
      if (!r || !r.instanceId) { this.wizardPlexAvailableTags = []; return; }
      this.wizardPlexTagsLoading = true;
      this.wizardPlexTagsError = '';
      try {
        const resp = await this.apiFetch('/api/instances/' + r.instanceId + '/tags');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const list = await resp.json();
        const out = (Array.isArray(list) ? list : [])
          .map(t => ({ label: (t.label || '').trim(), usageCount: t.usageCount || 0 }))
          .filter(t => t.label);
        out.sort((a, b) => a.label.localeCompare(b.label));
        this.wizardPlexAvailableTags = out;
      } catch (e) {
        this.wizardPlexTagsError = e.message;
        this.wizardPlexAvailableTags = [];
      } finally {
        this.wizardPlexTagsLoading = false;
      }
    },
    wizardPlexFilteredTags() {
      return this.filterTagsByTerms(this.wizardPlexAvailableTags, this.wizardPlexTagFilter);
    },
    wizardPlexHasLabel(label) {
      const ps = this.editingRule && this.editingRule.plexSync;
      return ps ? (ps.labels || []).some(l => l.toLowerCase() === label.toLowerCase()) : false;
    },
    wizardPlexToggleTag(label) {
      const ps = this.editingRule.plexSync;
      const idx = ps.labels.findIndex(l => l.toLowerCase() === label.toLowerCase());
      if (idx === -1) {
        ps.labels.push(label);
      } else {
        const removed = ps.labels[idx];
        ps.labels.splice(idx, 1);
        if (ps.labelDisplay && removed in ps.labelDisplay) delete ps.labelDisplay[removed];
      }
    },
    wizardPlexSetDisplay(arrTag, value) {
      const ps = this.editingRule.plexSync;
      if (!ps.labelDisplay) ps.labelDisplay = {};
      const t = (value || '').trim();
      if (!t || t === arrTag) delete ps.labelDisplay[arrTag];
      else ps.labelDisplay[arrTag] = t;
    },
    wizardPlexEffectiveLabel(arrTag) {
      const m = (this.editingRule.plexSync && this.editingRule.plexSync.labelDisplay) || {};
      const v = m[arrTag];
      if (v && v.trim() && v.trim() !== arrTag) return v.trim();
      return arrTag;
    },
    wizardPlexIsCustomLabel(label) {
      if (this.wizardPlexTagsLoading) return false;
      if (!this.wizardPlexAvailableTags || this.wizardPlexAvailableTags.length === 0) return false;
      return !this.wizardPlexAvailableTags.some(t => t.label.toLowerCase() === label.toLowerCase());
    },
    wizardPlexWantedLibType() {
      const t = this.ruleEditorInstanceType();
      if (t === 'radarr') return 'movie';
      if (t === 'sonarr') return 'show';
      return '';
    },
    wizardPlexSelectedLibraries() {
      const ps = this.editingRule && this.editingRule.plexSync;
      if (!ps || !ps.plexInstanceId) return [];
      const pi = (this.plexInstances || []).find(p => p.id === ps.plexInstanceId);
      return pi ? (pi.libraries || []) : [];
    },
    wizardPlexVisibleLibraryCount() {
      const want = this.wizardPlexWantedLibType();
      const libs = this.wizardPlexSelectedLibraries();
      return want ? libs.filter(l => l.type === want).length : libs.length;
    },
    wizardPlexToggleLibrary(key) {
      const ps = this.editingRule.plexSync;
      const idx = ps.libraryKeys.indexOf(key);
      if (idx === -1) ps.libraryKeys.push(key);
      else ps.libraryKeys.splice(idx, 1);
    },
    wizardPlexToggleTarget(t) {
      const ps = this.editingRule.plexSync;
      if (!ps.targetTypes) ps.targetTypes = [];
      const idx = ps.targetTypes.indexOf(t);
      if (idx === -1) ps.targetTypes.push(t);
      else ps.targetTypes.splice(idx, 1);
    },
    wizardPlexHasTarget(t) {
      const ps = this.editingRule && this.editingRule.plexSync;
      return ps ? (ps.targetTypes || []).includes(t) : false;
    },
    // Derived rule mode: when no quality / audio master is on, the
    // engine's CheckQuality + CheckAudio short-circuit to true, which
    // makes every filtered-group match tag — same outcome as simple
    // mode. So we present this as a single rule-level concept rather
    // than asking the user to think per-group.
    //
    // Returns 'simple' / 'filtered' / null. null = the rule doesn't
    // touch a tagging phase, so the badge is meaningless.
    ruleEffectiveMode() {
      const r = this.editingRule;
      if (!r) return null;
      if (!this.ruleAffectsTag() && !this.ruleAffectsDiscover()) return null;
      const f = r.filters || {};
      return (f.Quality || f.Audio) ? 'filtered' : 'simple';
    },
    // Same derivation but for an arbitrary saved schedule (list view).
    // Schedules with both masters off OR not affecting Tag/Discover get
    // 'simple'; the Tag-touching ones with any filter on get 'filtered'.
    scheduleEffectiveMode(sj) {
      if (!sj) return null;
      const touchesTag = sj.mode === 'tag' || (sj.mode === 'combined' && (sj.options?.combinedModes || []).includes('tag'));
      const touchesDiscover = sj.mode === 'discover' || (sj.mode === 'combined' && (sj.options?.combinedModes || []).includes('discover'));
      if (!touchesTag && !touchesDiscover) return null;
      const f = sj.filters || {};
      return (f.Quality || f.Audio) ? 'filtered' : 'simple';
    },
    // Tab visibility: which sections show up in tabbed-edit + wizard.
    // RG + Filters apply to Tag and Discover phases; Extra tags is its
    // own switch. Recover-only / cleanup-only rules show only Basics.
    ruleEditorTabVisible(tabId) {
      if (tabId === 'basics')   return true;
      const isWebhook = this.ruleEditorIsWebhook();
      if (tabId === 'rg')       return this.ruleAffectsTag() || this.ruleAffectsDiscover();
      if (tabId === 'filters')  return this.ruleAffectsTag() || this.ruleAffectsDiscover();
      if (tabId === 'audio')    return this.ruleAffectsAudio();
      if (tabId === 'video')    return this.ruleAffectsVideo();
      if (tabId === 'dvdetail') return this.ruleAffectsDvDetail();
      // Missing episodes + TBA refresh — Sonarr-only phases, each with
      // its own page like Audio/Video/DV. Schedule/QFA only (not
      // webhook). Config lives on the step; Review shows a summary.
      if (tabId === 'missingepisodes') return !isWebhook && this.ruleAffectsMissingEpisodes();
      if (tabId === 'tbarefresh')      return !isWebhook && this.ruleAffectsTbaRefresh();
      // Webhook-only steps. Hidden for schedule rules entirely; for
      // webhook rules visible only when the corresponding function is
      // ticked. Backend's webhook rule validate-then-persist requires
      // the matching criteria/rules struct, so the step appears
      // automatically the moment the user picks the function on
      // Basics — same affects-X gate logic the other steps use.
      if (tabId === 'grabrename') return isWebhook && this.ruleAffectsGrabRename();
      if (tabId === 'qbitse')     return isWebhook && this.ruleAffectsQbitSe();
      if (tabId === 'qbitcategoryfix') return isWebhook && this.ruleAffectsQbitCategoryFix();
      if (tabId === 'plexlabelsync') return isWebhook && this.ruleAffectsPlexLabelSync();
      // Plex sync — dedicated step for schedule + QFA rules (combined
      // phase). Distinct from the webhook-only 'plexlabelsync' step:
      // schedules carry the config on editingRule.plexSync, webhooks on
      // editingRule.plexLabelSync. Hidden for webhook rules.
      if (tabId === 'plexsync') return !isWebhook && this.ruleAffectsPlexSync();
      // Schedule step: schedule rules only (cron-driven, not Manual
      // and not quickfix). Webhook rules trigger on Connect events,
      // not cron — the step hides entirely for kind='webhook'.
      if (tabId === 'schedule') {
        if (isWebhook) return false;
        return !this.ruleEditor.isQuickFix && !!this.editingRule && !this.editingRule.manualOnly;
      }
      return false;
    },

    // Convenience: the editor is in webhook-rule mode when ruleEditor.
    // kind is explicitly 'webhook'. Empty / 'schedule' / undefined all
    // route to the legacy schedule-rule flow so existing entry points
    // (Create rule, QFA, per-action wizards) keep working unchanged.
    ruleEditorIsWebhook() {
      return !!(this.ruleEditor && this.ruleEditor.kind === 'webhook');
    },

    // True when the webhook-mode rule has at least one function ticked.
    // Mirrors webhookWizardAnyFunctionTicked but reads off editingRule
    // instead of the now-deprecated webhookWizard.fn* fields. Used by
    // the Basics-step warning + Save-time validation.
    webhookRuleAnyFunctionTicked() {
      const o = (this.editingRule && this.editingRule.options) || {};
      if (o.fnTagReleaseGroups || o.fnDiscover || o.fnTagAudio || o.fnTagVideo
          || o.fnTagDvDetail || o.fnRecover || o.fnSyncToSecondary
          || o.fnGrabRename || o.fnQbitSeTag || o.fnQbitCategoryFix
          || o.fnPlexLabelSync) {
        return true;
      }
      // Per-bucket strip-on-delete also counts as a rule action — a
      // rule that only strips audio tags on file delete with no other
      // function is valid (the dispatcher's FiresPerBucketStripOnDelete
      // gate enters the rule on delete events regardless of Functions).
      return this.webhookRuleHasBucketStripOnDelete();
    },

    // True when any bucket snapshot on the editing rule has
    // stripOnFileDelete=true. Used by the Basics-step gate +
    // webhookEventsForCurrentRule's delete-event surfacing.
    webhookRuleHasBucketStripOnDelete() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.audioTags && r.audioTags.stripOnFileDelete) return true;
      if (r.videoTags && r.videoTags.stripOnFileDelete) return true;
      // DV-detail is Radarr-only — the snapshot may exist on Sonarr
      // rules too via the schedule editor's shared shape, but the
      // dispatcher gates it. Treat flag-present as "wants delete
      // trigger surfaced" for consistency; webhookEventsForFunctions
      // applies the AppType gate below.
      if (r.dvDetail && r.dvDetail.stripOnFileDelete) return true;
      return false;
    },

    // Entry point for the Webhooks page +Add rule button. Opens the
    // unified rule editor in webhook mode against the picked instance.
    // Mirrors openCreateRuleWizard's shape but seeds editingRule with
    // a webhook-rule skeleton + sets ruleEditor.kind = 'webhook' so the
    // Basics step + visibleSteps + save flow branch correctly.
    openWebhookRuleEditor(instanceId) {
      if (!instanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      const inst = (this.instances || []).find(i => i.id === instanceId);
      if (!inst) {
        this.showToast('Instance not found — refresh the page', 'error');
        return;
      }
      // Webhook rules don't have a mode-string the way schedule rules
      // do (mode='tag'/'discover'/'combined'/etc. drives the schedule
      // chain). For the webhook editor mode='webhook' is a sentinel
      // value the editor recognises but the backend doesn't read —
      // backend uses Functions[] directly. Empty options.combinedModes
      // keeps the schedule-mode helpers correctly returning false.
      this.editingRule = {
        id: '',
        name: '',
        mode: 'webhook',
        instanceId,
        enabled: true,
        manualOnly: true, // keeps schedule-step hidden via the existing manualOnly gate
        options: {
          // Function flags — start clean, user picks on Basics.
          fnTagReleaseGroups: false,
          fnDiscover:         false,
          fnTagAudio:         false,
          fnTagVideo:         false,
          fnTagDvDetail:      false,
          fnRecover:          false,
          fnSyncToSecondary:  false,
          fnGrabRename:       false,
          fnQbitSeTag:        false,
          fnQbitCategoryFix:  false,
          fnPlexLabelSync:    false,
          // Per-action shared options the schedule editor seeds —
          // mirrored here so the per-step UIs (RG / Filters / Audio /
          // Video / DV) read the same shape and don't NaN on undefined.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          discoverWriteBack: false,
          discoverScanSecondary: false,
          autoActivateDiscovered: false,
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // M-Webhook notification kill-switch — default off so new
          // rules stay silent until the user opts in. Eligible agents
          // + their Functions filter handle the rest (Settings →
          // Notifications).
          notifyOnFire: false,
        },
        filters:         this.snapshotGlobalFilters(),
        audioTags:       this.snapshotGlobalAudioTags(),
        videoTags:       this.snapshotGlobalVideoTags(),
        dvDetail:        this.snapshotGlobalDvDetail(),
        releaseGroupIds: this.snapshotGlobalRGIds(instanceId),
        // Seed Grab Rename + qBit S/E + qBit Category Fix criteria with
        // sensible defaults. All three are pre-populated even when their
        // fn flag is off so the user can tick the function on Basics and
        // immediately have a hydrated form on Step 3b / 3c / 3d without
        // defensive null-checks sprinkled through the templates. Save
        // flow only sends them when the matching fn is enabled. Shape is
        // shared with normalizeWebhookRuleClientShape (see below) — if
        // you add a field here, mirror it in defaultWebhookRuleStructs()
        // so old rules loaded from the server backfill cleanly.
        ...this.defaultWebhookRuleStructs(),
      };
      this.ruleEditor = {
        open: true,
        isCreate: true,
        isQuickFix: false,
        step: 0,
        activeTab: 'basics',
        appType: inst.type,
        kind: 'webhook',
        busy: false,
        error: '',
        cronError: '',
        nextFires: [],
      };
    },

    // openEditWebhookRuleModal opens the rule editor in edit-mode for
    // an existing webhook rule. Mirror of openEditRuleModal (schedule
    // rules) but with webhook-shape hydration:
    //
    //   - Server rule's `functions[]` array is unpacked into the
    //     wizard's `options.fn*` flags (the wizard binds checkboxes
    //     to fnTagAudio / fnTagVideo / etc., not directly to the
    //     functions array)
    //   - Top-level rule fields (tagSource, filterOnlyTag,
    //     syncToInstanceId, discoverAutoEnable) are hoisted into
    //     `options.*` so the wizard's per-step bindings see them
    //     under the same keys as create-flow
    //   - Snapshots (filters / audioTags / videoTags / dvDetail /
    //     missingEpisodes / releaseGroupIds) are backfilled from
    //     globals when nil — defence against legacy rules saved
    //     before per-rule snapshots landed
    //   - Struct-typed fields (grabRename / qbitSe / qbitCategoryFix)
    //     get default-shape backfill via normalizeWebhookRuleClientShape
    //     so x-model bindings don't throw on undefined
    //
    // Locks appType to the existing rule's instance type — editing a
    // Radarr rule never exposes Sonarr instances in the dropdown
    // (cross-type "edit" is effectively a different rule; user must
    // delete + recreate). Same constraint openEditRuleModal applies.
    openEditWebhookRuleModal(rule) {
      if (!rule || !rule.id) {
        this.showToast('Cannot edit: rule has no id', 'error');
        return;
      }
      const inst = (this.instances || []).find(i => i.id === rule.instanceId);
      if (!inst) {
        this.showToast('Rule references a missing instance — refresh the page or fix the instance under Settings', 'error');
        return;
      }
      // Deep-copy so wizard edits don't mutate the cached
      // webhookRules entry until Save commits.
      let copy = JSON.parse(JSON.stringify(rule));
      // Backfill struct-typed fields (grabRename / qbitSe /
      // qbitCategoryFix) for legacy rules that pre-date them.
      copy = this.normalizeWebhookRuleClientShape(copy);
      // Webhook editor uses mode='webhook' as a sentinel — the
      // backend doesn't read it (function dispatch is via
      // Functions[]) but the wizard's manualOnly + isWebhook gates
      // depend on the shape.
      copy.mode = 'webhook';
      copy.manualOnly = true;
      // Backfill snapshots from globals so per-step UIs always have
      // a non-nil object to bind to. Legacy rules saved before
      // per-rule snapshots may have nil here.
      if (!copy.filters)         copy.filters         = this.snapshotGlobalFilters();
      if (!copy.audioTags)       copy.audioTags       = this.snapshotGlobalAudioTags();
      if (!copy.videoTags)       copy.videoTags       = this.snapshotGlobalVideoTags();
      if (!copy.dvDetail)        copy.dvDetail        = this.snapshotGlobalDvDetail();
      if (!copy.missingEpisodes) copy.missingEpisodes = this.snapshotGlobalMissingEpisodes();
      if (!copy.plexSync)        copy.plexSync        = this.snapshotDefaultPlexSync();
      if (!copy.tbaRefresh)      copy.tbaRefresh      = this.snapshotDefaultTbaRefresh();
      if (!Array.isArray(copy.releaseGroupIds)) copy.releaseGroupIds = this.snapshotGlobalRGIds(copy.instanceId);

      // Hoist server-shape rule fields into options.* so the
      // wizard's per-step bindings find them under the same keys
      // create-flow uses. Save flow at saveWebhookRuleEditor reads
      // back from options.* and emits to the right top-level keys.
      const fnSet = new Set((copy.functions || []).map(s => String(s)));
      copy.options = Object.assign({
        // Function flags — defaults match openWebhookRuleEditor.
        fnTagReleaseGroups: false,
        fnDiscover:         false,
        fnTagAudio:         false,
        fnTagVideo:         false,
        fnTagDvDetail:      false,
        fnRecover:          false,
        fnSyncToSecondary:  false,
        fnGrabRename:       false,
        fnQbitSeTag:        false,
        fnQbitCategoryFix:  false,
        fnPlexLabelSync:    false,
        // Per-action shared options.
        tagSource: '',
        filterOnlyTag: 'lossless-web',
        cleanupUnusedTags: false,
        syncToSecondary: false,
        syncToInstanceId: '',
        discoverWriteBack: false,
        discoverScanSecondary: false,
        autoActivateDiscovered: false,
        audioTagsTarget: 'primary',
        videoTagsTarget: 'primary',
        dvDetailTarget:  'primary',
        recoverTarget:   'primary',
        notifyOnFire:    false,
      }, copy.options || {});
      // Unpack functions[] → fn* flags. Server-side stores the
      // canonical function-name strings; wizard binds checkboxes to
      // fn* booleans. Mapping mirrors saveWebhookRuleEditor's
      // inverse fnList.push branch.
      copy.options.fnTagReleaseGroups = fnSet.has('tagReleaseGroups');
      copy.options.fnDiscover         = fnSet.has('discover');
      copy.options.fnTagAudio         = fnSet.has('tagAudio');
      copy.options.fnTagVideo         = fnSet.has('tagVideo');
      copy.options.fnTagDvDetail      = fnSet.has('tagDvDetail');
      copy.options.fnRecover          = fnSet.has('recover');
      copy.options.fnSyncToSecondary  = fnSet.has('syncToSecondary');
      copy.options.fnGrabRename       = fnSet.has('grabRename');
      copy.options.fnQbitSeTag        = fnSet.has('qbitSeTag');
      copy.options.fnQbitCategoryFix  = fnSet.has('qbitCategoryFix');
      copy.options.fnPlexLabelSync    = fnSet.has('plexLabelSync');
      // Hoist top-level rule fields that the wizard binds via
      // options.*. The save flow re-emits these as top-level keys
      // on PUT (saveWebhookRuleEditor handles the inverse mapping
      // for tagSource / filterOnlyTag / syncToInstanceId).
      if (typeof copy.tagSource === 'string') copy.options.tagSource = copy.tagSource;
      if (typeof copy.filterOnlyTag === 'string') copy.options.filterOnlyTag = copy.filterOnlyTag || copy.options.filterOnlyTag;
      if (typeof copy.syncToInstanceId === 'string') copy.options.syncToInstanceId = copy.syncToInstanceId;
      if (typeof copy.discoverAutoEnable === 'boolean') copy.options.autoActivateDiscovered = copy.discoverAutoEnable;
      if (typeof copy.cleanupUnusedTags === 'boolean') copy.options.cleanupUnusedTags = copy.cleanupUnusedTags;
      // discoverWriteBack is the gate the radio-button :checked
      // expressions look at — both options ("leave disabled" and
      // "add and enable") only render as checked when it's true.
      // Server-side, having WebhookFnDiscover in Functions implies
      // we WILL write discovered groups back to the Active list
      // (that's what the function does), so on edit-open we set it
      // to true whenever Discover is enabled. Without this, opening
      // edit on a rule with Discover would show neither radio
      // selected — the user couldn't tell which mode the rule was
      // saved in until they re-clicked.
      if (fnSet.has('discover')) {
        copy.options.discoverWriteBack = true;
      }
      if (typeof copy.syncSkipOrphanCleanup === 'boolean') copy.options.syncSkipOrphanCleanup = copy.syncSkipOrphanCleanup;
      if (typeof copy.notifyOnFire === 'boolean') copy.options.notifyOnFire = copy.notifyOnFire;

      this.editingRule = copy;
      this.ruleEditor = {
        open: true,
        isCreate: false,
        isQuickFix: false,
        step: 0,
        activeTab: 'basics',
        appType: inst.type,
        kind: 'webhook',
        busy: false,
        error: '',
        cronError: '',
        nextFires: [],
      };
    },

    // defaultWebhookRuleStructs is the single source of truth for the
    // default shape of struct-typed webhook-rule fields. Used by:
    //
    //   - openWebhookRuleEditor — seeds fresh rules.
    //   - normalizeWebhookRuleClientShape — backfills legacy rules
    //     loaded from /api/webhook-rules that pre-date the addition of
    //     a struct field.
    //
    // When adding a new struct-shaped rule field: add its default here
    // FIRST, then reference via defaultWebhookRuleStructs()[fieldName]
    // wherever a fresh-default is needed. Keeps create + edit + load
    // paths from drifting apart silently.
    defaultWebhookRuleStructs() {
      return {
        grabRename: {
          renameTarget: 'torrent',
          triggerOnMissingReleaseGroup: true,
          triggerOnMovieVersionMismatch: false,
          triggerOnSourceMismatch: false,
          triggerOnAudioMismatch: false,
          triggerOnSceneMismatch: false,
          triggerAlways: false,
          customTokens: [],
          groupBlocklist: [],
          qbitInstanceId: '',
        },
        qbitSe: {
          // Three-rule first-match-wins model — mirror of community
          // qbittorrent_auto_tagger.py. All three rules seeded ON so a
          // brand-new rule produces tags out of the box.
          qbitInstanceId: '',
          episodeEnabled: true,  episodeTag: 'Episode',
          seasonEnabled: true,   seasonTag: 'Season',
          unmatchedEnabled: true, unmatchedTag: 'Unmatched',
          // M-qBit-add Slice 6 — qBit-add debounce window (only
          // affects the qBit-side webhook path; Sonarr Connect Grab
          // events fire instantly regardless). 60s is a good default
          // for typical cross-seed bursts.
          aggregationWindowSeconds: 60,
        },
        // qBit Category Fix — defensive reconcile of stuck pre-import
        // categories. Snapshot fields filled in by autoFillQbitCategories
        // once the user picks a download client from the live Arr list.
        qbitCategoryFix: {
          qbitInstanceId: '',
          arrDownloadClientId: 0,
          preImportCategorySnapshot: '',
          postImportCategorySnapshot: '',
        },
        // Plex label sync — inline per-rule config. Same field shape
        // as standalone PlexLabelRule's Plex-side bits (instance +
        // libraries + labels + display + targetTypes). Seeded empty;
        // user populates via the Plex sync wizard step when the
        // function is enabled.
        plexLabelSync: {
          plexInstanceId: '',
          libraryKeys: [],
          labels: [],
          labelDisplay: {},
          targetTypes: ['label'],
        },
      };
    },

    // normalizeWebhookRuleClientShape backfills struct-typed fields that
    // a saved rule may pre-date. The Go side encodes nil pointers as
    // missing JSON keys (omitempty), so a rule saved before the qBit
    // Category Fix landed would deserialise with rule.qbitCategoryFix
    // === undefined. The wizard's Alpine bindings (x-model on
    // editingRule.qbitCategoryFix.qbitInstanceId etc.) would throw on
    // undefined. Run every server-sourced rule through this helper before
    // assigning it to editingRule.
    //
    // Mutates + returns the same object for ergonomic chaining.
    normalizeWebhookRuleClientShape(rule) {
      if (!rule || typeof rule !== 'object') return rule;
      const defaults = this.defaultWebhookRuleStructs();
      for (const key of Object.keys(defaults)) {
        if (rule[key] == null) {
          rule[key] = JSON.parse(JSON.stringify(defaults[key]));
        }
      }
      // M-qBit-add Slice 6 — field-level backfill for legacy rules
      // that pre-date AggregationWindowSeconds. Server treats stored
      // 0 as "use default 60" but UI input accepts 1..3600 only, so
      // pre-fill 60 for an unambiguous edit experience.
      if (rule.qbitSe && (rule.qbitSe.aggregationWindowSeconds == null || rule.qbitSe.aggregationWindowSeconds === 0)) {
        rule.qbitSe.aggregationWindowSeconds = 60;
      }
      return rule;
    },

    // Function-affects helpers for the new webhook-only steps. Mirror
    // the ruleAffects{Tag,Audio,Video,DvDetail,Discover}() pattern but
    // read off editingRule.options.fn* (set by the Basics step's
    // function checkboxes). Schedule rules never have these flags so
    // the helpers correctly return false there.
    ruleAffectsGrabRename() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnGrabRename;
    },
    ruleAffectsQbitSe() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnQbitSeTag;
    },
    ruleAffectsQbitCategoryFix() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnQbitCategoryFix;
    },
    ruleAffectsPlexLabelSync() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnPlexLabelSync;
    },

    // ---- Plex label sync wizard step helpers ----
    // Mirror the standalone PlexLabelRule modal helpers but operate
    // on editingRule.plexLabelSync (the inline webhook-rule config)
    // instead of plexLabelRuleModal. Tag-list state
    // (plexLabelRuleAvailableTags + filter + loading + error) is
    // shared with the standalone modal since the two surfaces are
    // mutually exclusive (you can't have both open at once).

    plexLabelSyncWizardLoadTags() {
      // Reuse the standalone modal's fetch state — repoint
      // plexLabelRuleModal.instanceId so plexLabelRuleLoadAvailableTags
      // hits the right Arr. The modal isn't open; we're piggybacking
      // on its tag-list state holders.
      this.plexLabelRuleModal.instanceId = (this.editingRule && this.editingRule.instanceId) || '';
      this.plexLabelRuleAvailableTags = [];
      this.plexLabelRuleTagsError = '';
      this.plexLabelRuleTagFilter = '';
      if (this.plexLabelRuleModal.instanceId) {
        this.plexLabelRuleLoadAvailableTags();
      }
    },

    plexLabelSyncWizardSelectedPlexLibraries() {
      const r = this.editingRule;
      if (!r || !r.plexLabelSync || !r.plexLabelSync.plexInstanceId) return [];
      const pi = (this.plexInstances || []).find(p => p.id === r.plexLabelSync.plexInstanceId);
      if (!pi) return [];
      return pi.libraries || [];
    },

    plexLabelSyncWizardWantedLibraryType() {
      const r = this.editingRule;
      if (!r) return '';
      if (r.appType === 'radarr') return 'movie';
      if (r.appType === 'sonarr') return 'show';
      // Fallback — read off the parent rule's linked Arr instance
      // (in case appType wasn't denormalised yet).
      const inst = (this.instances || []).find(i => i.id === (r && r.instanceId));
      if (inst) {
        if (inst.type === 'radarr') return 'movie';
        if (inst.type === 'sonarr') return 'show';
      }
      return '';
    },

    plexLabelSyncWizardVisibleLibraryCount() {
      const libs = this.plexLabelSyncWizardSelectedPlexLibraries();
      const want = this.plexLabelSyncWizardWantedLibraryType();
      if (!want) return libs.length;
      return libs.filter(l => l.type === want).length;
    },

    plexLabelSyncWizardNoLibrariesMessage() {
      const want = this.plexLabelSyncWizardWantedLibraryType();
      if (!want) return 'Pick an Arr instance on the Basics step so the library picker can filter to the matching type.';
      if (want === 'movie') return 'No movie libraries cached on this Plex instance. Add a Movies library in Plex, then click Fetch libraries on the Plex row in Settings.';
      return 'No show libraries cached on this Plex instance. Add a TV Shows library in Plex, then click Fetch libraries on the Plex row in Settings.';
    },

    plexLabelSyncWizardLibraryHint() {
      const want = this.plexLabelSyncWizardWantedLibraryType();
      if (want === 'movie') return 'Only movie libraries shown — Radarr rules can only manage Plex labels on movie libraries.';
      if (want === 'show') return 'Only show libraries shown — Sonarr rules can only manage Plex labels on show libraries.';
      return 'Pick the Plex libraries this rule will sync labels into.';
    },

    plexLabelSyncWizardToggleLibrary(key) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!Array.isArray(cfg.libraryKeys)) cfg.libraryKeys = [];
      const idx = cfg.libraryKeys.indexOf(key);
      if (idx === -1) cfg.libraryKeys.push(key);
      else cfg.libraryKeys.splice(idx, 1);
    },

    plexLabelSyncWizardToggleTag(label) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!Array.isArray(cfg.labels)) cfg.labels = [];
      const idx = cfg.labels.findIndex(l => l.toLowerCase() === label.toLowerCase());
      if (idx === -1) {
        cfg.labels.push(label);
      } else {
        const removed = cfg.labels[idx];
        cfg.labels.splice(idx, 1);
        // Drop matching display override so unchecking + re-checking
        // gives a clean slate (consistent with standalone modal).
        if (cfg.labelDisplay && removed in cfg.labelDisplay) {
          delete cfg.labelDisplay[removed];
        }
      }
    },

    plexLabelSyncWizardSetDisplay(arrTag, value) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!cfg.labelDisplay) cfg.labelDisplay = {};
      const trimmed = (value || '').trim();
      if (!trimmed || trimmed === arrTag) {
        delete cfg.labelDisplay[arrTag];
      } else {
        cfg.labelDisplay[arrTag] = trimmed;
      }
    },

    plexLabelSyncWizardToggleTargetType(t) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!Array.isArray(cfg.targetTypes)) cfg.targetTypes = [];
      const idx = cfg.targetTypes.indexOf(t);
      if (idx === -1) cfg.targetTypes.push(t);
      else cfg.targetTypes.splice(idx, 1);
    },

    // ---- qBit Category Fix helpers ----
    //
    // Loads the Arr's download-client list on demand so the user picks
    // a download client by name (not ID) and the pre/post category
    // names auto-populate from Sonarr/Radarr's own config. Cached
    // backend-side for 5min; forceRefresh=true invalidates that cache
    // so a user who just edited a category in Sonarr/Radarr UI can
    // see the change immediately.

    arrDownloadClients: [],
    arrDownloadClientsLoading: false,
    arrDownloadClientsError: '',

    async loadArrDownloadClients(arrInstanceId, forceRefresh) {
      if (!arrInstanceId) return;
      this.arrDownloadClientsLoading = true;
      this.arrDownloadClientsError = '';
      try {
        const qs = forceRefresh ? '?refresh=1' : '';
        const r = await this.apiFetch('/api/instances/' + arrInstanceId + '/download-clients' + qs);
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const data = await r.json();
        // Filter to qBit implementations only — other download-client
        // types (sabnzbd, nzbget, deluge) don't use the qBit category
        // pre/post model.
        this.arrDownloadClients = (Array.isArray(data) ? data : [])
          .filter(c => c.implementation && c.implementation.toLowerCase().includes('qbittorrent'));
        // Auto-select when there's exactly one + nothing picked yet,
        // so the typical single-qBit user lands with a working form
        // immediately on entering the step.
        const cf = this.editingRule && this.editingRule.qbitCategoryFix;
        if (cf && this.arrDownloadClients.length === 1 && !cf.arrDownloadClientId) {
          cf.arrDownloadClientId = this.arrDownloadClients[0].id;
          this.autoFillQbitCategories();
        }
      } catch (e) {
        this.arrDownloadClientsError = 'Failed to load download clients: ' + (e.message || e);
        this.arrDownloadClients = [];
      } finally {
        this.arrDownloadClientsLoading = false;
      }
    },

    // Pulls the pre/post category names out of the picked download
    // client and copies them onto the rule's snapshot fields. Called
    // on @change of the select + on auto-select.
    autoFillQbitCategories() {
      const cf = this.editingRule && this.editingRule.qbitCategoryFix;
      if (!cf || !cf.arrDownloadClientId) return;
      const dc = this.arrDownloadClients.find(c => c.id === cf.arrDownloadClientId);
      if (!dc) return;
      cf.preImportCategorySnapshot = dc.qbitPreCat || '';
      cf.postImportCategorySnapshot = dc.qbitPostCat || '';
    },

    // Inline-warning string for the qBit Category Fix step. Empty
    // string means the form is fillable / advancable; non-empty
    // returns a short message the template renders + the Next button
    // uses to gate advance.
    qbitCategoryFixWarning() {
      const cf = this.editingRule && this.editingRule.qbitCategoryFix;
      if (!cf) return '';
      if (!cf.qbitInstanceId) return 'Pick a qBit instance.';
      if (!cf.arrDownloadClientId) return 'Pick a download client from Sonarr/Radarr.';
      if (!cf.preImportCategorySnapshot || !cf.postImportCategorySnapshot) {
        return 'The selected download client doesn\'t have both Pre-import and Post-import categories configured. Set them in Sonarr/Radarr → Settings → Download Clients first.';
      }
      if (cf.preImportCategorySnapshot === cf.postImportCategorySnapshot) {
        return 'Pre-import and post-import categories must differ.';
      }
      return '';
    },

    // True when none of the three classifier rules (Episode / Season /
    // Unmatched) is enabled — backend would reject save with "must
    // enable at least one of episodeEnabled / seasonEnabled /
    // unmatchedEnabled". Surfaces inline on Step 3c so the user catches
    // it before clicking Save / Next.
    qbitSeNoRuleEnabled() {
      const q = (this.editingRule && this.editingRule.qbitSe) || {};
      return !q.episodeEnabled && !q.seasonEnabled && !q.unmatchedEnabled;
    },

    // Tag-name regex check — mirrors backend's reTagName pattern
    // (^[a-z0-9][a-z0-9_-]*$). Case-insensitive on input because
    // Radarr's API is strict (all-lowercase) but qBit accepts any
    // casing; backend lowercases the user's input before validating.
    // Empty / whitespace returns true (the validator backfills the
    // default name on save when the field is blank).
    qbitSeTagNameValid(name) {
      const trimmed = String(name || '').trim();
      if (trimmed === '') return true;
      return /^[a-z0-9][a-z0-9_-]*$/i.test(trimmed);
    },

    // ---- Grab Rename criteria editor helpers ----
    //
    // All read off editingRule.grabRename, which openWebhookRuleEditor
    // seeds with default values. Defensive ?? || fallbacks keep the
    // helpers safe even if a future entry path forgets to seed.

    // True when ALL six built-in trigger flags are off AND no custom
    // tokens are defined — backend would reject save with "must enable
    // at least one trigger". Surfaces inline on Step 3b so the user
    // catches it before clicking Save.
    grabRenameNoTriggerSelected() {
      const c = (this.editingRule && this.editingRule.grabRename) || {};
      if (c.triggerOnMissingReleaseGroup) return false;
      if (c.triggerOnMovieVersionMismatch) return false;
      if (c.triggerOnSourceMismatch) return false;
      if (c.triggerOnAudioMismatch) return false;
      if (c.triggerOnSceneMismatch) return false;
      if (c.triggerAlways) return false;
      if ((c.customTokens || []).length > 0) return false;
      return true;
    },

    // Group blocklist mutators — bind via @click / @input bindings on
    // Step 3b. Direct array push/splice is fine; Alpine reactivity
    // tracks modifications to arrays-on-objects.
    addGrabRenameBlocklist() {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c) return;
      if (!Array.isArray(c.groupBlocklist)) c.groupBlocklist = [];
      c.groupBlocklist.push('');
    },
    removeGrabRenameBlocklist(idx) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.groupBlocklist)) return;
      c.groupBlocklist.splice(idx, 1);
    },
    updateGrabRenameBlocklist(idx, val) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.groupBlocklist)) return;
      c.groupBlocklist[idx] = val;
    },

    // Custom-token mutators — Label:regex pairs. Server-side regex
    // compile is the load-bearing validation; the client try-compile
    // (grabRenameRegexInvalid) catches obvious typos before save.
    addGrabRenameCustomToken() {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c) return;
      if (!Array.isArray(c.customTokens)) c.customTokens = [];
      c.customTokens.push({ label: '', regex: '' });
    },
    removeGrabRenameCustomToken(idx) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.customTokens)) return;
      c.customTokens.splice(idx, 1);
    },
    updateGrabRenameCustomTokenLabel(idx, val) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.customTokens) || !c.customTokens[idx]) return;
      c.customTokens[idx].label = val;
    },
    updateGrabRenameCustomTokenRegex(idx, val) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.customTokens) || !c.customTokens[idx]) return;
      c.customTokens[idx].regex = val;
    },

    // Returns true when the JS regex engine rejects the source — best-
    // effort client-side check. Server's RE2 engine is the truth source;
    // an empty regex returns false (not invalid, just unset — a separate
    // "regex is required" check covers that on save).
    grabRenameRegexInvalid(regex) {
      if (!regex || !regex.trim()) return false;
      try {
        new RegExp(regex);
        return false;
      } catch (e) {
        return true;
      }
    },
    ruleEditorVisibleTabs()  { return this.ruleEditorTabs.filter(t => this.ruleEditorTabVisible(t.id)); },
    ruleEditorVisibleSteps() {
      // 'review' is always last; others share visibility with their tab.
      return this.ruleEditorSteps.filter(s => s === 'review' || s === 'basics' || this.ruleEditorTabVisible(s));
    },
    // ruleEditorStepLabel — single source of truth for step labels in
    // the wizard's progress strip. Without this, the inline ternary
    // chain in the template missed grabrename / qbitse / qbitcategoryfix
    // (they fell through to "Review"), so on Sonarr where multiple
    // qBit-related steps follow each other the user saw four "Review"
    // labels in a row.
    ruleEditorStepLabel(step) {
      // Match the labels used in ruleEditorTabs[] so wizard-mode
      // progress strip and edit-mode tab strip read identically for
      // the same section.
      const labels = {
        basics:          'Basics',
        filters:         'Filters',
        rg:              'Release Groups',
        audio:           'Audio tags',
        video:           'Video tags',
        dvdetail:        'DV detail',
        missingepisodes: 'Missing episodes',
        tbarefresh:      'TBA refresh',
        plexsync:        'Sync to Plex',
        grabrename:      'qBit Grab Rename',
        qbitse:          'qBit S/E tag',
        qbitcategoryfix: 'qBit category fix',
        schedule:        'Schedule',
        review:          'Review',
      };
      return labels[step] || step;
    },
    ruleEditorJumpToTab(tabId) { if (this.ruleEditorTabVisible(tabId)) this.ruleEditor.activeTab = tabId; },

    // ruleEditorStepBlockedReason returns null when the current step
    // is OK to advance from, or a short explanation string when it's
    // not. Used to gate the Next button (disabled + tooltip + inline
    // hint) so the user can't end up on a Review page only to be
    // sent back when the run dispatches and errors. Validates only
    // the auto-tags steps for now — Basics/Filters/RG already gate
    // through other paths (cron parse, RG count, etc.).
    ruleEditorStepBlockedReason() {
      const r = this.editingRule;
      if (!r) return null;
      const step = this.ruleEditorCurrentStep();
      if (step === 'basics') {
        // Name is required for saved rules (Create flow) — saving with
        // an empty name produces an unidentifiable card later. QFA is
        // exempt because it's a one-shot dispatcher: name auto-fills
        // to "Quick fix-all" and never persists. Block-reason gates
        // both the Next button (UI) and ruleEditorNext (keyboard).
        if (!this.ruleEditor.isQuickFix && !(r.name || '').trim()) {
          return 'Pick a name for the rule before continuing.';
        }
        // Tag-source picking now lives on the RG step (active /
        // discover / filter-only radios). The Basics step deliberately
        // doesn't pre-block on "no active groups" — that pushed the
        // user into a dead-end loop after toggling Discover off in
        // Basics (couldn't advance to RG step where filter-only is
        // selectable). The RG-step gate below handles the no-groups
        // case once the user is actually on the source-picker.
      }
      if (step === 'rg' && this.ruleAffectsTag()) {
        const o = this.editingRule.options || {};
        // Filter-only validates its own per-rule state: tag must be
        // non-empty and must not collide with an existing Active
        // group's Tag for this instance type. Backend would reject
        // either with 4xx; the Next-button gate stops the user
        // before they get there.
        if (o.tagSource === 'filter-only') {
          const t = (o.filterOnlyTag || '').trim();
          if (!t) {
            return 'Enter a tag name for filter-only mode before continuing.';
          }
          if (this.ruleEditorFilterOnlyCollides()) {
            return 'Tag name collides with an existing Active group rule. Pick a different name to continue.';
          }
        } else if (!this.ruleAffectsDiscover() && this.groupsFilteredByInstanceType().length === 0) {
          // Use active groups picked (explicitly or by default) but
          // there are no groups for this instance type and Discover
          // isn't in the chain — Tag pass would be a no-op. Banner
          // above shows the same options as actionable buttons.
          return 'No active release groups yet — Switch to Use filter only above, Add Discover to this rule, or close the wizard and add some via + Add on the Active groups list.';
        }
      }
      if (step === 'filters' && this.ruleAffectsTag()) {
        // Filter is mandatory for Tag quality releases runs after the
        // 2026-05-05 restructure. At least one master (Quality or
        // Audio) must be on. Per-group filtered/simple flag still
        // exists as override but globally we require a filter.
        const f = r.filters || {};
        if (!f.Quality && !f.Audio) {
          return 'Tag quality releases requires at least one filter — enable Quality or Audio above to continue.';
        }
      }
      if (step === 'audio' && this.ruleAffectsAudio()) {
        const a = r.audioTags && r.audioTags.audio;
        if (!a || !a.enabled) {
          return 'Enable the Audio bucket above before continuing — this rule includes the Audio tags phase.';
        }
        const av = a.allowedValues || [];
        // Empty AllowedValues = "all allowed" (engine convention) so
        // that's fine. The toggleAudioTagValue helper auto-disables
        // the bucket when the user un-checks every value, which the
        // first check above catches.
      }
      if (step === 'video' && this.ruleAffectsVideo()) {
        const v = r.videoTags || {};
        const anyOn = ['resolution', 'codec', 'hdr'].some(k => v[k] && v[k].enabled);
        if (!anyOn) {
          return 'Enable Resolution, Codec, or HDR before continuing — this rule includes the Video tags phase.';
        }
      }
      if (step === 'dvdetail' && this.ruleAffectsDvDetail()) {
        const dd = r.dvDetail;
        if (!dd || !dd.enabled) {
          return 'Enable DV detail above before continuing — this rule includes the Dolby Vision detail phase.';
        }
      }
      if (step === 'plexsync' && this.ruleAffectsPlexSync()) {
        const ps = r.plexSync || {};
        if (!ps.plexInstanceId) return 'Pick a Plex server before continuing.';
        if (!ps.libraryKeys || ps.libraryKeys.length === 0) return 'Pick at least one Plex library before continuing.';
        if (!ps.labels || ps.labels.length === 0) return 'Pick at least one tag to sync before continuing.';
        if (!ps.targetTypes || ps.targetTypes.length === 0) return 'Pick Labels and/or Collections before continuing.';
      }
      // Grab Rename — backend rejects rules with no qBit instance, no
      // trigger selected, or a custom-token row missing label/regex /
      // with bad regex. Catch at Next-button time so the user doesn't
      // hit save-failures from the Review step.
      if (step === 'grabrename' && this.ruleAffectsGrabRename()) {
        const c = r.grabRename || {};
        if (!c.qbitInstanceId) {
          return 'Pick a qBit instance for Grab Rename before continuing.';
        }
        if (this.grabRenameNoTriggerSelected()) {
          return 'Enable at least one trigger (or define a custom token) before continuing.';
        }
        const tokens = c.customTokens || [];
        for (let i = 0; i < tokens.length; i++) {
          const t = tokens[i] || {};
          if (!t.label || !String(t.label).trim()) {
            return 'Custom token #' + (i + 1) + ' is missing a label.';
          }
          if (!t.regex || !String(t.regex).trim()) {
            return 'Custom token #' + (i + 1) + ' is missing a regex.';
          }
          if (this.grabRenameRegexInvalid(t.regex)) {
            return 'Custom token #' + (i + 1) + ' has an invalid regex.';
          }
        }
      }
      // qBit Category Fix — backend rejects on empty qbit / missing
      // download-client / equal-or-empty pre/post categories. Pull the
      // canonical reason out of the dedicated helper so the gate and
      // the inline warning stay in sync.
      if (step === 'qbitcategoryfix' && this.ruleAffectsQbitCategoryFix()) {
        const w = this.qbitCategoryFixWarning();
        if (w) return w;
      }
      // Plex label sync — backend rejects on missing Plex instance,
      // empty library list, empty label whitelist, or library-type
      // mismatch with the rule's appType.
      if (step === 'plexlabelsync' && this.ruleAffectsPlexLabelSync()) {
        const p = r.plexLabelSync || {};
        if (!p.plexInstanceId) {
          return 'Pick a Plex instance before continuing.';
        }
        if (!p.libraryKeys || p.libraryKeys.length === 0) {
          return 'Pick at least one Plex library before continuing.';
        }
        if (!p.labels || p.labels.length === 0) {
          return 'Add at least one tag to the whitelist before continuing.';
        }
        if (!p.targetTypes || p.targetTypes.length === 0) {
          return 'Pick at least one Plex target (Labels or Collections).';
        }
      }
      // qBit S/E tag — backend (webhook_rules.go) rejects with empty
      // qbit instance OR no rule enabled OR an enabled rule's tag name
      // failing the regex check. Mirror all three gates here.
      if (step === 'qbitse' && this.ruleAffectsQbitSe()) {
        const q = r.qbitSe || {};
        if (!q.qbitInstanceId) {
          return 'Pick a qBit instance for tagging before continuing.';
        }
        if (this.qbitSeNoRuleEnabled()) {
          return 'Enable Episode, Season, or Unmatched before continuing.';
        }
        if (q.episodeEnabled && !this.qbitSeTagNameValid(q.episodeTag)) {
          return 'Episode tag name must be letters, digits, underscores, or dashes.';
        }
        if (q.seasonEnabled && !this.qbitSeTagNameValid(q.seasonTag)) {
          return 'Season tag name must be letters, digits, underscores, or dashes.';
        }
        if (q.unmatchedEnabled && !this.qbitSeTagNameValid(q.unmatchedTag)) {
          return 'Unmatched tag name must be letters, digits, underscores, or dashes.';
        }
      }
      return null;
    },
    ruleEditorNext() {
      // Hard-stop on the current step's blocked-reason. Belt-and-
      // suspenders alongside the :disabled binding on the button —
      // a keyboard-driven user pressing Enter could otherwise sneak
      // past the visual gate.
      if (this.ruleEditorStepBlockedReason()) return;
      const steps = this.ruleEditorVisibleSteps();
      this.ruleEditor.step = Math.min(steps.length - 1, this.ruleEditor.step + 1);
    },
    ruleEditorPrev() {
      this.ruleEditor.step = Math.max(0, this.ruleEditor.step - 1);
    },
    ruleEditorCurrentStep() { return this.ruleEditorVisibleSteps()[this.ruleEditor.step] || 'basics'; },
    ruleEditorIsLastStep()  { return this.ruleEditor.step === this.ruleEditorVisibleSteps().length - 1; },
    // The single source of truth for "which section is rendering right
    // now" — wizard reads from step index, tabbed-edit reads from
    // activeTab. Section markup checks against this so each block lives
    // in one place regardless of flow.
    ruleEditorCurrentSection() {
      return this.ruleEditor.isCreate ? this.ruleEditorCurrentStep() : this.ruleEditor.activeTab;
    },

    // ---- Schedule preset helpers (rule-scoped mirrors of applySchedulePreset etc.) ----
    ruleApplyPreset() {
      const r = this.editingRule;
      if (!r) return;
      const h = Number.isFinite(r.hour) ? Math.max(0, Math.min(23, Math.floor(r.hour))) : 3;
      const m = Number.isFinite(r.minute) ? Math.max(0, Math.min(59, Math.floor(r.minute))) : 0;
      switch (r.preset) {
        case 'hourly':       r.cron = '0 * * * *'; break;
        case 'every-6h':     r.cron = '0 */6 * * *'; break;
        case 'every-12h':    r.cron = '0 */12 * * *'; break;
        case 'daily':        r.cron = `${m} ${h} * * *`; break;
        case 'twice-daily':  r.cron = '0 0,12 * * *'; break;
        case 'weekly':       r.cron = `${m} ${h} * * ${r.dow}`; break;
        case 'monthly':      r.cron = `${m} ${h} ${r.dom} * *`; break;
        case 'custom':       /* user-typed */ break;
      }
      this.computeRuleEditorNextFires();
    },
    ruleSyncHour12To24() {
      const r = this.editingRule;
      if (!r) return;
      let h = parseInt(r.hour12, 10);
      if (!Number.isFinite(h)) return;
      h = Math.max(1, Math.min(12, h));
      if (r.ampm === 'AM') r.hour = (h === 12) ? 0 : h;
      else                 r.hour = (h === 12) ? 12 : h + 12;
      this.ruleApplyPreset();
    },
    ruleEditorCombinedHas(mode) { return ((this.editingRule && this.editingRule.options.combinedModes) || []).includes(mode); },
    ruleEditorCombinedToggle(mode) {
      const arr = this.editingRule.options.combinedModes || [];
      const i = arr.indexOf(mode);
      const turningOn = i < 0;
      if (i >= 0) arr.splice(i, 1);
      else arr.push(mode);
      this.editingRule.options.combinedModes = arr;
      if (mode === 'discover' && turningOn) this.ensureDiscoverDefaults();
      // Discover just left the chain — clear any stale tagSource pick
      // tied to Discover. Otherwise the RG step opens with the
      // "Discover isn't part of this rule" auto-correct prompt firing
      // on every open, and the validation surfaces a misleading
      // Discover-related error.
      if (mode === 'discover' && !turningOn && this.editingRule.options.tagSource === 'discover') {
        this.editingRule.options.tagSource = '';
      }
    },
    // ensureDiscoverDefaults seeds the Discover-add toggle pair to
    // "Add disabled" when the rule includes Discover but neither
    // flag is set yet. Preview-only Discover is no longer a wizard
    // option — the RG-step radio chooses between disabled-add and
    // enabled-add. Called whenever mode changes to discover or the
    // combined-mode list gains discover so the radio always has a
    // valid default selected.
    ensureDiscoverDefaults() {
      if (!this.editingRule || !this.editingRule.options) return;
      const o = this.editingRule.options;
      if (!o.discoverWriteBack) {
        o.discoverWriteBack = true;
        o.autoActivateDiscovered = false;
      }
    },
    // Bound to the Mode <select> @change so picking Discover as the
    // single mode also seeds the defaults. ruleAffectsDiscover is
    // re-checked after the change because Alpine has already updated
    // the model by the time @change fires.
    ruleEditorOnModeChange() {
      if (this.ruleAffectsDiscover()) this.ensureDiscoverDefaults();
    },
    // When the user changes the rule's instance, the RG selection must
    // re-snapshot so we don't carry Sonarr IDs into a Radarr rule (or
    // vice versa). Filters block stays — they're per-Arr-type
    // independent so user-pinned audio/quality preferences still apply.
    // Mode + combined substeps must also be filtered through the
    // per-instance-type catalog so a Sonarr rule doesn't end up on a
    // Radarr-only mode (which would 501 at scan time).
    ruleEditorOnInstanceChange() {
      const r = this.editingRule;
      if (!r) return;
      r.releaseGroupIds = this.snapshotGlobalRGIds(r.instanceId);
      // Mode is always 'combined' post-dropdown-removal; instance-type
      // change just filters combinedModes to substeps the new type
      // supports (Sonarr → only 'recover' today). Sonarr selection
      // with no 'recover' tick auto-seeds it so the rule has a phase.
      r.mode = 'combined';
      const supportedSubs = this.ruleCombinedSubstepsForInstance().map(s => s.value);
      r.options.combinedModes = (r.options.combinedModes || []).filter(m => supportedSubs.includes(m));
      if (this.ruleEditorInstanceType() === 'sonarr' && r.options.combinedModes.length === 0) {
        r.options.combinedModes = ['recover'];
      }
      // qBit Category Fix: the picked Arr-side download-client ID + the
      // pre/post category snapshots are scoped to the OLD instance's
      // /api/v3/downloadclient list. After switching to a new Arr the
      // saved ID may not exist on the new instance — clear them so the
      // user has to re-pick from the new instance's live list. Same for
      // the loaded list itself (different Arr → different clients) and
      // the error/loading status.
      if (r.qbitCategoryFix) {
        r.qbitCategoryFix.arrDownloadClientId = 0;
        r.qbitCategoryFix.preImportCategorySnapshot = '';
        r.qbitCategoryFix.postImportCategorySnapshot = '';
      }
      this.arrDownloadClients = [];
      this.arrDownloadClientsError = '';
      // If the rule currently has qBit Category Fix on, kick off a fresh
      // load against the new instance so the user lands on Step 3d with
      // a populated picker rather than an empty state.
      if (typeof this.ruleAffectsQbitCategoryFix === 'function' &&
          this.ruleAffectsQbitCategoryFix() && r.instanceId) {
        this.loadArrDownloadClients(r.instanceId);
      }
    },

    // ---- Release Groups picker ----
    groupsForRule() {
      if (!this.editingRule) return [];
      const inst = this.instances.find(i => i.id === this.editingRule.instanceId);
      if (!inst) return [];
      return this.groups.filter(g => g.type === inst.type);
    },
    ruleEditorRGChecked(id) { return ((this.editingRule && this.editingRule.releaseGroupIds) || []).includes(id); },
    ruleEditorRGToggle(id) {
      const arr = this.editingRule.releaseGroupIds || [];
      const i = arr.indexOf(id);
      if (i >= 0) arr.splice(i, 1);
      else arr.push(id);
      this.editingRule.releaseGroupIds = arr;
    },
    ruleEditorRGAllVisible() {
      const visible = this.groupsForRule();
      return visible.length > 0 && visible.every(g => (this.editingRule.releaseGroupIds || []).includes(g.id));
    },
    ruleEditorRGToggleAll() {
      const visible = this.groupsForRule();
      if (this.ruleEditorRGAllVisible()) {
        this.editingRule.releaseGroupIds = (this.editingRule.releaseGroupIds || []).filter(id => !visible.some(g => g.id === id));
      } else {
        const set = new Set(this.editingRule.releaseGroupIds || []);
        visible.forEach(g => set.add(g.id));
        this.editingRule.releaseGroupIds = [...set];
      }
    },

    // ---- Cron preview ----
    computeRuleEditorNextFires() {
      const r = this.editingRule;
      if (!r) return;
      const cron = (r.cron || '').trim();
      if (!cron) {
        this.ruleEditor.nextFires = [];
        this.ruleEditor.cronError = '';
        return;
      }
      try {
        const fires = nextCronFires(cron, 5, new Date());
        const opts = this.dateFormatOptions();
        const loc = this.serverLocale || 'en-GB';
        this.ruleEditor.nextFires = fires.map(d => d.toLocaleString(loc, opts));
        this.ruleEditor.cronError = '';
      } catch (e) {
        this.ruleEditor.nextFires = [];
        this.ruleEditor.cronError = 'Invalid cron: ' + (e.message || 'unknown');
      }
    },

    // ---- Save ----
    async saveRuleEditor() {
      const r = this.editingRule;
      if (!r) return;
      this.ruleEditor.error = '';
      // Webhook-rule branch — kind='webhook' takes precedence over the
      // schedule path. Builds the webhook-rule body (functions array,
      // per-rule snapshots, no cron) and POSTs to /api/webhook-rules.
      // Schedule-rule path below stays untouched for kind='schedule'.
      if (this.ruleEditorIsWebhook()) {
        return this.saveWebhookRuleEditor();
      }
      // Quickfix mode skips name/cron validation — the wizard hides
      // those fields. Save dispatches the chain instead of persisting
      // a rule (see runQuickFixChain in the dispatcher).
      if (this.ruleEditor.isQuickFix) {
        if (!r.instanceId) { this.ruleEditor.error = 'Pick an instance'; return; }
        if (r.mode === 'combined' && (!r.options.combinedModes || r.options.combinedModes.length === 0)) {
          this.ruleEditor.error = 'Combined mode needs at least one chain step';
          return;
        }
        if (this.tagPhaseNeedsGroups() && (!r.releaseGroupIds || r.releaseGroupIds.length === 0)) {
          this.ruleEditor.error = 'Pick at least one Release Group, or enable Discover with "Add to config + enable" so it seeds them at runtime';
          return;
        }
        return this.runQuickFixChain();
      }
      if (!r.name.trim()) { this.ruleEditor.error = 'Name is required'; return; }
      if (!r.instanceId) { this.ruleEditor.error = 'Pick an instance'; return; }
      // Manual-only rules persist with cron="" — skip cron validation.
      if (!r.manualOnly) {
        if (!r.cron.trim()) { this.ruleEditor.error = 'Cron expression is required'; return; }
        if (this.ruleEditor.cronError) { this.ruleEditor.error = this.ruleEditor.cronError; return; }
      }
      if (r.mode === 'combined' && (!r.options.combinedModes || r.options.combinedModes.length === 0)) {
        this.ruleEditor.error = 'Combined mode needs at least one chain step';
        return;
      }
      // Tag-touching rules must have at least one Release Group active —
      // an empty subset would tag nothing and just confuse the user when
      // the schedule fires with zero output. Bypassed for Discover →
      // auto-activate chains (see tagPhaseNeedsGroups).
      if (this.tagPhaseNeedsGroups() && (!r.releaseGroupIds || r.releaseGroupIds.length === 0)) {
        this.ruleEditor.error = 'Pick at least one Release Group for this rule, or enable Discover with "Add to config + enable" so it seeds them at runtime';
        return;
      }
      const labelErr = this.ruleEditorLabelError();
      if (labelErr) { this.ruleEditor.error = labelErr; return; }
      // Remember the picked instance per Arr-type so the next
      // Create-rule wizard open pre-fills with the same instance.
      // ruleEditor.appType holds the wizard's locked Arr-type.
      const memKey = 'create-rule-' + (this.ruleEditor.appType || 'radarr');
      this.rememberWizardInstance(memKey, r.instanceId);
      const options = { ...r.options };
      if (r.mode !== 'combined') options.combinedModes = [];
      if (r.mode === 'discover' || r.mode === 'recover') {
        options.runMode = '';
        options.cleanupUnusedTags = false;
        options.syncToSecondary = false;
      }
      const body = {
        name: r.name.trim(),
        mode: r.mode,
        instanceId: r.instanceId,
        // Manual-only rules persist with empty cron — backend
        // validation accepts that as "skip cron-loop registration".
        cron: r.manualOnly ? '' : r.cron.trim(),
        enabled: !!r.enabled,
        options,
      };
      // Per-rule snapshots: only sent when the section is relevant to
      // the current rule's mode. Recover-only rules don't carry RG/
      // Filters/Extra-tags noise, keeping the persisted shape clean.
      if (this.ruleEditorTabVisible('rg'))       body.releaseGroupIds = [...(r.releaseGroupIds || [])];
      if (this.ruleEditorTabVisible('filters'))  body.filters = { ...r.filters };
      if (this.ruleEditorTabVisible('audio') && r.audioTags)    body.audioTags = JSON.parse(JSON.stringify(r.audioTags));
      if (this.ruleEditorTabVisible('video') && r.videoTags)    body.videoTags = JSON.parse(JSON.stringify(r.videoTags));
      if (this.ruleEditorTabVisible('dvdetail') && r.dvDetail)  body.dvDetail  = JSON.parse(JSON.stringify(r.dvDetail));
      // Missing Episodes has no dedicated wizard step — config lives
      // inline in Review. Send snapshot whenever the phase is in
      // combinedModes so the backend persists it on the saved rule.
      if (this.ruleAffectsMissingEpisodes() && r.missingEpisodes) body.missingEpisodes = JSON.parse(JSON.stringify(r.missingEpisodes));
      // Plex sync — dedicated wizard step (editingRule.plexSync). Send
      // whenever the phase is selected so the backend persists the
      // snapshot on the saved schedule.
      if (this.ruleAffectsPlexSync() && r.plexSync) body.plexSync = JSON.parse(JSON.stringify(r.plexSync));
      // TBA refresh — Sonarr-only file-rename phase, config in Review.
      if (this.ruleAffectsTbaRefresh() && r.tbaRefresh) body.tbaRefresh = JSON.parse(JSON.stringify(r.tbaRefresh));
      this.ruleEditor.busy = true;
      try {
        const url = r.id ? `/api/schedules/${r.id}` : '/api/schedules';
        const method = r.id ? 'PUT' : 'POST';
        const res = await this.apiFetch(url, {
          method, headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(d.error || 'HTTP ' + res.status);
        this.ruleEditor.open = false;
        this.editingRule = null;
        this.showToast(r.id ? 'Schedule updated' : 'Schedule created', 'success');
        await this.loadSchedules();
      } catch (e) {
        this.ruleEditor.error = e.message || 'Save failed';
      } finally {
        this.ruleEditor.busy = false;
      }
    },

    // Webhook-rule save flow. Branched off saveRuleEditor when
    // ruleEditor.kind === 'webhook'. Builds the WebhookRule shape,
    // POSTs to /api/webhook-rules (or PUTs on update), refreshes the
    // local cache, closes the editor on success. Validation errors
    // surface inline + leave the wizard open so the user can fix.
    async saveWebhookRuleEditor() {
      const r = this.editingRule;
      const inst = (this.instances || []).find(i => i.id === r.instanceId);
      if (!inst) {
        this.ruleEditor.error = 'Pick a target instance';
        return;
      }
      if (!r.name || !r.name.trim()) {
        this.ruleEditor.error = 'Rule name is required';
        return;
      }
      if (!this.webhookRuleAnyFunctionTicked()) {
        this.ruleEditor.error = 'Pick at least one function on the Basics step';
        return;
      }
      const labelErr = this.ruleEditorLabelError();
      if (labelErr) { this.ruleEditor.error = labelErr; return; }
      // Build canonical-ordered functions array — matches dispatcher
      // execution order so saved Functions[] reads naturally in the
      // rule list + audit logs.
      const o = r.options || {};
      const fnList = [];
      if (o.fnDiscover)         fnList.push('discover');
      if (o.fnRecover)          fnList.push('recover');
      if (o.fnTagReleaseGroups) fnList.push('tagReleaseGroups');
      if (o.fnTagAudio)         fnList.push('tagAudio');
      if (o.fnTagVideo)         fnList.push('tagVideo');
      if (o.fnTagDvDetail)      fnList.push('tagDvDetail');
      if (o.fnSyncToSecondary)  fnList.push('syncToSecondary');
      if (o.fnGrabRename)       fnList.push('grabRename');
      if (o.fnQbitSeTag)        fnList.push('qbitSeTag');
      if (o.fnQbitCategoryFix)  fnList.push('qbitCategoryFix');
      if (o.fnPlexLabelSync)    fnList.push('plexLabelSync');
      const body = {
        name: r.name.trim(),
        enabled: r.enabled !== false,
        instanceId: r.instanceId,
        appType: inst.type,
        functions: fnList,
        // M-Webhook notification kill-switch. When true, the rule's
        // fires reach every enabled agent whose Events.OnX flag
        // matches the event class. Each agent's Functions whitelist
        // (Settings → Notifications) decides what actually renders.
        notifyOnFire: !!(r.options && r.options.notifyOnFire),
      };
      // Per-rule snapshots — only sent when the corresponding step
      // is relevant. Webhook rules use the same gates (ruleAffectsTag
      // etc.) as schedule rules, so this mirrors saveRuleEditor's
      // schedule branch.
      if (this.ruleEditorTabVisible('rg'))       body.releaseGroupIds = [...(r.releaseGroupIds || [])];
      if (this.ruleEditorTabVisible('filters'))  body.filters = { ...r.filters };
      if (this.ruleEditorTabVisible('audio') && r.audioTags)    body.audioTags = JSON.parse(JSON.stringify(r.audioTags));
      if (this.ruleEditorTabVisible('video') && r.videoTags)    body.videoTags = JSON.parse(JSON.stringify(r.videoTags));
      if (this.ruleEditorTabVisible('dvdetail') && r.dvDetail)  body.dvDetail  = JSON.parse(JSON.stringify(r.dvDetail));
      // Missing Episodes has no dedicated wizard step — config lives
      // inline in Review. Send snapshot whenever the phase is in
      // combinedModes so the backend persists it on the saved rule.
      if (this.ruleAffectsMissingEpisodes() && r.missingEpisodes) body.missingEpisodes = JSON.parse(JSON.stringify(r.missingEpisodes));
      // Filter-only tag-source — only sent when the rule is in
      // filter-only mode AND Tag-RG is enabled (otherwise the fields
      // are meaningless and the backend would reject filterOnlyTag
      // alone with an unrelated function set). Backend mirrors the
      // schedule path's validator: tagSource clamps to active /
      // filter-only; filterOnlyTag is required when filter-only +
      // Tag-RG; tag must match Radarr's `^[a-z0-9-]+$` regex; tag
      // must not collide with an existing per-group rule's Tag.
      // Emit tagSource whenever it's set — mirrors the schedule path
      // (saveRuleEditor) which sends 'active' / 'discover' / 'filter-only'
      // alike. Previously this branch only emitted 'filter-only', silently
      // dropping 'discover' so the user's "Use Discover" pick reverted to
      // "Use active groups" on next open. Backend validator at
      // webhook_rules.go accepts all three values + empty.
      const trimmedTagSource = (o.tagSource || '').trim();
      if (trimmedTagSource) {
        body.tagSource = trimmedTagSource;
        if (trimmedTagSource === 'filter-only' && o.fnTagReleaseGroups) {
          body.filterOnlyTag = (o.filterOnlyTag || 'lossless-web').trim();
        }
      }
      // Discover-add behaviour. UI binds to autoActivateDiscovered; wire
      // shape uses discoverAutoEnable (load-side hoist at
      // openEditWebhookRuleModal maps backend -> UI on the way in, so the
      // inverse mapping has to happen here on the way out). Only relevant
      // when the rule actually runs the Discover phase.
      if (o.fnDiscover) {
        body.discoverAutoEnable = !!o.autoActivateDiscovered;
      }
      // Cleanup-unused-tags toggle. Only relevant when the rule runs the
      // Tag-RG phase + isn't in filter-only mode (same UI gate as the
      // schedule/QFA editor at index.html ~line 7790). On webhook events
      // the per-item add+remove diff happens via applyAutoTagDiff
      // regardless of this flag; the flag persists here so the rule
      // editor shows the user's choice consistently on reopen.
      if (o.fnTagReleaseGroups && (o.tagSource || 'active') !== 'filter-only') {
        body.cleanupUnusedTags = !!o.cleanupUnusedTags;
      }

      // Sync target — only meaningful when fnSyncToSecondary is on.
      if (o.fnSyncToSecondary && o.syncToInstanceId) {
        body.syncToInstanceId = o.syncToInstanceId;
      }
      // GrabRename / QbitSe criteria — only sent when the matching fn
      // is enabled. Backend's validator (validateGrabRenameCriteria +
      // qbitInstanceExists checks) requires the struct + a valid
      // qbitInstanceId when the function is in the rule's Functions
      // list, so this gate is symmetric with the wizard's tab gating.
      if (o.fnGrabRename && r.grabRename) {
        body.grabRename = JSON.parse(JSON.stringify(r.grabRename));
      }
      if (o.fnQbitSeTag && r.qbitSe) {
        body.qbitSe = JSON.parse(JSON.stringify(r.qbitSe));
      }
      if (o.fnQbitCategoryFix && r.qbitCategoryFix) {
        body.qbitCategoryFix = JSON.parse(JSON.stringify(r.qbitCategoryFix));
      }
      if (o.fnPlexLabelSync && r.plexLabelSync) {
        // Drop empty labelDisplay map for clean JSON over the wire —
        // backend tolerates either shape but tidy is better.
        const ps = JSON.parse(JSON.stringify(r.plexLabelSync));
        if (ps.labelDisplay && Object.keys(ps.labelDisplay).length === 0) {
          delete ps.labelDisplay;
        }
        body.plexLabelSync = ps;
      }
      this.ruleEditor.busy = true;
      try {
        const url = r.id ? `/api/webhook-rules/${r.id}` : '/api/webhook-rules';
        const method = r.id ? 'PUT' : 'POST';
        const res = await this.apiFetch(url, {
          method, headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(d.error || 'HTTP ' + res.status);
        this.ruleEditor.open = false;
        this.editingRule = null;
        this.showToast(r.id ? 'Webhook rule updated' : 'Webhook rule created', 'success');
        await this.loadWebhookRules();
      } catch (e) {
        this.ruleEditor.error = e.message || 'Save failed';
      } finally {
        this.ruleEditor.busy = false;
      }
    },

    // Load the webhook-rules list from the server. Mirrors loadSchedules
    // — populates this.webhookRules so the Webhooks page list +
    // editor edit-flow can read off the same array.
    async loadWebhookRules() {
      try {
        const r = await this.apiFetch('/api/webhook-rules');
        if (r.ok) {
          const d = await r.json();
          this.webhookRules = Array.isArray(d) ? d : [];
        }
      } catch (e) {
        // Silent — Webhooks page surfaces its own load-failed state.
      }
    },

    // Filter the rules list to a specific instance. Used by the
    // per-instance card on the Webhooks page.
    webhookRulesForInstance(instanceId) {
      return (this.webhookRules || []).filter(r => r.instanceId === instanceId);
    },

    // Compute the Connect event types this instance's webhook needs
    // toggled on in Sonarr/Radarr. Derived from the enabled rules'
    // functions + per-bucket strip-on-delete opt-ins:
    //   tagReleaseGroups / discover / tagAudio / tagVideo / tagDvDetail
    //     / recover / syncToSecondary  → On Import + On Upgrade
    //     (Sonarr: On Download — download/import + upgrade)
    //   tagReleaseGroups (Radarr only)  → also On Movie File Delete +
    //     OnMovieFileDeleteForUpgrade (auto-strip Tag-RG flow)
    //   any audio/video/DV bucket with stripOnFileDelete=true → matching
    //     On Movie/Episode File Delete events
    //   grabRename / qbitSeTag → On Grab
    // "On Test" is NOT a togglable event — it's a button in Connect
    // that lets you fire a test ping. Listing it here would send users
    // hunting for a checkbox that doesn't exist.
    webhookEventsForFunctions(fnSet, isSonarr, opts = {}) {
      const events = new Set();
      const importLike = ['tagReleaseGroups', 'discover', 'tagAudio', 'tagVideo',
                          'tagDvDetail', 'recover', 'syncToSecondary'];
      if (importLike.some(f => fnSet.has(f))) {
        events.add('On File Import');
        events.add('On File Upgrade');
      }
      // Tag-RG (Radarr) drives the automatic strip-on-delete invariant,
      // so the user must enable Movie File Delete events even if no
      // user-toggleable function dispatches on them.
      if (!isSonarr && fnSet.has('tagReleaseGroups')) {
        events.add('On Movie File Delete');
      }
      // Per-bucket strip-on-delete: any bucket flagged → matching
      // file-delete events. Mirror of the server's
      // FiresPerBucketStripOnDelete + ConnectEventsNeeded extension.
      if (opts.bucketStripOnDelete) {
        events.add(isSonarr ? 'On Episode File Delete' : 'On Movie File Delete');
      }
      if (fnSet.has('grabRename') || fnSet.has('qbitSeTag')) {
        events.add('On Grab');
      }
      return Array.from(events);
    },

    // Events for an instance — union of events across all enabled
    // rules linked to that instance. Used by the per-instance card
    // on the Webhooks page so the user can see at a glance "what
    // do I need to keep enabled in Sonarr/Radarr Connect for these
    // rules to work". Empty when no rules exist (Configure webhook
    // alone doesn't dictate any events; the URL just sits ready
    // for whatever events arrive).
    webhookEventsForInstance(instanceId) {
      const inst = (this.instances || []).find(i => i.id === instanceId);
      if (!inst) return [];
      const rules = this.webhookRulesForInstance(instanceId).filter(r => r.enabled);
      const fnSet = new Set();
      let bucketStripOnDelete = false;
      for (const rule of rules) {
        for (const fn of (rule.functions || [])) fnSet.add(fn);
        if ((rule.audioTags && rule.audioTags.stripOnFileDelete)
            || (rule.videoTags && rule.videoTags.stripOnFileDelete)
            || (rule.dvDetail && rule.dvDetail.stripOnFileDelete)) {
          bucketStripOnDelete = true;
        }
      }
      return this.webhookEventsForFunctions(fnSet, inst.type === 'sonarr', { bucketStripOnDelete });
    },

    // Events for a single rule — used in the rule editor's Review
    // step. Reads off editingRule.options.fn* + bucket snapshots
    // directly so the events list updates live as the user toggles
    // function checkboxes + bucket strip flags on the Basics + bucket
    // steps, before the rule is saved.
    webhookEventsForCurrentRule() {
      const r = this.editingRule;
      if (!r || !r.options) return [];
      const o = r.options;
      const fnSet = new Set();
      if (o.fnTagReleaseGroups) fnSet.add('tagReleaseGroups');
      if (o.fnDiscover)         fnSet.add('discover');
      if (o.fnTagAudio)         fnSet.add('tagAudio');
      if (o.fnTagVideo)         fnSet.add('tagVideo');
      if (o.fnTagDvDetail)      fnSet.add('tagDvDetail');
      if (o.fnRecover)          fnSet.add('recover');
      if (o.fnSyncToSecondary)  fnSet.add('syncToSecondary');
      if (o.fnGrabRename)       fnSet.add('grabRename');
      if (o.fnQbitSeTag)        fnSet.add('qbitSeTag');
      const inst = (this.instances || []).find(i => i.id === r.instanceId);
      const isSonarr = inst ? inst.type === 'sonarr' : false;
      return this.webhookEventsForFunctions(fnSet, isSonarr, {
        bucketStripOnDelete: this.webhookRuleHasBucketStripOnDelete(),
      });
    },

    // App-type setter — persists to localStorage + mirrors the
    // hash via pushNav so back/forward + bookmark URLs include
    // the picked type. Refuses to switch to a type with no
    // instances (pills are :disabled in that state but a
    // programmatic call should still no-op).
    setWebhookAppType(type) {
      if (type !== 'radarr' && type !== 'sonarr') return;
      if (!this.webhookAppTypeAvailable(type)) return;
      if (this.webhookAppType === type) return;
      this.webhookAppType = type;
      localStorage.setItem('resolvarr-webhook-app-type', type);
      // Auto-pick the Recent activity instance whenever the new
      // pill has exactly one instance, OR the currently-selected
      // instance no longer matches the new app-type. Saves a click
      // when the user only has one Sonarr / one Radarr.
      const candidates = (this.instances || []).filter(i => i.type === type);
      const currentValid = candidates.some(i => i.id === this.webhookActivityInstanceId);
      if (!currentValid) {
        this.webhookActivityInstanceId = candidates.length > 0 ? candidates[0].id : '';
        if (this.webhookActivityInstanceId) {
          this.loadWebhookEvents(this.webhookActivityInstanceId);
        }
      }
      this.pushNav();
    },

    // True when there's at least one instance of the given type.
    // Used to gate the pill's :disabled state.
    webhookAppTypeAvailable(type) {
      return (this.instances || []).some(i => i.type === type);
    },

    // Filtered instance list for the Webhooks page — matches the
    // active app-type pill. Iterated by the per-instance card
    // template instead of the raw `instances` array.
    webhookInstancesForAppType() {
      return (this.instances || []).filter(i => i.type === this.webhookAppType);
    },

    // Toggle a rule's enabled state via PUT. Lighter than a full
    // edit-flow when the user just wants to pause/resume a rule
    // without re-walking the wizard.
    async toggleWebhookRuleEnabled(rule) {
      try {
        const body = {
          name: rule.name,
          enabled: !rule.enabled,
          instanceId: rule.instanceId,
          appType: rule.appType,
          functions: rule.functions || [],
        };
        // Per-rule snapshots round-trip via the response from a fresh
        // GET — toggling enabled doesn't need to re-send them.
        // Backend's update path preserves nil-fields when not present,
        // matching the schedule rule behaviour.
        if (rule.filters)         body.filters = rule.filters;
        if (rule.audioTags)       body.audioTags = rule.audioTags;
        if (rule.videoTags)       body.videoTags = rule.videoTags;
        if (rule.dvDetail)        body.dvDetail = rule.dvDetail;
        if (rule.releaseGroupIds) body.releaseGroupIds = rule.releaseGroupIds;
        if (rule.syncToInstanceId) body.syncToInstanceId = rule.syncToInstanceId;
        if (rule.discoverAutoEnable) body.discoverAutoEnable = rule.discoverAutoEnable;
        if (rule.grabRename)      body.grabRename = rule.grabRename;
        if (rule.qbitSe)          body.qbitSe = rule.qbitSe;
        const r = await this.apiFetch('/api/webhook-rules/' + rule.id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadWebhookRules();
        this.showToast('Rule ' + (body.enabled ? 'resumed' : 'paused'), 'success');
      } catch (e) {
        this.showToast('Toggle failed: ' + e.message, 'error');
      }
    },

    // Newest-first lookup of the rule's last fire. Returns null when
    // the rule has never fired. Used by the rule-card last-status
    // pill — shape is core.WebhookRuleRun (status + startedAt +
    // durationMs + eventType + itemTitle + itemContext + summary).
    lastWebhookRun(rule) {
      const h = (rule && rule.history) || [];
      return h.length > 0 ? h[h.length - 1] : null;
    },

    // Returns the .card-edge variant class for a webhook rule's
    // left-edge status strip. Mirror of scheduleEdgeClass — same
    // colour semantics (green=ok, amber=partial, red=error,
    // gray=never-fired) so users moving between schedule cards
    // (Tag Library) and webhook cards (Webhooks page) get the same
    // visual cue.
    webhookEdgeClass(rule) {
      const last = this.lastWebhookRun(rule);
      if (!last) return 'gray';
      switch (last.status) {
        case 'ok':      return 'green';
        case 'partial': return 'amber';
        case 'error':   return 'red';
        default:        return 'gray';
      }
    },

    // Connect events a SAVED webhook rule listens on. Mirrors
    // webhookEventsForCurrentRule but reads from a saved rule's
    // functions[] (server-shape) instead of the editor's options.fn*
    // (wizard-shape). Used by the rule card's "Triggers" body row
    // for parity with schedule cards' "Next" timestamp.
    webhookEventsForRule(rule) {
      if (!rule) return [];
      const fnSet = new Set((rule.functions || []).map(s => String(s)));
      const inst = (this.instances || []).find(i => i.id === rule.instanceId);
      const isSonarr = inst ? inst.type === 'sonarr' : false;
      const bucketStripOnDelete =
        (rule.audioTags && rule.audioTags.stripOnFileDelete) ||
        (rule.videoTags && rule.videoTags.stripOnFileDelete) ||
        (rule.dvDetail && rule.dvDetail.stripOnFileDelete);
      return this.webhookEventsForFunctions(fnSet, isSonarr, { bucketStripOnDelete });
    },

    // Parse a webhook rule-run summary string into structured per-
    // function rows for the history modal. Summary format from
    // buildWebhookRuleRun is "fn1: result1; fn2: result2; ..." where
    // each result MAY have a trailing ` (detail text)` block. We
    // split on '; ' for the outer separator and on the FIRST ' (' for
    // result-vs-detail — paren-balanced enough for every shape the
    // dispatcher emits today (nested parens like "movie tmdbId=X not
    // in Radarr (Remux) library" survive intact because we keep
    // everything after the first ' (' minus a trailing ')').
    parseRuleRunSummary(summary) {
      if (!summary || typeof summary !== 'string') return [];
      const out = [];
      const pieces = summary.split('; ');
      for (const piece of pieces) {
        const trimmed = piece.trim();
        if (!trimmed) continue;
        const colonIdx = trimmed.indexOf(': ');
        if (colonIdx === -1) {
          out.push({ fn: '', result: trimmed, detail: '', status: 'unknown' });
          continue;
        }
        const fn = trimmed.slice(0, colonIdx);
        const after = trimmed.slice(colonIdx + 2);
        const parenIdx = after.indexOf(' (');
        let result, detail;
        if (parenIdx === -1) {
          result = after;
          detail = '';
        } else {
          result = after.slice(0, parenIdx);
          detail = after.slice(parenIdx + 2);
          if (detail.endsWith(')')) detail = detail.slice(0, -1);
        }
        // Status is derived from the raw result string (still carries
        // the "error: " marker if the dispatcher prefixed one). Then
        // strip that marker from the display version — the red ✕ icon
        // + red chip background convey "error" without the user
        // needing to read the word twice.
        const status = this.webhookFnResultStatus(result);
        let displayResult = result;
        if (status === 'error' && /^error:\s*/i.test(displayResult)) {
          displayResult = displayResult.replace(/^error:\s*/i, '');
        }
        // Grab Rename gets a special structured render in the history
        // modal — the raw "renamed \"from\" → \"to\" (triggers: ...)"
        // is too long for a single chip and the trigger labels are
        // jargon. Precompute parsed names + diff tokens + humanized
        // triggers so the template can render them as a compact diff
        // without doing regex work on every Alpine reactivity tick.
        let grabRename = null;
        if (fn === 'grabRename' && status === 'change') {
          const names = this.parseGrabRenameNames(result);
          if (names) {
            grabRename = {
              from: names.from,
              to: names.to,
              triggers: this.parseGrabRenameTriggers(detail).map(t => this.humanizeGrabRenameTrigger(t)),
              diff: this.grabRenameDiffTokens(names.from, names.to),
            };
          }
        }
        out.push({
          fn,
          result: displayResult,
          detail,
          status,
          grabRename,
        });
      }
      return out;
    },

    // Classify a result-prefix into a status bucket so the modal can
    // pick the right icon + color. Kept in sync with the prefixes the
    // dispatcher actually emits (recover.go / discover.go / tag_*.go /
    // sync.go / file_delete.go / grab_rename.go / qbit_*.go).
    webhookFnResultStatus(result) {
      const r = (result || '').toLowerCase().trim();
      if (!r) return 'unknown';
      if (r.startsWith('skipped')) return 'skipped';
      if (r.startsWith('failed') || r.startsWith('error') || r.includes('err:')) return 'error';
      if (r.startsWith('no change') || r.startsWith('no diff') || r === 'noop') return 'noop';
      if (r.startsWith('+')) return 'change-add';
      if (r.startsWith('-')) return 'change-remove';
      if (r.startsWith('renamed') || r.startsWith('changed') || r.startsWith('applied') ||
          r.startsWith('mirrored') || r.startsWith('tagged') || r.startsWith('cleaned') ||
          r.startsWith('stripped') || r.startsWith('recovered') || r.startsWith('queued') ||
          r.startsWith('discovered')) return 'change';
      return 'change';
    },

    // Pretty-print a function id for the history modal. Falls back to
    // the raw id if we hit something un-mapped (defensive — a new
    // function added on the backend will still render, just without
    // a friendly label).
    webhookFnDisplayName(fn) {
      const map = {
        recover: 'Recover',
        discover: 'Discover',
        tagReleaseGroups: 'Tag quality releases',
        tagAudio: 'Tag Audio',
        tagVideo: 'Tag Video',
        tagDvDetail: 'Tag DV Detail',
        syncToSecondary: 'Sync to secondary',
        grabRename: 'Grab Rename',
        qbitSeTag: 'qBit S/E tag',
        qbitCategoryFix: 'qBit Category Fix',
        fileDeleteClean: 'File-delete strip',
        autoStripTagRgOnDelete: 'Auto-strip on delete',
      };
      return map[fn] || fn || '(unknown)';
    },

    // Icon character per status bucket. Plain unicode so we don't add
    // an icon-font dependency. ▲ = something changed, ✓ = checked and
    // OK / no diff, ⚠ = skipped with a reason, ✕ = error.
    webhookFnResultIcon(status) {
      switch (status) {
        case 'change': return '▲';      // ▲
        case 'change-add': return '▲';  // ▲
        case 'change-remove': return '▼'; // ▼
        case 'noop': return '✓';        // ✓
        case 'skipped': return '⚠';     // ⚠
        case 'error': return '✕';       // ✕
        default: return '·';            // ·
      }
    },

    // Background + text color tokens for each status. Returns an
    // object that Alpine binds via :style — values come from the
    // existing tokens.css palette so dark/light themes stay aligned.
    // Mirror .pill.* mappings in components.css: green/red use alpha-,
    // gray uses bg-muted (no alpha-gray exists), amber maps to orange.
    webhookFnResultColors(status) {
      switch (status) {
        case 'change':
        case 'change-add':
          return { bg: 'var(--alpha-green)', fg: 'var(--accent-green)' };
        case 'change-remove':
          return { bg: 'var(--alpha-orange)', fg: 'var(--accent-orange)' };
        case 'noop':
          return { bg: 'var(--bg-muted)', fg: 'var(--text-secondary)' };
        case 'skipped':
          return { bg: 'var(--alpha-orange)', fg: 'var(--accent-orange)' };
        case 'error':
          return { bg: 'var(--alpha-red)', fg: 'var(--accent-red)' };
        default:
          return { bg: 'var(--bg-muted)', fg: 'var(--text-muted-secondary)' };
      }
    },

    // ---- Grab Rename — special history-row helpers ---------------
    //
    // Grab Rename's Summary shape from webhook_grab_rename.go is:
    //   renamed "<from>" → "<to>" (triggers: <reason1>, <reason2>, ...)
    // The generic chip renderer would put the whole thing in a 200+
    // char green pill — too loud, hard to scan. These helpers tear
    // it apart so the modal can render a compact `renamed` chip + a
    // plain-language trigger list + a token-level from→to diff.

    // Pulls "from" + "to" out of "renamed \"<from>\" → \"<to>\"".
    // Returns null on shape mismatch (legacy / unexpected) so the
    // template falls back to the generic chip layout.
    parseGrabRenameNames(result) {
      if (!result || typeof result !== 'string') return null;
      const m = result.match(/^renamed\s+"([^"]*)"\s+→\s+"([^"]*)"\s*$/);
      if (!m) return null;
      return { from: m[1], to: m[2] };
    },

    // Pulls the trigger reason list out of detail "triggers: a, b, c".
    // Reasons can themselves contain ", " inside parens (e.g.
    // "missing-release-group (parser rejected: multi-token)") so we
    // split on top-level commas only — tiny depth-counter walk.
    parseGrabRenameTriggers(detail) {
      if (!detail || typeof detail !== 'string') return [];
      const m = detail.match(/^triggers:\s*(.+)$/);
      if (!m) return [];
      const out = [];
      let depth = 0, cur = '';
      for (const ch of m[1]) {
        if (ch === '(') depth++;
        else if (ch === ')') depth = Math.max(0, depth - 1);
        if (ch === ',' && depth === 0) {
          if (cur.trim()) out.push(cur.trim());
          cur = '';
        } else {
          cur += ch;
        }
      }
      if (cur.trim()) out.push(cur.trim());
      return out;
    },

    // Translate one raw trigger label into plain language. The label
    // vocabulary lives in evaluateGrabRenameTriggers
    // (webhook_grab_rename.go) — keep this map in sync when new
    // triggers are added. Unknown labels render verbatim so we never
    // hide a reason from the user.
    humanizeGrabRenameTrigger(t) {
      if (!t) return '';
      if (t === 'always-rename') return 'Always-rename setting is on';
      let m = t.match(/^missing-release-group \(parser rejected: (.+)\)$/);
      if (m) {
        // The user-facing phrasing here matters — "multi-token" /
        // "split-fragment" are parser-internal vocab. What the user
        // wants to know: "is my filename actually missing a -RG
        // suffix?" — so every reason explains the WHY in terms of
        // what the parser saw + what shape a valid RG would take.
        const reasonMap = {
          'no-hyphen': 'filename has no hyphen at all, so there is no "-RG" suffix to read',
          'empty': 'filename ends with a hyphen and nothing after it',
          'multi-token': 'filename does not end with a single release-group tag like "-TOLS" (text after the last hyphen had spaces or dots, so it looked like part of the title — not a group)',
          'codec': 'text after the last hyphen looked like a codec (h264 / h265), not a release-group',
          'split-fragment': 'text after the last hyphen looked like part of a hyphenated token (DL from WEB-DL, HD from DTS-HD) — not a real release-group',
          'resolution': 'text after the last hyphen looked like a resolution (1080p / 2160p), not a release-group',
        };
        return 'Release group missing — ' + (reasonMap[m[1]] || m[1]);
      }
      m = t.match(/^missing-release-group \(parsed="(.*)" expected="(.*)"\)$/);
      if (m) return 'Release group mismatch — filename has "' + m[1] + '", grab said "' + m[2] + '"';
      m = t.match(/^movie-version: (.+)$/);
      if (m) return 'Edition / version tokens missing: ' + m[1].split('/').join(', ');
      m = t.match(/^source: (.+)$/);
      if (m) return 'Source tokens missing: ' + m[1].split('/').join(', ');
      m = t.match(/^audio: (.+)$/);
      if (m) return 'Audio tokens missing: ' + m[1].split('/').join(', ');
      if (t === 'scene-stripped (rg not a known scene group)') {
        return 'Looks scene-stripped (release group is not a known scene group)';
      }
      m = t.match(/^custom: (.+)$/);
      if (m) return 'Custom tokens missing: ' + m[1].split('/').join(', ');
      return t;
    },

    // Token-level set diff between two names. Tokens unique to `from`
    // are flagged removed (rendered red + strike-through), tokens
    // unique to `to` are flagged added (rendered green + bold). Common
    // tokens stay neutral. Case-insensitive comparison so "DV" vs "dv"
    // reads as same. Order is preserved so the line still reads as
    // the original name.
    grabRenameDiffTokens(from, to) {
      const fromTokens = (from || '').split(/\s+/).filter(Boolean);
      const toTokens = (to || '').split(/\s+/).filter(Boolean);
      const fromSet = new Set(fromTokens.map(t => t.toLowerCase()));
      const toSet = new Set(toTokens.map(t => t.toLowerCase()));
      return {
        from: fromTokens.map(t => ({ text: t, removed: !toSet.has(t.toLowerCase()) })),
        to: toTokens.map(t => ({ text: t, added: !fromSet.has(t.toLowerCase()) })),
      };
    },

    // Open the rule-fire history modal for this rule. Binds to the
    // already-loaded rule object so re-entry of the modal post-fire
    // (after Refresh) shows fresh entries. closeWebhookRuleHistory
    // is the inverse.
    openWebhookRuleHistory(rule) {
      this.webhookRuleHistoryRule = rule;
      this.webhookRuleHistoryExpanded = {};
      this.webhookRuleHistoryOpen = true;
    },
    closeWebhookRuleHistory() {
      this.webhookRuleHistoryOpen = false;
      this.webhookRuleHistoryRule = null;
      this.webhookRuleHistoryExpanded = {};
    },

    // Toggle expand/collapse for one run card. Reassigns the object
    // so Alpine's reactivity picks up the change reliably (deleting a
    // prop in-place is tracked by the Proxy but reassigning is the
    // belt-and-braces version).
    toggleWebhookRuleRun(key) {
      const next = { ...this.webhookRuleHistoryExpanded };
      if (next[key]) delete next[key]; else next[key] = true;
      this.webhookRuleHistoryExpanded = next;
    },

    // Tally per-status counts from the parsed summary so the
    // collapsed card header can show "3 changes" / "1 error" / "no
    // changes" at a glance. Errors are tracked separately so they
    // can shout louder than mere changes.
    webhookRuleRunCounts(run) {
      const rows = this.parseRuleRunSummary(run && run.summary);
      let changes = 0, errors = 0, skipped = 0, noop = 0;
      for (const r of rows) {
        if (r.status === 'error') errors++;
        else if (r.status === 'change' || r.status === 'change-add' || r.status === 'change-remove') changes++;
        else if (r.status === 'skipped') skipped++;
        else if (r.status === 'noop') noop++;
      }
      return { changes, errors, skipped, noop, total: rows.length };
    },

    // Single chip text for the collapsed header. Errors-first so the
    // user notices them before the change count. "no changes" is the
    // catch-all for runs where every function was skipped or noop.
    webhookRuleRunHeadlineLabel(run) {
      const c = this.webhookRuleRunCounts(run);
      if (c.errors > 0) return c.errors === 1 ? '1 error' : c.errors + ' errors';
      if (c.changes > 0) return c.changes === 1 ? '1 change' : c.changes + ' changes';
      return 'no changes';
    },

    // Color bucket for the headline chip — mirrors webhookFnResult
    // Colors so the same green/red/gray vocabulary applies at the
    // card level.
    webhookRuleRunHeadlineColors(run) {
      const c = this.webhookRuleRunCounts(run);
      if (c.errors > 0) return { bg: 'var(--alpha-red)', fg: 'var(--accent-red)' };
      if (c.changes > 0) return { bg: 'var(--alpha-green)', fg: 'var(--accent-green)' };
      return { bg: 'var(--bg-muted)', fg: 'var(--text-secondary)' };
    },

    // Re-fetch /api/webhook-rules so a freshly-fired run shows up
    // without closing + reopening the modal. We don't have an SSE
    // channel for rule fires today (only /api/webhook/events for
    // raw deliveries) — manual refresh keeps it lightweight. After
    // the load, re-bind the modal to the freshly-loaded rule object
    // so the History[] reactive read picks up new entries; if the
    // rule was deleted server-side mid-poll the modal closes.
    async refreshWebhookRuleHistory() {
      if (!this.webhookRuleHistoryRule) return;
      this.webhookRuleHistoryRefreshing = true;
      try {
        const ruleId = this.webhookRuleHistoryRule.id;
        await this.loadWebhookRules();
        const fresh = (this.webhookRules || []).find(r => r.id === ruleId);
        if (fresh) {
          this.webhookRuleHistoryRule = fresh;
        } else {
          // Rule deleted underneath us — close out cleanly.
          this.closeWebhookRuleHistory();
          this.showToast('Rule no longer exists', 'error');
        }
      } finally {
        this.webhookRuleHistoryRefreshing = false;
      }
    },

    // Confirm + delete a webhook rule. Uses a lightweight inline
    // confirm via the browser dialog for now — a styled modal can
    // come in polish.
    async confirmDeleteWebhookRule(rule) {
      if (!await this.confirmDialog({
        title:       'Delete rule "' + rule.name + '"?',
        message:     'This cannot be undone. The rule stops firing immediately. Webhook URL + delivery stay configured.',
        confirmText: 'Delete',
        kind:        'danger',
      })) return;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + rule.id, { method: 'DELETE' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadWebhookRules();
        this.showToast('Rule deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

    // ---- Per-rule webhook URL modal (M-per-rule-webhook Slice 5) ----
    //
    // Lets the user generate / view / rotate / disable a dedicated
    // webhook URL for ONE rule. When configured, Sonarr/Radarr Connect
    // points at this rule's URL and only this rule fires from it (the
    // instance dispatcher excludes it per Slice 3). When not configured,
    // the rule fires via the shared instance URL alongside siblings.

    openPerRuleWebhookModal(rule) {
      this.perRuleWebhookRule = rule;
      this.perRuleWebhookData = null;
      this.perRuleWebhookShowCurl = false;
      this.perRuleWebhookConfirmDisable = false;
      this.perRuleWebhookOpen = true;
      this.loadPerRuleWebhookConfig();
    },

    closePerRuleWebhookModal() {
      if (this.perRuleWebhookActionInFlight) return;
      this.perRuleWebhookOpen = false;
      this.perRuleWebhookRule = null;
      this.perRuleWebhookData = null;
      this.perRuleWebhookShowCurl = false;
      this.perRuleWebhookConfirmDisable = false;
    },

    async loadPerRuleWebhookConfig() {
      if (!this.perRuleWebhookRule) return;
      this.perRuleWebhookLoading = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.perRuleWebhookData = d;
      } catch (e) {
        this.showToast('Load webhook config failed: ' + e.message, 'error');
        this.perRuleWebhookData = null;
      } finally {
        this.perRuleWebhookLoading = false;
      }
    },

    // Generate a fresh Token + Secret. Idempotent — clicking again on
    // an already-configured rule rotates the URL. RequireSignature
    // preserved across rotations (matches instance-rotate semantics).
    async doGeneratePerRuleWebhook() {
      if (!this.perRuleWebhookRule) return;
      const alreadyConfigured = !!(this.perRuleWebhookData && this.perRuleWebhookData.token);
      if (alreadyConfigured) {
        if (!await this.confirmDialog({
          title:       'Rotate the webhook URL?',
          message:     'The old URL stops working immediately — Sonarr/Radarr will start getting 404s until you paste the new URL into Connect. Use this only if you suspect the URL has leaked.',
          confirmText: 'Rotate',
          kind:        'warning',
        })) return;
      }
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook/generate', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.perRuleWebhookData = d;
        this.showToast(alreadyConfigured ? 'URL rotated — paste the new one into Sonarr/Radarr' : 'URL generated — paste it into Sonarr/Radarr Connect', 'success');
        await this.loadWebhookRules(); // refresh the card badge
      } catch (e) {
        this.showToast('Generate failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Rotate Secret only — keeps Token + URL stable so user doesn't
    // have to re-paste the URL in Sonarr/Radarr.
    async doRotatePerRuleWebhookSecret() {
      if (!this.perRuleWebhookRule) return;
      if (!await this.confirmDialog({
        title:       'Rotate the webhook secret?',
        message:     'The URL stays the same but you need to paste the new Secret as the Webhook password in Sonarr/Radarr.',
        confirmText: 'Rotate',
        kind:        'warning',
      })) return;
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast('Secret rotated', 'success');
        await this.loadPerRuleWebhookConfig();
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Flip strict-mode (RequireSignature) on/off. Backend rejects
    // enable=true when Secret is empty.
    async setPerRuleWebhookRequireSignature(enabled) {
      if (!this.perRuleWebhookRule) return;
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook/require-signature', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast(enabled ? 'Strict mode on — events without the Secret will be rejected' : 'Strict mode off — unsigned events accepted with a warning', 'success');
        await this.loadPerRuleWebhookConfig();
      } catch (e) {
        this.showToast('Setting failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Disable per-rule URL — rule reverts to instance-URL routing
    // (sibling rules will share the event with it again). Two-step
    // confirm via perRuleWebhookConfirmDisable so accidental clicks
    // don't kill a configured URL.
    async doDisablePerRuleWebhook() {
      if (!this.perRuleWebhookRule) return;
      if (!this.perRuleWebhookConfirmDisable) {
        this.perRuleWebhookConfirmDisable = true;
        return;
      }
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook', { method: 'DELETE' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast('Per-rule URL disabled — rule fires via the instance URL again', 'success');
        await this.loadPerRuleWebhookConfig();
        await this.loadWebhookRules();
        this.perRuleWebhookConfirmDisable = false;
      } catch (e) {
        this.showToast('Disable failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Computed state label for the modal header. Three states:
    //  - "Not configured" — no Token yet; CTA is Generate
    //  - "Active" — Token present; rule fires via dedicated URL
    perRuleWebhookStateLabel() {
      const d = this.perRuleWebhookData;
      if (!d) return { text: 'Loading…', color: 'var(--text-muted)' };
      if (!d.token) return { text: 'Not configured (rule uses the instance URL)', color: 'var(--text-secondary)' };
      return { text: 'Active — Sonarr/Radarr Connect should point at this URL', color: 'var(--accent-green)' };
    },

    // Build the curl-style example for the rule. Same shape as the
    // qBit-webhook curl helper but for Sonarr/Radarr Connect → URL +
    // Basic-auth password.
    perRuleWebhookCurlExample() {
      const d = this.perRuleWebhookData;
      if (!d || !d.url) return '';
      const auth = d.requireSignature
        ? ' \\\n  -H "Authorization: Basic $(echo -n \'resolvarr:' + (d.secret || '') + '\' | base64)"'
        : '';
      return 'curl -fsS -X POST ' + JSON.stringify(d.url) + auth + ' \\\n  -H "Content-Type: application/json" \\\n  -d \'{"eventType":"Test"}\'';
    },

    async copyPerRuleWebhookURL() {
      const d = this.perRuleWebhookData;
      if (!d || !d.url) return;
      const ok = await this.copyToClipboard(d.url);
      this.showToast(ok ? 'URL copied — paste into Sonarr/Radarr Connect → Webhook URL' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    async copyPerRuleWebhookSecret() {
      const d = this.perRuleWebhookData;
      if (!d || !d.secret) return;
      const ok = await this.copyToClipboard(d.secret);
      this.showToast(ok ? 'Secret copied — paste as the Webhook password in Sonarr/Radarr' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    // ---- qBit S/E backlog scan modal ------------------------------
    //
    // Entry: per-rule "Backlog scan" button on the Webhooks Setup tab.
    // Visible only on rules carrying the qbitSeTag function. The modal
    // walks the rule's qBit instance, classifies each torrent name via
    // engine.DetermineQbitTag (Episode → Season → Unmatched first-
    // match-wins), and shows the user what the apply pass would do.
    //
    // Phase 1 (initial): Run preview button + optional category filter
    // Phase 2 (preview loaded): per-row checkbox table + Apply selected
    // Phase 3 (apply complete): summary of Applied / Failed
    //
    // Per-row apply selection: SelectedHashes is sent to the backend
    // so unchecked rows are skipped by the apply pass (backend gate
    // added in qbit_se_backlog.go in the same change).

    // openQbitSeBacklog clears every modal-scoped state field then
    // opens the modal in Phase 1. Keep state reset coupled to open()
    // — opening a fresh scan must not leak Apply results from the
    // previous rule.
    openQbitSeBacklog(rule) {
      this.qbitSeBacklogRule = rule;
      this.qbitSeBacklogOpen = true;
      this.qbitSeBacklogPreview = null;
      this.qbitSeBacklogApplyResult = null;
      this.qbitSeBacklogSelected = {};
      this.qbitSeBacklogCategoryFilter = '';
      this.qbitSeBacklogFilter = 'taggable';
      this.qbitSeBacklogError = '';
    },

    closeQbitSeBacklog() {
      this.qbitSeBacklogOpen = false;
      this.qbitSeBacklogRule = null;
      this.qbitSeBacklogPreview = null;
      this.qbitSeBacklogApplyResult = null;
      this.qbitSeBacklogSelected = {};
      this.qbitSeBacklogError = '';
    },

    // Run the preview pass. Pre-selects every taggable row by
    // default; already-tagged + skipped rows start unchecked (the
    // user can flip them on if they want, though already-tagged is
    // a no-op on apply and skipped has no proposed tag).
    async runQbitSeBacklogPreview() {
      if (!this.qbitSeBacklogRule) return;
      // Re-entry guard — the disabled binding on the button already
      // handles UI-layer double-clicks but defence-in-depth at the
      // function boundary catches Alpine error-recovery retries.
      if (this.qbitSeBacklogLoading) return;
      this.qbitSeBacklogLoading = true;
      this.qbitSeBacklogError = '';
      // Clear any previous apply result — re-running preview after
      // an apply means the user wants to see the fresh state, not
      // stale results.
      this.qbitSeBacklogApplyResult = null;
      try {
        const body = {
          ruleId: this.qbitSeBacklogRule.id,
          categoryFilter: (this.qbitSeBacklogCategoryFilter || '').trim(),
        };
        const r = await this.apiFetch('/api/webhook-rules/qbit-se-backlog/preview', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        const data = await r.json();
        this.qbitSeBacklogPreview = data;
        // Pre-select every row that the apply pass would actually
        // touch — taggable, has a proposed tag, no skip reason.
        const sel = {};
        for (const item of (data.items || [])) {
          sel[item.hash] = !item.alreadyTagged && !!item.proposedTag && !item.skipReason;
        }
        this.qbitSeBacklogSelected = sel;
        this.showToast('Preview complete: ' + (data.totalTaggable || 0) + ' taggable, ' + (data.totalAlreadyOk || 0) + ' already OK', 'success');
      } catch (e) {
        this.qbitSeBacklogError = String(e.message || e);
        this.showToast('Preview failed: ' + this.qbitSeBacklogError, 'error');
      } finally {
        this.qbitSeBacklogLoading = false;
      }
    },

    // Run the apply pass. Sends the SelectedHashes set so unchecked
    // rows stay untouched. Backend gate in runQbitSeBacklogScan
    // honours the filter; without it apply would tag every taggable
    // item regardless of UI selection.
    async runQbitSeBacklogApply() {
      if (!this.qbitSeBacklogRule || !this.qbitSeBacklogPreview) return;
      if (this.qbitSeBacklogApplying) return;
      const selected = Object.keys(this.qbitSeBacklogSelected || {})
        .filter(h => this.qbitSeBacklogSelected[h]);
      if (selected.length === 0) {
        this.showToast('No torrents selected', 'error');
        return;
      }
      this.qbitSeBacklogApplying = true;
      this.qbitSeBacklogError = '';
      try {
        const body = {
          ruleId: this.qbitSeBacklogRule.id,
          categoryFilter: (this.qbitSeBacklogCategoryFilter || '').trim(),
          selectedHashes: selected,
        };
        const r = await this.apiFetch('/api/webhook-rules/qbit-se-backlog/apply', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        this.qbitSeBacklogApplyResult = await r.json();
        const applied = this.qbitSeBacklogApplyResult.applied || 0;
        const failed = this.qbitSeBacklogApplyResult.failed || 0;
        if (failed > 0) {
          this.showToast('Apply finished: ' + applied + ' tagged, ' + failed + ' failed', 'error');
        } else {
          this.showToast('Apply complete: ' + applied + ' torrent' + (applied === 1 ? '' : 's') + ' tagged', 'success');
        }
      } catch (e) {
        this.qbitSeBacklogError = String(e.message || e);
        this.showToast('Apply failed: ' + this.qbitSeBacklogError, 'error');
      } finally {
        this.qbitSeBacklogApplying = false;
      }
    },

    // Filter helper — drives the chip row in Phase 2. Re-derives
    // every render; no caching because preview Items[] is short.
    qbitSeBacklogVisibleItems() {
      const p = this.qbitSeBacklogPreview;
      if (!p || !p.items) return [];
      switch (this.qbitSeBacklogFilter) {
        case 'taggable':
          return p.items.filter(i => !i.alreadyTagged && i.proposedTag && !i.skipReason);
        case 'alreadyOk':
          return p.items.filter(i => i.alreadyTagged);
        case 'skipped':
          return p.items.filter(i => i.skipReason || !i.proposedTag);
        default:
          return p.items;
      }
    },

    // Selected-count helper — drives the "Apply selected (N)" button
    // label + disabled binding. Counts only rows the apply pass would
    // actually touch (taggable + has proposed tag + no skip reason);
    // already-tagged rows are silently no-ops on the backend, and a
    // legacy bug let stale checkbox state on those rows inflate the
    // button label so the user saw "(50)" but only 12 were applied.
    qbitSeBacklogSelectedCount() {
      const sel = this.qbitSeBacklogSelected || {};
      const items = (this.qbitSeBacklogPreview && this.qbitSeBacklogPreview.items) || [];
      let n = 0;
      for (const item of items) {
        if (item.alreadyTagged || !item.proposedTag || item.skipReason) continue;
        if (sel[item.hash]) n++;
      }
      return n;
    },

    // Format the parsed S/E for display. Empty parsed → em-dash.
    // Joins multi-episode packs with E (S01E05E06) to match Sonarr's
    // own convention. The (season=0, episodes=[…]) edge case falls
    // back to em-dash because S00E… is not a meaningful Sonarr token
    // for backlog-tagging purposes.
    qbitSeBacklogParsedLabel(item) {
      if (!item) return '—';
      const s = item.parsedSeason || 0;
      const eps = item.parsedEpisodes || [];
      if (s === 0 && eps.length === 0) return '—';
      if (s === 0 && eps.length > 0) return '—';
      const sPart = 'S' + String(s).padStart(2, '0');
      if (eps.length === 0) return sPart;
      return sPart + eps.map(e => 'E' + String(e).padStart(2, '0')).join('');
    },

    // Convenience getter for "every taggable visible-row is checked"
    // — used by the Phase-2 select-all checkbox in the table header.
    // Already-OK + skipped rows are excluded from both the "all on?"
    // calculation and the toggle action; their checkbox is hidden in
    // the row template, so attempting to bulk-set them would be a
    // no-op the user can't undo from the UI.
    qbitSeBacklogAllTaggableSelected() {
      const items = this.qbitSeBacklogVisibleItems();
      const taggable = items.filter(i => !i.alreadyTagged && i.proposedTag && !i.skipReason);
      if (taggable.length === 0) return false;
      const sel = this.qbitSeBacklogSelected || {};
      return taggable.every(i => !!sel[i.hash]);
    },

    // Toggle every taggable visible-row's checkbox. Bound to the
    // header checkbox; respects the active filter so "select all"
    // only touches what the user can see, and skips already-OK +
    // skipped rows because the apply pass would no-op them anyway.
    qbitSeBacklogToggleAll() {
      const items = this.qbitSeBacklogVisibleItems();
      const taggable = items.filter(i => !i.alreadyTagged && i.proposedTag && !i.skipReason);
      if (taggable.length === 0) return;
      const sel = { ...(this.qbitSeBacklogSelected || {}) };
      const allOn = taggable.every(i => !!sel[i.hash]);
      for (const i of taggable) sel[i.hash] = !allOn;
      this.qbitSeBacklogSelected = sel;
    },

    // Resolve the qBit instance referenced by a webhook rule's QbitSe
    // criteria. Returns null when the rule has no QbitSe block, no
    // QbitInstanceID, or the referenced instance has been removed
    // from config. Drives the Backlog-scan button's disabled-state +
    // tooltip — see the per-rule button binding in index.html.
    qbitInstanceForRule(rule) {
      const id = rule && rule.qbitSe && rule.qbitSe.qbitInstanceId;
      if (!id) return null;
      return (this.qbitInstances || []).find(q => q.id === id) || null;
    },

    // Quick fix-all chain dispatcher. Fires the rule's chain phases
    // sequentially through /api/scan/run with per-request overlays
    // (the wizard's filters / extra-tags / RG-IDs). Each phase's
    // response gets collected into quickFixResults so the user can
    // see the combined summary after the modal closes.
    //
    // Phase order is fixed (Discover → Recover → Tag → ExtraTags) to
    // match scheduler_runner.go's runCombinedSchedule. Skipped phases
    // (not in combinedModes for combined mode, or not the rule's own
    // mode for single-mode) are silently dropped.
    async runQuickFixChain(overrideRule) {
      // Defensive re-entry guard. The disabled-button binding on
      // both Save and Apply-now already prevents UI-layer
      // double-clicks, but Alpine event-recovery has been observed
      // to retry a click handler under unusual conditions (browser
      // back-button, Alpine error-recovery cycles). Refusing to
      // re-enter at the function boundary is belt-and-braces — no
      // false positives because legitimate flows always wait for
      // ruleEditor.busy to clear before re-firing.
      if (this.ruleEditor.busy) return;
      // Default source is the wizard's editingRule. overrideRule lets
      // "Apply now" re-fire a previous preview run without reopening
      // the wizard (Apply-after-preview lives at the result panel).
      const r = overrideRule || this.editingRule;
      if (!r) return;
      // Remember last-used instance per action. Per-action wizards
      // (Tag Audio / Video / DV Details / Recover) carry fixedAction
      // — save under that key. QFA proper has no single action;
      // save under 'qfa' for the generic chain. Apply-now re-fire
      // path also lands here but doesn't re-persist (overrideRule
      // means user already chose this instance via the original
      // wizard run, no need to overwrite).
      if (!overrideRule && r.instanceId) {
        const action = (this.ruleEditor && this.ruleEditor.fixedAction) || 'qfa';
        this.rememberWizardInstance(action, r.instanceId);
      }
      // For Apply-after-preview we use the same UI busy flag — the
      // result panel reads it to disable the button while running.
      this.ruleEditor.busy = true;
      this.ruleEditor.error = '';

      // Decide which phases to run based on mode + combinedModes.
      // headPhases: instance-A-only (discover/recover/tag); they run
      // first against the rule's primary instance.
      // autoPhases: per-bucket targets dispatch into a second sub-chain
      // on the secondary instance when target includes 'secondary'.
      const has = (m) => r.mode === m || (r.mode === 'combined' && (r.options.combinedModes || []).includes(m));
      const headPhases = [];
      if (has('discover'))  headPhases.push('discover');
      if (has('recover'))   headPhases.push('recover');
      if (has('tag'))       headPhases.push('tag');
      const autoPhases = [];
      if (has('audiotags')) autoPhases.push({ phase: 'audiotags', target: r.options.audioTagsTarget || 'primary' });
      if (has('videotags')) autoPhases.push({ phase: 'videotags', target: r.options.videoTagsTarget || 'primary' });
      if (has('dvdetail'))  autoPhases.push({ phase: 'dvdetail',  target: r.options.dvDetailTarget  || 'primary' });
      // Missing-episodes is a Sonarr-only phase that uses dedicated
      // endpoints (/api/scan/missing-episodes/{preview,tag,search})
      // instead of the generic /api/scan/run path. Treated as its own
      // bucket so the headPhase / autoPhase loops don't need to
      // special-case it.
      const runMissingEpisodes = has('missingepisodes');
      // Plex sync runs LAST (after every tag-writing phase) via the
      // shared one-off endpoint. Its own bucket like missing-episodes.
      const runPlexSync = has('plexsync');
      // TBA refresh — Sonarr-only file rename. Preview + (apply-mode)
      // rename-all, via the same endpoints the standalone tab uses.
      const runTbaRefresh = has('tbarefresh');
      if (headPhases.length === 0 && autoPhases.length === 0 && !runMissingEpisodes && !runPlexSync && !runTbaRefresh) {
        this.ruleEditor.error = 'No phases to run';
        this.ruleEditor.busy = false;
        return;
      }

      // Reset cancel flag for the new run. cancelRunningChain() sets
      // this and the loop's isCancelled() watches for it on each
      // iteration so subsequent phases are skipped.
      this.chainCancelRequested = false;

      // Overlay payload — every phase's request body carries these so
      // the backend uses the wizard's snapshot, not globals.
      const overlay = {
        overlayFilters: { ...r.filters },
        overlayReleaseGroupIds: [...(r.releaseGroupIds || [])],
      };
      if (r.audioTags) overlay.overlayAudioTags = JSON.parse(JSON.stringify(r.audioTags));
      if (r.videoTags) overlay.overlayVideoTags = JSON.parse(JSON.stringify(r.videoTags));
      if (r.dvDetail)  overlay.overlayDvDetail  = JSON.parse(JSON.stringify(r.dvDetail));

      // primaryType resolves below the results object — pre-compute
      // it here so the appType tag goes on the result up-front. Used
      // by the result-panel's x-show to scope rendering to the
      // currently-active scanAppType (Radarr results stay hidden
      // when the user flips to Sonarr context, and vice versa).
      const primaryType = (this.instances.find(i => i.id === r.instanceId) || {}).type;
      const results = {
        startedAt: new Date().toISOString(),
        instance: r.instanceId,
        appType: primaryType, // 'radarr' | 'sonarr' | undefined
        phases: [],
        // Stash the rule snapshot used for this run so the result
        // panel's "Apply now" button can re-fire with the same
        // settings + flipped runMode. Deep-cloned so subsequent
        // wizard edits can't mutate it.
        ruleSnapshot: JSON.parse(JSON.stringify(r)),
      };
      const runMode = r.options.runMode === 'preview' ? 'preview' : 'apply';
      results.runMode = runMode;

      // Resolve secondary instance ID once — used by tag-sync AND
      // extratags-on-secondary. Same logic the backend uses when
      // syncToInstanceId is empty: pick the first other-of-same-type.
      // primaryType already resolved above for the appType tag on
      // the results object — reuse it.
      const secondaryTarget = r.options.syncToInstanceId ||
        (this.instances.find(i => i.type === primaryType && i.id !== r.instanceId) || {}).id;

      // Single fetch helper — phases call this once for primary, and
      // extratags optionally a second time for secondary. The dvdetail
      // phase is the only slow one (per-file ffmpeg+dovi_tool extract);
      // we start the global progress poll around it so the floating
      // DV-scan banner surfaces during chain runs the same way it does
      // for standalone DV scans. Backend's dvScanMu enforces single
      // in-flight, so the poll never races with another scan.
      const fetchPhase = async (phase, instanceId) => {
        const body = {
          instanceId,
          action: phase,
          mode: phase === 'discover' ? 'preview' : runMode,
          ...overlay,
        };
        if (phase === 'discover') {
          // Pass the rule's auto-add settings through to the backend
          // so /api/scan/run's runDiscover can persist new groups
          // when the user picked "Add to config" in the wizard. Same
          // contract as the schedule-runner; both paths converge on
          // applyDiscoverWriteBack server-side.
          //
          // Master-mode gate: when the wizard is in preview, the WHOLE
          // chain is read-only — discover write-back doesn't fire even
          // if the rule has it enabled. The backend forces Mode=preview
          // for discover regardless, so this gate is the only place
          // that can distinguish preview-of-chain from standalone-discover.
          // Standalone Discover (Release Groups → Find new groups) keeps
          // the user's explicit "+ Add" / "+ Add Selected" flow; this
          // only affects the chain-orchestrated auto-add path.
          body.discoverWriteBack = !!r.options.discoverWriteBack && runMode === 'apply';
          body.autoActivateDiscovered = !!r.options.autoActivateDiscovered;
        }
        if (phase === 'tag') {
          body.cleanupUnusedTags = !!r.options.cleanupUnusedTags;
          if (r.options.syncToSecondary && secondaryTarget) {
            body.syncToInstanceId = secondaryTarget;
          }
          // Pass-through tag-source + filter-only tag so the backend
          // routes to runTagFilterOnly when the user picked "Use
          // filter only" on the RG step. Without this the request
          // falls into runTag (per-group) and errors with
          // "no release groups configured" when Active is empty.
          // Empty / "active" / "discover" all hit the legacy runTag
          // path — only "filter-only" branches.
          if (r.options.tagSource) {
            body.tagSource = r.options.tagSource;
            if (r.options.tagSource === 'filter-only') {
              body.filterOnlyTag = r.options.filterOnlyTag || '';
              // Cleanup-tail is no-op in filter-only by design (one
              // rule, one tag, no orphan candidates) — strip the
              // flag so the backend doesn't try to compute it.
              delete body.cleanupUnusedTags;
            }
          }
        }
        if (phase === 'recover') body.recoverRename = true;

        const isDv = phase === 'dvdetail';
        if (isDv) {
          // Per-rule cache bypass — wizard "Skip DV cache on every fire"
          // checkbox lives on the DV step. Schedule runner does the same
          // translation server-side from JobOptions.BypassDvCache; the
          // QFA chain is the live-UI mirror of that path.
          body.bypassDvCache = !!r.options.bypassDvCache;
          this.dvScanProgress = { running: true, total: 0, processed: 0, currentTitle: '' };
          this.startDvScanPoll();
        }
        try {
          const res = await this.apiFetch('/api/scan/run', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          });
          if (!res.ok) {
            const d = await res.json().catch(() => ({}));
            throw new Error(`${phase}: ${d.error || 'HTTP ' + res.status}`);
          }
          return await res.json();
        } finally {
          if (isDv) {
            this.stopDvScanPoll();
            this.dvScanProgress = null;
          }
        }
      };

      // Cancel-mid-chain check: only applies in the wizard flow, where
      // `editingRule` is the live wizard state. The Apply-after-preview
      // override flow runs without ever opening the wizard (editingRule
      // is null by design), so we'd false-trip the break and abort
      // every override run on phase 0. Use a closure-bound flag instead.
      // Two cancel signals: (1) wizard close while a wizard-driven
      // chain is mid-flight clears editingRule (Apply-after-preview
      // sets overrideRule and is exempt — closing the wizard there
      // mustn't false-trip a chain that has nothing to do with the
      // wizard); (2) chainCancelRequested is the explicit "Cancel"
      // button signal, valid for both flows.
      const isCancelled = () => this.chainCancelRequested || (!overrideRule && !this.editingRule);
      try {
        // Phase 1 — head phases (discover / recover / tag).
        // Discover and Tag run on primary only — both have one-instance
        // semantics (Discover finds new groups, Tag-sync mirrors tag
        // decisions to secondary via TmdbID inside the tag phase).
        // Recover walks each instance's own movie/episode files
        // independently — no shared state, no mirror — so it honors
        // recoverTarget the same way auto-tag phases honor their per-
        // bucket target. When target='both' (or 'secondary'), recover
        // runs an additional pass on the secondary instance after the
        // primary pass, before the tag phase. Result rows carry the
        // per-pass instanceId so the variant switcher in the result
        // modal can flip between them.
        const recoverTarget = r.options.recoverTarget || 'primary';
        for (const phase of headPhases) {
          if (isCancelled()) break;
          if (phase === 'recover') {
            // Primary pass — fires when target includes primary.
            if (recoverTarget === 'primary' || recoverTarget === 'both') {
              const data = await fetchPhase(phase, r.instanceId);
              results.phases.push({ phase, ok: true, response: data, instanceId: r.instanceId });
            }
            // Secondary pass — fires when target includes secondary
            // AND a secondary instance is actually configured. Defence
            // in depth: the wizard hides secondary/both options when
            // none is available, so this guard catches legacy rules
            // saved when a secondary existed and was later removed.
            if ((recoverTarget === 'secondary' || recoverTarget === 'both') && secondaryTarget) {
              if (isCancelled()) break;
              const data = await fetchPhase(phase, secondaryTarget);
              results.phases.push({ phase, ok: true, response: data, instanceId: secondaryTarget, instanceLabel: 'secondary' });
            }
            continue;
          }
          const data = await fetchPhase(phase, r.instanceId);
          results.phases.push({ phase, ok: true, response: data, instanceId: r.instanceId });

          // Discover write-back wiring (apply mode): when this phase
          // added new release groups, fold their IDs into the overlay
          // used by subsequent phases. Without this, the next phase's
          // overlay still carries the rule's pre-run RG-ID snapshot —
          // which doesn't include the just-added groups — and the Tag
          // phase silently skips them.
          if (phase === 'discover' && data && data.applied && Array.isArray(data.applied.discoverAdded) && data.applied.discoverAdded.length > 0) {
            const ids = (overlay.overlayReleaseGroupIds || []).slice();
            for (const a of data.applied.discoverAdded) {
              if (a && a.id) ids.push(a.id);
            }
            overlay.overlayReleaseGroupIds = ids;
          }

          // Discover ephemeral injection (preview mode): in preview the
          // backend doesn't write to config, so subsequent phases (Tag)
          // would see zero groups. Inject discover's findings as
          // ephemeral groups — live only for this run; backend never
          // persists them.
          if (phase === 'discover' && runMode === 'preview' && data && Array.isArray(data.discovered) && data.discovered.length > 0) {
            const inject = (overlay.overlayInjectGroups || []).slice();
            const seen = new Set(inject.map(g => g.search.toLowerCase()));
            for (let i = 0; i < data.discovered.length; i++) {
              const d = data.discovered[i];
              const search = d.search || '';
              if (!search || seen.has(search.toLowerCase())) continue;
              seen.add(search.toLowerCase());
              inject.push({
                id: 'ephemeral-' + i,
                search: search,
                tag: search.toLowerCase(),
                display: search,
                mode: 'filtered',
                type: primaryType,
                enabled: true,
              });
            }
            overlay.overlayInjectGroups = inject;
          }
        }

        // Phase 2 — auto-tag sub-chains. Each auto phase
        // (audiotags / videotags / dvdetail) carries its own per-bucket
        // target (primary | secondary | both). Run primary's enabled
        // auto phases first as a contiguous group, then secondary's —
        // matches the user model "finish chain on instance A, then
        // chain on instance B". Token allow-lists are universal: same
        // overlay payload is used for both runs.
        const runOnInstance = async (instanceId, instanceLabel, includeForTarget) => {
          for (const a of autoPhases) {
            if (!includeForTarget(a.target)) continue;
            if (isCancelled()) break;
            const data = await fetchPhase(a.phase, instanceId);
            const row = { phase: a.phase, ok: true, response: data, instanceId };
            if (instanceLabel) row.instanceLabel = instanceLabel;
            results.phases.push(row);
          }
        };
        // A-chain: auto phases targeting primary (target = 'primary' OR 'both').
        await runOnInstance(r.instanceId, null, t => t === 'primary' || t === 'both');
        // B-chain: auto phases targeting secondary (target = 'secondary' OR 'both').
        // Skipped silently when no secondary instance configured — the
        // wizard's target picker hides 'secondary' / 'both' options in
        // that case, so this guard is defence-in-depth for legacy rules
        // saved when a secondary existed and later removed.
        if (secondaryTarget) {
          await runOnInstance(secondaryTarget, 'secondary', t => t === 'secondary' || t === 'both');
        }

        // Phase 3 — missing-episodes (Sonarr only). Uses dedicated
        // endpoints rather than the generic /api/scan/run path.
        // Sequence:
        //   1. /preview always — surfaces the gaps + series list.
        //   2. Apply mode: invoke /tag (when actionTag) and/or /search
        //      (when actionSearch). Preview mode: skip the writes.
        // Result row carries the merged response so the result panel
        // can render the standard missing-episodes drill-in alongside
        // the rest of the chain phases.
        if (runMissingEpisodes && !isCancelled()) {
          const me = r.missingEpisodes || {};
          const phaseRow = { phase: 'missingepisodes', ok: true, instanceId: r.instanceId };
          try {
            const previewBody = {
              instanceId: r.instanceId,
              threshold: (me.thresholdPercent || 70) / 100,
              bufferHours: (me.bufferHours === undefined || me.bufferHours === null || me.bufferHours === '') ? 24 : Number(me.bufferHours),
              includeContinuing: !!me.includeContinuing,
              includeEnded: !!me.includeEnded,
              includeSpecials: !!me.includeSpecials,
            };
            const previewRes = await this.apiFetch('/api/scan/missing-episodes/preview', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(previewBody),
            });
            if (!previewRes.ok) {
              const d = await previewRes.json().catch(() => ({}));
              throw new Error(`missingepisodes: ${d.error || 'HTTP ' + previewRes.status}`);
            }
            const previewData = await previewRes.json();
            phaseRow.response = previewData;
            phaseRow.actionTag = !!me.actionTag;
            phaseRow.actionSearch = !!me.actionSearch;
            phaseRow.tagName = (me.tagName || 'missing-episodes');

            if (runMode === 'apply' && (me.actionTag || me.actionSearch)) {
              const seriesIDs = ((previewData && previewData.series) || []).map(s => s.seriesID);
              const episodeIDs = [];
              for (const s of ((previewData && previewData.series) || [])) {
                for (const season of (s.seasons || [])) {
                  for (const ep of (season.missingEpisodes || [])) {
                    if (ep && ep.episodeID) episodeIDs.push(ep.episodeID);
                  }
                }
              }
              if (me.actionTag && seriesIDs.length > 0) {
                const tagRes = await this.apiFetch('/api/scan/missing-episodes/tag', {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({
                    instanceId: r.instanceId,
                    tagName: ((me.tagName || 'missing-episodes') + '').trim() || 'missing-episodes',
                    seriesIds: seriesIDs,
                    removeFromOthers: true,
                  }),
                });
                if (tagRes.ok) {
                  phaseRow.tagApplied = await tagRes.json();
                } else {
                  const d = await tagRes.json().catch(() => ({}));
                  phaseRow.tagError = d.error || 'HTTP ' + tagRes.status;
                  phaseRow.ok = false;
                }
              }
              if (me.actionSearch && episodeIDs.length > 0) {
                const searchRes = await this.apiFetch('/api/scan/missing-episodes/search', {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({
                    instanceId: r.instanceId,
                    episodeIds: episodeIDs,
                  }),
                });
                if (searchRes.ok) {
                  phaseRow.searchApplied = await searchRes.json();
                } else {
                  const d = await searchRes.json().catch(() => ({}));
                  phaseRow.searchError = d.error || 'HTTP ' + searchRes.status;
                  phaseRow.ok = false;
                }
              }
            }
          } catch (e) {
            phaseRow.ok = false;
            phaseRow.error = String((e && e.message) || e);
          }
          results.phases.push(phaseRow);
        }

        // Phase 4 — Plex sync (Radarr + Sonarr). Runs LAST so Plex
        // reads the final Arr-side tag state. POSTs the inline config
        // to the shared /api/plex-sync/run endpoint (no saved rule).
        if (runPlexSync && !isCancelled() && r.plexSync) {
          const ps = r.plexSync;
          const phaseRow = { phase: 'plexsync', ok: true, instanceId: r.instanceId };
          try {
            const res = await this.apiFetch('/api/plex-sync/run', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                arrInstanceId: r.instanceId,
                runMode: runMode,
                plexLabelSync: {
                  plexInstanceId: ps.plexInstanceId,
                  libraryKeys: ps.libraryKeys || [],
                  labels: ps.labels || [],
                  labelDisplay: ps.labelDisplay || {},
                  targetTypes: (ps.targetTypes && ps.targetTypes.length > 0) ? ps.targetTypes : ['label'],
                },
              }),
            });
            if (!res.ok) {
              const d = await res.json().catch(() => ({}));
              throw new Error(`plexsync: ${d.error || 'HTTP ' + res.status}`);
            }
            phaseRow.response = await res.json();
          } catch (e) {
            phaseRow.ok = false;
            phaseRow.error = String((e && e.message) || e);
          }
          results.phases.push(phaseRow);
        }

        // Phase 5 — TBA refresh (Sonarr only). Preview, then in apply
        // mode rename every TBA file found (no per-file selection in
        // the chain). Uses the standalone tab's two endpoints.
        if (runTbaRefresh && !isCancelled() && r.tbaRefresh) {
          const tc = r.tbaRefresh;
          const phaseRow = { phase: 'tbarefresh', ok: true, instanceId: r.instanceId };
          try {
            const pr = await this.apiFetch('/api/scan/tba-refresh/preview', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                instanceId: r.instanceId,
                includeContinuing: !!tc.includeContinuing,
                includeEnded: !!tc.includeEnded,
                includeSpecials: !!tc.includeSpecials,
              }),
            });
            if (!pr.ok) {
              const d = await pr.json().catch(() => ({}));
              throw new Error(`tbarefresh: ${d.error || 'HTTP ' + pr.status}`);
            }
            const preview = await pr.json();
            phaseRow.response = preview;
            if (runMode === 'apply' && (preview.totalFiles || 0) > 0) {
              const groups = (preview.series || []).map(ser => ({
                seriesId: ser.seriesId,
                fileIds: (ser.files || []).map(f => f.episodeFileId),
              }));
              const ar = await this.apiFetch('/api/scan/tba-refresh/apply', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ instanceId: r.instanceId, groups }),
              });
              if (ar.ok) {
                phaseRow.applied = await ar.json();
              } else {
                const d = await ar.json().catch(() => ({}));
                phaseRow.applyError = d.error || 'HTTP ' + ar.status;
                phaseRow.ok = false;
              }
            }
          } catch (e) {
            phaseRow.ok = false;
            phaseRow.error = String((e && e.message) || e);
          }
          results.phases.push(phaseRow);
        }

        results.finishedAt = new Date().toISOString();
        results.ok = true;
        this.quickFixResults = results;
        const verb = runMode === 'apply' ? 'applied' : 'previewed';
        const ranList = [...headPhases, ...autoPhases.map(a => a.phase)];
        if (runMissingEpisodes) ranList.push('missingepisodes');
        if (runPlexSync) ranList.push('plexsync');
        if (runTbaRefresh) ranList.push('tbarefresh');
        // Toast wording: per-action wizards (fixedAction) use the
        // action's display name so the toast matches what the user
        // clicked. QFA proper keeps the chain wording. Apply-after-
        // preview falls into the QFA branch since overrideRule
        // doesn't set fixedAction.
        const fixedAction = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
        const actionLabels = {
          audiotags:       'Tag Audio',
          videotags:       'Tag Video',
          dvdetail:        'Tag DV Details',
          recover:         'Recover',
          missingepisodes: 'Missing episodes',
        };
        if (!overrideRule && fixedAction && actionLabels[fixedAction]) {
          this.showToast(actionLabels[fixedAction] + ' ' + verb, 'success');
        } else {
          this.showToast('Quick fix-all ' + verb + ': ' + ranList.join(', '), 'success');
        }
        // Pop the standalone result modal for per-action runs so
        // users see the result in-place (Discover does this via
        // scanResults.discover auto-pop; the same applies here for
        // audiotags / videotags / dvdetail / recover via
        // viewPhaseDetails). When target='both' the chain ran the
        // phase on TWO instances — collect both responses as
        // variants so the modal can render an instance switcher
        // above the body.
        if (!overrideRule && fixedAction) {
          const matches = (results.phases || []).filter(
            p => p.phase === fixedAction && p.ok && p.response
          );
          if (matches.length > 0) {
            const variants = matches.map(m => {
              const inst = this.instances.find(i => i.id === m.instanceId);
              return {
                instanceId: m.instanceId || '',
                label: inst ? inst.name : (m.instanceLabel === 'secondary' ? 'Secondary' : 'Primary'),
                response: m.response,
              };
            });
            this.qfaDetailVariants = variants;
            this.qfaDetailVariantIdx = 0;
            this.viewPhaseDetails({ phase: fixedAction, response: variants[0].response });
          }
        }
        // Per-action wizard save-to-globals: when the user fired a
        // per-action wizard (Tag Audio / Tag Video / Tag DV Details)
        // and the chain succeeded, persist the rule's bucket config
        // back to globals so next open of that wizard pre-fills with
        // the just-used values. overrideRule (Apply-after-preview) is
        // exempt — re-running a previous decision shouldn't re-mutate
        // globals.
        if (!overrideRule && this.ruleEditor && this.ruleEditor.fixedAction) {
          // Fire-and-forget — failures here don't undo the run that
          // just succeeded; surface as a soft toast and leave the
          // run results intact.
          this.persistRuleSnapshotsToGlobals(r, this.ruleEditor.fixedAction);
        }
        // QFA proper (no fixedAction) saves to its own localStorage
        // bucket per Arr-type. Independent of globals — per-action
        // wizards can't perturb QFA's memory and vice versa. Skipped
        // for overrideRule because Apply-after-preview re-fires the
        // already-saved snapshot; no new state to remember.
        if (!overrideRule && this.ruleEditor && this.ruleEditor.isQuickFix && !this.ruleEditor.fixedAction) {
          this._saveQfaState(this.ruleEditor.appType || 'radarr', r);
        }
        // Only close + clear the wizard when this was a wizard-driven
        // run. Apply-after-preview re-fires without ever opening the
        // wizard — leave editingRule alone in that case.
        if (!overrideRule) {
          this.ruleEditor.open = false;
          this.editingRule = null;
        }
      } catch (e) {
        results.finishedAt = new Date().toISOString();
        results.ok = false;
        results.error = e.message || 'Run failed';
        this.quickFixResults = results;
        this.ruleEditor.error = e.message || 'Run failed';
      } finally {
        this.ruleEditor.busy = false;
      }
    },

    // Opens the styled delete-confirm modal for a schedule. Mirrors the
    // openDeleteGroup / confirmDeleteGroup pattern so we don't fall back
    // to native confirm() (memory: feedback_dryrun_preview.md — show
    // concrete details before destructive action, not just a count).
    openDeleteSchedule(sj) {
      this.deleteScheduleTarget = sj;
    },

    // Opens the per-schedule history modal. Latest-first ordering is
    // imposed in the template via reversed iteration so the freshest
    // run is at the top.
    openHistory(sj) {
      this.historyTarget = sj;
      // Default to expanded view of the latest run if any — saves an
      // extra click for the most-common "did the last fire succeed?"
      // question.
      const runs = (sj.history || []);
      this.selectedHistoryRunIdx = runs.length > 0 ? runs.length - 1 : null;
    },

    closeHistory() {
      this.historyTarget = null;
      this.selectedHistoryRunIdx = null;
      this.historyResultError = '';
    },

    // Fetches the persisted scan-response JSON for one historical run
    // and hydrates the appropriate scanResults slot so the live Run-mode
    // UI replays the same per-movie drill-in. Closes the history modal
    // and sets historicalRunInfo so the user sees a "Historical run:"
    // banner above the result block.
    //
    // Mode handling:
    //   tag       → scanResults.tag
    //   recover   → recoverResults
    //   discover  → scanResults.discover
    //   audiotags → scanResults.audioTags
    //   videotags → scanResults.videoTags
    //   dvdetail  → scanResults.dvDetail
    //   combined  → server-side combinedScheduleResult shape:
    //               { tag?, discover?, recover?, audioTags?,
    //                 audioTagsSecondary?, videoTags?,
    //                 videoTagsSecondary?, dvDetail? } — every
    //               present phase gets a row in the result.
    buildActivityResult(schedule, run, data) {
      const phases = [];
      const mode = (schedule.mode || '').toLowerCase();
      if (!data) {
        // No persisted result (e.g. log-only run) — leave phases empty.
      } else if (mode === 'tag') {
        phases.push({ phase: 'tag', ok: true, response: data });
      } else if (mode === 'discover') {
        phases.push({ phase: 'discover', ok: true, response: data });
      } else if (mode === 'recover') {
        phases.push({ phase: 'recover', ok: true, response: data });
      } else if (mode === 'audiotags') {
        phases.push({ phase: 'audiotags', ok: true, response: data });
      } else if (mode === 'videotags') {
        phases.push({ phase: 'videotags', ok: true, response: data });
      } else if (mode === 'dvdetail') {
        phases.push({ phase: 'dvdetail', ok: true, response: data });
      } else if (mode === 'combined') {
        if (data.discover)           phases.push({ phase: 'discover',  ok: true, response: data.discover });
        if (data.recover)            phases.push({ phase: 'recover',   ok: true, response: data.recover });
        if (data.tag)                phases.push({ phase: 'tag',       ok: true, response: data.tag });
        if (data.audioTags)          phases.push({ phase: 'audiotags', ok: true, response: data.audioTags });
        if (data.audioTagsSecondary) phases.push({ phase: 'audiotags', ok: true, response: data.audioTagsSecondary, instanceLabel: 'secondary' });
        if (data.videoTags)          phases.push({ phase: 'videotags', ok: true, response: data.videoTags });
        if (data.videoTagsSecondary) phases.push({ phase: 'videotags', ok: true, response: data.videoTagsSecondary, instanceLabel: 'secondary' });
        if (data.dvDetail)           phases.push({ phase: 'dvdetail',  ok: true, response: data.dvDetail });
        if (data.dvDetailSecondary)  phases.push({ phase: 'dvdetail',  ok: true, response: data.dvDetailSecondary, instanceLabel: 'secondary' });
      }
      // Resolve appType from the schedule's instance — used by the
      // result panel's x-show to scope rendering to the active
      // scanAppType so a Sonarr schedule's result doesn't bleed
      // through Radarr context (and vice versa). Same pattern as
      // quickFixResults.appType.
      const inst = schedule && (this.instances || []).find(i => i.id === schedule.instanceId);
      return {
        startedAt: (run && run.startedAt) || new Date().toISOString(),
        scheduleName: schedule && schedule.name,
        scheduleId: schedule && schedule.id,
        instance: schedule && schedule.instanceId,
        appType: inst ? inst.type : undefined,
        phases,
        ok: !run || run.status === 'ok',
        partial: run && run.status === 'partial',
        error: run && run.status === 'error' ? run.summary : '',
        summary: run && run.summary,
        durationMs: run && run.durationMs,
      };
    },

    dismissActivityResults() {
      this.activityResults = null;
      if (this.activityRunPoll) {
        clearInterval(this.activityRunPoll);
        this.activityRunPoll = null;
      }
    },

    async viewScheduleRunDetails(schedule, run) {
      if (!run || !run.resultPath) {
        this.historyResultError = 'No result persisted for this run';
        return;
      }
      const startedAt = encodeURIComponent(run.startedAt);
      this.historyResultLoading = true;
      this.historyResultError = '';
      try {
        const r = await this.apiFetch('/api/schedules/' + schedule.id + '/runs/' + startedAt + '/result');
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          throw new Error(msg || 'HTTP ' + r.status);
        }
        const data = await r.json();
        // Build the shared phases-array shape so the Run-mode result
        // panel can render the same phase rows + drill-in modals as
        // Quick fix-all. No tab navigation, no clobbering of the
        // user's standalone scanResults.* slots. (Variable name kept
        // as `activityResults` for legacy reasons — internal-only.)
        this.activityResults = this.buildActivityResult(schedule, run, data);
        this.closeHistory();
      } catch (e) {
        this.historyResultError = e.message || 'Failed to load result';
      } finally {
        this.historyResultLoading = false;
      }
    },

    selectHistoryRun(idx) {
      // Clicking the already-selected row collapses the detail panel.
      this.selectedHistoryRunIdx = (this.selectedHistoryRunIdx === idx) ? null : idx;
    },

    // Resolves the schedule's history runs in newest-first order for
    // the modal. Adds an "originalIdx" so selectHistoryRun keeps using
    // the same indices as the underlying array (avoids off-by-one when
    // we reverse for display).
    historyRunsNewestFirst(sj) {
      if (!sj || !sj.history) return [];
      const out = [];
      for (let i = sj.history.length - 1; i >= 0; i--) {
        out.push({ run: sj.history[i], originalIdx: i });
      }
      return out;
    },

    // Pretty-print an ms duration into "1.2 s" / "45 s" / "2 m 14 s".
    // Used in the history modal so users don't have to mentally convert
    // 1228 ms to "just over a second."
    formatDuration(ms) {
      if (!ms || ms < 0) return '—';
      if (ms < 1000)  return ms + ' ms';
      if (ms < 10000) return (ms / 1000).toFixed(1) + ' s';
      const s = Math.floor(ms / 1000);
      if (s < 120) return s + ' s';
      const m = Math.floor(s / 60), rs = s % 60;
      return m + ' m ' + rs + ' s';
    },

    async confirmDeleteSchedule() {
      const sj = this.deleteScheduleTarget;
      if (!sj) return;
      this.scheduleBusyId = sj.id;
      try {
        const r = await this.apiFetch(`/api/schedules/${sj.id}`, { method: 'DELETE' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.showToast('Schedule deleted', 'success');
        this.deleteScheduleTarget = null;
        await this.loadSchedules();
      } catch (e) {
        this.showToast('Delete failed: ' + (e.message || 'unknown'), 'error');
      } finally {
        this.scheduleBusyId = null;
      }
    },

    // POST /api/schedules/{id}/run — fires the schedule via the same
    // code path the cron loop uses. The backend returns 202 (queued);
    // result lands in the schedule's history. We wait briefly then
    // refresh the list so the new history row appears without a manual
    // reload, then poll once more after a short delay since the run is
    // async (per-instance mutex serializes overlapping fires).
    async runScheduleNow(sj) {
      this.scheduleBusyId = sj.id;
      // Snapshot the schedule's current latest history-row timestamp
      // so the post-fire poll can detect a new completion (a row
      // newer than `before` is the run we just queued).
      const before = ((sj.history || [])[((sj.history || []).length || 0) - 1] || {}).startedAt || '';
      try {
        const r = await this.apiFetch(`/api/schedules/${sj.id}/run`, { method: 'POST' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.showToast(`Run queued: ${sj.name}`, 'info');
      } catch (e) {
        this.showToast('Run failed: ' + (e.message || 'unknown'), 'error');
        this.scheduleBusyId = null;
        return;
      }

      // If this rule contains a dvdetail phase, start the global DV
      // progress poll so the floating DV-scan banner surfaces while
      // the rule's DV phase runs server-side. activityRunPoll's
      // cleanup paths below stop it. Backend emits {running: false}
      // outside the actual DV scan window, so the banner only shows
      // during the slow phase — earlier phases don't trigger it.
      const ruleHasDv = sj.mode === 'dvdetail' ||
        (sj.mode === 'combined' && Array.isArray(sj.options && sj.options.combinedModes) &&
         sj.options.combinedModes.includes('dvdetail'));
      if (ruleHasDv) {
        this.startDvScanPoll();
      }

      // Show a placeholder activityResults entry so the user sees
      // "Run started, waiting for result…" immediately rather than
      // an empty UI. The poll below replaces it once the real
      // result lands.
      const placeholderInst = (this.instances || []).find(i => i.id === sj.instanceId);
      this.activityResults = {
        startedAt: new Date().toISOString(),
        scheduleName: sj.name,
        scheduleId: sj.id,
        instance: sj.instanceId,
        appType: placeholderInst ? placeholderInst.type : undefined,
        phases: [],
        pending: true,
        partial: false,
        ok: false,
      };

      // Poll for the new history row + persisted result. Cron-fire
      // and Run-now both go through the same fire() path, so the
      // history list is the single source of truth. Cap the poll at
      // ~5 minutes so a stuck run can't leak the interval; a real-
      // world tag scan against ~5k movies usually finishes in seconds.
      if (this.activityRunPoll) clearInterval(this.activityRunPoll);
      const pollStart = Date.now();
      const pollMaxMs = 5 * 60 * 1000;
      const tick = async () => {
        if (Date.now() - pollStart > pollMaxMs) {
          clearInterval(this.activityRunPoll);
          this.activityRunPoll = null;
          this.scheduleBusyId = null;
          this.stopDvScanPoll();
          this.dvScanProgress = null;
          if (this.activityResults && this.activityResults.pending) {
            this.activityResults = null;
            this.showToast('Run-now timed out — check Schedule history', 'error');
          }
          return;
        }
        try {
          await this.loadSchedules();
          const updated = (this.schedules || []).find(x => x.id === sj.id);
          const latest = ((updated && updated.history) || [])[((updated && updated.history) || []).length - 1];
          if (latest && latest.startedAt && latest.startedAt !== before && latest.resultPath) {
            // New row landed AND has a persisted result — fetch + populate.
            clearInterval(this.activityRunPoll);
            this.activityRunPoll = null;
            this.scheduleBusyId = null;
            this.stopDvScanPoll();
            this.dvScanProgress = null;
            const startedAt = encodeURIComponent(latest.startedAt);
            try {
              const res = await this.apiFetch('/api/schedules/' + sj.id + '/runs/' + startedAt + '/result');
              if (res.ok) {
                const data = await res.json();
                this.activityResults = this.buildActivityResult(updated, latest, data);
              } else {
                // Result-fetch failed — fall back to history-summary-only display.
                this.activityResults = this.buildActivityResult(updated, latest, null);
              }
            } catch (_) {
              this.activityResults = this.buildActivityResult(updated, latest, null);
            }
          } else if (latest && latest.startedAt && latest.startedAt !== before && !latest.resultPath) {
            // History row exists but no result-file (preview-only or
            // log-only run). Show summary without phases.
            clearInterval(this.activityRunPoll);
            this.activityRunPoll = null;
            this.scheduleBusyId = null;
            this.stopDvScanPoll();
            this.dvScanProgress = null;
            this.activityResults = this.buildActivityResult(updated, latest, null);
          }
        } catch (_) {
          // Silently retry next tick.
        }
      };
      // First poll quickly (fast runs finish in ~1s), then back off.
      this.activityRunPoll = setInterval(tick, 2500);
      setTimeout(tick, 800);
    },

    // Quick pause/resume directly from the schedule card. PUT-bodies
    // the existing rule with enabled flipped — backend re-validates +
    // re-loads the cron loop to drop or pick the rule. Keeps every
    // other field untouched (we send nil for the per-rule snapshots
    // so the wholesale-replace path doesn't wipe them — see schedules.go
    // PUT-handler).
    async toggleScheduleEnabled(sj) {
      this.scheduleBusyId = sj.id;
      try {
        const body = {
          name: sj.name,
          mode: sj.mode,
          instanceId: sj.instanceId,
          cron: sj.cron,
          enabled: !sj.enabled,
          options: sj.options || {},
          // Echo back the per-rule snapshots so the PUT-handler doesn't
          // see them as nil and fall through to the keep-existing path
          // (which is fine, but echoing is the cleaner contract).
          filters: sj.filters,
          audioTags: sj.audioTags,
          videoTags: sj.videoTags,
          dvDetail: sj.dvDetail,
          releaseGroupIds: sj.releaseGroupIds,
        };
        const r = await this.apiFetch(`/api/schedules/${sj.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.showToast(sj.enabled ? `Paused: ${sj.name}` : `Resumed: ${sj.name}`, 'success');
        await this.loadSchedules();
      } catch (e) {
        this.showToast('Toggle failed: ' + (e.message || 'unknown'), 'error');
      } finally {
        this.scheduleBusyId = null;
      }
    },

    // Computes the next fire time for a schedule. Reuses the modal's
    // client-side cron evaluator. Returns 'paused' for disabled
    // schedules, an empty string for missing cron, or '(invalid cron)'
    // if the expression doesn't parse. The backend uses robfig/cron in
    // time.Local mode (container TZ), so what we compute here matches
    // the actual fire time the cron loop will hit.
    scheduleNextRun(sj) {
      if (!sj || !sj.enabled) return 'paused';
      if (!sj.cron) return '';
      try {
        const fires = nextCronFires(sj.cron, 1, new Date());
        if (fires.length === 0) return '';
        return fires[0].toLocaleString(this.serverLocale || 'en-GB', this.dateFormatOptions());
      } catch (e) {
        return '(invalid cron)';
      }
    },

    // Returns an Alpine :style object for a status chip — object form
    // (not string) so it merges with the element's static `style`
    // attribute instead of replacing it (see structure-baseline §8.4
    // ":style with a string replaces the static style attribute"
    // trap). Status values come from core.JobRun.Status — see
    // internal/core/jobs.go: "ok" (green), "partial" (amber),
    // "error" (red); anything else neutral.
    scheduleStatusStyle(status) {
      switch (status) {
        case 'ok':      return { color: 'var(--accent-green)' };
        case 'partial': return { color: 'var(--accent-orange)' };
        case 'error':   return { color: 'var(--accent-red)' };
        default:        return { color: 'var(--text-secondary)' };
      }
    },

    // Returns the .card-edge variant class for a schedule's left-edge
    // status strip. Driven by the most-recent run's status (or "never"
    // for schedules that haven't fired yet). Drives the visual
    // glance-test of "is this schedule healthy?" without needing to
    // read the summary text.
    scheduleEdgeClass(sj) {
      const last = (sj.history || [])[(sj.history || []).length - 1];
      if (!last) return 'gray';
      switch (last.status) {
        case 'ok':      return 'green';
        case 'partial': return 'amber';
        case 'error':   return 'red';
        default:        return 'gray';
      }
    },

    // Pretty mode label for the schedule card pill — e.g. "Combined ·
    // tag + discover" instead of just "combined". Falls back to the
    // raw mode value when no special handling applies.
    scheduleModeLabel(sj) {
      if (sj.mode !== 'combined') return sj.mode;
      const modes = (sj.options && sj.options.combinedModes) || [];
      return modes.length > 0 ? 'Combined · ' + modes.join(' + ') : 'Combined';
    },

    // --- Display ---
    applyUIScale() {
      // CSS zoom: scales every element (fonts, padding, images) uniformly.
      document.documentElement.style.zoom = this.uiScale;
    },

    async setUIScale(value) {
      this.uiScale = value;
      this.applyUIScale();
      await this.saveDisplay();
    },

    setTheme(value) {
      this.theme = value;
      try { localStorage.setItem('resolvarr-theme', value); } catch (e) { /* private-mode safari */ }
      this.applyTheme();
    },

    applyTheme() {
      // 'system' resolves to light/dark via prefers-color-scheme; explicit
      // light / dark always win. data-theme is the selector tokens.css uses.
      // matchMedia missing on very old browsers / SSR — fall back to 'dark'.
      let resolved;
      if (this.theme === 'system') {
        const mql = (typeof matchMedia === 'function') ? matchMedia('(prefers-color-scheme: light)') : null;
        resolved = (mql && mql.matches) ? 'light' : 'dark';
      } else {
        resolved = this.theme;
      }
      document.documentElement.setAttribute('data-theme', resolved);
    },

    async setTimeFormat(value) {
      this.timeFormat = value;
      await this.saveDisplay();
      // Force recompute of the rule-editor Next-5-fires preview if
      // it's open so the user sees the new setting take effect.
      if (this.ruleEditor.open) this.computeRuleEditorNextFires();
    },

    // saveDisplay sends the full DisplayConfig (uiScale + timeFormat)
    // — backend overwrites c.Display in one transaction so a partial
    // body would clobber whichever field wasn't included. Send both.
    async saveDisplay() {
      try {
        const r = await this.apiFetch('/api/config/display', {
          method: 'PUT', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ uiScale: this.uiScale, timeFormat: this.timeFormat }),
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },
  };
}

// ===== Cron helpers (M3d) =================================================
//
// Tiny client-side cron evaluator for the schedule modal's Next-5-fires
// preview. Supports the standard 5-field syntax (m h dom mon dow) with
// numbers, '*', '*/N', 'a-b', 'a,b,c'. Backend uses robfig/cron which has
// the same field semantics, so what the UI shows matches what the
// scheduler will fire.
//
// Walks minute-by-minute from "now+1" until it has `count` matches or
// hits a 2-year ceiling. The naive walk is fast enough for the cron
// expressions a sane user types (worst case: monthly on day 31 → ~32
// days of skipping, ~46k iterations per fire; well under one frame in
// JS).
//
// Cron OR semantics on day-of-month + day-of-week: when both are
// restricted (neither '*'), match if EITHER matches — same as Vixie/cron
// and robfig.

function nextCronFires(cron, count, fromDate) {
  const parts = (cron || '').trim().split(/\s+/);
  if (parts.length !== 5) throw new Error('expected 5 fields (m h dom mon dow)');
  const [minF, hourF, domF, monF, dowF] = parts;
  const minSet  = parseCronField(minF,  0, 59);
  const hourSet = parseCronField(hourF, 0, 23);
  const domSet  = parseCronField(domF,  1, 31);
  const monSet  = parseCronField(monF,  1, 12);
  const dowSet  = parseCronField(dowF,  0, 6);
  const domStar = domF === '*';
  const dowStar = dowF === '*';

  const fires = [];
  const t = new Date(fromDate);
  t.setSeconds(0, 0);
  t.setMinutes(t.getMinutes() + 1);

  const maxIter = 2 * 366 * 24 * 60; // ~2 years
  let iter = 0;
  while (fires.length < count && iter < maxIter) {
    iter++;
    const m = t.getMinutes(), h = t.getHours();
    const dom = t.getDate(), mon = t.getMonth() + 1, dow = t.getDay();
    if (minSet.has(m) && hourSet.has(h) && monSet.has(mon)) {
      let dayMatch;
      if (domStar && dowStar)      dayMatch = true;
      else if (domStar)            dayMatch = dowSet.has(dow);
      else if (dowStar)            dayMatch = domSet.has(dom);
      else                         dayMatch = domSet.has(dom) || dowSet.has(dow);
      if (dayMatch) fires.push(new Date(t));
    }
    t.setMinutes(t.getMinutes() + 1);
  }
  if (fires.length === 0) throw new Error('no upcoming fires within 2 years');
  return fires;
}

function parseCronField(spec, lo, hi) {
  const set = new Set();
  for (const part of String(spec).split(',')) {
    let step = 1;
    let body = part;
    const slashIdx = body.indexOf('/');
    if (slashIdx >= 0) {
      step = parseInt(body.slice(slashIdx + 1), 10);
      if (isNaN(step) || step <= 0) throw new Error('bad step in field: ' + part);
      body = body.slice(0, slashIdx);
    }
    let from, to;
    if (body === '*' || body === '') {
      from = lo; to = hi;
    } else if (body.includes('-')) {
      const [a, b] = body.split('-').map(s => parseInt(s, 10));
      if (isNaN(a) || isNaN(b)) throw new Error('bad range: ' + part);
      from = a; to = b;
    } else {
      const v = parseInt(body, 10);
      if (isNaN(v)) throw new Error('bad value: ' + part);
      from = v; to = (slashIdx >= 0) ? hi : v;
    }
    if (from < lo || to > hi || from > to) throw new Error('out of range: ' + part);
    for (let v = from; v <= to; v += step) set.add(v);
  }
  return set;
}

// Reverse-derives the modal's preset id + time/dow/dom pickers from a
// stored cron string so re-opening Edit reflects the saved choice. Only
// recognises the exact strings ruleApplyPreset() would emit; anything
// else collapses to 'custom' so the user can still see and edit it.
// hour24To12 maps a 0-23 clock hour to its 1-12 equivalent. 0 → 12 AM,
// 1-11 → 1-11 (still AM), 12 → 12 PM, 13-23 → 1-11 PM. Used when the
// rule editor initialises its 12h hour-helper field from the canonical
// editingRule.hour. AM/PM is derived separately via `hour >= 12`.
function hour24To12(h) {
  if (!Number.isFinite(h)) return 12;
  h = ((h % 24) + 24) % 24;
  if (h === 0) return 12;
  if (h > 12) return h - 12;
  return h;
}

function derivePresetFromCron(cron) {
  if (!cron) return { id: 'daily', hour: 3, minute: 0, dow: 0, dom: 1 };
  const parts = cron.trim().split(/\s+/);
  if (parts.length !== 5) return { id: 'custom' };
  const [m, h, dom, mon, dow] = parts;
  if (cron === '0 * * * *')    return { id: 'hourly',      hour: 0, minute: 0, dow: 0, dom: 1 };
  if (cron === '0 */6 * * *')  return { id: 'every-6h',    hour: 0, minute: 0, dow: 0, dom: 1 };
  if (cron === '0 */12 * * *') return { id: 'every-12h',   hour: 0, minute: 0, dow: 0, dom: 1 };
  if (cron === '0 0,12 * * *') return { id: 'twice-daily', hour: 0, minute: 0, dow: 0, dom: 1 };
  if (mon !== '*') return { id: 'custom' };
  const mNum = parseInt(m, 10), hNum = parseInt(h, 10);
  if (isNaN(mNum) || isNaN(hNum) || mNum < 0 || mNum >= 60 || hNum < 0 || hNum >= 24) {
    return { id: 'custom' };
  }
  if (dom === '*' && dow === '*') return { id: 'daily',   hour: hNum, minute: mNum, dow: 0, dom: 1 };
  if (dom === '*' && dow !== '*') {
    const d = parseInt(dow, 10);
    if (!isNaN(d) && d >= 0 && d < 7) return { id: 'weekly', hour: hNum, minute: mNum, dow: d, dom: 1 };
  }
  if (dow === '*' && dom !== '*') {
    const d = parseInt(dom, 10);
    if (!isNaN(d) && d >= 1 && d <= 31) return { id: 'monthly', hour: hNum, minute: mNum, dow: 0, dom: d };
  }
  return { id: 'custom' };
}
