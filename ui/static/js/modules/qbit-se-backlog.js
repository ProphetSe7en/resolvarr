// resolvarr UI — qBit S/E backlog scan modal module
//
// Composed into the Alpine root via { ...appQbitSeBacklog() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appQbitSeBacklog() {
  return {
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
      this.qbitSeBacklogConfig = null;
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
      this.qbitSeBacklogConfig = null;
      this.qbitSeBacklogPreview = null;
      this.qbitSeBacklogApplyResult = null;
      this.qbitSeBacklogSelected = {};
      this.qbitSeBacklogError = '';
    },

    // openQbitSeRunFromConfig drives the SAME backlog-scan modal from
    // the Tag Library one-off "qBit S/E tags" sub-tab: it builds an
    // inline QbitSe config from the sub-tab form (no saved rule), opens
    // the modal, and runs the preview immediately. The preview/apply
    // methods branch on qbitSeBacklogConfig to hit the inline-config
    // endpoints (/api/qbit-se/run/*) instead of the webhook-rule ones.
    openQbitSeRunFromConfig() {
      const f = this.qbitSeRunForm || {};
      if (!f.qbitInstanceId) {
        this.showToast('Pick a qBittorrent instance first', 'error');
        return;
      }
      if (!f.episodeEnabled && !f.seasonEnabled && !f.unmatchedEnabled) {
        this.showToast('Enable at least one of Episode / Season / Unmatched', 'error');
        return;
      }
      this.qbitSeBacklogConfig = {
        qbitInstanceId:   f.qbitInstanceId,
        episodeEnabled:   !!f.episodeEnabled, episodeTag: (f.episodeTag || '').trim(),
        seasonEnabled:    !!f.seasonEnabled,  seasonTag: (f.seasonTag || '').trim(),
        unmatchedEnabled: !!f.unmatchedEnabled, unmatchedTag: (f.unmatchedTag || '').trim(),
      };
      this.qbitSeBacklogRule = null;
      this.qbitSeBacklogOpen = true;
      this.qbitSeBacklogPreview = null;
      this.qbitSeBacklogApplyResult = null;
      this.qbitSeBacklogSelected = {};
      this.qbitSeBacklogCategoryFilter = (f.categoryFilter || '').trim();
      this.qbitSeBacklogFilter = 'taggable';
      this.qbitSeBacklogError = '';
      this.runQbitSeBacklogPreview();
    },

    // Run the preview pass. Pre-selects every taggable row by
    // default; already-tagged + skipped rows start unchecked (the
    // user can flip them on if they want, though already-tagged is
    // a no-op on apply and skipped has no proposed tag).
    async runQbitSeBacklogPreview() {
      if (!this.qbitSeBacklogRule && !this.qbitSeBacklogConfig) return;
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
        const catFilter = (this.qbitSeBacklogCategoryFilter || '').trim();
        const body = this.qbitSeBacklogConfig
          ? { qbitSe: this.qbitSeBacklogConfig, categoryFilter: catFilter }
          : { ruleId: this.qbitSeBacklogRule.id, categoryFilter: catFilter };
        const url = this.qbitSeBacklogConfig
          ? '/api/qbit-se/run/preview'
          : '/api/webhook-rules/qbit-se-backlog/preview';
        const r = await this.apiFetch(url, {
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
      if ((!this.qbitSeBacklogRule && !this.qbitSeBacklogConfig) || !this.qbitSeBacklogPreview) return;
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
        const catFilter = (this.qbitSeBacklogCategoryFilter || '').trim();
        const body = this.qbitSeBacklogConfig
          ? { qbitSe: this.qbitSeBacklogConfig, categoryFilter: catFilter, selectedHashes: selected }
          : { ruleId: this.qbitSeBacklogRule.id, categoryFilter: catFilter, selectedHashes: selected };
        const url = this.qbitSeBacklogConfig
          ? '/api/qbit-se/run/apply'
          : '/api/webhook-rules/qbit-se-backlog/apply';
        const r = await this.apiFetch(url, {
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

    // runAction (the single /api/scan/run request builder) moved to
    // js/modules/run.js, composed via { ...appRunModule() } in app(). See
    // Stage 4 in docs/resolvarr/frontend-restructure-plan.md.

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
      // qBit S/E tags — Sonarr-only qBit-tagging phase via the shared
      // one-off endpoints. Own bucket like plexsync / tbarefresh.
      const runQbitSe = has('qbitsetag');
      if (headPhases.length === 0 && autoPhases.length === 0 && !runMissingEpisodes && !runPlexSync && !runTbaRefresh && !runQbitSe) {
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
        // Resolve the per-action fields from the rule's options; runAction
        // builds + POSTs the body uniformly (one request shape for chain +
        // standalone). See runAction.
        const opts = {};
        if (phase === 'discover') {
          // Master-mode gate: discover write-back only fires in apply mode.
          // The backend forces discover to preview regardless, so this gate
          // is the only place distinguishing preview-of-chain from a real
          // apply. Standalone Discover (Release Groups -> Find new groups)
          // keeps its own explicit + Add flow; this is the chain auto-add.
          opts.discoverWriteBack = !!r.options.discoverWriteBack && runMode === 'apply';
          opts.autoActivateDiscovered = !!r.options.autoActivateDiscovered;
        } else if (phase === 'tag') {
          opts.cleanupUnusedTags = !!r.options.cleanupUnusedTags;
          if (r.options.syncToSecondary && secondaryTarget) opts.syncToInstanceId = secondaryTarget;
          if (r.options.tagSource) {
            opts.tagSource = r.options.tagSource;
            if (r.options.tagSource === 'filter-only') opts.filterOnlyTag = r.options.filterOnlyTag || '';
          }
        } else if (phase === 'recover') {
          opts.recoverRename = true;
        } else if (phase === 'dvdetail') {
          // Per-rule cache bypass — wizard "Skip DV cache on every fire".
          opts.bypassDvCache = !!r.options.bypassDvCache;
        }
        const isDv = phase === 'dvdetail';
        if (isDv) {
          this.dvScanProgress = { running: true, total: 0, processed: 0, currentTitle: '' };
          this.startDvScanPoll();
        }
        try {
          return await this.runAction({ instanceId, action: phase, mode: runMode, overlay, options: opts });
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
          const plexLabelSync = {
            plexInstanceId: ps.plexInstanceId,
            libraryKeys: ps.libraryKeys || [],
            labels: ps.labels || [],
            labelDisplay: ps.labelDisplay || {},
            targetTypes: (ps.targetTypes && ps.targetTypes.length > 0) ? ps.targetTypes : ['label'],
          };
          // plexConfig lets the result drill-in's Apply-now re-fire in apply
          // mode (the run result itself doesn't carry the input config).
          const phaseRow = { phase: 'plexsync', ok: true, instanceId: r.instanceId, plexConfig: { arrInstanceId: r.instanceId, plexLabelSync } };
          try {
            const res = await this.apiFetch('/api/plex-sync/run', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ arrInstanceId: r.instanceId, runMode: runMode, plexLabelSync }),
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

        // Phase 6 — qBit S/E tags (Sonarr only). Tag every torrent in
        // the chosen qBit instance by Season / Episode / Unmatched via
        // the shared one-off endpoints. Apply tags all taggable torrents
        // (no per-row selection in the chain); preview reports the plan.
        if (runQbitSe && !isCancelled() && r.qbitSe) {
          const phaseRow = { phase: 'qbitsetag', ok: true, instanceId: r.instanceId };
          try {
            const url = runMode === 'apply' ? '/api/qbit-se/run/apply' : '/api/qbit-se/run/preview';
            const res = await this.apiFetch(url, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ qbitSe: r.qbitSe }),
            });
            if (!res.ok) {
              const d = await res.json().catch(() => ({}));
              throw new Error(`qbitsetag: ${d.error || 'HTTP ' + res.status}`);
            }
            phaseRow.response = await res.json();
          } catch (e) {
            phaseRow.ok = false;
            phaseRow.error = String((e && e.message) || e);
          }
          results.phases.push(phaseRow);
        }

        results.finishedAt = new Date().toISOString();
        results.ok = true;
        // Stamp the run's overlay onto every phase response so the
        // result-panel "Apply now" buttons re-fire with the SAME config
        // this run used (the rule's overlay), not the global config. The
        // stamp travels with the result via viewPhaseDetails into
        // qfaDetailAudio / qfaDetailVideo / qfaDetailDv / scanResults.tag,
        // where buildSnapshotOverlay reads it. See ui-section-map.md.
        for (const p of results.phases) {
          if (p && p.response) p.response.__overlay = overlay;
        }
        this.quickFixResults = results;
        const verb = runMode === 'apply' ? 'applied' : 'previewed';
        const ranList = [...headPhases, ...autoPhases.map(a => a.phase)];
        if (runMissingEpisodes) ranList.push('missingepisodes');
        if (runPlexSync) ranList.push('plexsync');
        if (runTbaRefresh) ranList.push('tbarefresh');
        if (runQbitSe) ranList.push('qbitsetag');
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
        // Per-action wizard remembers its config SCAN-LOCALLY so the next
        // open pre-fills with the just-used values — WITHOUT mutating the
        // global template (which seeds new rules) or any saved rule.
        // overrideRule (Apply-after-preview) is exempt — re-running a
        // previous decision shouldn't re-save. See frontend-restructure-plan.md.
        if (!overrideRule && this.ruleEditor && this.ruleEditor.fixedAction) {
          this._savePerActionState(this.ruleEditor.fixedAction, this.ruleEditor.appType || 'radarr', r);
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
        // Plex sync / missing-episodes / TBA refresh phases were omitted
        // here, so a schedule run's result showed only their summary line
        // with no clickable "View details" drill-in (unlike audio/video/tag).
        // The combinedScheduleResult persists each, and viewPhaseDetails has
        // a case for each — they just never got a phase row. instanceId is
        // the schedule's (viewPhaseDetails' plexsync case reads p.instanceId).
        if (data.missingEpisodes) phases.push({ phase: 'missingepisodes', ok: true, response: data.missingEpisodes, instanceId: schedule.instanceId });
        if (data.plexSync)        phases.push({ phase: 'plexsync',        ok: true, response: data.plexSync,        instanceId: schedule.instanceId, plexConfig: { arrInstanceId: schedule.instanceId, plexLabelSync: schedule.plexSync || null } });
        if (data.tbaRefresh)      phases.push({ phase: 'tbarefresh',      ok: true, response: data.tbaRefresh,      instanceId: schedule.instanceId });
      }
      // Stamp the schedule's own config as the overlay on every phase
      // response, so an "Apply now" on a schedule-run preview re-fires
      // with the RULE's config instead of the global Library-scan config.
      // Schedule runs land here (activityResults), not in
      // quickFixResults — without this stamp apply-now silently fell back
      // to globals (the 2026-06-03 saved-rule bug). See ui-section-map.md.
      const schedOverlay = this.overlayFromRule(schedule);
      for (const p of phases) {
        if (p && p.response) p.response.__overlay = schedOverlay;
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
