// resolvarr UI — qBit webhook hook module
//
// Composed into the Alpine root via { ...appQbitWebhookHook() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appQbitWebhookHook() {
  return {
    // ---- qBit webhook hook (M-qBit-add Slice 5) -------------------
    //
    // Per-instance modal showing the curl ready to paste into qBit's
    // "Run external program on torrent added" field, plus auto-
    // configure / rotate / test / reset helpers backed by the Slice 4
    // endpoints. State indicator at the top distinguishes:
    //   - Not configured        — qBit autorun field is empty
    //   - Configured by us      — our path prefix is in the field
    //   - Third-party content   — non-empty + not ours (Configure
    //                             prompts for Append vs Replace)
    //   - qBit unreachable      — fetchError surfaced; manual paste
    //                             still viable

    openQbitWebhookModal(qi) {
      this.qbitWebhookInstance = qi;
      this.qbitWebhookData = null;
      this.qbitWebhookShowCurl = false;
      this.qbitWebhookTestResult = null;
      this.qbitWebhookConflictMode = 'append';
      this.qbitWebhookConflictOpen = false;
      this.qbitWebhookOverrideInput = '';
      this.qbitWebhookOpen = true;
      this.loadQbitWebhookConfig();
    },

    closeQbitWebhookModal() {
      if (this.qbitWebhookActionInFlight) return;
      this.qbitWebhookOpen = false;
      this.qbitWebhookInstance = null;
      this.qbitWebhookData = null;
      this.qbitWebhookShowCurl = false;
      this.qbitWebhookTestResult = null;
      this.qbitWebhookConflictOpen = false;
      this.qbitWebhookOverrideInput = '';
    },

    async loadQbitWebhookConfig() {
      if (!this.qbitWebhookInstance) return;
      this.qbitWebhookLoading = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitWebhookData = d;
        // Hydrate the editable override from the persisted value.
        // Empty = use the browser-detected default.
        this.qbitWebhookOverrideInput = d.webhookCallbackUrl || '';
      } catch (e) {
        this.showToast('Load webhook config failed: ' + e.message, 'error');
        this.qbitWebhookData = null;
      } finally {
        this.qbitWebhookLoading = false;
      }
    },

    // qbitWebhookOverrideInvalid — client-side mirror of the backend
    // validateQbitWebhookCallbackURL contract. Empty is valid (clears
    // override). Non-empty must parse, be http(s), have a host, and
    // carry no path/query/fragment.
    qbitWebhookOverrideInvalid() {
      const v = (this.qbitWebhookOverrideInput || '').trim();
      if (v === '') return false;
      try {
        const u = new URL(v);
        if (u.protocol !== 'http:' && u.protocol !== 'https:') return true;
        if (!u.host) return true;
        if (u.pathname !== '/' && u.pathname !== '') return true;
        if (u.search || u.hash) return true;
      } catch (_) {
        return true;
      }
      return false;
    },

    // qbitWebhookResolvedURL returns the URL qBit will actually call
    // back on given the current override input + the backend-supplied
    // default. Used by the curl-preview + the hint text.
    qbitWebhookResolvedURL() {
      const d = this.qbitWebhookData;
      if (!d) return '';
      const v = (this.qbitWebhookOverrideInput || '').trim().replace(/\/+$/, '');
      if (v === '' || this.qbitWebhookOverrideInvalid()) {
        return d.defaultWebhookUrl || d.webhookUrl || '';
      }
      const id = (this.qbitWebhookInstance || {}).id || '';
      return v + '/api/qbit/torrent-added/' + id;
    },

    // qbitWebhookCurlPreview rebuilds the curl client-side from the
    // current override + the secret loaded from the backend. Mirrors
    // buildQbitCurlCommand format so what the user sees is exactly
    // what gets written into qBit's autorun.
    qbitWebhookCurlPreview() {
      const d = this.qbitWebhookData;
      if (!d) return '';
      const url = this.qbitWebhookResolvedURL();
      const secret = d.secret || '';
      // Quoting matches Go's %q semantics (the backend's renderer).
      const q = (s) => '"' + String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
      return 'curl -fsS -X POST ' + q(url) + ' -H ' + q('X-API-Key: ' + secret) +
             ' --data-urlencode "infoHash=%I" --data-urlencode "name=%N" --data-urlencode "category=%L"';
    },

    // qbitWebhookStateLabel derives a human-friendly status string +
    // colour from the loaded config + qBit fetch state. Used in the
    // modal header.
    qbitWebhookStateLabel() {
      const d = this.qbitWebhookData;
      if (!d) return { text: 'Loading…', color: 'var(--text-muted)' };
      const st = d.qbitState || {};
      if (st.fetchError) {
        return { text: 'qBit unreachable — manual paste only', color: 'var(--accent-orange)' };
      }
      if (st.configuredByUs) {
        return { text: 'Configured (resolvarr is wired into qBit)', color: 'var(--accent-green)' };
      }
      if (st.thirdPartyContent) {
        return { text: 'qBit autorun has third-party content — Configure will prompt', color: 'var(--accent-orange)' };
      }
      return { text: 'Not configured (qBit autorun is empty)', color: 'var(--text-secondary)' };
    },

    // Configure click — if qBit has third-party content, opens the
    // conflict resolution sub-flow. Otherwise fires append directly
    // (semantics for empty + already-ours are the same regardless
    // of mode, so 'append' is safe).
    onQbitConfigureClick() {
      const st = (this.qbitWebhookData || {}).qbitState || {};
      if (st.thirdPartyContent && !st.fetchError) {
        this.qbitWebhookConflictMode = 'append';
        this.qbitWebhookConflictOpen = true;
        return;
      }
      this.doConfigureQbitWebhook('append');
    },

    async doConfigureQbitWebhook(mode) {
      if (!this.qbitWebhookInstance) return;
      if (this.qbitWebhookOverrideInvalid()) {
        this.showToast('Fix the Resolvarr URL — must be http:// or https:// with a host, no path', 'error');
        return;
      }
      this.qbitWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/configure', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            mode,
            // Always send hasOverride=true so a blank field genuinely
            // clears the persisted value (vs "field not sent").
            callbackUrlOverride: (this.qbitWebhookOverrideInput || '').trim(),
            hasOverride: true,
          }),
        });
        const d = await r.json();
        if (!r.ok) {
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.qbitWebhookConflictOpen = false;
        this.showToast('qBit autorun configured — hook should fire on next add', 'success');
        await this.loadQbitWebhookConfig();
      } catch (e) {
        // Surface the qBit-side error verbatim (often "qui blocked
        // this") so user knows manual paste is the workaround.
        this.showToast('Configure failed: ' + e.message + ' — try Show command for manual paste', 'error');
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    async doRotateQbitWebhookSecret() {
      if (!this.qbitWebhookInstance) return;
      if (!await this.confirmDialog({
        title:       'Rotate the webhook secret?',
        message:     'The old secret stops working immediately. If qBit was auto-configured, the new secret is pushed there too — but if that push fails (qui block / network), you will need to manually update qBit\'s autorun field with the new curl.',
        confirmText: 'Rotate',
        kind:        'warning',
      })) return;
      this.qbitWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        if (d.qbitOutOfSync) {
          this.showToast('Secret rotated locally, but qBit update failed: ' + (d.qbitUpdateError || 'unknown') + ' — re-run Configure to fix qBit', 'error');
        } else {
          this.showToast('Secret rotated', 'success');
        }
        await this.loadQbitWebhookConfig();
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    async doTestQbitWebhook() {
      if (!this.qbitWebhookInstance) return;
      this.qbitWebhookActionInFlight = true;
      this.qbitWebhookTestResult = null;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/test', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitWebhookTestResult = { ok: true, message: 'Receiver is wired correctly. (This does NOT verify qBit can reach resolvarr — only an actual qBit add proves end-to-end.)' };
      } catch (e) {
        this.qbitWebhookTestResult = { ok: false, message: 'Test failed: ' + e.message };
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    async doResetQbitWebhook() {
      if (!this.qbitWebhookInstance) return;
      const hadBackup = !!((this.qbitWebhookData || {}).qbitState && this.qbitWebhookInstance.previousAutorunBackup);
      const msg = hadBackup
        ? 'Resolvarr will restore the value qBit had before you clicked Configure, then forget the backup. The hook stops firing.'
        : 'Resolvarr will clear qBit\'s autorun field and disable it (no previous value to restore). The hook stops firing.';
      if (!await this.confirmDialog({
        title:       'Reset qBit autorun?',
        message:     msg,
        confirmText: 'Reset',
        kind:        'warning',
      })) return;
      this.qbitWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + this.qbitWebhookInstance.id + '/webhook/reset', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast(d.hadPreviousBackup ? 'qBit autorun restored to previous value' : 'qBit autorun cleared', 'success');
        await this.loadQbitWebhookConfig();
      } catch (e) {
        this.showToast('Reset failed: ' + e.message, 'error');
      } finally {
        this.qbitWebhookActionInFlight = false;
      }
    },

    // qbitWebhookConflictPreview computes the resulting qBit autorun
    // field for the conflict resolution sub-modal. Same join semantics
    // the backend uses ("; " separator on append).
    qbitWebhookConflictPreview() {
      const d = this.qbitWebhookData;
      if (!d || !d.qbitState) return '';
      const current = d.qbitState.currentProgram || '';
      // Use the live-computed preview so the conflict view reflects
      // the user's current Resolvarr URL override — not the stale
      // backend-rendered curl from page-load. Falls back to the
      // backend value if for any reason the preview returns empty.
      const ours = this.qbitWebhookCurlPreview() || d.curlCommand || '';
      if (this.qbitWebhookConflictMode === 'replace') return ours;
      // append (default)
      if (!current) return ours;
      return current + '; ' + ours;
    },

    async copyQbitWebhookSecret() {
      const d = this.qbitWebhookData;
      if (!d) return;
      const ok = await this.copyToClipboard(d.secret || '');
      this.showToast(ok ? 'Secret copied' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    async copyQbitWebhookCurl() {
      const d = this.qbitWebhookData;
      if (!d) return;
      // Use the live-computed preview so the copied curl reflects the
      // user's current override input — not the value persisted at
      // last load.
      const curl = this.qbitWebhookCurlPreview() || d.curlCommand || '';
      const ok = await this.copyToClipboard(curl);
      this.showToast(ok ? 'Curl command copied' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    async loadLogging() {
      try {
        const r = await this.apiFetch('/api/config/logging');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.loggingDebug = !!d.debug;
        this.loggingKeepDays = d.keepDays || 14;
        this.loggingPath = d.logPath || '';
        this.loggingError = '';
      } catch (e) {
        this.loggingError = 'Could not load logging settings: ' + e.message;
      }
    },

    async saveLogging() {
      const keepDays = parseInt(this.loggingKeepDays, 10);
      if (isNaN(keepDays) || keepDays < 1 || keepDays > 90) {
        this.loggingError = 'Keep-days must be between 1 and 90';
        return;
      }
      try {
        const r = await this.apiFetch('/api/config/logging', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ debug: !!this.loggingDebug, keepDays }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.loggingError = '';
        this.showToast('Logging settings saved', 'success');
      } catch (e) {
        this.loggingError = 'Save failed: ' + e.message;
      }
    },

    setScanSection(section) {
      // Migrate legacy section IDs that were folded into 'groups'
      // during the 2026-05-05 restructure (Tag library + Release
      // Groups + Sonarr Recover all live on one tab now). Old
      // localStorage values + any code path that still passes 'tag'
      // / 'recover' lands cleanly on the unified tab instead of a
      // dead sub-tab.
      if (section === 'tag' || section === 'recover') section = 'groups';
      this.scanSection = section;
      localStorage.setItem('resolvarr-scan-section', section);
      // Schedules feed the Run mode rules grid AND the History tab — keep
      // the poll alive whenever the user is on either of those sub-tabs.
      // Scan history only lives on the History tab, so its initial fetch
      // is gated separately.
      if (section === 'run' || section === 'history') {
        this.loadSchedules();
        this.startSchedulePoll();
      } else {
        this.stopSchedulePoll();
      }
      if (section === 'history') {
        this.loadScanHistory();
      }
      if (section === 'dvdetail') {
        // Refresh cache stats on tab landing — covers schedule fires +
        // QFA chains that populated entries while the user was on
        // another tab. loadConfig fires this once on app boot, but
        // background scans can happen any time after.
        this.loadDvCacheStats();
      }
      if (section === 'plex-sync') {
        // Plex server list drives the one-off run form's picker + the
        // pre-flight banner (shown when no Plex server is configured).
        this.loadPlexInstances();
      }
      this.pushNav();
    },
  };
}
