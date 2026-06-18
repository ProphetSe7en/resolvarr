// resolvarr UI — Mode / tab visibility helpers module
//
// Composed into the Alpine root via { ...appModeVisibility() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appModeVisibility() {
  return {
    // ---- Mode / tab visibility helpers ----
    //
    // Each helper checks BOTH the legacy schedule-mode flag (mode +
    // combinedModes) AND the webhook-mode function flag (options.fnX,
    // set by the Basics step's function checkboxes when kind='webhook').
    // The unified rule editor uses the same RG / Filters / Audio /
    // Video / DV / Recover / Sync steps for both kinds — these helpers
    // are how the wizard decides which steps to show.
    ruleAffectsTag()       {
      const r = this.editingRule; if (!r) return false;
      if (r.mode === 'tag' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('tag'))) return true;
      return !!(r.options && r.options.fnTagReleaseGroups);
    },
    ruleAffectsDiscover()  {
      const r = this.editingRule; if (!r) return false;
      if (r.mode === 'discover' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('discover'))) return true;
      return !!(r.options && r.options.fnDiscover);
    },

    // Tag-phase release-group requirement. Returns true if validation
    // should reject an empty releaseGroupIds list. Bypassed when:
    //   - there is no Tag phase in the rule (Discover-only rules don't
    //     need a pre-selected RG list — discover scans the library
    //     independent of selection); OR
    //   - Discover IS in the chain AND it's set to write-back AND
    //     auto-activate. In that case the chain runner injects the
    //     newly-discovered group IDs into the overlay before the Tag
    //     phase fires, so an initially-empty list resolves at runtime.
    //     This is the canonical "first-time setup with no groups yet"
    //     flow — must not be blocked at the wizard.
    tagPhaseNeedsGroups() {
      if (!this.ruleAffectsTag()) return false;
      // Filter-only tags every movie passing the quality+audio filter
      // (release group ignored). The RG picker is moot for this path,
      // so the save validator must not require it. Without this guard
      // the user gets "Pick at least one Release Group..." even after
      // explicitly choosing Use filter only on the tag-source step.
      const tagSource = (this.editingRule && this.editingRule.options && this.editingRule.options.tagSource) || '';
      if (tagSource === 'filter-only') return false;
      // Discover-in-chain bypasses the requirement entirely.
      // Rationale (2026-05-05): if user picks "Add + leave disabled"
      // Discover still augments the Active list (just with Enabled
      // off). Tag phase then runs against whatever's currently
      // enabled; if that's empty, the result is a no-op (0 movies
      // tagged), not an error. The user explicitly chose Discover —
      // we trust their intent rather than blocking the run because
      // autoActivate is off. Previously this check required BOTH
      // discoverWriteBack AND autoActivateDiscovered, which produced
      // a misleading "enable Discover with Add + enable" error even
      // when Discover was already enabled with Add + disabled.
      if (this.ruleAffectsDiscover()) return false;
      return true;
    },

    // Inline action for the "no active groups + Discover not in rule"
    // banner on the tag-source step. Adds Discover to the rule chain
    // without forcing the user back to the Basics step. Mode flips to
    // 'combined' when needed; tagSource flips to 'discover' so the
    // freshly-enabled chain is the picked source.
    enableDiscoverFromTagSourceBanner() {
      const r = this.editingRule;
      if (!r) return;
      if (r.mode === 'tag') {
        r.mode = 'combined';
        r.options.combinedModes = ['tag', 'discover'];
      } else if (r.mode === 'combined') {
        if (!Array.isArray(r.options.combinedModes)) r.options.combinedModes = [];
        if (!r.options.combinedModes.includes('discover')) r.options.combinedModes.push('discover');
      }
      this.ensureDiscoverDefaults();
      r.options.tagSource = 'discover';
    },
    ruleAffectsRecover()   {
      const r = this.editingRule; if (!r) return false;
      if (r.mode === 'recover' || (r.mode === 'combined' && (r.options.combinedModes || []).includes('recover'))) return true;
      return !!(r.options && r.options.fnRecover);
    },
    // Webhook-only function affects-helpers — schedule rules never
    // set these so they stay false for that path.
    ruleAffectsSyncToSecondaryFn() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnSyncToSecondary;
    },
    // ruleAffectsAutoTags: any of the three auto-tag sub-flows is
    // selected. Used for cross-cutting gates (e.g. the
    // AutoTagsRunOnSecondary checkbox + Review-step summary).
    ruleAffectsAutoTags() {
      return this.ruleAffectsAudio() || this.ruleAffectsVideo() || this.ruleAffectsDvDetail();
    },
    // ruleAffectsAudio: gates the Audio tags wizard step.
    ruleAffectsAudio() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'audiotags') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('audiotags')) return true;
      return !!(r.options && r.options.fnTagAudio);
    },
    // ruleAffectsVideo: gates the Video tags wizard step (resolution
    // / codec / HDR buckets only — DV detail is its own step now).
    ruleAffectsVideo() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'videotags') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('videotags')) return true;
      return !!(r.options && r.options.fnTagVideo);
    },
    // ruleAffectsDvDetail: gates the DV detail wizard step. Lives
    // on its own fane + step because it requires extra tools and
    // most users won't run it.
    ruleAffectsDvDetail() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'dvdetail') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('dvdetail')) return true;
      return !!(r.options && r.options.fnTagDvDetail);
    },
    // ruleAffectsMissingEpisodes: gates the Missing Episodes wizard
    // step + chain phase. Sonarr-only; the catalog entry's appliesTo
    // makes the checkbox invisible on Radarr Basics, but defence-in-
    // depth keeps the helper returning false if a stale combinedModes
    // value lands on a Radarr rule.
    ruleAffectsMissingEpisodes() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'missingepisodes') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('missingepisodes')) return true;
      return false;
    },
    ruleAffectsTbaRefresh() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'tbarefresh') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('tbarefresh')) return true;
      return false;
    },
    // Read-only one-line summaries for the Review step. Config itself
    // lives on the dedicated Missing episodes / TBA refresh steps.
    reviewMissingEpisodesSummary() {
      const m = this.editingRule && this.editingRule.missingEpisodes;
      if (!m) return '';
      const series = [];
      if (m.includeContinuing) series.push('continuing');
      if (m.includeEnded) series.push('ended');
      if (m.includeSpecials) series.push('specials');
      const actions = [];
      if (m.actionTag) actions.push('tag "' + (m.tagName || 'missing-episodes') + '"');
      if (m.actionSearch) actions.push('search');
      const actionStr = actions.length ? actions.join(' + ') : 'preview only';
      return `${m.thresholdPercent ?? 70}% coverage · ${m.bufferHours ?? 24}h buffer · ${series.join('/') || 'no series picked'} · on apply: ${actionStr}`;
    },
    reviewTbaRefreshSummary() {
      const t = this.editingRule && this.editingRule.tbaRefresh;
      if (!t) return '';
      const series = [];
      if (t.includeContinuing) series.push('continuing');
      if (t.includeEnded) series.push('ended');
      if (t.includeSpecials) series.push('specials');
      return `${series.join('/') || 'no series picked'} · renames every TBA file found on apply`;
    },




  };
}
