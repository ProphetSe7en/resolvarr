// resolvarr UI — DV detail drill-in helpers module
//
// Composed into the Alpine root via { ...appDvDetailDrillin() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appDvDetailDrillin() {
  return {
    // ---- DV detail drill-in helpers ----
    // Same UX shape as the audio/video drill-in (qfaDetailAutoActive +
    // friends) but reads the DV-specific item fields: dvDecisions
    // (action+status+tag, no bucket), dvDetail (profile/cmVersion/layer),
    // dvStatus (cached/extracted/tools-missing/failed/skipped).

    pickDvDetailFilter(r) {
      if (!r || !r.totals) return 'add';
      const t = r.totals;
      if ((t.toAdd || 0) > 0)    return 'add';
      if ((t.toRemove || 0) > 0) return 'remove';
      if ((t.toKeep || 0) > 0)   return 'keep';
      return 'add';
    },

    // Per-status counts for the secondary chip row (cached / extracted /
    // failed). Reads each item's dvStatus once. Items without DV
    // (dvStatus === 'no-dv' or empty) are excluded from these counts —
    // those rows produce no decisions and aren't useful here.
    qfaDetailDvStatusCounts() {
      const r = this.qfaDetailDv;
      const out = { cached: 0, extracted: 0, failed: 0, 'tools-missing': 0, skipped: 0 };
      if (!r || !Array.isArray(r.items)) return out;
      for (const it of r.items) {
        const s = (it.dvStatus || '').toLowerCase();
        if (s && s !== 'no-dv' && out[s] !== undefined) out[s]++;
      }
      return out;
    },

    // Per-action counts (movie-level: a movie counts once per action it
    // contains, even if multiple decisions share the action). Drives
    // the action-chip badges.
    qfaDetailDvFilterCounts() {
      const r = this.qfaDetailDv;
      const out = { add: 0, remove: 0, keep: 0 };
      if (!r || !Array.isArray(r.items)) return out;
      for (const it of r.items) {
        const decs = it.dvDecisions || [];
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

    // Movies whose dvDecisions match the active action chip; further
    // narrowed by qfaDetailDvStatusFilter (cached/extracted/failed)
    // and qfaDetailDvTagFilter (specific tag from a breakdown click).
    // Each item is annotated with the matching subset so the row shows
    // only the relevant decisions.
    qfaDetailDvFilteredItems() {
      const r = this.qfaDetailDv;
      if (!r || !Array.isArray(r.items)) return [];
      const f = (this.qfaDetailDvFilter || 'add').toLowerCase();
      const sf = this.qfaDetailDvStatusFilter;
      const tagF = this.qfaDetailDvTagFilter;
      const out = [];
      for (const it of r.items) {
        if (sf && (it.dvStatus || '').toLowerCase() !== sf) continue;
        const decs = it.dvDecisions || [];
        const matched = decs.filter(d => {
          if ((d.action || '').toLowerCase() !== f) return false;
          if (tagF && (d.tag || '').toLowerCase() !== (tagF.tag || '').toLowerCase()) return false;
          return true;
        });
        if (matched.length === 0) continue;
        out.push({ ...it, decisionsFiltered: matched });
      }
      return out;
    },

    // Click-handler for a per-tag breakdown row (DV variant). Mirrors
    // setAutoTagFilter — sets action chip + tag filter; clicking the
    // same row again clears.
    setDvTagFilter(action, tag) {
      const cur = this.qfaDetailDvTagFilter;
      if (cur && cur.tag === tag && this.qfaDetailDvFilter === action) {
        this.qfaDetailDvTagFilter = null;
        return;
      }
      this.qfaDetailDvFilter = action;
      this.qfaDetailDvTagFilter = { tag, action };
    },

    clearDvTagFilter() {
      this.qfaDetailDvTagFilter = null;
    },

    // Pretty short-form for the DV detail summary line on a row, e.g.
    // "Profile 8 · MEL · CM v2.9". Reads from the dvDetail blob.
    qfaDvDetailSummary(it) {
      const d = it && (it.dvDetail);
      if (!d) return '';
      const parts = [];
      if (d.profile)   parts.push('Profile ' + d.profile);
      if (d.layer)     parts.push(d.layer.toUpperCase());
      if (d.cmVersion) parts.push('CM v' + (d.cmVersion === 4 ? '4.0' : '2.9'));
      return parts.join(' · ');
    },

    // Plain-English description of a dvStatus value. Used as the
    // tooltip on each status chip + each per-row pill so users don't
    // have to memorise the vocabulary. Keep in sync with the engine's
    // status emit logic in scan_dv_detail.go.
    dvStatusExplain(status) {
      const s = (status || '').toLowerCase();
      switch (s) {
        case 'cached':
          return 'DV detail was already extracted on a previous scan and read from /config/dv-cache.json — no ffmpeg work needed this time.';
        case 'extracted':
          return 'DV detail was freshly extracted from the file via ffmpeg + dovi_tool during this scan, then cached.';
        case 'failed':
          return 'Extraction tried but failed — file unreachable, RPU corrupt, or an ffmpeg/dovi_tool error. See the per-row error in the expanded view.';
        case 'tools-missing':
          return 'ffmpeg or dovi_tool not on PATH. Tools ship baked into the image; if you see this status the image build is broken — check docker logs and report the issue.';
        case 'skipped':
          return 'Not a Dolby Vision file (Radarr mediaInfo says so) — DV detail does not run.';
        case 'no-dv':
          return 'Not a Dolby Vision file — skipped before extraction.';
        default:
          return 'DV detail status: ' + s;
      }
    },

    // dvStatus pill colour. Returns object form (not a string) so
    // Alpine merges with the static pill style instead of replacing
    // it. The string-form trap documented in CLAUDE.md (M4 results-
    // table grid bug) bites the per-row pill markup that pairs a
    // static font-size/padding/border-radius style with this binding.
    qfaDvStatusStyle(status) {
      const s = (status || '').toLowerCase();
      if (s === 'cached')        return { color: 'var(--accent-green)', background: '#0d2f1a' };
      if (s === 'extracted')     return { color: 'var(--accent-sky)', background: '#0c1f33' };
      if (s === 'failed')        return { color: 'var(--accent-red)', background: 'var(--accent-red-bg)' };
      if (s === 'tools-missing') return { color: 'var(--accent-orange)', background: '#3d2f0a' };
      return { color: 'var(--text-secondary)', background: 'var(--bg-card)' };
    },

    // Phase-row label — usually just the phase name, but when audio
    // or video
    // tags ran twice (primary + secondary), show the instance name
    // alongside so the two rows are distinguishable.
    quickFixPhaseLabel(p) {
      if (!p) return '';
      // Audio + Video tags can run twice in one chain (primary +
      // secondary). When that happens, suffix with instance name to
      // distinguish the rows.
      if ((p.phase === 'audiotags' || p.phase === 'videotags') &&
          p.response && p.response.instance && p.response.instance.name) {
        const list = (this.quickFixResults && this.quickFixResults.phases) || [];
        const count = list.filter(x => x.phase === p.phase).length;
        if (count > 1) return p.phase + ' · ' + p.response.instance.name;
      }
      return p.phase;
    },

    // Per-phase summary string for the result panel — pulls a short
    // line from the response based on which action ran.
    //
    // Field names match scan_types.go scanTotals: toAdd / toRemove /
    // toKeep / noFile / discovered / recoverWouldFix / etc. The
    // Discovered LIST lives at p.response.discovered (top-level) —
    // separate from the Discovered COUNT at totals.discovered. Tag/
    // extratags totals also have secondary-* counterparts populated
    // when sync was on; surfaced inline for visibility.
    quickFixPhaseSummary(p) {
      if (!p || !p.response) return '(no response)';
      const t = p.response.totals || {};
      switch (p.phase) {
        case 'tag': {
          const parts = [
            `${t.toAdd || 0} to add`,
            `${t.toRemove || 0} to remove`,
            `${t.toKeep || 0} to keep`,
            `${t.noFile || 0} no file`,
          ];
          if (t.secondaryToAdd || t.secondaryToRemove || t.secondaryMissing) {
            parts.push(`secondary +${t.secondaryToAdd || 0} / -${t.secondaryToRemove || 0} / missing ${t.secondaryMissing || 0}`);
          }
          return parts.join(' · ');
        }
        case 'discover': {
          const list = p.response.discovered || [];
          return `${list.length} new ${list.length === 1 ? 'group' : 'groups'} found`;
        }
        case 'recover':
          return `${t.recoverWouldFix || 0} would fix · ${t.recoverFixed || 0} fixed · ${t.recoverFlagged || 0} flagged · ${t.recoverNoHistory || 0} no history`;
        case 'audiotags':
        case 'videotags': {
          const parts = [
            `${t.toAdd || 0} to add`,
            `${t.toRemove || 0} to remove`,
            `${t.toKeep || 0} to keep`,
          ];
          if (t.missingMediaInfo) parts.push(`${t.missingMediaInfo} missing mediaInfo`);
          return parts.join(' · ');
        }
        case 'dvdetail': {
          const parts = [
            `${t.dvCandidates || 0} DV candidates`,
            `${t.toAdd || 0} to add`,
            `${t.toRemove || 0} to remove`,
          ];
          if (t.dvExtractFailed) parts.push(`${t.dvExtractFailed} failed`);
          return parts.join(' · ');
        }
        case 'missingepisodes': {
          // Response shape comes from missingEpisodesPreviewResponse:
          // { seriesScanned, seriesWithGaps, totalMissingEpisodes }.
          // Apply-step results (tagApplied / searchApplied) live on the
          // phase row itself, not on p.response — render them inline so
          // users see what the chain actually wrote.
          const r = p.response || {};
          const parts = [
            `${r.seriesScanned || 0} scanned`,
            `${r.seriesWithGaps || 0} with gaps`,
            `${r.totalMissingEpisodes || 0} missing episodes`,
          ];
          if (p.tagApplied) parts.push(`tagged ${p.tagApplied.applied || 0}, untagged ${p.tagApplied.removed || 0}`);
          if (p.searchApplied) parts.push(`search triggered for ${p.searchApplied.triggered || 0}`);
          if (p.tagError) parts.push(`tag error: ${p.tagError}`);
          if (p.searchError) parts.push(`search error: ${p.searchError}`);
          return parts.join(' · ');
        }
        case 'plexsync': {
          // p.response is a PlexLabelRuleRun (no .totals). Prefer its
          // one-line summary; fall back to counts from the Added /
          // Removed / InSync maps.
          const run = p.response || {};
          if (run.summary) return run.summary;
          const sum = (m) => m ? Object.values(m).reduce((a, b) => a + b, 0) : 0;
          return `${run.matched || 0} matched · ${sum(run.added)} added · ${sum(run.removed)} removed · ${sum(run.inSync)} in sync`;
        }
        case 'tbarefresh': {
          const pr = p.response || {};
          let s = `${pr.totalFiles || 0} TBA files · ${pr.seriesWithTba || 0} series`;
          if (p.applied) s += ` · queued ${p.applied.queued || 0} renames`;
          return s;
        }
        default:          return '(unknown phase)';
      }
    },

  };
}
