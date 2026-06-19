// resolvarr UI — recover-exclusions (extracted from app.js, Stage 4 split).
// Composed via { ...appRecoverExclusions() } in app(); methods bind `this` to the Alpine component.
function appRecoverExclusions() {
  return {
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

    // isRecoverItemExcluded reports whether a recover result ROW is
    // currently excluded, so the active result view + apply selection
    // drop it the instant the user clicks Exclude — not only on the next
    // scan. Sonarr rows match by series/season (whole-series covered by
    // isSeasonExcluded); Radarr rows are movies, matched by movie id
    // (= row id, what excludeMovie(it.id) wrote).
    isRecoverItemExcluded(it) {
      if (!it) return false;
      if (this.recoverResults && this.recoverResults.instance && this.recoverResults.instance.type === 'sonarr') {
        if (typeof it.seasonNumber === 'number') return this.isSeasonExcluded(it.seriesId, it.seasonNumber);
        return this.isSeriesExcluded(it.seriesId);
      }
      return this.isMovieExcluded(it.id);
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

  };
}
