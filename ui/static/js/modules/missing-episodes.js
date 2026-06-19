// resolvarr UI — missing-episodes (extracted from app.js, Stage 4 split).
// Composed via { ...appMissingEpisodes() } in app(); methods bind `this` to the Alpine component.
function appMissingEpisodes() {
  return {
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
  };
}
