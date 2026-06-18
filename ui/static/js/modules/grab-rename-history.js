// resolvarr UI — Grab Rename history-row helpers module
//
// Composed into the Alpine root via { ...appGrabRenameHistory() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appGrabRenameHistory() {
  return {
    // ---- Grab Rename — special history-row helpers ---------------
    //
    // Grab Rename's Summary shape from webhook_grab_rename.go is:
    //   renamed "<from>" → "<to>" (triggers: <reason1>, <reason2>, ...)
    // The generic chip renderer would put the whole thing in a 200+
    // char green pill — too loud, hard to scan. These helpers tear
    // it apart so the modal can render a compact `renamed` chip + a
    // plain-language trigger list + a token-level from→to diff.

    // Pulls "from" + "to" out of "renamed \"<from>\" → \"<to>\"".
    // Returns null on shape mismatch (legacy / unexpected) so the
    // template falls back to the generic chip layout.
    parseGrabRenameNames(result) {
      if (!result || typeof result !== 'string') return null;
      const m = result.match(/^renamed\s+"([^"]*)"\s+→\s+"([^"]*)"\s*$/);
      if (!m) return null;
      return { from: m[1], to: m[2] };
    },

    // Pulls the trigger reason list out of detail "triggers: a, b, c".
    // Reasons can themselves contain ", " inside parens (e.g.
    // "missing-release-group (parser rejected: multi-token)") so we
    // split on top-level commas only — tiny depth-counter walk.
    parseGrabRenameTriggers(detail) {
      if (!detail || typeof detail !== 'string') return [];
      const m = detail.match(/^triggers:\s*(.+)$/);
      if (!m) return [];
      const out = [];
      let depth = 0, cur = '';
      for (const ch of m[1]) {
        if (ch === '(') depth++;
        else if (ch === ')') depth = Math.max(0, depth - 1);
        if (ch === ',' && depth === 0) {
          if (cur.trim()) out.push(cur.trim());
          cur = '';
        } else {
          cur += ch;
        }
      }
      if (cur.trim()) out.push(cur.trim());
      return out;
    },

    // Translate one raw trigger label into plain language. The label
    // vocabulary lives in evaluateGrabRenameTriggers
    // (webhook_grab_rename.go) — keep this map in sync when new
    // triggers are added. Unknown labels render verbatim so we never
    // hide a reason from the user.
    humanizeGrabRenameTrigger(t) {
      if (!t) return '';
      if (t === 'always-rename') return 'Always-rename setting is on';
      let m = t.match(/^missing-release-group \(parser rejected: (.+)\)$/);
      if (m) {
        // The user-facing phrasing here matters — "multi-token" /
        // "split-fragment" are parser-internal vocab. What the user
        // wants to know: "is my filename actually missing a -RG
        // suffix?" — so every reason explains the WHY in terms of
        // what the parser saw + what shape a valid RG would take.
        const reasonMap = {
          'no-hyphen': 'filename has no hyphen at all, so there is no "-RG" suffix to read',
          'empty': 'filename ends with a hyphen and nothing after it',
          'multi-token': 'filename does not end with a single release-group tag like "-TOLS" (text after the last hyphen had spaces or dots, so it looked like part of the title — not a group)',
          'codec': 'text after the last hyphen looked like a codec (h264 / h265), not a release-group',
          'split-fragment': 'text after the last hyphen looked like part of a hyphenated token (DL from WEB-DL, HD from DTS-HD) — not a real release-group',
          'resolution': 'text after the last hyphen looked like a resolution (1080p / 2160p), not a release-group',
        };
        return 'Release group missing — ' + (reasonMap[m[1]] || m[1]);
      }
      m = t.match(/^missing-release-group \(parsed="(.*)" expected="(.*)"\)$/);
      if (m) return 'Release group mismatch — filename has "' + m[1] + '", grab said "' + m[2] + '"';
      m = t.match(/^movie-version: (.+)$/);
      if (m) return 'Edition / version tokens missing: ' + m[1].split('/').join(', ');
      m = t.match(/^source: (.+)$/);
      if (m) return 'Source tokens missing: ' + m[1].split('/').join(', ');
      m = t.match(/^audio: (.+)$/);
      if (m) return 'Audio tokens missing: ' + m[1].split('/').join(', ');
      if (t === 'scene-stripped (rg not a known scene group)') {
        return 'Looks scene-stripped (release group is not a known scene group)';
      }
      m = t.match(/^custom: (.+)$/);
      if (m) return 'Custom tokens missing: ' + m[1].split('/').join(', ');
      return t;
    },

    // Token-level set diff between two names. Tokens unique to `from`
    // are flagged removed (rendered red + strike-through), tokens
    // unique to `to` are flagged added (rendered green + bold). Common
    // tokens stay neutral. Case-insensitive comparison so "DV" vs "dv"
    // reads as same. Order is preserved so the line still reads as
    // the original name.
    grabRenameDiffTokens(from, to) {
      const fromTokens = (from || '').split(/\s+/).filter(Boolean);
      const toTokens = (to || '').split(/\s+/).filter(Boolean);
      const fromSet = new Set(fromTokens.map(t => t.toLowerCase()));
      const toSet = new Set(toTokens.map(t => t.toLowerCase()));
      return {
        from: fromTokens.map(t => ({ text: t, removed: !toSet.has(t.toLowerCase()) })),
        to: toTokens.map(t => ({ text: t, added: !fromSet.has(t.toLowerCase()) })),
      };
    },

    // Open the rule-fire history modal for this rule. Binds to the
    // already-loaded rule object so re-entry of the modal post-fire
    // (after Refresh) shows fresh entries. closeWebhookRuleHistory
    // is the inverse.
    openWebhookRuleHistory(rule) {
      this.webhookRuleHistoryRule = rule;
      this.webhookRuleHistoryExpanded = {};
      this.webhookRuleHistoryOpen = true;
    },
    closeWebhookRuleHistory() {
      this.webhookRuleHistoryOpen = false;
      this.webhookRuleHistoryRule = null;
      this.webhookRuleHistoryExpanded = {};
    },

    // Toggle expand/collapse for one run card. Reassigns the object
    // so Alpine's reactivity picks up the change reliably (deleting a
    // prop in-place is tracked by the Proxy but reassigning is the
    // belt-and-braces version).
    toggleWebhookRuleRun(key) {
      const next = { ...this.webhookRuleHistoryExpanded };
      if (next[key]) delete next[key]; else next[key] = true;
      this.webhookRuleHistoryExpanded = next;
    },

    // Tally per-status counts from the parsed summary so the
    // collapsed card header can show "3 changes" / "1 error" / "no
    // changes" at a glance. Errors are tracked separately so they
    // can shout louder than mere changes.
    webhookRuleRunCounts(run) {
      const rows = this.parseRuleRunSummary(run && run.summary);
      let changes = 0, errors = 0, skipped = 0, noop = 0;
      for (const r of rows) {
        if (r.status === 'error') errors++;
        else if (r.status === 'change' || r.status === 'change-add' || r.status === 'change-remove') changes++;
        else if (r.status === 'skipped') skipped++;
        else if (r.status === 'noop') noop++;
      }
      return { changes, errors, skipped, noop, total: rows.length };
    },

    // Single chip text for the collapsed header. Errors-first so the
    // user notices them before the change count. "no changes" is the
    // catch-all for runs where every function was skipped or noop.
    webhookRuleRunHeadlineLabel(run) {
      const c = this.webhookRuleRunCounts(run);
      if (c.errors > 0) return c.errors === 1 ? '1 error' : c.errors + ' errors';
      if (c.changes > 0) return c.changes === 1 ? '1 change' : c.changes + ' changes';
      return 'no changes';
    },

    // Color bucket for the headline chip — mirrors webhookFnResult
    // Colors so the same green/red/gray vocabulary applies at the
    // card level.
    webhookRuleRunHeadlineColors(run) {
      const c = this.webhookRuleRunCounts(run);
      if (c.errors > 0) return { bg: 'var(--alpha-red)', fg: 'var(--accent-red)' };
      if (c.changes > 0) return { bg: 'var(--alpha-green)', fg: 'var(--accent-green)' };
      return { bg: 'var(--bg-muted)', fg: 'var(--text-secondary)' };
    },

    // Re-fetch /api/webhook-rules so a freshly-fired run shows up
    // without closing + reopening the modal. We don't have an SSE
    // channel for rule fires today (only /api/webhook/events for
    // raw deliveries) — manual refresh keeps it lightweight. After
    // the load, re-bind the modal to the freshly-loaded rule object
    // so the History[] reactive read picks up new entries; if the
    // rule was deleted server-side mid-poll the modal closes.
    async refreshWebhookRuleHistory() {
      if (!this.webhookRuleHistoryRule) return;
      this.webhookRuleHistoryRefreshing = true;
      try {
        const ruleId = this.webhookRuleHistoryRule.id;
        await this.loadWebhookRules();
        const fresh = (this.webhookRules || []).find(r => r.id === ruleId);
        if (fresh) {
          this.webhookRuleHistoryRule = fresh;
        } else {
          // Rule deleted underneath us — close out cleanly.
          this.closeWebhookRuleHistory();
          this.showToast('Rule no longer exists', 'error');
        }
      } finally {
        this.webhookRuleHistoryRefreshing = false;
      }
    },

    // Confirm + delete a webhook rule. Uses a lightweight inline
    // confirm via the browser dialog for now — a styled modal can
    // come in polish.
    async confirmDeleteWebhookRule(rule) {
      if (!await this.confirmDialog({
        title:       'Delete rule "' + rule.name + '"?',
        message:     'This cannot be undone. The rule stops firing immediately. Webhook URL + delivery stay configured.',
        confirmText: 'Delete',
        kind:        'danger',
      })) return;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + rule.id, { method: 'DELETE' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadWebhookRules();
        this.showToast('Rule deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

  };
}
