// resolvarr UI — Plex label sync module
//
// Plex instances (Settings) + Plex label-sync rules (Library scan sub-tab) +
// the one-off Plex sync run. Composed into the Alpine root via
// { ...appPlexLabelSync() } in app(); methods use `this` = the Alpine component
// (bound on spread), so peer calls work exactly as before.
function appPlexLabelSync() {
  return {
    // ---- Plex instances (label-sync Settings tab) ----------------
    // Same CRUD shape as the qBit handlers above; just operate on a
    // different endpoint family. Token preservation mirrors qBit's
    // password preservation — empty / masked input on edit keeps the
    // stored value.

    async loadPlexInstances() {
      try {
        const r = await this.apiFetch('/api/plex-instances');
        if (r.ok) {
          const d = await r.json();
          this.plexInstances = Array.isArray(d) ? d : [];
          this.refreshAllPlexStatus();
        }
      } catch (e) {
        // Silent — page renders empty state until next refresh.
      }
    },

    openPlexInstanceModal(pi) {
      this.plexInstanceModal = {
        open: true,
        id: pi ? pi.id : '',
        name: pi ? pi.name : '',
        url: pi ? pi.url : '',
        token: '', // never pre-populate — masked on edit, blank on create
        trustedCerts: !!(pi && pi.trustedCerts),
        busy: false,
        testing: false,
        testResult: '',
        testOk: false,
      };
    },

    closePlexInstanceModal() {
      if (this.plexInstanceModal.busy) return;
      this.plexInstanceModal.open = false;
    },

    // testPlexInstanceModal — inline-creds probe used before save so
    // the user can verify URL + token without committing. Distinct
    // from testPlexInstance() which probes saved creds for a row.
    async testPlexInstanceModal() {
      const m = this.plexInstanceModal;
      if (!m.url) return;
      m.testing = true;
      m.testResult = '';
      try {
        const r = await this.apiFetch('/api/plex-instances/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: m.id, // empty on create — backend ignores; populated on edit so masked-token path can pull stored creds
            url: m.url,
            token: m.token,
            trustedCerts: m.trustedCerts,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        m.testOk = !!d.ok;
        m.testResult = d.ok ? (d.message || 'Connected') : ('Failed: ' + (d.error || 'unknown'));
        // Bridge into the row-status map so closing the modal doesn't
        // leave the row pill stuck at "Not tested".
        if (m.id) {
          this.plexStatus[m.id] = d.ok ? 'connected' : 'failed';
          this.plexError[m.id] = d.ok ? '' : (d.error || '');
        }
      } catch (e) {
        m.testOk = false;
        m.testResult = 'Failed: ' + e.message;
        if (m.id) {
          this.plexStatus[m.id] = 'failed';
          this.plexError[m.id] = e.message;
        }
      } finally {
        m.testing = false;
      }
    },

    async savePlexInstanceModal() {
      const m = this.plexInstanceModal;
      if (!m.name || !m.url) return;
      m.busy = true;
      try {
        const body = {
          name: m.name,
          url: m.url,
          token: m.token,
          trustedCerts: m.trustedCerts,
        };
        const path = m.id ? '/api/plex-instances/' + m.id : '/api/plex-instances';
        const method = m.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        await this.loadPlexInstances();
        this.plexInstanceModal.open = false;
        this.showToast('Plex instance saved', 'success');
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    async testPlexInstance(id, silent = false) {
      if (!silent) this.plexStatus[id] = 'testing';
      this.plexError[id] = '';
      try {
        const r = await this.apiFetch('/api/plex-instances/' + id + '/test', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.plexStatus[id] = d.ok ? 'connected' : 'failed';
        this.plexError[id] = d.ok ? '' : (d.error || '');
      } catch (e) {
        this.plexStatus[id] = 'failed';
        this.plexError[id] = e.message;
      }
    },

    async refreshAllPlexStatus() {
      for (const pi of (this.plexInstances || [])) {
        if (this.plexStatus[pi.id] === 'testing') continue;
        this.testPlexInstance(pi.id, true);
      }
    },

    confirmDeletePlexInstance(pi) {
      this.deletePlexTarget = pi;
    },

    async deletePlexInstance() {
      const target = this.deletePlexTarget;
      if (!target) return;
      this.deletePlexBusy = true;
      try {
        const r = await this.apiFetch('/api/plex-instances/' + target.id, {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadPlexInstances();
        this.deletePlexTarget = null;
        this.showToast('Plex instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deletePlexBusy = false;
      }
    },

    // fetchPlexLibraries hits the per-instance refresh endpoint, which
    // calls Plex's /library/sections and persists the result back to
    // PlexInstance.Libraries. On success the in-memory list is reloaded
    // so the row's library-count pill updates without a manual refresh.
    async fetchPlexLibraries(id) {
      this.plexLibrariesBusy[id] = true;
      try {
        const r = await this.apiFetch('/api/plex-instances/' + id + '/fetch-libraries', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok || d.ok === false) {
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        await this.loadPlexInstances();
        const count = (d.libraries || []).length;
        this.showToast(count + ' libr' + (count === 1 ? 'y' : 'ies') + ' fetched', 'success');
      } catch (e) {
        this.showToast('Fetch libraries failed: ' + e.message, 'error');
      } finally {
        this.plexLibrariesBusy[id] = false;
      }
    },

    // ---- Plex label-sync rules (Library scan → Plex sync sub-tab) -----
    // plexLabelRuleAvailableInstances filters the Add/Edit modal's
    // Arr-instance dropdown to instances matching the page-level
    // scanAppType. Defensive default: when scanAppType is unset
    // (rare) returns the full list.
    plexLabelRuleAvailableInstances() {
      const want = this.scanAppType;
      if (!want) return this.instances || [];
      return (this.instances || []).filter(i => i.type === want);
    },

    openPlexLabelRuleModal(rule) {
      const editing = !!rule;
      // Pull AppType from the linked instance so the library picker
      // can filter immediately, without waiting for the user to
      // re-pick the instance dropdown.
      let appType = '';
      if (editing) {
        appType = rule.appType || '';
      }
      this.plexLabelRuleModal = {
        open: true,
        id: editing ? rule.id : '',
        name: editing ? rule.name : '',
        enabled: editing ? !!rule.enabled : true,
        instanceId: editing ? rule.instanceId : '',
        appType: appType,
        labels: editing ? [...(rule.labels || [])] : [],
        labelDisplay: editing && rule.labelDisplay ? { ...rule.labelDisplay } : {},
        plexInstanceId: editing && rule.targets && rule.targets[0] ? rule.targets[0].plexInstanceId : '',
        libraryKeys: editing && rule.targets && rule.targets[0] ? [...(rule.targets[0].libraryKeys || [])] : [],
        runMode: editing && rule.runMode ? rule.runMode : 'apply',
        // Empty array treated as ["label"] by the backend for
        // backward compatibility — surfaced explicitly here so the
        // UI checkboxes show the selected state on edit.
        targetTypes: (editing && Array.isArray(rule.targetTypes) && rule.targetTypes.length > 0)
          ? [...rule.targetTypes]
          : ['label'],
        busy: false,
      };
      // Pre-fetch the picked Arr's tag list so the checkbox picker has
      // data immediately. Edit flow has instanceId from the start;
      // Add flow fires the fetch later via onInstanceChange.
      this.plexLabelRuleAvailableTags = [];
      this.plexLabelRuleTagsError = '';
      this.plexLabelRuleTagFilter = '';
      if (this.plexLabelRuleModal.instanceId) {
        this.plexLabelRuleLoadAvailableTags();
      }
    },

    // plexLabelRuleFilteredAvailableTags applies the search filter to
    // the available-tags list. Multi-term OR: whitespace splits the
    // query into terms, a tag matches when it contains ANY term — so
    // "fel mel" surfaces both `fel` and `mel` in one search instead of
    // forcing a search-select-clear-search loop. Empty filter returns
    // the full list. Used by the one-off form AND the webhook step.
    plexLabelRuleFilteredAvailableTags() {
      return this.filterTagsByTerms(this.plexLabelRuleAvailableTags, this.plexLabelRuleTagFilter);
    },

    // filterTagsByTerms — shared multi-term OR filter for the Plex
    // tag-pickers (one-off form, webhook step, wizard step). Splits the
    // query on whitespace; a tag matches when its label contains ANY
    // term. "fel mel" → both `fel` and `mel`. Empty query = full list.
    filterTagsByTerms(list, query) {
      const terms = (query || '').trim().toLowerCase().split(/\s+/).filter(Boolean);
      if (terms.length === 0) return list;
      return (list || []).filter(t => {
        const lbl = (t.label || '').toLowerCase();
        return terms.some(term => lbl.includes(term));
      });
    },

    // plexLabelRuleLoadAvailableTags fetches the live tag list from the
    // currently-picked Arr instance (uses the same /api/instances/{id}/tags
    // endpoint that Tag inventory / Compare use). Drops on AppType
    // change so the picker always reflects the current Arr's tag set.
    async plexLabelRuleLoadAvailableTags() {
      const m = this.plexLabelRuleModal;
      if (!m.instanceId) {
        this.plexLabelRuleAvailableTags = [];
        return;
      }
      this.plexLabelRuleTagsLoading = true;
      this.plexLabelRuleTagsError = '';
      try {
        const r = await this.apiFetch('/api/instances/' + m.instanceId + '/tags');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const list = await r.json();
        // Normalise + sort alphabetically so the checkbox order is
        // predictable across page-reloads.
        const out = (Array.isArray(list) ? list : [])
          .map(t => ({ label: (t.label || '').trim(), usageCount: t.usageCount || 0 }))
          .filter(t => t.label);
        out.sort((a, b) => a.label.localeCompare(b.label));
        this.plexLabelRuleAvailableTags = out;
      } catch (e) {
        this.plexLabelRuleTagsError = e.message;
        this.plexLabelRuleAvailableTags = [];
      } finally {
        this.plexLabelRuleTagsLoading = false;
      }
    },

    // onPlexLabelRuleInstanceChange — fires when the user picks a
    // different Arr instance from the dropdown. Refreshes AppType +
    // tag list; resets library selection (since the new instance's
    // type may differ and library-keys would no longer match).
    onPlexLabelRuleInstanceChange() {
      const m = this.plexLabelRuleModal;
      const inst = (this.instances || []).find(i => i.id === m.instanceId);
      m.appType = inst ? inst.type : '';
      // Library keys are appType-gated; force re-pick on Arr change.
      m.libraryKeys = [];
      this.plexLabelRuleLoadAvailableTags();
    },

    // plexLabelRuleHasLabel — case-insensitive membership check for the
    // checkbox-row's :checked binding. Lets the user see at a glance
    // which available tags are already part of the rule's whitelist.
    plexLabelRuleHasLabel(label) {
      return (this.plexLabelRuleModal.labels || []).some(l => l.toLowerCase() === label.toLowerCase());
    },

    // plexLabelRuleToggleAvailableTag — checkbox click handler. Adds
    // or removes the tag from the rule's labels list, preserving the
    // exact case as stored on the Arr. Drops the matching
    // labelDisplay entry on untick so an unchecked tag doesn't leave
    // a stale override hanging around (backend would drop it on save
    // anyway, but keeping in-modal state tidy avoids confusion if
    // the user re-ticks the tag and expects a fresh slate).
    plexLabelRuleToggleAvailableTag(label) {
      const m = this.plexLabelRuleModal;
      const idx = m.labels.findIndex(l => l.toLowerCase() === label.toLowerCase());
      if (idx === -1) {
        m.labels.push(label);
      } else {
        const removed = m.labels[idx];
        m.labels.splice(idx, 1);
        if (m.labelDisplay && removed in m.labelDisplay) {
          delete m.labelDisplay[removed];
        }
      }
    },

    // plexLabelRuleSetDisplay — write a per-tag override into the
    // labelDisplay map. Empty value deletes the entry so JSON
    // round-trips cleanly (the backend treats empty + missing as
    // equivalent, but we keep client state tidy).
    plexLabelRuleSetDisplay(arrTag, value) {
      const m = this.plexLabelRuleModal;
      if (!m.labelDisplay) m.labelDisplay = {};
      const trimmed = (value || '').trim();
      if (!trimmed || trimmed === arrTag) {
        delete m.labelDisplay[arrTag];
      } else {
        m.labelDisplay[arrTag] = trimmed;
      }
    },

    // plexLabelRuleEffectiveLabel — what the engine will write to Plex
    // for a given Arr tag. Drives the chip-strip badge + the rule-
    // card "Manages:" line so the user can see at a glance what the
    // Plex side will look like.
    plexLabelRuleEffectiveLabel(arrTag, displayMap) {
      const m = displayMap || this.plexLabelRuleModal.labelDisplay || {};
      const v = m[arrTag];
      if (v && v.trim() && v.trim() !== arrTag) return v.trim();
      return arrTag;
    },

    // plexLabelRuleIsCustomLabel — returns true when a label on the
    // rule is NOT in the current Arr's tag list. Either the user typed
    // it manually (preempt-config for a tag they'll create later), or
    // the Arr tag was deleted after the rule was saved. Either way,
    // it's surfaced in the UI with a "not in Arr" hint so the user
    // knows the rule won't match anything until the tag exists.
    plexLabelRuleIsCustomLabel(label) {
      if (this.plexLabelRuleTagsLoading) return false;
      if (!this.plexLabelRuleAvailableTags || this.plexLabelRuleAvailableTags.length === 0) return false;
      return !this.plexLabelRuleAvailableTags.some(t => t.label.toLowerCase() === label.toLowerCase());
    },

    closePlexLabelRuleModal() {
      if (this.plexLabelRuleModal.busy) return;
      this.plexLabelRuleModal.open = false;
    },

    // togglePlexLabelRuleLibraryKey — multi-select checkbox handler.
    // Keeps libraryKeys as a clean string array (no dupes).
    togglePlexLabelRuleLibraryKey(key) {
      const m = this.plexLabelRuleModal;
      const idx = m.libraryKeys.indexOf(key);
      if (idx === -1) {
        m.libraryKeys.push(key);
      } else {
        m.libraryKeys.splice(idx, 1);
      }
    },

    // plexLabelRuleSelectedPlexLibraries — list of libraries on the
    // currently-picked Plex instance, filtered by Arr type (Radarr →
    // movie libs, Sonarr → show libs). Returns the FULL list (no type
    // filter) when AppType isn't picked yet so the user can see what
    // would appear once they finish the form.
    plexLabelRuleSelectedPlexLibraries() {
      const m = this.plexLabelRuleModal;
      if (!m.plexInstanceId) return [];
      const pi = (this.plexInstances || []).find(p => p.id === m.plexInstanceId);
      if (!pi) return [];
      return pi.libraries || [];
    },

    // plexLabelRuleVisibleLibraryCount — count of libraries matching
    // the rule's app-type (drives the "no movie libs available"
    // empty-state message inside the picker).
    plexLabelRuleVisibleLibraryCount() {
      const libs = this.plexLabelRuleSelectedPlexLibraries();
      const want = this.plexLabelRuleWantedLibraryType();
      if (!want) return libs.length;
      return libs.filter(l => l.type === want).length;
    },

    plexLabelRuleWantedLibraryType() {
      // AppType comes from the linked Arr instance. Derive on the fly
      // when the modal hasn't pre-populated it yet (Add-flow before the
      // user picks the Arr).
      const m = this.plexLabelRuleModal;
      let appType = m.appType;
      if (!appType && m.instanceId) {
        const inst = (this.instances || []).find(i => i.id === m.instanceId);
        if (inst) appType = inst.type;
      }
      if (appType === 'radarr') return 'movie';
      if (appType === 'sonarr') return 'show';
      return '';
    },

    plexLabelRuleNoLibrariesMessage() {
      const want = this.plexLabelRuleWantedLibraryType();
      if (!want) return 'Pick an Arr instance above so the library picker can filter to the matching type.';
      if (want === 'movie') return 'No movie libraries cached on this Plex instance. Add a Movies library in Plex, then click Fetch libraries on the Plex row in Settings.';
      return 'No show libraries cached on this Plex instance. Add a TV Shows library in Plex, then click Fetch libraries on the Plex row in Settings.';
    },

    plexLabelRuleLibraryHint() {
      const want = this.plexLabelRuleWantedLibraryType();
      if (!want) return 'Pick the Plex libraries this rule will sync labels into. Multi-select supported.';
      if (want === 'movie') return 'Only movie libraries shown — Radarr instances can only manage labels on movie libraries.';
      return 'Only show libraries shown — Sonarr instances can only manage labels on show libraries.';
    },

    // plexLabelRuleModalValid — UI-side gate for the Save button. Mirrors
    // the backend validator's core checks (server still re-validates).
    plexLabelRuleModalValid() {
      const m = this.plexLabelRuleModal;
      // No name requirement — this is a one-off run form, not a saved
      // rule. Persistence lives on Schedule / Webhook only.
      if (!m.instanceId) return false;
      if (!m.labels || m.labels.length === 0) return false;
      if (!m.plexInstanceId) return false;
      if (!m.libraryKeys || m.libraryKeys.length === 0) return false;
      if (m.runMode !== 'apply' && m.runMode !== 'preview') return false;
      if (!m.targetTypes || m.targetTypes.length === 0) return false;
      return true;
    },

    // plexLabelRuleToggleTargetType — checkbox click handler for the
    // Plex-target picker. Toggles "label" / "collection" on the
    // targetTypes array; preserves order so the UI's read-back of
    // selected state is stable.
    plexLabelRuleToggleTargetType(t) {
      const m = this.plexLabelRuleModal;
      const idx = m.targetTypes.indexOf(t);
      if (idx === -1) {
        m.targetTypes.push(t);
      } else {
        m.targetTypes.splice(idx, 1);
      }
    },

    plexLabelRuleHasTargetType(t) {
      return (this.plexLabelRuleModal.targetTypes || []).includes(t);
    },

    // describePlexLabelRuleTargets — one-line summary for the rule card.
    // "Radarr (Main) → Main Plex / Movies + Movies 4K"
    describePlexLabelRuleTargets(rule) {
      const inst = (this.instances || []).find(i => i.id === rule.instanceId);
      const arrLabel = inst ? inst.name : '(unknown Arr)';
      if (!rule.targets || rule.targets.length === 0) return arrLabel + ' → (no target)';
      const tgt = rule.targets[0];
      const plex = (this.plexInstances || []).find(p => p.id === tgt.plexInstanceId);
      const plexLabel = plex ? plex.name : '(unknown Plex)';
      // Map library keys to titles via the cached library list on the
      // Plex instance. Falls back to the key if the cache hasn't been
      // refreshed.
      const libs = plex ? (plex.libraries || []) : [];
      const libNames = (tgt.libraryKeys || []).map(k => {
        const l = libs.find(x => x.key === k);
        return l ? l.title : k;
      });
      return arrLabel + ' → ' + plexLabel + ' / ' + libNames.join(' + ');
    },

    // ---- One-off Plex label sync run ------------------------------
    // Builds the inline PlexLabelSyncConfig from the form state and
    // POSTs it to /api/plex-sync/run — no saved rule, nothing
    // persisted (matches every other Tag Library sub-tab). On success
    // the form modal closes and the result is shown in the run modal's
    // result stage via a synthetic rule (the run-modal markup is
    // wrapped in x-if="rule").
    async runOneOffPlexSync() {
      const m = this.plexLabelRuleModal;
      if (!this.plexLabelRuleModalValid()) return;
      m.busy = true;
      try {
        // Per-tag display overrides — drop empty / identity / orphan
        // entries client-side (backend re-cleans, but a tidy body
        // mirrors what runs).
        const labelDisplay = {};
        for (const k of Object.keys(m.labelDisplay || {})) {
          const v = (m.labelDisplay[k] || '').trim();
          if (!v || v === k) continue;
          if (!m.labels.some(l => l === k)) continue;
          labelDisplay[k] = v;
        }
        const body = {
          arrInstanceId: m.instanceId,
          runMode: m.runMode || 'apply',
          plexLabelSync: {
            plexInstanceId: m.plexInstanceId,
            libraryKeys: m.libraryKeys,
            labels: m.labels,
            labelDisplay: labelDisplay,
            targetTypes: (m.targetTypes && m.targetTypes.length > 0) ? m.targetTypes : ['label'],
          },
        };
        const r = await this.apiFetch('/api/plex-sync/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Close the form, surface the result in the run modal. The run
        // modal's markup is wrapped in x-if="rule", so we hand it a
        // synthetic rule shaped just enough for the header +
        // describePlexLabelRuleTargets() (name + instanceId + first
        // target). No persistence — this object lives only for the
        // result view.
        const syntheticRule = {
          name: 'Plex label sync',
          instanceId: m.instanceId,
          targetTypes: body.plexLabelSync.targetTypes,
          labelDisplay: body.plexLabelSync.labelDisplay,
          targets: [{
            plexInstanceId: m.plexInstanceId,
            libraryKeys: m.libraryKeys,
          }],
        };
        m.open = false;
        this.plexLabelRunModal = {
          open: true,
          stage: 'result',
          rule: syntheticRule,
          runMode: d.runMode || (m.runMode || 'apply'),
          result: d,
          error: '',
          detailsFilter: '',
        };
      } catch (e) {
        this.showToast('Plex sync failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    // The run modal is opened directly in the result stage by
    // runOneOffPlexSync + the QFA result drill-in (viewPhaseDetails).
    closePlexLabelRunModal() {
      this.plexLabelRunModal.open = false;
    },

    // Apply-now for a Plex-sync PREVIEW shown in the run modal — re-fires
    // the SAME config in apply mode so the user doesn't have to re-run the
    // whole rule with runMode flipped. applyConfig is stamped on the phase
    // by buildActivityResult (schedule runs) / runQuickFixChain (QFA); a
    // result with no applyConfig (no source config) hides the button. On
    // success the modal swaps to the apply result, so the PREVIEW pill +
    // this button disappear. Mirrors the audio/video Apply-now affordance.
    async applyPlexSyncFromPreview() {
      const m = this.plexLabelRunModal;
      if (!m || !m.applyConfig || m.applying) return;
      m.applying = true;
      try {
        const resp = await this.apiFetch('/api/plex-sync/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            arrInstanceId: m.applyConfig.arrInstanceId,
            runMode: 'apply',
            plexLabelSync: m.applyConfig.plexLabelSync,
          }),
        });
        const d = await resp.json();
        if (!resp.ok) throw new Error(d.error || 'HTTP ' + resp.status);
        m.result = d;
        m.runMode = d.runMode || 'apply';
        this.showToast('Plex sync applied: ' + (d.summary || 'done'), 'success');
      } catch (e) {
        this.showToast('Plex sync apply failed: ' + (e.message || 'unknown'), 'error');
      } finally {
        m.applying = false;
      }
    },

    // Helpers for the result modal — keep the template tidy by
    // hoisting the count math out of x-text expressions.
    plexLabelRunAddedTotal(result) {
      if (!result || !result.added) return 0;
      return Object.values(result.added).reduce((a, b) => a + b, 0);
    },

    plexLabelRunRemovedTotal(result) {
      if (!result || !result.removed) return 0;
      return Object.values(result.removed).reduce((a, b) => a + b, 0);
    },

    plexLabelRunInSyncTotal(result) {
      if (!result || !result.inSync) return 0;
      return Object.values(result.inSync).reduce((a, b) => a + b, 0);
    },

    // plexLabelRunLabelSummary — flattens Added / Removed / InSync
    // maps into one sorted per-label table for the result modal. Each
    // entry: { label, added, removed, inSync }. Includes labels that
    // appear in ANY of the three maps so the user sees all four
    // numbers per label even when one bucket is zero.
    plexLabelRunLabelSummary(result) {
      if (!result) return [];
      const labels = new Set();
      for (const k of Object.keys(result.added || {})) labels.add(k);
      for (const k of Object.keys(result.removed || {})) labels.add(k);
      for (const k of Object.keys(result.inSync || {})) labels.add(k);
      const out = [];
      for (const lbl of labels) {
        out.push({
          label: lbl,
          added: (result.added && result.added[lbl]) || 0,
          removed: (result.removed && result.removed[lbl]) || 0,
          inSync: (result.inSync && result.inSync[lbl]) || 0,
        });
      }
      out.sort((a, b) => a.label.localeCompare(b.label));
      return out;
    },

    // plexLabelRunFilteredPerLabel — applies the detailsFilter (label
    // name) to the per-item change list so the user can drill into a
    // single label at a time.
    plexLabelRunFilteredPerLabel(result) {
      if (!result || !result.perLabel) return [];
      const filter = (this.plexLabelRunModal.detailsFilter || '').trim().toLowerCase();
      if (!filter) return result.perLabel;
      return result.perLabel.filter(c => (c.label || '').toLowerCase() === filter);
    },

    // Status pill colour mapping — same legend the engine returns:
    //   "ok"      every library + every item synced cleanly
    //   "partial" something went wrong on a subset (per-item errors)
    //   "error"   couldn't fire at all (Plex unreachable, missing
    //             instance, etc.)
    plexLabelRunStatusStyle(status) {
      if (status === 'ok')      return 'background:var(--alpha-green);color:var(--accent-green)';
      if (status === 'partial') return 'background:var(--alpha-orange,rgba(255,165,0,0.15));color:var(--accent-orange,#ff9800)';
      if (status === 'error')   return 'background:var(--accent-red-bg);color:var(--accent-red)';
      return 'background:var(--bg-muted);color:var(--text-muted)';
    },
  };
}
