// resolvarr UI — schedule-cron-save (extracted from app.js, Stage 4 split).
// Composed via { ...appScheduleCronSave() } in app(); methods bind `this` to the Alpine component.
function appScheduleCronSave() {
  return {
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
