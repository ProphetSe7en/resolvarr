// resolvarr UI — rule-editor-core (extracted from app.js, Stage 4 split).
// Composed via { ...appRuleEditorCore() } in app(); methods bind `this` to the Alpine component.
function appRuleEditorCore() {
  return {
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

  };
}
