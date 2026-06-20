// resolvarr UI — qfa-wizards (extracted from app.js, Stage 4 split).
// Composed via { ...appQfaWizards() } in app(); methods bind `this` to the Alpine component.
function appQfaWizards() {
  return {
    // ===== QFA localStorage state =====
    //
    // QFA stores its full editingRule snapshot per Arr-type so the
    // wizard remembers the user's last chain configuration without
    // mutating globals (which per-action wizards own). Key shape:
    // 'resolvarr-qfa-state-radarr' / '-sonarr'. Per-Arr-type because
    // Radarr QFA and Sonarr QFA have different valid chain phases.

    _qfaStateKey(arrType) {
      return 'resolvarr-qfa-state-' + (arrType === 'sonarr' ? 'sonarr' : 'radarr');
    },
    _saveQfaState(arrType, rule) {
      if (!rule) return;
      try {
        // Strip volatile fields — `id` empty, `name` auto-generated.
        // Keep only what should re-hydrate next open. Deep-clone via
        // JSON.stringify-then-parse so a later mutation of rule.options
        // (e.g. an analytics post-run hook that mucks with combinedModes)
        // can't bleed into the persisted snapshot.
        const persist = {
          mode: rule.mode,
          instanceId: rule.instanceId,
          options: rule.options,
          filters: rule.filters,
          audioTags: rule.audioTags,
          videoTags: rule.videoTags,
          dvDetail: rule.dvDetail,
          missingEpisodes: rule.missingEpisodes,
          plexSync: rule.plexSync,
          tbaRefresh: rule.tbaRefresh,
          qbitSe: rule.qbitSe,
          releaseGroupIds: rule.releaseGroupIds || [],
        };
        localStorage.setItem(this._qfaStateKey(arrType), JSON.stringify(persist));
      } catch (e) { /* ignore — Safari private mode et al */ }
    },

    // Per-field merge for QFA-state hydration. Old localStorage payloads
    // written before a new bucket field landed (e.g. SonarrAggregation
    // added in M-Sonarr Phase A) shouldn't leak `undefined` past the
    // backend's validator on next Apply. Recursively layer restored
    // values over defaults so missing fields fall back to fresh defaults.
    // ===== Per-action wizard: unified instance + "both" picker =====
    //
    // For Tag Audio / Video / DV Details runs, "primary" has no
    // semantic meaning — each instance reads its own mediaInfo
    // independently, there's no TmdbID-mirror like tag-release-groups
    // does. So the Step-1 "Primary instance" picker and the Step-2
    // "Run X on" target picker are redundant pairs encoding the same
    // intent twice.
    //
    // Per-action wizards collapse them into ONE dropdown on Step 1
    // listing instances by name + a "Both instances" option (when
    // 2+ same-type instances exist). State translation:
    //   value = '<inst-id>'  →  instanceId = <inst-id>, target = 'primary'
    //   value = '__both__'   →  instanceId = first-of-type, target = 'both'
    // The chain runner already handles target='both' by running on
    // primary (instanceId) + secondary (auto-derived first-other-of-type),
    // so keeping instanceId as a real ID + target='both' just works.

    _perActionTargetField() {
      const fa = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
      if (fa === 'audiotags') return 'audioTagsTarget';
      if (fa === 'videotags') return 'videoTagsTarget';
      if (fa === 'dvdetail')  return 'dvDetailTarget';
      if (fa === 'recover')   return 'recoverTarget';
      return '';
    },
    // True when the current per-action wizard supports the unified
    // primary/secondary/both picker. Auto-tag phases (audiotags /
    // videotags / dvdetail) walk per-instance media independently —
    // primary vs secondary has no semantic meaning. Recover joined
    // 2026-05-09 because each instance has its own movie/episode files
    // with their own missing-releaseGroup history; "primary only" was
    // an artificial restriction. Used to gate the unified-picker markup
    // vs the legacy primary-instance-only picker on Basics step.
    isPerActionAutoTag() {
      const fa = (this.ruleEditor && this.ruleEditor.fixedAction) || '';
      return fa === 'audiotags' || fa === 'videotags' || fa === 'dvdetail' || fa === 'recover';
    },
    perActionInstanceSelectorValue() {
      if (!this.editingRule || !this.editingRule.options) return '';
      const tf = this._perActionTargetField();
      if (!tf) return this.editingRule.instanceId || '';
      if (this.editingRule.options[tf] === 'both') return '__both__';
      return this.editingRule.instanceId || '';
    },
    setPerActionInstance(value) {
      if (!this.editingRule || !this.editingRule.options) return;
      const tf = this._perActionTargetField();
      if (!tf) {
        this.editingRule.instanceId = value;
        return;
      }
      if (value === '__both__') {
        // Anchor instanceId to the first available instance of the
        // wizard's app type. The chain runner picks the secondary
        // automatically (first other-of-same-type).
        const pool = this.ruleEditorInstancesAvailable();
        if (pool.length > 0) this.editingRule.instanceId = pool[0].id;
        this.editingRule.options[tf] = 'both';
      } else {
        this.editingRule.instanceId = value;
        this.editingRule.options[tf] = 'primary';
      }
    },

    _mergeBucketSnapshot(restored, fallback) {
      if (!restored || typeof restored !== 'object') return fallback;
      if (!fallback || typeof fallback !== 'object') return restored;
      const out = { ...fallback };
      for (const k of Object.keys(restored)) {
        const rv = restored[k];
        const fv = fallback[k];
        if (rv && typeof rv === 'object' && !Array.isArray(rv) &&
            fv && typeof fv === 'object' && !Array.isArray(fv)) {
          out[k] = this._mergeBucketSnapshot(rv, fv);
        } else if (rv !== undefined) {
          out[k] = rv;
        }
      }
      return out;
    },
    _loadQfaState(arrType) {
      try {
        const s = localStorage.getItem(this._qfaStateKey(arrType));
        if (!s) return null;
        const parsed = JSON.parse(s);
        return (parsed && typeof parsed === 'object') ? parsed : null;
      } catch (e) { return null; }
    },

    // ===== Per-action standalone wizards =====
    //
    // openSpecificActionWizard reuses the QFA wizard infrastructure
    // but pre-seeds combinedModes to a single action and sets a
    // fixedAction flag so the Basics step's chain-checkboxes
    // section can hide (the user came here to run ONE thing — not
    // the whole "fix everything" chain). Wizard hydrates from
    // globals like QFA does, lets the user tweak per-run, fires
    // via runQuickFixChain on the Review step's Run button.
    //
    // Action keys: 'audiotags' | 'videotags' | 'dvdetail' | 'recover'.
    // Cleanup is handled by a separate small wizard (cleanup isn't
    // a combinedModes phase today).
    openSpecificActionWizard(action) {
      if (!this.instances || this.instances.length === 0) {
        this.showToast('Configure at least one instance first', 'error');
        return;
      }
      // Recover, Audio, Video work for both Arr types. DV detail is
      // Radarr-only today. Audio/Video on Sonarr work via the M-Sonarr
      // path. Discover/Tag remain Sonarr-deferred.
      const wizardAppType = this.scanAppType === 'sonarr' ? 'sonarr' : 'radarr';
      if (action === 'dvdetail' && wizardAppType !== 'radarr') {
        this.showToast('Tag DV Details is Radarr-only', 'error');
        return;
      }
      const pool = this.instances.filter(i => i.type === wizardAppType);
      // Seed precedence:
      //   1. last-used remembered instance for this action (if still in pool)
      //   2. current scanInstanceId (when matches type — pre-removal of
      //      header dropdown this was the legacy default; kept as a
      //      sensible fallback for users who just clicked from a tab)
      //   3. first-of-type
      const remembered = this.recallWizardInstance(action, pool);
      const seedInst = (remembered && pool.find(i => i.id === remembered))
                    || pool.find(i => i.id === this.scanInstanceId)
                    || pool[0];
      if (!seedInst) {
        this.showToast(
          'Need a ' + (wizardAppType === 'sonarr' ? 'Sonarr' : 'Radarr') +
          ' instance — add one in Settings → Instances', 'error');
        return;
      }
      const inst = seedInst.id;
      const titleByAction = {
        audiotags:       'Tag Audio',
        videotags:       'Tag Video',
        dvdetail:        'Tag DV Details',
        recover:         'Run Recover',
        missingepisodes: 'Find missing episodes',
      };
      this.editingRule = {
        id: '',
        name: titleByAction[action] || 'Run',
        mode: 'combined',
        instanceId: inst,
        preset: 'daily', hour: 3, minute: 0, hour12: 3, ampm: 'AM', dow: 0, dom: 1,
        cron: '0 3 * * *',
        enabled: true,
        options: {
          runMode: 'preview',
          cleanupUnusedTags: false,
          syncToSecondary: false,
          syncToInstanceId: '',
          combinedModes: [action],
          includeDiscovery: false,
          autoActivateDiscovered: false,
          discoverWriteBack: false,
          discoverScanSecondary: false,
          recoverIncludeSecondary: false,
          recoverIncludeSonarr: false,
          recoverSonarrSecondary: false,
          recoverTestItemId: 0,
          debugTrace: false,
          bypassDvCache: false,
          audioTagsTarget: 'primary',
          videoTagsTarget: 'primary',
          dvDetailTarget:  'primary',
          recoverTarget:   'primary',
          // Tag-mode source. Empty / "active" = legacy default
          // (per-group decisions). "discover" = Discover→Tag chain.
          // "filter-only" = ignore release group; tag every movie
          // passing the filter with FilterOnlyTag.
          tagSource: '',
          filterOnlyTag: 'lossless-web',
        },
        filters:          this.snapshotGlobalFilters(),
        audioTags:        this.snapshotGlobalAudioTags(),
        videoTags:        this.snapshotGlobalVideoTags(),
        dvDetail:         this.snapshotGlobalDvDetail(),
        missingEpisodes:  this.snapshotGlobalMissingEpisodes(),
        plexSync:         this.snapshotDefaultPlexSync(),
        tbaRefresh:       this.snapshotDefaultTbaRefresh(),
        qbitSe:           this.snapshotDefaultQbitSe(),
        releaseGroupIds:  this.snapshotGlobalRGIds(inst),
      };
      // Overlay the SCAN-LOCAL remembered config for this action (set by
      // _savePerActionState on the last run) over the global-seeded
      // defaults, so the wizard pre-fills with the user's last per-action
      // choices WITHOUT those choices ever having touched the global. Falls
      // back to the global defaults above when there's no remembered state.
      const rememberedCfg = this._loadPerActionState(action, wizardAppType);
      if (rememberedCfg) {
        if (rememberedCfg.audioTags) this.editingRule.audioTags = this._mergeBucketSnapshot(rememberedCfg.audioTags, this.editingRule.audioTags);
        if (rememberedCfg.videoTags) this.editingRule.videoTags = this._mergeBucketSnapshot(rememberedCfg.videoTags, this.editingRule.videoTags);
        if (rememberedCfg.dvDetail)  this.editingRule.dvDetail  = this._mergeBucketSnapshot(rememberedCfg.dvDetail,  this.editingRule.dvDetail);
      }
      this.ruleEditor = {
        open: true, isCreate: true, isQuickFix: true,
        step: 0, activeTab: 'basics', appType: wizardAppType,
        busy: false, error: '', cronError: '', nextFires: [],
        // fixedAction tells the Basics step to hide its chain-
        // checkboxes section — the user came here to run a single
        // action, not configure a chain. They can still hit Back
        // from later steps to tweak instance / run-mode.
        fixedAction: action,
      };
    },

    // Convenience entry points the sub-tab Run buttons call.
    openAudioWizard()   { this.openSpecificActionWizard('audiotags'); },
    openVideoWizard()   { this.openSpecificActionWizard('videotags'); },
    openDvWizard()      { this.openSpecificActionWizard('dvdetail'); },
    openRecoverWizard() { this.openSpecificActionWizard('recover'); },

    // ===== Per-wizard last-instance memory =====
    //
    // Each wizard remembers the instance the user picked on its
    // last successful run. Stored per-action so Tag Audio's last
    // pick doesn't bleed into Tag Video's. Bucket configs etc. are
    // intentionally NOT remembered — page-as-defaults handles
    // those, and rules handle "always identical settings" via
    // explicit save. Ad-hoc bucket tweaks are per-run by design.
    //
    // Storage key: 'resolvarr-wizard-instance-<action>'.
    // try/catch on every read+write — Safari private mode + similar
    // can block localStorage.
    _wizardInstanceKey(action) {
      return 'resolvarr-wizard-instance-' + action;
    },
    rememberWizardInstance(action, instanceId) {
      if (!action || !instanceId) return;
      try { localStorage.setItem(this._wizardInstanceKey(action), instanceId); }
      catch (e) { /* ignore — non-fatal */ }
    },
    // Returns the remembered instance ID iff it's still in pool +
    // matches the wizard's required type. Caller falls back to its
    // existing seed logic on null.
    recallWizardInstance(action, pool) {
      if (!action || !Array.isArray(pool) || pool.length === 0) return null;
      let stored = null;
      try { stored = localStorage.getItem(this._wizardInstanceKey(action)); }
      catch (e) { return null; }
      if (!stored) return null;
      return pool.some(i => i.id === stored) ? stored : null;
    },
  };
}
