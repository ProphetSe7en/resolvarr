// resolvarr UI — FUNCTION_INFO accessors module
//
// Composed into the Alpine root via { ...appFunctionInfo() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appFunctionInfo() {
  return {
    // ---- FUNCTION_INFO accessors ----
    //
    // Render-time helpers for the rule editor (and any future surface
    // that wants the canonical short copy). Filters by current instance
    // type + rule kind (schedule-only / webhook-only) so the same data
    // object can drive both Basics blocks.
    //
    // List order matches the order the entries appear in FUNCTION_INFO
    // — Object.values() preserves insertion order in modern JS engines.
    functionInfoList(opts) {
      const o = opts || {};
      const wantWebhook = !!o.webhook;
      const includeSeparate = !!o.includeSeparate;
      const t = this.ruleEditorInstanceType();
      return Object.values(this.FUNCTION_INFO).filter(fn => {
        // Arr-type gate.
        if (fn.appliesTo !== 'both' && t && fn.appliesTo !== t) return false;
        // Schedule-only (Cleanup) — only on schedule rules.
        if (fn.scheduleOnly && wantWebhook) return false;
        // Webhook-only (file delete / grab rename / qbit S/E) — only
        // on webhook rules.
        if (fn.webhookOnly && !wantWebhook) return false;
        // Separate-control (syncToSecondary) — excluded from lean
        // checkbox lists; help-panel renders includes them via
        // includeSeparate=true so the description still appears.
        if (fn.separateControl && !includeSeparate) return false;
        return true;
      });
    },
    // Picks the right summary based on rule kind + instance type.
    // Webhook context (single-file fire on Connect events) often differs
    // from schedule context (full-library walk) — summaryWebhook /
    // summaryWebhookSonarr override when present.  Falls through to
    // summary / summarySonarr otherwise.
    //
    // Pass { webhook: true } to force webhook resolution; default reads
    // the current ruleEditor kind so library-scan callers (which don't
    // touch ruleEditor) still get the schedule wording.
    functionInfoSummary(fn, opts) {
      if (!fn) return '';
      const o = opts || {};
      const isWebhook = (o.webhook !== undefined) ? !!o.webhook : !!this.ruleEditorIsWebhook?.();
      const t = this.ruleEditorInstanceType();
      if (isWebhook) {
        if (t === 'sonarr' && fn.summaryWebhookSonarr) return fn.summaryWebhookSonarr;
        if (fn.summaryWebhook) return fn.summaryWebhook;
      }
      if (t === 'sonarr' && fn.summarySonarr) return fn.summarySonarr;
      return fn.summary || '';
    },
    functionInfoTriggers(fn) {
      if (!fn) return [];
      const t = this.ruleEditorInstanceType();
      if (t === 'sonarr' && fn.triggersSonarr) return fn.triggersSonarr;
      return fn.triggers || [];
    },
    // Webhook-mode checkbox onChange — writes the value AND runs the
    // same side-effects QFA's combined-mode toggle does, so dependent
    // state stays consistent.  Mirror of ruleEditorCombinedToggle but
    // for the webhook fn* flags.
    //
    // Side-effects today:
    //   - Discover ON  → ensureDiscoverDefaults() (mirror QFA)
    //   - Tag-RG OFF   → clear fnSyncToSecondary (Sync mirrors Tag-RG
    //                    decisions — without Tag-RG it's invalid and
    //                    backend would reject; rule-state stays clean)
    webhookFnToggle(fn, checked) {
      const r = this.editingRule;
      if (!r || !r.options || !fn) return;
      r.options[fn.optionFlag] = checked;
      if (fn.id === 'discover' && checked) {
        this.ensureDiscoverDefaults();
      }
      if (fn.id === 'tagReleaseGroups' && !checked) {
        r.options.fnSyncToSecondary = false;
      }
    },
    // Functions marked requiresQbit (Grab Rename, qBit S/E tag, Category
    // Fix) need at least one qBit instance configured under Settings →
    // qBit before they can run. Without one, the checkbox is disabled
    // with a tooltip so the user can't tick a function that has no chance
    // of working. Once they add a qBit instance the checkbox unlocks
    // automatically (qbitInstances is reactive).
    webhookFnDisabled(fn) {
      if (!fn) return false;
      if (fn.requiresQbit && (this.qbitInstances || []).length === 0) return true;
      return false;
    },
    webhookFnDisabledReason(fn) {
      if (!fn) return '';
      if (fn.requiresQbit && (this.qbitInstances || []).length === 0) {
        return 'Add a qBittorrent instance under Settings → qBit before enabling this function.';
      }
      return '';
    },
  };
}
