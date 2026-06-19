// resolvarr UI — recent-activity (extracted from app.js, Stage 4 split).
// Composed via { ...appRecentActivity() } in app(); methods bind `this` to the Alpine component.
function appRecentActivity() {
  return {
    // ===== Recent Activity table renderers (Phase A3) =====
    //
    // Per-event-type helpers pull the relevant fields from raw payload
    // and produce display strings for the table. Backend's eventTitle/
    // eventSubtitle are coarse — these renderers go deeper and produce
    // a quality summary + outcome chips. Pure functions on the event
    // shape; safe to call in x-text bindings (no side effects).
    eventIcon(eventType) {
      switch (eventType) {
        case 'Grab':                return '🎯';
        case 'Download':            return '📥';
        case 'MovieFileDelete':
        case 'EpisodeFileDelete':   return '🗑';
        case 'Rename':              return '✏️';
        case 'qbit:torrentAdded':   return '📦';
        case 'HealthIssue':
        case 'HealthRestored':      return '🏥';
        case 'ApplicationUpdate':   return '⬆️';
        case 'ManualInteractionRequired': return '⚠️';
        case 'Test':                return '✨';
        default:                    return 'ⓘ';
      }
    },

    // eventQualitySummary builds the right-side compact summary like
    // "1080p WEB-DL · EAC3 5.1 · h264 · FLUX · 3.04 GiB".
    // Resilient against missing fields (older payloads, partial data).
    eventQualitySummary(ev) {
      const body = ev.raw || {};
      // For Download events, episodeFiles/movieFile has the canonical
      // info. For Grab events, release.releaseTitle is the source of
      // truth (mediainfo not yet known). For Delete, the old file's
      // quality is in episodeFile/movieFile.
      let parts = [];
      let file = null;
      if (Array.isArray(body.episodeFiles) && body.episodeFiles.length) {
        file = body.episodeFiles[0];
      } else if (body.episodeFile) {
        file = body.episodeFile;
      } else if (body.movieFile) {
        file = body.movieFile;
      }
      if (file) {
        if (file.quality) parts.push(file.quality);
        const mi = file.mediaInfo || {};
        if (mi.audioCodec) {
          const ch = mi.audioChannels ? ' ' + mi.audioChannels : '';
          parts.push(mi.audioCodec + ch);
        }
        if (mi.videoCodec) parts.push(mi.videoCodec);
        if (mi.videoDynamicRangeType) parts.push(mi.videoDynamicRangeType);
        if (file.releaseGroup) parts.push(file.releaseGroup);
        if (file.size) parts.push(this.formatBytes(file.size));
      } else if (body.release && body.release.releaseTitle) {
        // Grab event — no file yet. Just show the release title.
        return body.release.releaseTitle;
      }
      return parts.join(' · ');
    },

    // eventIndexerLine returns the indexer + downloader source-trail
    // string ("BHD → qBit-tv") shown on the expanded card. Empty when
    // both fields are missing.
    eventIndexerLine(ev) {
      const body = ev.raw || {};
      const indexer = (body.release && body.release.indexer) || '';
      const dlClient = body.downloadClient || '';
      if (indexer && dlClient) return indexer + ' → ' + dlClient;
      return indexer || dlClient || '';
    },

    // eventSceneName returns episodeFile.sceneName or movieFile.sceneName
    // for the expand-card display. Useful when comparing grab-name vs
    // import-name drift.
    eventSceneName(ev) {
      const body = ev.raw || {};
      if (Array.isArray(body.episodeFiles) && body.episodeFiles.length) {
        return body.episodeFiles[0].sceneName || '';
      }
      if (body.episodeFile) return body.episodeFile.sceneName || '';
      if (body.movieFile) return body.movieFile.sceneName || '';
      return '';
    },

    // eventFilePath returns the imported file's full path. Empty for
    // events that don't have a file (Grab, Test, Health).
    eventFilePath(ev) {
      const body = ev.raw || {};
      if (Array.isArray(body.episodeFiles) && body.episodeFiles.length) {
        return body.episodeFiles[0].path || '';
      }
      if (body.episodeFile) return body.episodeFile.path || '';
      if (body.movieFile) return body.movieFile.path || '';
      return '';
    },

    // (formatBytes lives in app.js — the KB/MB/GB variant that already won
    // the object-literal override before this split; the dead IEC duplicate
    // that used to sit here was removed.)

    // eventOutcomeChips returns the per-rule outcome chips to render in
    // the Outcome column. Empty array → "no rule matched" placeholder
    // in the template.
    eventOutcomeChips(ev) {
      const outcomes = ev.outcomes || [];
      return outcomes.map(o => ({
        ruleId:   o.ruleId,
        ruleName: o.ruleName || '(unnamed rule)',
        status:   o.status || 'ok',
        changed:  !!o.changed,
        summary:  o.summary || '',
        symbol:   o.status === 'error'   ? '✗'
                : o.status === 'partial' ? '~'
                : o.changed              ? '✓'
                : '·',
        color:    o.status === 'error'   ? 'var(--accent-red)'
                : o.status === 'partial' ? 'var(--accent-orange)'
                : o.changed              ? 'var(--accent-green)'
                : 'var(--text-muted-secondary)',
      }));
    },

    // eventOutcomeFilterMatch returns true when the event's outcomes
    // satisfy the current outcome filter. Drives webhookEventsFiltered
    // when the outcome dropdown isn't "all".
    eventOutcomeFilterMatch(ev) {
      const outcomes = ev.outcomes || [];
      const f = this.webhookOutcomeFilter;
      if (!f || f === 'all') return true;
      if (f === 'no-rule')  return outcomes.length === 0;
      if (f === 'changed')  return outcomes.some(o => o.changed && o.status !== 'error');
      if (f === 'no-change') return outcomes.length > 0 && outcomes.every(o => !o.changed && o.status !== 'error');
      if (f === 'errors')   return outcomes.some(o => o.status === 'error' || o.status === 'partial');
      return true;
    },

    // eventContentShape classifies an event by what kind of content it
    // describes. Sonarr-quirk-aware:
    //
    //   - Grab events report ALL episodes the release covers in
    //     episodes[]. A season-pack grab has episodes.length=24 + no
    //     episodeFiles. → 'season-pack'.
    //
    //   - Download (Import) events fire ONCE PER EPISODE FILE. But for
    //     files imported AS PART OF a season pack, Sonarr lists ALL of
    //     the pack's episodes in episodes[] (not just the one in this
    //     file). The signal that distinguishes per-episode-from-pack
    //     from true multi-episode is episodeFiles[] — exactly one entry
    //     means the event is about that single file. So
    //     "episodes.length=24 + episodeFiles.length=1" = single episode
    //     import that happens to be from a pack, NOT a season-pack
    //     event in the user's sense.
    //
    //   - True multi-episode events are exceedingly rare. They'd be
    //     either a Grab (no files yet) or a special "multi-ep file"
    //     (e.g. an S01E01E02 mux) — both result in episodeFiles being
    //     empty OR a single file holding multiple episode IDs.
    //
    // Detection order:
    //   movie field         → 'movie'
    //   no episodes context → 'system'
    //   has 1+ episodeFiles → 'episode'   (single file, regardless of
    //                                       episodes[] count)
    //   episodes.length > 1 → 'season-pack' (Grab without files)
    //   else                → 'episode'
    eventContentShape(ev) {
      const body = ev.raw || {};
      if (body.movie) return 'movie';
      const epCount = Array.isArray(body.episodes) ? body.episodes.length : 0;
      if (epCount === 0 && !body.series) return 'system';
      const fileCount = Array.isArray(body.episodeFiles) ? body.episodeFiles.length : 0;
      if (fileCount >= 1) return 'episode';
      if (epCount > 1) return 'season-pack';
      return 'episode';
    },

    // eventContentShapeFilterMatch returns true when the event's shape
    // is in the active filter set, or when no shape filter is active.
    eventContentShapeFilterMatch(ev) {
      const f = this.webhookContentShapeFilter || [];
      if (f.length === 0) return true;
      return f.includes(this.eventContentShape(ev));
    },

    // Toggle a content-shape in the filter set. Click cycles a shape
    // in/out independently — multi-select semantics.
    toggleWebhookContentShape(shape) {
      const cur = this.webhookContentShapeFilter || [];
      const i = cur.indexOf(shape);
      if (i >= 0) {
        this.webhookContentShapeFilter = cur.filter(s => s !== shape);
      } else {
        this.webhookContentShapeFilter = [...cur, shape];
      }
    },

    // Returns the content-shape chips that make sense for the active
    // webhookAppType. Radarr can't produce episode / season-pack
    // events; Sonarr can't produce movie events. System (Test/Health
    // /Manual/qBit-add) applies regardless.
    activeWebhookContentShapes() {
      const all = [
        {id: 'episode',     label: '🎬 Episode',      help: 'Single-episode events (1 entry in episodes[])',     appType: 'sonarr'},
        {id: 'season-pack', label: '📚 Season pack',  help: 'Multi-episode grab — covers a full season',          appType: 'sonarr'},
        {id: 'movie',       label: '🎞 Movie',        help: 'Radarr events (movie field present)',                appType: 'radarr'},
        {id: 'system',      label: 'ⓘ System',         help: 'Test / Health / Manual / qBit-add / other',          appType: 'any'},
      ];
      const t = this.webhookAppType || 'radarr';
      return all.filter(s => s.appType === 'any' || s.appType === t);
    },

    // Auto-select the activity-instance picker when exactly one
    // instance exists for the active app-type. Saves a click when
    // the user has just one Sonarr OR one Radarr — common single-
    // host setups. Triggered on app-type pill change AND on initial
    // load. Doesn't clobber an existing selection.
    autoPickWebhookActivityInstance() {
      const candidates = this.webhookInstancesForAppType();
      if (candidates.length === 1) {
        // Only force when current selection isn't already among the
        // candidates — if user switched app-type pill and the
        // previous instance was a different type, replace it.
        const stillValid = candidates.some(i => i.id === this.webhookActivityInstanceId);
        if (!stillValid) {
          this.webhookActivityInstanceId = candidates[0].id;
          this.loadWebhookEvents(this.webhookActivityInstanceId);
        }
      } else if (candidates.length === 0) {
        this.webhookActivityInstanceId = '';
      }
    },

  };
}
