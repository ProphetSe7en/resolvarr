// release-type.js — Recover release types (Sonarr): the read-only
// overview, the grab-history Recover (preview + optional qBittorrent
// byte-size verification), and Apply via re-import. Also carries the
// small dev-build banner dismiss. Composed via { ...appReleaseType() }
// in app(). State lives in state.js; this module is methods only.
function appReleaseType() {
  return {
    // Runs the scan and stores the per-series breakdown. There is no
    // apply path — Sonarr only writes releaseType via a re-import, so
    // this view is purely informational.
    async runReleaseTypeOverview() {
      const avail = this.scanAvailableInstances().filter(i => i.type === 'sonarr');
      if (!avail.length) { this.showToast('No Sonarr instances configured', 'error'); return; }
      let id = this.scanInstanceId;
      if (!id || !avail.some(i => i.id === id)) id = avail[0].id;
      this.releaseTypeLoading = true;
      this.releaseTypeError = '';
      this.releaseTypeResults = null;
      this.releaseTypeExpanded = {};
      this.releaseTypeFilter = '';
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: id, action: 'release-type-overview', mode: 'preview' }),
        });
        const body = await resp.text();
        let data = {}; try { data = JSON.parse(body); } catch {}
        if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
        this.releaseTypeResults = data;
        this.showToast('Release type scan complete', 'success');
      } catch (e) {
        this.releaseTypeError = e.message || 'Release type scan failed';
        this.showToast(this.releaseTypeError, 'error');
      } finally {
        this.releaseTypeLoading = false;
      }
    },
    // Click a tally chip to filter by one release type; click the active
    // one again (or "Show all") to clear. The field names match the JSON
    // keys on both the series summary and each season, so the same key
    // filters both levels.
    setReleaseTypeFilter(type) {
      this.releaseTypeFilter = (this.releaseTypeFilter === type) ? '' : type;
    },
    // Series rows for the result table, filtered to those that have at
    // least one file of the selected type. Backend already sorts by title.
    releaseTypeFilteredSeries() {
      if (!this.releaseTypeResults) return [];
      const rows = this.releaseTypeResults.releaseTypeOverview || [];
      const f = this.releaseTypeFilter;
      if (!f) return rows;
      return rows.filter(s => (s[f] || 0) > 0);
    },
    // Seasons within an expanded series, filtered the same way so the
    // drill-down shows only the seasons that carry the selected type.
    releaseTypeFilteredSeasons(s) {
      const f = this.releaseTypeFilter;
      if (!f) return s.seasons || [];
      return (s.seasons || []).filter(se => (se[f] || 0) > 0);
    },
    // Inline style for a tally filter chip — filled when it's the active
    // filter, outlined otherwise.
    releaseTypeChipStyle(type) {
      const active = this.releaseTypeFilter === type;
      const base = 'display:inline-flex;align-items:center;gap:5px;cursor:pointer;padding:3px 10px;border-radius:14px;font-size:12px;';
      return base + (active
        ? 'border:1px solid var(--accent-blue);background:var(--accent-blue);color:#fff;'
        : 'border:1px solid var(--border-subtle);background:var(--bg-card);color:var(--text-secondary);');
    },
    toggleReleaseTypeExpanded(seriesId) {
      this.releaseTypeExpanded[seriesId] = !this.releaseTypeExpanded[seriesId];
    },

    // ---- Recover release types (Sonarr-only, preview) ----
    async runReleaseTypeRecover() {
      const avail = this.scanAvailableInstances().filter(i => i.type === 'sonarr');
      if (!avail.length) { this.showToast('No Sonarr instances configured', 'error'); return; }
      let id = this.scanInstanceId;
      if (!id || !avail.some(i => i.id === id)) id = avail[0].id;
      this.rtrLoading = true;
      this.rtrError = '';
      this.rtrResults = null;
      this.rtrConfFilter = '';
      this.rtrExpanded = {};
      // Drop a stale qBit pick that no longer exists (instance removed).
      if (this.rtrQbitInstanceId && !(this.qbitInstances || []).some(q => q.id === this.rtrQbitInstanceId)) {
        this.rtrQbitInstanceId = '';
      }
      const reqBody = { instanceId: id, action: 'release-type-recover', mode: 'preview' };
      if (this.rtrQbitInstanceId) reqBody.releaseTypeQbitInstanceId = this.rtrQbitInstanceId;
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(reqBody),
        });
        const body = await resp.text();
        let data = {}; try { data = JSON.parse(body); } catch {}
        if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
        this.rtrResults = data;
        const n = (data.totals && data.totals.releaseTypeRecoverCandidates) || 0;
        const conf = (data.totals && data.totals.releaseTypeRecoverQbitConfirmed) || 0;
        this.showToast(n + ' recovery candidate' + (n === 1 ? '' : 's') + ' found' + (conf ? ' · ' + conf + ' confirmed by qBittorrent' : ''), 'success');
      } catch (e) {
        this.rtrError = e.message || 'Recover scan failed';
        this.showToast(this.rtrError, 'error');
      } finally {
        this.rtrLoading = false;
      }
    },
    rtrFilteredCandidates() {
      if (!this.rtrResults) return [];
      const rows = this.rtrResults.releaseTypeRecover || [];
      if (!this.rtrConfFilter) return rows;
      return rows.filter(c => c.confidence === this.rtrConfFilter);
    },
    rtrTypeLabel(t) {
      return ({ seasonPack: 'Season Pack', singleEpisode: 'Single Episode', multiEpisode: 'Multi-Episode', unknown: 'Unknown' })[t] || (t || 'Unknown');
    },
    toggleRtrExpanded(id) {
      this.rtrExpanded[id] = !this.rtrExpanded[id];
    },

    // Set the confidence filter (method form so the chip click is robust).
    rtrSetFilter(v) {
      this.rtrConfFilter = v;
    },
    // Full inline style for a confidence chip. Mirrors the Overview tab's
    // releaseTypeChipStyle so both release-type filters look identical and
    // obviously clickable.
    rtrChipStyle(v) {
      const active = this.rtrConfFilter === v;
      const base = 'display:inline-flex;align-items:center;gap:5px;cursor:pointer;padding:3px 10px;border-radius:14px;font-size:12px;';
      return base + (active
        ? 'border:1px solid var(--accent-blue);background:var(--accent-blue);color:#fff;'
        : 'border:1px solid var(--border-subtle);background:var(--bg-card);color:var(--text-secondary);');
    },

    // ---- Recover release types: apply (Stage C) ----
    // A row is applyable when its type is determined with enough confidence
    // to write. Unconfirmed (a single from the grab name only, no qBit
    // confirmation) is never applyable: a season pack looks identical on
    // disk, so the server refuses it too.
    rtrIsApplyable(c) {
      return c && c.confidence !== 'unconfirmed' && !!c.recoveredType && c.status !== 'fixed';
    },
    rtrApplyableCandidates() {
      return this.rtrFilteredCandidates().filter(c => this.rtrIsApplyable(c));
    },

    // ---- Grouping: series -> season -> episodes (mirrors the Overview tab) ----
    rtrGroupedSeries() {
      const bySeries = new Map();
      for (const c of this.rtrFilteredCandidates()) {
        let g = bySeries.get(c.seriesId);
        if (!g) { g = { seriesId: c.seriesId, seriesTitle: c.seriesTitle, year: c.year, seasons: new Map() }; bySeries.set(c.seriesId, g); }
        let s = g.seasons.get(c.seasonNumber);
        if (!s) { s = { seasonNumber: c.seasonNumber, items: [] }; g.seasons.set(c.seasonNumber, s); }
        s.items.push(c);
      }
      return Array.from(bySeries.values()).map(g => ({
        seriesId: g.seriesId, seriesTitle: g.seriesTitle, year: g.year,
        seasons: Array.from(g.seasons.values()).sort((a, b) => a.seasonNumber - b.seasonNumber),
        count: Array.from(g.seasons.values()).reduce((n, s) => n + s.items.length, 0),
      }));
    },
    rtrSeasonKey(seriesId, seasonNumber) { return seriesId + ':' + seasonNumber; },
    rtrToggleSeriesOpen(id) { this.rtrSeriesOpen[id] = !this.rtrSeriesOpen[id]; },
    rtrToggleSeasonOpen(key) { this.rtrSeasonOpen[key] = !this.rtrSeasonOpen[key]; },

    // ---- Selection (episode / season / series level) ----
    rtrToggleSelect(id) {
      this.rtrSelected[id] = !this.rtrSelected[id];
    },
    // Applyable items inside an arbitrary list of candidate rows.
    rtrApplyableIn(items) {
      return (items || []).filter(c => this.rtrIsApplyable(c));
    },
    rtrGroupAllSelected(items) {
      const rows = this.rtrApplyableIn(items);
      return rows.length > 0 && rows.every(c => this.rtrSelected[c.episodeFileId]);
    },
    rtrToggleGroup(items) {
      const rows = this.rtrApplyableIn(items);
      const select = !this.rtrGroupAllSelected(rows);
      rows.forEach(c => { this.rtrSelected[c.episodeFileId] = select; });
    },
    // Season helpers take a season group; series helpers flatten the seasons.
    rtrSeasonItems(season) { return season ? season.items : []; },
    rtrSeriesItems(group) { return group ? group.seasons.reduce((a, s) => a.concat(s.items), []) : []; },
    rtrSelectedIds() {
      // Only ticks that are still applyable + visible under the current filter.
      const ok = new Set(this.rtrApplyableCandidates().map(c => c.episodeFileId));
      return Object.keys(this.rtrSelected)
        .filter(k => this.rtrSelected[k])
        .map(k => parseInt(k, 10))
        .filter(id => ok.has(id));
    },
    rtrAllApplyableSelected() {
      const rows = this.rtrApplyableCandidates();
      return rows.length > 0 && rows.every(c => this.rtrSelected[c.episodeFileId]);
    },
    rtrToggleSelectAll() {
      const rows = this.rtrApplyableCandidates();
      const select = !this.rtrAllApplyableSelected();
      rows.forEach(c => { this.rtrSelected[c.episodeFileId] = select; });
    },

    // ---- Apply entry points (both funnel through the confirm modal) ----
    // Fix one row immediately (no need to tick anything first).
    rtrApplyOne(c) {
      if (!this.rtrIsApplyable(c)) return;
      this.rtrPendingIds = [c.episodeFileId];
      this.rtrApplyConfirm = true;
    },
    // Fix the rows the user ticked.
    rtrApplySelected() {
      const ids = this.rtrSelectedIds();
      if (ids.length === 0) { this.showToast('Tick at least one file to fix', 'error'); return; }
      this.rtrPendingIds = ids;
      this.rtrApplyConfirm = true;
    },
    rtrPendingRows() {
      const ids = new Set(this.rtrPendingIds || []);
      return (this.rtrResults && this.rtrResults.releaseTypeRecover || []).filter(c => ids.has(c.episodeFileId));
    },
    // Cap the confirm-modal list so a 1000-file selection doesn't render
    // a thousand rows; the count in the title is the real total.
    rtrPendingRowsCapped() {
      return this.rtrPendingRows().slice(0, 50);
    },
    rtrCancelApplyRun() { this.rtrCancelApply = true; },

    // Re-import the pending files SERIES BY SERIES so the run shows progress
    // and can be cancelled, and so each request only scans one series (a
    // single 1000-episode request would time out). The server re-derives the
    // verdict per file, so a tampered client can't force a wrong type.
    async runRtrApply() {
      if (this.rtrApplying) return;
      const ids = this.rtrPendingIds || [];
      if (ids.length === 0) { this.rtrApplyConfirm = false; return; }
      const avail = this.scanAvailableInstances().filter(i => i.type === 'sonarr');
      let instId = this.scanInstanceId;
      if (!instId || !avail.some(i => i.id === instId)) instId = avail.length ? avail[0].id : '';
      if (!instId) { this.showToast('No Sonarr instance', 'error'); return; }

      // Group the selected file IDs by series (from the preview rows).
      const rowById = {};
      (this.rtrResults && this.rtrResults.releaseTypeRecover || []).forEach(c => { rowById[c.episodeFileId] = c; });
      const bySeries = new Map();
      ids.forEach(fid => {
        const c = rowById[fid];
        if (!c) return;
        if (!bySeries.has(c.seriesId)) bySeries.set(c.seriesId, []);
        bySeries.get(c.seriesId).push(fid);
      });

      this.rtrApplying = true;
      this.rtrCancelApply = false;
      this.rtrApplyConfirm = false;
      this.rtrApplyProgress = { done: 0, total: ids.length, fixed: 0, failed: 0 };
      try {
        for (const [seriesId, batch] of bySeries) {
          if (this.rtrCancelApply) break;
          const reqBody = {
            instanceId: instId, action: 'release-type-recover', mode: 'apply',
            releaseTypeApplyItems: batch, releaseTypeApplySeriesIds: [seriesId],
          };
          if (this.rtrQbitInstanceId) reqBody.releaseTypeQbitInstanceId = this.rtrQbitInstanceId;
          try {
            const resp = await this.apiFetch('/api/scan/run', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(reqBody),
            });
            const text = await resp.text();
            let data = {}; try { data = JSON.parse(text); } catch {}
            if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
            // Merge per-row outcome back into the displayed list, but only
            // for the files in this batch (others in the series weren't
            // selected and come back "skipped").
            const inBatch = new Set(batch);
            (data.releaseTypeRecover || []).forEach(r => {
              if (!inBatch.has(r.episodeFileId)) return;
              const ex = rowById[r.episodeFileId];
              if (ex) { ex.status = r.status; ex.error = r.error; ex.recoveredType = r.recoveredType; ex.confidence = r.confidence; }
            });
            this.rtrApplyProgress.fixed += (data.totals && data.totals.releaseTypeRecoverFixed) || 0;
            this.rtrApplyProgress.failed += (data.totals && data.totals.releaseTypeRecoverFixFailed) || 0;
          } catch (e) {
            // Whole-series failure: mark the batch failed and keep going.
            batch.forEach(fid => { const ex = rowById[fid]; if (ex && ex.status !== 'fixed') { ex.status = 'fix-failed'; ex.error = (e && e.message) || 'request failed'; } });
            this.rtrApplyProgress.failed += batch.length;
          }
          this.rtrApplyProgress.done += batch.length;
        }
        this.rtrSelected = {};
        this.rtrPendingIds = [];
        const p = this.rtrApplyProgress;
        const msg = p.fixed + ' file' + (p.fixed === 1 ? '' : 's') + ' sent to Sonarr (it finishes importing in the background)'
          + (p.failed ? ' · ' + p.failed + ' failed' : '')
          + (this.rtrCancelApply ? ' · cancelled' : '');
        this.showToast(msg, p.failed ? 'error' : 'success');
      } finally {
        this.rtrApplying = false;
        this.rtrCancelApply = false;
      }
    },


    // Dismiss the dev banner for this exact version. A later dev build has a
    // different version string, so the banner returns.
    dismissDevBanner() {
      try { localStorage.setItem('resolvarr-dev-banner-dismissed', this.version); } catch {}
      this.devBannerShow = false;
    },
  };
}
