// resolvarr UI — Scan history adhoc dumps module
//
// Composed into the Alpine root via { ...appScanHistory() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appScanHistory() {
  return {
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
      // Scheduled-rule rows drill in through the schedule run-result
      // endpoint (keyed by schedule id + startedAt), not the adhoc
      // per-file endpoint — they have no scan-*.json dump. Reuse the
      // schedule run-details viewer, which handles combined runs +
      // every phase. Prefer the loaded schedule object (carries the
      // plexSync config for Apply-now); fall back to a minimal stub
      // built from the row when the schedule list isn't loaded.
      if (row.source === 'schedule') {
        // Prefer the loaded schedule (carries the full rule config, so an
        // Apply-now from the drill-in re-fires with the RULE's overlay,
        // not the global config). Load it first if the list isn't in
        // memory; only fall back to the config-less stub if that fails.
        if (!this.schedules || this.schedules.length === 0) {
          try { await this.loadSchedules(); } catch (e) { /* fall through to stub */ }
        }
        const sched = (this.schedules || []).find(s => s.id === row.scheduleId)
          || { id: row.scheduleId, mode: row.action, instanceId: row.instanceId, name: row.scheduleName, plexSync: null };
        const run = ((sched.history || []).find(h => h.startedAt === row.timestamp))
          || { startedAt: row.timestamp, resultPath: 'schedule-run' };
        // The schedule result renders in the Run-mode "Run result" panel
        // (multi-phase, lives under scanSection 'run'), not as a top-level
        // modal like the adhoc rows. Switch to that section so the result
        // is actually visible instead of populating a hidden panel.
        await this.viewScheduleRunDetails(sched, run);
        if (!this.historyResultError) this.scanSection = 'run';
        return;
      }
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
  };
}
