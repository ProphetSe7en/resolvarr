// resolvarr UI — reconcile stuck downloads module
//
// Extracted from app.js (Stage 4 split). Composed into the Alpine root
// via { ...appReconcile() } in app(); methods bind `this` to the
// component, so peer calls (this.apiFetch, this.showToast, ...) work.
function appReconcile() {
  return {
    // ===== Reconcile stuck downloads =====
    // Preview reads the Arr queue + classifies each stuck (completed-but-
    // not-imported) item as redundant (already satisfied by an equal/
    // better file) or needs-attention. Apply changes the qBit category of
    // the selected redundant downloads so the user's cleanup removes them.
    // Open the reconcile config modal — seed instance from the page
    // picker, auto-pick the qBit instance when there's only one, and
    // pre-fetch the Arr's qBit download-client categories so the target
    // category is pre-filled (override-able).
    openReconcileWizard() {
      const insts = this.scanAvailableInstances();
      this.reconcileWizard = {
        open: true,
        instanceId: this.scanInstanceId || (insts[0] && insts[0].id) || '',
        mode: 'preview',
        busy: false,
        error: '',
      };
      this.reconcileOnInstanceChange();
    },
    closeReconcileWizard() { this.reconcileWizard.open = false; },

    // Called on open + whenever the modal's instance picker changes. When
    // the instance actually changed (e.g. Sonarr → Radarr), reset the qBit
    // instance + categories — a different Arr means a different download
    // client + categories. Then re-fetch the categories and auto-pick the
    // qBit instance when there's only one.
    // The qBit instance locked to an Arr instance (Settings → Instances →
    // Default qBittorrent instance), if it still points to a real qBit.
    defaultQbitForArr(arrInstanceId) {
      const inst = (this.instances || []).find(i => i.id === arrInstanceId);
      const id = inst && inst.defaultQbitInstanceId;
      if (id && (this.qbitInstances || []).some(q => q.id === id)) return id;
      return '';
    },

    reconcileOnInstanceChange() {
      const id = this.reconcileWizard.instanceId;
      if (id !== this.reconcileLastInstanceId) {
        this.reconcilePostCategory = '';
        this.reconcilePreCategory = '';
        // Prefer the qBit instance locked to this Arr; else fall through
        // to the single-instance auto-pick below.
        this.reconcileQbitInstanceId = this.defaultQbitForArr(id) || '';
        this.reconcileLastInstanceId = id;
      }
      if (!this.reconcileQbitInstanceId && (this.qbitInstances || []).length === 1) {
        this.reconcileQbitInstanceId = this.qbitInstances[0].id;
      }
      this.fetchReconcileCategories();
    },

    async fetchReconcileCategories() {
      const id = this.reconcileWizard.instanceId;
      if (!id) return;
      try {
        const r = await this.apiFetch('/api/instances/' + encodeURIComponent(id) + '/qbit-categories');
        if (!r.ok) return;
        const d = await r.json();
        this.reconcilePreCategory = d.preImport || '';
        if (!this.reconcilePostCategory.trim() && d.postImport) this.reconcilePostCategory = d.postImport;
      } catch (e) { /* best-effort pre-fill */ }
    },

    // Run from the config modal. Preview opens the result panel (per-row
    // select + Apply selected). Apply recategorises every redundant
    // download immediately using the modal's qBit instance + category.
    async runReconcileWizard() {
      const id = this.reconcileWizard.instanceId;
      if (!id) { this.reconcileWizard.error = 'Pick an instance'; return; }
      const mode = this.reconcileWizard.mode;
      if (mode === 'apply') {
        if (!this.reconcileQbitInstanceId) { this.reconcileWizard.error = 'Pick the qBittorrent instance'; return; }
        if (!this.reconcilePostCategory.trim()) { this.reconcileWizard.error = 'Enter the target category'; return; }
      }
      this.reconcileWizard.busy = true;
      this.reconcileWizard.error = '';
      this.reconcileError = '';
      try {
        const body = { instanceId: id, action: 'reconcile', mode };
        if (mode === 'apply') {
          body.reconcileQbitInstanceId = this.reconcileQbitInstanceId;
          body.reconcilePostCategory = this.reconcilePostCategory.trim();
          // empty reconcileApplyItems → backend acts on ALL redundant
        }
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!resp.ok) {
          const t = await resp.text();
          let msg = t; try { msg = JSON.parse(t).error || t; } catch {}
          this.reconcileWizard.error = msg || ('HTTP ' + resp.status);
          return;
        }
        this.reconcileResults = await resp.json();
        this.reconcileInstanceId = id;
        if (this.reconcileResults.reconcilePreCategory) this.reconcilePreCategory = this.reconcileResults.reconcilePreCategory;
        if (!this.reconcilePostCategory.trim() && this.reconcileResults.reconcilePostCategory) this.reconcilePostCategory = this.reconcileResults.reconcilePostCategory;
        // Default-select redundant rows for the result panel's per-row Apply.
        const sel = {};
        for (const it of (this.reconcileResults.reconcile || [])) {
          if (it.status === 'redundant') sel[it.downloadId] = true;
        }
        this.reconcileApplySelected = sel;
        if (mode === 'apply') {
          const n = (this.reconcileResults.totals && this.reconcileResults.totals.reconcileRecategorised) || 0;
          this.showToast('Recategorised ' + n + ' download' + (n === 1 ? '' : 's'), 'success');
        }
        this.reconcileWizard.open = false;
      } catch (e) {
        this.reconcileWizard.error = e.message || 'Reconcile failed';
      } finally {
        this.reconcileWizard.busy = false;
      }
    },

    reconcileRedundantRows() {
      if (!this.reconcileResults) return [];
      return (this.reconcileResults.reconcile || []).filter(it => it.status === 'redundant' || it.status === 'recategorised');
    },
    reconcileNeedsAttentionRows() {
      if (!this.reconcileResults) return [];
      return (this.reconcileResults.reconcile || []).filter(it => it.status === 'needs-attention' || it.status === 'failed');
    },
    reconcileSelectedCount() {
      return Object.keys(this.reconcileApplySelected).filter(k => !!this.reconcileApplySelected[k]).length;
    },

    async applyReconcile() {
      if (!this.reconcileResults) return;
      if (!this.reconcileQbitInstanceId) { this.showToast('Re-run the scan and pick a qBittorrent instance', 'error'); return; }
      if (!this.reconcilePostCategory.trim()) { this.showToast('Re-run the scan and set a target category', 'error'); return; }
      const ids = Object.keys(this.reconcileApplySelected).filter(k => !!this.reconcileApplySelected[k]);
      if (ids.length === 0) { this.showToast('Select at least one redundant download', 'error'); return; }
      this.reconcileApplying = true;
      this.reconcileError = '';
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.reconcileInstanceId || this.scanInstanceId,
            action: 'reconcile',
            mode: 'apply',
            reconcileQbitInstanceId: this.reconcileQbitInstanceId,
            reconcilePostCategory: this.reconcilePostCategory.trim(),
            reconcileApplyItems: ids,
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body; try { msg = JSON.parse(body).error || body; } catch {}
          this.reconcileError = msg || ('HTTP ' + resp.status);
          this.showToast('Reconcile apply failed: ' + this.reconcileError, 'error');
          return;
        }
        this.reconcileResults = await resp.json();
        const n = (this.reconcileResults.totals && this.reconcileResults.totals.reconcileRecategorised) || 0;
        this.showToast('Recategorised ' + n + ' download' + (n === 1 ? '' : 's'), 'success');
        const sel = {};
        for (const it of (this.reconcileResults.reconcile || [])) {
          if (it.status === 'redundant') sel[it.downloadId] = true;
        }
        this.reconcileApplySelected = sel;
      } catch (e) {
        this.reconcileError = e.message || 'Reconcile apply failed';
        this.showToast(this.reconcileError, 'error');
      } finally {
        this.reconcileApplying = false;
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
      // Drop any selected id whose row is now excluded — excluding after
      // ticking must never apply that row (belt-and-braces; the backend
      // also re-checks exclusions on the apply scan).
      const excludedIds = new Set(
        (this.recoverResults.recover || [])
          .filter(it => this.isRecoverItemExcluded(it))
          .map(it => it.id)
      );
      const ids = Object.keys(this.recoverApplySelected)
        .filter(k => !!this.recoverApplySelected[k])
        .map(k => parseInt(k, 10))
        .filter(id => !excludedIds.has(id));
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
        .filter(it => !this.isRecoverItemExcluded(it))
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
        if (this.isRecoverItemExcluded(it)) continue;
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
      // Drop currently-excluded rows from the active view — they move to
      // the "Show excluded" panel the moment Exclude is clicked, so the
      // user gets immediate feedback (the row disappears here).
      const list = (this.recoverResults.recover || []).filter(it => !this.isRecoverItemExcluded(it));
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
  };
}
