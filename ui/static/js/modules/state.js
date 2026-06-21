// resolvarr UI - state module
//
// All reactive data fields for the Alpine root, extracted verbatim from
// app.js (Stage 4 split). Pure data: no methods, no this-references in
// initializers. Composed via { ...appState() } in app(); fields become
// own-properties of the merged Alpine component. See frontend-restructure-plan.md.
function appState() {
  return {
    // state
    version: 'dev',
    // Dev-build flag — true when the version string carries "-dev".
    // Gates dev-only diagnostics in the UI (section-id badge + the
    // scan-result Debug strip). Mirrors the backend's s.isDev() gate so
    // the two stay in lockstep. See docs/resolvarr/ui-section-map.md.
    isDevBuild: false,
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
    // Replay (Run again) modal for a failed/partial event. preview holds
    // the GET replay-preview response (which rules + functions would fire).
    // kind: '' = Connect-event replay, 'qbit' = qBit-add re-run.
    replayModal: { open: false, kind: '', loading: false, running: false, event: null, preview: null, error: '' },
    // qBit-webhook activity view (qBit-add events, scoped per qBit instance).
    qbitActivityInstanceId: '',
    qbitWebhookEvents: {},
    qbitWebhookEventsLoading: false,
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
    scanSection: 'run',           // 'run' | 'groups' | 'filters' | 'recover' | 'audio' | 'video' | 'dvdetail' | 'profile-by-tag' | 'history'

    // Profile by tag — one-off Library scan (Radarr + Sonarr). Moves items to a
    // quality profile based on their Arr tags, via AND/OR rules. Opens its own
    // modal wizard (like Tag quality releases); the result pops a result modal.
    // 3-step wizard: instance+mode -> rules -> review. Nothing persisted.
    profileByTagWizard: {
      open: false,
      step: 0,                  // 0 instance+mode, 1 rules, 2 review
      instanceId: '',
      runMode: 'preview',       // 'preview' | 'apply'
      // rules: [{ conditions: [{ type:'tag', value:'<tagId>', join:'and'|'or' }], profileId: <int> }]
      rules: [],
      tags: [],                 // [{id,label,usageCount}] from the picked instance
      profiles: [],             // [{id,name}] from the picked instance
      pickersLoading: false,
      pickersError: '',
      busy: false,              // a run is in flight
    },
    profileByTagResult: null,   // profileByTagRun from the last preview/apply -> result modal
    pbtResultFilter: 'all',     // 'all' | 'moves' | 'conflicts'
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

    // Reconcile stuck downloads (reconcile-stuck-downloads sub-tab).
    // Config-modal pattern (like the cleanup wizard): launcher opens
    // reconcileWizard, you pick instance + qBit instance + category +
    // preview/apply, then Run. Preview shows the result panel with per-
    // row select; Apply (from the modal) recategorises all redundant.
    reconcileWizard: { open: false, instanceId: '', mode: 'preview', busy: false, error: '' },
    reconcileApplying: false,
    reconcileError: '',
    reconcileResults: null,        // { instance, totals, reconcile: [{downloadId, status, stuckScore, importedScore, ...}] }
    reconcileInstanceId: '',       // the Arr instance the current result ran against (used by per-row Apply)
    reconcileApplySelected: {},    // { [downloadId]: true } — which redundant rows to recategorise
    reconcileQbitInstanceId: '',   // which qBit client holds the downloads (set in the modal)
    reconcilePostCategory: '',     // category to move redundant downloads to (set in the modal)
    reconcilePreCategory: '',      // info: the category stuck downloads currently sit in
    reconcileLastInstanceId: '',   // tracks which instance the qBit + categories were resolved for
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
    qbitSeBacklogConfig: null,           // inline QbitSe config when the scan runs from the Tag Library one-off sub-tab (null = webhook-rule path)
    qbitSeRunForm: {                     // Tag Library "qBit S/E tags" one-off form state (Sonarr-only)
      qbitInstanceId: '',
      episodeEnabled: true, episodeTag: 'Episode',
      seasonEnabled: true, seasonTag: 'Season',
      unmatchedEnabled: false, unmatchedTag: 'Unmatched',
      categoryFilter: '',
    },
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
      // Review tab — edit-mode only. Create/wizard mode reaches the
      // 'review' step through ruleEditorSteps instead; this entry gives
      // tabbed-edit the same read-only config summary as the final
      // wizard step. ruleEditorTabVisible always returns true for it.
      { id: 'review', label: 'Review' },
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
    instForm: { id: '', name: '', type: 'radarr', iconVariant: 'standard', defaultQbitInstanceId: '', url: '', apiKey: '', pathMappings: [] },
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
    authStatus: { trustedNetworksLocked: false, trustedProxiesLocked: false, authentication: '', authenticationRequired: '' },
    // Disable-auth confirmation modal. Turning authentication off entirely
    // is the one protected transition (the backend requires the current
    // password as confirm_password); this modal collects it. Declared here
    // so the binding is in template scope before loadSecurityPanel runs.
    disableAuthModalOpen: false,
    disableAuthPassword: '',
    disableAuthError: '',
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
  };
}
