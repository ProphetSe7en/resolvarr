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
    webhookSection: 'setup',  // M-soon placeholder sub-tab — Setup / Grab / Import-Upgrade / Delete / Activity
    scanSection: 'run',           // 'run' | 'groups' | 'filters' | 'audio' | 'video' | 'dvdetail' | 'history'
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
    helpOpen: { run: false, groups: false, filters: false, audio: false, video: false, dvdetail: false, tags: false, history: false,
      // Wizard-step help panels — same toggle pattern as the Library
      // scan fanes. Each wizard step renders its own collapsible
      // "How it works" panel so the inline form copy can stay short.
      ruleBasics: false, ruleRG: false, ruleFilters: false, ruleAudio: false, ruleVideo: false, ruleDvDetail: false, ruleSchedule: false },
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
    showDiscoverResultsModal: false,     // modal-overlay flag for the standalone Run Discover candidate panel
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
    recoverExpanded: {},         // { [movieId]: true } for drill-down rows
    recoverApplySelected: {},    // { [movieId]: true } — which would-fix rows to apply
    recoverRename: true,         // mirrors bash RENAME=true default
    recoverApplying: false,      // loading state for apply
    showScanApplyConfirm: false,  // confirm modal before applying a preview's decisions
    instances: [],
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
      config: {},
      busy: false,
      testing: false,
      testPassed: false,
      testResult: '',
      error: '',
    },
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
      audio: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
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
      resolution: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
      codec:      { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
      hdr:        { enabled: false, prefix: '', sonarrAggregation: 'strict',        allowedValues: [], selectMode: '' },
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
    dvDetail: { enabled: false, prefix: '', allowedValues: [], selectMode: '', removeOrphanedTags: false },
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
    // first as fallback). Empty until first poll. The DV detail tab
    // shows a "Tools required" notice when dvTools.installed is false
    // (the install itself happens at container start when
    // ENABLE_DV_TOOLS=true is set on the container template).
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
      { action: 'tag',       label: 'Tag library' },
      { action: 'discover',  label: 'Discover' },
      { action: 'recover',   label: 'Recover' },
      { action: 'cleanup',   label: 'Cleanup' },
      { action: 'audiotags', label: 'Audio tags' },
      { action: 'videotags', label: 'Video tags' },
      { action: 'dvdetail',  label: 'DV detail' },
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
    qfaDetail: null,             // 'tag' | 'recover' | 'audio' | 'video' | 'dv' | null
    qfaDetailTag: null,
    qfaDetailRecover: null,
    qfaDetailAudio: null,        // scan_auto_tags response for an audio phase row
    qfaDetailVideo: null,        // scan_auto_tags response for a video phase row
    qfaDetailDv: null,           // scan_dv_detail response for a DV phase row
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
    qfaDetailScanFilter: 'add',          // tag-modal filter chip
    qfaDetailScanInstanceFilter: 'both', // tag-modal primary/secondary toggle
    qfaDetailRecoverFilter: 'all',       // recover-modal bucket chip
    qfaDetailAutoFilter: 'add',          // shared audio/video drill-in chip
    // Wizard step order. 'review' is the final-confirmation step; the
    // others map 1:1 onto ruleEditorTabs and inherit the same
    // visibility rules.
    ruleEditorSteps: ['basics', 'filters', 'rg', 'audio', 'video', 'dvdetail', 'schedule', 'review'],

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
      } else if (['run','groups','filters','audio','video','dvdetail','history'].includes(savedScanSection)) {
        this.scanSection = savedScanSection;
      }
      const savedGroupsSection = localStorage.getItem('resolvarr-groups-section');
      // 'filters' is no longer a groups-sidebar item — it's a top-level Scan sub-tab now.
      // Migrate stale 'filters' → scanSection='filters' + groups inner stays on 'active'.
      if (savedGroupsSection === 'filters') {
        this.scanSection = 'filters';
        this.groupsSection = 'active';
        localStorage.setItem('resolvarr-scan-section', 'filters');
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
      }
      // Load notification agents when landing on Settings → Notifications.
      if (this.currentPage === 'settings' && this.section === 'notifications') {
        this.loadAgents();
      }
      // Same lazy-load for the Security panel.
      if (this.currentPage === 'settings' && this.section === 'security') {
        this.loadSecurityPanel();
      }
      // Kick off initial status check for all instances, then poll every 60s.
      this.refreshAllStatus();
      this.pollHandle = setInterval(() => this.refreshAllStatus(), 60000);
    },

    setCurrentPage(page) {
      this.currentPage = page;
      localStorage.setItem('resolvarr-page', page);
      // Schedules feed both the Run mode rules grid AND the History scan
      // filter chips — load + poll whenever the user is anywhere on the
      // Scan tab. Stop the poll when leaving the Scan tab entirely.
      if (page === 'scan' && (this.scanSection === 'run' || this.scanSection === 'history')) {
        this.loadSchedules();
        this.startSchedulePoll();
      } else {
        this.stopSchedulePoll();
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

    // Click a row → fetch the dump + open the matching drill-in modal
    // ON THE HISTORY TAB. The modal renders over whichever tab the
    // user is on, so the History tab stays a true history-browser
    // instead of bouncing the user across sub-tabs every time they
    // want to peek at a saved result. historicalRunInfo is still set
    // so if the user later navigates to the originating sub-tab, the
    // per-tab banner and Apply-gating still surface (defensive; modal
    // is read-only).
    //
    // Cleanup is the one exception: there's no QFA-style drill-in for
    // it (cleanup result is a small list of deleted tag labels). For
    // now it still routes to the Filters tab so the existing inline
    // result panel renders. A dedicated cleanup modal can land later.
    async openScanHistory(row) {
      try {
        const r = await this.apiFetch('/api/scan/history/' + encodeURIComponent(row.file));
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const data = await r.json();
        switch (row.action) {
          case 'tag':       this.scanResults.tag = data;       this.viewPhaseDetails({phase: 'tag',       response: data}); break;
          case 'discover':  this.scanResults.discover = data;  this.viewPhaseDetails({phase: 'discover',  response: data}); break;
          case 'recover':   this.recoverResults = data;        this.viewPhaseDetails({phase: 'recover',   response: data}); break;
          case 'audiotags': this.scanResults.audioTags = data; this.viewPhaseDetails({phase: 'audiotags', response: data}); break;
          case 'videotags': this.scanResults.videoTags = data; this.viewPhaseDetails({phase: 'videotags', response: data}); break;
          case 'dvdetail':  this.scanResults.dvDetail = data;  this.viewPhaseDetails({phase: 'dvdetail',  response: data}); break;
          // Cleanup falls back to sub-tab routing — no drill-in modal yet.
          case 'cleanup':   this.cleanupResults = data;        this.setScanSection('filters'); break;
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
      this.recoverFilter = 'all';
      this.recoverError = '';
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'recover') {
        this.historicalRunInfo = null;
      }
    },
    dismissDiscoverResults() {
      this.scanResults.discover = null;
      this.scanDiscoverSelected = {};
      this.scanDiscoverExpanded = {};
      this.scanDiscoverBannerDismissed = false;
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
      this.recoverFilter = 'all';
      // Belt-and-suspenders — if a check or apply is in flight when the
      // user switches instances, clear the in-flight flags too so the
      // Find / Apply buttons don't stay stuck disabled. Mid-flight HTTP
      // request is orphaned but its result drops on the floor since
      // recoverResults is now null.
      this.recoverLoading = false;
      this.recoverApplying = false;
    },
    async runLibraryScan() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      if (!this.anyScanModeEnabled()) return;
      this.scanError = '';
      this.scanResults = { tag: null, discover: null, audioTags: null, videoTags: null, dvDetail: null };
      this.historicalRunInfo = null;
      this.scanGroupExpanded = {};
      this.scanRowExpanded = {};
      this.scanDiscoverSelected = {};
      this.scanDiscoverExpanded = {};
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
        this.scrollToScanResults();
      }
    },

    // Scrolls the result block into view after a successful scan, but only
    // when needed. block:'nearest' is the trick — if the anchor is already
    // visible (Tag library Run scan: result renders right under the card,
    // user is already looking at the spot) the call is a no-op. When the
    // anchor is out of viewport (Quick fix-all clicked from the bottom of
    // the page, results render at the top) it scrolls just enough to bring
    // the anchor to the closest viewport edge.
    scrollToScanResults() {
      if (this.scanError) return;
      this.$nextTick(() => {
        const el = document.getElementById('scan-results-anchor');
        if (el) el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      });
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
        if (!this.scanError && this.scanResults.discover) {
          this.showDiscoverResultsModal = true;
        }
      } finally {
        this.scanLoading = false;
      }
    },

    closeDiscoverResultsModal() {
      this.showDiscoverResultsModal = false;
      // Restore stashed standalone-Discover state when the modal was
      // opened from a QFA phase row. Keeps the user's own scan data
      // intact across the QFA drill-in.
      if (this._qfaDiscoverActive) {
        this.scanResults.discover = this._qfaStashedDiscover || null;
        this._qfaStashedDiscover = null;
        this._qfaDiscoverActive = false;
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
      const instType = this.scanResults.discover.instance.type;
      this.scanDiscoverAdding = true;
      let added = 0;
      let failed = '';
      const succeeded = [];
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
          const payload = {
            search,
            tag: tagLabel,
            display: search,
            type: instType,
            mode: 'filtered',
            enabled: true,
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
        if (added > 0) {
          // Prune the just-added entries from the visible discover list so
          // the user doesn't see a "+ Add" button next to a group already
          // in Active (which would 409 on a second click). Match by search
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
          this.showToast(`Added ${added} group${added === 1 ? '' : 's'}`, 'success');
          await this.loadGroups();
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
      this.recoverLoading = true;
      this.recoverError = '';
      this.recoverResults = null;
      this.recoverApplySelected = {};
      this.recoverExpanded = {};
      this.recoverFilter = 'all';
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
      const ids = Object.keys(this.recoverApplySelected).filter(k => !!this.recoverApplySelected[k]).map(k => parseInt(k, 10));
      if (ids.length === 0) return;
      this.recoverApplying = true;
      this.recoverError = '';
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
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
      const list = this.recoverResults.recover || [];
      if (this.recoverFilter === 'all') return list;
      return list.filter(it => it.status === this.recoverFilter);
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

    // installDvTools / uninstallDvTools removed — install now happens
    // at container start when ENABLE_DV_TOOLS=true is set on the
    // container template (entrypoint.sh runs apk add ffmpeg + downloads
    // dovi_tool from GitHub before privilege drop). The DV detail tab
    // surfaces a notice with the env-var instructions when the tools
    // aren't resolvable on $PATH; loadDvToolsStatus stays for that
    // banner check.

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

    async runDvDetailScan(mode, bypassOverride) {
      if (!this.scanInstanceId) return;
      // bypassOverride lets confirmDvDetailApply pass the snapshot it
      // captured before resetting dvBypassCache. Falsy → fall back to
      // the live state (Preview path).
      const bypassDvCache = bypassOverride !== undefined ? !!bypassOverride : !!this.dvBypassCache;
      this.scanLoading = true;
      this.scanResults.dvDetail = null;
      // Fresh scan supersedes any historical replay that was on screen.
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'dvdetail') {
        this.historicalRunInfo = null;
      }
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
            instanceId: this.scanInstanceId,
            action: 'dvdetail',
            mode,
            bypassDvCache,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
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
      } catch (e) {
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

    dismissDvDetailResults() {
      this.scanResults.dvDetail = null;
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
      // Reset Skip cache before Apply so a destructive write doesn't
      // silently inherit a Preview's bypass setting. Snapshot first
      // so the actual run uses whatever the user had ticked.
      const bypass = !!this.dvBypassCache;
      this.dvBypassCache = false;
      // runDvDetailScan handles its own scanLoading flag.
      await this.runDvDetailScan('apply', bypass);
    },

    // --- Groups ---
    setGroupsSection(section) {
      this.groupsSection = section;
      localStorage.setItem('resolvarr-groups-section', section);
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
    // Audio / Video / DV detail are Radarr-only until each is ported.
    // When the user switches to Sonarr, the sidebar collapses to just
    // the relevant sub-tabs and a Sonarr-instance-only run mode card.
    scanSectionVisible(section) {
      if (this.scanAppType === 'sonarr') {
        // Sonarr currently exposes Run mode (Quick fix-all + standalone
        // Recover card + saved rules grid) and History (adhoc scan
        // replays). Release Groups, Tag library, Filters, Audio, Video,
        // DV detail are all
        // radarr-only — they centre on filter-driven group tagging and
        // per-Movie-File MediaInfo, which Sonarr's per-episode-file model
        // doesn't share. Recover is the one Sonarr action implemented today
        // and lives on Run mode (closer to where the user looks for actions
        // when Sonarr is selected).
        return section === 'run' || section === 'history';
      }
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
          this.audioTags.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.audioCodecs))   this.audioVocab.codecs   = data.audioCodecs;
        if (Array.isArray(data && data.audioChannels)) this.audioVocab.channels = data.audioChannels;
        if (Array.isArray(data && data.audioFlags))    this.audioVocab.flags    = data.audioFlags;
      } catch (_) {
        // Silent — config-load is best-effort.
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

    async runAudioTagsScan(mode = 'preview') {
      if (!this.scanInstanceId) { this.showToast('Pick an instance first', 'error'); return; }
      if (!this.anyAudioTagsBucketEnabled()) { this.showToast('Enable Audio bucket first', 'error'); return; }
      this.scanLoading = true;
      this.scanError = '';
      this.autoTagRowExpanded = {};
      this.scanResults.audioTags = null;
      // Fresh scan supersedes any historical replay that was on screen.
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'audiotags') {
        this.historicalRunInfo = null;
      }
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: this.scanInstanceId, action: 'audiotags', mode }),
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
      } catch (e) {
        this.scanError = e.message || 'Audio-tags scan failed';
      } finally {
        this.scanLoading = false;
        this.scrollToScanResults();
      }
    },

    audioTagsPendingChangeCount() {
      const r = this.scanResults && this.scanResults.audioTags;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },
    openAudioTagsApplyConfirm() {
      if (this.audioTagsPendingChangeCount() === 0) return;
      this.showAudioTagsApplyConfirm = true;
    },
    async confirmAudioTagsApply() {
      this.showAudioTagsApplyConfirm = false;
      await this.runAudioTagsScan('apply');
    },
    dismissAudioTagsResults() {
      if (this.scanResults) this.scanResults.audioTags = null;
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

    async runVideoTagsScan(mode = 'preview') {
      if (!this.scanInstanceId) { this.showToast('Pick an instance first', 'error'); return; }
      if (!this.anyVideoTagsBucketEnabled()) { this.showToast('Enable at least one Video bucket first', 'error'); return; }
      this.scanLoading = true;
      this.scanError = '';
      this.autoTagRowExpanded = {};
      this.scanResults.videoTags = null;
      // Fresh scan supersedes any historical replay that was on screen.
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'videotags') {
        this.historicalRunInfo = null;
      }
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: this.scanInstanceId, action: 'videotags', mode }),
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
      } catch (e) {
        this.scanError = e.message || 'Video-tags scan failed';
      } finally {
        this.scanLoading = false;
        this.scrollToScanResults();
      }
    },

    videoTagsPendingChangeCount() {
      const r = this.scanResults && this.scanResults.videoTags;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },
    openVideoTagsApplyConfirm() {
      if (this.videoTagsPendingChangeCount() === 0) return;
      this.showVideoTagsApplyConfirm = true;
    },
    async confirmVideoTagsApply() {
      this.showVideoTagsApplyConfirm = false;
      await this.runVideoTagsScan('apply');
    },
    dismissVideoTagsResults() {
      if (this.scanResults) this.scanResults.videoTags = null;
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
        tag: 'Tag library',
        discover: 'Discover release groups',
        recover: 'Recover missing release groups',
        audiotags: 'Audio tags',
        videotags: 'Video tags',
        dvdetail: 'DV detail',
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
      if (!confirm(`Delete "${inst.name}"?`)) return;
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
      try {
        await navigator.clipboard.writeText(this.securityApiKey);
        this.securityApiKeyCopied = true;
        setTimeout(() => { this.securityApiKeyCopied = false; }, 2000);
      } catch (e) {
        this.showToast('Copy failed: ' + e.message, 'error');
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
      if (a) {
        this.agentModal = {
          id: a.id,
          name: a.name || '',
          type: a.type,
          enabled: !!a.enabled,
          events: Object.assign({ onScheduleSuccess: true, onScheduleFailure: true }, a.events || {}),
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
          events: { onScheduleSuccess: true, onScheduleFailure: true },
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
        id:      this.agentModal.id,
        name:    (this.agentModal.name || '').trim(),
        type:    this.agentModal.type,
        enabled: !!this.agentModal.enabled,
        events:  Object.assign({}, this.agentModal.events),
        config:  Object.assign({}, this.agentModal.config),
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
    renameMergeTarget() {
      const newLabel = this.renameNewLabel.trim().toLowerCase();
      if (!newLabel || newLabel === this.renameTarget.label.toLowerCase()) return null;
      return this.tags.find(t => t.label.toLowerCase() === newLabel && t.id !== this.renameTarget.id) || null;
    },

    async submitRename() {
      const newLabel = this.renameNewLabel.trim();
      if (!newLabel) return;
      if (newLabel === this.renameTarget.label) {
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
      const tagRegex = /^[a-z0-9-]+$/;
      const selectedIds = new Set(this.batchRenameTargets.map(t => t.id));
      // Tally new labels within the batch to detect dupes. Key by lowercase
      // so Sonarr (which accepts mixed case) doesn't slip a "Foo" / "foo"
      // collision past the gate. Radarr enforces lowercase via the regex
      // check below regardless.
      const newLabelCounts = new Map();
      for (const t of this.batchRenameTargets) {
        const newLabel = this.batchRenameApplyTemplate(t.label);
        const key = newLabel.toLowerCase();
        newLabelCounts.set(key, (newLabelCounts.get(key) || 0) + 1);
      }
      for (const t of this.batchRenameTargets) {
        const newLabel = this.batchRenameApplyTemplate(t.label);
        let status = 'ok';
        let mergeTarget = null;
        if (newLabel === t.label) {
          status = 'unchanged';
        } else if (!newLabel || !tagRegex.test(newLabel)) {
          status = 'invalid';
        } else if (newLabelCounts.get(newLabel.toLowerCase()) > 1) {
          status = 'merge-batch';
        } else {
          // Look for existing tag (outside the selection) that matches.
          const existing = this.tags.find(
            x => x.label.toLowerCase() === newLabel.toLowerCase() && !selectedIds.has(x.id)
          );
          if (existing) {
            status = 'merge-existing';
            mergeTarget = existing;
          }
        }
        rows.push({ id: t.id, oldLabel: t.label, newLabel, status, mergeTarget });
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
      this.deleteTargets = [{ id: t.id, label: t.label, usageCount: t.usageCount }];
      this.deleteKeepDefinition = false;
      this.deleteError = '';
      this.deleteProgress = '';
      this.deleteBusy = false;
      this.deletePreviewGroups = [];
      this.showDeleteModal = true;
      this.loadDeletePreview();
    },

    openBulkDelete() {
      if (this.tagsSelected.size === 0) return;
      this.deleteTargets = this.tags
        .filter(t => this.tagsSelected.has(t.id))
        .map(t => ({ id: t.id, label: t.label, usageCount: t.usageCount }));
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
    snapshotGlobalAudioTags() {
      return JSON.parse(JSON.stringify(this.audioTags));
    },
    snapshotGlobalVideoTags() {
      return JSON.parse(JSON.stringify(this.videoTags));
    },
    snapshotGlobalDvDetail() {
      return JSON.parse(JSON.stringify(this.dvDetail));
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
      const inst = this.scanInstanceId ||
        (this.instances.find(i => i.type === 'radarr') || this.instances[0] || {}).id || '';
      this.editingRule = {
        id: '', name: '', mode: 'tag', instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        manualOnly: false,
        enabled: true,
        options: {
          runMode: 'apply',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: [],
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
        },
        filters:         this.snapshotGlobalFilters(),
        audioTags:       this.snapshotGlobalAudioTags(),
        videoTags:       this.snapshotGlobalVideoTags(),
        dvDetail:        this.snapshotGlobalDvDetail(),
        releaseGroupIds: this.snapshotGlobalRGIds(inst),
      };
      this.ruleEditor = { open: true, isCreate: true, isQuickFix: false, step: 0, activeTab: 'basics', busy: false, error: '', cronError: '', nextFires: [] };
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
      // Sonarr scans are not implemented yet (scan.go:117 returns 501).
      // Default to the user's current scan-picker only when it's
      // Radarr; otherwise fall back to first Radarr instance. Refuse
      // entirely if no Radarr exists so the user gets a clear message
      // instead of a per-phase 501.
      const radarrSeed = this.instances.find(i => i.id === this.scanInstanceId && i.type === 'radarr')
                      || this.instances.find(i => i.type === 'radarr');
      if (!radarrSeed) {
        this.showToast('Quick fix-all needs a Radarr instance — Sonarr scans land with M-Sonarr', 'error');
        return;
      }
      const inst = radarrSeed.id;
      // Pre-fill runMode from the Run mode radio on Run mode (this.scanMode).
      // If the user has Preview selected for the standalone Tag library
      // run-card, opening Quick fix-all should default to Preview too.
      // Avoids the gotcha where a user picks Preview on the radio and
      // assumes the wizard inherits it — wizard defaulted to Apply
      // before, which silently mismatched user intent.
      const seedRunMode = this.scanMode === 'preview' ? 'preview' : 'apply';
      this.editingRule = {
        id: '',
        // Auto-name lets the chain-summary header read as something
        // recognisable in the activity log without making the user type.
        name: 'Quick fix-all',
        // Quickfix defaults to combined-mode with the most-common chain
        // (discover + recover + tag) so the user doesn't have to click
        // through the chain-step list for the typical "do everything"
        // case. They can still narrow the chain in Basics.
        mode: 'combined',
        instanceId: inst,
        // Cron stays unused but kept on the shape so the existing
        // Review markup doesn't have to special-case quickfix.
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        enabled: true,
        options: {
          runMode: seedRunMode,
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: ['discover', 'recover', 'tag'],
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
        },
        filters:         this.snapshotGlobalFilters(),
        audioTags:       this.snapshotGlobalAudioTags(),
        videoTags:       this.snapshotGlobalVideoTags(),
        dvDetail:        this.snapshotGlobalDvDetail(),
        releaseGroupIds: this.snapshotGlobalRGIds(inst),
      };
      this.ruleEditor = { open: true, isCreate: true, isQuickFix: true, step: 0, activeTab: 'basics', busy: false, error: '', cronError: '', nextFires: [] };
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
      if (!copy.releaseGroupIds) copy.releaseGroupIds = this.snapshotGlobalRGIds(copy.instanceId);
      copy.options = Object.assign({
        runMode: 'apply', cleanupUnusedTags: false, syncToSecondary: false, syncToInstanceId: '',
        includeDiscovery: false, autoActivateDiscovered: false,
        discoverWriteBack: false, discoverScanSecondary: false,
        recoverIncludeSecondary: false, recoverIncludeSonarr: false, recoverSonarrSecondary: false,
        recoverTestItemId: 0, debugTrace: false, bypassDvCache: false,
      }, copy.options || {});
      this.editingRule = copy;
      this.ruleEditor = { open: true, isCreate: false, isQuickFix: false, step: 0, activeTab: 'basics', busy: false, error: '', cronError: '', nextFires: [] };
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

    closeQfaDetail() {
      this.qfaDetail = null;
      this.qfaDetailExpanded = {};
      this.qfaDetailTag = null;
      this.qfaDetailRecover = null;
      this.qfaDetailAudio = null;
      this.qfaDetailVideo = null;
      this.qfaDetailDv = null;
      this.qfaDetailDvStatusFilter = null;
      this.qfaDetailDvTagFilter = null;
      this.qfaDetailDvStatusHelpOpen = false;
      this.qfaDetailAutoTagFilter = null;
      this.qfaDetailBreakdownOpen = false;
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
      this.qfaDetailExpanded = {};
      // Pick a sensible default chip based on what the run produced —
      // mirrors pickDefaultScanFilter for live runs so the modal opens
      // on the chip with content rather than an empty default.
      switch (p.phase) {
        case 'tag': {
          this.qfaDetailTag = p.response;
          this.qfaDetail = 'tag';
          this.qfaDetailScanInstanceFilter = 'both';
          const t = (p.response.totals || {});
          if ((t.toAdd || 0) + (t.secondaryToAdd || 0) > 0) this.qfaDetailScanFilter = 'add';
          else if ((t.toRemove || 0) + (t.secondaryToRemove || 0) > 0) this.qfaDetailScanFilter = 'remove';
          else if ((t.toKeep || 0) + (t.secondaryToKeep || 0) > 0) this.qfaDetailScanFilter = 'keep';
          else this.qfaDetailScanFilter = 'add';
          break;
        }
        case 'recover':
          this.qfaDetailRecover = p.response;
          this.qfaDetail = 'recover';
          this.qfaDetailRecoverFilter = 'all';
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
          // Discover already has a polished modal — reuse it. Stash
          // the user's previous standalone-Discover result so a QFA
          // drill-in doesn't clobber their own scan when closed.
          this._qfaStashedDiscover = this.scanResults.discover;
          this._qfaDiscoverActive = true;
          this.scanResults.discover = p.response;
          this.showDiscoverResultsModal = true;
          break;
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

    // ===== QFA Tag-modal helpers — mirror scanFilter / decisionsByGroup
    // / scanFilterCounts / noFileScanItems / missingScanItems but read
    // from qfaDetailTag instead of scanResults.tag. Same UX as the
    // standalone Tag results panel.
    qfaDetailScanFilterCounts() {
      const t = this.qfaDetailTag;
      const out = { add: 0, remove: 0, keep: 0, nofile: 0 };
      if (!t || !Array.isArray(t.items)) return out;
      const inst = this.qfaDetailScanInstanceFilter || 'both';
      for (const it of t.items) {
        if (it.noFile) { out.nofile++; continue; }
        for (const d of (it.decisions || [])) {
          const a = d.action;
          if (inst === 'secondary') {
            const sa = d.secondaryAction;
            if (sa === 'add')    out.add++;
            if (sa === 'remove') out.remove++;
            if (sa === 'keep')   out.keep++;
          } else if (inst === 'primary') {
            if (a === 'add')    out.add++;
            if (a === 'remove') out.remove++;
            if (a === 'keep')   out.keep++;
          } else {
            // 'both' — count primary, then add secondary on top.
            if (a === 'add')    out.add++;
            if (a === 'remove') out.remove++;
            if (a === 'keep')   out.keep++;
            const sa = d.secondaryAction;
            if (sa === 'add')    out.add++;
            if (sa === 'remove') out.remove++;
            if (sa === 'keep')   out.keep++;
          }
        }
      }
      return out;
    },
    qfaDetailDecisionsByGroup() {
      const t = this.qfaDetailTag;
      if (!t || !Array.isArray(t.items)) return [];
      const filter = this.qfaDetailScanFilter || 'add';
      const inst = this.qfaDetailScanInstanceFilter || 'both';
      // Skip nofile/missing — these have their own list rendered below.
      if (filter === 'nofile' || filter === 'missing') return [];
      const byGroup = new Map();
      for (const it of t.items) {
        if (it.noFile) continue;
        for (const d of (it.decisions || [])) {
          // Decide which action to compare against the filter chip.
          let actionMatches = false;
          let primaryAction = d.action;
          let secondaryAction = d.secondaryAction;
          if (inst === 'secondary') {
            actionMatches = secondaryAction === filter;
          } else if (inst === 'primary') {
            actionMatches = primaryAction === filter;
          } else {
            actionMatches = primaryAction === filter || secondaryAction === filter;
          }
          if (!actionMatches) continue;
          const key = d.groupId || d.groupTag || '(unknown)';
          let g = byGroup.get(key);
          if (!g) {
            g = {
              group: { id: key, tag: d.groupTag || key, display: d.groupDisplay || d.groupTag || key },
              totals: { add: 0, remove: 0, keep: 0 },
              items: [],
            };
            byGroup.set(key, g);
          }
          if (primaryAction === 'add')    g.totals.add++;
          if (primaryAction === 'remove') g.totals.remove++;
          if (primaryAction === 'keep')   g.totals.keep++;
          // Build a flat item shape compatible with the standalone-panel
          // markup so we can mirror its drill-in row layout (title +
          // chips + file context).
          g.items.push({
            id: it.movieId || it.id,
            title: it.title,
            year: it.year,
            tmdbId: it.tmdbId,
            releaseGroup: it.releaseGroup,
            sceneName: it.sceneName,
            relativePath: it.relativePath,
            quality: d.quality,
            qualityDetail: d.qualityDetail,
            audio: d.audio,
            audioDetail: d.audioDetail,
            matched: d.matched,
            matchLocation: d.matchLocation,
            reason: d.reason,
            action: primaryAction,
            secondaryAction: secondaryAction || '',
          });
        }
      }
      // Match the standalone Tag library sort: items inside each group
      // sorted alphabetically by title, then groups sorted alphabetically
      // by tag (case-insensitive). The earlier "biggest-bucket-first"
      // sort surprised users — the standalone view orders by tag, so
      // switching from one to the other shuffled the same data.
      const out = [];
      for (const g of byGroup.values()) {
        g.items.sort((a, b) => a.title.localeCompare(b.title, undefined, { sensitivity: 'base' }));
        out.push(g);
      }
      out.sort((a, b) => a.group.tag.localeCompare(b.group.tag, undefined, { sensitivity: 'base' }));
      return out;
    },
    qfaDetailNoFileItems() {
      const t = this.qfaDetailTag;
      if (!t || !Array.isArray(t.items) || (this.qfaDetailScanFilter || 'add') !== 'nofile') return [];
      return t.items.filter(it => it.noFile);
    },
    qfaDetailMissingItems() {
      const t = this.qfaDetailTag;
      if (!t || !Array.isArray(t.items) || (this.qfaDetailScanFilter || 'add') !== 'missing') return [];
      return t.items.filter(it => (it.decisions || []).some(d => d.secondaryAction === 'missing'));
    },
    qfaDetailSecondaryName() {
      const t = this.qfaDetailTag;
      if (!t || !t.instance) return 'secondary';
      const sec = this.instances.find(i => i.id !== t.instance.id && i.type === 'radarr');
      return (sec && sec.name) || 'secondary';
    },

    // ===== Recover-modal helpers ===================================
    // Field name is `status` not `bucket` — the inline panel uses
    // it.status throughout (see index.html:1013). Modal had a typo
    // that made every chip return 0 rows even when totals had counts.
    qfaDetailRecoverFiltered() {
      const r = this.qfaDetailRecover;
      if (!r || !Array.isArray(r.recover)) return [];
      const f = this.qfaDetailRecoverFilter || 'all';
      if (f === 'all') return r.recover;
      return r.recover.filter(it => (it.status || '').toLowerCase() === f);
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

    // Movies whose AutoDecisions have at least one entry matching
    // the active filter chip. Each item is annotated with the
    // filtered subset of decisions (so the row only shows matching
    // tags, not the full decision list). Optional tag-filter narrows
    // further to a specific (bucket, tag) pair — set by clicking a
    // row in the per-tag breakdown table.
    qfaDetailAutoFilteredItems() {
      const r = this.qfaDetailAutoActive();
      if (!r || !Array.isArray(r.items)) return [];
      const f = (this.qfaDetailAutoFilter || 'add').toLowerCase();
      const tagF = this.qfaDetailAutoTagFilter;
      const out = [];
      for (const it of r.items) {
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
          return 'ffmpeg or dovi_tool not installed — set ENABLE_DV_TOOLS=true on the container to install them on next start.';
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
        default:          return '(unknown phase)';
      }
    },

    // ---- Mode / tab visibility helpers ----
    ruleAffectsTag()       { const r = this.editingRule; if (!r) return false; return r.mode === 'tag' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('tag')); },
    ruleAffectsDiscover()  { const r = this.editingRule; if (!r) return false; return r.mode === 'discover' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('discover')); },

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
      const r = this.editingRule;
      const o = (r && r.options) || {};
      if (this.ruleAffectsDiscover() && o.discoverWriteBack && o.autoActivateDiscovered) return false;
      return true;
    },
    ruleAffectsRecover()   { const r = this.editingRule; if (!r) return false; return r.mode === 'recover' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('recover')); },
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
      if (r.mode !== 'combined') return false;
      return (r.options.combinedModes || []).includes('audiotags');
    },
    // ruleAffectsVideo: gates the Video tags wizard step (resolution
    // / codec / HDR buckets only — DV detail is its own step now).
    ruleAffectsVideo() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'videotags') return true;
      if (r.mode !== 'combined') return false;
      return (r.options.combinedModes || []).includes('videotags');
    },
    // ruleAffectsDvDetail: gates the DV detail wizard step. Lives
    // on its own fane + step because it requires extra tools and
    // most users won't run it.
    ruleAffectsDvDetail() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'dvdetail') return true;
      if (r.mode !== 'combined') return false;
      return (r.options.combinedModes || []).includes('dvdetail');
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
      if (tabId === 'rg')       return this.ruleAffectsTag() || this.ruleAffectsDiscover();
      if (tabId === 'filters')  return this.ruleAffectsTag() || this.ruleAffectsDiscover();
      if (tabId === 'audio')    return this.ruleAffectsAudio();
      if (tabId === 'video')    return this.ruleAffectsVideo();
      if (tabId === 'dvdetail') return this.ruleAffectsDvDetail();
      // Schedule step: visible only for cron-driven rules (not Manual
      // run only) and never in quickfix mode (quickfix fires once and
      // doesn't persist). Lets the wizard collapse Basics down to a
      // simple "Schedule vs Manual" radio and put the cron pickers on
      // their own step so the flow is shorter and clearer.
      if (tabId === 'schedule') return !this.ruleEditor.isQuickFix && !!this.editingRule && !this.editingRule.manualOnly;
      return false;
    },
    ruleEditorVisibleTabs()  { return this.ruleEditorTabs.filter(t => this.ruleEditorTabVisible(t.id)); },
    ruleEditorVisibleSteps() {
      // 'review' is always last; others share visibility with their tab.
      return this.ruleEditorSteps.filter(s => s === 'review' || s === 'basics' || this.ruleEditorTabVisible(s));
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
    ruleEditorOnInstanceChange() {
      const r = this.editingRule;
      if (!r) return;
      r.releaseGroupIds = this.snapshotGlobalRGIds(r.instanceId);
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
      // Default source is the wizard's editingRule. overrideRule lets
      // "Apply now" re-fire a previous preview run without reopening
      // the wizard (Apply-after-preview lives at the result panel).
      const r = overrideRule || this.editingRule;
      if (!r) return;
      // For Apply-after-preview we use the same UI busy flag — the
      // result panel reads it to disable the button while running.
      this.ruleEditor.busy = true;
      this.ruleEditor.error = '';

      // Decide which phases to run based on mode + combinedModes.
      const phases = [];
      const has = (m) => r.mode === m || (r.mode === 'combined' && (r.options.combinedModes || []).includes(m));
      if (has('discover'))  phases.push('discover');
      if (has('recover'))   phases.push('recover');
      if (has('tag'))       phases.push('tag');
      if (has('audiotags')) phases.push('audiotags');
      if (has('videotags')) phases.push('videotags');
      if (has('dvdetail'))  phases.push('dvdetail');
      if (phases.length === 0) {
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

      const results = {
        startedAt: new Date().toISOString(),
        instance: r.instanceId,
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
      const primaryType = (this.instances.find(i => i.id === r.instanceId) || {}).type;
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
        for (const phase of phases) {
          if (isCancelled()) break;
          const data = await fetchPhase(phase, r.instanceId);
          results.phases.push({ phase, ok: true, response: data });

          // Discover write-back wiring (apply mode): when this phase
          // added new release groups, fold their IDs into the overlay
          // used by subsequent phases. Without this, the next phase's
          // overlay still carries the rule's pre-run RG-ID snapshot —
          // which doesn't include the just-added groups — and the Tag
          // phase silently skips them. Same fix the schedule path's
          // runCombinedSchedule does via local cfg.ReleaseGroups
          // injection; here we extend overlayReleaseGroupIds.
          if (phase === 'discover' && data && data.applied && Array.isArray(data.applied.discoverAdded) && data.applied.discoverAdded.length > 0) {
            const ids = (overlay.overlayReleaseGroupIds || []).slice();
            for (const a of data.applied.discoverAdded) {
              if (a && a.id) ids.push(a.id);
            }
            overlay.overlayReleaseGroupIds = ids;
          }

          // Discover ephemeral injection (preview mode): in preview the
          // backend doesn't write to config, so subsequent phases (Tag)
          // would see zero groups and either error or do nothing. To
          // give the user a meaningful "what would happen" run, we take
          // discover's findings and inject them as ephemeral groups —
          // each marked filtered+enabled with the discovered search
          // string as both search and tag. They live ONLY for this run;
          // backend never persists them.
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

          // Audio + Video tags can optionally run a SECOND time
          // against the secondary instance. Distinct from tag-sync
          // (which mirrors by TmdbID) — auto-tags are mediaInfo-
          // derived per file, so each instance is scanned
          // independently.
          if ((phase === 'audiotags' || phase === 'videotags') &&
              r.options.autoTagsRunOnSecondary &&
              r.options.syncToSecondary && secondaryTarget) {
            if (isCancelled()) break;
            const secData = await fetchPhase(phase, secondaryTarget);
            results.phases.push({ phase, ok: true, response: secData, instanceLabel: 'secondary' });
          }
        }
        results.finishedAt = new Date().toISOString();
        results.ok = true;
        this.quickFixResults = results;
        const verb = runMode === 'apply' ? 'applied' : 'previewed';
        this.showToast('Quick fix-all ' + verb + ': ' + phases.join(', '), 'success');
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
      }
      return {
        startedAt: (run && run.startedAt) || new Date().toISOString(),
        scheduleName: schedule && schedule.name,
        scheduleId: schedule && schedule.id,
        instance: schedule && schedule.instanceId,
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
      this.activityResults = {
        startedAt: new Date().toISOString(),
        scheduleName: sj.name,
        scheduleId: sj.id,
        instance: sj.instanceId,
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
      // Clonarr-style zoom: scales every element (fonts, padding, images) uniformly.
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
