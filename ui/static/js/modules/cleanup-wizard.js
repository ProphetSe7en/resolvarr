// resolvarr UI — cleanup-wizard (extracted from app.js, Stage 4 split).
// Composed via { ...appCleanupWizard() } in app(); methods bind `this` to the Alpine component.
function appCleanupWizard() {
  return {
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
      if (!copy.qbitSe) copy.qbitSe = this.snapshotDefaultQbitSe();
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
    // overlayFromRule + buildSnapshotOverlay moved to js/modules/run.js
    // (composed via { ...appRunModule() }). See Stage 4 in
    // docs/resolvarr/frontend-restructure-plan.md.
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
          // p.plexConfig is stamped on the phase by buildActivityResult
          // (schedule runs) + runQuickFixChain (QFA) — it carries the exact
          // rule config to re-fire in apply mode (the run result itself
          // doesn't carry the input config). Also lets us fill the synthetic
          // rule's targets so the header reads the real Plex, not "(unknown)".
          const pcfg = p.plexConfig || null;
          const ps = (pcfg && pcfg.plexLabelSync) || {};
          this.plexLabelRunModal = {
            open: true,
            stage: 'result',
            rule: {
              name: 'Plex label sync',
              instanceId: (pcfg && pcfg.arrInstanceId) || p.instanceId || '',
              targetTypes: ps.targetTypes || run.targetTypes || [],
              labelDisplay: ps.labelDisplay || {},
              targets: [{ plexInstanceId: ps.plexInstanceId || '', libraryKeys: ps.libraryKeys || [] }],
            },
            runMode: run.runMode || 'apply',
            result: run,
            error: '',
            detailsFilter: '',
            applyConfig: pcfg,
            applying: false,
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

  };
}
