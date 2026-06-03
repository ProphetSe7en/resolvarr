// resolvarr UI — run module
//
// The single home for "how a scan/action is executed":
//   - runAction          : the ONE /api/scan/run request builder
//   - overlayFromRule     : rule/schedule config -> per-request overlay fields
//   - buildSnapshotOverlay: read the __overlay stamp off the displayed result
//   - _perActionState*    : scan-local (localStorage) per-action wizard memory
//
// Composed into the Alpine root via { ...appRunModule() } in app(). Methods
// use `this` = the Alpine component (bound when the spread merges them onto
// the component), so they call peers (this.apiFetch, this.quickFixResults,
// this._perActionStateKey, ...) exactly as before — moving them to this file
// changes nothing at runtime. First module of the Stage 4 split; see
// docs/resolvarr/frontend-restructure-plan.md.
function appRunModule() {
  return {
    // runAction is the SINGLE builder for /api/scan/run requests. Every
    // chain phase (fetchPhase) and every standalone Apply (audio / video /
    // dv / tag) routes through here, so the request shape lives in ONE
    // place and can't drift between contexts — the drift that caused the
    // 2026-06-03 apply-now bug (one builder carried the overlay, the others
    // didn't). It builds the body + POSTs + returns the parsed response. It
    // does NOT do side-effects (DV poll, modal, toast, result routing) —
    // those stay in the callers. Throws Error("<action>: ...") on a non-ok
    // response. `options` carries the per-action fields the caller resolved
    // (from rule state for the chain, page state for standalone).
    async runAction({ instanceId, action, mode, overlay, options }) {
      const o = options || {};
      const body = {
        instanceId,
        action,
        // Discover is preview-only server-side — force it so a chain
        // running in apply mode never asks discover to "apply".
        mode: action === 'discover' ? 'preview' : mode,
        ...(overlay || {}),
      };
      switch (action) {
        case 'discover':
          body.discoverWriteBack = !!o.discoverWriteBack;
          body.autoActivateDiscovered = !!o.autoActivateDiscovered;
          break;
        case 'tag':
          if (o.tagSource === 'filter-only') {
            body.tagSource = 'filter-only';
            body.filterOnlyTag = o.filterOnlyTag || '';
            // filter-only: cleanup-tail is a no-op by design — never sent.
          } else {
            if (o.cleanupUnusedTags) body.cleanupUnusedTags = true;
            if (o.tagSource) body.tagSource = o.tagSource;
          }
          if (o.syncToInstanceId) body.syncToInstanceId = o.syncToInstanceId;
          break;
        case 'recover':
          if (o.recoverRename !== undefined) body.recoverRename = !!o.recoverRename;
          break;
        case 'dvdetail':
          if (o.bypassDvCache !== undefined) body.bypassDvCache = !!o.bypassDvCache;
          break;
      }
      const res = await this.apiFetch('/api/scan/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(`${action}: ${d.error || 'HTTP ' + res.status}`);
      }
      return await res.json();
    },

    // overlayFromRule turns a rule/schedule object (which carries filters /
    // releaseGroupIds / audioTags / videoTags / dvDetail) into the
    // per-request overlay fields the backend's applyRuleOverlay consumes.
    // Shared by the chain runner, the schedule runner, and the result-
    // stamping so all three produce identical overlays.
    overlayFromRule(rule) {
      const o = {};
      if (!rule) return o;
      if (rule.filters)         o.overlayFilters = JSON.parse(JSON.stringify(rule.filters));
      if (rule.releaseGroupIds) o.overlayReleaseGroupIds = [...rule.releaseGroupIds];
      if (rule.audioTags)       o.overlayAudioTags = JSON.parse(JSON.stringify(rule.audioTags));
      if (rule.videoTags)       o.overlayVideoTags = JSON.parse(JSON.stringify(rule.videoTags));
      if (rule.dvDetail)        o.overlayDvDetail = JSON.parse(JSON.stringify(rule.dvDetail));
      return o;
    },

    // buildSnapshotOverlay returns the per-request overlay that produced the
    // result the user is looking at, read from the `__overlay` stamp on that
    // result. EVERY per-phase "Apply now" spreads this into its
    // /api/scan/run body so the apply writes with the EXACT config the
    // preview ran with — never the global Library-scan config. The stamp
    // travels WITH the result (set by runQuickFixChain for QFA / per-action
    // runs and by buildActivityResult for schedule runs), so apply never
    // guesses the origin and there's no stale-state window. A result with no
    // stamp (a true standalone Library-scan run) yields {} -> global config,
    // which is correct for that case.
    buildSnapshotOverlay(result) {
      const o = result && result.__overlay;
      return o ? JSON.parse(JSON.stringify(o)) : {};
    },

    // Per-action wizard config is remembered SCAN-LOCALLY (localStorage),
    // never written back to the global config. A one-off / per-action run
    // must not mutate the global template that seeds NEW rules, nor any
    // saved rule (Stage 0). Replaced persistRuleSnapshotsToGlobals, which
    // PUT the wizard's config to /api/audio-tags etc. after every run — the
    // global-pollution source behind the 2026-06-03 apply-now bug.
    _perActionStateKey(action, arrType) {
      return 'resolvarr-scan-' + action + '-' + (arrType === 'sonarr' ? 'sonarr' : 'radarr');
    },
    _savePerActionState(action, arrType, rule) {
      if (!rule) return;
      const persist = {};
      if (action === 'audiotags')      persist.audioTags = rule.audioTags;
      else if (action === 'videotags') persist.videoTags = rule.videoTags;
      else if (action === 'dvdetail')  persist.dvDetail  = rule.dvDetail;
      else return; // recover / missing-episodes carry no bucket config to remember
      try {
        localStorage.setItem(this._perActionStateKey(action, arrType), JSON.stringify(persist));
      } catch (e) { /* ignore — Safari private mode et al */ }
    },
    _loadPerActionState(action, arrType) {
      try {
        const s = localStorage.getItem(this._perActionStateKey(action, arrType));
        return s ? JSON.parse(s) : null;
      } catch (e) { return null; }
    },
  };
}
