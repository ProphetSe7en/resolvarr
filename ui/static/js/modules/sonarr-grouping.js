// resolvarr UI — Sonarr per-series episode grouping module
//
// Composed into the Alpine root via { ...appSonarrGrouping() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appSonarrGrouping() {
  return {
    // ---- Sonarr per-series episode grouping (M-Sonarr Audio/Video) ----
    //
    // Sonarr audio/video scans return per-series rows whose Episodes[]
    // payload carries one entry per episodefile. The drill-in's expanded
    // view renders these grouped season → episodes (collapsible
    // seasons, flat episodes inside) — same pattern Recover-Sonarr uses
    // (partials/recover-result-panel.html). Pure data-shaping; no I/O.
    //
    // Empty / non-Sonarr items return [] so the markup branch can
    // safely no-op render against any item.

    episodesGroupedBySeason(item) {
      if (!item || !Array.isArray(item.episodes) || item.episodes.length === 0) return [];
      // Episode-number extraction for stable per-season ordering.
      // localeCompare on relativePath sorts S01E10 BEFORE S01E2
      // lexicographically, so we mine the SxxExx token and sort by
      // (season, episode) numeric. Falls back to episodeFileId when
      // the token isn't found (mid-process renames).
      const epOrderKey = (ev) => {
        const m = (ev.relativePath || '').match(/S(\d+)E(\d+)/i);
        if (m) return parseInt(m[1], 10) * 1000 + parseInt(m[2], 10);
        return ev.episodeFileId || 0;
      };
      const buckets = new Map();
      for (const ep of item.episodes) {
        const k = (typeof ep.seasonNumber === 'number') ? ep.seasonNumber : 'unknown';
        if (!buckets.has(k)) buckets.set(k, []);
        buckets.get(k).push(ep);
      }
      const out = [];
      for (const [k, eps] of buckets.entries()) {
        eps.sort((a, b) => epOrderKey(a) - epOrderKey(b));
        out.push({
          seasonNumber: typeof k === 'number' ? k : null,
          episodes: eps,
        });
      }
      out.sort((a, b) => {
        // Specials (season 0) ahead of "Unknown" (null); regular
        // seasons ascending.
        if (a.seasonNumber === null && b.seasonNumber === null) return 0;
        if (a.seasonNumber === null) return 1;
        if (b.seasonNumber === null) return -1;
        return a.seasonNumber - b.seasonNumber;
      });
      return out;
    },

    // Reused state map: { seriesId: { seasonNumber: true } } so each
    // (series, season) pair toggles independently. Lives on root state
    // not item-local because Alpine x-for keys would re-create item
    // objects on every reactivity tick and clobber expanded-state.
    qfaDetailSeasonExpanded: {},

    toggleQfaDetailSeasonExpanded(seriesId, seasonNumber) {
      if (!this.qfaDetailSeasonExpanded[seriesId]) {
        this.qfaDetailSeasonExpanded[seriesId] = {};
      }
      const k = seasonNumber === null ? 'unknown' : seasonNumber;
      this.qfaDetailSeasonExpanded[seriesId][k] = !this.qfaDetailSeasonExpanded[seriesId][k];
    },

    qfaDetailSeasonIsExpanded(seriesId, seasonNumber) {
      const sm = this.qfaDetailSeasonExpanded[seriesId];
      if (!sm) return false;
      const k = seasonNumber === null ? 'unknown' : seasonNumber;
      return !!sm[k];
    },

    // Format "S01E05" or "S01" fallback for an episode row label.
    // ev.relativePath usually carries the full release name; we mine
    // out the first SxxExx token to render a compact label. When the
    // file doesn't yield one (mid-process renames), fall back to
    // "S<season>" so the row still has identity. Mirrors backend
    // sonarrEpisodeLabel().
    qfaEpisodeLabel(ev) {
      if (!ev) return '';
      const m = (ev.relativePath || '').match(/S\d+E\d+(?:[E-]\d+)*/i);
      if (m) return m[0].toUpperCase();
      if (typeof ev.seasonNumber === 'number') {
        return 'S' + String(ev.seasonNumber).padStart(2, '0');
      }
      return '';
    },

    // Compact one-line summary the per-episode row shows next to its
    // S01E05 label. Pulls from the strings the backend pre-computed
    // via SummariseMediaInfo (resolution / videoCodec / hdr / audio /
    // channels). Skips empty pieces so the line stays clean.
    qfaEpisodeMediaLine(ev) {
      if (!ev) return '';
      const parts = [];
      if (ev.resolution) parts.push(ev.resolution);
      if (ev.videoCodec) parts.push(ev.videoCodec);
      if (ev.videoBitDepth === 10) parts.push('10bit');
      if (ev.hdr && ev.hdr !== 'sdr') parts.push(ev.hdr);
      if (ev.audioCodec) parts.push(ev.audioCodec);
      if (ev.audioChannels) parts.push(ev.audioChannels);
      if (ev.hasAtmos) parts.push('atmos');
      return parts.join(' · ');
    },

    // Friendly header label for a season row.
    qfaSeasonLabel(seasonNumber) {
      if (seasonNumber === null) return 'Unknown season';
      if (seasonNumber === 0) return 'Specials';
      return 'Season ' + seasonNumber;
    },

  };
}
