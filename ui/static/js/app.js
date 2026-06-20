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
