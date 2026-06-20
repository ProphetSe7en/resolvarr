// resolvarr UI — qfa-drillin (extracted from app.js, Stage 4 split).
// Composed via { ...appQfaDrillin() } in app(); methods bind `this` to the Alpine component.
function appQfaDrillin() {
  return {
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

    // qfaDetailAutoNounPlural / qfaDetailAutoNounSingular pick the
    // right word for the active QFA result based on the instance
    // type. Sonarr Audio/Video tags apply at the series level (per
    // the help text in the rule editor), so "series" is the right
    // word — not "episode". Radarr is "movie" / "movies".
    qfaDetailAutoNounPlural() {
      const r = this.qfaDetailAutoActive();
      if (r && r.instance && r.instance.type === 'sonarr') return 'series';
      return 'movies';
    },
    qfaDetailAutoNounSingular() {
      const r = this.qfaDetailAutoActive();
      if (r && r.instance && r.instance.type === 'sonarr') return 'series';
      return 'movie';
    },
    // Capitalised plural for sentence-start titles + tooltips. Saves
    // .charAt(0).toUpperCase() inlining in every template.
    qfaDetailAutoNounPluralCap() {
      return this.qfaDetailAutoNounPlural() === 'series' ? 'Series' : 'Movies';
    },

    // currentSectionId resolves the active page (+ sub-tab) to the
    // stable section-id from docs/resolvarr/ui-section-map.md. Rendered
    // as a dev-only corner badge so a bug report can name the exact
    // section both of us read in the code. Keep this switch in lockstep
    // with the doc when adding/renaming sub-tabs.
    currentSectionId() {
      const scanMap = {
        run: 'SCAN-RUN', groups: 'SCAN-GROUPS', filters: 'SCAN-FILTERS',
        recover: 'SCAN-RECOVER', audio: 'SCAN-AUDIO', video: 'SCAN-VIDEO',
        dvdetail: 'SCAN-DV', 'missing-episodes': 'SCAN-MISSINGEP',
        'tba-refresh': 'SCAN-TBA', 'plex-sync': 'SCAN-PLEXSYNC', history: 'SCAN-HISTORY',
      };
      switch (this.currentPage) {
        case 'scan':     return scanMap[this.scanSection] || 'P-SCAN';
        case 'tags':     return 'P-TAGS';
        case 'lists':    return 'P-LISTS';
        case 'webhooks': return this.webhookSection === 'activity' ? 'WH-ACTIVITY' : 'WH-SETUP';
        case 'settings': return 'P-SETTINGS';
      }
      return this.currentPage || '';
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

    // Movies / series whose AutoDecisions have at least one entry
    // matching the active filter chip. Each item is annotated with
    // the filtered subset of decisions (so the row only shows
    // matching tags, not the full decision list). Optional tag-filter
    // narrows further to a specific (bucket, tag) pair.
    //
    // Series with a non-empty error field (Sonarr fetch failure) are
    // ALWAYS surfaced — they have no decisions to filter on, but
    // they're failures the user needs to see. Without this branch,
    // Sonarr fetch errors disappear silently (the row exists in the
    // response but no chip catches it).
    qfaDetailAutoFilteredItems() {
      const r = this.qfaDetailAutoActive();
      if (!r || !Array.isArray(r.items)) return [];
      const f = (this.qfaDetailAutoFilter || 'add').toLowerCase();
      const tagF = this.qfaDetailAutoTagFilter;
      const out = [];
      for (const it of r.items) {
        if (it.error) {
          out.push({ ...it, decisionsFiltered: [], _errorRow: true });
          continue;
        }
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

    // Count of error rows in the active scan response — used to
    // surface a "N series failed to fetch" banner above the chip row
    // when there are any. Sonarr-only in practice (Radarr handler
    // doesn't produce error rows in this shape) but cheap to evaluate
    // on Radarr too.
    qfaDetailAutoErrorCount() {
      const r = this.qfaDetailAutoActive();
      if (!r || !Array.isArray(r.items)) return 0;
      let n = 0;
      for (const it of r.items) if (it.error) n++;
      return n;
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

  };
}
