// app.js — resolvarr Alpine.js app. Single Alpine `app()` factory returns a
// state-bag + methods object that x-data binds to. Past the structure-
// baseline §8.3 1500-line warn threshold; split into feature modules
// (js/scan.js, js/tag-inventory.js, etc.) when it crosses 3000 or grows
// a clear feature seam.
//
// Browser global helpers used inside this file (fetch, document, etc.)
// don't need imports — single-file, no module bundler.

function app() {
  return {
    // Stage 4 module split: extracted method groups are merged in here.
    // Spreads first so the rest of this literal (state + methods) can
    // freely override on any name collision. Each module's methods bind
    // `this` to the Alpine component at merge time. js/modules/*.js are
    // loaded before app.js in index.html. See frontend-restructure-plan.md.
    ...appRunModule(),
    ...appState(),
    ...appConfigSnapshots(),
    ...appRuleEditorPlex(),
    ...appProfileSwitcher(),
    ...appAudioVideoTags(),
    ...appPlexLabelSync(),
    ...appTagSearch(),
    ...appDvScan(),
    ...appQbitWebhookHook(),
    ...appScanHistory(),
    ...appPlexWizardSteps(),
    ...appQbitCategoryFix(),
    ...appGrabRenameEditor(),
    ...appSonarrGrouping(),
    ...appDvDetailDrillin(),
    ...appModeAvailability(),
    ...appFunctionInfo(),
    ...appModeVisibility(),
    ...appGrabRenameHistory(),
    ...appPerRuleWebhookUrl(),
    ...appQbitSeBacklog(),
    ...appReconcile(),
    ...appTbaRefresh(),
    ...appMissingEpisodes(),
    ...appRecoverRunMode(),
    ...appCleanupGroups(),

    // apiFetch wraps window.fetch to handle two cross-cutting concerns:
    //
    // (1) CSRF — attach X-CSRF-Token header to every same-origin API call.
    //     Required on POST / PUT / DELETE; safely a no-op on GET (backend
    //     middleware skips GETs anyway).
    //
    // (2) 401 → /login redirect — if the backend returns 401 (session
    //     expired, logged out elsewhere), bounce the user to /login so
    //     they can re-auth. Centralised here so every caller doesn't
    //     need `if (r.status === 401) ...`. A never-resolving promise is
    //     returned on redirect so callers don't try to .json() a body
    //     that won't arrive before navigation completes.
    //
    //     Skip the redirect when:
    //       - Already on /login or /setup (avoid loop — those pages probe
    //         /api/auth/status as a normal public endpoint).
    //       - Caller opts out via X-Skip-Login-Redirect header. The only
    //         legitimate use is the disable-auth modal, where 401 means
    //         "confirm_password incorrect" not "session expired".
    //
    // The backend returns text/plain error bodies for CSRF / auth failures
    // (not JSON). apiFetch returns the raw Response exactly like fetch —
    // callers decide parsing — but callers that blindly r.json() on a 4xx
    // will throw. Guard with resp.ok before parsing.
    async apiFetch(url, opts) {
      opts = opts || {};
      const headers = new Headers(opts.headers || {});
      const m = document.cookie.match(/(?:^|; )resolvarr_csrf=([^;]+)/);
      if (m) headers.set('X-CSRF-Token', decodeURIComponent(m[1]));
      const skipLoginRedirect = headers.get('X-Skip-Login-Redirect') === '1';
      // Client-side hint only — strip before sending to server.
      headers.delete('X-Skip-Login-Redirect');
      opts.headers = headers;
      const resp = await fetch(url, opts);
      if (resp.status === 401 && !skipLoginRedirect) {
        const path = window.location.pathname;
        if (path !== '/login' && path !== '/setup') {
          window.location.href = '/login';
          return new Promise(() => {});
        }
      }
      return resp;
    },

    async init() {
      // Apply theme on init and listen for system-pref changes when theme=system.
      // The FOUC-prevention <script> in index.html already set data-theme before
      // first paint; this re-applies it once Alpine state exists so setTheme()
      // works reactively from the Settings UI.
      this.applyTheme();
      try {
        matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
          if (this.theme === 'system') this.applyTheme();
        });
      } catch (e) { /* older browsers without addEventListener on MediaQueryList */ }
      // Cross-tab sync: when another tab changes the theme, mirror it here.
      window.addEventListener('storage', (e) => {
        if (e.key === 'resolvarr-theme' && e.newValue && e.newValue !== this.theme) {
          this.theme = e.newValue;
          this.applyTheme();
        }
      });

      // Restore last-visited page and settings subsection from localStorage so
      // refreshes land where the user left off. Validated against known values
      // to guard against stale keys from older versions.
      const savedPage = localStorage.getItem('resolvarr-page');
      if (['scan','tags','settings'].includes(savedPage)) this.currentPage = savedPage;
      // Migrate stale 'groups' → 'scan' with groups sub-tab active.
      if (savedPage === 'groups') {
        this.currentPage = 'scan';
        this.scanSection = 'groups';
        localStorage.setItem('resolvarr-page', 'scan');
        localStorage.setItem('resolvarr-scan-section', 'groups');
      }
      const savedSection = localStorage.getItem('resolvarr-settings-section');
      if (['instances','notifications','display','about'].includes(savedSection)) this.section = savedSection;
      const savedScanSection = localStorage.getItem('resolvarr-scan-section');
      // M4 split: legacy 'extra' (the old Extra tags fane) folds into
      // 'video' as the sensible default. 'dvdetail' has been a stable
      // section since M4b — accept directly. Audio lives on its own
      // 'audio' fane.
      if (savedScanSection === 'extra') {
        this.scanSection = 'video';
        localStorage.setItem('resolvarr-scan-section', 'video');
      } else if (savedScanSection === 'activity') {
        // Step-5 follow-up rename: 'Activity' tab → 'History'. Migrate
        // existing testers' persisted value so their next page-load
        // lands them on the renamed (same) tab.
        this.scanSection = 'history';
        localStorage.setItem('resolvarr-scan-section', 'history');
      } else if (savedScanSection === 'tag' || savedScanSection === 'recover' || savedScanSection === 'filters') {
        // 2026-05-05 restructure folded standalone Tag library / Recover /
        // Filters sub-tabs into one 'groups' (Tag quality releases) tab.
        // Testers persisting any of those stale ids would land on a
        // hidden section with no nav back. Migrate forward.
        this.scanSection = 'groups';
        localStorage.setItem('resolvarr-scan-section', 'groups');
      } else if (['run','groups','audio','video','dvdetail','history'].includes(savedScanSection)) {
        this.scanSection = savedScanSection;
      }
      const savedGroupsSection = localStorage.getItem('resolvarr-groups-section');
      // 'filters' was briefly persisted as a groups-sidebar item before
      // the 2026-05-05 fold. Same forward-migration target as above —
      // route legacy testers to 'groups' so the page renders.
      if (savedGroupsSection === 'filters') {
        this.scanSection = 'groups';
        this.groupsSection = 'active';
        localStorage.setItem('resolvarr-scan-section', 'groups');
        localStorage.setItem('resolvarr-groups-section', 'active');
      } else if (['active','discovered'].includes(savedGroupsSection)) {
        this.groupsSection = savedGroupsSection;
      }
      const savedTagsInstance = localStorage.getItem('resolvarr-tags-instance');
      if (savedTagsInstance) this.tagsInstanceId = savedTagsInstance;
      const savedTagsAppType = localStorage.getItem('resolvarr-tags-app-type');
      if (savedTagsAppType === 'radarr' || savedTagsAppType === 'sonarr') {
        this.tagsAppType = savedTagsAppType;
      }
      const savedScanAppType = localStorage.getItem('resolvarr-scan-app-type');
      if (savedScanAppType === 'radarr' || savedScanAppType === 'sonarr') {
        this.scanAppType = savedScanAppType;
      }
      const savedWebhookAppType = localStorage.getItem('resolvarr-webhook-app-type');
      if (savedWebhookAppType === 'radarr' || savedWebhookAppType === 'sonarr') {
        this.webhookAppType = savedWebhookAppType;
      }

      // Hash-routing: restore from URL hash (deep-link / refresh) AFTER
      // localStorage so the hash wins when present. Empty hash falls
      // through to localStorage state. Listener wired below for browser
      // back/forward + sibling-tab navigation.
      if (location.hash) {
        this.restoreFromHash(location.hash);
      } else {
        // Seed the hash with current state so the URL matches what's
        // visible from the very first render. Without this the first
        // user click writes hash + adds a history entry; better to
        // anchor the initial entry to the current page.
        const initial = this.buildNavHash();
        if (initial && initial !== '#') history.replaceState(null, '', initial);
      }
      window.addEventListener('hashchange', () => {
        this.restoreFromHash(location.hash);
      });

      await this.loadConfig();
      this.applyUIScale();
      try {
        const r = await this.apiFetch('/api/version');
        const d = await r.json();
        this.version = d.version || 'dev';
        this.isDevBuild = (d.version || '').includes('-dev');
        if (d.timezone) this.serverTimezone = d.timezone;
        if (d.locale)   this.serverLocale = d.locale;
      } catch {}
      // If the saved tags-instance no longer exists, drop it.
      if (this.tagsInstanceId && !this.instances.find(i => i.id === this.tagsInstanceId)) {
        this.tagsInstanceId = '';
        localStorage.removeItem('resolvarr-tags-instance');
      }
      // If the saved tags-instance is of a different type than the saved
      // app-type, the app-type wins (user explicitly clicked Radarr/Sonarr
      // last) and the instance gets re-picked from the matching pool.
      const savedInst = this.instances.find(i => i.id === this.tagsInstanceId);
      if (savedInst && savedInst.type !== this.tagsAppType) {
        this.tagsInstanceId = '';
      }
      // Auto-pick the first instance of the active app-type if none chosen.
      if (!this.tagsInstanceId) {
        const first = this.instances.find(i => i.type === this.tagsAppType);
        if (first) {
          this.tagsInstanceId = first.id;
          localStorage.setItem('resolvarr-tags-instance', first.id);
        }
      }
      // Reconcile scanAppType against the live instances list. If the
      // saved app-type has no matching instance (e.g. user deleted the
      // last Radarr after a scanAppType=radarr session), flip to the
      // other type when one is available so the page lands somewhere
      // usable. Same logic protects sonarr-only deployments — initScan
      // below seeds scanInstanceId from scanAvailableInstances() which
      // is filtered by scanAppType.
      if (!this.scanAppTypeAvailable(this.scanAppType)) {
        const other = this.scanAppType === 'radarr' ? 'sonarr' : 'radarr';
        if (this.scanAppTypeAvailable(other)) {
          this.scanAppType = other;
          localStorage.setItem('resolvarr-scan-app-type', other);
        }
      }
      // If we landed on the Tags page, load its data now.
      if (this.currentPage === 'tags' && this.tagsInstanceId) this.loadTags();
      // If we landed on Scan tab, run initial setup. Groups data loads only when the Groups sub-tab is active.
      if (this.currentPage === 'scan') {
        this.initScan();
        if (this.scanSection === 'groups') this.loadGroups();
        // Direct-hash landing on the Plex-sync sub-tab — load the Plex
        // server list so the one-off run form's picker + pre-flight
        // banner render with current data.
        if (this.scanSection === 'plex-sync') {
          this.loadPlexInstances();
        }
      }
      // Load notification agents when landing on Settings → Notifications.
      if (this.currentPage === 'settings' && this.section === 'notifications') {
        this.loadAgents();
      }
      // Same lazy-load for the Security panel.
      if (this.currentPage === 'settings' && this.section === 'security') {
        this.loadSecurityPanel();
      }
      // Same lazy-load for the Plex panel — direct-hash landings on
      // #settings/plex hit here.
      if (this.currentPage === 'settings' && this.section === 'plex') {
        this.loadPlexInstances();
      }
      // qBit instances feed the qBit S/E one-off sub-tab + the schedule
      // / QFA qBit S/E step dropdowns. Load on a direct-hash / refresh
      // landing on the Scan tab so those dropdowns aren't empty there.
      if (this.currentPage === 'scan') {
        this.loadQbitInstances();
      }
      // Webhooks page mount effects — fired here (post-loadConfig)
      // rather than from restoreFromHash since restoreFromHash runs
      // before instances are populated. Both refresh-on-#webhooks
      // and refresh-with-localStorage-page=webhooks land here with
      // full data context.
      if (this.currentPage === 'webhooks') {
        this.loadWebhookSetupPage();
        this.loadWebhookRules();
        // qBit instances feed the Grab Rename + qBit S/E tag step
        // dropdowns inside the rule editor. Without this load, opening
        // the Webhooks page directly (refresh / bookmark / hash) before
        // visiting Settings → qBit leaves both dropdowns empty and the
        // Next-button gate stuck on "Pick a qBit instance".
        this.loadQbitInstances();
        // Plex instances feed the Plex label sync step's dropdown.
        // Same reasoning as the qBit case above.
        this.loadPlexInstances();
      }
      // Kick off initial status check for all instances, then poll every 60s.
      // Same cadence applies to qBit + Plex so the row pills reflect live
      // state, not the last-clicked Test result.
      this.refreshAllStatus();
      this.refreshAllQbitStatus();
      this.refreshAllPlexStatus();
      this.pollHandle = setInterval(() => {
        this.refreshAllStatus();
        this.refreshAllQbitStatus();
        this.refreshAllPlexStatus();
      }, 60000);
    },

    setCurrentPage(page) {
      this.currentPage = page;
      localStorage.setItem('resolvarr-page', page);
      this.pushNav();
      // qBit instances feed the qBit S/E one-off sub-tab + the schedule
      // / QFA qBit S/E step dropdowns — load on Scan-tab entry so they
      // are present without first visiting Webhooks / Settings.
      if (page === 'scan') {
        this.loadQbitInstances();
      }
      // Schedules feed both the Run mode rules grid AND the History scan
      // filter chips — load + poll whenever the user is anywhere on the
      // Scan tab. Stop the poll when leaving the Scan tab entirely.
      if (page === 'scan' && (this.scanSection === 'run' || this.scanSection === 'history')) {
        this.loadSchedules();
        this.startSchedulePoll();
      } else {
        this.stopSchedulePoll();
      }
      // Lazy-load webhook configs + recent events the first time the
      // user lands on the Webhooks page. Subsequent visits re-fetch
      // so events captured while the user was elsewhere show up.
      // Leaving the page closes the SSE stream so we don't hold an
      // open EventSource for a tab the user isn't looking at.
      if (page === 'webhooks') {
        this.loadWebhookSetupPage();
        // Webhook rules are listed per-instance on the Setup card;
        // load them alongside the per-instance webhook configs so
        // the rule list renders on first paint.
        this.loadWebhookRules();
        // Mirror init() — qBit instances feed Grab Rename + qBit S/E
        // tag dropdowns. Idempotent (loadQbitInstances just refetches).
        this.loadQbitInstances();
        // Plex instances feed the Plex label sync step's dropdown.
        this.loadPlexInstances();
      } else {
        this.stopWebhookEventStream();
      }
    },

    setSettingsSection(section) {
      this.section = section;
      localStorage.setItem('resolvarr-settings-section', section);
      // Lazy-load notification agents the first time the user lands on
      // the section. Future opens still re-fetch so changes from
      // another tab / Direct API edit are picked up.
      if (section === 'notifications') {
        this.loadAgents();
      }
      if (section === 'security') {
        this.loadSecurityPanel();
      }
      if (section === 'logging') {
        this.loadLogging();
      }
      if (section === 'qbit') {
        this.loadQbitInstances();
      }
      if (section === 'plex') {
        this.loadPlexInstances();
      }
      this.pushNav();
    },

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

    // ===== qBittorrent instances =====
    //
    // Standalone CRUD list. Loaded on Settings → qBit visit, refreshed
    // after each create / update / delete + the on-boot loadConfig
    // (since /api/config also returns the list, masked-passwords).
    // The dedicated /api/qbit-instances endpoint is the source-of-truth
    // for the Settings page; loadConfig's mirror is a fallback for
    // first-render before the dedicated fetch returns.

    async loadQbitInstances() {
      try {
        const r = await this.apiFetch('/api/qbit-instances');
        if (r.ok) {
          const d = await r.json();
          this.qbitInstances = Array.isArray(d) ? d : [];
          // Kick a silent status sweep so freshly-loaded rows don't
          // sit at "Not tested" until the 60s poll catches up. Honors
          // the silent path so a manual click already in flight isn't
          // overwritten.
          this.refreshAllQbitStatus();
        }
      } catch (e) {
        // Silent — page renders empty state until next refresh.
      }
    },

    openQbitInstanceModal(qi) {
      // qi is the existing instance (edit) or undefined (create).
      // Password placeholder for edit shows '••••••••' so the user
      // knows leaving it blank preserves the stored value.
      this.qbitInstanceModal = {
        open: true,
        id: qi ? qi.id : '',
        name: qi ? qi.name : '',
        url: qi ? qi.url : '',
        username: qi ? (qi.username || '') : '',
        password: '', // never pre-populate — masked on edit, blank on create
        trustedCerts: !!(qi && qi.trustedCerts),
        busy: false,
        testing: false,
        testResult: '',
        testOk: false,
      };
    },

    closeQbitInstanceModal() {
      if (this.qbitInstanceModal.busy) return;
      this.qbitInstanceModal.open = false;
    },

    // testQbitInstanceModal hits the inline-creds endpoint that
    // probes against whatever's currently in the modal — lets the
    // user verify creds BEFORE saving. Distinct from
    // testQbitInstance() which probes against the SAVED creds for
    // a row in the list.
    async testQbitInstanceModal() {
      const m = this.qbitInstanceModal;
      if (!m.url) return;
      m.testing = true;
      m.testResult = '';
      try {
        const r = await this.apiFetch('/api/qbit-instances/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: m.id, // empty on create — backend ignores; populated on edit so the masked-password path can pull stored creds
            url: m.url,
            username: m.username,
            password: m.password,
            trustedCerts: m.trustedCerts,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        m.testOk = !!d.ok;
        m.testResult = d.ok ? (d.message || 'Connected') : ('Failed: ' + (d.error || 'unknown'));
        // Bridge into the row-status map when editing a saved instance,
        // so closing the modal doesn't leave the row pill stuck at "Not
        // tested" right after the user verified it works.
        if (m.id) {
          this.qbitStatus[m.id] = d.ok ? 'connected' : 'failed';
          this.qbitError[m.id] = d.ok ? '' : (d.error || '');
        }
      } catch (e) {
        m.testOk = false;
        m.testResult = 'Failed: ' + e.message;
        if (m.id) {
          this.qbitStatus[m.id] = 'failed';
          this.qbitError[m.id] = e.message;
        }
      } finally {
        m.testing = false;
      }
    },

    async saveQbitInstanceModal() {
      const m = this.qbitInstanceModal;
      if (!m.name || !m.url) return;
      m.busy = true;
      try {
        const body = {
          name: m.name,
          url: m.url,
          username: m.username,
          password: m.password,
          trustedCerts: m.trustedCerts,
        };
        const path = m.id ? '/api/qbit-instances/' + m.id : '/api/qbit-instances';
        const method = m.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadQbitInstances();
        this.qbitInstanceModal.open = false;
        this.showToast('qBit instance saved', 'success');
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    // testQbitInstance — same shape as testInstance (Arr): probes the
    // saved-creds endpoint, writes flat status/error into qbitStatus +
    // qbitError. silent=true skips the "testing" pill flash so the
    // 60s background refresh doesn't strobe every row.
    async testQbitInstance(id, silent = false) {
      if (!silent) this.qbitStatus[id] = 'testing';
      this.qbitError[id] = '';
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + id + '/test', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitStatus[id] = d.ok ? 'connected' : 'failed';
        this.qbitError[id] = d.ok ? '' : (d.error || '');
      } catch (e) {
        this.qbitStatus[id] = 'failed';
        this.qbitError[id] = e.message;
      }
    },

    // Mirror refreshAllStatus for qBit. Skip rows already mid-Test so
    // a manual click doesn't get clobbered by the silent background
    // sweep.
    async refreshAllQbitStatus() {
      for (const qi of (this.qbitInstances || [])) {
        if (this.qbitStatus[qi.id] === 'testing') continue;
        this.testQbitInstance(qi.id, true);
      }
    },

    confirmDeleteQbitInstance(qi) {
      this.deleteQbitTarget = qi;
    },

    async deleteQbitInstance() {
      const target = this.deleteQbitTarget;
      if (!target) return;
      this.deleteQbitBusy = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + target.id, {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadQbitInstances();
        this.deleteQbitTarget = null;
        this.showToast('qBit instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteQbitBusy = false;
      }
    },




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

    // ===== Tag quality releases mini-wizard =====
    //
    // Replaces the legacy Tag library run-card with a guided flow
    // that walks the user through Use-active-vs-Use-Discover, filter
    // (mandatory), active-list review, and Preview-vs-Apply. Final
    // Run hands off to the existing runLibraryScan after copying
    // wizard picks into the matching Alpine state — same scan, same
    // backend, just better UX.

    openTagRgWizard() {
      // Wizard is openable without a pre-picked instance — the user
      // can pick (or change) the target inside the Choices step,
      // matching the QFA flow. We just need at least one instance
      // of the current app-type to exist; the launcher button
      // already gates on that, so this is defence in depth for
      // programmatic callers.
      const pool = this.scanAvailableInstances();
      if (pool.length === 0) {
        const t = this.scanAppType === 'sonarr' ? 'Sonarr' : 'Radarr';
        this.showToast('Add a ' + t + ' instance in Settings → Instances first', 'error');
        return;
      }
      // Seed precedence: last-used remembered → current scanInstanceId
      // (when in pool) → first-of-type. Wizard's instance picker on
      // Choices lets the user override.
      const remembered = this.recallWizardInstance('tag-rg', pool);
      if (remembered) {
        this.scanInstanceId = remembered;
      } else if (!this.scanInstanceId || !pool.some(i => i.id === this.scanInstanceId)) {
        this.scanInstanceId = pool[0].id;
      }
      // Hydrate from current global state so a user who's already
      // tweaked filters / sync / cleanup on the standalone surfaces
      // sees their picks reflected in the wizard. They can still
      // change anything per-run.
      this.tagRgWizard = {
        open: true,
        step: 0,
        source: 'active',
        discoverAdd: 'enabled',
        filterOnlyTag: 'lossless-web',
        syncToSecondary: !!this.scanSyncToSecondary,
        cleanupUnusedTags: !!this.scanCleanupUnusedTags,
        runMode: 'preview',
        busy: false,
      };
    },

    closeTagRgWizard() {
      if (this.tagRgWizard.busy) return;
      this.tagRgWizard.open = false;
      // Clear the wizard-only Discover-enable-on-add override so the
      // next standalone "+ Add" / "Add Selected" click on the
      // Discover sub-tab doesn't inherit our prior pick. Without this
      // reset a user who ran the wizard with "Add + leave disabled"
      // would silently get enabled:false on every subsequent
      // standalone Discover add. addDiscoveredSearches treats
      // undefined as "use the legacy explicit-pick = enabled
      // contract" — exactly what we want.
      delete this._tagRgDiscoverEnableOnAdd;
    },

    // Steps the wizard renders. Active-groups step skipped on Use
    // Discover (the list will be augmented by Discover at run-time)
    // and on Use filter only (no group list applies — every passing
    // movie gets the single filter-only tag).
    tagRgWizardVisibleSteps() {
      const steps = ['Choices', 'Filter'];
      if (this.tagRgWizard.source === 'active') steps.push('Active groups');
      steps.push('Review');
      return steps;
    },

    // Filter-only collision check — true when the user-typed tag
    // matches any existing Active-group's Tag for this instance type.
    // Backend rejects the request with 409 in this state; the inline
    // warning + Run-button gate let the user fix it before clicking.
    // Case-insensitive to mirror the backend's strings.EqualFold.
    tagRgWizardFilterOnlyCollides() {
      const t = (this.tagRgWizard.filterOnlyTag || '').toLowerCase().trim();
      if (!t) return false;
      const appType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      return (this.groups || []).some(g => g.type === appType && g.tag && g.tag.toLowerCase() === t);
    },
    // Same collision check for the rule editor (QFA / Create Rule
    // wizards) — reads filterOnlyTag off editingRule.options instead
    // of the standalone tag-rg wizard. App-type comes from the rule's
    // primary instance.
    ruleEditorFilterOnlyCollides() {
      if (!this.editingRule || !this.editingRule.options) return false;
      const t = (this.editingRule.options.filterOnlyTag || '').toLowerCase().trim();
      if (!t) return false;
      const appType = this.ruleEditorInstanceType() || 'radarr';
      return (this.groups || []).some(g => g.type === appType && g.tag && g.tag.toLowerCase() === t);
    },

    // Clamp the current step to the new visibleSteps length. Called
    // from any state mutation that could shrink the list (today only
    // the source radios on Step 1 — Active mode adds the Active
    // groups step, Discover removes it). Without this, switching
    // source while past the affected step lands the wizard on an
    // index with no template match: every Step body keys off
    // tagRgWizardVisibleSteps()[step], so undefined → no render and
    // the footer's Next/Run buttons mis-key against a stale length.
    tagRgWizardClampStep() {
      const max = this.tagRgWizardVisibleSteps().length - 1;
      if (this.tagRgWizard.step > max) this.tagRgWizard.step = max;
    },

    tagRgWizardCanAdvance() {
      const cur = this.tagRgWizardVisibleSteps()[this.tagRgWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have a target instance picked. Auto-seeded
        // on open, but defending against an instance being deleted
        // mid-wizard or a programmatic clear.
        if (!this.scanInstanceId) return false;
        // Hard gate: source==='active' with 0 active groups would
        // produce a no-op scan — refuse advance until user switches
        // to Use Discover or closes wizard to add groups manually.
        // The inline orange banner explains the state + offers a
        // one-click switch.
        if (this.tagRgWizard.source === 'active' && this.groupsFilteredByInstanceType().length === 0) {
          return false;
        }
        // Hard gate: filter-only requires a non-empty, non-colliding
        // tag name. Empty would 400 at the backend; colliding would
        // 409. The inline collision warning shows the state inline;
        // an empty input shows the placeholder + the user just hasn't
        // filled it yet.
        if (this.tagRgWizard.source === 'filter-only') {
          const t = (this.tagRgWizard.filterOnlyTag || '').trim();
          if (!t) return false;
          if (this.tagRgWizardFilterOnlyCollides()) return false;
        }
        return true;
      }
      if (cur === 'Filter') {
        // Gate: at least one master must be on. The whole
        // restructure assumes filter is mandatory — this is the
        // enforcement.
        return !!(this.filters && (this.filters.quality || this.filters.audio));
      }
      if (cur === 'Active groups') {
        // Soft gate: warn if 0 enabled but allow continue (user might
        // want to see the Review step anyway). Run-button on Review
        // catches the empty case with a toast.
        return true;
      }
      return true;
    },

    tagRgWizardNext() {
      if (!this.tagRgWizardCanAdvance()) return;
      const max = this.tagRgWizardVisibleSteps().length - 1;
      if (this.tagRgWizard.step < max) this.tagRgWizard.step++;
    },

    tagRgWizardPrev() {
      if (this.tagRgWizard.step > 0) this.tagRgWizard.step--;
    },

    // Helpers for the secondary-instance picker. Reuses existing
    // single-secondary-auto-pick semantics from the standalone Tag
    // library card.
    tagRgWizardSecondaryAvailable() {
      const t = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      return (this.instances || []).filter(i => i.type === t && i.id !== this.scanInstanceId).length > 0;
    },
    tagRgWizardSecondaryName() {
      const t = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      const sec = (this.instances || []).find(i => i.type === t && i.id !== this.scanInstanceId);
      return sec ? sec.name : '';
    },

    // Run hands off to runLibraryScan after seeding state from
    // wizard picks. Same underlying chain — Discover-then-Tag (when
    // source==='discover') or Tag-only (when source==='active').
    async runTagRgWizard() {
      if (!this.scanInstanceId) {
        this.tagRgWizard.busy = false;
        this.showToast('Pick an instance first', 'error');
        return;
      }
      // Active-list emptiness check — for Use active mode only.
      if (this.tagRgWizard.source === 'active') {
        const enabled = this.groupsFilteredByInstanceType().filter(g => g.enabled).length;
        if (enabled === 0) {
          this.showToast('No active release groups enabled. Pick at least one or switch to Use Discover.', 'error');
          return;
        }
      }
      // Filter-only validation — defense in depth. Empty and
      // collision states are also blocked at the Choices step's
      // Next-button, but a programmatic call could bypass that.
      if (this.tagRgWizard.source === 'filter-only') {
        const t = (this.tagRgWizard.filterOnlyTag || '').trim();
        if (!t) {
          this.showToast('Enter a tag name for filter-only mode.', 'error');
          return;
        }
        if (this.tagRgWizardFilterOnlyCollides()) {
          this.showToast('Tag name collides with an Active group rule. Pick a different name.', 'error');
          return;
        }
      }
      // Filter gate — defense in depth. The wizard already blocks
      // advance from the Filter step, but a programmatic call could
      // bypass that.
      if (!this.filters || !(this.filters.quality || this.filters.audio)) {
        this.showToast('Enable at least one filter before tagging.', 'error');
        return;
      }
      // Remember the picked instance for next time the wizard opens.
      this.rememberWizardInstance('tag-rg', this.scanInstanceId);

      // Seed the standard scan state from wizard picks. runLibraryScan
      // reads these.
      this.scanModes = {
        tag: true,
        discover: this.tagRgWizard.source === 'discover',
        recover: false,
      };
      this.scanMode = this.tagRgWizard.runMode;
      this.scanSyncToSecondary = !!this.tagRgWizard.syncToSecondary;
      // Cleanup is only valid with Use active per the safety rail.
      this.scanCleanupUnusedTags = this.tagRgWizard.source === 'active'
        ? !!this.tagRgWizard.cleanupUnusedTags
        : false;
      // Filter-only pass-through. runTagInternal reads these to add
      // tagSource + filterOnlyTag to the /api/scan/run body. For
      // active / discover modes leave scanTagSource empty so the
      // backend's per-group runTag fires (legacy default).
      if (this.tagRgWizard.source === 'filter-only') {
        this.scanTagSource = 'filter-only';
        this.scanFilterOnlyTag = (this.tagRgWizard.filterOnlyTag || '').trim();
      } else {
        this.scanTagSource = '';
        this.scanFilterOnlyTag = '';
      }
      // Discover add-behavior (only meaningful when source==='discover').
      // runLibraryScan reads scanMode + scanModes; the discover-add
      // semantics are applied via the existing addDiscoveredSearches
      // path which auto-adds + enables. The "leave disabled" branch
      // requires the addDiscoveredSearches helper to honour an
      // explicit Enabled flag — it does, via cfg.ReleaseGroups
      // append where Enabled is set per the request body. We stash
      // the choice on a class-level flag the runner reads.
      this._tagRgDiscoverEnableOnAdd = this.tagRgWizard.discoverAdd === 'enabled';

      this.tagRgWizard.busy = true;
      try {
        await this.runLibraryScan();
      } finally {
        this.tagRgWizard.busy = false;
        // Close wizard after run completes (whether success or error).
        // Result modal pops via runLibraryScan's normal viewPhaseDetails
        // route, so user lands on the result panel directly.
        this.tagRgWizard.open = false;
        // Drop the wizard-only override so subsequent standalone
        // Discover adds don't inherit our pick. See closeTagRgWizard
        // for the full rationale.
        delete this._tagRgDiscoverEnableOnAdd;
      }
    },

    // ===== Discover-only mini-wizard =====
    //
    // Opened by the "Run Discover" button on the Tag quality releases
    // actions card. Walks the user through audit-mode + add-behavior
    // + filter selection before firing the existing runDiscover()
    // handler. Replaces the previous one-click button that fired
    // immediately against current globals (no way to confirm the
    // filter the scan would use).

    openDiscoverWizard() {
      const pool = this.scanAvailableInstances();
      if (pool.length === 0) {
        const t = this.scanAppType === 'sonarr' ? 'Sonarr' : 'Radarr';
        this.showToast('Add a ' + t + ' instance in Settings → Instances first', 'error');
        return;
      }
      // Seed precedence: last-used remembered → current scanInstanceId
      // (when in pool) → first-of-type.
      const remembered = this.recallWizardInstance('discover', pool);
      if (remembered) {
        this.scanInstanceId = remembered;
      } else if (!this.scanInstanceId || !pool.some(i => i.id === this.scanInstanceId)) {
        this.scanInstanceId = pool[0].id;
      }
      this.discoverWizard = {
        open: true,
        step: 0,
        runMode: 'preview',
        addBehavior: 'enabled',
        includeKnown: !!this.scanIncludeKnown,
        busy: false,
      };
    },

    closeDiscoverWizard() {
      if (this.discoverWizard.busy) return;
      this.discoverWizard.open = false;
    },

    discoverWizardVisibleSteps() {
      return ['Choices', 'Filter', 'Review'];
    },

    discoverWizardCanAdvance() {
      const cur = this.discoverWizardVisibleSteps()[this.discoverWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have a target instance. Auto-seeded on open
        // but defending against mid-wizard instance deletion.
        return !!this.scanInstanceId;
      }
      if (cur === 'Filter') {
        // Same gate as the Tag wizard — at least one filter must be on.
        // Discover with no filter would surface every release group
        // in the library (huge, useless result).
        return !!(this.filters && (this.filters.quality || this.filters.audio));
      }
      return true;
    },

    discoverWizardNext() {
      if (!this.discoverWizardCanAdvance()) return;
      const max = this.discoverWizardVisibleSteps().length - 1;
      if (this.discoverWizard.step < max) this.discoverWizard.step++;
    },

    discoverWizardPrev() {
      if (this.discoverWizard.step > 0) this.discoverWizard.step--;
    },

    // Run hands off to runDiscover() after seeding the two globals
    // it reads — scanIncludeKnown for audit mode, _tagRgDiscoverEnableOnAdd
    // for the per-row Add Selected behavior. The flag is named "tagRg"
    // historically but it's the shared "did the user pick enabled-on-add"
    // bit; both wizards write it. Result modal pops automatically when
    // scanResults.discover lands; user ticks candidates + Add Selected
    // applies the chosen enable behavior.
    async runDiscoverWizard() {
      if (!this.scanInstanceId) {
        this.discoverWizard.busy = false;
        this.showToast('Pick an instance first', 'error');
        return;
      }
      if (!this.filters || !(this.filters.quality || this.filters.audio)) {
        this.showToast('Enable at least one filter before running Discover.', 'error');
        return;
      }
      // Remember the picked instance for next time the wizard opens.
      this.rememberWizardInstance('discover', this.scanInstanceId);
      this.scanIncludeKnown = !!this.discoverWizard.includeKnown;
      this._tagRgDiscoverEnableOnAdd = this.discoverWizard.addBehavior === 'enabled';
      this.discoverWizard.busy = true;
      try {
        await this.runDiscover();
        // Apply mode — auto-add every discovered candidate with the
        // chosen add-behavior. Skips the manual Add Selected step.
        // Preview mode (default) just leaves the result modal open
        // for the user to tick which to add.
        if (this.discoverWizard.runMode === 'apply' &&
            this.scanResults && this.scanResults.discover) {
          const found = this.scanResults.discover.discovered || [];
          if (found.length > 0) {
            const searches = found.map(d => d.search);
            await this.addDiscoveredSearches(searches);
            // Dismiss the result modal — user lands back on the
            // Tag quality releases page with a toast + the updated
            // Active list. Otherwise the modal would still show
            // the auto-added candidates as if pending review.
            this.scanResults.discover = null;
          } else {
            this.showToast('Discover found no candidates.', 'info');
          }
        }
      } finally {
        this.discoverWizard.busy = false;
        this.discoverWizard.open = false;
        // The Add-Selected override stays alive ON PURPOSE in preview
        // mode — the user just opened the result modal and is about
        // to click Add Selected. Cleared on dismissDiscoverResults
        // / next wizard open. In apply mode addDiscoveredSearches
        // already consumed it; safe to leave dangling.
      }
    },

    // Tag-mode internal — fires POST /api/scan/run with action=tag and
    // stores the result in scanResults.tag. No top-level loading flag
    // toggle (the orchestrator handles that). Sets scanError on failure.
    // syncToInstanceId is set only when the user enabled "Also sync
    // decisions to <secondary>" AND there's an eligible secondary
    // (different radarr instance). Backend ignores empty value.
    async runTagInternal(opts = {}) {
      try {
        // forceMode lets a caller (e.g. confirmScanApply, Quick fix-all)
        // override the user-bound scanMode for a single call without
        // mutating the radio-group state. Avoids the visible flicker of
        // flipping `this.scanMode = 'apply'` then back to 'preview'.
        const requestMode = opts.forceMode || this.scanMode;
        // Resolve sync target / cleanup / tag-source from page state;
        // runAction builds + POSTs the body uniformly. buildSnapshotOverlay
        // carries the rule's filters + release-group subset when this Tag
        // run came from a QFA/rule preview (Apply-now writes the rule's
        // config, not global filters / Active list); {} for a true
        // standalone Library-scan run. See buildSnapshotOverlay + runAction.
        const tagOpts = {};
        if (this.scanSyncToSecondary) {
          // Prefer the explicit picker selection; fall back to first
          // other-of-same-type when the user has only one candidate
          // (legacy auto-pick) or hasn't touched the picker yet.
          let target = this.scanSyncTargetId;
          if (!target || !this.instances.find(i => i.id === target && i.type === 'radarr' && i.id !== this.scanInstanceId)) {
            const sec = this.instances.find(i => i.type === 'radarr' && i.id !== this.scanInstanceId);
            if (sec) target = sec.id;
          }
          if (target) tagOpts.syncToInstanceId = target;
        }
        // cleanupUnusedTags fires when the standalone toggle is on OR a
        // forceMode caller passes it (apply-after-preview re-fire). runAction
        // drops it in filter-only mode (no-op there by design).
        if (this.scanCleanupUnusedTags || opts.cleanupUnusedTags) tagOpts.cleanupUnusedTags = true;
        if (this.scanTagSource) {
          tagOpts.tagSource = this.scanTagSource;
          if (this.scanTagSource === 'filter-only') tagOpts.filterOnlyTag = this.scanFilterOnlyTag;
        }
        this.scanResults.tag = await this.runAction({
          instanceId: this.scanInstanceId,
          action: 'tag',
          mode: requestMode,
          overlay: this.buildSnapshotOverlay(this.scanResults.tag),
          options: tagOpts,
        });
        // Reset modal-internal expand/filter state so the new result
        // doesn't inherit stale group/row keys from the previous scan.
        // (viewPhaseDetails does the same when QFA chain or History
        // routes through it; runTagInternal is the standalone path.)
        this.scanGroupExpanded = {};
        this.scanRowExpanded = {};
        this.scanInstanceFilter = 'both';
        // Pick the most useful default filter based on the response. In
        // an apply run with 0 added / 0 removed (typical when running
        // QFA against an already-synced library) the old default of
        // 'add' landed on an empty list and made the result look broken.
        // pickDefaultScanFilter resolves to whichever bucket has the
        // most items, with a small bias toward action-buckets so a
        // preview with pending changes still lands on Match.
        this.scanFilter = this.pickDefaultScanFilter();
        if (requestMode === 'apply' && this.scanResults.tag.applied) {
          const a = this.scanResults.tag.applied;
          let msg = 'Applied: ' + a.itemsAdded + ' added, ' + a.itemsRemoved + ' removed';
          if (a.tagsDeleted && a.tagsDeleted.length > 0) {
            msg += ', ' + a.tagsDeleted.length + ' tag' + (a.tagsDeleted.length === 1 ? '' : 's') + ' deleted';
          }
          this.showToast(msg, 'success');
        }
      } catch (e) {
        this.scanError = e.message || 'Tag scan failed';
      }
    },

    // Promote an existing preview to apply. The user ran Preview first, saw
    // the decisions, and wants to commit them. We re-run with mode='apply'
    // against the same instance so the backend recomputes (not cached — the
    // library might have moved since preview) and applies the result.
    openScanApplyConfirm() {
      // Include secondary deltas in the gate — sync-only runs (primary fully
      // in sync, but secondary needs add/remove) must still be applyable.
      // Match the disabled-check on the button itself for consistency.
      if (!this.scanResults.tag) return;
      const t = this.scanResults.tag.totals;
      const total = (t.toAdd || 0) + (t.toRemove || 0) + (t.secondaryToAdd || 0) + (t.secondaryToRemove || 0);
      if (total === 0) return;
      this.showScanApplyConfirm = true;
    },

    async confirmScanApply() {
      this.showScanApplyConfirm = false;
      // Function-boundary re-entry guard — the modal Apply button
      // already :disabled-gates on scanLoading, but a programmatic
      // call (keyboard shortcut, browser back/forward, Alpine error
      // recovery cycles) could otherwise bypass that and double-fire
      // a 5+ second apply against the same instance.
      if (this.scanLoading) return;
      // Defense in depth — same reasoning as runRecoverApply.
      if (this.isHistoricalForAction('tag')) {
        this.showToast('Run a fresh Tag preview before applying — current panel is a snapshot.', 'error');
        return;
      }
      // Promote-to-apply runs against the live instance — clear any
      // historical-run banner so the result that lands isn't labelled
      // "Historical run:" when it's actually fresh-from-Radarr.
      this.historicalRunInfo = null;
      // Run apply via forceMode so the user-bound scanMode radio doesn't
      // briefly flip to 'apply' and back. The Tag library card's radio
      // stays on Preview throughout — explicit user choice is required to
      // make Apply the default.
      this.scanLoading = true;
      try {
        await this.runTagInternal({ forceMode: 'apply' });
      } finally {
        this.scanLoading = false;
      }
    },

    // Standalone Discover entrypoint — used by the "Run Discover" button on
    // the Release Groups sub-tab. Manages scanLoading itself (the orchestrator
    // wraps that around its own chain) and clears stale results so the user
    // sees a fresh run, not the previous one fading in/out under the spinner.
    async runDiscover() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      this.scanLoading = true;
      this.scanError = '';
      this.scanResults.discover = null;
      this.scanDiscoverSelected = {};
      this.scanDiscoverExpanded = {};
      this.scanDiscoverBannerDismissed = false;
      // Fresh discover replaces any historical-run banner whose kind
      // was discover (no consumer today, but the bookkeeping keeps the
      // historical-run state honest if/when a discover banner lands).
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'discover') {
        this.historicalRunInfo = null;
      }
      try {
        await this.runDiscoverInternal();
        // Surface results in a modal so they don't push the Active
        // groups list off the screen — the candidate review is a
        // one-time action, not persistent state. Only opens on a
        // standalone Run Discover (not on the Quick fix-all chain
        // which has its own combined-result panel on Run mode).
        // scanResults.discover being non-null is enough to auto-pop
        // the Discover detail modal — no separate flag needed.
      } finally {
        this.scanLoading = false;
      }
    },

    // Discover internal — same shape as runTagInternal. Stores result
    // in scanResults.discover. The orchestrator (runLibraryScan / Quick
    // fix-all) handles the top-level loading flag.
    //
    // includeKnown defaults to the user's audit-mode toggle (set on the
    // Release Groups → Find new groups panel) when called standalone, but
    // callers can override via opts. Quick fix-all overrides to false —
    // a chained run that auto-adds discovered candidates must NEVER run
    // in audit mode (would drown the chain in 409 duplicate-name errors
    // when re-adding groups already in config).
    async runDiscoverInternal(opts = {}) {
      try {
        const includeKnown = (opts && Object.prototype.hasOwnProperty.call(opts, 'includeKnown'))
          ? !!opts.includeKnown
          : !!this.scanIncludeKnown;
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'discover',
            includeKnown: includeKnown,
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.scanError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.scanResults.discover = await resp.json();
      } catch (e) {
        this.scanError = e.message || 'Discover failed';
      }
    },

    // Discover-mode selection helpers. scanDiscoverSelected and
    // scanDiscoverExpanded are {search: true} maps — Alpine reactivity
    // needs a fresh object on every mutation, so reassign instead of
    // mutating in place.
    toggleDiscoverSelected(search) {
      const next = { ...this.scanDiscoverSelected };
      if (next[search]) delete next[search];
      else next[search] = true;
      this.scanDiscoverSelected = next;
    },
    toggleDiscoverExpanded(search) {
      const next = { ...this.scanDiscoverExpanded };
      if (next[search]) delete next[search];
      else next[search] = true;
      this.scanDiscoverExpanded = next;
    },
    toggleDiscoverSampleRow(search, movieId) {
      const key = search + ':' + movieId;
      const next = { ...this.discoverSampleExpanded };
      if (next[key]) delete next[key];
      else next[key] = true;
      this.discoverSampleExpanded = next;
    },
    selectAllDiscovered() {
      if (!this.scanResults.discover || !this.scanResults.discover.discovered) return;
      const next = {};
      for (const d of this.scanResults.discover.discovered) next[d.search] = true;
      this.scanDiscoverSelected = next;
    },
    deselectAllDiscovered() {
      this.scanDiscoverSelected = {};
    },
    discoveredSelectedCount() {
      return Object.keys(this.scanDiscoverSelected).length;
    },

    // addDiscoveredSearches is the shared add-flow for both per-row
    // "+ Add" and bulk "Add Selected". Each search POSTs to /api/groups
    // with auto-generated tag/display fields. We hit the existing
    // endpoint per group rather than batching — keeps the backend's
    // Add-Group validation in one place. If any group fails (e.g.
    // tag-name collision), we surface the error and stop; remaining
    // searches stay queued in the selection map so the user can resolve
    // and retry without re-checking.
    async addDiscoveredSearches(searches) {
      if (!searches || searches.length === 0) return;
      if (!this.scanResults.discover || !this.scanResults.discover.instance) {
        this.showToast('No active instance for the discover result', 'error');
        return;
      }
      // Defense in depth — Add buttons are :disabled when viewing a
      // snapshot, but refuse here too in case anything bypasses them.
      if (this.isHistoricalForAction('discover')) {
        this.showToast('Run a fresh Discover before adding — current panel is a snapshot.', 'error');
        return;
      }
      const instType = this.scanResults.discover.instance.type;
      this.scanDiscoverAdding = true;
      let added = 0;
      let skipped = 0;       // already-existing groups — non-fatal
      let failed = '';       // first non-skip failure stops the loop
      const succeeded = [];  // includes skipped — both prune from visible list
      try {
        const bySearch = {};
        for (const d of this.scanResults.discover.discovered) bySearch[d.search] = d;
        for (const search of searches) {
          const d = bySearch[search];
          if (!d) continue;
          // Auto-generated fields. Tag label = lowercase search, sanitized
          // for Radarr's [a-z0-9_-] tag-label rules. No "rg-" prefix —
          // user prefers the bare release-group name as the tag, matching
          // how they tag manually. Display = original-case search. Mode =
          // filtered (matches bash discovery default — discovered groups
          // are always filter-mode candidates).
          const tagLabel = search.toLowerCase().replace(/[^a-z0-9_-]/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '');
          // enabled defaults true (legacy "Add Selected" semantics —
          // user explicitly picked these). Mini-wizard's Use Discover
          // mode passes _tagRgDiscoverEnableOnAdd to override; when
          // false, new groups land on Active list with Enabled off so
          // the Tag pass right after this call skips them. The wizard
          // banner explains this trade-off.
          const enabledOnAdd = (this._tagRgDiscoverEnableOnAdd === false) ? false : true;
          const payload = {
            search,
            tag: tagLabel,
            display: search,
            type: instType,
            mode: 'filtered',
            enabled: enabledOnAdd,
          };
          const resp = await this.apiFetch('/api/groups', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          });
          if (!resp.ok) {
            const body = await resp.text();
            let msg = body;
            try { msg = JSON.parse(body).error || body; } catch {}
            // 409 with "tag name already exists" is the "already in
            // your Active list" case — non-fatal. Skip + continue
            // so a bulk Add-all doesn't abort just because one of
            // the discovered groups overlaps with what you already
            // have. Other 409 reasons (filter-only schedule
            // collision) and any non-409 stay fatal.
            const isAlreadyExists = resp.status === 409 && /already exists|already used/i.test(msg || '');
            if (isAlreadyExists) {
              skipped++;
              succeeded.push(search); // prune from visible list — it IS in Active now
              if (this.scanDiscoverSelected[search]) {
                const sel = { ...this.scanDiscoverSelected };
                delete sel[search];
                this.scanDiscoverSelected = sel;
              }
              continue;
            }
            failed = `${search}: ${msg || ('HTTP ' + resp.status)}`;
            break;
          }
          // Remove from selection so retries after partial failure don't
          // re-attempt this one.
          if (this.scanDiscoverSelected[search]) {
            const sel = { ...this.scanDiscoverSelected };
            delete sel[search];
            this.scanDiscoverSelected = sel;
          }
          succeeded.push(search);
          added++;
        }
        if (succeeded.length > 0) {
          // Prune the just-added (and just-skipped) entries from the
          // visible discover list so the user doesn't see a "+ Add"
          // button next to a group already in Active. Match by search
          // string — discover keys discovered groups by raw RG (case
          // preserved from the first sample).
          if (this.scanResults.discover && Array.isArray(this.scanResults.discover.discovered)) {
            const succeededSet = new Set(succeeded);
            this.scanResults.discover.discovered = this.scanResults.discover.discovered.filter(d => !succeededSet.has(d.search));
            // Update the count too so the totals strip stays honest.
            if (this.scanResults.discover.totals) {
              this.scanResults.discover.totals.discovered = this.scanResults.discover.discovered.length;
            }
          }
          if (added > 0) await this.loadGroups();
        }
        // Toast — combines added + skipped counts so the user sees
        // the full picture in one message.
        if (added > 0 && skipped > 0) {
          this.showToast(`Added ${added} group${added === 1 ? '' : 's'} (${skipped} already in Active list — skipped)`, 'success');
        } else if (added > 0) {
          this.showToast(`Added ${added} group${added === 1 ? '' : 's'}`, 'success');
        } else if (skipped > 0 && !failed) {
          this.showToast(`All ${skipped} group${skipped === 1 ? ' was' : 's were'} already in Active list — nothing to add`, '');
        }
        if (failed) {
          this.scanError = `Add failed: ${failed}`;
          this.showToast(`Stopped after ${added} added — ${failed}`, 'error');
        }
      } finally {
        this.scanDiscoverAdding = false;
      }
    },

    // Bulk "Add Selected" — submits everything in scanDiscoverSelected.
    async addSelectedDiscovered() {
      await this.addDiscoveredSearches(Object.keys(this.scanDiscoverSelected));
    },

    // Per-row "+ Add" — submits a single discovered group. Uses the same
    // pipeline as the bulk action; the only difference is scope.
    async addOneDiscovered(search) {
      await this.addDiscoveredSearches([search]);
    },






    // ===== Webhook subsystem (M-Webhook foundation, logging-only today) =====

    // Loads the per-instance webhook configs for every Arr instance.
    // Called when the Webhooks page mounts + after wizard completion
    // / token rotation. Failures are logged but don't block the UI —
    // an instance whose GET fails just shows "not configured" and
    // works the next time the page reloads.
    //
    // Fetches in parallel via Promise.all — serialised awaits stack
    // latency on slow networks (5 instances × 50ms = 250ms wall).
    async loadWebhookSetupPage() {
      const insts = this.instances || [];
      // Defensive: if the user picked an instance for the activity tab
      // and that instance was deleted under Settings → Instances, the
      // dropdown selected-id stays dangling. Reset before re-render.
      if (this.webhookActivityInstanceId &&
          !insts.find(i => i.id === this.webhookActivityInstanceId)) {
        this.webhookActivityInstanceId = '';
      }
      const results = await Promise.all(insts.map(async inst => {
        try {
          const r = await this.apiFetch('/api/instances/' + inst.id + '/webhook');
          if (r.ok) {
            const d = await r.json();
            return [inst.id, d];
          }
        } catch (e) {
          // Tolerate per-instance failure — leave the entry unset
          // and let the UI render "not configured" for that one.
        }
        return null;
      }));
      const next = {};
      for (const r of results) {
        if (r) next[r[0]] = r[1];
      }
      // Whole-object replacement so Alpine v3's reactivity proxy
      // sees the change on EVERY key — nested-key writes don't
      // always trigger re-render when the key wasn't tracked yet
      // (the same trap that bit the Extra-tags toggle on M4).
      this.webhookConfigs = next;
      // Prefetch event lists for every CONFIGURED instance so the
      // Setup-tab "Last received" label reflects reality. Only the
      // ones with a token are worth fetching (untokenized rows can't
      // have received anything). Runs in parallel — slowest one
      // doesn't block the others.
      for (const inst of insts) {
        const cfg = next[inst.id];
        if (cfg && cfg.token) this.prefetchWebhookEventsForLabel(inst.id);
      }
      // Auto-pick the first instance OF THE ACTIVE app-type for the
      // Recent activity sub-tab if the user hasn't picked one yet —
      // saves a click on first visit. Honours the webhookAppType pill
      // so a user on the Sonarr pill auto-lands on Sonarr-1 even when
      // Radarr-1 is first in the global instance list.
      if (!this.webhookActivityInstanceId && insts.length > 0) {
        const t = this.webhookAppType || 'radarr';
        const first = insts.find(i => i.type === t) || insts[0];
        this.webhookActivityInstanceId = first.id;
        this.loadWebhookEvents(first.id);
      }
    },

    // Fetches recent events for one instance and caches them. Writes
    // to webhookEvents via spread-assign so Alpine reliably re-renders
    // when adding a new instanceId key (nested-key writes don't always
    // trigger updates in Alpine v3 — same trap class as Extra-tags).
    //
    // Also (re)starts the SSE stream for the picked instance so future
    // events arrive in real-time without further GET calls. Stream
    // disconnect happens when the user picks another instance, leaves
    // the Webhooks page, or closes the tab.
    async loadWebhookEvents(instanceId) {
      if (!instanceId) {
        this.stopWebhookEventStream();
        return;
      }
      this.webhookEventsLoading = true;
      try {
        let events = [];
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events');
        if (r.ok) {
          const d = await r.json();
          events = Array.isArray(d) ? d : [];
        }
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: events };
      } catch (e) {
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: [] };
      } finally {
        this.webhookEventsLoading = false;
      }
      this.startWebhookEventStream(instanceId);
    },

    // Lightweight events-only fetch (no SSE, no loading flag) used by
    // the Setup tab to populate webhookLastReceivedLabel for every
    // configured instance. Without this the label says "Never received"
    // for any instance the user hasn't opened in the Activity dropdown
    // yet, even when the server-side ring buffer has events. Failures
    // are silent — the label just stays at "Never" until the next
    // refresh.
    async prefetchWebhookEventsForLabel(instanceId) {
      if (!instanceId) return;
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events');
        if (!r.ok) return;
        const d = await r.json();
        const events = Array.isArray(d) ? d : [];
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: events };
      } catch (e) {
        // silent — label falls back to "Never received"
      }
    },

    // SSE-driven live updates for the Recent activity panel.
    // Replaces a polling alternative — the receiver knows when an
    // event arrives, so it pushes; browser doesn't have to ask.
    //
    // Owner of `_webhookEventSource` is this Alpine component. Only
    // one stream is alive at a time; switching instances closes the
    // old one. Browser's EventSource auto-reconnects on transient
    // network drop; we don't add a manual reconnect loop.
    startWebhookEventStream(instanceId) {
      this.stopWebhookEventStream();
      if (!instanceId) return;
      if (typeof EventSource === 'undefined') {
        // Old browser — no SSE support. Recent activity still
        // works via the GET endpoint + the ↻ button. No-op here.
        return;
      }
      try {
        const url = '/api/instances/' + instanceId + '/webhook/events/stream';
        const es = new EventSource(url);
        es.addEventListener('webhook', (msgEvent) => {
          this._handleWebhookSseEvent(instanceId, msgEvent.data);
        });
        // No reconnect spam — EventSource handles transient drops
        // itself. We only log the FIRST onerror so dev consoles
        // aren't full of "EventSource failed" reconnect noise.
        es.onerror = () => {
          // Browser will auto-reconnect; no action needed.
        };
        this._webhookEventSource = es;
        this._webhookEventSourceInstanceId = instanceId;
      } catch (e) {
        // Mostly defensive — `new EventSource` only throws on
        // a malformed URL, which we control. Swallow + leave the
        // panel in poll-via-↻ mode.
      }
      // Start the 10-second polling safety-net regardless of SSE
      // outcome — proxies sometimes silently buffer SSE responses, or
      // EventSource fails to reconnect after long idle. Polling makes
      // sure the panel reflects new events even when push is broken.
      this.startWebhookEventPolling(instanceId);
    },

    stopWebhookEventStream() {
      if (this._webhookEventSource) {
        try { this._webhookEventSource.close(); } catch (e) { /* ignore */ }
      }
      this._webhookEventSource = null;
      this._webhookEventSourceInstanceId = null;
      // Stop polling fallback too — paired lifecycle with the SSE stream.
      if (this._webhookEventPollHandle) {
        clearInterval(this._webhookEventPollHandle);
        this._webhookEventPollHandle = null;
      }
    },

    // startWebhookEventPolling is a safety-net fallback that runs
    // alongside the SSE stream. SSE is the primary push channel, but
    // it's fragile in real deployments — reverse proxies (SWAG /
    // nginx) sometimes buffer event-stream responses, or drop the
    // connection silently after the heartbeat window. Without a poll,
    // the user has to click ↻ to see new events.
    //
    // 10-second poll is cheap (the GET endpoint returns a JSON list
    // capped at 100 entries) and small enough that it doesn't fight
    // SSE — when SSE works, the poll just re-fetches the same list
    // we already have. The Activity panel re-renders against the new
    // list (Alpine spread-assign on webhookEvents).
    //
    // Stops cleanly via stopWebhookEventStream — paired lifecycle.
    startWebhookEventPolling(instanceId) {
      if (this._webhookEventPollHandle) {
        clearInterval(this._webhookEventPollHandle);
      }
      if (!instanceId) return;
      this._webhookEventPollHandle = setInterval(async () => {
        // Stale-tab guard: if the user navigated away the instance
        // dropdown will have changed; skip in that case.
        if (instanceId !== this._webhookEventSourceInstanceId) return;
        try {
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events', {
            headers: { 'X-Skip-Login-Redirect': '1' },
          });
          if (r.ok) {
            const d = await r.json();
            if (Array.isArray(d)) {
              this.webhookEvents = { ...this.webhookEvents, [instanceId]: d };
            }
          }
        } catch (e) {
          // Network blip — next tick will retry. No user-facing error.
        }
        // Piggyback: refresh the rules list too. Each rule's History
        // field carries its per-fire summaries — without this call,
        // the per-rule History modal stays stale until the user
        // manually clicks Reload. Cheap GET (small JSON, capped at
        // a handful of rules per instance).
        try {
          await this.loadWebhookRules();
        } catch (e) {
          // loadWebhookRules already swallows errors silently.
        }
      }, 10000); // 10s
    },

    // Handler for one SSE 'webhook' event payload. Prepends the new
    // event to the in-memory list (newest-first). Trims to the
    // server-side cap (100) so the display matches the persisted
    // ring on disk. Spread-assign on webhookEvents to dodge the
    // Alpine v3 nested-key reactivity trap that caught us before.
    _handleWebhookSseEvent(instanceId, payload) {
      // Stale-stream guard: if the user already switched instances
      // between event-arrival and this handler firing, drop the
      // event. The new stream will deliver future events.
      if (instanceId !== this._webhookEventSourceInstanceId) return;
      let ev;
      try { ev = JSON.parse(payload); }
      catch (e) { return; }
      if (!ev || typeof ev !== 'object') return;
      // Defence-in-depth: server stream is per-instance, so
      // ev.instanceId should always equal instanceId. Drop
      // mismatches rather than silently land an event on the
      // wrong instance's panel.
      if (ev.instanceId && ev.instanceId !== instanceId) return;
      const cur = this.webhookEvents[instanceId] || [];
      const next = [ev].concat(cur).slice(0, 100);
      this.webhookEvents = { ...this.webhookEvents, [instanceId]: next };
    },

    // "Last: 2 min ago" / "Never received" status pill text. Pulls
    // from the per-instance event cache, falls back to "Never" when
    // the cache is empty (server-side ring would be empty too in
    // that case).
    webhookLastReceivedLabel(instanceId) {
      const events = this.webhookEvents[instanceId];
      if (!events || events.length === 0) return 'Never received an event';
      const newest = events[0]; // events are newest-first
      if (!newest || !newest.receivedAt) return '';
      try {
        const d = new Date(newest.receivedAt);
        const ageMs = Date.now() - d.getTime();
        if (ageMs < 60 * 1000) return 'Last: just now';
        if (ageMs < 60 * 60 * 1000) return 'Last: ' + Math.floor(ageMs / 60000) + ' min ago';
        if (ageMs < 24 * 60 * 60 * 1000) return 'Last: ' + Math.floor(ageMs / 3600000) + ' h ago';
        return 'Last: ' + Math.floor(ageMs / 86400000) + ' d ago';
      } catch (e) {
        return '';
      }
    },

    // Copy text to clipboard with a legacy fallback. The modern
    // navigator.clipboard API only works in secure contexts (HTTPS
    // or localhost) — most users run resolvarr on a LAN HTTP URL,
    // which fails the SecureContext check. The textarea +
    // execCommand('copy') path still works there. Returns true on
    // success.
    async copyToClipboard(text) {
      if (!text) return false;
      if (navigator.clipboard && window.isSecureContext) {
        try {
          await navigator.clipboard.writeText(text);
          return true;
        } catch (e) {
          // fall through to legacy
        }
      }
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      // Off-screen but in the layout so focus + select work in every
      // browser. position:fixed avoids triggering page scroll on
      // .focus() in iOS Safari.
      ta.style.position = 'fixed';
      ta.style.top = '0';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      let ok = false;
      try {
        ok = document.execCommand('copy');
      } catch (e) {
        ok = false;
      }
      document.body.removeChild(ta);
      return ok;
    },

    async copyWebhookUrl(instanceId) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.url) return;
      const ok = await this.copyToClipboard(cfg.url);
      this.showToast(
        ok ? 'Webhook URL copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Confirm + execute token rotation. New URL means the user has
    // to update Sonarr/Radarr's Connect entry; warn explicitly so
    // they don't lose the previous URL accidentally.
    async confirmRegenerateWebhookToken(instanceId, name) {
      const cfg = this.webhookConfigs[instanceId];
      const wasConfigured = !!(cfg && cfg.token);
      const arrName = (this.instances.find(i => i.id === instanceId) || {}).type === 'sonarr' ? 'Sonarr' : 'Radarr';
      // Rotation also rotates the Secret (Phase 2 Slice A) — surface
      // that in the confirm message so users with Require-signature on
      // know they'll need to re-paste the new Secret into Arr's
      // Connect password field too.
      const msg = wasConfigured
        ? 'The current URL will stop working immediately. You\'ll need to paste BOTH the new URL and the new Secret into ' + arrName + ' → Settings → Connect → your Webhook (Secret goes in the password field).'
        : 'A unique webhook URL + Secret will be generated for ' + name + '.';
      const title = wasConfigured
        ? 'Generate a new webhook URL AND new Secret for "' + name + '"?'
        : 'Generate webhook URL for "' + name + '"?';
      if (!await this.confirmDialog({
        title:       title,
        message:     msg,
        confirmText: wasConfigured ? 'Rotate' : 'Generate',
        kind:        wasConfigured ? 'warning' : 'default',
      })) return;
      this.regenerateWebhookToken(instanceId);
    },

    async regenerateWebhookToken(instanceId) {
      try {
        // No body — preserve LoggingEnabled. Backend reads it as
        // *bool and now distinguishes "field omitted" (preserve)
        // from "field present and false" (explicit disable). Old
        // behaviour silently flipped logging off on rotate.
        //
        // Backend also preserves RequireSignature across rotation,
        // so users in strict mode stay in strict mode — but their
        // Sonarr/Radarr config still has the OLD Secret in the
        // password field, so events will start 401-ing until they
        // re-paste. Toast surfaces this; the Setup-tab card's
        // Require-signature toggle is still visible for an emergency
        // downgrade to grace mode if the user wants events flowing
        // while they go update Arr's config.
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: d };
        if (d.requireSignature) {
          this.showToast(
            'New URL + Secret generated. Require-signature is ON — re-paste the new Secret into your Webhook password field in Sonarr/Radarr or events will be rejected.',
            'success',
          );
        } else {
          this.showToast('New webhook URL + Secret generated', 'success');
        }
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      }
    },

    // Confirm + delete a configured webhook. Clears the token so the
    // receiver path returns 404 for the previous URL, reverts the
    // row to "not configured" + drops the persisted Connect entry's
    // ability to reach us. Recent activity events are NOT wiped —
    // they're keyed by instance ID, not token, and the user has a
    // separate Clear log button if they want both.
    async confirmDeleteWebhook(instanceId, name) {
      const arr = (this.instances.find(i => i.id === instanceId) || {}).type === 'sonarr' ? 'Sonarr' : 'Radarr';
      const msg = 'The current URL will stop working immediately. To reconfigure later you\'ll need to:\n' +
        '  1. Generate a new webhook here.\n' +
        '  2. Update the URL in ' + arr + ' → Settings → Connect, or remove the Connect entry there.';
      if (!await this.confirmDialog({
        title:       'Remove the webhook for "' + name + '"?',
        message:     msg,
        confirmText: 'Remove',
        kind:        'danger',
      })) return;
      this.deleteWebhook(instanceId);
    },

    async deleteWebhook(instanceId) {
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook', {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Reflect the cleared state locally — empty the entry rather
        // than dropping the key, so x-text on the row sees a defined
        // (but empty) config and renders the "not configured" branch.
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { token: '', url: '', loggingEnabled: false },
        };
        this.showToast('Webhook removed', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

    // Toggle the LoggingEnabled flag for an existing webhook config.
    // Optimistic update — the checkbox flips immediately, server
    // confirms in the background. On failure we revert. Writes use
    // spread-assign on the parent object so Alpine reliably re-renders.
    async toggleWebhookLogging(instanceId, enabled) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.token) return;
      const prev = cfg.loggingEnabled;
      this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, loggingEnabled: enabled } };
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/logging', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ loggingEnabled: enabled }),
        });
        if (!r.ok) {
          const d = await r.json();
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        // Revert
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, loggingEnabled: prev } };
        this.showToast('Logging toggle failed: ' + e.message, 'error');
      }
    },

    // Confirm + clear the per-instance log. Idempotent on the
    // server but the confirm step matches the rest of the
    // destructive-action UX (delete tag, delete instance, etc.).
    async confirmClearWebhookEvents(instanceId) {
      if (!instanceId) return;
      const events = this.webhookEvents[instanceId] || [];
      if (events.length === 0) return;
      const inst = this.instances.find(i => i.id === instanceId);
      const name = inst ? inst.name : 'this instance';
      if (!await this.confirmDialog({
        title:       'Clear logged events?',
        message:     'Clear ' + events.length + ' logged event(s) for "' + name + '". The events are also removed from the on-disk log file.',
        confirmText: 'Clear',
        kind:        'danger',
      })) return;
      this.clearWebhookEventsApi(instanceId);
    },

    async clearWebhookEventsApi(instanceId) {
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events', {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json();
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: [] };
        this.showToast('Log cleared', 'success');
      } catch (e) {
        this.showToast('Clear failed: ' + e.message, 'error');
      }
    },

    // connectEventLabel maps the raw Connect event-type strings
    // (what the payload calls them) to the labels Sonarr/Radarr show
    // in their own UI — so the user sees the same word here that
    // they ticked in the Arr's Connect settings. Most notable: the
    // payload says "Download" for what the Arr UI calls "Import".
    //
    // Unknown event types pass through verbatim so future Arr
    // additions don't crash the UI. Also handles resolvarr's own
    // custom event types (qbit:torrentAdded).
    connectEventLabel(rawType) {
      if (!rawType) return '(unknown event)';
      switch (rawType) {
        case 'Grab':                return 'Grab';
        case 'Download':            return 'Import';
        case 'Upgrade':             return 'Upgrade';
        case 'Rename':              return 'Rename';
        case 'MovieAdded':          return 'Movie added';
        case 'MovieDelete':         return 'Movie deleted';
        case 'MovieFileDelete':     return 'Movie file deleted';
        case 'SeriesAdd':           return 'Series added';
        case 'SeriesDelete':        return 'Series deleted';
        case 'EpisodeFileDelete':   return 'Episode file deleted';
        case 'HealthIssue':         return 'Health issue';
        case 'HealthRestored':      return 'Health restored';
        case 'ApplicationUpdate':   return 'Application update';
        case 'ManualInteractionRequired': return 'Manual interaction required';
        case 'Test':                return 'Test';
        case 'qbit:torrentAdded':   return 'qBit torrent added';
        default:                    return rawType;
      }
    },

    // Build [label, rawType, count] triples from the per-instance event
    // cache, sorted by count desc. Drives the filter chip row on the
    // Recent activity sub-tab. Label is the user-facing version (so
    // "Download" appears as "Import" in chips); rawType is what the
    // filter compares against to keep the filter logic correct even
    // when labels collide between Arr versions.
    webhookEventTypeCounts(instanceId) {
      const events = this.webhookEvents[instanceId] || [];
      const counts = new Map();
      for (const ev of events) {
        const t = ev.eventType || '(unknown)';
        counts.set(t, (counts.get(t) || 0) + 1);
      }
      return Array.from(counts.entries()).sort((a, b) => b[1] - a[1]);
    },

    webhookEventsFiltered() {
      const events = this.webhookEvents[this.webhookActivityInstanceId] || [];
      let out = events;
      const q = (this.webhookActivitySearch || '').trim().toLowerCase();
      if (q) {
        out = out.filter(ev =>
          ((ev.title || '').toLowerCase().includes(q)) ||
          ((ev.subtitle || '').toLowerCase().includes(q)));
      }
      if (this.webhookEventFilter !== 'all') {
        out = out.filter(ev => ev.eventType === this.webhookEventFilter);
      }
      if (this.webhookOutcomeFilter && this.webhookOutcomeFilter !== 'all') {
        out = out.filter(ev => this.eventOutcomeFilterMatch(ev));
      }
      if ((this.webhookContentShapeFilter || []).length > 0) {
        out = out.filter(ev => this.eventContentShapeFilterMatch(ev));
      }
      return out;
    },

    // Toggle the expand-to-see-JSON state on a single event card.
    // Spread-assign so first-touch keys land cleanly on Alpine v3's
    // proxy (object-key writes are the same trap class that bit
    // webhookEvents / webhookConfigs).
    toggleWebhookEventExpanded(eventId) {
      const cur = !!this.webhookEventExpanded[eventId];
      this.webhookEventExpanded = { ...this.webhookEventExpanded, [eventId]: !cur };
    },

    // Pretty-print the raw event JSON for the expand-to-see view.
    // Falls back to showing the raw string when JSON.parse fails
    // (the receiver stamps unparseable bodies through with
    // eventType="(unparseable)"; pre-pretty-printing them as
    // raw text is the right behaviour).
    formatWebhookRaw(raw) {
      if (!raw) return '';
      // raw is a JSON value (object/array/etc.) decoded by Alpine
      // from the JSON response — stringify it pretty.
      try {
        return JSON.stringify(raw, null, 2);
      } catch (e) {
        return String(raw);
      }
    },

    // ===== Webhook setup details (URL + Secret) =====
    //
    // Toggles inline display of a configured webhook's URL + Secret on
    // the Setup-tab instance card. Lets the user copy the Secret after
    // initial wizard config without having to regenerate the token
    // (which would invalidate the URL Sonarr/Radarr currently has).

    toggleWebhookDetails(instanceId) {
      const cur = !!this.webhookDetailsExpanded[instanceId];
      this.webhookDetailsExpanded = { ...this.webhookDetailsExpanded, [instanceId]: !cur };
      // Auto-mask secret again when collapsing
      if (cur) {
        this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: false };
      }
    },
    toggleWebhookSecretReveal(instanceId) {
      const cur = !!this.webhookSecretRevealed[instanceId];
      this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: !cur };
    },
    webhookSecretDisplay(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const secret = cfg.secret || '';
      if (!secret) return '(not yet generated)';
      if (this.webhookSecretRevealed[instanceId]) return secret;
      // Masked: keep first 4 + last 4 chars so user can verify the
      // value matches what they pasted into Sonarr/Radarr without
      // exposing the whole secret in over-shoulder screenshots.
      if (secret.length <= 12) return '•'.repeat(secret.length);
      return secret.slice(0, 4) + '•'.repeat(Math.max(0, secret.length - 8)) + secret.slice(-4);
    },
    // rotateWebhookSecret asks the backend for a fresh Secret while
    // keeping the existing Token (URL stays valid in Sonarr/Radarr).
    // Use when the instance has no Secret (legacy migration), or when
    // the user explicitly wants to rotate after a suspected leak.
    //
    // No confirm modal for the "missing Secret" case (zero-impact —
    // there was nothing to invalidate); shows a confirm for explicit
    // rotation when a Secret already exists because Sonarr/Radarr
    // will need its password re-pasted.
    async rotateWebhookSecret(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const hadSecret = !!cfg.secret;
      if (hadSecret) {
        const ok = await this.confirmDialog({
          title:       'Rotate webhook Secret?',
          message:     "A new Secret will be generated. The URL in Sonarr/Radarr stays the same, but you must paste the new Secret into the Connect → Password field — until you do, events with the old Secret will be rejected if Require signature is on.",
          confirmText: 'Rotate',
          kind:        'warning',
        });
        if (!ok) return;
      }
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Patch the local cache so the details panel updates without
        // a full reload.
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, secret: d.secret, url: d.url } };
        // Auto-reveal so user can immediately copy it.
        this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: true };
        this.showToast(hadSecret ? 'Secret rotated — paste the new one into Sonarr/Radarr' : 'Secret generated — paste it into Sonarr/Radarr → Connect → Password', 'success');
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      }
    },

    async copyWebhookSecret(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const secret = cfg.secret || '';
      if (!secret) {
        this.showToast('No secret to copy', 'error');
        return;
      }
      const ok = await this.copyToClipboard(secret);
      this.showToast(ok ? 'Secret copied' : 'Copy failed — reveal + select manually', ok ? 'success' : 'error');
    },

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

    // formatBytes converts a byte count into a human-readable string
    // (KiB/MiB/GiB). Standard binary IEC prefixes.
    formatBytes(bytes) {
      if (!bytes || bytes < 1024) return (bytes || 0) + ' B';
      const units = ['KiB', 'MiB', 'GiB', 'TiB'];
      let v = bytes / 1024;
      let i = 0;
      while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
      return v.toFixed(2) + ' ' + units[i];
    },

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

    // ===== Webhook configuration wizard =====
    //
    // Two-step (Choices → Summary), Arr-type-first layout matching
    // the QFA / Tag / Discover wizards. Today's only function pick
    // is "Enable logging"; later releases add per-function checkboxes
    // (release-group tag on import, DV detail on import, qBit S/E
    // tag on grab, etc.) that drive the Step-2 summary's "Connect
    // events to enable" list.

    openWebhookWizard() {
      // Seed Arr-type from the page-level pill (webhookAppType).
      // User picks the type out front via the pills, so the wizard
      // mirrors that choice rather than re-deriving from instance
      // pool. Wizard's internal Arr-type radios still let the user
      // flip mid-configuration, mostly for users who haven't seen
      // the pill yet.
      const insts = this.instances || [];
      let appType = this.webhookAppType || 'radarr';
      // Defence: if the picked app-type has no instances (shouldn't
      // happen since the pill is :disabled in that state, but
      // keyboard navigation could still trigger), fall back to
      // whichever type does have one.
      const hasPicked = insts.some(i => i.type === appType);
      if (!hasPicked) {
        const hasRadarr = insts.some(i => i.type === 'radarr');
        const hasSonarr = insts.some(i => i.type === 'sonarr');
        if (hasRadarr) appType = 'radarr';
        else if (hasSonarr) appType = 'sonarr';
      }
      // Seed instance: last-used remembered (when still in pool of
      // the picked appType — note: remembered may be from a different
      // type, in which case we ignore it) → first-of-type.
      const remembered = this.recallWizardInstance('webhook', insts.filter(i => i.type === appType));
      const firstOfType = insts.find(i => i.type === appType);
      const seedId = remembered || (firstOfType ? firstOfType.id : '');
      this.webhookWizard = {
        open: true,
        step: 0,
        appType,
        instanceId: seedId,
        fnLogging: true,
        busy: false,
        generatedUrl: '',
        generatedSecret: '',
        generatedLoggingEnabled: false,
        requireSignature: false,
      };
    },

    closeWebhookWizard() {
      if (this.webhookWizard.busy) return;
      this.webhookWizard.open = false;
    },

    webhookWizardVisibleSteps() {
      return ['Choices', 'Summary'];
    },

    // Reactive instance list for the Step 1 picker — filtered to
    // the chosen Arr type, sorted by name.
    webhookWizardInstancesForType() {
      return (this.instances || [])
        .filter(i => i.type === this.webhookWizard.appType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // When the user flips the Arr-type radio, re-seed instanceId
    // to the first instance of the new type (or empty when none).
    webhookWizardSetAppType(type) {
      this.webhookWizard.appType = type;
      const list = this.webhookWizardInstancesForType();
      this.webhookWizard.instanceId = list.length > 0 ? list[0].id : '';
    },

    webhookWizardCanAdvance() {
      const cur = this.webhookWizardVisibleSteps()[this.webhookWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have an instance picked + at least one
        // function ticked (today: logging is the only function so
        // this collapses to "fnLogging must be true").
        if (!this.webhookWizard.instanceId) return false;
        if (!this.webhookWizard.fnLogging) return false;
        return true;
      }
      return true;
    },

    webhookWizardPrev() {
      if (this.webhookWizard.step > 0) this.webhookWizard.step--;
    },

    // Advance from Choices → Summary. This is where we actually
    // mutate config: persist the function picks (Logging) and,
    // if the instance has no token yet, generate one. Existing
    // tokens are preserved so a user re-running the wizard for an
    // already-configured instance doesn't accidentally rotate.
    async webhookWizardNext() {
      if (!this.webhookWizardCanAdvance()) return;
      const cur = this.webhookWizardVisibleSteps()[this.webhookWizard.step];
      if (cur === 'Choices') {
        await this.webhookWizardCommit();
        return;
      }
      // No further forward steps today; Summary is terminal (footer
      // shows Close instead of Next).
    },

    async webhookWizardCommit() {
      const { instanceId, fnLogging } = this.webhookWizard;
      // Remember the picked instance for next time the wizard opens.
      if (instanceId) this.rememberWizardInstance('webhook', instanceId);
      this.webhookWizard.busy = true;
      try {
        // If instance already has a token, just update the logging
        // flag. Otherwise rotate to generate one. Both calls return
        // a JSON body containing { token, url, loggingEnabled }.
        const existing = this.webhookConfigs[instanceId];
        let result;
        if (existing && existing.token) {
          // Toggle logging only — token preserved. Then re-fetch the
          // full config so URL stays computed against the current
          // request host.
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/logging', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ loggingEnabled: !!fnLogging }),
          });
          if (!r.ok) {
            const d = await r.json();
            throw new Error(d.error || 'HTTP ' + r.status);
          }
          // Re-fetch to get the URL (the logging endpoint doesn't
          // return it). On failure surface the error rather than
          // silently advancing with the cached URL — that URL was
          // computed against a previous request's host header and
          // could be stale if the user moved the container behind
          // a different reverse proxy.
          const g = await this.apiFetch('/api/instances/' + instanceId + '/webhook');
          if (!g.ok) {
            const d = await g.json().catch(() => ({}));
            throw new Error(d.error || 'fetch URL: HTTP ' + g.status);
          }
          result = await g.json();
        } else {
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ loggingEnabled: !!fnLogging }),
          });
          const d = await r.json();
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          result = d;
        }
        // Mirror into the page-level cache via spread-assign so the
        // Setup sub-tab sees the new state without a manual refresh
        // and Alpine reliably re-renders even on first-write to a
        // previously-untracked instance key.
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: result };
        this.webhookWizard.generatedUrl = result.url || '';
        // Phase 2 Slice A: rotate response includes the new Secret +
        // preserved RequireSignature flag. handleWebhookSetLogging's
        // response doesn't carry these, but we re-fetched via GET
        // /webhook above when the token already existed, so the
        // result has them in both branches.
        this.webhookWizard.generatedSecret = result.secret || '';
        this.webhookWizard.requireSignature = !!result.requireSignature;
        this.webhookWizard.generatedLoggingEnabled = !!result.loggingEnabled;
        this.webhookWizard.step = 1;
      } catch (e) {
        this.showToast('Webhook setup failed: ' + e.message, 'error');
      } finally {
        this.webhookWizard.busy = false;
      }
    },

    // Connect-events-to-enable matrix for the Summary step. Today
    // logging is everything-mode — Sonarr/Radarr should toggle ALL
    // event types so resolvarr can capture them. When functions
    // land, this list will whittle to only the events the picked
    // functions need.
    webhookWizardArrEvents() {
      const isSonarr = this.webhookWizard.appType === 'sonarr';
      // Matches Sonarr's / Radarr's actual checkbox labels in their
      // Connect → Webhook config form so users can match them
      // 1:1 in the Summary step.
      if (isSonarr) {
        return [
          'On Test (verify connectivity)',
          'On Grab',
          'On Import (Download)',
          'On Upgrade (Download — upgrade)',
          'On Episode File Delete',
          'On Series Add',
          'On Series Delete',
          'On Health Issue (optional)',
          'On Health Restored (optional)',
          'On Application Update (optional)',
        ];
      }
      return [
        'On Test (verify connectivity)',
        'On Grab',
        'On Import',
        'On Upgrade',
        'On Rename',
        'On Movie Added',
        'On Movie Delete',
        'On Movie File Delete',
        'On Health Issue (optional)',
        'On Health Restored (optional)',
        'On Application Update (optional)',
      ];
    },

    // Copy the wizard's generated URL.
    async copyWebhookWizardUrl() {
      if (!this.webhookWizard.generatedUrl) return;
      const ok = await this.copyToClipboard(this.webhookWizard.generatedUrl);
      this.showToast(
        ok ? 'URL copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Copy the wizard's generated Secret. Same UX pattern as
    // copyWebhookWizardUrl — toast on success/failure. The Secret is
    // the shared password the user pastes into Sonarr/Radarr → Connect
    // → Webhook → password. Phase 2 Slice A.
    async copyWebhookWizardSecret() {
      if (!this.webhookWizard.generatedSecret) return;
      const ok = await this.copyToClipboard(this.webhookWizard.generatedSecret);
      this.showToast(
        ok ? 'Secret copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Toggle the Require-signature flag from within the wizard's
    // Summary step. Optimistic update with revert-on-failure, same
    // pattern as toggleWebhookLogging. Backend validator rejects
    // {enabled:true} with 400 when stored Secret is empty — we
    // shouldn't hit that here because the wizard reached Summary by
    // generating both Token + Secret, but we surface the error
    // through the toast just in case. Phase 2 Slice B.
    async toggleWebhookWizardRequireSignature(enabled) {
      const instanceId = this.webhookWizard.instanceId;
      if (!instanceId) return;
      const prev = this.webhookWizard.requireSignature;
      this.webhookWizard.requireSignature = enabled;
      try {
        const r = await this.apiFetch(
          '/api/instances/' + instanceId + '/webhook/require-signature',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled }),
          },
        );
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Mirror the new flag into the page-level cache so the
        // Setup-tab card pill stays in sync without a refresh.
        const cur = this.webhookConfigs[instanceId] || {};
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { ...cur, requireSignature: enabled },
        };
        this.showToast(
          enabled
            ? 'Require signature on — events without the Secret will be rejected'
            : 'Require signature off — events without the Secret pass with a warning',
          'success',
        );
      } catch (e) {
        // Revert optimistic update.
        this.webhookWizard.requireSignature = prev;
        this.showToast('Require-signature toggle failed: ' + e.message, 'error');
      }
    },

    // Per-instance Require-signature toggle from the Setup tab's
    // instance card. Same backend endpoint; this just lives outside
    // the wizard so users can flip strict mode anytime. Phase 2
    // Slice B.
    async toggleWebhookRequireSignature(instanceId, enabled) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.token) return;
      const prev = !!cfg.requireSignature;
      this.webhookConfigs = {
        ...this.webhookConfigs,
        [instanceId]: { ...cfg, requireSignature: enabled },
      };
      try {
        const r = await this.apiFetch(
          '/api/instances/' + instanceId + '/webhook/require-signature',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled }),
          },
        );
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        // Revert.
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { ...cfg, requireSignature: prev },
        };
        this.showToast('Require-signature toggle failed: ' + e.message, 'error');
      }
    },

    // formatBytes renders a byte count in B / KB / MB / GB with one
    // decimal place. Used for the DV cache file-size display. KiB-style
    // 1024-base for "what the OS reports" matches du/ls expectations.
    formatBytes(n) {
      if (n == null || isNaN(n)) return '0 B';
      if (n < 1024) return n + ' B';
      if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
      if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
      return (n / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    },

    // dateFormatOptions builds the Intl.DateTimeFormat options object
    // shared by formatDate + scheduleNextFires + scheduleNextRun so
    // every timestamp in the UI honours the same Settings → Display
    // preference. Only sets hour12 when the user explicitly picks one
    // — passing undefined hour12 + a locale lets Intl pick the
    // locale-native default, which is what the "auto" choice means.
    dateFormatOptions() {
      const opts = {};
      if (this.serverTimezone) opts.timeZone = this.serverTimezone;
      if (this.timeFormat === '24h') opts.hour12 = false;
      else if (this.timeFormat === '12h') opts.hour12 = true;
      return opts;
    },

    // highlightMatchTokens wraps tokens that drove the filter pass in
    // CSS-class spans so the user can see at a glance WHY a movie
    // qualified. Three classes (palette + style live in components.css):
    //   - .tok-rg      release group (blue, matched as `-<RG>` or bare)
    //   - .tok-quality quality-filter tokens (MA WEB-DL, Play WEB-DL)
    //   - .tok-audio   audio-filter tokens (TrueHD, Atmos, DTS-X, DTS-HD MA)
    //
    // Order matters: longer patterns first so DTS-HD MA isn't partially
    // matched by DTS alone. Word boundaries via \b avoid coloring tokens
    // mid-word (e.g. "DTSomething" wouldn't match \bDTS\b).
    //
    // We HTML-escape the input first so user-controlled scene/path strings
    // can't inject markup; the wrapping <span> tags are added on top of
    // the escaped text. Search term is escaped for regex too — release
    // groups like `126811` are safe but `[GROUP]` would otherwise blow up.
    //
    // Note: the spans use class= rather than inline style so the design
    // tokens (palette, dotted-underline vs filled-bg, etc.) live in CSS.
    // One stylesheet update changes every panel that calls this helper.
    highlightMatchTokens(text, releaseGroup, opts) {
      if (!text) return '';
      const options = opts || {};
      // Escape HTML
      let html = String(text)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');

      const wrap = (re, cls) => {
        html = html.replace(re, (m) => `<span class="${cls}">${m}</span>`);
      };

      // Quality + audio tokens are filter-decision context — only useful
      // where the user is checking whether a release passes the engine's
      // filter set (scan results, discover samples). Compare-tags and the
      // tag-inventory drill-down don't care about filter passage; they're
      // listing tag membership. opts.releaseGroupOnly suppresses these
      // wraps so those views stay clean (just the blue tag-label highlight).
      if (!options.releaseGroupOnly) {
        // Audio tokens — longest first so DTS-HD MA isn't pre-eaten.
        wrap(/\bDTS[._\-\s]?HD[._\-\s]?MA\b/gi, 'tok-audio');
        wrap(/\bDTS[._\-\s]?X\b/gi, 'tok-audio');
        wrap(/\bAtmos\b/gi, 'tok-audio');
        wrap(/\bTrueHD\b/gi, 'tok-audio');

        // Quality tokens — bash check_quality_match uses these regexes:
        //   MA WEB-DL  : \bma(\]?\s*\[?|[._-])web([-.]?dl)?
        //   Play WEB-DL: \bplay(\]?\s*\[?|[._-])web([-.]?dl)?
        wrap(/\bMA([._\-\s]|\][\s_]?\[?)WEB(?:[._\-]?DL)?\b/gi, 'tok-quality');
        wrap(/\bPlay([._\-\s]|\][\s_]?\[?)WEB(?:[._\-]?DL)?\b/gi, 'tok-quality');
      }

      // Release group — match `-<RG>` (the typical "filename-FLUX" form)
      // AND bare `<RG>` (the case where the value IS the release group,
      // e.g. the releaseGroup: field on its own). Optional dash prefix
      // + word boundaries keeps it from matching mid-token. Escape
      // regex metacharacters in the group name to be safe.
      //
      // releaseGroup may be a string (single RG, the typical case) or an
      // array (used by compare-tags where two tag labels both want
      // highlighting in the same file-context block).
      const rgs = Array.isArray(releaseGroup) ? releaseGroup : (releaseGroup ? [releaseGroup] : []);
      for (const rg of rgs) {
        if (!rg) continue;
        const rgEsc = rg.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        wrap(new RegExp('-?\\b' + rgEsc + '\\b', 'gi'), 'tok-rg');
      }

      return html;
    },

    // Returns the subset of scanResult.items that match the current filter
    // chip. 'all' returns everything; the action filters return items that
    // contain at least one decision with the matching action. 'nofile'
    // returns items with no decisions (which indicates the backend skipped
    // them because MovieFile was nil).
    filteredScanItems() {
      // Legacy filter — kept for the no-file flow + filter-counts. The
      // group-first display uses decisionsByGroup() instead, which only
      // covers items that have decisions (no-file items by definition
      // don't appear under any group).
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter === 'all') return r.items;
      if (this.scanFilter === 'nofile') return r.items.filter(i => !i.decisions || i.decisions.length === 0);
      return r.items.filter(item => (item.decisions || []).some(d => d.action === this.scanFilter));
    },

    // No-file items shown only when filter is "nofile" — they don't fit
    // any release-group bucket since they have no decisions.
    noFileScanItems() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter !== 'nofile') return [];
      return r.items.filter(i => !i.decisions || i.decisions.length === 0);
    },

    // Missing-in-secondary items (sync mode only). One row per primary
    // movie that has no matching TmdbID in secondary — `secondaryAction`
    // is identical across all groups for the same item (the movie is
    // either present in secondary or not), so we dedupe by movie ID.
    missingScanItems() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter !== 'missing') return [];
      const seen = new Set();
      const out = [];
      for (const item of r.items) {
        if (!item.decisions) continue;
        const isMissing = item.decisions.some(d => d.secondaryAction === 'missing');
        if (isMissing && !seen.has(item.id)) {
          seen.add(item.id);
          out.push(item);
        }
      }
      return out;
    },

    // Counts per action across the tag result. Respects scanInstanceFilter:
    //   'primary'   → only primary action counted
    //   'secondary' → only secondary action counted (secondary has no nofile concept)
    //   'both'      → counts EITHER side that matches (so a movie matching primary
    //                 'add' AND secondary 'add' is counted twice — that's
    //                 intentional, both are real actions to apply).
    // Also respects orphan-pass-only contributions to secondary remove counts.
    // Switches the visible-instance filter and re-picks the status chip
    // if the new filter would orphan it (e.g. scanFilter='missing' is
    // a secondary-only concept, scanFilter='nofile' is a primary-only
    // concept — when scanInstanceFilter flips, the chip can become
    // incompatible). Re-pick keeps the user on a valid chip without
    // forcing them to click again.
    setScanInstanceFilter(next) {
      this.scanInstanceFilter = next;
      const f = this.scanFilter;
      // Hidden-chip cases per index.html @x-show conditions:
      //   No file: hidden when filter=secondary
      //   Missing: hidden when filter=primary
      if ((f === 'nofile' && next === 'secondary') || (f === 'missing' && next === 'primary')) {
        this.scanFilter = this.pickDefaultScanFilter();
      }
    },

    // Pick the most useful default filter chip based on the response.
    // Action buckets (add/remove) win when they have anything, since the
    // user typically came to act on changes. When all action buckets are
    // empty (common: QFA against already-synced library, returned 0/0),
    // fall back to whichever non-action bucket has the most items so the
    // user lands on data instead of an empty filter. Honors the active
    // scanInstanceFilter so the counts reflect what's currently visible.
    pickDefaultScanFilter() {
      const c = this.scanFilterCounts();
      if (c.add > 0) return 'add';
      if (c.remove > 0) return 'remove';
      // No actionable change. Pick the largest of keep / nofile / missing.
      const r = this.scanResults.tag;
      const missing = (r && r.totals && r.totals.secondaryMissing) || 0;
      const candidates = [
        { name: 'keep',    count: c.keep },
        { name: 'nofile',  count: c.nofile },
        { name: 'missing', count: missing },
      ];
      candidates.sort((a, b) => b.count - a.count);
      return candidates[0].count > 0 ? candidates[0].name : 'add';
    },

    scanFilterCounts() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return { add: 0, remove: 0, keep: 0, nofile: 0 };
      const fil = this.scanInstanceFilter;
      let add = 0, remove = 0, keep = 0, nofile = 0;
      for (const item of r.items) {
        if (!item.decisions || item.decisions.length === 0) {
          if (fil !== 'secondary') nofile++;
          continue;
        }
        for (const d of item.decisions) {
          if (fil === 'primary' || fil === 'both') {
            if (d.action === 'add') add++;
            else if (d.action === 'remove') remove++;
            else if (d.action === 'keep') keep++;
          }
          if (fil === 'secondary' || fil === 'both') {
            if (d.secondaryAction === 'add') add++;
            else if (d.secondaryAction === 'remove') remove++;
            else if (d.secondaryAction === 'keep') keep++;
          }
        }
      }
      return { add, remove, keep, nofile };
    },

    // Reorganize per-movie items into per-group buckets. Each group row
    // collapses N decisions across the library into one expandable entry,
    // matching Discover's UI pattern. Filtered to actions matching the
    // current scanFilter (or all actions when scanFilter is "all").
    //
    // Each bucket carries:
    //   group: { id, tag, display }
    //   items: [{ ...item fields, action, matched, matchLocation,
    //             quality, qualityDetail, audio, audioDetail, reason }]
    //   totals: { add, remove, keep, skip }  (full per-group totals,
    //                                          unfiltered — for the chip)
    //
    // Sort: groups by tag-label alphabetical. Within a group, items by
    // movie title.
    decisionsByGroup() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      const filter = this.scanFilter;
      const instFil = this.scanInstanceFilter;
      // No-file rows handled in noFileScanItems(), missing handled separately.
      if (filter === 'nofile' || filter === 'missing') return [];
      const buckets = new Map();
      for (const item of r.items) {
        if (!item.decisions || item.decisions.length === 0) continue;
        for (const d of item.decisions) {
          let b = buckets.get(d.groupId);
          if (!b) {
            b = {
              group: { id: d.groupId, tag: d.groupTag, display: d.groupDisplay },
              items: [],
              totals: { add: 0, remove: 0, keep: 0, skip: 0 },
            };
            buckets.set(d.groupId, b);
          }
          b.totals[d.action] = (b.totals[d.action] || 0) + 1;
          // Decide whether this (item, decision) matches the chosen filter,
          // taking instance-filter into account. For 'both', either side
          // matching qualifies the row for inclusion.
          let matchesFilter = false;
          if (instFil === 'primary' || instFil === 'both') {
            if (d.action === filter) matchesFilter = true;
          }
          if (!matchesFilter && (instFil === 'secondary' || instFil === 'both')) {
            if (d.secondaryAction === filter) matchesFilter = true;
          }
          if (matchesFilter) {
            b.items.push({
              ...item,
              action: d.action,
              matched: d.matched,
              matchLocation: d.matchLocation,
              quality: d.quality,
              qualityDetail: d.qualityDetail,
              audio: d.audio,
              audioDetail: d.audioDetail,
              reason: d.reason,
              secondaryAction: d.secondaryAction,
              secondaryHasTag: d.secondaryHasTag,
            });
          }
        }
      }
      // Drop groups with no items after the filter — keeps the screen
      // tight when the user picks "Add" and most groups have only Keeps.
      const out = [];
      for (const b of buckets.values()) {
        if (b.items.length === 0) continue;
        b.items.sort((a, c) => a.title.localeCompare(c.title, undefined, { sensitivity: 'base' }));
        out.push(b);
      }
      out.sort((a, b) => a.group.tag.localeCompare(b.group.tag, undefined, { sensitivity: 'base' }));
      return out;
    },

    toggleScanRowExpanded(itemId) {
      const next = { ...this.scanRowExpanded };
      if (next[itemId]) delete next[itemId];
      else next[itemId] = true;
      this.scanRowExpanded = next;
    },

    toggleScanGroupExpanded(groupId) {
      const next = { ...this.scanGroupExpanded };
      if (next[groupId]) delete next[groupId];
      else next[groupId] = true;
      this.scanGroupExpanded = next;
    },

    iconUrl(type, variant) {
      if (type === 'radarr') return variant === '4k' ? '/icons/radarr4kNew.png' : '/icons/radarrNew.png';
      return variant === '4k' ? '/icons/sonarr4k.png' : '/icons/sonarr.png';
    },

    async refreshAllStatus() {
      for (const inst of this.instances) {
        // Don't overwrite a 'testing' that the user just triggered
        if (this.instStatus[inst.id] === 'testing') continue;
        this.testInstance(inst, true);
      }
    },

    async loadConfig() {
      try {
        const r = await this.apiFetch('/api/config');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const cfg = await r.json();
        this.instances = cfg.instances || [];
        // Reconcile picker state — if the previously-chosen cross-instance
        // compare target was deleted (or had its type changed) since the
        // last config load, clear it so the user doesn't see stale UI or
        // hit "Instance not found" on the next Compare click. Same pattern
        // already applied to tagsInstanceId in init() at app.js:627.
        if (this.compareCrossInstanceTarget) {
          const t = this.currentInstanceType();
          const stillThere = this.instances.find(
            i => i.id === this.compareCrossInstanceTarget && i.type === t && i.id !== this.tagsInstanceId
          );
          if (!stillThere) this.compareCrossInstanceTarget = '';
        }
        this.discord = cfg.discord || { enabled: false, webhookUrl: '' };
        this.uiScale = (cfg.display && cfg.display.uiScale) || '1.1';
        this.timeFormat = (cfg.display && cfg.display.timeFormat) || 'auto';
        this.groups = cfg.releaseGroups || [];
        // FilterSet has per-Arr-type structure (cfg.filters.radarr +
        // cfg.filters.sonarr). The Filters sub-tab only renders one set
        // today — UI-side mirroring of per-type filters lands with the
        // M-Sonarr per-episode walker. Until then we read/write the
        // Radarr block, which matches the active scan type for every
        // shipped feature (Radarr-only handlers).
        const f = (cfg.filters && cfg.filters.radarr) || {};
        this.filters = {
          quality: f.Quality !== false,
          maWebDL: f.MAWebDL !== false,
          playWebDL: f.PlayWebDL !== false,
          audio: f.Audio !== false,
          trueHD: f.TrueHD !== false,
          trueHDAtmos: f.TrueHDAtmos !== false,
          dtsX: f.DTSX !== false,
          dtsHDMA: f.DTSHDMA !== false,
        };
        // Snapshot the Sonarr block so we can round-trip it through
        // saveFilters without the user's checkbox toggles wiping the
        // Sonarr-side defaults that Load() backfilled.
        this._savedSonarrFilters = (cfg.filters && cfg.filters.sonarr) || null;
        // Audio + Video tags load from their own endpoints (each ships
        // {config, ...vocab} together so the UI can render the closed-
        // vocab checkbox matrix without hardcoding values).
        await this.loadAudioTags();
        await this.loadVideoTags();
        await this.loadDvDetail();
        // Best-effort tools status — ignore errors so a 503 (DV not wired
        // in dev mode) doesn't trip the toast banner.
        try { await this.loadDvToolsStatus(); } catch (_) {}
        try { await this.loadDvCacheStats(); } catch (_) {}
      } catch (e) {
        this.showToast('Load failed: ' + e.message, 'error');
      }
    },

    // --- DV detail (M4b) ---
    async loadDvDetail() {
      try {
        // GET — CSRF doesn't gate it but use apiFetch for consistency.
        const r = await this.apiFetch('/api/dv-detail');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config) {
          this.dvDetail.enabled = !!data.config.enabled;
          this.dvDetail.prefix = data.config.prefix || '';
          this.dvDetail.allowedValues = Array.isArray(data.config.allowedValues) ? data.config.allowedValues : [];
          this.dvDetail.selectMode = data.config.selectMode || '';
          this.dvDetail.labels = (data.config.labels && typeof data.config.labels === 'object') ? { ...data.config.labels } : {};
          this.dvDetail.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.vocabulary)) {
          this.dvDetailVocab = data.vocabulary;
        }
      } catch (e) {
        // Silent — config-load is best-effort. saveDvDetail will surface
        // network errors if the user actually tries to write.
      }
    },

    async saveDvDetail() {
      // Same pattern as saveExtraTags: PUT the full object (server treats
      // it as wholesale replacement). Validation errors come back as 400
      // with a clear message — surface as a toast.
      // apiFetch (not raw fetch) — CSRF middleware rejects PUT without
      // the X-CSRF-Token header, same trap that bit the DV-tools handlers.
      try {
        const r = await this.apiFetch('/api/dv-detail', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            enabled: !!this.dvDetail.enabled,
            prefix: this.dvDetail.prefix || '',
            allowedValues: this.dvDetail.allowedValues,
            selectMode: this.dvDetail.selectMode || '',
            labels: this.dvDetail.labels || {},
            removeOrphanedTags: !!this.dvDetail.removeOrphanedTags,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          this.showToast('DV detail save failed: ' + (err.error || r.status), 'error');
          return;
        }
      } catch (e) {
        this.showToast('DV detail save failed: ' + e.message, 'error');
      }
    },

    // toggleDvDetailValue uses the same two-mode semantics as the audio
    // and video bucket toggles — see audioTagValueChecked for the full
    // SelectMode rationale. Default mode treats empty=all; first per-
    // value click switches to "select" mode using the full vocab as
    // baseline so the unchecked one disappears as expected.
    toggleDvDetailValue(value) {
      const fullVocab = [...this.dvDetailVocab];
      if (this.dvDetail.selectMode !== 'select') {
        if (!this.dvDetail.allowedValues || this.dvDetail.allowedValues.length === 0) {
          this.dvDetail.allowedValues = [...fullVocab];
        }
        this.dvDetail.selectMode = 'select';
      }
      const av = this.dvDetail.allowedValues || [];
      const idx = av.indexOf(value);
      if (idx >= 0) av.splice(idx, 1);
      else          av.push(value);
      const order = (v) => this.dvDetailVocab.indexOf(v);
      av.sort((a, b) => order(a) - order(b));
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        this.dvDetail.selectMode = '';
        this.dvDetail.allowedValues = [];
      } else {
        this.dvDetail.allowedValues = av;
      }
      this.saveDvDetail();
    },
    selectAllDvDetailValues() {
      this.dvDetail.selectMode = '';
      this.dvDetail.allowedValues = [];
      this.saveDvDetail();
    },
    selectNoneDvDetailValues() {
      this.dvDetail.selectMode = 'select';
      this.dvDetail.allowedValues = [];
      this.saveDvDetail();
    },

    isDvDetailValueAllowed(value) {
      const av = this.dvDetail.allowedValues || [];
      if (this.dvDetail.selectMode !== 'select' && av.length === 0) return true;
      return av.includes(value);
    },

    async loadDvToolsStatus() {
      try {
        // GET — CSRF doesn't gate it but use apiFetch for consistency
        // with the rest of the codebase + any future auth wiring.
        const r = await this.apiFetch('/api/tools/dv/status');
        if (r.status === 503) {
          // AttachDV not called server-side — dev/test build, not a
          // production deployment. Banner stays in default
          // {installed:false} state which shows the [Install] CTA;
          // the user clicking Install will get a 503 from the install
          // endpoint with a clear "DV tools not configured" message.
          // Console.warn so a dev running locally sees the wiring gap.
          // eslint-disable-next-line no-console
          console.warn('DV tools status endpoint returned 503 — AttachDV likely not wired in main; install button will fail with 503 too.');
          return;
        }
        if (!r.ok) return;
        this.dvTools = await r.json();
      } catch (e) {
        // Silent on poll failure; banner just stays as-is.
      }
    },

    // installDvTools / uninstallDvTools removed — DV tools (ffmpeg +
    // dovi_tool) ship baked into the image as of v0.3.5 via the
    // Dockerfile dv-tools stage. No env var, no install step.
    // loadDvToolsStatus stays as a defensive health check for the
    // "Tools unreachable" UI branch (only fires if the image build
    // is somehow broken — should never happen in normal CI).

    // --- DV cache panel (Library scan → DV detail tab) ---
    // GET /api/dv-cache/stats. Server returns zero-valued struct when
    // DvCache is nil (defensive — shouldn't happen in normal deploys
    // but covered). UI renders "No files cached yet" copy in that case.
    async loadDvCacheStats() {
      try {
        const r = await this.apiFetch('/api/dv-cache/stats');
        if (!r.ok) return;
        this.dvCacheStats = await r.json();
      } catch (_) {
        // Silent — panel just shows the empty-state copy.
      }
    },
    // Open the confirm modal. Stats are already loaded; if not, we
    // still open the modal so the user can see "0 cached files" and
    // the Clear button is naturally disabled.
    openClearDvCacheConfirm() {
      this.showClearDvCacheConfirm = true;
    },
    // Fire the DELETE. Server wipes in-memory + persists empty file
    // and returns the post-clear stats so we don't need a follow-up
    // GET. Toast on success/failure.
    async confirmClearDvCache() {
      if (this.clearingDvCache) return;
      this.clearingDvCache = true;
      try {
        const r = await this.apiFetch('/api/dv-cache', { method: 'DELETE' });
        if (!r.ok) {
          let msg = 'HTTP ' + r.status;
          try { msg = (await r.json()).error || msg; } catch (_) {}
          throw new Error(msg);
        }
        this.dvCacheStats = await r.json();
        this.showToast('DV cache cleared — next scan will re-extract from scratch', 'success');
        this.showClearDvCacheConfirm = false;
      } catch (e) {
        this.showToast('Clear DV cache failed: ' + e.message, 'error');
      } finally {
        this.clearingDvCache = false;
      }
    },

    async runDvDetailScan(mode, bypassOverride, instanceIdOverride = '') {
      // instanceIdOverride: same target=both fan-out as runAudioTagsScan.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) return;
      // Defensive re-entry guard — same pattern as runQuickFixChain.
      // The Run-button gates on scanLoading at the UI layer; this
      // is the function-level safety net.
      if (this.scanLoading) return;
      // bypassOverride lets confirmDvDetailApply pass the snapshot it
      // captured before resetting dvBypassCache. Falsy → fall back to
      // the live state (Preview path).
      const bypassDvCache = bypassOverride !== undefined ? !!bypassOverride : !!this.dvBypassCache;
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('dv');
        this.scanResults.dvDetail = null;
        this.scanError = '';
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'dvdetail') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      // Reset progress + start polling. The scan is slow enough
      // (~1-3s per file × N files) that the user needs both a
      // current-file label and a way to cancel. Poll runs every 1.2s
      // — frequent enough to feel live, slow enough that polling
      // never bottlenecks anything.
      this.dvScanProgress = { running: true, total: 0, processed: 0, currentTitle: '' };
      this.startDvScanPoll();
      try {
        // runAction builds + POSTs uniformly with the run's overlay, not
        // global. On failure it throws; the catch below sets scanError
        // (which confirmDvDetailApply's target=both fan-out reads to
        // short-circuit) + surfaces the toast.
        this.scanResults.dvDetail = await this.runAction({
          instanceId: targetInstanceId, action: 'dvdetail', mode,
          overlay: this.buildSnapshotOverlay(this.qfaDetailDv),
          options: { bypassDvCache },
        });
        const totals = (this.scanResults.dvDetail && this.scanResults.dvDetail.totals) || {};
        const summary = (totals.dvCandidates || 0) + ' candidates · ' +
                        (totals.toAdd || 0) + ' add · ' +
                        (totals.toRemove || 0) + ' remove · ' +
                        (totals.toKeep || 0) + ' keep';
        this.showToast((mode === 'apply' ? 'DV detail applied — ' : 'DV detail preview ready — ') + summary, 'success');
        this.viewPhaseDetails({ phase: 'dvdetail', response: this.scanResults.dvDetail });
      } catch (e) {
        this.scanError = e.message || 'DV detail scan failed';
        this.showToast('DV detail scan failed: ' + e.message, 'error');
      } finally {
        this.scanLoading = false;
        this.stopDvScanPoll();
        this.dvScanProgress = null;
        // Refresh the History tab's scan-list if the user is sitting
        // on it so the just-dumped scan appears without manual reload.
        // No-op when they're elsewhere; the loadScanHistory tick on
        // History-tab landing will pick it up.
        if (this.scanSection === 'history') this.loadScanHistory();
        // Refresh the cache panel — a cache-active scan populates new
        // entries; a bypass scan changes nothing on disk but loadDvCacheStats
        // is cheap enough to run regardless.
        try { await this.loadDvCacheStats(); } catch (_) {}
      }
    },



    // ---- Rule-editor per-value allow-list helpers --------------------
    // Mirror of the global toggleAudioTagValue / toggleVideoTagValue /
    // toggleDvDetailValue but bound to editingRule.* instead of global
    // state. Don't call save* — the rule-editor's Save button persists
    // everything atomically.

    ruleAudioTagValueChecked(value) {
      if (!this.editingRule || !this.editingRule.audioTags) return false;
      // The rule editor has no select-none affordance (clearing a bucket
      // disables it), so an empty allow-list always means "all values" —
      // both the clean all-mode and the legacy select-mode + empty state.
      const av = this.editingRule.audioTags.audio && this.editingRule.audioTags.audio.allowedValues;
      if (!av || av.length === 0) return true;
      return av.includes(value);
    },
    ruleToggleAudioTagValue(value, fullVocab) {
      if (!this.editingRule || !this.editingRule.audioTags) return;
      const bucket = this.editingRule.audioTags.audio;
      // Empty allow-list means "all" here, so seed the full vocab before
      // toggling — unchecking one value then keeps the rest, instead of
      // collapsing to just the one clicked.
      let av = bucket.allowedValues || [];
      if (av.length === 0) av = [...fullVocab];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      if (av.length === 0) {
        bucket.enabled = false;
        bucket.selectMode = '';
        bucket.allowedValues = [];
        this.showToast('Audio bucket disabled — no values were left allowed', 'info');
        return;
      }
      // All selected → all-mode (empty + no select). Partial → explicit
      // select-list. Setting selectMode is the fix: the old code left it
      // as "select" with an empty list, which the engine read as "tag
      // nothing" and stripped every tag.
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        bucket.selectMode = '';
        bucket.allowedValues = [];
      } else {
        bucket.selectMode = 'select';
        bucket.allowedValues = av;
      }
    },

    ruleVideoTagValueChecked(bucketKey, value) {
      if (!this.editingRule || !this.editingRule.videoTags) return false;
      // Empty allow-list = "all values" in the rule editor (no select-none
      // affordance here), covering both clean all-mode and legacy state.
      const av = this.editingRule.videoTags[bucketKey] && this.editingRule.videoTags[bucketKey].allowedValues;
      if (!av || av.length === 0) return true;
      return av.includes(value);
    },
    ruleToggleVideoTagValue(bucketKey, value, fullVocab) {
      if (!this.editingRule || !this.editingRule.videoTags) return;
      const bucket = this.editingRule.videoTags[bucketKey];
      // Empty allow-list means "all" — seed the full vocab before toggling
      // so unchecking one value keeps the rest.
      let av = bucket.allowedValues || [];
      if (av.length === 0) av = [...fullVocab];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      if (av.length === 0) {
        bucket.enabled = false;
        bucket.selectMode = '';
        bucket.allowedValues = [];
        this.showToast(bucketKey + ' bucket disabled — no values were left allowed', 'info');
        return;
      }
      // All selected → all-mode (empty + no select). Partial → explicit
      // select-list. Setting selectMode is the fix: the old code left it
      // as "select" with an empty list, which the engine read as "tag
      // nothing" and stripped every tag.
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        bucket.selectMode = '';
        bucket.allowedValues = [];
      } else {
        bucket.selectMode = 'select';
        bucket.allowedValues = av;
      }
    },

    ruleDvDetailValueChecked(value) {
      if (!this.editingRule || !this.editingRule.dvDetail) return false;
      const av = this.editingRule.dvDetail.allowedValues;
      if (!av || av.length === 0) return true;
      return av.includes(value);
    },
    ruleToggleDvDetailValue(value) {
      if (!this.editingRule || !this.editingRule.dvDetail) return;
      const dd = this.editingRule.dvDetail;
      const av = dd.allowedValues || [];
      const idx = av.indexOf(value);
      if (idx >= 0) av.splice(idx, 1);
      else          av.push(value);
      const order = (v) => this.dvDetailVocab.indexOf(v);
      av.sort((a, b) => order(a) - order(b));
      dd.allowedValues = av;
    },

    // ---- Review-step helpers (Quick fix-all + schedule wizard) ----
    // The Review step is the user's last chance to catch a wrong
    // toggle before committing — so it spells out everything the
    // run will do, not just the headline. Each helper returns a
    // readable string; templates render them inline.

    // Run-mode label as it'll behave on dispatch.
    reviewRunModeLabel() {
      if (!this.editingRule) return '';
      const app = (this.ruleEditor && this.ruleEditor.appType === 'sonarr') ? 'Sonarr' : 'Radarr';
      const m = this.editingRule.options && this.editingRule.options.runMode;
      if (m === 'preview') return `Preview (read-only — nothing is written to ${app})`;
      return `Apply (writes tag changes to ${app})`;
    },

    // Resolve the secondary instance that sync / auto-tags-on-secondary
    // would target. Mirrors backend resolveSyncTarget logic: explicit
    // SyncToInstanceID wins; otherwise pick first other-of-same-type.
    // Returns the instance object or null when sync is disabled / no
    // valid target exists.
    reviewSecondaryInstance() {
      const r = this.editingRule;
      if (!r || !r.options || !r.options.syncToSecondary) return null;
      if (r.options.syncToInstanceId) {
        return this.instances.find(i => i.id === r.options.syncToInstanceId) || null;
      }
      const primary = this.instances.find(i => i.id === r.instanceId);
      if (!primary) return null;
      return this.instances.find(i => i.type === primary.type && i.id !== r.instanceId) || null;
    },

    // reviewTargetLabel — render a per-bucket target ('primary' |
    // 'secondary' | 'both') for the Review step using the resolved
    // instance names. Falls back to "primary only" when the rule has
    // a single instance (in which case the picker isn't shown anyway,
    // but a stale target value should still render gracefully).
    reviewTargetLabel(target) {
      const t = target || 'primary';
      const r = this.editingRule;
      if (!r) return t;
      const primary = (this.instances || []).find(i => i.id === r.instanceId) || {};
      const secondary = this.ruleSecondaryInstance() || {};
      const pName = primary.name || 'primary';
      const sName = secondary.name || 'secondary';
      if (t === 'primary')   return pName + ' only';
      if (t === 'secondary') return sName + ' only';
      if (t === 'both')      return 'both — ' + pName + ' then ' + sName;
      return t;
    },

    // ruleHasSecondary — true when the rule's primary instance has at
    // least one same-type sibling (so a secondary target is available).
    // Drives visibility of the per-bucket target pickers (audio /
    // video / DV) on each step. With one instance the picker is hidden;
    // bucket runs against primary implicitly.
    ruleHasSecondary() {
      const r = this.editingRule;
      if (!r) return false;
      const primary = (this.instances || []).find(i => i.id === r.instanceId);
      if (!primary) return false;
      return (this.instances || []).some(i => i.id !== r.instanceId && i.type === primary.type);
    },

    // ruleSecondaryInstance — the resolved secondary instance for the
    // current rule. Prefers the explicit syncToInstanceId pick; falls
    // back to first other-of-same-type. Distinct from
    // reviewSecondaryInstance which only resolves when tag-sync is on
    // — the per-bucket target pickers care about secondary regardless
    // of whether tag-sync is configured (auto-tags can run on secondary
    // without tag mirroring).
    ruleSecondaryInstance() {
      const r = this.editingRule;
      if (!r) return {};
      const primary = (this.instances || []).find(i => i.id === r.instanceId);
      if (!primary) return {};
      if (r.options && r.options.syncToInstanceId) {
        const explicit = (this.instances || []).find(i => i.id === r.options.syncToInstanceId);
        if (explicit) return explicit;
      }
      return (this.instances || []).find(i => i.id !== r.instanceId && i.type === primary.type) || {};
    },

    // Active filter list — returns an array of human labels for every
    // enabled filter on the rule's per-rule snapshot. Empty array
    // when all are off (which means "no quality/audio gating — every
    // matched release group passes").
    reviewActiveFilters() {
      if (!this.editingRule || !this.editingRule.filters) return [];
      const f = this.editingRule.filters;
      const out = [];
      if (f.Quality)     out.push('Quality');
      if (f.MAWebDL)     out.push('MA WebDL');
      if (f.PlayWebDL)   out.push('Play WebDL');
      if (f.Audio)       out.push('Audio');
      if (f.TrueHD)      out.push('TrueHD');
      if (f.TrueHDAtmos) out.push('TrueHD Atmos');
      if (f.DTSX)        out.push('DTS:X');
      if (f.DTSHDMA)     out.push('DTS-HD MA');
      return out;
    },

    // Per-bucket summary helper used by the Audio / Video / DV
    // sections of Review. Returns one of:
    //   "Disabled"
    //   "Enabled — bare values, all allowed"
    //   "Enabled — prefix `audio-`, 4 of 12 values"
    // Plex label sync review helpers — render an inline Plex-sync
    // config as four readable lines for the Review step. cfg defaults
    // to the webhook field (editingRule.plexLabelSync); the schedule /
    // QFA review block passes editingRule.plexSync. Defensive against
    // partially-filled state — returns "(none picked)" not a crash.
    _reviewPlexCfg(cfg) {
      if (cfg) return cfg;
      return this.editingRule && this.editingRule.plexLabelSync;
    },

    reviewPlexInstanceName(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      if (!cfg || !cfg.plexInstanceId) return '(none picked)';
      const pi = (this.plexInstances || []).find(p => p.id === cfg.plexInstanceId);
      return pi ? pi.name : cfg.plexInstanceId;
    },

    reviewPlexLibraryTitles(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      if (!cfg || !cfg.libraryKeys || cfg.libraryKeys.length === 0) return '(none picked)';
      const pi = (this.plexInstances || []).find(p => p.id === cfg.plexInstanceId);
      const libs = pi ? (pi.libraries || []) : [];
      const titles = cfg.libraryKeys.map(k => {
        const l = libs.find(x => x.key === k);
        return l ? l.title : k;
      });
      return titles.join(', ');
    },

    reviewPlexTagsSummary(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      if (!cfg || !cfg.labels || cfg.labels.length === 0) return '(none picked)';
      const display = cfg.labelDisplay || {};
      const parts = cfg.labels.map(lbl => {
        const override = (display[lbl] || '').trim();
        return override && override !== lbl ? `${lbl} → ${override}` : lbl;
      });
      return parts.join(', ');
    },

    reviewPlexTargetTypesLabel(cfgArg) {
      const cfg = this._reviewPlexCfg(cfgArg);
      const types = (cfg && cfg.targetTypes && cfg.targetTypes.length > 0) ? cfg.targetTypes : ['label'];
      const hasLabel = types.includes('label');
      const hasCollection = types.includes('collection');
      if (hasLabel && hasCollection) return 'Plex labels and collections';
      if (hasCollection) return 'Plex collections only';
      return 'Plex labels';
    },

    reviewBucketSummary(bucket, vocabSize) {
      if (!bucket || !bucket.enabled) return 'Disabled';
      const parts = ['Enabled'];
      const prefix = bucket.prefix && bucket.prefix.trim();
      parts.push(prefix ? 'prefix `' + prefix + '`' : 'bare values');
      const av = bucket.allowedValues || [];
      if (av.length === 0) {
        parts.push('all values allowed');
      } else if (vocabSize) {
        parts.push(av.length + ' of ' + vocabSize + ' values');
      } else {
        parts.push(av.length + ' value' + (av.length === 1 ? '' : 's') + ' selected');
      }
      return parts.join(' — ').replace(' — ', ' · ');
    },

    // Mode-label for human-readable rendering. Combined-mode also
    // returns the chain order joined by arrows.
    reviewModeLabel() {
      if (!this.editingRule) return '';
      const m = this.editingRule.mode;
      const map = {
        tag: 'Tag quality releases',
        discover: 'Discover release groups',
        recover: 'Recover missing release groups',
        audiotags: 'Tag Audio',
        videotags: 'Tag Video',
        dvdetail: 'Tag DV Details',
        combined: 'Combined chain',
      };
      const label = map[m] || m;
      if (m !== 'combined') return label;
      const cm = (this.editingRule.options.combinedModes || []).map(x => map[x] || x);
      return label + (cm.length ? ' — ' + cm.join(' → ') : ' — (no phases picked)');
    },

    // Schedule label for non-quickfix rules.
    reviewScheduleLabel() {
      if (!this.editingRule) return '';
      // manualOnly is the authoritative UI flag — cron carries a default
      // placeholder during the wizard session (so the picker has
      // something to show if the user toggles manualOnly off again).
      // Save-time persists cron='' for manual rules; both paths land
      // on the same answer here.
      if (this.editingRule.manualOnly) {
        return 'Manual run only (Run-now button on the Run mode card)';
      }
      const cron = (this.editingRule.cron || '').trim();
      return cron === '' ? 'Manual run only (Run-now button on the Run mode card)' : cron;
    },

    // Tracks which auto-tag sections will actually emit on this run
    // — used by the Review template to decide which subsections to
    // render. Same gating as ruleAffectsAudio / ruleAffectsVideo.
    ruleAutoTagsSummary() {
      if (!this.editingRule) return '(none)';
      const parts = [];
      const a = this.editingRule.audioTags;
      if (a && a.audio && a.audio.enabled) parts.push('audio');
      const v = this.editingRule.videoTags;
      if (v) {
        ['resolution', 'codec', 'hdr'].forEach(k => {
          if (v[k] && v[k].enabled) parts.push(k);
        });
      }
      const dd = this.editingRule.dvDetail;
      if (dd && dd.enabled) parts.push('dv-detail');
      return parts.length === 0 ? '(none)' : parts.join(', ');
    },

    // Toast queue. Each toast gets its own timer so a fast burst (e.g. multiple
    // group adds in a row) shows every message stacked top-down instead of the
    // first one being clobbered by the next.
    showToast(msg, type = '') {
      const id = ++this._toastSeq;
      this.toasts = [...this.toasts, { id, msg, type }];
      // 8 s default — long enough that a user looking elsewhere when
      // the toast pops up still gets to read it. Click the × on the
      // toast to dismiss early; the markup wires dismissToast(id) to
      // every toast regardless of type so the user is never trapped
      // waiting for a wall of text to expire.
      setTimeout(() => {
        this.toasts = this.toasts.filter(t => t.id !== id);
      }, 8000);
    },

    // confirmDialog — Promise<boolean> wrapper around the shared
    // confirm-modal. Drop-in replacement for window.confirm():
    //
    //   if (!await this.confirmDialog({ title: 'Delete?', message: '...' })) return;
    //
    // Options: { title, message, confirmText, cancelText, kind }.
    // 'kind' is 'default' | 'danger' | 'warning' — drives the
    // Confirm-button colour. Default cancelText is 'Cancel'.
    //
    // Returns the previous Promise's resolution as false if a
    // second confirmDialog call comes in while one is open
    // (defensive — shouldn't happen with proper UI gating).
    confirmDialog(opts) {
      // Resolve any in-flight prompt as Cancel before opening a new
      // one to avoid orphaned promises.
      if (this.confirmModal.open && this.confirmModal._resolve) {
        try { this.confirmModal._resolve(false); } catch (_) { /* noop */ }
      }
      return new Promise((resolve) => {
        this.confirmModal = {
          open:        true,
          title:       opts && opts.title ? String(opts.title) : 'Are you sure?',
          message:     opts && opts.message ? String(opts.message) : '',
          confirmText: opts && opts.confirmText ? String(opts.confirmText) : 'Confirm',
          cancelText:  opts && opts.cancelText ? String(opts.cancelText) : 'Cancel',
          kind:        opts && opts.kind ? String(opts.kind) : 'default',
          _resolve:    resolve,
        };
      });
    },

    // Helpers wired by the confirm-modal template.
    _resolveConfirmDialog(value) {
      const r = this.confirmModal._resolve;
      this.confirmModal = { open: false, title: '', message: '', confirmText: 'Confirm', cancelText: 'Cancel', kind: 'default', _resolve: null };
      if (r) r(value);
    },
    confirmDialogOk()     { this._resolveConfirmDialog(true); },
    confirmDialogCancel() { this._resolveConfirmDialog(false); },

    // Manual dismiss — wired to the × button on every toast row.
    // Filters by id so dismissing one toast doesn't disturb others
    // that happen to be showing in the stack.
    dismissToast(id) {
      this.toasts = this.toasts.filter(t => t.id !== id);
    },

    // --- Instances ---
    openInstanceModal(inst) {
      if (inst) {
        this.instForm = {
          id: inst.id, name: inst.name, type: inst.type,
          iconVariant: inst.iconVariant || 'standard',
          defaultQbitInstanceId: inst.defaultQbitInstanceId || '',
          url: inst.url, apiKey: inst.apiKey,
          // Deep-copy pathMappings so editor mutations don't bleed
          // into the live instances array before save. Empty/missing
          // → empty array so the editor renders cleanly.
          pathMappings: Array.isArray(inst.pathMappings)
            ? inst.pathMappings.map(m => ({ from: m.from || '', to: m.to || '' }))
            : [],
        };
      } else {
        this.instForm = {
          id: '', name: '', type: 'radarr', iconVariant: 'standard',
          defaultQbitInstanceId: '',
          url: '', apiKey: '', pathMappings: [],
        };
      }
      this.instFormError = '';
      this.instFormTestResult = '';
      this.instFormTestedOK = false;
      this.instFormTestedKey = '';
      this.instFormTestedVersion = '';
      this.showInstModal = true;
    },

    addInstancePathMapping() {
      this.instForm.pathMappings = [...(this.instForm.pathMappings || []), { from: '', to: '' }];
      this.instFormPathMappingsOpen = true;
    },
    removeInstancePathMapping(idx) {
      this.instForm.pathMappings = (this.instForm.pathMappings || []).filter((_, i) => i !== idx);
    },

    async saveInstance() {
      this.instFormError = '';
      // Filter out empty rows from the path-mapping editor before save
      // — saving a half-filled mapping would silently fail-open at
      // translation time. Both sides must be non-empty to count.
      const mappings = (this.instForm.pathMappings || [])
        .map(m => ({ from: (m.from || '').trim(), to: (m.to || '').trim() }))
        .filter(m => m.from !== '' && m.to !== '');
      const body = {
        name: this.instForm.name, type: this.instForm.type,
        iconVariant: this.instForm.iconVariant,
        defaultQbitInstanceId: this.instForm.defaultQbitInstanceId || '',
        url: this.instForm.url, apiKey: this.instForm.apiKey,
        pathMappings: mappings,
      };
      // Preserve Test result across save: if url+apiKey match the tested combo,
      // reuse the cached version from the Test call so status shows 'connected' immediately.
      const curKey = this.instForm.url + '|' + this.instForm.apiKey;
      const preserveVersion = (this.instFormTestedOK && curKey === this.instFormTestedKey)
        ? this.instFormTestedVersion : null;
      try {
        const url = this.instForm.id ? `/api/instances/${this.instForm.id}` : '/api/instances';
        const method = this.instForm.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        if (!r.ok) { const d = await r.json(); throw new Error(d.error || 'HTTP ' + r.status); }
        const saved = this.instForm.id ? null : await r.json();
        this.showInstModal = false;
        await this.loadConfig();
        // Carry over the Test result if it still applies
        if (preserveVersion != null) {
          const id = this.instForm.id || saved?.id;
          if (id) {
            this.instStatus[id] = 'connected';
            this.instVersion[id] = preserveVersion;
          }
        }
        this.refreshAllStatus();
        this.showToast(this.instForm.id ? 'Instance updated' : 'Instance added', 'success');
      } catch (e) {
        this.instFormError = e.message;
      }
    },

    async deleteInstance(inst) {
      if (!await this.confirmDialog({
        title:       `Delete "${inst.name}"?`,
        message:     'Removes the Sonarr/Radarr connection from resolvarr. Webhooks, rules, and schedules tied to this instance will lose their link.',
        confirmText: 'Delete',
        kind:        'danger',
      })) return;
      try {
        const r = await this.apiFetch(`/api/instances/${inst.id}`, { method: 'DELETE' });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        await this.loadConfig();
        this.showToast('Instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

    async testInstance(inst, silent = false) {
      if (!silent) this.instStatus[inst.id] = 'testing';
      this.instError[inst.id] = '';
      try {
        const r = await this.apiFetch(`/api/instances/${inst.id}/test`, { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.instStatus[inst.id] = 'connected';
        this.instVersion[inst.id] = d.version;
      } catch (e) {
        this.instStatus[inst.id] = 'failed';
        this.instError[inst.id] = e.message;
      }
    },

    async testInstanceForm() {
      this.instFormTesting = true;
      this.instFormTestResult = '';
      this.instFormTestedOK = false;
      this.instFormTestedVersion = '';
      try {
        const body = {
          name: this.instForm.name || 'test', type: this.instForm.type,
          iconVariant: this.instForm.iconVariant,
          url: this.instForm.url, apiKey: this.instForm.apiKey,
        };
        const r = await this.apiFetch('/api/instances/test', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.instFormTestResult = 'OK — v' + d.version;
        this.instFormTestedOK = true;
        this.instFormTestedKey = this.instForm.url + '|' + this.instForm.apiKey;
        this.instFormTestedVersion = d.version;
      } catch (e) {
        this.instFormTestResult = 'Failed: ' + e.message;
      } finally {
        this.instFormTesting = false;
      }
    },

    // ============ Security panel ============

    // Hydrate the security form from the live config + auth-status
    // endpoint. authStatus surfaces trustedNetworksLocked /
    // trustedProxiesLocked so the inputs disable when env vars override.
    async loadSecurityPanel() {
      this.securitySaveMsg = '';
      try {
        const r = await this.apiFetch('/api/auth/status');
        if (r.ok) {
          const d = await r.json();
          // Backend uses snake_case for these keys (auth_handlers.go).
          this.authStatus = {
            trustedNetworksLocked: !!(d.trusted_networks_locked || d.trustedNetworksLocked),
            trustedProxiesLocked:  !!(d.trusted_proxies_locked  || d.trustedProxiesLocked),
          };
        }
      } catch {}
      // Hydrate form from current config (already loaded into this.config
      // by loadConfig on init).
      const c = this.config || {};
      this.securityForm = {
        authentication:         c.authentication || 'forms',
        authenticationRequired: c.authenticationRequired || 'disabled_for_local_addresses',
        trustedNetworks:        c.trustedNetworks || '',
        trustedProxies:         c.trustedProxies || '',
        sessionTtlDays:         c.sessionTtlDays || 30,
      };
      this.securityFormDirty = false;
      // Lazy-fetch the API key — server returns it in plaintext (auth-
      // gated endpoint), we mask it client-side until "Show" is clicked.
      this.fetchSecurityApiKey();
    },

    async fetchSecurityApiKey() {
      try {
        const r = await this.apiFetch('/api/auth/api-key');
        if (!r.ok) return;
        const d = await r.json();
        // Backend returns api_key (snake_case in auth_handlers.go).
        this.securityApiKey = d.api_key || d.apiKey || '';
      } catch {}
    },

    async copySecurityApiKey() {
      if (!this.securityApiKey) return;
      const ok = await this.copyToClipboard(this.securityApiKey);
      if (ok) {
        this.securityApiKeyCopied = true;
        setTimeout(() => { this.securityApiKeyCopied = false; }, 2000);
      } else {
        this.showToast('Copy failed — your browser blocked clipboard access', 'error');
      }
    },

    async regenerateApiKey() {
      this.securityRegenerating = true;
      try {
        const r = await this.apiFetch('/api/auth/regenerate-api-key', { method: 'POST' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        const d = await r.json();
        this.securityApiKey = d.api_key || d.apiKey || '';
        this.securityApiKeyVisible = true;
        this.securityRegenConfirm = false;
        this.showToast('API key regenerated — old key invalid immediately', 'success');
      } catch (e) {
        this.showToast('Regenerate failed: ' + e.message, 'error');
      } finally {
        this.securityRegenerating = false;
      }
    },

    async saveSecurityConfig() {
      this.securitySaving = true;
      this.securitySaveMsg = '';
      try {
        const r = await this.apiFetch('/api/config/auth', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.securityForm),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Mirror saved values back into the live config object so other
        // surfaces (the loadConfig cache) stay consistent.
        Object.assign(this.config || {}, this.securityForm);
        this.securityFormDirty = false;
        this.securitySaveOk = true;
        this.securitySaveMsg = 'Saved.';
        setTimeout(() => { this.securitySaveMsg = ''; }, 4000);
      } catch (e) {
        this.securitySaveOk = false;
        this.securitySaveMsg = e.message || 'Save failed';
      } finally {
        this.securitySaving = false;
      }
    },

    async changePassword() {
      // Client-side belt-and-braces — server validates too.
      if (this.pwChange.next !== this.pwChange.confirm) {
        this.pwChangeOk = false;
        this.pwChangeMsg = 'New password and confirmation do not match';
        return;
      }
      this.pwChangeSaving = true;
      this.pwChangeMsg = '';
      try {
        const r = await this.apiFetch('/api/auth/change-password', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          // Backend uses snake_case (auth_handlers.go:347-349) — must
          // send current_password / new_password / new_password_confirm
          // exactly so the JSON decoder picks them up.
          body: JSON.stringify({
            current_password:     this.pwChange.current,
            new_password:         this.pwChange.next,
            new_password_confirm: this.pwChange.confirm,
          }),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.pwChangeOk = true;
        this.pwChangeMsg = 'Password changed. Other sessions signed out.';
        this.pwChange = { current: '', next: '', confirm: '' };
        setTimeout(() => { this.pwChangeMsg = ''; }, 6000);
      } catch (e) {
        this.pwChangeOk = false;
        this.pwChangeMsg = e.message || 'Change failed';
      } finally {
        this.pwChangeSaving = false;
      }
    },

    // POST /logout — invalidates this browser's session cookie and
    // redirects to /login. Other sessions stay active. Best-effort:
    // even if the POST fails (network blip), we still nuke the cookie
    // client-side via redirect so the user isn't stuck on a stale page.
    async logout() {
      try {
        await this.apiFetch('/logout', { method: 'POST' });
      } catch (_) {
        // Ignore — we redirect regardless.
      }
      window.location.href = '/login';
    },

    // ============ Notification agents (multi-provider) ============

    // Load the agents list from the server, masking credentials applied
    // server-side. Called on settings section open, after a CRUD action,
    // and after the modal closes.
    async loadAgents() {
      this.agentsLoading = true;
      this.agentsLoadError = '';
      try {
        const r = await this.apiFetch('/api/notifications/agents');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.agents = (await r.json()) || [];
      } catch (e) {
        this.agentsLoadError = e.message;
      } finally {
        this.agentsLoading = false;
      }
    },

    // Provider icon path. Most types ship an SVG; Gotify and Apprise
    // ship a PNG bitmap (no clean SVG available upstream).
    agentIconSrc(type) {
      if (type === 'gotify' || type === 'apprise') {
        return `/icons/${type}.png`;
      }
      return `/icons/${type}.svg`;
    },

    // Open the modal — empty form for Add, prefilled for Edit. Editing
    // sets agentModal.testPassed=true so trivial edits (renaming, event-
    // toggle) save without re-testing; touching any credential input
    // resets it to false via the @input handler.
    openAgentModal(a) {
      // Event defaults: schedule events on (legacy default), webhook
      // events off. Existing agent's Events object is round-tripped
      // wholesale via Object.assign so future flag additions don't
      // get silently zeroed on save.
      const eventDefaults = {
        onScheduleSuccess: true, onScheduleFailure: true,
        onImport: false, onGrab: false, onFileDelete: false,
      };
      if (a) {
        this.agentModal = {
          id: a.id,
          name: a.name || '',
          type: a.type,
          enabled: !!a.enabled,
          events: Object.assign({}, eventDefaults, a.events || {}),
          functions: Array.isArray(a.functions) ? a.functions.slice() : [],
          config: Object.assign({}, a.config || {}),
          busy: false,
          testing: false,
          testPassed: true, // existing agent — was tested before save
          testResult: '',
          error: '',
        };
      } else {
        this.agentModal = {
          id: '',
          name: '',
          type: 'discord',
          enabled: true,
          events: Object.assign({}, eventDefaults),
          functions: [],
          config: {},
          busy: false,
          testing: false,
          testPassed: false,
          testResult: '',
          error: '',
        };
      }
      this.showAgentModal = true;
    },

    // Functions checkbox handler — adds/removes the function ID from
    // the agentModal.functions array. Empty array (= no filter) is
    // the default; non-empty acts as a whitelist.
    toggleAgentFunction(id, checked) {
      if (checked) {
        if (!this.agentModal.functions.includes(id)) {
          this.agentModal.functions.push(id);
        }
      } else {
        this.agentModal.functions = this.agentModal.functions.filter(f => f !== id);
      }
    },

    closeAgentModal() {
      if (this.agentModal.busy) return;
      this.showAgentModal = false;
    },

    onAgentTypeChange() {
      // Switching type wipes any provider-specific creds the user typed
      // for the old type — they don't apply to the new one. Reset
      // testPassed too since fresh config needs verification.
      this.agentModal.config = {};
      this.agentModal.testPassed = false;
      this.agentModal.testResult = '';
      this.agentModal.error = '';
    },

    // Returns true when the modal has enough fields populated to even
    // attempt a Test. Discord needs a webhook URL; Gotify needs URL +
    // token; etc. Empty fields → button greyed out.
    agentTypeFilled() {
      const c = this.agentModal.config || {};
      switch (this.agentModal.type) {
        case 'discord':  return !!(c.discordWebhook || '').trim();
        case 'gotify':   return !!(c.gotifyUrl || '').trim() && !!(c.gotifyToken || '').trim();
        case 'ntfy':     return !!(c.ntfyUrl || '').trim() && !!(c.ntfyTopic || '').trim();
        case 'pushover': return !!(c.pushoverUserKey || '').trim() && !!(c.pushoverAppToken || '').trim();
        case 'apprise':  return !!(c.appriseUrl || '').trim() && (c.appriseUrls || []).some(u => (u || '').trim() !== '');
        default:         return false;
      }
    },

    // Save is enabled when (a) the form has a name, (b) credentials are
    // populated for the active type, and (c) a successful test has run
    // since the last credential edit (testPassed). Edit mode arrives
    // with testPassed=true so renames and event-toggle changes save
    // without re-testing; credential input resets it.
    canSaveAgent() {
      if (!(this.agentModal.name || '').trim()) return false;
      if (!this.agentTypeFilled()) return false;
      return this.agentModal.testPassed;
    },

    // Build the body sent to /api/notifications/agents{,/test}.
    // Events object is round-tripped fully — openAgentModal hydrates
    // ALL event keys from the existing agent (including webhook events
    // not yet exposed in the modal: onImport / onGrab / onUpgrade /
    // onFileDelete). This Object.assign preserves them so a save round-
    // trip doesn't wipe webhook flags set by future UI surfaces.
    agentRequestBody() {
      return {
        id:        this.agentModal.id,
        name:      (this.agentModal.name || '').trim(),
        type:      this.agentModal.type,
        enabled:   !!this.agentModal.enabled,
        events:    Object.assign({}, this.agentModal.events),
        functions: (this.agentModal.functions || []).slice(),
        config:    Object.assign({}, this.agentModal.config),
      };
    },

    // Inline-test using the modal's current values without saving.
    // POST /api/notifications/agents/test. Server receives the in-form
    // credentials (or the masked placeholder + ID for an edit, which
    // it preserves to the stored value).
    async testInlineAgent() {
      this.agentModal.testing = true;
      this.agentModal.error = '';
      this.agentModal.testResult = '';
      this.agentModal.testPassed = false;
      try {
        const r = await this.apiFetch('/api/notifications/agents/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.agentRequestBody()),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        const results = (d.results || []);
        const failed = results.filter(x => x.status !== 'ok');
        if (results.length === 0) {
          // Provider returned no channels — treat as failure so a buggy
          // provider can't slip past the Save guard with an empty pass.
          this.agentModal.testPassed = false;
          this.agentModal.testResult = 'Test returned no results.';
        } else if (failed.length === 0) {
          this.agentModal.testPassed = true;
          this.agentModal.testResult = results.length === 1
            ? 'Sent successfully.'
            : `${results.length} channel(s) verified.`;
        } else {
          this.agentModal.testPassed = false;
          this.agentModal.testResult = failed.map(f => (f.label ? f.label + ': ' : '') + (f.error || 'failed')).join(' · ');
        }
      } catch (e) {
        this.agentModal.testResult = e.message || 'Test failed';
      } finally {
        this.agentModal.testing = false;
      }
    },

    // Save creates (POST) when no ID, updates (PUT) when present.
    // After success, refreshes the list and closes the modal.
    async saveAgent() {
      this.agentModal.busy = true;
      this.agentModal.error = '';
      try {
        const url = this.agentModal.id
          ? '/api/notifications/agents/' + encodeURIComponent(this.agentModal.id)
          : '/api/notifications/agents';
        const method = this.agentModal.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(url, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.agentRequestBody()),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadAgents();
        this.showToast(this.agentModal.id ? 'Agent updated' : 'Agent added', 'success');
        this.showAgentModal = false;
      } catch (e) {
        this.agentModal.error = e.message || 'Save failed';
      } finally {
        this.agentModal.busy = false;
      }
    },

    // Toggle the Enabled flag inline from the agents-list row. Sends a
    // PUT with the existing config (server preserves masked creds).
    async toggleAgentEnabled(a) {
      if (this.agentBusyId) return;
      this.agentBusyId = a.id;
      try {
        const r = await this.apiFetch('/api/notifications/agents/' + encodeURIComponent(a.id), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: a.id, name: a.name, type: a.type,
            enabled: !a.enabled,
            events: a.events || {},
            config: a.config || {},
          }),
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadAgents();
      } catch (e) {
        this.showToast('Toggle failed: ' + e.message, 'error');
      } finally {
        this.agentBusyId = null;
      }
    },

    // Test a saved agent inline from the list. Hits POST /agents/{id}/test
    // which uses the stored credentials (no need to re-send). Result lands
    // in agentTestStatus[agent.id] for the inline status indicator next
    // to the Test button — green ✓ / red ✗ / per-channel breakdown.
    async testSavedAgent(a) {
      this.agentTestStatus = Object.assign({}, this.agentTestStatus, {
        [a.id]: { testing: true, results: [] },
      });
      try {
        const r = await this.apiFetch('/api/notifications/agents/' + encodeURIComponent(a.id) + '/test', { method: 'POST' });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.agentTestStatus = Object.assign({}, this.agentTestStatus, {
          [a.id]: { testing: false, results: d.results || [] },
        });
      } catch (e) {
        this.agentTestStatus = Object.assign({}, this.agentTestStatus, {
          [a.id]: { testing: false, results: [{ label: 'test', status: 'error', error: e.message || 'failed' }] },
        });
      }
    },

    openDeleteAgent(a) {
      this.deleteAgentTarget = a;
    },

    async confirmDeleteAgent() {
      if (!this.deleteAgentTarget) return;
      this.deleteAgentBusy = true;
      const deletedId = this.deleteAgentTarget.id;
      try {
        const r = await this.apiFetch('/api/notifications/agents/' + encodeURIComponent(deletedId), { method: 'DELETE' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Drop the per-row test status entry so it doesn't linger
        // forever in agentTestStatus after the agent is gone.
        if (this.agentTestStatus[deletedId]) {
          const next = Object.assign({}, this.agentTestStatus);
          delete next[deletedId];
          this.agentTestStatus = next;
        }
        await this.loadAgents();
        this.showToast('Agent deleted', 'success');
        this.deleteAgentTarget = null;
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteAgentBusy = false;
      }
    },

    // --- Tags ---
    currentInstanceType() {
      const inst = this.instances.find(i => i.id === this.tagsInstanceId);
      return inst ? inst.type : '';
    },

    // Filtered instance list for the Tag inventory dropdown — only the
    // active app-type. Sorted by name for stable order.
    tagsAvailableInstances() {
      return this.instances
        .filter(i => i.type === this.tagsAppType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // True if at least one instance of the given app-type is configured.
    // Drives the disabled+tooltip state on the App-type pills.
    tagsAppTypeAvailable(type) {
      return this.instances.some(i => i.type === type);
    },

    // Switch the active app-type. Persists to localStorage, re-picks the
    // first matching instance (or clears if none), and reloads tags.
    setTagsAppType(type) {
      if (type !== 'radarr' && type !== 'sonarr') return;
      if (!this.tagsAppTypeAvailable(type)) return;
      if (this.tagsAppType === type) return;
      this.tagsAppType = type;
      localStorage.setItem('resolvarr-tags-app-type', type);
      // Reset selection state — different instance pool means stale selections.
      this.tagsSelected = new Set();
      this.compareOpen = false;
      this.compareResults = null;
      this.compareCrossInstanceTarget = '';
      // Pick first matching instance, or clear if no instances of this type.
      const first = this.tagsAvailableInstances()[0];
      if (first) {
        this.tagsInstanceId = first.id;
        localStorage.setItem('resolvarr-tags-instance', first.id);
        this.loadTags();
      } else {
        this.tagsInstanceId = '';
        localStorage.removeItem('resolvarr-tags-instance');
        this.tags = [];
      }
      // App-type switch invalidates the search cache (tags + items belong
      // to the previous instance) and clears the search field.
      this.resetTagSearchState({ clearCache: true });
      this.pushNav();
    },

    // Resets the tag-search UI to its empty-query state. Used both on
    // app-type switch (cache invalid) and on plain instance-dropdown
    // switch within the same app-type (cache for the new instance may
    // not exist yet, but query/expanded state from the previous instance
    // shouldn't carry over). Pass clearCache:true to also wipe the
    // payload cache and bump the version counter.
    resetTagSearchState(opts = {}) {
      this.tagSearchQuery = '';
      this.tagSearchExpanded = {};
      this.tagSearchError = '';
      this.tagSearchParseError = '';
      if (opts.clearCache) {
        this.tagSearchCache = {};
        this.tagSearchCacheVersion++;
      }
      this._tagSearchResultsKey = '';
      this._tagSearchResultsValue = null;
    },



    // ---- Rule editor (self-contained schedule rules) ----
    //
    // Single wizard modal covering three flows: Create (saved
    // schedule rule), Edit (tabbed-edit of an existing rule), and
    // Quickfix (one-shot dispatcher that runs the chain without
    // persisting). All three share the same editingRule shape and
    // section components — flows differ only in which steps render
    // and whether Save persists a rule (Create/Edit) or fires a chain
    // via runQuickFixChain (Quickfix).

    // Rule config snapshot/default builders (defaultRule* / snapshotGlobal*
    // / snapshotDefault*) moved to js/modules/config.js (appConfigSnapshots),
    // composed via { ...appConfigSnapshots() } in app(). Stage 4.

    // ---- Open / close ----
    openCreateRuleWizard() {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Lock the wizard to the Arr-type the user picked in the Library
      // scan header. The instance dropdown + mode catalog both filter
      // through this. If the user is on a tab without an app picker we
      // fall back to whatever scanAppType currently is.
      const wizardAppType = this.scanAppType || 'radarr';
      const poolForType = this.instances.filter(i => i.type === wizardAppType);
      if (poolForType.length === 0) {
        this.showToast('No ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') + ' instance configured — add one in Settings → Instances', 'error');
        return;
      }
      // Seed precedence: last-used remembered (per-Arr-type key so
      // Radarr Create-rule and Sonarr Create-rule remember separately) →
      // current scanInstanceId → first-of-type.
      const memKey = 'create-rule-' + wizardAppType;
      const remembered = this.recallWizardInstance(memKey, poolForType);
      const scanPicked = poolForType.find(i => i.id === this.scanInstanceId);
      const inst = remembered || (scanPicked || poolForType[0]).id;
      // mode='combined' for every rule. Single-mode runs are expressed
      // by ticking one chain step (combinedModes carries the user's
      // pick). Sonarr-context seeds 'recover' since that's the only
      // currently-supported phase; Radarr starts with no boxes ticked
      // (user must pick at least one before Save).
      const seedCombined = wizardAppType === 'sonarr' ? ['recover'] : [];
      this.editingRule = {
        id: '', name: '', mode: 'combined', instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        manualOnly: false,
        enabled: true,
        options: {
          runMode: 'apply',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: seedCombined,
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          // Per-bucket instance target. 'primary' (default), 'secondary',
          // or 'both'. Audio/Video/DV-tags each pick independently. The
          // chain runs primary phases first (discover/recover/tag), then
          // a sub-chain on the primary instance with whichever of
          // audio/video/dv have target=primary or 'both', then a sub-chain
          // on the secondary instance with whichever have target=secondary
          // or 'both'. Token allow-lists are universal — same per-rule
          // settings get applied to whichever instance(s) run.
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        qbitSe:           this.snapshotDefaultQbitSe(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      this.ruleEditor = { open: true, isCreate: true, isQuickFix: false, step: 0, activeTab: 'basics', appType: wizardAppType, busy: false, error: '', cronError: '', nextFires: [], fixedAction: '' };
      this.computeRuleEditorNextFires();
    },

    // Quick fix-all entry point. Same wizard, but flagged as one-shot:
    // no Name field, no cron picker, no enabled toggle, Save button
    // becomes "Run now" and dispatches the chain immediately without
    // persisting anything. Pre-fills from current globals (same seed
    // as Create) so the user lands on Review with sensible defaults
    // and can fire-fast by clicking through Next-Next-Next-RunNow.
    openQuickFixWizard() {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Quick fix-all locks to whichever Arr-type the user picked in
      // the Library scan header (scanAppType). Radarr seed includes
      // the full discover→recover→tag head chain; Sonarr seed picks
      // only the phases backend supports today (recover + audio +
      // video — tag/discover land with M-Sonarr Phase 2).
      const wizardAppType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      const poolForType = this.instances.filter(i => i.type === wizardAppType);
      if (poolForType.length === 0) {
        this.showToast(
          'Quick fix-all needs a ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') +
          ' instance — add one in Settings → Instances', 'error');
        return;
      }
      // QFA carries its own state in localStorage (resolvarr-qfa-state-<arrtype>)
      // — independent of globals, independent of per-action wizard
      // defaults. User's last-fired QFA configuration (chain checks,
      // bucket configs, sync targets, instance, run-mode) becomes the
      // pre-fill for next open. Falls back to globals on first open
      // (cleared cache, new install).
      const restored = this._loadQfaState(wizardAppType);
      // Validate restored instance is still in pool. If the user
      // deleted their Radarr and the localStorage points to a dead
      // ID, we'd seed a wizard that can't fire.
      let inst;
      if (restored && restored.instanceId && poolForType.some(i => i.id === restored.instanceId)) {
        inst = restored.instanceId;
      } else {
        const scanPicked = poolForType.find(i => i.id === this.scanInstanceId);
        inst = (scanPicked || poolForType[0]).id;
      }

      // Default to Preview for safety — the wizard is interactive
      // and one-shot, so a Preview-then-Apply flow is the right
      // pattern. The result panel's "Apply now" button promotes
      // a clean preview to apply without re-walking the wizard.
      const defaults = {
        id: '',
        name: 'Quick fix-all',
        mode: 'combined',
        instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        enabled: true,
        options: {
          runMode: 'preview',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: wizardAppType === 'sonarr' ? ['recover', 'audiotags', 'videotags', 'missingepisodes'] : ['discover', 'recover', 'tag'],
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          // Per-bucket instance target. 'primary' (default), 'secondary',
          // or 'both'. Audio/Video/DV-tags each pick independently.
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        qbitSe:           this.snapshotDefaultQbitSe(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      // Merge restored state over defaults. Bucket snapshots use
      // recursive per-field merge so a localStorage payload written
      // before a new bucket field landed (e.g. SonarrAggregation in
      // M-Sonarr Phase A) doesn't leak undefined past the backend's
      // overlay validator on next Apply. Top-level fields use
      // shallow-merge — restored wins on every field that's
      // actually present.
      this.editingRule = restored
        ? {
            ...defaults,
            ...restored,
            instanceId: inst, // post-validation pick; restored may have a stale ID
            options: { ...defaults.options, ...(restored.options || {}) },
            filters:   this._mergeBucketSnapshot(restored.filters,   defaults.filters),
            audioTags: this._mergeBucketSnapshot(restored.audioTags, defaults.audioTags),
            videoTags: this._mergeBucketSnapshot(restored.videoTags, defaults.videoTags),
            dvDetail:  this._mergeBucketSnapshot(restored.dvDetail,  defaults.dvDetail),
            missingEpisodes: this._mergeBucketSnapshot(restored.missingEpisodes, defaults.missingEpisodes),
            plexSync: (restored.plexSync && typeof restored.plexSync === 'object') ? restored.plexSync : this.snapshotDefaultPlexSync(),
            tbaRefresh: (restored.tbaRefresh && typeof restored.tbaRefresh === 'object') ? restored.tbaRefresh : this.snapshotDefaultTbaRefresh(),
            qbitSe: (restored.qbitSe && typeof restored.qbitSe === 'object') ? restored.qbitSe : this.snapshotDefaultQbitSe(),
            releaseGroupIds: Array.isArray(restored.releaseGroupIds)
              ? restored.releaseGroupIds
              : defaults.releaseGroupIds,
          }
        : defaults;
      this.ruleEditor = { open: true, isCreate: true, isQuickFix: true, step: 0, activeTab: 'basics', appType: wizardAppType, busy: false, error: '', cronError: '', nextFires: [], fixedAction: '' };
    },

    // ===== QFA localStorage state =====
    //
    // QFA stores its full editingRule snapshot per Arr-type so the
    // wizard remembers the user's last chain configuration without
    // mutating globals (which per-action wizards own). Key shape:
    // 'resolvarr-qfa-state-radarr' / '-sonarr'. Per-Arr-type because
    // Radarr QFA and Sonarr QFA have different valid chain phases.

    _qfaStateKey(arrType) {
      return 'resolvarr-qfa-state-' + (arrType === 'sonarr' ? 'sonarr' : 'radarr');
    },
    _saveQfaState(arrType, rule) {
      if (!rule) return;
      try {
        // Strip volatile fields — `id` empty, `name` auto-generated.
        // Keep only what should re-hydrate next open. Deep-clone via
        // JSON.stringify-then-parse so a later mutation of rule.options
        // (e.g. an analytics post-run hook that mucks with combinedModes)
        // can't bleed into the persisted snapshot.
        const persist = {
          mode: rule.mode,
          instanceId: rule.instanceId,
          options: rule.options,
          filters: rule.filters,
          audioTags: rule.audioTags,
          videoTags: rule.videoTags,
          dvDetail: rule.dvDetail,
          missingEpisodes: rule.missingEpisodes,
          plexSync: rule.plexSync,
          tbaRefresh: rule.tbaRefresh,
          qbitSe: rule.qbitSe,
          releaseGroupIds: rule.releaseGroupIds || [],
        };
        localStorage.setItem(this._qfaStateKey(arrType), JSON.stringify(persist));
      } catch (e) { /* ignore — Safari private mode et al */ }
    },

    // Per-field merge for QFA-state hydration. Old localStorage payloads
    // written before a new bucket field landed (e.g. SonarrAggregation
    // added in M-Sonarr Phase A) shouldn't leak `undefined` past the
    // backend's validator on next Apply. Recursively layer restored
    // values over defaults so missing fields fall back to fresh defaults.
    // ===== Per-action wizard: unified instance + "both" picker =====
    //
    // For Tag Audio / Video / DV Details runs, "primary" has no
    // semantic meaning — each instance reads its own mediaInfo
    // independently, there's no TmdbID-mirror like tag-release-groups
    // does. So the Step-1 "Primary instance" picker and the Step-2
    // "Run X on" target picker are redundant pairs encoding the same
    // intent twice.
    //
    // Per-action wizards collapse them into ONE dropdown on Step 1
    // listing instances by name + a "Both instances" option (when
    // 2+ same-type instances exist). State translation:
    //   value = '<inst-id>'  →  instanceId = <inst-id>, target = 'primary'
    //   value = '__both__'   →  instanceId = first-of-type, target = 'both'
    // The chain runner already handles target='both' by running on
    // primary (instanceId) + secondary (auto-derived first-other-of-type),
    // so keeping instanceId as a real ID + target='both' just works.

    _perActionTargetField() {
      const fa = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
      if (fa === 'audiotags') return 'audioTagsTarget';
      if (fa === 'videotags') return 'videoTagsTarget';
      if (fa === 'dvdetail')  return 'dvDetailTarget';
      if (fa === 'recover')   return 'recoverTarget';
      return '';
    },
    // True when the current per-action wizard supports the unified
    // primary/secondary/both picker. Auto-tag phases (audiotags /
    // videotags / dvdetail) walk per-instance media independently —
    // primary vs secondary has no semantic meaning. Recover joined
    // 2026-05-09 because each instance has its own movie/episode files
    // with their own missing-releaseGroup history; "primary only" was
    // an artificial restriction. Used to gate the unified-picker markup
    // vs the legacy primary-instance-only picker on Basics step.
    isPerActionAutoTag() {
      const fa = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
      return fa === 'audiotags' || fa === 'videotags' || fa === 'dvdetail' || fa === 'recover';
    },
    perActionInstanceSelectorValue() {
      if (!this.editingRule || !this.editingRule.options) return '';
      const tf = this._perActionTargetField();
      if (!tf) return this.editingRule.instanceId || '';
      if (this.editingRule.options[tf] === 'both') return '__both__';
      return this.editingRule.instanceId || '';
    },
    setPerActionInstance(value) {
      if (!this.editingRule || !this.editingRule.options) return;
      const tf = this._perActionTargetField();
      if (!tf) {
        this.editingRule.instanceId = value;
        return;
      }
      if (value === '__both__') {
        // Anchor instanceId to the first available instance of the
        // wizard's app type. The chain runner picks the secondary
        // automatically (first other-of-same-type).
        const pool = this.ruleEditorInstancesAvailable();
        if (pool.length > 0) this.editingRule.instanceId = pool[0].id;
        this.editingRule.options[tf] = 'both';
      } else {
        this.editingRule.instanceId = value;
        this.editingRule.options[tf] = 'primary';
      }
    },

    _mergeBucketSnapshot(restored, fallback) {
      if (!restored || typeof restored !== 'object') return fallback;
      if (!fallback || typeof fallback !== 'object') return restored;
      const out = { ...fallback };
      for (const k of Object.keys(restored)) {
        const rv = restored[k];
        const fv = fallback[k];
        if (rv && typeof rv === 'object' && !Array.isArray(rv) &&
            fv && typeof fv === 'object' && !Array.isArray(fv)) {
          out[k] = this._mergeBucketSnapshot(rv, fv);
        } else if (rv !== undefined) {
          out[k] = rv;
        }
      }
      return out;
    },
    _loadQfaState(arrType) {
      try {
        const s = localStorage.getItem(this._qfaStateKey(arrType));
        if (!s) return null;
        const parsed = JSON.parse(s);
        return (parsed && typeof parsed === 'object') ? parsed : null;
      } catch (e) { return null; }
    },

    // ===== Per-action standalone wizards =====
    //
    // openSpecificActionWizard reuses the QFA wizard infrastructure
    // but pre-seeds combinedModes to a single action and sets a
    // fixedAction flag so the Basics step's chain-checkboxes
    // section can hide (the user came here to run ONE thing — not
    // the whole "fix everything" chain). Wizard hydrates from
    // globals like QFA does, lets the user tweak per-run, fires
    // via runQuickFixChain on the Review step's Run button.
    //
    // Action keys: 'audiotags' | 'videotags' | 'dvdetail' | 'recover'.
    // Cleanup is handled by a separate small wizard (cleanup isn't
    // a combinedModes phase today).
    openSpecificActionWizard(action) {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Recover, Audio, Video work for both Arr types. DV detail is
      // Radarr-only today. Audio/Video on Sonarr work via the M-Sonarr
      // path. Discover/Tag remain Sonarr-deferred.
      const wizardAppType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      if (action === 'dvdetail' && wizardAppType !== 'radarr') {
        this.showToast('Tag DV Details is Radarr-only', 'error');
        return;
      }
      const pool = this.instances.filter(i => i.type === wizardAppType);
      // Seed precedence:
      //   1. last-used remembered instance for this action (if still in pool)
      //   2. current scanInstanceId (when matches type — pre-removal of
      //      header dropdown this was the legacy default; kept as a
      //      sensible fallback for users who just clicked from a tab)
      //   3. first-of-type
      const remembered = this.recallWizardInstance(action, pool);
      const seedInst = (remembered && pool.find(i => i.id === remembered))
                    || pool.find(i => i.id === this.scanInstanceId)
                    || pool[0];
      if (!seedInst) {
        this.showToast(
          'Need a ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') +
          ' instance — add one in Settings → Instances', 'error');
        return;
      }
      const inst = seedInst.id;
      const titleByAction = {
        audiotags:       'Tag Audio',
        videotags:       'Tag Video',
        dvdetail:        'Tag DV Details',
        recover:         'Run Recover',
        missingepisodes: 'Find missing episodes',
      };
      this.editingRule = {
        id: '',
        name: titleByAction[action] || 'Run',
        mode: 'combined',
        instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        enabled: true,
        options: {
          runMode: 'preview',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: [action],
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        qbitSe:           this.snapshotDefaultQbitSe(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      // Overlay the SCAN-LOCAL remembered config for this action (set by
      // _savePerActionState on the last run) over the global-seeded
      // defaults, so the wizard pre-fills with the user's last per-action
      // choices WITHOUT those choices ever having touched the global. Falls
      // back to the global defaults above when there's no remembered state.
      const rememberedCfg = this._loadPerActionState(action, wizardAppType);
      if (rememberedCfg) {
        if (rememberedCfg.audioTags) this.editingRule.audioTags = this._mergeBucketSnapshot(rememberedCfg.audioTags, this.editingRule.audioTags);
        if (rememberedCfg.videoTags) this.editingRule.videoTags = this._mergeBucketSnapshot(rememberedCfg.videoTags, this.editingRule.videoTags);
        if (rememberedCfg.dvDetail)  this.editingRule.dvDetail  = this._mergeBucketSnapshot(rememberedCfg.dvDetail,  this.editingRule.dvDetail);
      }
      this.ruleEditor = {
        open: true, isCreate: true, isQuickFix: true,
        step: 0, activeTab: 'basics', appType: wizardAppType,
        busy: false, error: '', cronError: '', nextFires: [],
        // fixedAction tells the Basics step to hide its chain-
        // checkboxes section — the user came here to run a single
        // action, not configure a chain. They can still hit Back
        // from later steps to tweak instance / run-mode.
        fixedAction: action,
      };
    },

    // Convenience entry points the sub-tab Run buttons call.
    openAudioWizard()   { this.openSpecificActionWizard('audiotags'); },
    openVideoWizard()   { this.openSpecificActionWizard('videotags'); },
    openDvWizard()      { this.openSpecificActionWizard('dvdetail'); },
    openRecoverWizard() { this.openSpecificActionWizard('recover'); },

    // ===== Per-wizard last-instance memory =====
    //
    // Each wizard remembers the instance the user picked on its
    // last successful run. Stored per-action so Tag Audio's last
    // pick doesn't bleed into Tag Video's. Bucket configs etc. are
    // intentionally NOT remembered — page-as-defaults handles
    // those, and rules handle "always identical settings" via
    // explicit save. Ad-hoc bucket tweaks are per-run by design.
    //
    // Storage key: 'resolvarr-wizard-instance-<action>'.
    // try/catch on every read+write — Safari private mode + similar
    // can block localStorage.
    _wizardInstanceKey(action) {
      return 'resolvarr-wizard-instance-' + action;
    },
    rememberWizardInstance(action, instanceId) {
      if (!action || !instanceId) return;
      try { localStorage.setItem(this._wizardInstanceKey(action), instanceId); }
      catch (e) { /* ignore — non-fatal */ }
    },
    // Returns the remembered instance ID iff it's still in pool +
    // matches the wizard's required type. Caller falls back to its
    // existing seed logic on null.
    recallWizardInstance(action, pool) {
      if (!action || !Array.isArray(pool) || pool.length === 0) return null;
      let stored = null;
      try { stored = localStorage.getItem(this._wizardInstanceKey(action)); }
      catch (e) { return null; }
      if (!stored) return null;
      return pool.some(i => i.id === stored) ? stored : null;
    },

    // ===== Cleanup unused tags wizard =====
    //
    // Cleanup isn't a combinedModes phase (it's a separate scan
    // action server-side), so it gets its own small wizard rather
    // than reusing the QFA flow. One-step: pick instance + Run.
    // The cleanup scan is preview-style — finds tags with 0 usage,
    // pops the result modal where the user picks which to delete.
    cleanupWizardState: {
      open: false,
      instanceId: '',
      busy: false,
    },
    openCleanupWizard() {
      const pool = (this.instances || []).filter(i => i.type === this.scanAppType);
      if (pool.length === 0) {
        const t = this.scanAppType === 'sonarr' ? 'Sonarr' : 'Radarr';
        this.showToast('Add a ' + t + ' instance in Settings → Instances first', 'error');
        return;
      }
      // Seed precedence: last-used remembered → current scanInstanceId
      // (when in pool) → first-of-type.
      const remembered = this.recallWizardInstance('cleanup', pool);
      const seedId = remembered
        || (pool.find(i => i.id === this.scanInstanceId) || pool[0]).id;
      this.cleanupWizardState = {
        open: true,
        instanceId: seedId,
        busy: false,
      };
    },
    closeCleanupWizard() {
      if (this.cleanupWizardState.busy) return;
      this.cleanupWizardState.open = false;
    },
    async runCleanupWizard() {
      if (!this.cleanupWizardState.instanceId) return;
      // Remember the picked instance for next time the wizard opens.
      this.rememberWizardInstance('cleanup', this.cleanupWizardState.instanceId);
      this.cleanupWizardState.busy = true;
      // Seed scanInstanceId so runCleanupCheck (which reads from
      // it) targets the wizard's pick. Auto-revert on close isn't
      // needed — when cleanup completes, scanInstanceId stays on
      // the user's last pick which is consistent with how Tag
      // and Audio runs already work.
      this.scanInstanceId = this.cleanupWizardState.instanceId;
      try {
        await this.runCleanupCheck();
      } finally {
        this.cleanupWizardState.busy = false;
        this.cleanupWizardState.open = false;
      }
    },
    cleanupWizardInstancesForType() {
      return (this.instances || [])
        .filter(i => i.type === this.scanAppType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // Translates an existing schedule row into the editingRule shape.
    // Cron is decomposed into preset/hour/minute via derivePresetFromCron
    // so the time-of-day pickers can render it back; non-standard cron
    // expressions land in 'custom' so the user can edit raw.
    openEditRuleModal(sj) {
      const copy = JSON.parse(JSON.stringify(sj));
      // Manual-only rules persist with cron="" — derive the UI flag
      // from that and substitute a sane default cron so the pickers
      // have something to show if the user toggles back to scheduled.
      copy.manualOnly = !copy.cron;
      if (copy.manualOnly) copy.cron = '0 3 * * *';
      const preset = derivePresetFromCron(copy.cron);
      const h24 = preset.hour ?? 3;
      copy.preset = preset.id;
      copy.hour = h24;
      copy.minute = preset.minute ?? 0;
      copy.hour12 = hour24To12(h24);
      copy.ampm = h24 >= 12 ? 'PM' : 'AM';
      copy.dow = preset.dow ?? 0;
      copy.dom = preset.dom ?? 1;
      // Backfill missing snapshots from globals — defence against any
      // pre-migration row that slipped through. Post-Step-1 every row
      // already has them populated.
      if (!copy.filters)         copy.filters         = this.snapshotGlobalFilters();
      if (!copy.audioTags)       copy.audioTags       = this.snapshotGlobalAudioTags();
      if (!copy.videoTags)       copy.videoTags       = this.snapshotGlobalVideoTags();
      if (!copy.dvDetail)        copy.dvDetail        = this.snapshotGlobalDvDetail();
      if (!copy.missingEpisodes) copy.missingEpisodes = this.snapshotGlobalMissingEpisodes();
      if (!copy.qbitSe) copy.qbitSe = this.snapshotDefaultQbitSe();
      if (!copy.plexSync)        copy.plexSync        = this.snapshotDefaultPlexSync();
      if (!copy.tbaRefresh)      copy.tbaRefresh      = this.snapshotDefaultTbaRefresh();
      if (!copy.releaseGroupIds) copy.releaseGroupIds = this.snapshotGlobalRGIds(copy.instanceId);
      copy.options = Object.assign({
        runMode: 'apply', cleanupUnusedTags: false, syncToSecondary: false, syncToInstanceId: '',
        includeDiscovery: false, autoActivateDiscovered: false,
        discoverWriteBack: false, discoverScanSecondary: false,
        recoverIncludeSecondary: false, recoverIncludeSonarr: false, recoverSonarrSecondary: false,
        recoverTestItemId: 0, debugTrace: false, bypassDvCache: false,
        audioTagsTarget: 'primary', videoTagsTarget: 'primary', dvDetailTarget: 'primary',
        recoverTarget: 'primary',
        tagSource: '', filterOnlyTag: 'lossless-web',
      }, copy.options || {});
      // Migrate legacy autoTagsRunOnSecondary boolean → per-bucket
      // targets. true → audio + video both targets='both'; false →
      // 'primary'. DV target stays 'primary' (it was always
      // single-instance pre-migration). Drop the legacy key after
      // translation so the saved shape stays clean.
      if (typeof copy.options.autoTagsRunOnSecondary === 'boolean') {
        const t = copy.options.autoTagsRunOnSecondary ? 'both' : 'primary';
        if (copy.options.audioTagsTarget === 'primary') copy.options.audioTagsTarget = t;
        if (copy.options.videoTagsTarget === 'primary') copy.options.videoTagsTarget = t;
        delete copy.options.autoTagsRunOnSecondary;
      }
      // Migrate legacy recoverIncludeSecondary boolean → recoverTarget.
      // true → 'both' (run primary AND secondary); false → 'primary'
      // (default). recoverTarget defaulted to 'primary' above so an
      // older rule without the flag stays primary-only. Keep the legacy
      // key on the saved shape — backend's JobOptions.RecoverIncludeSecondary
      // is still read by the scheduler runner; the new field is purely
      // a wizard/chain-dispatcher concern. Drop only if the user
      // explicitly toggled the new picker (handled inline via
      // setPerActionInstance).
      if (typeof copy.options.recoverIncludeSecondary === 'boolean' &&
          copy.options.recoverTarget === 'primary') {
        copy.options.recoverTarget = copy.options.recoverIncludeSecondary ? 'both' : 'primary';
      }
      // Migrate legacy single-mode rules to combined-mode shape so the
      // wizard's chain-checkbox UI can edit them. mode='tag' → mode=
      // 'combined' + combinedModes=['tag']. Save-time persists the
      // new shape; the chain runner / scheduler-runner already accept
      // both shapes via has() → r.mode === m || combinedModes.includes(m).
      if (copy.mode && copy.mode !== 'combined') {
        const legacyMode = copy.mode;
        copy.mode = 'combined';
        copy.options.combinedModes = copy.options.combinedModes || [];
        if (!copy.options.combinedModes.includes(legacyMode)) {
          copy.options.combinedModes.push(legacyMode);
        }
      }
      this.editingRule = copy;
      // Lock appType to whatever the existing rule's instance is —
      // editing a Radarr rule should never expose Sonarr instances in
      // the dropdown, and vice versa. Cross-type "edit" is effectively
      // a different rule; user must delete + re-create.
      const editInst = (this.instances || []).find(i => i.id === copy.instanceId);
      const editAppType = editInst ? editInst.type : 'radarr';
      this.ruleEditor = { open: true, isCreate: false, isQuickFix: false, step: 0, activeTab: 'basics', appType: editAppType, busy: false, error: '', cronError: '', nextFires: [], fixedAction: '' };
      this.computeRuleEditorNextFires();
    },
    closeRuleEditor() {
      // Closing the wizard mid-run signals "abort the chain". The
      // chain's isCancelled() guard reads this between phases, so
      // the next phase won't fire. The CURRENT phase keeps running
      // unless it's a DV scan — those have a backend cancel endpoint
      // that flips the scan's context (ffmpeg/dovi_tool die within
      // a second). Other phases (tag/audio/video/recover/discover)
      // are short enough that letting them complete is fine.
      if (this.ruleEditor.busy) {
        this.cancelRunningChain();
      }
      this.ruleEditor.open = false;
      this.editingRule = null;
    },
    dismissQuickFixResults() {
      this.quickFixResults = null;
    },

    // Aborts whatever Quick fix-all / rule-edit chain is mid-flight.
    // Sets a flag the chain loop's isCancelled() picks up between
    // phases AND fires the backend's DV cancel endpoint when a DV
    // phase is the current slow one. Idempotent — Cancel button +
    // Esc + wizard close all funnel here.
    cancelRunningChain() {
      this.chainCancelRequested = true;
      if (this.dvScanProgress && this.dvScanProgress.running) {
        this.cancelDvScan();
      }
    },

    // Re-fire the previously-previewed Quick fix-all chain in apply
    // mode. Reuses the rule snapshot stored on the result so the
    // user doesn't have to re-walk the wizard. Discover stays
    // preview-only (it has no apply concept); other phases get
    // mode='apply' via the chain runner's per-phase logic.
    canApplyQuickFixFromPreview() {
      const q = this.quickFixResults;
      return !!(q && q.runMode === 'preview' && q.ruleSnapshot && !this.ruleEditor.busy);
    },
    // Tooltip for the QFA result panel's "⚡ Apply now" button. The
    // chain reads per-phase targets from the saved snapshot — if any
    // phase resolves to secondary (target='secondary' / 'both', or
    // tag-mode syncToSecondary), the apply hits both instances.
    // Otherwise it's primary only. Tells the user up-front rather
    // than letting them assume the variant they're viewing is the
    // only target.
    applyQuickFixTooltip() {
      const q = this.quickFixResults;
      if (!q || !q.ruleSnapshot) return 'Re-fire the same chain in apply mode using these settings.';
      const r = q.ruleSnapshot;
      const opts = r.options || {};
      const has = (m) => r.mode === m || (r.mode === 'combined' && (opts.combinedModes || []).includes(m));
      const targets = [];
      if (has('recover'))   targets.push(opts.recoverTarget   || 'primary');
      if (has('audiotags')) targets.push(opts.audioTagsTarget || 'primary');
      if (has('videotags')) targets.push(opts.videoTagsTarget || 'primary');
      if (has('dvdetail'))  targets.push(opts.dvDetailTarget  || 'primary');
      const tagHitsSecondary = has('tag') && !!opts.syncToSecondary;
      const hitsSecondary = tagHitsSecondary || targets.some(t => t === 'secondary' || t === 'both');
      if (!hitsSecondary) {
        return 'Re-fires the chain in apply mode against this instance only.';
      }
      // Resolve concrete names — primary from rule.instanceId, secondary
      // = first other-of-same-type. Falls back to "Primary"/"Secondary"
      // if either lookup fails (e.g. instance was deleted post-preview).
      const primary = (this.instances || []).find(i => i.id === r.instanceId);
      const primaryName = primary ? primary.name : 'Primary';
      const secondary = primary
        ? (this.instances || []).find(i => i.type === primary.type && i.id !== primary.id)
        : null;
      const secondaryName = secondary ? secondary.name : 'Secondary';
      return 'Re-fires the chain in apply mode. Writes to ' + primaryName + ' + ' + secondaryName + '.';
    },
    // overlayFromRule + buildSnapshotOverlay moved to js/modules/run.js
    // (composed via { ...appRunModule() }). See Stage 4 in
    // docs/resolvarr/frontend-restructure-plan.md.
    async applyQuickFixFromPreview() {
      const q = this.quickFixResults;
      if (!q || !q.ruleSnapshot) return;
      // Deep clone so we don't mutate the result panel's snapshot.
      const rule = JSON.parse(JSON.stringify(q.ruleSnapshot));
      rule.options = rule.options || {};
      rule.options.runMode = 'apply';
      // Empty out previous result so the re-run replaces it cleanly.
      this.quickFixResults = null;
      await this.runQuickFixChain(rule);
    },

    // Switch between primary / secondary variants when the chain
    // ran the active phase on multiple instances (target='both').
    selectQfaDetailVariant(idx) {
      if (!this.qfaDetailVariants || idx < 0 || idx >= this.qfaDetailVariants.length) return;
      this.qfaDetailVariantIdx = idx;
      const v = this.qfaDetailVariants[idx];
      // Replay viewPhaseDetails-equivalent slot writes for the
      // active phase. Re-using the dispatcher would close+reopen
      // the modal; we just want to swap the response in place.
      if (this.qfaDetail === 'audio') {
        this.qfaDetailAudio = v.response;
        this.qfaDetailAutoFilter = this.pickAutoDetailFilter(v.response);
      } else if (this.qfaDetail === 'video') {
        this.qfaDetailVideo = v.response;
        this.qfaDetailAutoFilter = this.pickAutoDetailFilter(v.response);
      } else if (this.qfaDetail === 'dv') {
        this.qfaDetailDv = v.response;
        this.qfaDetailDvStatusFilter = null;
        this.qfaDetailDvTagFilter = null;
      } else if (this.recoverResults) {
        // Recover lives in its own modal (recoverResults slot, not
        // qfaDetail*) but reuses qfaDetailVariants for the switcher.
        // Swapping the response means re-running viewPhaseDetails-
        // recover's setup: hydrate, auto-select would-fix rows, pick
        // a sensible filter chip default, and reload exclusions for
        // the variant's instance.
        this.recoverResults = v.response;
        this.recoverError = '';
        this.recoverExpanded = {};
        this.recoverSeriesExpanded = {};
        this.recoverSeasonExpanded = {};
        const sel = {};
        for (const it of (v.response.recover || [])) {
          if (it.status === 'would-fix') sel[it.id] = true;
        }
        this.recoverApplySelected = sel;
        const t = (v.response.totals || {});
        if (t.recoverWouldFix) this.recoverFilter = 'would-fix';
        else if (t.recoverFlagged) this.recoverFilter = 'flagged';
        else this.recoverFilter = 'all';
        if (v.response.instance && v.response.instance.id) {
          this.loadRecoverExclusions(v.response.instance.id);
        }
      }
      // Clear filters/expansions that were keyed off the prior
      // variant's data so the new variant renders cleanly.
      this.qfaDetailExpanded = {};
      this.qfaDetailAutoTagFilter = null;
    },

    closeQfaDetail() {
      this.qfaDetail = null;
      this.qfaDetailExpanded = {};
      this.qfaDetailAudio = null;
      this.qfaDetailVideo = null;
      this.qfaDetailDv = null;
      // Drop variant memory on close — next open populates fresh.
      this.qfaDetailVariants = [];
      this.qfaDetailVariantIdx = 0;
      this.qfaDetailDvStatusFilter = null;
      this.qfaDetailDvTagFilter = null;
      this.qfaDetailDvStatusHelpOpen = false;
      this.qfaDetailAutoTagFilter = null;
      this.qfaDetailBreakdownOpen = false;
      // Sonarr per-series-season expansion — keyed by (seriesId,
      // seasonNumber). Wipes on modal close so a fresh scan doesn't
      // see leftover expand-state from a prior viewing AND so the map
      // doesn't grow unbounded across sessions.
      this.qfaDetailSeasonExpanded = {};
      // Audio/Video/DV standalone Run scans set scanResults.audioTags etc.
      // BEFORE viewPhaseDetails routes through this modal. Clear those too
      // so the orphan state doesn't linger after the modal closes — the
      // historicalRunInfo banner stays in sync regardless of which path
      // populated the modal (Run scan / History click / QFA chain phase).
      if (this.scanResults) {
        this.scanResults.audioTags = null;
        this.scanResults.videoTags = null;
        this.scanResults.dvDetail = null;
      }
      if (this.historicalRunInfo &&
          (this.historicalRunInfo.kind === 'audiotags' ||
           this.historicalRunInfo.kind === 'videotags' ||
           this.historicalRunInfo.kind === 'dvdetail')) {
        this.historicalRunInfo = null;
      }
    },

    // Click handler for phase rows on either result panel (Quick fix-
    // all + saved-rule run-now both render on Run mode; the History
    // tab opens the same drill-in modal overlaid). Takes the phase
    // object directly — no need to look it up in a collection — so
    // the same handler serves both panels.
    //
    // Opens a dedicated drill-in modal (Tag / Recover / ExtraTags) or
    // the existing Discover modal. Modals keep the user inside their
    // current tab/context; closing returns to the panel underneath.
    viewPhaseDetails(p) {
      if (!p || !p.response) {
        this.showToast('No detail available for this phase', 'error');
        return;
      }
      // Close any other open result modal first so two don't stack.
      // Map phase name to the closeAllResultModals except-key.
      const exceptMap = {
        tag: 'tag', discover: 'discover',
        audiotags: 'audio', videotags: 'video', dvdetail: 'dv',
      };
      this.closeAllResultModals(exceptMap[p.phase] || null);
      this.qfaDetailExpanded = {};
      // Pick a sensible default chip based on what the run produced —
      // mirrors pickDefaultScanFilter for live runs so the modal opens
      // on the chip with content rather than an empty default.
      switch (p.phase) {
        case 'tag': {
          // Tag unified through the top-level Tag detail modal — same
          // partial that the standalone Tag scan + History surfaces
          // use. Hydrate scanResults.tag instead of qfaDetailTag; the
          // modal pops up on scanResults.tag being non-null.
          this.scanResults.tag = p.response;
          this.scanGroupExpanded = {};
          this.scanRowExpanded = {};
          // Hydrate the page-level scan-state that confirmScanApply →
          // runTagInternal reads on Apply-now re-fire. Without this,
          // a chain-driven Tag preview (target=both / filter-only /
          // sync-on) would re-fire with stale page-level state and
          // silently downgrade scope (e.g. drop syncToInstanceId).
          // Source of truth in priority order:
          //   1. quickFixResults.ruleSnapshot (chain context — full
          //      rule state including tagSource + filterOnlyTag +
          //      cleanup + sync settings)
          //   2. p.response.instance.id (whichever instance the
          //      preview actually ran against — covers History replay
          //      and standalone runs).
          // Standalone Tag-RG wizard already seeds these in
          // runTagRgWizard, so steps below idempotently re-affirm.
          const snap = (this.quickFixResults && this.quickFixResults.ruleSnapshot) || null;
          if (snap && snap.instanceId) {
            this.scanInstanceId = snap.instanceId;
            const o = snap.options || {};
            this.scanSyncToSecondary = !!o.syncToSecondary;
            this.scanCleanupUnusedTags = !!o.cleanupUnusedTags;
            this.scanTagSource = o.tagSource || '';
            this.scanFilterOnlyTag = o.filterOnlyTag || '';
          } else if (p.response.instance && p.response.instance.id) {
            this.scanInstanceId = p.response.instance.id;
            // No ruleSnapshot → can't recover sync intent. The
            // confirm modal's secondary count comes from response
            // totals, so if secondary deltas are present we know
            // sync was on. Setting scanSyncToSecondary based on the
            // observed deltas keeps the Apply re-fire honoring it.
            const t = p.response.totals || {};
            this.scanSyncToSecondary = !!(t.secondaryToAdd || t.secondaryToRemove || t.secondaryToKeep || t.secondaryMissing);
          }
          const t = (p.response.totals || {});
          if ((t.toAdd || 0) + (t.secondaryToAdd || 0) > 0) this.scanFilter = 'add';
          else if ((t.toRemove || 0) + (t.secondaryToRemove || 0) > 0) this.scanFilter = 'remove';
          else if ((t.toKeep || 0) + (t.secondaryToKeep || 0) > 0) this.scanFilter = 'keep';
          else this.scanFilter = 'add';
          this.scanInstanceFilter = 'both';
          break;
        }
        case 'recover':
          // Recover unified through the top-level Recover detail modal
          // — same partial that the standalone Run Recover + History
          // surfaces use. Hydrate recoverResults instead of the
          // QFA-modal-only qfaDetailRecover; the modal pops up on
          // recoverResults being non-null.
          this.recoverResults = p.response;
          this.recoverError = '';
          this.recoverExpanded = {};
          this.recoverSeriesExpanded = {};
          this.recoverSeasonExpanded = {};
          // Auto-select would-fix rows + sensible filter default — same
          // semantics as runRecoverCheck so a wizard-driven preview lands
          // ready to Apply with one click. Without this the Apply button
          // sits disabled and the user has to manually re-check every row
          // they already implicitly approved by running the preview.
          {
            const sel = {};
            for (const it of (this.recoverResults.recover || [])) {
              if (it.status === 'would-fix') sel[it.id] = true;
            }
            this.recoverApplySelected = sel;
            const t = this.recoverResults.totals || {};
            if (t.recoverWouldFix) this.recoverFilter = 'would-fix';
            else if (t.recoverFlagged) this.recoverFilter = 'flagged';
            else this.recoverFilter = 'all';
          }
          if (p.response && p.response.instance && p.response.instance.id) {
            this.loadRecoverExclusions(p.response.instance.id);
          }
          break;
        case 'audiotags': {
          this.qfaDetailAudio = p.response;
          this.qfaDetail = 'audio';
          this.qfaDetailAutoFilter = this.pickAutoDetailFilter(p.response);
          break;
        }
        case 'videotags': {
          this.qfaDetailVideo = p.response;
          this.qfaDetail = 'video';
          this.qfaDetailAutoFilter = this.pickAutoDetailFilter(p.response);
          break;
        }
        case 'dvdetail': {
          this.qfaDetailDv = p.response;
          this.qfaDetail = 'dv';
          this.qfaDetailDvStatusFilter = null;
          this.qfaDetailDvTagFilter = null;
          this.qfaDetailDvFilter = this.pickDvDetailFilter(p.response);
          break;
        }
        case 'discover':
          // Discover unified through the top-level Discover detail modal
          // — auto-pops on scanResults.discover being non-null. Same
          // trigger pattern as Recover and Tag.
          this.scanResults.discover = p.response;
          this.scanDiscoverSelected = {};
          this.scanDiscoverExpanded = {};
          break;
        case 'missingepisodes': {
          // Missing-episodes uses the existing standalone-tab UI for
          // drill-down (per-series → seasons → episodes with per-row
          // Search + bulk Tag). Hydrate that state from the chain
          // response + the rule snapshot, then navigate the user to
          // the Missing Episodes sub-tab so they can act on the
          // findings. Apply re-fire happens via the QFA result panel's
          // Apply button (flips runMode='apply'); the standalone tab
          // is for ad-hoc selective Search / Tag after the chain run.
          this.missingEpisodesPreview = p.response;
          this.missingEpisodesError = '';
          const snap = (this.quickFixResults && this.quickFixResults.ruleSnapshot) || null;
          if (snap) {
            this.scanInstanceId = snap.instanceId || this.scanInstanceId;
            if (snap.missingEpisodes) {
              this.missingEpisodesConfig = {
                ...this.missingEpisodesConfig,
                ...JSON.parse(JSON.stringify(snap.missingEpisodes)),
              };
            }
          }
          // Pre-select all missing episodes — same default the
          // standalone Preview button uses, so the user lands on a
          // result ready for Search Selected / Tag series.
          const sel = {};
          for (const s of (p.response.series || [])) {
            for (const season of (s.seasons || [])) {
              for (const ep of (season.missingEpisodes || [])) {
                sel[ep.episodeID] = true;
              }
            }
          }
          this.missingEpisodesSelected = sel;
          this.scanAppType = 'sonarr';
          this.scanSection = 'missing-episodes';
          break;
        }
        case 'plexsync': {
          // Reuse the one-off run modal's result view. The phase row's
          // response is a PlexLabelRuleRun; we hand the run modal a
          // synthetic rule shaped just enough for its header (the
          // result markup is wrapped in x-if="rule"). Plex target
          // config isn't carried on the run, so the context strip may
          // read "(unknown Plex)" — cosmetic; the counts + per-label
          // table all come from the run itself.
          const run = p.response || {};
          // p.plexConfig is stamped on the phase by buildActivityResult
          // (schedule runs) + runQuickFixChain (QFA) — it carries the exact
          // rule config to re-fire in apply mode (the run result itself
          // doesn't carry the input config). Also lets us fill the synthetic
          // rule's targets so the header reads the real Plex, not "(unknown)".
          const pcfg = p.plexConfig || null;
          const ps = (pcfg && pcfg.plexLabelSync) || {};
          this.plexLabelRunModal = {
            open: true,
            stage: 'result',
            rule: {
              name: 'Plex label sync',
              instanceId: (pcfg && pcfg.arrInstanceId) || p.instanceId || '',
              targetTypes: ps.targetTypes || run.targetTypes || [],
              labelDisplay: ps.labelDisplay || {},
              targets: [{ plexInstanceId: ps.plexInstanceId || '', libraryKeys: ps.libraryKeys || [] }],
            },
            runMode: run.runMode || 'apply',
            result: run,
            error: '',
            detailsFilter: '',
            applyConfig: pcfg,
            applying: false,
          };
          break;
        }
        case 'tbarefresh': {
          // No dedicated TBA modal — hydrate the sub-tab's preview state
          // and jump there, same as missingepisodes does.
          this.tbaRefreshPreview = p.response || null;
          this.scanAppType = 'sonarr';
          this.scanSection = 'tba-refresh';
          break;
        }
        default:
          this.showToast('Unknown phase: ' + p.phase, 'error');
      }
    },
    // Back-compat thin wrapper for the QFA result panel that still
    // calls this method by name with phase/idx args. Looks up the
    // phase row in quickFixResults.phases and forwards to viewPhaseDetails.
    viewQuickFixPhaseDetails(phase, idx) {
      const phases = (this.quickFixResults && this.quickFixResults.phases) || [];
      let p = (typeof idx === 'number' && phases[idx] && phases[idx].phase === phase) ? phases[idx] : null;
      if (!p) p = phases.find(x => x.phase === phase);
      this.viewPhaseDetails(p);
    },

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

    // ---- Schedule preset helpers (rule-scoped mirrors of applySchedulePreset etc.) ----
    ruleApplyPreset() {
      const r = this.editingRule;
      if (!r) return;
      const h = Number.isFinite(r.hour) ? Math.max(0, Math.min(23, Math.floor(r.hour))) : 3;
      const m = Number.isFinite(r.minute) ? Math.max(0, Math.min(59, Math.floor(r.minute))) : 0;
      switch (r.preset) {
        case 'hourly':       r.cron = '0 * * * *'; break;
        case 'every-6h':     r.cron = '0 */6 * * *'; break;
        case 'every-12h':    r.cron = '0 */12 * * *'; break;
        case 'daily':        r.cron = `${m} ${h} * * *`; break;
        case 'twice-daily':  r.cron = '0 0,12 * * *'; break;
        case 'weekly':       r.cron = `${m} ${h} * * ${r.dow}`; break;
        case 'monthly':      r.cron = `${m} ${h} ${r.dom} * *`; break;
        case 'custom':       /* user-typed */ break;
      }
      this.computeRuleEditorNextFires();
    },
    ruleSyncHour12To24() {
      const r = this.editingRule;
      if (!r) return;
      let h = parseInt(r.hour12, 10);
      if (!Number.isFinite(h)) return;
      h = Math.max(1, Math.min(12, h));
      if (r.ampm === 'AM') r.hour = (h === 12) ? 0 : h;
      else                 r.hour = (h === 12) ? 12 : h + 12;
      this.ruleApplyPreset();
    },
    ruleEditorCombinedHas(mode) { return ((this.editingRule && this.editingRule.options.combinedModes) || []).includes(mode); },
    ruleEditorCombinedToggle(mode) {
      const arr = this.editingRule.options.combinedModes || [];
      const i = arr.indexOf(mode);
      const turningOn = i < 0;
      if (i >= 0) arr.splice(i, 1);
      else arr.push(mode);
      this.editingRule.options.combinedModes = arr;
      if (mode === 'discover' && turningOn) this.ensureDiscoverDefaults();
      // Discover just left the chain — clear any stale tagSource pick
      // tied to Discover. Otherwise the RG step opens with the
      // "Discover isn't part of this rule" auto-correct prompt firing
      // on every open, and the validation surfaces a misleading
      // Discover-related error.
      if (mode === 'discover' && !turningOn && this.editingRule.options.tagSource === 'discover') {
        this.editingRule.options.tagSource = '';
      }
    },
    // ensureDiscoverDefaults seeds the Discover-add toggle pair to
    // "Add disabled" when the rule includes Discover but neither
    // flag is set yet. Preview-only Discover is no longer a wizard
    // option — the RG-step radio chooses between disabled-add and
    // enabled-add. Called whenever mode changes to discover or the
    // combined-mode list gains discover so the radio always has a
    // valid default selected.
    ensureDiscoverDefaults() {
      if (!this.editingRule || !this.editingRule.options) return;
      const o = this.editingRule.options;
      if (!o.discoverWriteBack) {
        o.discoverWriteBack = true;
        o.autoActivateDiscovered = false;
      }
    },
    // Bound to the Mode <select> @change so picking Discover as the
    // single mode also seeds the defaults. ruleAffectsDiscover is
    // re-checked after the change because Alpine has already updated
    // the model by the time @change fires.
    ruleEditorOnModeChange() {
      if (this.ruleAffectsDiscover()) this.ensureDiscoverDefaults();
    },
    // When the user changes the rule's instance, the RG selection must
    // re-snapshot so we don't carry Sonarr IDs into a Radarr rule (or
    // vice versa). Filters block stays — they're per-Arr-type
    // independent so user-pinned audio/quality preferences still apply.
    // Mode + combined substeps must also be filtered through the
    // per-instance-type catalog so a Sonarr rule doesn't end up on a
    // Radarr-only mode (which would 501 at scan time).
    ruleEditorOnInstanceChange() {
      const r = this.editingRule;
      if (!r) return;
      r.releaseGroupIds = this.snapshotGlobalRGIds(r.instanceId);
      // Mode is always 'combined' post-dropdown-removal; instance-type
      // change just filters combinedModes to substeps the new type
      // supports (Sonarr → only 'recover' today). Sonarr selection
      // with no 'recover' tick auto-seeds it so the rule has a phase.
      r.mode = 'combined';
      const supportedSubs = this.ruleCombinedSubstepsForInstance().map(s => s.value);
      r.options.combinedModes = (r.options.combinedModes || []).filter(m => supportedSubs.includes(m));
      if (this.ruleEditorInstanceType() === 'sonarr' && r.options.combinedModes.length === 0) {
        r.options.combinedModes = ['recover'];
      }
      // qBit Category Fix: the picked Arr-side download-client ID + the
      // pre/post category snapshots are scoped to the OLD instance's
      // /api/v3/downloadclient list. After switching to a new Arr the
      // saved ID may not exist on the new instance — clear them so the
      // user has to re-pick from the new instance's live list. Same for
      // the loaded list itself (different Arr → different clients) and
      // the error/loading status.
      if (r.qbitCategoryFix) {
        r.qbitCategoryFix.arrDownloadClientId = 0;
        r.qbitCategoryFix.preImportCategorySnapshot = '';
        r.qbitCategoryFix.postImportCategorySnapshot = '';
      }
      this.arrDownloadClients = [];
      this.arrDownloadClientsError = '';
      // If the rule currently has qBit Category Fix on, kick off a fresh
      // load against the new instance so the user lands on Step 3d with
      // a populated picker rather than an empty state.
      if (typeof this.ruleAffectsQbitCategoryFix === 'function' &&
          this.ruleAffectsQbitCategoryFix() && r.instanceId) {
        this.loadArrDownloadClients(r.instanceId);
      }
    },

    // ---- Release Groups picker ----
    groupsForRule() {
      if (!this.editingRule) return [];
      const inst = this.instances.find(i => i.id === this.editingRule.instanceId);
      if (!inst) return [];
      return this.groups.filter(g => g.type === inst.type);
    },
    ruleEditorRGChecked(id) { return ((this.editingRule && this.editingRule.releaseGroupIds) || []).includes(id); },
    ruleEditorRGToggle(id) {
      const arr = this.editingRule.releaseGroupIds || [];
      const i = arr.indexOf(id);
      if (i >= 0) arr.splice(i, 1);
      else arr.push(id);
      this.editingRule.releaseGroupIds = arr;
    },
    ruleEditorRGAllVisible() {
      const visible = this.groupsForRule();
      return visible.length > 0 && visible.every(g => (this.editingRule.releaseGroupIds || []).includes(g.id));
    },
    ruleEditorRGToggleAll() {
      const visible = this.groupsForRule();
      if (this.ruleEditorRGAllVisible()) {
        this.editingRule.releaseGroupIds = (this.editingRule.releaseGroupIds || []).filter(id => !visible.some(g => g.id === id));
      } else {
        const set = new Set(this.editingRule.releaseGroupIds || []);
        visible.forEach(g => set.add(g.id));
        this.editingRule.releaseGroupIds = [...set];
      }
    },

    // ---- Cron preview ----
    computeRuleEditorNextFires() {
      const r = this.editingRule;
      if (!r) return;
      const cron = (r.cron || '').trim();
      if (!cron) {
        this.ruleEditor.nextFires = [];
        this.ruleEditor.cronError = '';
        return;
      }
      try {
        const fires = nextCronFires(cron, 5, new Date());
        const opts = this.dateFormatOptions();
        const loc = this.serverLocale || 'en-GB';
        this.ruleEditor.nextFires = fires.map(d => d.toLocaleString(loc, opts));
        this.ruleEditor.cronError = '';
      } catch (e) {
        this.ruleEditor.nextFires = [];
        this.ruleEditor.cronError = 'Invalid cron: ' + (e.message || 'unknown');
      }
    },

    // ---- Save ----
    async saveRuleEditor() {
      const r = this.editingRule;
      if (!r) return;
      this.ruleEditor.error = '';
      // Webhook-rule branch — kind='webhook' takes precedence over the
      // schedule path. Builds the webhook-rule body (functions array,
      // per-rule snapshots, no cron) and POSTs to /api/webhook-rules.
      // Schedule-rule path below stays untouched for kind='schedule'.
      if (this.ruleEditorIsWebhook()) {
        return this.saveWebhookRuleEditor();
      }
      // Quickfix mode skips name/cron validation — the wizard hides
      // those fields. Save dispatches the chain instead of persisting
      // a rule (see runQuickFixChain in the dispatcher).
      if (this.ruleEditor.isQuickFix) {
        if (!r.instanceId) { this.ruleEditor.error = 'Pick an instance'; return; }
        if (r.mode === 'combined' && (!r.options.combinedModes || r.options.combinedModes.length === 0)) {
          this.ruleEditor.error = 'Combined mode needs at least one chain step';
          return;
        }
        if (this.tagPhaseNeedsGroups() && (!r.releaseGroupIds || r.releaseGroupIds.length === 0)) {
          this.ruleEditor.error = 'Pick at least one Release Group, or enable Discover with "Add to config + enable" so it seeds them at runtime';
          return;
        }
        return this.runQuickFixChain();
      }
      if (!r.name.trim()) { this.ruleEditor.error = 'Name is required'; return; }
      if (!r.instanceId) { this.ruleEditor.error = 'Pick an instance'; return; }
      // Manual-only rules persist with cron="" — skip cron validation.
      if (!r.manualOnly) {
        if (!r.cron.trim()) { this.ruleEditor.error = 'Cron expression is required'; return; }
        if (this.ruleEditor.cronError) { this.ruleEditor.error = this.ruleEditor.cronError; return; }
      }
      if (r.mode === 'combined' && (!r.options.combinedModes || r.options.combinedModes.length === 0)) {
        this.ruleEditor.error = 'Combined mode needs at least one chain step';
        return;
      }
      // Tag-touching rules must have at least one Release Group active —
      // an empty subset would tag nothing and just confuse the user when
      // the schedule fires with zero output. Bypassed for Discover →
      // auto-activate chains (see tagPhaseNeedsGroups).
      if (this.tagPhaseNeedsGroups() && (!r.releaseGroupIds || r.releaseGroupIds.length === 0)) {
        this.ruleEditor.error = 'Pick at least one Release Group for this rule, or enable Discover with "Add to config + enable" so it seeds them at runtime';
        return;
      }
      const labelErr = this.ruleEditorLabelError();
      if (labelErr) { this.ruleEditor.error = labelErr; return; }
      // Remember the picked instance per Arr-type so the next
      // Create-rule wizard open pre-fills with the same instance.
      // ruleEditor.appType holds the wizard's locked Arr-type.
      const memKey = 'create-rule-' + (this.ruleEditor.appType || 'radarr');
      this.rememberWizardInstance(memKey, r.instanceId);
      const options = { ...r.options };
      if (r.mode !== 'combined') options.combinedModes = [];
      if (r.mode === 'discover' || r.mode === 'recover') {
        options.runMode = '';
        options.cleanupUnusedTags = false;
        options.syncToSecondary = false;
      }
      const body = {
        name: r.name.trim(),
        mode: r.mode,
        instanceId: r.instanceId,
        // Manual-only rules persist with empty cron — backend
        // validation accepts that as "skip cron-loop registration".
        cron: r.manualOnly ? '' : r.cron.trim(),
        enabled: !!r.enabled,
        options,
      };
      // Per-rule snapshots: only sent when the section is relevant to
      // the current rule's mode. Recover-only rules don't carry RG/
      // Filters/Extra-tags noise, keeping the persisted shape clean.
      if (this.ruleEditorTabVisible('rg'))       body.releaseGroupIds = [...(r.releaseGroupIds || [])];
      if (this.ruleEditorTabVisible('filters'))  body.filters = { ...r.filters };
      if (this.ruleEditorTabVisible('audio') && r.audioTags)    body.audioTags = JSON.parse(JSON.stringify(r.audioTags));
      if (this.ruleEditorTabVisible('video') && r.videoTags)    body.videoTags = JSON.parse(JSON.stringify(r.videoTags));
      if (this.ruleEditorTabVisible('dvdetail') && r.dvDetail)  body.dvDetail  = JSON.parse(JSON.stringify(r.dvDetail));
      // Missing Episodes has no dedicated wizard step — config lives
      // inline in Review. Send snapshot whenever the phase is in
      // combinedModes so the backend persists it on the saved rule.
      if (this.ruleAffectsMissingEpisodes() && r.missingEpisodes) body.missingEpisodes = JSON.parse(JSON.stringify(r.missingEpisodes));
      // qBit S/E — Sonarr-only qbitsetag phase (editingRule.qbitSe).
      if (this.ruleAffectsQbitSe() && r.qbitSe) body.qbitSe = JSON.parse(JSON.stringify(r.qbitSe));
      // Plex sync — dedicated wizard step (editingRule.plexSync). Send
      // whenever the phase is selected so the backend persists the
      // snapshot on the saved schedule.
      if (this.ruleAffectsPlexSync() && r.plexSync) body.plexSync = JSON.parse(JSON.stringify(r.plexSync));
      // TBA refresh — Sonarr-only file-rename phase, config in Review.
      if (this.ruleAffectsTbaRefresh() && r.tbaRefresh) body.tbaRefresh = JSON.parse(JSON.stringify(r.tbaRefresh));
      this.ruleEditor.busy = true;
      try {
        const url = r.id ? `/api/schedules/${r.id}` : '/api/schedules';
        const method = r.id ? 'PUT' : 'POST';
        const res = await this.apiFetch(url, {
          method, headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(d.error || 'HTTP ' + res.status);
        this.ruleEditor.open = false;
        this.editingRule = null;
        this.showToast(r.id ? 'Schedule updated' : 'Schedule created', 'success');
        await this.loadSchedules();
      } catch (e) {
        this.ruleEditor.error = e.message || 'Save failed';
      } finally {
        this.ruleEditor.busy = false;
      }
    },

    // Webhook-rule save flow. Branched off saveRuleEditor when
    // ruleEditor.kind === 'webhook'. Builds the WebhookRule shape,
    // POSTs to /api/webhook-rules (or PUTs on update), refreshes the
    // local cache, closes the editor on success. Validation errors
    // surface inline + leave the wizard open so the user can fix.
    async saveWebhookRuleEditor() {
      const r = this.editingRule;
      const inst = (this.instances || []).find(i => i.id === r.instanceId);
      if (!inst) {
        this.ruleEditor.error = 'Pick a target instance';
        return;
      }
      if (!r.name || !r.name.trim()) {
        this.ruleEditor.error = 'Rule name is required';
        return;
      }
      if (!this.webhookRuleAnyFunctionTicked()) {
        this.ruleEditor.error = 'Pick at least one function on the Basics step';
        return;
      }
      const labelErr = this.ruleEditorLabelError();
      if (labelErr) { this.ruleEditor.error = labelErr; return; }
      // Build canonical-ordered functions array — matches dispatcher
      // execution order so saved Functions[] reads naturally in the
      // rule list + audit logs.
      const o = r.options || {};
      const fnList = [];
      if (o.fnDiscover)         fnList.push('discover');
      if (o.fnRecover)          fnList.push('recover');
      if (o.fnTagReleaseGroups) fnList.push('tagReleaseGroups');
      if (o.fnTagAudio)         fnList.push('tagAudio');
      if (o.fnTagVideo)         fnList.push('tagVideo');
      if (o.fnTagDvDetail)      fnList.push('tagDvDetail');
      if (o.fnSyncToSecondary)  fnList.push('syncToSecondary');
      if (o.fnGrabRename)       fnList.push('grabRename');
      if (o.fnQbitSeTag)        fnList.push('qbitSeTag');
      if (o.fnQbitCategoryFix)  fnList.push('qbitCategoryFix');
      if (o.fnPlexLabelSync)    fnList.push('plexLabelSync');
      const body = {
        name: r.name.trim(),
        enabled: r.enabled !== false,
        instanceId: r.instanceId,
        appType: inst.type,
        functions: fnList,
        // M-Webhook notification kill-switch. When true, the rule's
        // fires reach every enabled agent whose Events.OnX flag
        // matches the event class. Each agent's Functions whitelist
        // (Settings → Notifications) decides what actually renders.
        notifyOnFire: !!(r.options && r.options.notifyOnFire),
      };
      // Per-rule snapshots — only sent when the corresponding step
      // is relevant. Webhook rules use the same gates (ruleAffectsTag
      // etc.) as schedule rules, so this mirrors saveRuleEditor's
      // schedule branch.
      if (this.ruleEditorTabVisible('rg'))       body.releaseGroupIds = [...(r.releaseGroupIds || [])];
      if (this.ruleEditorTabVisible('filters'))  body.filters = { ...r.filters };
      if (this.ruleEditorTabVisible('audio') && r.audioTags)    body.audioTags = JSON.parse(JSON.stringify(r.audioTags));
      if (this.ruleEditorTabVisible('video') && r.videoTags)    body.videoTags = JSON.parse(JSON.stringify(r.videoTags));
      if (this.ruleEditorTabVisible('dvdetail') && r.dvDetail)  body.dvDetail  = JSON.parse(JSON.stringify(r.dvDetail));
      // Missing Episodes has no dedicated wizard step — config lives
      // inline in Review. Send snapshot whenever the phase is in
      // combinedModes so the backend persists it on the saved rule.
      if (this.ruleAffectsMissingEpisodes() && r.missingEpisodes) body.missingEpisodes = JSON.parse(JSON.stringify(r.missingEpisodes));
      // qBit S/E — Sonarr-only qbitsetag phase (editingRule.qbitSe).
      if (this.ruleAffectsQbitSe() && r.qbitSe) body.qbitSe = JSON.parse(JSON.stringify(r.qbitSe));
      // Tag-source ("Source of release groups") — persist the user's
      // pick so it survives save -> reopen. Previously only filter-only
      // was sent, so picking active / discover was silently dropped and
      // the radio reverted on the next edit. The webhook tag adapter
      // handles filter-only (rule.TagSource=="filter-only"); active +
      // discover fall through to per-group matching (discover's new
      // groups arrive via the separate Discover function). filterOnlyTag
      // is only sent + validated for filter-only.
      const ts = (o.tagSource || '').trim();
      if (ts) {
        body.tagSource = ts;
        if (ts === 'filter-only') {
          body.filterOnlyTag = (o.filterOnlyTag || 'lossless-web').trim();
        }
      }
      // Persist the Discover auto-activate choice ("Add to config + enable"
      // vs "leave disabled") so it survives reopen. Only meaningful when the
      // Discover function is on. The backend field (DiscoverAutoEnable) +
      // the load-side hoist already exist; this completes the round-trip
      // that shipped with the v0.6.17 Use-Discover-persist fix.
      if (o.fnDiscover) {
        body.discoverAutoEnable = !!o.autoActivateDiscovered;
      }

      // Sync target — only meaningful when fnSyncToSecondary is on.
      if (o.fnSyncToSecondary && o.syncToInstanceId) {
        body.syncToInstanceId = o.syncToInstanceId;
      }
      // GrabRename / QbitSe criteria — only sent when the matching fn
      // is enabled. Backend's validator (validateGrabRenameCriteria +
      // qbitInstanceExists checks) requires the struct + a valid
      // qbitInstanceId when the function is in the rule's Functions
      // list, so this gate is symmetric with the wizard's tab gating.
      if (o.fnGrabRename && r.grabRename) {
        body.grabRename = JSON.parse(JSON.stringify(r.grabRename));
      }
      if (o.fnQbitSeTag && r.qbitSe) {
        body.qbitSe = JSON.parse(JSON.stringify(r.qbitSe));
      }
      if (o.fnQbitCategoryFix && r.qbitCategoryFix) {
        body.qbitCategoryFix = JSON.parse(JSON.stringify(r.qbitCategoryFix));
      }
      if (o.fnPlexLabelSync && r.plexLabelSync) {
        // Drop empty labelDisplay map for clean JSON over the wire —
        // backend tolerates either shape but tidy is better.
        const ps = JSON.parse(JSON.stringify(r.plexLabelSync));
        if (ps.labelDisplay && Object.keys(ps.labelDisplay).length === 0) {
          delete ps.labelDisplay;
        }
        body.plexLabelSync = ps;
      }
      this.ruleEditor.busy = true;
      try {
        const url = r.id ? `/api/webhook-rules/${r.id}` : '/api/webhook-rules';
        const method = r.id ? 'PUT' : 'POST';
        const res = await this.apiFetch(url, {
          method, headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(d.error || 'HTTP ' + res.status);
        this.ruleEditor.open = false;
        this.editingRule = null;
        this.showToast(r.id ? 'Webhook rule updated' : 'Webhook rule created', 'success');
        await this.loadWebhookRules();
      } catch (e) {
        this.ruleEditor.error = e.message || 'Save failed';
      } finally {
        this.ruleEditor.busy = false;
      }
    },

    // Load the webhook-rules list from the server. Mirrors loadSchedules
    // — populates this.webhookRules so the Webhooks page list +
    // editor edit-flow can read off the same array.
    async loadWebhookRules() {
      try {
        const r = await this.apiFetch('/api/webhook-rules');
        if (r.ok) {
          const d = await r.json();
          this.webhookRules = Array.isArray(d) ? d : [];
        }
      } catch (e) {
        // Silent — Webhooks page surfaces its own load-failed state.
      }
    },

    // Filter the rules list to a specific instance. Used by the
    // per-instance card on the Webhooks page.
    webhookRulesForInstance(instanceId) {
      return (this.webhookRules || []).filter(r => r.instanceId === instanceId);
    },

    // Compute the Connect event types this instance's webhook needs
    // toggled on in Sonarr/Radarr. Derived from the enabled rules'
    // functions + per-bucket strip-on-delete opt-ins:
    //   tagReleaseGroups / discover / tagAudio / tagVideo / tagDvDetail
    //     / recover / syncToSecondary  → On Import + On Upgrade
    //     (Sonarr: On Download — download/import + upgrade)
    //   tagReleaseGroups (Radarr only)  → also On Movie File Delete +
    //     OnMovieFileDeleteForUpgrade (auto-strip Tag-RG flow)
    //   any audio/video/DV bucket with stripOnFileDelete=true → matching
    //     On Movie/Episode File Delete events
    //   grabRename / qbitSeTag → On Grab
    // "On Test" is NOT a togglable event — it's a button in Connect
    // that lets you fire a test ping. Listing it here would send users
    // hunting for a checkbox that doesn't exist.
    webhookEventsForFunctions(fnSet, isSonarr, opts = {}) {
      const events = new Set();
      const importLike = ['tagReleaseGroups', 'discover', 'tagAudio', 'tagVideo',
                          'tagDvDetail', 'recover', 'syncToSecondary'];
      if (importLike.some(f => fnSet.has(f))) {
        events.add('On File Import');
        events.add('On File Upgrade');
      }
      // Tag-RG (Radarr) drives the automatic strip-on-delete invariant,
      // so the user must enable Movie File Delete events even if no
      // user-toggleable function dispatches on them.
      if (!isSonarr && fnSet.has('tagReleaseGroups')) {
        events.add('On Movie File Delete');
      }
      // Per-bucket strip-on-delete: any bucket flagged → matching
      // file-delete events. Mirror of the server's
      // FiresPerBucketStripOnDelete + ConnectEventsNeeded extension.
      if (opts.bucketStripOnDelete) {
        events.add(isSonarr ? 'On Episode File Delete' : 'On Movie File Delete');
      }
      if (fnSet.has('grabRename') || fnSet.has('qbitSeTag')) {
        events.add('On Grab');
      }
      return Array.from(events);
    },

    // Events for an instance — union of events across all enabled
    // rules linked to that instance. Used by the per-instance card
    // on the Webhooks page so the user can see at a glance "what
    // do I need to keep enabled in Sonarr/Radarr Connect for these
    // rules to work". Empty when no rules exist (Configure webhook
    // alone doesn't dictate any events; the URL just sits ready
    // for whatever events arrive).
    webhookEventsForInstance(instanceId) {
      const inst = (this.instances || []).find(i => i.id === instanceId);
      if (!inst) return [];
      const rules = this.webhookRulesForInstance(instanceId).filter(r => r.enabled);
      const fnSet = new Set();
      let bucketStripOnDelete = false;
      for (const rule of rules) {
        for (const fn of (rule.functions || [])) fnSet.add(fn);
        if ((rule.audioTags && rule.audioTags.stripOnFileDelete)
            || (rule.videoTags && rule.videoTags.stripOnFileDelete)
            || (rule.dvDetail && rule.dvDetail.stripOnFileDelete)) {
          bucketStripOnDelete = true;
        }
      }
      return this.webhookEventsForFunctions(fnSet, inst.type === 'sonarr', { bucketStripOnDelete });
    },

    // Events for a single rule — used in the rule editor's Review
    // step. Reads off editingRule.options.fn* + bucket snapshots
    // directly so the events list updates live as the user toggles
    // function checkboxes + bucket strip flags on the Basics + bucket
    // steps, before the rule is saved.
    webhookEventsForCurrentRule() {
      const r = this.editingRule;
      if (!r || !r.options) return [];
      const o = r.options;
      const fnSet = new Set();
      if (o.fnTagReleaseGroups) fnSet.add('tagReleaseGroups');
      if (o.fnDiscover)         fnSet.add('discover');
      if (o.fnTagAudio)         fnSet.add('tagAudio');
      if (o.fnTagVideo)         fnSet.add('tagVideo');
      if (o.fnTagDvDetail)      fnSet.add('tagDvDetail');
      if (o.fnRecover)          fnSet.add('recover');
      if (o.fnSyncToSecondary)  fnSet.add('syncToSecondary');
      if (o.fnGrabRename)       fnSet.add('grabRename');
      if (o.fnQbitSeTag)        fnSet.add('qbitSeTag');
      const inst = (this.instances || []).find(i => i.id === r.instanceId);
      const isSonarr = inst ? inst.type === 'sonarr' : false;
      return this.webhookEventsForFunctions(fnSet, isSonarr, {
        bucketStripOnDelete: this.webhookRuleHasBucketStripOnDelete(),
      });
    },

    // App-type setter — persists to localStorage + mirrors the
    // hash via pushNav so back/forward + bookmark URLs include
    // the picked type. Refuses to switch to a type with no
    // instances (pills are :disabled in that state but a
    // programmatic call should still no-op).
    setWebhookAppType(type) {
      if (type !== 'radarr' && type !== 'sonarr') return;
      if (!this.webhookAppTypeAvailable(type)) return;
      if (this.webhookAppType === type) return;
      this.webhookAppType = type;
      localStorage.setItem('resolvarr-webhook-app-type', type);
      // Auto-pick the Recent activity instance whenever the new
      // pill has exactly one instance, OR the currently-selected
      // instance no longer matches the new app-type. Saves a click
      // when the user only has one Sonarr / one Radarr.
      const candidates = (this.instances || []).filter(i => i.type === type);
      const currentValid = candidates.some(i => i.id === this.webhookActivityInstanceId);
      if (!currentValid) {
        this.webhookActivityInstanceId = candidates.length > 0 ? candidates[0].id : '';
        if (this.webhookActivityInstanceId) {
          this.loadWebhookEvents(this.webhookActivityInstanceId);
        }
      }
      this.pushNav();
    },

    // True when there's at least one instance of the given type.
    // Used to gate the pill's :disabled state.
    webhookAppTypeAvailable(type) {
      return (this.instances || []).some(i => i.type === type);
    },

    // Filtered instance list for the Webhooks page — matches the
    // active app-type pill. Iterated by the per-instance card
    // template instead of the raw `instances` array.
    webhookInstancesForAppType() {
      return (this.instances || []).filter(i => i.type === this.webhookAppType);
    },

    // Toggle a rule's enabled state via PUT. Lighter than a full
    // edit-flow when the user just wants to pause/resume a rule
    // without re-walking the wizard.
    async toggleWebhookRuleEnabled(rule) {
      try {
        const body = {
          name: rule.name,
          enabled: !rule.enabled,
          instanceId: rule.instanceId,
          appType: rule.appType,
          functions: rule.functions || [],
        };
        // Per-rule snapshots round-trip via the response from a fresh
        // GET — toggling enabled doesn't need to re-send them.
        // Backend's update path preserves nil-fields when not present,
        // matching the schedule rule behaviour.
        if (rule.filters)         body.filters = rule.filters;
        if (rule.audioTags)       body.audioTags = rule.audioTags;
        if (rule.videoTags)       body.videoTags = rule.videoTags;
        if (rule.dvDetail)        body.dvDetail = rule.dvDetail;
        if (rule.releaseGroupIds) body.releaseGroupIds = rule.releaseGroupIds;
        if (rule.syncToInstanceId) body.syncToInstanceId = rule.syncToInstanceId;
        if (rule.discoverAutoEnable) body.discoverAutoEnable = rule.discoverAutoEnable;
        if (rule.grabRename)      body.grabRename = rule.grabRename;
        if (rule.qbitSe)          body.qbitSe = rule.qbitSe;
        if (rule.qbitCategoryFix) body.qbitCategoryFix = rule.qbitCategoryFix;
        if (rule.plexLabelSync)   body.plexLabelSync = rule.plexLabelSync;
        const r = await this.apiFetch('/api/webhook-rules/' + rule.id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadWebhookRules();
        this.showToast('Rule ' + (body.enabled ? 'resumed' : 'paused'), 'success');
      } catch (e) {
        this.showToast('Toggle failed: ' + e.message, 'error');
      }
    },

    // Newest-first lookup of the rule's last fire. Returns null when
    // the rule has never fired. Used by the rule-card last-status
    // pill — shape is core.WebhookRuleRun (status + startedAt +
    // durationMs + eventType + itemTitle + itemContext + summary).
    lastWebhookRun(rule) {
      const h = (rule && rule.history) || [];
      return h.length > 0 ? h[h.length - 1] : null;
    },

    // Returns the .card-edge variant class for a webhook rule's
    // left-edge status strip. Mirror of scheduleEdgeClass — same
    // colour semantics (green=ok, amber=partial, red=error,
    // gray=never-fired) so users moving between schedule cards
    // (Tag Library) and webhook cards (Webhooks page) get the same
    // visual cue.
    webhookEdgeClass(rule) {
      const last = this.lastWebhookRun(rule);
      if (!last) return 'gray';
      switch (last.status) {
        case 'ok':      return 'green';
        case 'partial': return 'amber';
        case 'error':   return 'red';
        default:        return 'gray';
      }
    },

    // Connect events a SAVED webhook rule listens on. Mirrors
    // webhookEventsForCurrentRule but reads from a saved rule's
    // functions[] (server-shape) instead of the editor's options.fn*
    // (wizard-shape). Used by the rule card's "Triggers" body row
    // for parity with schedule cards' "Next" timestamp.
    webhookEventsForRule(rule) {
      if (!rule) return [];
      const fnSet = new Set((rule.functions || []).map(s => String(s)));
      const inst = (this.instances || []).find(i => i.id === rule.instanceId);
      const isSonarr = inst ? inst.type === 'sonarr' : false;
      const bucketStripOnDelete =
        (rule.audioTags && rule.audioTags.stripOnFileDelete) ||
        (rule.videoTags && rule.videoTags.stripOnFileDelete) ||
        (rule.dvDetail && rule.dvDetail.stripOnFileDelete);
      return this.webhookEventsForFunctions(fnSet, isSonarr, { bucketStripOnDelete });
    },

    // Parse a webhook rule-run summary string into structured per-
    // function rows for the history modal. Summary format from
    // buildWebhookRuleRun is "fn1: result1; fn2: result2; ..." where
    // each result MAY have a trailing ` (detail text)` block. We
    // split on '; ' for the outer separator and on the FIRST ' (' for
    // result-vs-detail — paren-balanced enough for every shape the
    // dispatcher emits today (nested parens like "movie tmdbId=X not
    // in Radarr (Remux) library" survive intact because we keep
    // everything after the first ' (' minus a trailing ')').
    parseRuleRunSummary(summary) {
      if (!summary || typeof summary !== 'string') return [];
      const out = [];
      const pieces = summary.split('; ');
      for (const piece of pieces) {
        const trimmed = piece.trim();
        if (!trimmed) continue;
        const colonIdx = trimmed.indexOf(': ');
        if (colonIdx === -1) {
          out.push({ fn: '', result: trimmed, detail: '', status: 'unknown' });
          continue;
        }
        const fn = trimmed.slice(0, colonIdx);
        const after = trimmed.slice(colonIdx + 2);
        const parenIdx = after.indexOf(' (');
        let result, detail;
        if (parenIdx === -1) {
          result = after;
          detail = '';
        } else {
          result = after.slice(0, parenIdx);
          detail = after.slice(parenIdx + 2);
          if (detail.endsWith(')')) detail = detail.slice(0, -1);
        }
        // Status is derived from the raw result string (still carries
        // the "error: " marker if the dispatcher prefixed one). Then
        // strip that marker from the display version — the red ✕ icon
        // + red chip background convey "error" without the user
        // needing to read the word twice.
        const status = this.webhookFnResultStatus(result);
        let displayResult = result;
        if (status === 'error' && /^error:\s*/i.test(displayResult)) {
          displayResult = displayResult.replace(/^error:\s*/i, '');
        }
        // Grab Rename gets a special structured render in the history
        // modal — the raw "renamed \"from\" → \"to\" (triggers: ...)"
        // is too long for a single chip and the trigger labels are
        // jargon. Precompute parsed names + diff tokens + humanized
        // triggers so the template can render them as a compact diff
        // without doing regex work on every Alpine reactivity tick.
        let grabRename = null;
        if (fn === 'grabRename' && status === 'change') {
          const names = this.parseGrabRenameNames(result);
          if (names) {
            grabRename = {
              from: names.from,
              to: names.to,
              triggers: this.parseGrabRenameTriggers(detail).map(t => this.humanizeGrabRenameTrigger(t)),
              diff: this.grabRenameDiffTokens(names.from, names.to),
            };
          }
        }
        out.push({
          fn,
          result: displayResult,
          detail,
          status,
          grabRename,
        });
      }
      return out;
    },

    // Classify a result-prefix into a status bucket so the modal can
    // pick the right icon + color. Kept in sync with the prefixes the
    // dispatcher actually emits (recover.go / discover.go / tag_*.go /
    // sync.go / file_delete.go / grab_rename.go / qbit_*.go).
    webhookFnResultStatus(result) {
      const r = (result || '').toLowerCase().trim();
      if (!r) return 'unknown';
      if (r.startsWith('skipped')) return 'skipped';
      if (r.startsWith('failed') || r.startsWith('error') || r.includes('err:')) return 'error';
      if (r.startsWith('no change') || r.startsWith('no diff') || r === 'noop') return 'noop';
      if (r.startsWith('+')) return 'change-add';
      if (r.startsWith('-')) return 'change-remove';
      if (r.startsWith('renamed') || r.startsWith('changed') || r.startsWith('applied') ||
          r.startsWith('mirrored') || r.startsWith('tagged') || r.startsWith('cleaned') ||
          r.startsWith('stripped') || r.startsWith('recovered') || r.startsWith('queued') ||
          r.startsWith('discovered')) return 'change';
      return 'change';
    },

    // Pretty-print a function id for the history modal. Falls back to
    // the raw id if we hit something un-mapped (defensive — a new
    // function added on the backend will still render, just without
    // a friendly label).
    webhookFnDisplayName(fn) {
      const map = {
        recover: 'Recover',
        discover: 'Discover',
        tagReleaseGroups: 'Tag quality releases',
        tagAudio: 'Tag Audio',
        tagVideo: 'Tag Video',
        tagDvDetail: 'Tag DV Detail',
        syncToSecondary: 'Sync to secondary',
        grabRename: 'Grab Rename',
        qbitSeTag: 'qBit S/E tag',
        qbitCategoryFix: 'qBit Category Fix',
        fileDeleteClean: 'File-delete strip',
        autoStripTagRgOnDelete: 'Auto-strip on delete',
      };
      return map[fn] || fn || '(unknown)';
    },

    // Icon character per status bucket. Plain unicode so we don't add
    // an icon-font dependency. ▲ = something changed, ✓ = checked and
    // OK / no diff, ⚠ = skipped with a reason, ✕ = error.
    webhookFnResultIcon(status) {
      switch (status) {
        case 'change': return '▲';      // ▲
        case 'change-add': return '▲';  // ▲
        case 'change-remove': return '▼'; // ▼
        case 'noop': return '✓';        // ✓
        case 'skipped': return '⚠';     // ⚠
        case 'error': return '✕';       // ✕
        default: return '·';            // ·
      }
    },

    // Background + text color tokens for each status. Returns an
    // object that Alpine binds via :style — values come from the
    // existing tokens.css palette so dark/light themes stay aligned.
    // Mirror .pill.* mappings in components.css: green/red use alpha-,
    // gray uses bg-muted (no alpha-gray exists), amber maps to orange.
    webhookFnResultColors(status) {
      switch (status) {
        case 'change':
        case 'change-add':
          return { bg: 'var(--alpha-green)', fg: 'var(--accent-green)' };
        case 'change-remove':
          return { bg: 'var(--alpha-orange)', fg: 'var(--accent-orange)' };
        case 'noop':
          return { bg: 'var(--bg-muted)', fg: 'var(--text-secondary)' };
        case 'skipped':
          return { bg: 'var(--alpha-orange)', fg: 'var(--accent-orange)' };
        case 'error':
          return { bg: 'var(--alpha-red)', fg: 'var(--accent-red)' };
        default:
          return { bg: 'var(--bg-muted)', fg: 'var(--text-muted-secondary)' };
      }
    },

  };
}

// ===== Cron helpers (M3d) =================================================
//
// Tiny client-side cron evaluator for the schedule modal's Next-5-fires
// preview. Supports the standard 5-field syntax (m h dom mon dow) with
// numbers, '*', '*/N', 'a-b', 'a,b,c'. Backend uses robfig/cron which has
// the same field semantics, so what the UI shows matches what the
// scheduler will fire.
//
// Walks minute-by-minute from "now+1" until it has `count` matches or
// hits a 2-year ceiling. The naive walk is fast enough for the cron
// expressions a sane user types (worst case: monthly on day 31 → ~32
// days of skipping, ~46k iterations per fire; well under one frame in
// JS).
//
// Cron OR semantics on day-of-month + day-of-week: when both are
// restricted (neither '*'), match if EITHER matches — same as Vixie/cron
// and robfig.

function nextCronFires(cron, count, fromDate) {
  const parts = (cron || '').trim().split(/\s+/);
  if (parts.length !== 5) throw new Error('expected 5 fields (m h dom mon dow)');
  const [minF, hourF, domF, monF, dowF] = parts;
  const minSet  = parseCronField(minF,  0, 59);
  const hourSet = parseCronField(hourF, 0, 23);
  const domSet  = parseCronField(domF,  1, 31);
  const monSet  = parseCronField(monF,  1, 12);
  const dowSet  = parseCronField(dowF,  0, 6);
  const domStar = domF === '*';
  const dowStar = dowF === '*';

  const fires = [];
  const t = new Date(fromDate);
  t.setSeconds(0, 0);
  t.setMinutes(t.getMinutes() + 1);

  const maxIter = 2 * 366 * 24 * 60; // ~2 years
  let iter = 0;
  while (fires.length < count && iter < maxIter) {
    iter++;
    const m = t.getMinutes(), h = t.getHours();
    const dom = t.getDate(), mon = t.getMonth() + 1, dow = t.getDay();
    if (minSet.has(m) && hourSet.has(h) && monSet.has(mon)) {
      let dayMatch;
      if (domStar && dowStar)      dayMatch = true;
      else if (domStar)            dayMatch = dowSet.has(dow);
      else if (dowStar)            dayMatch = domSet.has(dom);
      else                         dayMatch = domSet.has(dom) || dowSet.has(dow);
      if (dayMatch) fires.push(new Date(t));
    }
    t.setMinutes(t.getMinutes() + 1);
  }
  if (fires.length === 0) throw new Error('no upcoming fires within 2 years');
  return fires;
}

function parseCronField(spec, lo, hi) {
  const set = new Set();
  for (const part of String(spec).split(',')) {
    let step = 1;
    let body = part;
    const slashIdx = body.indexOf('/');
    if (slashIdx >= 0) {
      step = parseInt(body.slice(slashIdx + 1), 10);
      if (isNaN(step) || step <= 0) throw new Error('bad step in field: ' + part);
      body = body.slice(0, slashIdx);
    }
    let from, to;
    if (body === '*' || body === '') {
      from = lo; to = hi;
    } else if (body.includes('-')) {
      const [a, b] = body.split('-').map(s => parseInt(s, 10));
      if (isNaN(a) || isNaN(b)) throw new Error('bad range: ' + part);
      from = a; to = b;
    } else {
      const v = parseInt(body, 10);
      if (isNaN(v)) throw new Error('bad value: ' + part);
      from = v; to = (slashIdx >= 0) ? hi : v;
    }
    if (from < lo || to > hi || from > to) throw new Error('out of range: ' + part);
    for (let v = from; v <= to; v += step) set.add(v);
  }
  return set;
}

// Reverse-derives the modal's preset id + time/dow/dom pickers from a
// stored cron string so re-opening Edit reflects the saved choice. Only
// recognises the exact strings ruleApplyPreset() would emit; anything
// else collapses to 'custom' so the user can still see and edit it.
// hour24To12 maps a 0-23 clock hour to its 1-12 equivalent. 0 → 12 AM,
// 1-11 → 1-11 (still AM), 12 → 12 PM, 13-23 → 1-11 PM. Used when the
// rule editor initialises its 12h hour-helper field from the canonical
// editingRule.hour. AM/PM is derived separately via `hour >= 12`.
function hour24To12(h) {
  if (!Number.isFinite(h)) return 12;
  h = ((h % 24) + 24) % 24;
  if (h === 0) return 12;
  if (h > 12) return h - 12;
  return h;
}

function derivePresetFromCron(cron) {
  if (!cron) return { id: 'daily', hour: 3, minute: 0, dow: 0, dom: 1 };
  const parts = cron.trim().split(/\s+/);
  if (parts.length !== 5) return { id: 'custom' };
  const [m, h, dom, mon, dow] = parts;
  if (cron === '0 * * * *')    return { id: 'hourly',      hour: 0, minute: 0, dow: 0, dom: 1 };
  if (cron === '0 */6 * * *')  return { id: 'every-6h',    hour: 0, minute: 0, dow: 0, dom: 1 };
  if (cron === '0 */12 * * *') return { id: 'every-12h',   hour: 0, minute: 0, dow: 0, dom: 1 };
  if (cron === '0 0,12 * * *') return { id: 'twice-daily', hour: 0, minute: 0, dow: 0, dom: 1 };
  if (mon !== '*') return { id: 'custom' };
  const mNum = parseInt(m, 10), hNum = parseInt(h, 10);
  if (isNaN(mNum) || isNaN(hNum) || mNum < 0 || mNum >= 60 || hNum < 0 || hNum >= 24) {
    return { id: 'custom' };
  }
  if (dom === '*' && dow === '*') return { id: 'daily',   hour: hNum, minute: mNum, dow: 0, dom: 1 };
  if (dom === '*' && dow !== '*') {
    const d = parseInt(dow, 10);
    if (!isNaN(d) && d >= 0 && d < 7) return { id: 'weekly', hour: hNum, minute: mNum, dow: d, dom: 1 };
  }
  if (dow === '*' && dom !== '*') {
    const d = parseInt(dom, 10);
    if (!isNaN(d) && d >= 1 && d <= 31) return { id: 'monthly', hour: hNum, minute: mNum, dow: 0, dom: d };
  }
  return { id: 'custom' };
}
