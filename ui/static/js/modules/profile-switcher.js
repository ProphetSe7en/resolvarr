// resolvarr UI — profile switcher module
//
// "Profile switcher" (internal id: profile-by-tag): move a movie/series to a
// quality profile based on its tags, via AND / OR / NOT rules. Scan one-off
// (modal wizard + result modal). Composed into the Alpine root via
// { ...appProfileSwitcher() } in app(); methods use `this` = the Alpine
// component (bound when the spread merges them on), so peer calls
// (this.apiFetch, this.showToast, this.instances, ...) work exactly as before.
//
// State lives in modules/state.js: profileByTagWizard, profileByTagResult,
// pbtResultFilter.
function appProfileSwitcher() {
  return {
    pbtAvailableInstances() {
      return (this.instances || []).filter(i => i.type === this.scanAppType);
    },
    pbtNewRule() {
      return { conditions: [{ type: 'tag', value: '', join: 'and', not: false }], profileId: 0 };
    },
    openProfileByTagWizard() {
      const p = this.profileByTagWizard;
      const avail = this.pbtAvailableInstances();
      if (!p.instanceId || !avail.some(i => i.id === p.instanceId)) {
        p.instanceId = avail.length ? avail[0].id : '';
      }
      p.step = 0;
      if (!p.rules || p.rules.length === 0) p.rules = [this.pbtNewRule()];
      p.open = true;
      if (p.instanceId) this.loadProfileByTagPickers();
    },
    closeProfileByTagWizard() {
      this.profileByTagWizard.open = false;
    },
    async loadProfileByTagPickers() {
      const p = this.profileByTagWizard;
      if (!p.instanceId) { p.tags = []; p.profiles = []; return; }
      p.pickersLoading = true;
      p.pickersError = '';
      try {
        const [tr, pr] = await Promise.all([
          this.apiFetch('/api/instances/' + encodeURIComponent(p.instanceId) + '/tags'),
          this.apiFetch('/api/instances/' + encodeURIComponent(p.instanceId) + '/quality-profiles'),
        ]);
        const tags = await tr.json();
        const profs = await pr.json();
        if (!tr.ok) throw new Error((tags && tags.error) || 'tags HTTP ' + tr.status);
        if (!pr.ok) throw new Error((profs && profs.error) || 'profiles HTTP ' + pr.status);
        p.tags = Array.isArray(tags) ? tags : [];
        p.profiles = Array.isArray(profs) ? profs : [];
      } catch (e) {
        p.pickersError = e.message;
        p.tags = []; p.profiles = [];
      } finally {
        p.pickersLoading = false;
      }
    },
    pbtOnInstanceChange() {
      // Tags + profiles are instance-specific, so the rules built against the
      // previous instance no longer apply — reset to a single blank rule.
      this.profileByTagWizard.rules = [this.pbtNewRule()];
      this.loadProfileByTagPickers();
    },
    pbtAddRule() {
      this.profileByTagWizard.rules.push(this.pbtNewRule());
    },
    pbtRemoveRule(i) {
      this.profileByTagWizard.rules.splice(i, 1);
      if (this.profileByTagWizard.rules.length === 0) this.pbtAddRule();
    },
    pbtAddCondition(ruleIdx) {
      this.profileByTagWizard.rules[ruleIdx].conditions.push({ type: 'tag', value: '', join: 'and', not: false });
    },
    pbtToggleNot(ruleIdx, condIdx) {
      const c = this.profileByTagWizard.rules[ruleIdx].conditions[condIdx];
      c.not = !c.not;
    },
    pbtRemoveCondition(ruleIdx, condIdx) {
      const r = this.profileByTagWizard.rules[ruleIdx];
      if (r.conditions.length > 1) r.conditions.splice(condIdx, 1);
    },
    pbtToggleJoin(ruleIdx, condIdx) {
      const c = this.profileByTagWizard.rules[ruleIdx].conditions[condIdx];
      c.join = (c.join === 'or') ? 'and' : 'or';
    },
    pbtTagLabel(tagId) {
      const id = parseInt(tagId, 10);
      const t = (this.profileByTagWizard.tags || []).find(x => x.id === id);
      return t ? t.label : (tagId ? ('tag ' + tagId) : '(choose tag)');
    },
    pbtProfileName(profileId) {
      const id = parseInt(profileId, 10);
      const p = (this.profileByTagWizard.profiles || []).find(x => x.id === id);
      return p ? p.name : (id ? ('profile ' + id) : '(choose profile)');
    },
    pbtRuleValid(rule) {
      if (!rule || !parseInt(rule.profileId, 10)) return false;
      if (!rule.conditions || rule.conditions.length === 0) return false;
      return rule.conditions.every(c => c.value !== '' && c.value != null);
    },
    pbtRulesValid() {
      const rs = this.profileByTagWizard.rules || [];
      return rs.length > 0 && rs.every(r => this.pbtRuleValid(r));
    },
    pbtRuleSummary(rule) {
      if (!rule || !rule.conditions) return '';
      const parts = rule.conditions.map((c, i) =>
        (i > 0 ? (c.join === 'or' ? 'OR ' : 'AND ') : '') + (c.not ? 'NOT ' : '') + this.pbtTagLabel(c.value));
      return parts.join(' ') + ' → ' + this.pbtProfileName(rule.profileId);
    },
    pbtStepValid(step) {
      const p = this.profileByTagWizard;
      if (step === 0) return !!p.instanceId;
      if (step === 1) return this.pbtRulesValid();
      return true;
    },
    pbtNext() {
      const p = this.profileByTagWizard;
      if (!this.pbtStepValid(p.step)) return;
      if (p.step < 2) p.step++;
    },
    pbtPrev() {
      if (this.profileByTagWizard.step > 0) this.profileByTagWizard.step--;
    },
    async pbtRun(mode) {
      const p = this.profileByTagWizard;
      if (!this.pbtRulesValid()) return;
      p.busy = true;
      try {
        const body = {
          arrInstanceId: p.instanceId,
          runMode: mode === 'apply' ? 'apply' : 'preview',
          rules: p.rules.map(r => ({
            profileId: parseInt(r.profileId, 10) || 0,
            conditions: r.conditions.map((c, i) => ({
              type: 'tag',
              value: String(c.value),
              join: i === 0 ? '' : (c.join === 'or' ? 'or' : 'and'),
              not: !!c.not,
            })),
          })),
        };
        const r = await this.apiFetch('/api/profile-by-tag/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.profileByTagResult = d;
        this.pbtResultFilter = 'all';
        p.open = false; // close the wizard; the result modal takes over
        if (d.status === 'error') {
          this.showToast('Profile by tag: ' + (d.error || 'apply error'), 'error');
        } else if (mode === 'apply') {
          this.showToast('Profile by tag applied', 'success');
        }
      } catch (e) {
        this.showToast('Profile by tag failed: ' + e.message, 'error');
      } finally {
        p.busy = false;
      }
    },
    pbtResultMovesCount() {
      return ((this.profileByTagResult || {}).moves || []).length;
    },
    pbtResultConflictsCount() {
      return ((this.profileByTagResult || {}).conflicts || []).length;
    },
    pbtDismissResult() {
      this.profileByTagResult = null;
    },
  };
}
