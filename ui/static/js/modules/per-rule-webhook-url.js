// resolvarr UI — Per-rule webhook URL modal module
//
// Composed into the Alpine root via { ...appPerRuleWebhookUrl() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appPerRuleWebhookUrl() {
  return {
    // ---- Per-rule webhook URL modal (M-per-rule-webhook Slice 5) ----
    //
    // Lets the user generate / view / rotate / disable a dedicated
    // webhook URL for ONE rule. When configured, Sonarr/Radarr Connect
    // points at this rule's URL and only this rule fires from it (the
    // instance dispatcher excludes it per Slice 3). When not configured,
    // the rule fires via the shared instance URL alongside siblings.

    openPerRuleWebhookModal(rule) {
      this.perRuleWebhookRule = rule;
      this.perRuleWebhookData = null;
      this.perRuleWebhookShowCurl = false;
      this.perRuleWebhookConfirmDisable = false;
      this.perRuleWebhookOpen = true;
      this.loadPerRuleWebhookConfig();
    },

    closePerRuleWebhookModal() {
      if (this.perRuleWebhookActionInFlight) return;
      this.perRuleWebhookOpen = false;
      this.perRuleWebhookRule = null;
      this.perRuleWebhookData = null;
      this.perRuleWebhookShowCurl = false;
      this.perRuleWebhookConfirmDisable = false;
    },

    async loadPerRuleWebhookConfig() {
      if (!this.perRuleWebhookRule) return;
      this.perRuleWebhookLoading = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.perRuleWebhookData = d;
      } catch (e) {
        this.showToast('Load webhook config failed: ' + e.message, 'error');
        this.perRuleWebhookData = null;
      } finally {
        this.perRuleWebhookLoading = false;
      }
    },

    // Generate a fresh Token + Secret. Idempotent — clicking again on
    // an already-configured rule rotates the URL. RequireSignature
    // preserved across rotations (matches instance-rotate semantics).
    async doGeneratePerRuleWebhook() {
      if (!this.perRuleWebhookRule) return;
      const alreadyConfigured = !!(this.perRuleWebhookData && this.perRuleWebhookData.token);
      if (alreadyConfigured) {
        if (!await this.confirmDialog({
          title:       'Rotate the webhook URL?',
          message:     'The old URL stops working immediately — Sonarr/Radarr will start getting 404s until you paste the new URL into Connect. Use this only if you suspect the URL has leaked.',
          confirmText: 'Rotate',
          kind:        'warning',
        })) return;
      }
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook/generate', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.perRuleWebhookData = d;
        this.showToast(alreadyConfigured ? 'URL rotated — paste the new one into Sonarr/Radarr' : 'URL generated — paste it into Sonarr/Radarr Connect', 'success');
        await this.loadWebhookRules(); // refresh the card badge
      } catch (e) {
        this.showToast('Generate failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Rotate Secret only — keeps Token + URL stable so user doesn't
    // have to re-paste the URL in Sonarr/Radarr.
    async doRotatePerRuleWebhookSecret() {
      if (!this.perRuleWebhookRule) return;
      if (!await this.confirmDialog({
        title:       'Rotate the webhook secret?',
        message:     'The URL stays the same but you need to paste the new Secret as the Webhook password in Sonarr/Radarr.',
        confirmText: 'Rotate',
        kind:        'warning',
      })) return;
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast('Secret rotated', 'success');
        await this.loadPerRuleWebhookConfig();
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Flip strict-mode (RequireSignature) on/off. Backend rejects
    // enable=true when Secret is empty.
    async setPerRuleWebhookRequireSignature(enabled) {
      if (!this.perRuleWebhookRule) return;
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook/require-signature', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast(enabled ? 'Strict mode on — events without the Secret will be rejected' : 'Strict mode off — unsigned events accepted with a warning', 'success');
        await this.loadPerRuleWebhookConfig();
      } catch (e) {
        this.showToast('Setting failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Disable per-rule URL — rule reverts to instance-URL routing
    // (sibling rules will share the event with it again). Two-step
    // confirm via perRuleWebhookConfirmDisable so accidental clicks
    // don't kill a configured URL.
    async doDisablePerRuleWebhook() {
      if (!this.perRuleWebhookRule) return;
      if (!this.perRuleWebhookConfirmDisable) {
        this.perRuleWebhookConfirmDisable = true;
        return;
      }
      this.perRuleWebhookActionInFlight = true;
      try {
        const r = await this.apiFetch('/api/webhook-rules/' + this.perRuleWebhookRule.id + '/webhook', { method: 'DELETE' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showToast('Per-rule URL disabled — rule fires via the instance URL again', 'success');
        await this.loadPerRuleWebhookConfig();
        await this.loadWebhookRules();
        this.perRuleWebhookConfirmDisable = false;
      } catch (e) {
        this.showToast('Disable failed: ' + e.message, 'error');
      } finally {
        this.perRuleWebhookActionInFlight = false;
      }
    },

    // Computed state label for the modal header. Three states:
    //  - "Not configured" — no Token yet; CTA is Generate
    //  - "Active" — Token present; rule fires via dedicated URL
    perRuleWebhookStateLabel() {
      const d = this.perRuleWebhookData;
      if (!d) return { text: 'Loading…', color: 'var(--text-muted)' };
      if (!d.token) return { text: 'Not configured (rule uses the instance URL)', color: 'var(--text-secondary)' };
      return { text: 'Active — Sonarr/Radarr Connect should point at this URL', color: 'var(--accent-green)' };
    },

    // Build the curl-style example for the rule. Same shape as the
    // qBit-webhook curl helper but for Sonarr/Radarr Connect → URL +
    // Basic-auth password.
    perRuleWebhookCurlExample() {
      const d = this.perRuleWebhookData;
      if (!d || !d.url) return '';
      const auth = d.requireSignature
        ? ' \\\n  -H "Authorization: Basic $(echo -n \'resolvarr:' + (d.secret || '') + '\' | base64)"'
        : '';
      return 'curl -fsS -X POST ' + JSON.stringify(d.url) + auth + ' \\\n  -H "Content-Type: application/json" \\\n  -d \'{"eventType":"Test"}\'';
    },

    async copyPerRuleWebhookURL() {
      const d = this.perRuleWebhookData;
      if (!d || !d.url) return;
      const ok = await this.copyToClipboard(d.url);
      this.showToast(ok ? 'URL copied — paste into Sonarr/Radarr Connect → Webhook URL' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

    async copyPerRuleWebhookSecret() {
      const d = this.perRuleWebhookData;
      if (!d || !d.secret) return;
      const ok = await this.copyToClipboard(d.secret);
      this.showToast(ok ? 'Secret copied — paste as the Webhook password in Sonarr/Radarr' : 'Copy failed — select and copy manually', ok ? 'success' : 'error');
    },

  };
}
