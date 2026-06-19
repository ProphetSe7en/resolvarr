// resolvarr UI — hash-routing (extracted from app.js, Stage 4 split).
// Composed via { ...appHashRouting() } in app(); methods bind `this` to the Alpine component.
function appHashRouting() {
  return {
    // ===== Hash routing (back/forward, bookmarks, copyable nav links) =====
    //
    // Hash format:
    //   #scan/<appType>/<scanSection>[/<groupsSection>] — Tag Library
    //   #tags/<tagsAppType>                              — Tag inventory
    //   #lists                                           — Lists (M5 placeholder)
    //   #webhooks                                        — Webhooks
    //   #settings/<section>                              — Settings sub-section
    //
    // setCurrentPage / setScanSection / setSettingsSection / setScanAppType
    // already write to localStorage on every change; pushNav() is just a
    // mirror to location.hash so browser back/forward + right-click "Open
    // in new tab" + bookmarks work. localStorage stays the source of truth
    // for "where was I" on a fresh tab open with no hash; the hash wins
    // when present (handled in init via restoreFromHash).

    buildNavHash() {
      const p = this.currentPage;
      if (p === 'tags')     return '#tags/' + (this.tagsAppType || 'radarr');
      if (p === 'lists')    return '#lists';
      if (p === 'webhooks') return '#webhooks/' + (this.webhookAppType || 'radarr');
      if (p === 'settings') return '#settings/' + (this.section || 'instances');
      // 'scan' (Tag Library) — appType + scan section + optional groups sub.
      const app = this.scanAppType || 'radarr';
      const sec = this.scanSection || 'run';
      let hash = '#scan/' + app + '/' + sec;
      if (sec === 'groups') hash += '/' + (this.groupsSection || 'active');
      return hash;
    },

    // navHref builds the hash a target page/section would produce, without
    // mutating any state. Used by nav anchors so right-click → "Open in new
    // tab" / "Copy link address" work, and the browser shows the URL on
    // hover. opts: { appType, scanSection, groupsSection, settingsSection,
    //                tagsAppType } — each defaults to current state.
    navHref(page, opts = {}) {
      if (page === 'tags')     return '#tags/' + (opts.tagsAppType || this.tagsAppType || 'radarr');
      if (page === 'lists')    return '#lists';
      if (page === 'webhooks') return '#webhooks/' + (opts.webhookAppType || this.webhookAppType || 'radarr');
      if (page === 'settings') return '#settings/' + (opts.settingsSection || this.section || 'instances');
      const app = opts.appType || this.scanAppType || 'radarr';
      const sec = opts.scanSection || this.scanSection || 'run';
      let hash = '#scan/' + app + '/' + sec;
      if (sec === 'groups') hash += '/' + (opts.groupsSection || this.groupsSection || 'active');
      return hash;
    },

    pushNav() {
      if (this._navSkipPush) return;
      const hash = this.buildNavHash();
      // Initial render: location.hash is "" — pushState gives us the
      // canonical hash. Subsequent pushes only fire when the hash
      // actually changes so we don't pile up identical history entries.
      if (location.hash !== hash) {
        history.pushState(null, '', hash);
      }
    },

    restoreFromHash(hash) {
      if (!hash || hash === '#') return false;
      // Loop guard: if the parsed hash already matches current state,
      // skip — pushNav write triggered hashchange triggered restore;
      // bailing here breaks the cycle.
      if (hash === this.buildNavHash()) return true;
      const parts = hash.replace(/^#/, '').split('/').filter(Boolean);
      if (parts.length === 0) return false;
      const page = parts[0];
      const validPages = ['scan', 'tags', 'lists', 'webhooks', 'settings'];
      if (!validPages.includes(page)) return false;
      const validScanSections = ['run', 'groups', 'recover', 'audio', 'video', 'dvdetail', 'history'];
      const validGroupsSections = ['active', 'discovered'];
      const validSettings = ['instances', 'qbit', 'notifications', 'security', 'display', 'logging', 'about'];
      this._navSkipPush = true;
      try {
        if (page === 'scan') {
          this.currentPage = 'scan';
          if (parts[1] === 'radarr' || parts[1] === 'sonarr') this.scanAppType = parts[1];
          if (parts[2] && validScanSections.includes(parts[2])) this.scanSection = parts[2];
          if (this.scanSection === 'groups' && parts[3] && validGroupsSections.includes(parts[3])) {
            this.groupsSection = parts[3];
          }
          // Mirror what setCurrentPage('scan') would have done so the
          // page lands in a coherent state on a fresh tab open with a
          // deep-link hash. Light side-effects only — no extra API
          // hits beyond what setCurrentPage already triggers.
          if (this.scanSection === 'run' || this.scanSection === 'history') {
            this.loadSchedules();
            this.startSchedulePoll();
          }
        } else if (page === 'tags') {
          this.currentPage = 'tags';
          if (parts[1] === 'radarr' || parts[1] === 'sonarr') this.tagsAppType = parts[1];
        } else if (page === 'lists') {
          this.currentPage = 'lists';
        } else if (page === 'webhooks') {
          this.currentPage = 'webhooks';
          if (parts[1] === 'radarr' || parts[1] === 'sonarr') this.webhookAppType = parts[1];
          // Side-effects (loadWebhookSetupPage / loadWebhookRules)
          // deferred to _dispatchPageMountEffects after loadConfig
          // resolves — restoreFromHash fires during init BEFORE
          // this.instances is populated, so iterating it here would
          // wipe webhookConfigs. The post-loadConfig dispatcher
          // re-fires the same side-effects with full data context.
        } else if (page === 'settings') {
          this.currentPage = 'settings';
          if (parts[1] && validSettings.includes(parts[1])) this.section = parts[1];
        }
        // Mirror localStorage so a refresh without the hash also lands
        // back here — both surfaces stay aligned.
        localStorage.setItem('resolvarr-page', this.currentPage);
        if (this.section) localStorage.setItem('resolvarr-settings-section', this.section);
        if (this.scanSection) localStorage.setItem('resolvarr-scan-section', this.scanSection);
        if (this.scanAppType) localStorage.setItem('resolvarr-scan-app-type', this.scanAppType);
        if (this.tagsAppType) localStorage.setItem('resolvarr-tags-app-type', this.tagsAppType);
        if (this.webhookAppType) localStorage.setItem('resolvarr-webhook-app-type', this.webhookAppType);
        if (this.groupsSection) localStorage.setItem('resolvarr-groups-section', this.groupsSection);
        return true;
      } finally {
        this._navSkipPush = false;
      }
    },

  };
}
