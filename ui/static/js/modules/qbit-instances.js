// resolvarr UI — qbit-instances (extracted from app.js, Stage 4 split).
// Composed via { ...appQbitInstances() } in app(); methods bind `this` to the Alpine component.
function appQbitInstances() {
  return {
    // ===== qBittorrent instances =====
    //
    // Standalone CRUD list. Loaded on Settings → qBit visit, refreshed
    // after each create / update / delete + the on-boot loadConfig
    // (since /api/config also returns the list, masked-passwords).
    // The dedicated /api/qbit-instances endpoint is the source-of-truth
    // for the Settings page; loadConfig's mirror is a fallback for
    // first-render before the dedicated fetch returns.

    async loadQbitInstances() {
      try {
        const r = await this.apiFetch('/api/qbit-instances');
        if (r.ok) {
          const d = await r.json();
          this.qbitInstances = Array.isArray(d) ? d : [];
          // Kick a silent status sweep so freshly-loaded rows don't
          // sit at "Not tested" until the 60s poll catches up. Honors
          // the silent path so a manual click already in flight isn't
          // overwritten.
          this.refreshAllQbitStatus();
        }
      } catch (e) {
        // Silent — page renders empty state until next refresh.
      }
    },

    openQbitInstanceModal(qi) {
      // qi is the existing instance (edit) or undefined (create).
      // Password placeholder for edit shows '••••••••' so the user
      // knows leaving it blank preserves the stored value.
      this.qbitInstanceModal = {
        open: true,
        id: qi ? qi.id : '',
        name: qi ? qi.name : '',
        url: qi ? qi.url : '',
        username: qi ? (qi.username || '') : '',
        password: '', // never pre-populate — masked on edit, blank on create
        trustedCerts: !!(qi && qi.trustedCerts),
        busy: false,
        testing: false,
        testResult: '',
        testOk: false,
      };
    },

    closeQbitInstanceModal() {
      if (this.qbitInstanceModal.busy) return;
      this.qbitInstanceModal.open = false;
    },

    // testQbitInstanceModal hits the inline-creds endpoint that
    // probes against whatever's currently in the modal — lets the
    // user verify creds BEFORE saving. Distinct from
    // testQbitInstance() which probes against the SAVED creds for
    // a row in the list.
    async testQbitInstanceModal() {
      const m = this.qbitInstanceModal;
      if (!m.url) return;
      m.testing = true;
      m.testResult = '';
      try {
        const r = await this.apiFetch('/api/qbit-instances/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            id: m.id, // empty on create — backend ignores; populated on edit so the masked-password path can pull stored creds
            url: m.url,
            username: m.username,
            password: m.password,
            trustedCerts: m.trustedCerts,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        m.testOk = !!d.ok;
        m.testResult = d.ok ? (d.message || 'Connected') : ('Failed: ' + (d.error || 'unknown'));
        // Bridge into the row-status map when editing a saved instance,
        // so closing the modal doesn't leave the row pill stuck at "Not
        // tested" right after the user verified it works.
        if (m.id) {
          this.qbitStatus[m.id] = d.ok ? 'connected' : 'failed';
          this.qbitError[m.id] = d.ok ? '' : (d.error || '');
        }
      } catch (e) {
        m.testOk = false;
        m.testResult = 'Failed: ' + e.message;
        if (m.id) {
          this.qbitStatus[m.id] = 'failed';
          this.qbitError[m.id] = e.message;
        }
      } finally {
        m.testing = false;
      }
    },

    async saveQbitInstanceModal() {
      const m = this.qbitInstanceModal;
      if (!m.name || !m.url) return;
      m.busy = true;
      try {
        const body = {
          name: m.name,
          url: m.url,
          username: m.username,
          password: m.password,
          trustedCerts: m.trustedCerts,
        };
        const path = m.id ? '/api/qbit-instances/' + m.id : '/api/qbit-instances';
        const method = m.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        const savedId = m.id || (d && d.id);
        await this.loadQbitInstances();
        this.qbitInstanceModal.open = false;
        // Warn-on-save: the instance is saved regardless (qBit may be down
        // now, or you're setting up ahead of time), but run a connection
        // test so a bad address/port surfaces immediately instead of only
        // as a red row the user might miss.
        if (savedId) {
          await this.testQbitInstance(savedId);
          if (this.qbitStatus[savedId] === 'failed') {
            this.showToast('Saved, but could not connect: ' + this.connErrorShort(this.qbitError[savedId]), 'error');
          } else {
            this.showToast('qBit instance saved', 'success');
          }
        } else {
          this.showToast('qBit instance saved', 'success');
        }
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      } finally {
        m.busy = false;
      }
    },

    // testQbitInstance — same shape as testInstance (Arr): probes the
    // saved-creds endpoint, writes flat status/error into qbitStatus +
    // qbitError. silent=true skips the "testing" pill flash so the
    // 60s background refresh doesn't strobe every row.
    async testQbitInstance(id, silent = false) {
      if (!silent) this.qbitStatus[id] = 'testing';
      this.qbitError[id] = '';
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + id + '/test', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.qbitStatus[id] = d.ok ? 'connected' : 'failed';
        this.qbitError[id] = d.ok ? '' : (d.error || '');
      } catch (e) {
        this.qbitStatus[id] = 'failed';
        this.qbitError[id] = e.message;
      }
    },

    // Mirror refreshAllStatus for qBit. Skip rows already mid-Test so
    // a manual click doesn't get clobbered by the silent background
    // sweep.
    async refreshAllQbitStatus() {
      for (const qi of (this.qbitInstances || [])) {
        if (this.qbitStatus[qi.id] === 'testing') continue;
        this.testQbitInstance(qi.id, true);
      }
    },

    confirmDeleteQbitInstance(qi) {
      this.deleteQbitTarget = qi;
    },

    async deleteQbitInstance() {
      const target = this.deleteQbitTarget;
      if (!target) return;
      this.deleteQbitBusy = true;
      try {
        const r = await this.apiFetch('/api/qbit-instances/' + target.id, {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        await this.loadQbitInstances();
        this.deleteQbitTarget = null;
        this.showToast('qBit instance deleted', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteQbitBusy = false;
      }
    },




  };
}
