// resolvarr UI — tba-refresh (extracted from app.js, Stage 4 split).
// Composed via { ...appTbaRefresh() } in app(); methods bind `this` to the Alpine component.
function appTbaRefresh() {
  return {
    // ---- TBA refresh (Sonarr-only) -------------------------------
    async runTbaRefreshScan() {
      if (!this.scanInstanceId) {
        this.showToast('Pick a Sonarr instance first', 'error');
        return;
      }
      this.tbaRefreshLoading = true;
      this.tbaRefreshError = '';
      try {
        const res = await this.apiFetch('/api/scan/tba-refresh/preview', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            includeContinuing: !!this.tbaRefreshConfig.includeContinuing,
            includeEnded: !!this.tbaRefreshConfig.includeEnded,
            includeSpecials: !!this.tbaRefreshConfig.includeSpecials,
          }),
        });
        if (!res.ok) throw new Error(await res.text());
        this.tbaRefreshPreview = await res.json();
        // Pre-select every found file — same default as Missing Episodes.
        const sel = {};
        for (const ser of (this.tbaRefreshPreview.series || [])) {
          for (const f of (ser.files || [])) sel[f.episodeFileId] = true;
        }
        this.tbaRefreshSelected = sel;
      } catch (e) {
        this.tbaRefreshError = (e && e.message) || String(e);
        this.tbaRefreshPreview = null;
      } finally {
        this.tbaRefreshLoading = false;
      }
    },
    tbaRefreshSelectAll() {
      const sel = {};
      for (const ser of ((this.tbaRefreshPreview && this.tbaRefreshPreview.series) || [])) {
        for (const f of (ser.files || [])) sel[f.episodeFileId] = true;
      }
      this.tbaRefreshSelected = sel;
    },
    tbaRefreshSelectNone() { this.tbaRefreshSelected = {}; },
    tbaRefreshSelectedCount() {
      return Object.values(this.tbaRefreshSelected || {}).filter(Boolean).length;
    },
    // Group a series' flat file list into [{season, files}] for the
    // series → season → file rendering (and the same shape the Discord
    // notification groups by). Files arrive season-sorted from the API.
    tbaSeasonGroups(series) {
      const groups = [];
      let cur = null;
      for (const f of ((series && series.files) || [])) {
        if (!cur || cur.season !== f.seasonNumber) {
          cur = { season: f.seasonNumber, files: [] };
          groups.push(cur);
        }
        cur.files.push(f);
      }
      return groups;
    },
    // SxxExx label; collapses multi-episode files to S03E07E08.
    tbaEpLabel(file) {
      const s = 'S' + String(file.seasonNumber).padStart(2, '0');
      const eps = (file.episodeNumbers || []);
      if (eps.length === 0) return s;
      return s + eps.map(e => 'E' + String(e).padStart(2, '0')).join('');
    },
    async applyTbaRefresh() {
      const groups = [];
      for (const ser of ((this.tbaRefreshPreview && this.tbaRefreshPreview.series) || [])) {
        const fileIds = (ser.files || [])
          .filter(f => this.tbaRefreshSelected[f.episodeFileId])
          .map(f => f.episodeFileId);
        if (fileIds.length > 0) groups.push({ seriesId: ser.seriesId, fileIds });
      }
      if (groups.length === 0) {
        this.showToast('Select at least one file to rename', 'error');
        return;
      }
      const total = groups.reduce((n, g) => n + g.fileIds.length, 0);
      if (!await this.confirmDialog({
        title:       'Rename ' + total + ' file' + (total === 1 ? '' : 's') + '?',
        message:     'Trigger Sonarr to rename ' + total + ' file' + (total === 1 ? '' : 's') + ' across ' + groups.length + ' series. Sonarr renames per its configured naming pattern; this is queued and runs in the background.',
        confirmText: 'Rename',
      })) return;
      this.tbaRefreshApplying = true;
      try {
        const res = await this.apiFetch('/api/scan/tba-refresh/apply', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ instanceId: this.scanInstanceId, groups }),
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        const failed = (data.errors || []).length;
        let msg = 'Queued ' + (data.queued || 0) + ' rename' + ((data.queued === 1) ? '' : 's') + ' across ' + (data.seriesCount || 0) + ' series';
        if (failed > 0) msg += ' — ' + failed + ' series failed';
        this.showToast(msg, failed > 0 ? 'error' : 'success');
        // Clear — Sonarr renames async; re-run Preview to confirm.
        this.tbaRefreshPreview = null;
        this.tbaRefreshSelected = {};
      } catch (e) {
        this.showToast('Rename failed: ' + ((e && e.message) || e), 'error');
      } finally {
        this.tbaRefreshApplying = false;
      }
    },

    // formatDate renders an ISO8601 timestamp in the CONTAINER'S host
    // context. Three controls feed in:
    //   - serverTimezone (from $TZ on init) — the moment is shown in
    //     the container's local time, not the browser's
    //   - serverLocale (from $LANG, or derived from $TZ when LANG is
    //     unset) — drives date order (DD/MM, MM/DD, YYYY-MM-DD)
    //   - timeFormat (user-set in Settings → Display) — auto lets the
    //     locale pick 12h vs 24h; "24h" or "12h" forces it
    // So an Oslo-TZ admin sees "28.04.2026, 17:30:00" by default; an
    // en-US-TZ admin sees "4/28/2026, 5:30:00 PM"; either can flip the
    // setting if they want the other format. Falls back to raw string
    // if Date parsing fails.
    formatDate(iso) {
      if (!iso) return '';
      try {
        const d = new Date(iso);
        if (isNaN(d.getTime())) return iso;
        return d.toLocaleString(this.serverLocale || 'en-GB', this.dateFormatOptions());
      } catch (e) {
        return iso;
      }
    },

    // connErrorShort turns a raw connection/test error (Go's transport
    // errors are verbose: `Get "..." : dial tcp: lookup host on
    // 127.0.0.1:53: no such host`) into a short, plain message for the
    // instance status rows (Instances / qBittorrent / Plex). The full
    // message stays in the row's title tooltip. Secrets are already
    // scrubbed server-side; this only improves readability + keeps the
    // row from overflowing. Falls back to a trimmed raw for unknown cases.
    connErrorShort(msg) {
      if (!msg) return '';
      const m = String(msg).toLowerCase();
      if (m.includes('no such host') || m.includes('lookup'))         return 'Host not found (check the address)';
      if (m.includes('connection refused'))                           return 'Connection refused (running on that port?)';
      if (m.includes('no route to host') || m.includes('network is unreachable')) return 'Host unreachable (check the address/network)';
      if (m.includes('timeout') || m.includes('deadline exceeded') || m.includes('i/o timeout')) return 'Timed out (host unreachable?)';
      if (m.includes('x509') || m.includes('certificate'))            return 'Certificate error (enable Trusted certs for self-signed?)';
      if (m.includes('401') || m.includes('unauthorized'))            return 'Authentication failed (check credentials / token)';
      if (m.includes('403') || m.includes('forbidden'))               return 'Access forbidden (check credentials)';
      const t = String(msg).trim();
      return t.length > 90 ? t.slice(0, 90) + '…' : t;
    },
  };
}
