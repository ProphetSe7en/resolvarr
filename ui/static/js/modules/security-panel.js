// resolvarr UI — security-panel (extracted from app.js, Stage 4 split).
// Composed via { ...appSecurityPanel() } in app(); methods bind `this` to the Alpine component.
function appSecurityPanel() {
  return {
    // ============ Security panel ============

    // Hydrate the security form from the live config + auth-status
    // endpoint. authStatus surfaces trustedNetworksLocked /
    // trustedProxiesLocked so the inputs disable when env vars override.
    async loadSecurityPanel() {
      this.securitySaveMsg = '';
      try {
        const r = await this.apiFetch('/api/auth/status');
        if (r.ok) {
          const d = await r.json();
          // Backend uses snake_case for these keys (auth_handlers.go).
          this.authStatus = {
            trustedNetworksLocked: !!(d.trusted_networks_locked || d.trustedNetworksLocked),
            trustedProxiesLocked:  !!(d.trusted_proxies_locked  || d.trustedProxiesLocked),
            // Saved auth mode, drives the disable-auth modal trigger and
            // the modal's Cancel reset (backend uses snake_case).
            authentication:        d.authentication || d.authentication_mode || 'forms',
            authenticationRequired: d.authentication_required || d.authenticationRequired || 'disabled_for_local_addresses',
          };
        }
      } catch {}
      // Hydrate form from current config (already loaded into this.config
      // by loadConfig on init).
      const c = this.config || {};
      this.securityForm = {
        authentication:         c.authentication || 'forms',
        authenticationRequired: c.authenticationRequired || 'disabled_for_local_addresses',
        trustedNetworks:        c.trustedNetworks || '',
        trustedProxies:         c.trustedProxies || '',
        sessionTtlDays:         c.sessionTtlDays || 30,
      };
      this.securityFormDirty = false;
      // Lazy-fetch the API key — server returns it in plaintext (auth-
      // gated endpoint), we mask it client-side until "Show" is clicked.
      this.fetchSecurityApiKey();
    },

    async fetchSecurityApiKey() {
      try {
        const r = await this.apiFetch('/api/auth/api-key');
        if (!r.ok) return;
        const d = await r.json();
        // Backend returns api_key (snake_case in auth_handlers.go).
        this.securityApiKey = d.api_key || d.apiKey || '';
      } catch {}
    },

    async copySecurityApiKey() {
      if (!this.securityApiKey) return;
      const ok = await this.copyToClipboard(this.securityApiKey);
      if (ok) {
        this.securityApiKeyCopied = true;
        setTimeout(() => { this.securityApiKeyCopied = false; }, 2000);
      } else {
        this.showToast('Copy failed — your browser blocked clipboard access', 'error');
      }
    },

    async regenerateApiKey() {
      this.securityRegenerating = true;
      try {
        const r = await this.apiFetch('/api/auth/regenerate-api-key', { method: 'POST' });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        const d = await r.json();
        this.securityApiKey = d.api_key || d.apiKey || '';
        this.securityApiKeyVisible = true;
        this.securityRegenConfirm = false;
        this.showToast('API key regenerated — old key invalid immediately', 'success');
      } catch (e) {
        this.showToast('Regenerate failed: ' + e.message, 'error');
      } finally {
        this.securityRegenerating = false;
      }
    },

    async saveSecurityConfig(confirmedNone) {
      // Disabling authentication entirely is the one protected transition
      // (Radarr/Sonarr parity): the backend requires the current admin
      // password as confirm_password when authentication goes to "none"
      // from anything else. Intercept that case and collect the password
      // through the confirmation modal before sending the save.
      if (this.securityForm.authentication === 'none' && !confirmedNone) {
        try {
          const r = await this.apiFetch('/api/auth/status');
          if (r.ok) {
            const d = await r.json();
            const savedMode = d.authentication || d.authentication_mode || '';
            if (savedMode !== 'none') {
              this.disableAuthPassword = '';
              this.disableAuthError = '';
              this.disableAuthModalOpen = true;
              return;
            }
          }
        } catch (_) { /* fall through and let the backend gate it */ }
      }

      this.securitySaving = true;
      this.securitySaveMsg = '';
      try {
        const body = { ...this.securityForm };
        const headers = { 'Content-Type': 'application/json' };
        if (confirmedNone && this.disableAuthPassword) {
          body.confirm_password = this.disableAuthPassword;
          // A wrong password returns 401 here meaning "password incorrect",
          // not "session expired", so opt out of apiFetch's /login
          // redirect so the error surfaces in the modal.
          headers['X-Skip-Login-Redirect'] = '1';
        }
        const r = await this.apiFetch('/api/config/auth', {
          method: 'PUT',
          headers,
          body: JSON.stringify(body),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) {
          if (r.status === 401 && confirmedNone) {
            // Wrong confirm password: reopen the modal with the error.
            this.disableAuthError = d.error || 'Current password is incorrect';
            this.disableAuthPassword = '';
            this.disableAuthModalOpen = true;
            return;
          }
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Mirror saved values back into the live config object so other
        // surfaces (the loadConfig cache) stay consistent.
        Object.assign(this.config || {}, this.securityForm);
        this.authStatus.authentication = this.securityForm.authentication;
        this.securityFormDirty = false;
        this.disableAuthPassword = '';
        this.disableAuthError = '';
        this.securitySaveOk = true;
        this.securitySaveMsg = 'Saved.';
        setTimeout(() => { this.securitySaveMsg = ''; }, 4000);
      } catch (e) {
        this.securitySaveOk = false;
        this.securitySaveMsg = e.message || 'Save failed';
      } finally {
        this.securitySaving = false;
      }
    },

    async changePassword() {
      // Client-side belt-and-braces — server validates too.
      if (this.pwChange.next !== this.pwChange.confirm) {
        this.pwChangeOk = false;
        this.pwChangeMsg = 'New password and confirmation do not match';
        return;
      }
      this.pwChangeSaving = true;
      this.pwChangeMsg = '';
      try {
        const r = await this.apiFetch('/api/auth/change-password', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          // Backend uses snake_case (auth_handlers.go:347-349) — must
          // send current_password / new_password / new_password_confirm
          // exactly so the JSON decoder picks them up.
          body: JSON.stringify({
            current_password:     this.pwChange.current,
            new_password:         this.pwChange.next,
            new_password_confirm: this.pwChange.confirm,
          }),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.pwChangeOk = true;
        this.pwChangeMsg = 'Password changed. Other sessions signed out.';
        this.pwChange = { current: '', next: '', confirm: '' };
        setTimeout(() => { this.pwChangeMsg = ''; }, 6000);
      } catch (e) {
        this.pwChangeOk = false;
        this.pwChangeMsg = e.message || 'Change failed';
      } finally {
        this.pwChangeSaving = false;
      }
    },

    // POST /logout — invalidates this browser's session cookie and
    // redirects to /login. Other sessions stay active. Best-effort:
    // even if the POST fails (network blip), we still nuke the cookie
    // client-side via redirect so the user isn't stuck on a stale page.
    async logout() {
      try {
        await this.apiFetch('/logout', { method: 'POST' });
      } catch (_) {
        // Ignore — we redirect regardless.
      }
      window.location.href = '/login';
    },

  };
}
