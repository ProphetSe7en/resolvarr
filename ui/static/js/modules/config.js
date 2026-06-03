// resolvarr UI - config-snapshots module
//
// Builders that produce a rule's config snapshot from the live globals
// / sensible defaults: defaultRule* + snapshotGlobal* + snapshotDefault*.
// These seed QFA / Create-rule / Webhook-rule editors. Extracted verbatim
// from app.js (Stage 4). Read this.filters/audioTags/videoTags/dvDetail/
// instances/groups (provided by appState) and return plain objects — no
// behaviour change. Composed via { ...appConfigSnapshots() } in app().
function appConfigSnapshots() {
  return {
    // Default snapshots used when a brand-new rule is being created or
    // when an older row is missing a snapshot field. PascalCase mirrors
    // engine.FilterConfig's wire shape (no JSON tags on the Go side).
    defaultRuleFilters() {
      return {
        Quality: true, MAWebDL: true, PlayWebDL: true,
        Audio: true, TrueHD: true, TrueHDAtmos: true,
        DTSX: true, DTSHDMA: true,
      };
    },
    defaultRuleAudioTags() {
      return {
        audio: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
        removeOrphanedTags: false,
      };
    },
    defaultRuleVideoTags() {
      return {
        resolution: { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
        codec:      { enabled: false, prefix: '', sonarrAggregation: 'all-occurring', allowedValues: [], selectMode: '' },
        hdr:        { enabled: false, prefix: '', sonarrAggregation: 'strict',         allowedValues: [], selectMode: '' },
        removeOrphanedTags: false,
      };
    },
    defaultRuleDvDetail() {
      return { enabled: false, prefix: '', allowedValues: [], removeOrphanedTags: false };
    },
    // Snapshot the live global Filters block (Radarr-side — UI only
    // edits that side today) into the per-rule wire shape.
    snapshotGlobalFilters() {
      return {
        Quality: this.filters.quality, MAWebDL: this.filters.maWebDL, PlayWebDL: this.filters.playWebDL,
        Audio: this.filters.audio, TrueHD: this.filters.trueHD, TrueHDAtmos: this.filters.trueHDAtmos,
        DTSX: this.filters.dtsX, DTSHDMA: this.filters.dtsHDMA,
      };
    },
    // Per-snapshot helpers — each returns an independent deep-clone of
    // the matching global config so the rule editor can mutate freely.
    // removeOrphanedTags is FORCED off in the snapshot regardless of
    // the global setting: rules are user-explicit by definition (you
    // sit in the wizard and tick what you want), and a destructive
    // cleanup that silently inherits from a Library-scan tab toggle
    // most users don't remember setting is exactly the kind of
    // surprise the wizard flow is meant to prevent. The user can
    // still tick the orphan-cleanup checkbox on the audio/video/dv
    // step explicitly if they want it.
    snapshotGlobalAudioTags() {
      const snap = JSON.parse(JSON.stringify(this.audioTags));
      // Destructive opt-ins reset per-rule — globals never carry these
      // forward to a fresh rule snapshot. User opts in explicitly.
      snap.removeOrphanedTags = false;
      snap.stripOnFileDelete = false;
      return snap;
    },
    snapshotGlobalVideoTags() {
      const snap = JSON.parse(JSON.stringify(this.videoTags));
      snap.removeOrphanedTags = false;
      snap.stripOnFileDelete = false;
      return snap;
    },
    snapshotGlobalDvDetail() {
      const snap = JSON.parse(JSON.stringify(this.dvDetail));
      snap.removeOrphanedTags = false;
      snap.stripOnFileDelete = false;
      return snap;
    },
    // Per-rule snapshot of the global Missing-Episodes config. Used by
    // the QFA + Create-Rule wizards when missingepisodes is in
    // combinedModes. The wizard's actionTag / actionSearch live on the
    // snapshot itself so each rule (or QFA chain) picks its own
    // application semantics independently of the standalone tab's
    // last-used setting.
    snapshotGlobalMissingEpisodes() {
      const c = this.missingEpisodesConfig || {};
      return {
        thresholdPercent: c.thresholdPercent ?? 70,
        bufferHours: (c.bufferHours === undefined || c.bufferHours === null || c.bufferHours === '') ? 24 : Number(c.bufferHours),
        includeContinuing: c.includeContinuing !== false,
        includeEnded: c.includeEnded !== false,
        includeSpecials: !!c.includeSpecials,
        tagName: ((c.tagName || 'missing-episodes') + '').trim() || 'missing-episodes',
        // Default: Tag on, Search off. Tag is informative + auto-cleans;
        // Search fires Sonarr indexer calls, more aggressive.
        actionTag: true,
        actionSearch: false,
      };
    },
    // Blank Plex-sync snapshot for the wizard's plexsync step. Unlike
    // the auto-tag snapshots there is NO global Plex-sync config to
    // copy from (one-off / schedule / webhook / qfa each own their
    // inline config), so this is just an empty starting point the user
    // fills in on the Plex sync step.
    snapshotDefaultPlexSync() {
      return {
        plexInstanceId: '',
        libraryKeys: [],
        labels: [],
        labelDisplay: {},
        targetTypes: ['label'],
      };
    },
    // Blank TBA-refresh snapshot for the wizard's tbarefresh phase
    // (Sonarr-only). No global to copy from; sensible default is all
    // monitored series, specials off.
    snapshotDefaultTbaRefresh() {
      return {
        includeContinuing: true,
        includeEnded: true,
        includeSpecials: false,
      };
    },
    // Subset of cfg.ReleaseGroups[].id matching the rule's instance type
    // AND currently Enabled. Used to seed editingRule.releaseGroupIds
    // when the user opens the wizard — gives them a sensible default
    // ("everything I currently track") that they can prune in the RG
    // step.
    snapshotGlobalRGIds(instanceId) {
      const inst = this.instances.find(i => i.id === instanceId);
      if (!inst) return [];
      return this.groups.filter(g => g.type === inst.type && g.enabled).map(g => g.id);
    },
  };
}
