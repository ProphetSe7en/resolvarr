// resolvarr UI — notification-agents (extracted from app.js, Stage 4 split).
// Composed via { ...appNotificationAgents() } in app(); methods bind `this` to the Alpine component.
function appNotificationAgents() {
  return {
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



  };
}
