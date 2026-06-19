// resolvarr UI — webhook-setup (extracted from app.js, Stage 4 split).
// Composed via { ...appWebhookSetup() } in app(); methods bind `this` to the Alpine component.
function appWebhookSetup() {
  return {
    // ===== Webhook setup details (URL + Secret) =====
    //
    // Toggles inline display of a configured webhook's URL + Secret on
    // the Setup-tab instance card. Lets the user copy the Secret after
    // initial wizard config without having to regenerate the token
    // (which would invalidate the URL Sonarr/Radarr currently has).

    toggleWebhookDetails(instanceId) {
      const cur = !!this.webhookDetailsExpanded[instanceId];
      this.webhookDetailsExpanded = { ...this.webhookDetailsExpanded, [instanceId]: !cur };
      // Auto-mask secret again when collapsing
      if (cur) {
        this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: false };
      }
    },
    toggleWebhookSecretReveal(instanceId) {
      const cur = !!this.webhookSecretRevealed[instanceId];
      this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: !cur };
    },
    webhookSecretDisplay(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const secret = cfg.secret || '';
      if (!secret) return '(not yet generated)';
      if (this.webhookSecretRevealed[instanceId]) return secret;
      // Masked: keep first 4 + last 4 chars so user can verify the
      // value matches what they pasted into Sonarr/Radarr without
      // exposing the whole secret in over-shoulder screenshots.
      if (secret.length <= 12) return '•'.repeat(secret.length);
      return secret.slice(0, 4) + '•'.repeat(Math.max(0, secret.length - 8)) + secret.slice(-4);
    },
    // rotateWebhookSecret asks the backend for a fresh Secret while
    // keeping the existing Token (URL stays valid in Sonarr/Radarr).
    // Use when the instance has no Secret (legacy migration), or when
    // the user explicitly wants to rotate after a suspected leak.
    //
    // No confirm modal for the "missing Secret" case (zero-impact —
    // there was nothing to invalidate); shows a confirm for explicit
    // rotation when a Secret already exists because Sonarr/Radarr
    // will need its password re-pasted.
    async rotateWebhookSecret(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const hadSecret = !!cfg.secret;
      if (hadSecret) {
        const ok = await this.confirmDialog({
          title:       'Rotate webhook Secret?',
          message:     "A new Secret will be generated. The URL in Sonarr/Radarr stays the same, but you must paste the new Secret into the Connect → Password field — until you do, events with the old Secret will be rejected if Require signature is on.",
          confirmText: 'Rotate',
          kind:        'warning',
        });
        if (!ok) return;
      }
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate-secret', { method: 'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Patch the local cache so the details panel updates without
        // a full reload.
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, secret: d.secret, url: d.url } };
        // Auto-reveal so user can immediately copy it.
        this.webhookSecretRevealed = { ...this.webhookSecretRevealed, [instanceId]: true };
        this.showToast(hadSecret ? 'Secret rotated — paste the new one into Sonarr/Radarr' : 'Secret generated — paste it into Sonarr/Radarr → Connect → Password', 'success');
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      }
    },

    async copyWebhookSecret(instanceId) {
      const cfg = this.webhookConfigs[instanceId] || {};
      const secret = cfg.secret || '';
      if (!secret) {
        this.showToast('No secret to copy', 'error');
        return;
      }
      const ok = await this.copyToClipboard(secret);
      this.showToast(ok ? 'Secret copied' : 'Copy failed — reveal + select manually', ok ? 'success' : 'error');
    },

  };
}
