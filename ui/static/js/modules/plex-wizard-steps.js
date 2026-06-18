// resolvarr UI — Plex sync wizard step helpers module
//
// Composed into the Alpine root via { ...appPlexWizardSteps() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appPlexWizardSteps() {
  return {
    // ---- Wizard "Sync to Plex" step ------------------------------
    // All bound to editingRule.plexSync. Mirrors the one-off form's
    // picker logic but on isolated wizardPlex* state so the verified
    // one-off flow is untouched. The Arr instance is the wizard's
    // editingRule.instanceId (locked in the Basics step).
    ruleAffectsPlexSync() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.mode === 'plexsync') return true;
      if (r.mode === 'combined' && (r.options.combinedModes || []).includes('plexsync')) return true;
      return false;
    },
    // wizardPlex* (rule-editor Plex-sync step) moved to
    // js/modules/rule-editor-plex.js (appRuleEditorPlex), composed in app().
    // Derived rule mode: when no quality / audio master is on, the
    // engine's CheckQuality + CheckAudio short-circuit to true, which
    // makes every filtered-group match tag — same outcome as simple
    // mode. So we present this as a single rule-level concept rather
    // than asking the user to think per-group.
    //
    // Returns 'simple' / 'filtered' / null. null = the rule doesn't
    // touch a tagging phase, so the badge is meaningless.
    ruleEffectiveMode() {
      const r = this.editingRule;
      if (!r) return null;
      if (!this.ruleAffectsTag() && !this.ruleAffectsDiscover()) return null;
      const f = r.filters || {};
      return (f.Quality || f.Audio) ? 'filtered' : 'simple';
    },
    // Same derivation but for an arbitrary saved schedule (list view).
    // Schedules with both masters off OR not affecting Tag/Discover get
    // 'simple'; the Tag-touching ones with any filter on get 'filtered'.
    scheduleEffectiveMode(sj) {
      if (!sj) return null;
      const touchesTag = sj.mode === 'tag' || (sj.mode === 'combined' && (sj.options?.combinedModes || []).includes('tag'));
      const touchesDiscover = sj.mode === 'discover' || (sj.mode === 'combined' && (sj.options?.combinedModes || []).includes('discover'));
      if (!touchesTag && !touchesDiscover) return null;
      const f = sj.filters || {};
      return (f.Quality || f.Audio) ? 'filtered' : 'simple';
    },
    // Tab visibility: which sections show up in tabbed-edit + wizard.
    // RG + Filters apply to Tag and Discover phases; Extra tags is its
    // own switch. Recover-only / cleanup-only rules show only Basics.
    ruleEditorTabVisible(tabId) {
      if (tabId === 'basics')   return true;
      const isWebhook = this.ruleEditorIsWebhook();
      if (tabId === 'rg')       return this.ruleAffectsTag() || this.ruleAffectsDiscover();
      if (tabId === 'filters')  return this.ruleAffectsTag() || this.ruleAffectsDiscover();
      if (tabId === 'audio')    return this.ruleAffectsAudio();
      if (tabId === 'video')    return this.ruleAffectsVideo();
      if (tabId === 'dvdetail') return this.ruleAffectsDvDetail();
      // Missing episodes + TBA refresh — Sonarr-only phases, each with
      // its own page like Audio/Video/DV. Schedule/QFA only (not
      // webhook). Config lives on the step; Review shows a summary.
      if (tabId === 'missingepisodes') return !isWebhook && this.ruleAffectsMissingEpisodes();
      if (tabId === 'tbarefresh')      return !isWebhook && this.ruleAffectsTbaRefresh();
      // Webhook-only steps. Hidden for schedule rules entirely; for
      // webhook rules visible only when the corresponding function is
      // ticked. Backend's webhook rule validate-then-persist requires
      // the matching criteria/rules struct, so the step appears
      // automatically the moment the user picks the function on
      // Basics — same affects-X gate logic the other steps use.
      if (tabId === 'grabrename') return isWebhook && this.ruleAffectsGrabRename();
      // qBit S/E step is visible for webhook rules (function ticked) AND
      // for schedule/QFA rules (qbitsetag phase chosen). Sonarr-only on
      // both: the webhook function + the combined-mode catalog entry are
      // each gated to Sonarr, so ruleAffectsQbitSe implies Sonarr here.
      if (tabId === 'qbitse')     return this.ruleAffectsQbitSe();
      if (tabId === 'qbitcategoryfix') return isWebhook && this.ruleAffectsQbitCategoryFix();
      if (tabId === 'plexlabelsync') return isWebhook && this.ruleAffectsPlexLabelSync();
      // Plex sync — dedicated step for schedule + QFA rules (combined
      // phase). Distinct from the webhook-only 'plexlabelsync' step:
      // schedules carry the config on editingRule.plexSync, webhooks on
      // editingRule.plexLabelSync. Hidden for webhook rules.
      if (tabId === 'plexsync') return !isWebhook && this.ruleAffectsPlexSync();
      // Schedule step: schedule rules only (cron-driven, not Manual
      // and not quickfix). Webhook rules trigger on Connect events,
      // not cron — the step hides entirely for kind='webhook'.
      if (tabId === 'schedule') {
        if (isWebhook) return false;
        return !this.ruleEditor.isQuickFix && !!this.editingRule && !this.editingRule.manualOnly;
      }
      // Review tab is always available in tabbed-edit — it's the read-
      // only summary of everything configured, mirroring the wizard's
      // final 'review' step (which create-mode reaches via steps, not
      // tabs). Last in the tab strip, same as the wizard step order.
      if (tabId === 'review') return true;
      return false;
    },

    // Convenience: the editor is in webhook-rule mode when ruleEditor.
    // kind is explicitly 'webhook'. Empty / 'schedule' / undefined all
    // route to the legacy schedule-rule flow so existing entry points
    // (Create rule, QFA, per-action wizards) keep working unchanged.
    ruleEditorIsWebhook() {
      return !!(this.ruleEditor && this.ruleEditor.kind === 'webhook');
    },

    // True when the webhook-mode rule has at least one function ticked.
    // Mirrors webhookWizardAnyFunctionTicked but reads off editingRule
    // instead of the now-deprecated webhookWizard.fn* fields. Used by
    // the Basics-step warning + Save-time validation.
    webhookRuleAnyFunctionTicked() {
      const o = (this.editingRule && this.editingRule.options) || {};
      if (o.fnTagReleaseGroups || o.fnDiscover || o.fnTagAudio || o.fnTagVideo
          || o.fnTagDvDetail || o.fnRecover || o.fnSyncToSecondary
          || o.fnGrabRename || o.fnQbitSeTag || o.fnQbitCategoryFix
          || o.fnPlexLabelSync) {
        return true;
      }
      // Per-bucket strip-on-delete also counts as a rule action — a
      // rule that only strips audio tags on file delete with no other
      // function is valid (the dispatcher's FiresPerBucketStripOnDelete
      // gate enters the rule on delete events regardless of Functions).
      return this.webhookRuleHasBucketStripOnDelete();
    },

    // True when any bucket snapshot on the editing rule has
    // stripOnFileDelete=true. Used by the Basics-step gate +
    // webhookEventsForCurrentRule's delete-event surfacing.
    webhookRuleHasBucketStripOnDelete() {
      const r = this.editingRule;
      if (!r) return false;
      if (r.audioTags && r.audioTags.stripOnFileDelete) return true;
      if (r.videoTags && r.videoTags.stripOnFileDelete) return true;
      // DV-detail is Radarr-only — the snapshot may exist on Sonarr
      // rules too via the schedule editor's shared shape, but the
      // dispatcher gates it. Treat flag-present as "wants delete
      // trigger surfaced" for consistency; webhookEventsForFunctions
      // applies the AppType gate below.
      if (r.dvDetail && r.dvDetail.stripOnFileDelete) return true;
      return false;
    },

    // Entry point for the Webhooks page +Add rule button. Opens the
    // unified rule editor in webhook mode against the picked instance.
    // Mirrors openCreateRuleWizard's shape but seeds editingRule with
    // a webhook-rule skeleton + sets ruleEditor.kind = 'webhook' so the
    // Basics step + visibleSteps + save flow branch correctly.
    openWebhookRuleEditor(instanceId) {
      if (!instanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      const inst = (this.instances || []).find(i => i.id === instanceId);
      if (!inst) {
        this.showToast('Instance not found — refresh the page', 'error');
        return;
      }
      // Webhook rules don't have a mode-string the way schedule rules
      // do (mode='tag'/'discover'/'combined'/etc. drives the schedule
      // chain). For the webhook editor mode='webhook' is a sentinel
      // value the editor recognises but the backend doesn't read —
      // backend uses Functions[] directly. Empty options.combinedModes
      // keeps the schedule-mode helpers correctly returning false.
      this.editingRule = {
        id: '',
        name: '',
        mode: 'webhook',
        instanceId,
        enabled: true,
        manualOnly: true, // keeps schedule-step hidden via the existing manualOnly gate
        options: {
          // Function flags — start clean, user picks on Basics.
          fnTagReleaseGroups: false,
          fnDiscover:         false,
          fnTagAudio:         false,
          fnTagVideo:         false,
          fnTagDvDetail:      false,
          fnRecover:          false,
          fnSyncToSecondary:  false,
          fnGrabRename:       false,
          fnQbitSeTag:        false,
          fnQbitCategoryFix:  false,
          fnPlexLabelSync:    false,
          // Per-action shared options the schedule editor seeds —
          // mirrored here so the per-step UIs (RG / Filters / Audio /
          // Video / DV) read the same shape and don't NaN on undefined.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          discoverWriteBack: false,
          discoverScanSecondary: false,
          autoActivateDiscovered: false,
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // M-Webhook notification kill-switch — default off so new
          // rules stay silent until the user opts in. Eligible agents
          // + their Functions filter handle the rest (Settings →
          // Notifications).
          notifyOnFire: false,
        },
        filters:         this.snapshotGlobalFilters(),
        audioTags:       this.snapshotGlobalAudioTags(),
        videoTags:       this.snapshotGlobalVideoTags(),
        dvDetail:        this.snapshotGlobalDvDetail(),
        releaseGroupIds: this.snapshotGlobalRGIds(instanceId),
        // Seed Grab Rename + qBit S/E + qBit Category Fix criteria with
        // sensible defaults. All three are pre-populated even when their
        // fn flag is off so the user can tick the function on Basics and
        // immediately have a hydrated form on Step 3b / 3c / 3d without
        // defensive null-checks sprinkled through the templates. Save
        // flow only sends them when the matching fn is enabled. Shape is
        // shared with normalizeWebhookRuleClientShape (see below) — if
        // you add a field here, mirror it in defaultWebhookRuleStructs()
        // so old rules loaded from the server backfill cleanly.
        ...this.defaultWebhookRuleStructs(),
      };
      this.ruleEditor = {
        open: true,
        isCreate: true,
        isQuickFix: false,
        step: 0,
        activeTab: 'basics',
        appType: inst.type,
        kind: 'webhook',
        busy: false,
        error: '',
        cronError: '',
        nextFires: [],
      };
    },

    // openEditWebhookRuleModal opens the rule editor in edit-mode for
    // an existing webhook rule. Mirror of openEditRuleModal (schedule
    // rules) but with webhook-shape hydration:
    //
    //   - Server rule's `functions[]` array is unpacked into the
    //     wizard's `options.fn*` flags (the wizard binds checkboxes
    //     to fnTagAudio / fnTagVideo / etc., not directly to the
    //     functions array)
    //   - Top-level rule fields (tagSource, filterOnlyTag,
    //     syncToInstanceId, discoverAutoEnable) are hoisted into
    //     `options.*` so the wizard's per-step bindings see them
    //     under the same keys as create-flow
    //   - Snapshots (filters / audioTags / videoTags / dvDetail /
    //     missingEpisodes / releaseGroupIds) are backfilled from
    //     globals when nil — defence against legacy rules saved
    //     before per-rule snapshots landed
    //   - Struct-typed fields (grabRename / qbitSe / qbitCategoryFix)
    //     get default-shape backfill via normalizeWebhookRuleClientShape
    //     so x-model bindings don't throw on undefined
    //
    // Locks appType to the existing rule's instance type — editing a
    // Radarr rule never exposes Sonarr instances in the dropdown
    // (cross-type "edit" is effectively a different rule; user must
    // delete + recreate). Same constraint openEditRuleModal applies.
    openEditWebhookRuleModal(rule) {
      if (!rule || !rule.id) {
        this.showToast('Cannot edit: rule has no id', 'error');
        return;
      }
      const inst = (this.instances || []).find(i => i.id === rule.instanceId);
      if (!inst) {
        this.showToast('Rule references a missing instance — refresh the page or fix the instance under Settings', 'error');
        return;
      }
      // Deep-copy so wizard edits don't mutate the cached
      // webhookRules entry until Save commits.
      let copy = JSON.parse(JSON.stringify(rule));
      // Backfill struct-typed fields (grabRename / qbitSe /
      // qbitCategoryFix) for legacy rules that pre-date them.
      copy = this.normalizeWebhookRuleClientShape(copy);
      // Webhook editor uses mode='webhook' as a sentinel — the
      // backend doesn't read it (function dispatch is via
      // Functions[]) but the wizard's manualOnly + isWebhook gates
      // depend on the shape.
      copy.mode = 'webhook';
      copy.manualOnly = true;
      // Backfill snapshots from globals so per-step UIs always have
      // a non-nil object to bind to. Legacy rules saved before
      // per-rule snapshots may have nil here.
      if (!copy.filters)         copy.filters         = this.snapshotGlobalFilters();
      if (!copy.audioTags)       copy.audioTags       = this.snapshotGlobalAudioTags();
      if (!copy.videoTags)       copy.videoTags       = this.snapshotGlobalVideoTags();
      if (!copy.dvDetail)        copy.dvDetail        = this.snapshotGlobalDvDetail();
      if (!copy.missingEpisodes) copy.missingEpisodes = this.snapshotGlobalMissingEpisodes();
      if (!copy.qbitSe) copy.qbitSe = this.snapshotDefaultQbitSe();
      if (!copy.plexSync)        copy.plexSync        = this.snapshotDefaultPlexSync();
      if (!copy.tbaRefresh)      copy.tbaRefresh      = this.snapshotDefaultTbaRefresh();
      if (!Array.isArray(copy.releaseGroupIds)) copy.releaseGroupIds = this.snapshotGlobalRGIds(copy.instanceId);

      // Hoist server-shape rule fields into options.* so the
      // wizard's per-step bindings find them under the same keys
      // create-flow uses. Save flow at saveWebhookRuleEditor reads
      // back from options.* and emits to the right top-level keys.
      const fnSet = new Set((copy.functions || []).map(s => String(s)));
      copy.options = Object.assign({
        // Function flags — defaults match openWebhookRuleEditor.
        fnTagReleaseGroups: false,
        fnDiscover:         false,
        fnTagAudio:         false,
        fnTagVideo:         false,
        fnTagDvDetail:      false,
        fnRecover:          false,
        fnSyncToSecondary:  false,
        fnGrabRename:       false,
        fnQbitSeTag:        false,
        fnQbitCategoryFix:  false,
        fnPlexLabelSync:    false,
        // Per-action shared options.
        tagSource: '',
        filterOnlyTag: 'lossless-web',
        cleanupUnusedTags: false,
        syncToSecondary: false,
        syncToInstanceId: '',
        discoverWriteBack: false,
        discoverScanSecondary: false,
        autoActivateDiscovered: false,
        audioTagsTarget: 'primary',
        videoTagsTarget: 'primary',
        dvDetailTarget:  'primary',
        recoverTarget:   'primary',
        notifyOnFire:    false,
      }, copy.options || {});
      // Unpack functions[] → fn* flags. Server-side stores the
      // canonical function-name strings; wizard binds checkboxes to
      // fn* booleans. Mapping mirrors saveWebhookRuleEditor's
      // inverse fnList.push branch.
      copy.options.fnTagReleaseGroups = fnSet.has('tagReleaseGroups');
      copy.options.fnDiscover         = fnSet.has('discover');
      copy.options.fnTagAudio         = fnSet.has('tagAudio');
      copy.options.fnTagVideo         = fnSet.has('tagVideo');
      copy.options.fnTagDvDetail      = fnSet.has('tagDvDetail');
      copy.options.fnRecover          = fnSet.has('recover');
      copy.options.fnSyncToSecondary  = fnSet.has('syncToSecondary');
      copy.options.fnGrabRename       = fnSet.has('grabRename');
      copy.options.fnQbitSeTag        = fnSet.has('qbitSeTag');
      copy.options.fnQbitCategoryFix  = fnSet.has('qbitCategoryFix');
      copy.options.fnPlexLabelSync    = fnSet.has('plexLabelSync');
      // Hoist top-level rule fields that the wizard binds via
      // options.*. The save flow re-emits these as top-level keys
      // on PUT (saveWebhookRuleEditor handles the inverse mapping
      // for tagSource / filterOnlyTag / syncToInstanceId).
      if (typeof copy.tagSource === 'string') copy.options.tagSource = copy.tagSource;
      if (typeof copy.filterOnlyTag === 'string') copy.options.filterOnlyTag = copy.filterOnlyTag || copy.options.filterOnlyTag;
      if (typeof copy.syncToInstanceId === 'string') copy.options.syncToInstanceId = copy.syncToInstanceId;
      if (typeof copy.discoverAutoEnable === 'boolean') copy.options.autoActivateDiscovered = copy.discoverAutoEnable;
      // discoverWriteBack is the gate the radio-button :checked
      // expressions look at — both options ("leave disabled" and
      // "add and enable") only render as checked when it's true.
      // Server-side, having WebhookFnDiscover in Functions implies
      // we WILL write discovered groups back to the Active list
      // (that's what the function does), so on edit-open we set it
      // to true whenever Discover is enabled. Without this, opening
      // edit on a rule with Discover would show neither radio
      // selected — the user couldn't tell which mode the rule was
      // saved in until they re-clicked.
      if (fnSet.has('discover')) {
        copy.options.discoverWriteBack = true;
      }
      if (typeof copy.syncSkipOrphanCleanup === 'boolean') copy.options.syncSkipOrphanCleanup = copy.syncSkipOrphanCleanup;
      if (typeof copy.notifyOnFire === 'boolean') copy.options.notifyOnFire = copy.notifyOnFire;

      this.editingRule = copy;
      this.ruleEditor = {
        open: true,
        isCreate: false,
        isQuickFix: false,
        step: 0,
        activeTab: 'basics',
        appType: inst.type,
        kind: 'webhook',
        busy: false,
        error: '',
        cronError: '',
        nextFires: [],
      };
    },

    // defaultWebhookRuleStructs is the single source of truth for the
    // default shape of struct-typed webhook-rule fields. Used by:
    //
    //   - openWebhookRuleEditor — seeds fresh rules.
    //   - normalizeWebhookRuleClientShape — backfills legacy rules
    //     loaded from /api/webhook-rules that pre-date the addition of
    //     a struct field.
    //
    // When adding a new struct-shaped rule field: add its default here
    // FIRST, then reference via defaultWebhookRuleStructs()[fieldName]
    // wherever a fresh-default is needed. Keeps create + edit + load
    // paths from drifting apart silently.
    defaultWebhookRuleStructs() {
      return {
        grabRename: {
          renameTarget: 'torrent',
          triggerOnMissingReleaseGroup: true,
          triggerOnMovieVersionMismatch: false,
          triggerOnSourceMismatch: false,
          triggerOnAudioMismatch: false,
          triggerOnSceneMismatch: false,
          triggerAlways: false,
          customTokens: [],
          groupBlocklist: [],
          qbitInstanceId: '',
        },
        qbitSe: {
          // Three-rule first-match-wins model — mirror of community
          // qbittorrent_auto_tagger.py. All three rules seeded ON so a
          // brand-new rule produces tags out of the box.
          qbitInstanceId: '',
          episodeEnabled: true,  episodeTag: 'Episode',
          seasonEnabled: true,   seasonTag: 'Season',
          unmatchedEnabled: true, unmatchedTag: 'Unmatched',
          // M-qBit-add Slice 6 — qBit-add debounce window (only
          // affects the qBit-side webhook path; Sonarr Connect Grab
          // events fire instantly regardless). 60s is a good default
          // for typical cross-seed bursts.
          aggregationWindowSeconds: 60,
        },
        // qBit Category Fix — defensive reconcile of stuck pre-import
        // categories. Snapshot fields filled in by autoFillQbitCategories
        // once the user picks a download client from the live Arr list.
        qbitCategoryFix: {
          qbitInstanceId: '',
          arrDownloadClientId: 0,
          preImportCategorySnapshot: '',
          postImportCategorySnapshot: '',
        },
        // Plex label sync — inline per-rule config. Same field shape
        // as standalone PlexLabelRule's Plex-side bits (instance +
        // libraries + labels + display + targetTypes). Seeded empty;
        // user populates via the Plex sync wizard step when the
        // function is enabled.
        plexLabelSync: {
          plexInstanceId: '',
          libraryKeys: [],
          labels: [],
          labelDisplay: {},
          targetTypes: ['label'],
        },
      };
    },

    // normalizeWebhookRuleClientShape backfills struct-typed fields that
    // a saved rule may pre-date. The Go side encodes nil pointers as
    // missing JSON keys (omitempty), so a rule saved before the qBit
    // Category Fix landed would deserialise with rule.qbitCategoryFix
    // === undefined. The wizard's Alpine bindings (x-model on
    // editingRule.qbitCategoryFix.qbitInstanceId etc.) would throw on
    // undefined. Run every server-sourced rule through this helper before
    // assigning it to editingRule.
    //
    // Mutates + returns the same object for ergonomic chaining.
    normalizeWebhookRuleClientShape(rule) {
      if (!rule || typeof rule !== 'object') return rule;
      const defaults = this.defaultWebhookRuleStructs();
      for (const key of Object.keys(defaults)) {
        if (rule[key] == null) {
          rule[key] = JSON.parse(JSON.stringify(defaults[key]));
        }
      }
      // M-qBit-add Slice 6 — field-level backfill for legacy rules
      // that pre-date AggregationWindowSeconds. Server treats stored
      // 0 as "use default 60" but UI input accepts 1..3600 only, so
      // pre-fill 60 for an unambiguous edit experience.
      if (rule.qbitSe && (rule.qbitSe.aggregationWindowSeconds == null || rule.qbitSe.aggregationWindowSeconds === 0)) {
        rule.qbitSe.aggregationWindowSeconds = 60;
      }
      return rule;
    },

    // Function-affects helpers for the new webhook-only steps. Mirror
    // the ruleAffects{Tag,Audio,Video,DvDetail,Discover}() pattern but
    // read off editingRule.options.fn* (set by the Basics step's
    // function checkboxes). Schedule rules never have these flags so
    // the helpers correctly return false there.
    ruleAffectsGrabRename() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnGrabRename;
    },
    ruleAffectsQbitSe() {
      const r = this.editingRule;
      if (!r) return false;
      // Webhook rule: the qBit S/E tag function is ticked.
      if (r.options && r.options.fnQbitSeTag) return true;
      // Schedule / QFA: qbitsetag is the chosen mode or a chain phase.
      if (r.mode === 'qbitsetag') return true;
      if (r.mode === 'combined' && ((r.options && r.options.combinedModes) || []).includes('qbitsetag')) return true;
      return false;
    },
    // Default qBit S/E config for schedule / QFA rules (Sonarr). Same
    // shape the webhook rule + one-off run use; all three rules seeded
    // on so a fresh schedule tags out of the box. qbitInstanceId stays
    // blank until the user picks one (validated on save).
    snapshotDefaultQbitSe() {
      return {
        qbitInstanceId: '',
        episodeEnabled: true,  episodeTag: 'Episode',
        seasonEnabled: true,   seasonTag: 'Season',
        unmatchedEnabled: true, unmatchedTag: 'Unmatched',
      };
    },
    ruleAffectsQbitCategoryFix() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnQbitCategoryFix;
    },
    ruleAffectsPlexLabelSync() {
      const o = (this.editingRule && this.editingRule.options) || {};
      return !!o.fnPlexLabelSync;
    },

    // ---- Plex label sync wizard step helpers ----
    // Mirror the standalone PlexLabelRule modal helpers but operate
    // on editingRule.plexLabelSync (the inline webhook-rule config)
    // instead of plexLabelRuleModal. Tag-list state
    // (plexLabelRuleAvailableTags + filter + loading + error) is
    // shared with the standalone modal since the two surfaces are
    // mutually exclusive (you can't have both open at once).

    plexLabelSyncWizardLoadTags() {
      // Reuse the standalone modal's fetch state — repoint
      // plexLabelRuleModal.instanceId so plexLabelRuleLoadAvailableTags
      // hits the right Arr. The modal isn't open; we're piggybacking
      // on its tag-list state holders.
      this.plexLabelRuleModal.instanceId = (this.editingRule && this.editingRule.instanceId) || '';
      this.plexLabelRuleAvailableTags = [];
      this.plexLabelRuleTagsError = '';
      this.plexLabelRuleTagFilter = '';
      if (this.plexLabelRuleModal.instanceId) {
        this.plexLabelRuleLoadAvailableTags();
      }
    },

    plexLabelSyncWizardSelectedPlexLibraries() {
      const r = this.editingRule;
      if (!r || !r.plexLabelSync || !r.plexLabelSync.plexInstanceId) return [];
      const pi = (this.plexInstances || []).find(p => p.id === r.plexLabelSync.plexInstanceId);
      if (!pi) return [];
      return pi.libraries || [];
    },

    plexLabelSyncWizardWantedLibraryType() {
      const r = this.editingRule;
      if (!r) return '';
      if (r.appType === 'radarr') return 'movie';
      if (r.appType === 'sonarr') return 'show';
      // Fallback — read off the parent rule's linked Arr instance
      // (in case appType wasn't denormalised yet).
      const inst = (this.instances || []).find(i => i.id === (r && r.instanceId));
      if (inst) {
        if (inst.type === 'radarr') return 'movie';
        if (inst.type === 'sonarr') return 'show';
      }
      return '';
    },

    plexLabelSyncWizardVisibleLibraryCount() {
      const libs = this.plexLabelSyncWizardSelectedPlexLibraries();
      const want = this.plexLabelSyncWizardWantedLibraryType();
      if (!want) return libs.length;
      return libs.filter(l => l.type === want).length;
    },

    plexLabelSyncWizardNoLibrariesMessage() {
      const want = this.plexLabelSyncWizardWantedLibraryType();
      if (!want) return 'Pick an Arr instance on the Basics step so the library picker can filter to the matching type.';
      if (want === 'movie') return 'No movie libraries cached on this Plex instance. Add a Movies library in Plex, then click Fetch libraries on the Plex row in Settings.';
      return 'No show libraries cached on this Plex instance. Add a TV Shows library in Plex, then click Fetch libraries on the Plex row in Settings.';
    },

    plexLabelSyncWizardLibraryHint() {
      const want = this.plexLabelSyncWizardWantedLibraryType();
      if (want === 'movie') return 'Only movie libraries shown — Radarr rules can only manage Plex labels on movie libraries.';
      if (want === 'show') return 'Only show libraries shown — Sonarr rules can only manage Plex labels on show libraries.';
      return 'Pick the Plex libraries this rule will sync labels into.';
    },

    plexLabelSyncWizardToggleLibrary(key) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!Array.isArray(cfg.libraryKeys)) cfg.libraryKeys = [];
      const idx = cfg.libraryKeys.indexOf(key);
      if (idx === -1) cfg.libraryKeys.push(key);
      else cfg.libraryKeys.splice(idx, 1);
    },

    plexLabelSyncWizardToggleTag(label) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!Array.isArray(cfg.labels)) cfg.labels = [];
      const idx = cfg.labels.findIndex(l => l.toLowerCase() === label.toLowerCase());
      if (idx === -1) {
        cfg.labels.push(label);
      } else {
        const removed = cfg.labels[idx];
        cfg.labels.splice(idx, 1);
        // Drop matching display override so unchecking + re-checking
        // gives a clean slate (consistent with standalone modal).
        if (cfg.labelDisplay && removed in cfg.labelDisplay) {
          delete cfg.labelDisplay[removed];
        }
      }
    },

    plexLabelSyncWizardSetDisplay(arrTag, value) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!cfg.labelDisplay) cfg.labelDisplay = {};
      const trimmed = (value || '').trim();
      if (!trimmed || trimmed === arrTag) {
        delete cfg.labelDisplay[arrTag];
      } else {
        cfg.labelDisplay[arrTag] = trimmed;
      }
    },

    plexLabelSyncWizardToggleTargetType(t) {
      const cfg = this.editingRule && this.editingRule.plexLabelSync;
      if (!cfg) return;
      if (!Array.isArray(cfg.targetTypes)) cfg.targetTypes = [];
      const idx = cfg.targetTypes.indexOf(t);
      if (idx === -1) cfg.targetTypes.push(t);
      else cfg.targetTypes.splice(idx, 1);
    },
  };
}
