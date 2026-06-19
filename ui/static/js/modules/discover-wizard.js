// resolvarr UI — discover-wizard (extracted from app.js, Stage 4 split).
// Composed via { ...appDiscoverWizard() } in app(); methods bind `this` to the Alpine component.
function appDiscoverWizard() {
  return {
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
        // Resolve sync target / cleanup / tag-source from page state;
        // runAction builds + POSTs the body uniformly. buildSnapshotOverlay
        // carries the rule's filters + release-group subset when this Tag
        // run came from a QFA/rule preview (Apply-now writes the rule's
        // config, not global filters / Active list); {} for a true
        // standalone Library-scan run. See buildSnapshotOverlay + runAction.
        const tagOpts = {};
        if (this.scanSyncToSecondary) {
          // Prefer the explicit picker selection; fall back to first
          // other-of-same-type when the user has only one candidate
          // (legacy auto-pick) or hasn't touched the picker yet.
          let target = this.scanSyncTargetId;
          if (!target || !this.instances.find(i => i.id === target && i.type === 'radarr' && i.id !== this.scanInstanceId)) {
            const sec = this.instances.find(i => i.type === 'radarr' && i.id !== this.scanInstanceId);
            if (sec) target = sec.id;
          }
          if (target) tagOpts.syncToInstanceId = target;
        }
        // cleanupUnusedTags fires when the standalone toggle is on OR a
        // forceMode caller passes it (apply-after-preview re-fire). runAction
        // drops it in filter-only mode (no-op there by design).
        if (this.scanCleanupUnusedTags || opts.cleanupUnusedTags) tagOpts.cleanupUnusedTags = true;
        if (this.scanTagSource) {
          tagOpts.tagSource = this.scanTagSource;
          if (this.scanTagSource === 'filter-only') tagOpts.filterOnlyTag = this.scanFilterOnlyTag;
        }
        this.scanResults.tag = await this.runAction({
          instanceId: this.scanInstanceId,
          action: 'tag',
          mode: requestMode,
          overlay: this.buildSnapshotOverlay(this.scanResults.tag),
          options: tagOpts,
        });
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






  };
}
