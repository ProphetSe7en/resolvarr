// resolvarr UI — tag-quality-wizard (extracted from app.js, Stage 4 split).
// Composed via { ...appTagQualityWizard() } in app(); methods bind `this` to the Alpine component.
function appTagQualityWizard() {
  return {
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

  };
}
