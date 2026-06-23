// resolvarr UI — Grab Rename criteria editor module
//
// Composed into the Alpine root via { ...appGrabRenameEditor() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appGrabRenameEditor() {
  return {
    // ---- Grab Rename criteria editor helpers ----
    //
    // All read off editingRule.grabRename, which openWebhookRuleEditor
    // seeds with default values. Defensive ?? || fallbacks keep the
    // helpers safe even if a future entry path forgets to seed.

    // True when ALL six built-in trigger flags are off AND no custom
    // tokens are defined — backend would reject save with "must enable
    // at least one trigger". Surfaces inline on Step 3b so the user
    // catches it before clicking Save.
    grabRenameNoTriggerSelected() {
      const c = (this.editingRule && this.editingRule.grabRename) || {};
      if (c.triggerOnMissingReleaseGroup) return false;
      if (c.triggerOnMovieVersionMismatch) return false;
      if (c.triggerOnSourceMismatch) return false;
      if (c.triggerOnAudioMismatch) return false;
      if (c.triggerOnSceneMismatch) return false;
      if (c.triggerOnBadNaming) return false;
      if (c.triggerOnFileExtension) return false;
      if (c.triggerAlways) return false;
      if ((c.customTokens || []).length > 0) return false;
      return true;
    },

    // Group blocklist mutators — bind via @click / @input bindings on
    // Step 3b. Direct array push/splice is fine; Alpine reactivity
    // tracks modifications to arrays-on-objects.
    addGrabRenameBlocklist() {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c) return;
      if (!Array.isArray(c.groupBlocklist)) c.groupBlocklist = [];
      c.groupBlocklist.push('');
    },
    removeGrabRenameBlocklist(idx) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.groupBlocklist)) return;
      c.groupBlocklist.splice(idx, 1);
    },
    updateGrabRenameBlocklist(idx, val) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.groupBlocklist)) return;
      c.groupBlocklist[idx] = val;
    },

    // Custom-token mutators — Label:regex pairs. Server-side regex
    // compile is the load-bearing validation; the client try-compile
    // (grabRenameRegexInvalid) catches obvious typos before save.
    addGrabRenameCustomToken() {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c) return;
      if (!Array.isArray(c.customTokens)) c.customTokens = [];
      c.customTokens.push({ label: '', regex: '' });
    },
    removeGrabRenameCustomToken(idx) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.customTokens)) return;
      c.customTokens.splice(idx, 1);
    },
    updateGrabRenameCustomTokenLabel(idx, val) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.customTokens) || !c.customTokens[idx]) return;
      c.customTokens[idx].label = val;
    },
    updateGrabRenameCustomTokenRegex(idx, val) {
      const c = this.editingRule && this.editingRule.grabRename;
      if (!c || !Array.isArray(c.customTokens) || !c.customTokens[idx]) return;
      c.customTokens[idx].regex = val;
    },

    // Returns true when the JS regex engine rejects the source — best-
    // effort client-side check. Server's RE2 engine is the truth source;
    // an empty regex returns false (not invalid, just unset — a separate
    // "regex is required" check covers that on save).
    grabRenameRegexInvalid(regex) {
      if (!regex || !regex.trim()) return false;
      try {
        new RegExp(regex);
        return false;
      } catch (e) {
        return true;
      }
    },
    ruleEditorVisibleTabs()  { return this.ruleEditorTabs.filter(t => this.ruleEditorTabVisible(t.id)); },
    ruleEditorVisibleSteps() {
      // 'review' is always last; others share visibility with their tab.
      return this.ruleEditorSteps.filter(s => s === 'review' || s === 'basics' || this.ruleEditorTabVisible(s));
    },
    // ruleEditorStepLabel — single source of truth for step labels in
    // the wizard's progress strip. Without this, the inline ternary
    // chain in the template missed grabrename / qbitse / qbitcategoryfix
    // (they fell through to "Review"), so on Sonarr where multiple
    // qBit-related steps follow each other the user saw four "Review"
    // labels in a row.
    ruleEditorStepLabel(step) {
      // Match the labels used in ruleEditorTabs[] so wizard-mode
      // progress strip and edit-mode tab strip read identically for
      // the same section.
      const labels = {
        basics:          'Basics',
        filters:         'Filters',
        rg:              'Release Groups',
        audio:           'Audio tags',
        video:           'Video tags',
        dvdetail:        'DV detail',
        missingepisodes: 'Missing episodes',
        tbarefresh:      'TBA refresh',
        plexsync:        'Sync to Plex',
        grabrename:      'qBit Grab Rename',
        qbitse:          'qBit S/E tag',
        qbitcategoryfix: 'qBit category fix',
        schedule:        'Schedule',
        review:          'Review',
      };
      return labels[step] || step;
    },
    ruleEditorJumpToTab(tabId) { if (this.ruleEditorTabVisible(tabId)) this.ruleEditor.activeTab = tabId; },

    // ruleEditorStepBlockedReason returns null when the current step
    // is OK to advance from, or a short explanation string when it's
    // not. Used to gate the Next button (disabled + tooltip + inline
    // hint) so the user can't end up on a Review page only to be
    // sent back when the run dispatches and errors. Validates only
    // the auto-tags steps for now — Basics/Filters/RG already gate
    // through other paths (cron parse, RG count, etc.).
    ruleEditorStepBlockedReason() {
      const r = this.editingRule;
      if (!r) return null;
      const step = this.ruleEditorCurrentStep();
      if (step === 'basics') {
        // Name is required for saved rules (Create flow) — saving with
        // an empty name produces an unidentifiable card later. QFA is
        // exempt because it's a one-shot dispatcher: name auto-fills
        // to "Quick fix-all" and never persists. Block-reason gates
        // both the Next button (UI) and ruleEditorNext (keyboard).
        if (!this.ruleEditor.isQuickFix && !(r.name || '').trim()) {
          return 'Pick a name for the rule before continuing.';
        }
        // Tag-source picking now lives on the RG step (active /
        // discover / filter-only radios). The Basics step deliberately
        // doesn't pre-block on "no active groups" — that pushed the
        // user into a dead-end loop after toggling Discover off in
        // Basics (couldn't advance to RG step where filter-only is
        // selectable). The RG-step gate below handles the no-groups
        // case once the user is actually on the source-picker.
      }
      if (step === 'rg' && this.ruleAffectsTag()) {
        const o = this.editingRule.options || {};
        // Filter-only validates its own per-rule state: tag must be
        // non-empty and must not collide with an existing Active
        // group's Tag for this instance type. Backend would reject
        // either with 4xx; the Next-button gate stops the user
        // before they get there.
        if (o.tagSource === 'filter-only') {
          const t = (o.filterOnlyTag || '').trim();
          if (!t) {
            return 'Enter a tag name for filter-only mode before continuing.';
          }
          if (this.ruleEditorFilterOnlyCollides()) {
            return 'Tag name collides with an existing Active group rule. Pick a different name to continue.';
          }
        } else if (!this.ruleAffectsDiscover() && this.groupsFilteredByInstanceType().length === 0) {
          // Use active groups picked (explicitly or by default) but
          // there are no groups for this instance type and Discover
          // isn't in the chain — Tag pass would be a no-op. Banner
          // above shows the same options as actionable buttons.
          return 'No active release groups yet — Switch to Use filter only above, Add Discover to this rule, or close the wizard and add some via + Add on the Active groups list.';
        }
      }
      if (step === 'filters' && this.ruleAffectsTag()) {
        // Filter is mandatory for Tag quality releases runs after the
        // 2026-05-05 restructure. At least one master (Quality or
        // Audio) must be on. Per-group filtered/simple flag still
        // exists as override but globally we require a filter.
        const f = r.filters || {};
        if (!f.Quality && !f.Audio) {
          return 'Tag quality releases requires at least one filter — enable Quality or Audio above to continue.';
        }
      }
      if (step === 'audio' && this.ruleAffectsAudio()) {
        const a = r.audioTags && r.audioTags.audio;
        if (!a || !a.enabled) {
          return 'Enable the Audio bucket above before continuing — this rule includes the Audio tags phase.';
        }
        const av = a.allowedValues || [];
        // Empty AllowedValues = "all allowed" (engine convention) so
        // that's fine. The toggleAudioTagValue helper auto-disables
        // the bucket when the user un-checks every value, which the
        // first check above catches.
      }
      if (step === 'video' && this.ruleAffectsVideo()) {
        const v = r.videoTags || {};
        const anyOn = ['resolution', 'codec', 'hdr'].some(k => v[k] && v[k].enabled);
        if (!anyOn) {
          return 'Enable Resolution, Codec, or HDR before continuing — this rule includes the Video tags phase.';
        }
      }
      if (step === 'dvdetail' && this.ruleAffectsDvDetail()) {
        const dd = r.dvDetail;
        if (!dd || !dd.enabled) {
          return 'Enable DV detail above before continuing — this rule includes the Dolby Vision detail phase.';
        }
      }
      if (step === 'plexsync' && this.ruleAffectsPlexSync()) {
        const ps = r.plexSync || {};
        if (!ps.plexInstanceId) return 'Pick a Plex server before continuing.';
        if (!ps.libraryKeys || ps.libraryKeys.length === 0) return 'Pick at least one Plex library before continuing.';
        if (!ps.labels || ps.labels.length === 0) return 'Pick at least one tag to sync before continuing.';
        if (!ps.targetTypes || ps.targetTypes.length === 0) return 'Pick Labels and/or Collections before continuing.';
      }
      // Grab Rename — backend rejects rules with no qBit instance, no
      // trigger selected, or a custom-token row missing label/regex /
      // with bad regex. Catch at Next-button time so the user doesn't
      // hit save-failures from the Review step.
      if (step === 'grabrename' && this.ruleAffectsGrabRename()) {
        const c = r.grabRename || {};
        if (!c.qbitInstanceId) {
          return 'Pick a qBit instance for Grab Rename before continuing.';
        }
        if (this.grabRenameNoTriggerSelected()) {
          return 'Enable at least one trigger (or define a custom token) before continuing.';
        }
        const tokens = c.customTokens || [];
        for (let i = 0; i < tokens.length; i++) {
          const t = tokens[i] || {};
          if (!t.label || !String(t.label).trim()) {
            return 'Custom token #' + (i + 1) + ' is missing a label.';
          }
          if (!t.regex || !String(t.regex).trim()) {
            return 'Custom token #' + (i + 1) + ' is missing a regex.';
          }
          if (this.grabRenameRegexInvalid(t.regex)) {
            return 'Custom token #' + (i + 1) + ' has an invalid regex.';
          }
        }
      }
      // qBit Category Fix — backend rejects on empty qbit / missing
      // download-client / equal-or-empty pre/post categories. Pull the
      // canonical reason out of the dedicated helper so the gate and
      // the inline warning stay in sync.
      if (step === 'qbitcategoryfix' && this.ruleAffectsQbitCategoryFix()) {
        const w = this.qbitCategoryFixWarning();
        if (w) return w;
      }
      // Plex label sync — backend rejects on missing Plex instance,
      // empty library list, empty label whitelist, or library-type
      // mismatch with the rule's appType.
      if (step === 'plexlabelsync' && this.ruleAffectsPlexLabelSync()) {
        const p = r.plexLabelSync || {};
        if (!p.plexInstanceId) {
          return 'Pick a Plex instance before continuing.';
        }
        if (!p.libraryKeys || p.libraryKeys.length === 0) {
          return 'Pick at least one Plex library before continuing.';
        }
        if (!p.labels || p.labels.length === 0) {
          return 'Add at least one tag to the whitelist before continuing.';
        }
        if (!p.targetTypes || p.targetTypes.length === 0) {
          return 'Pick at least one Plex target (Labels or Collections).';
        }
      }
      // qBit S/E tag — backend (webhook_rules.go) rejects with empty
      // qbit instance OR no rule enabled OR an enabled rule's tag name
      // failing the regex check. Mirror all three gates here.
      if (step === 'qbitse' && this.ruleAffectsQbitSe()) {
        const q = r.qbitSe || {};
        if (!q.qbitInstanceId) {
          return 'Pick a qBit instance for tagging before continuing.';
        }
        if (this.qbitSeNoRuleEnabled()) {
          return 'Enable Episode, Season, or Unmatched before continuing.';
        }
        if (q.episodeEnabled && !this.qbitSeTagNameValid(q.episodeTag)) {
          return 'Episode tag name must be letters, digits, underscores, or dashes.';
        }
        if (q.seasonEnabled && !this.qbitSeTagNameValid(q.seasonTag)) {
          return 'Season tag name must be letters, digits, underscores, or dashes.';
        }
        if (q.unmatchedEnabled && !this.qbitSeTagNameValid(q.unmatchedTag)) {
          return 'Unmatched tag name must be letters, digits, underscores, or dashes.';
        }
      }
      return null;
    },
    ruleEditorNext() {
      // Hard-stop on the current step's blocked-reason. Belt-and-
      // suspenders alongside the :disabled binding on the button —
      // a keyboard-driven user pressing Enter could otherwise sneak
      // past the visual gate.
      if (this.ruleEditorStepBlockedReason()) return;
      const steps = this.ruleEditorVisibleSteps();
      this.ruleEditor.step = Math.min(steps.length - 1, this.ruleEditor.step + 1);
    },
    ruleEditorPrev() {
      this.ruleEditor.step = Math.max(0, this.ruleEditor.step - 1);
    },
    ruleEditorCurrentStep() { return this.ruleEditorVisibleSteps()[this.ruleEditor.step] || 'basics'; },
    ruleEditorIsLastStep()  { return this.ruleEditor.step === this.ruleEditorVisibleSteps().length - 1; },
    // The single source of truth for "which section is rendering right
    // now" — wizard reads from step index, tabbed-edit reads from
    // activeTab. Section markup checks against this so each block lives
    // in one place regardless of flow.
    ruleEditorCurrentSection() {
      return this.ruleEditor.isCreate ? this.ruleEditorCurrentStep() : this.ruleEditor.activeTab;
    },
  };
}
