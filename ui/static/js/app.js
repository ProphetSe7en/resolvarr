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
    ...appReleaseType(),
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
    ...appDiscoverWizard(),
    ...appTagQualityWizard(),
    ...appRecoverExclusions(),
    ...appQbitInstances(),
    ...appHashRouting(),
    ...appWebhookSubsystem(),
    ...appWebhookSetup(),
    ...appRecentActivity(),
    ...appSecurityPanel(),
    ...appNotificationAgents(),
    ...appScheduleCronSave(),
    ...appQfaDrillin(),
    ...appCleanupWizard(),
    ...appQfaWizards(),
    ...appRuleEditorCore(),
    ...appWebhookConfigWizard(),

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
        // Show the dev banner unless this exact version was dismissed.
        this.devBannerShow = this.isDevBuild && localStorage.getItem('resolvarr-dev-banner-dismissed') !== this.version;
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
      // Same lazy-load for the qBit panel. A refresh while on
      // Settings -> qBittorrent otherwise leaves the instance list empty
      // (only the section-change handler loaded it), so the rows vanish
      // until the user switches tabs and back.
      if (this.currentPage === 'settings' && this.section === 'qbit') {
        this.loadQbitInstances();
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
