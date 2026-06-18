// resolvarr UI — Per-instance-type mode availability module
//
// Composed into the Alpine root via { ...appModeAvailability() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appModeAvailability() {
  return {
    // ---- Per-instance-type mode availability ----
    // Single source of truth for which modes apply to which Arr type.
    // Sonarr today only supports recover; everything else is Radarr-only
    // until the per-episode-file refactor lands (M-Sonarr). The wizard
    // mode dropdown + Combined chain checkboxes filter through this so
    // users on a Sonarr instance never see Radarr-only options that
    // would 501 at scan time.
    ruleModeCatalog: [
      { value: 'tag',       label: 'Tag quality releases — tag movies whose release group passes your filters',      appliesTo: ['radarr'] },
      { value: 'discover',  label: 'Discover — find new release-groups in your library',                            appliesTo: ['radarr'] },
      { value: 'recover',   label: 'Recover — fill missing release-group fields from grab history',                 appliesTo: ['radarr', 'sonarr'] },
      { value: 'audiotags', label: 'Tag Audio — informative tags from audio mediaInfo (codec / channels / atmos)',  appliesTo: ['radarr', 'sonarr'] },
      { value: 'videotags', label: 'Tag Video — informative tags from video mediaInfo (resolution / codec / HDR)',  appliesTo: ['radarr', 'sonarr'] },
      { value: 'dvdetail',  label: 'Tag DV Details — Dolby Vision profile / CM tags (requires ffmpeg + dovi_tool)', appliesTo: ['radarr'] },
      { value: 'combined',  label: 'Combined — chain several of the above in one run',                              appliesTo: ['radarr', 'sonarr'] },
    ],
    // ruleCombinedSubstepCatalog drives the chain-step checkboxes on
    // Basics. M-Sonarr extension contract: when Sonarr support lands
    // for a phase, add 'sonarr' to the corresponding appliesTo array
    // here AND wire the matching Sonarr scan path in
    // internal/api/scan*.go. The wizard, instance dropdown, tab
    // visibility, and chain runner all key off appliesTo — no other
    // frontend changes needed.
    //
    // Sonarr coverage today: recover + audiotags + videotags. Tag
    // library + discover land with M-Sonarr Phase 2 (per-episode-file
    // walk for the release-group tagging path). DV detail stays
    // Radarr-only — extraction is per-file and series-level
    // aggregation isn't meaningful.
    ruleCombinedSubstepCatalog: [
      { value: 'discover',        label: 'Discover new release-groups',         appliesTo: ['radarr'] },
      { value: 'recover',         label: 'Recover missing release-groups',      appliesTo: ['radarr', 'sonarr'] },
      { value: 'tag',             label: 'Tag quality releases',                  appliesTo: ['radarr'] },
      { value: 'audiotags',       label: 'Tag Audio',                           appliesTo: ['radarr', 'sonarr'] },
      { value: 'videotags',       label: 'Tag Video',                           appliesTo: ['radarr', 'sonarr'] },
      { value: 'dvdetail',        label: 'Tag DV Details',                      appliesTo: ['radarr'], optIn: true },
      { value: 'missingepisodes', label: 'Find missing episodes',               appliesTo: ['sonarr'] },
      { value: 'plexsync',        label: 'Sync to Plex',                        appliesTo: ['radarr', 'sonarr'], optIn: true },
      { value: 'tbarefresh',      label: 'TBA refresh',                         appliesTo: ['sonarr'], optIn: true },
      { value: 'qbitsetag',       label: 'qBit S/E tags',                       appliesTo: ['sonarr'], optIn: true },
    ],
    ruleEditorInstanceType() {
      // Locked at open-time on ruleEditor.appType (Create/QFA seed from
      // scanAppType, Edit seeds from existing rule's instance.type) so
      // the wizard can't mid-flight cross the Arr-type boundary.
      if (this.ruleEditor && this.ruleEditor.appType) return this.ruleEditor.appType;
      // Fallback only for transitional state where appType isn't set —
      // resolve from the rule's current instance.
      const r = this.editingRule;
      if (!r) return null;
      const inst = (this.instances || []).find(i => i.id === r.instanceId);
      return inst ? inst.type : null;
    },
    ruleEditorInstancesAvailable() {
      const t = this.ruleEditorInstanceType();
      return (this.instances || [])
        .filter(i => !t || i.type === t)
        .sort((a, b) => a.name.localeCompare(b.name));
    },
    ruleModeOptionsForInstance() {
      const t = this.ruleEditorInstanceType();
      if (!t) return this.ruleModeCatalog;
      return this.ruleModeCatalog.filter(o => o.appliesTo.includes(t));
    },
    ruleCombinedSubstepsForInstance() {
      const t = this.ruleEditorInstanceType();
      if (!t) return this.ruleCombinedSubstepCatalog;
      return this.ruleCombinedSubstepCatalog.filter(s => s.appliesTo.includes(t));
    },
    ruleDefaultModeForInstanceType(type) {
      if (type === 'sonarr') return 'recover';
      return 'tag';
    },

  };
}
