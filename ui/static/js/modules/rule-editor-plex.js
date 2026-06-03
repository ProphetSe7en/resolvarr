// resolvarr UI - rule-editor Plex-sync step module
//
// The wizardPlex* helpers backing the QFA / Create-rule / schedule
// wizard's Plex label-sync step (tag picker, per-tag display override,
// library + target selection). Self-contained; touches only
// editingRule.plexSync + plexInstances. Extracted verbatim from app.js
// (Stage 4). Composed via { ...appRuleEditorPlex() } in app(). No overlap
// with the discover/recover/tag flows.
function appRuleEditorPlex() {
  return {
    async wizardPlexLoadTags() {
      const r = this.editingRule;
      // The schedule / QFA wizard can open on a page that never loaded
      // the Plex server list (it's loaded per-page, like the webhooks
      // page). Without this the step shows a false "No Plex servers
      // configured" banner. Pull it on step entry if it's empty.
      if (!this.plexInstances || this.plexInstances.length === 0) {
        await this.loadPlexInstances();
      }
      if (!r || !r.instanceId) { this.wizardPlexAvailableTags = []; return; }
      this.wizardPlexTagsLoading = true;
      this.wizardPlexTagsError = '';
      try {
        const resp = await this.apiFetch('/api/instances/' + r.instanceId + '/tags');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const list = await resp.json();
        const out = (Array.isArray(list) ? list : [])
          .map(t => ({ label: (t.label || '').trim(), usageCount: t.usageCount || 0 }))
          .filter(t => t.label);
        out.sort((a, b) => a.label.localeCompare(b.label));
        this.wizardPlexAvailableTags = out;
      } catch (e) {
        this.wizardPlexTagsError = e.message;
        this.wizardPlexAvailableTags = [];
      } finally {
        this.wizardPlexTagsLoading = false;
      }
    },
    wizardPlexFilteredTags() {
      return this.filterTagsByTerms(this.wizardPlexAvailableTags, this.wizardPlexTagFilter);
    },
    wizardPlexHasLabel(label) {
      const ps = this.editingRule && this.editingRule.plexSync;
      return ps ? (ps.labels || []).some(l => l.toLowerCase() === label.toLowerCase()) : false;
    },
    wizardPlexToggleTag(label) {
      const ps = this.editingRule.plexSync;
      const idx = ps.labels.findIndex(l => l.toLowerCase() === label.toLowerCase());
      if (idx === -1) {
        ps.labels.push(label);
      } else {
        const removed = ps.labels[idx];
        ps.labels.splice(idx, 1);
        if (ps.labelDisplay && removed in ps.labelDisplay) delete ps.labelDisplay[removed];
      }
    },
    wizardPlexSetDisplay(arrTag, value) {
      const ps = this.editingRule.plexSync;
      if (!ps.labelDisplay) ps.labelDisplay = {};
      const t = (value || '').trim();
      if (!t || t === arrTag) delete ps.labelDisplay[arrTag];
      else ps.labelDisplay[arrTag] = t;
    },
    wizardPlexEffectiveLabel(arrTag) {
      const m = (this.editingRule.plexSync && this.editingRule.plexSync.labelDisplay) || {};
      const v = m[arrTag];
      if (v && v.trim() && v.trim() !== arrTag) return v.trim();
      return arrTag;
    },
    wizardPlexIsCustomLabel(label) {
      if (this.wizardPlexTagsLoading) return false;
      if (!this.wizardPlexAvailableTags || this.wizardPlexAvailableTags.length === 0) return false;
      return !this.wizardPlexAvailableTags.some(t => t.label.toLowerCase() === label.toLowerCase());
    },
    wizardPlexWantedLibType() {
      const t = this.ruleEditorInstanceType();
      if (t === 'radarr') return 'movie';
      if (t === 'sonarr') return 'show';
      return '';
    },
    wizardPlexSelectedLibraries() {
      const ps = this.editingRule && this.editingRule.plexSync;
      if (!ps || !ps.plexInstanceId) return [];
      const pi = (this.plexInstances || []).find(p => p.id === ps.plexInstanceId);
      return pi ? (pi.libraries || []) : [];
    },
    wizardPlexVisibleLibraryCount() {
      const want = this.wizardPlexWantedLibType();
      const libs = this.wizardPlexSelectedLibraries();
      return want ? libs.filter(l => l.type === want).length : libs.length;
    },
    wizardPlexToggleLibrary(key) {
      const ps = this.editingRule.plexSync;
      const idx = ps.libraryKeys.indexOf(key);
      if (idx === -1) ps.libraryKeys.push(key);
      else ps.libraryKeys.splice(idx, 1);
    },
    wizardPlexToggleTarget(t) {
      const ps = this.editingRule.plexSync;
      if (!ps.targetTypes) ps.targetTypes = [];
      const idx = ps.targetTypes.indexOf(t);
      if (idx === -1) ps.targetTypes.push(t);
      else ps.targetTypes.splice(idx, 1);
    },
    wizardPlexHasTarget(t) {
      const ps = this.editingRule && this.editingRule.plexSync;
      return ps ? (ps.targetTypes || []).includes(t) : false;
    },
  };
}
