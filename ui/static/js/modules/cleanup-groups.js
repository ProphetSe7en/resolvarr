// resolvarr UI — cleanup-groups (extracted from app.js, Stage 4 split).
// Composed via { ...appCleanupGroups() } in app(); methods bind `this` to the Alpine component.
function appCleanupGroups() {
  return {
    // ====== Cleanup unused tags (Release Groups sub-tab) ======
    // Standalone cleanup entry point — runs against scanInstanceId, surfaces
    // managed tags with 0 movies, lets user delete per-row or in bulk.
    // Backend is the same /api/scan/run with action=cleanup; preview lists
    // candidates, apply (with optional cleanupLabels filter) deletes them.

    async runCleanupCheck() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      this.closeAllResultModals('cleanup');
      this.cleanupLoading = true;
      this.cleanupError = '';
      this.cleanupResults = null;
      this.cleanupSelected = {};
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'cleanup',
            mode: 'preview',
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.cleanupError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.cleanupResults = await resp.json();
      } catch (e) {
        this.cleanupError = e.message || 'Cleanup check failed';
      } finally {
        this.cleanupLoading = false;
      }
    },

    toggleCleanupSelected(label) {
      const next = { ...this.cleanupSelected };
      if (next[label]) delete next[label];
      else next[label] = true;
      this.cleanupSelected = next;
    },
    selectAllCleanup() {
      if (!this.cleanupResults || !this.cleanupResults.totals.tagsToDelete) return;
      const next = {};
      for (const c of this.cleanupResults.totals.tagsToDelete) next[c.label] = true;
      this.cleanupSelected = next;
    },
    deselectAllCleanup() {
      this.cleanupSelected = {};
    },
    cleanupSelectedCount() {
      return Object.keys(this.cleanupSelected).length;
    },

    // Per-row Delete — deletes a single label. Reuses the same cleanup-apply
    // backend path with cleanupLabels=[label] so the safety-bound logic
    // applies (label must be a valid candidate).
    async deleteOneCleanup(label) {
      await this.applyCleanupDeletes([label]);
    },

    // Bulk Delete-Selected — submits everything in cleanupSelected.
    async deleteSelectedCleanup() {
      await this.applyCleanupDeletes(Object.keys(this.cleanupSelected));
    },

    // applyCleanupDeletes is the shared backend call. After a successful
    // delete, removes the deleted labels from the in-memory cleanup result
    // so the row(s) disappear from the UI without a full re-fetch. Also
    // clears those labels from cleanupSelected.
    async applyCleanupDeletes(labels) {
      if (!labels || labels.length === 0) return;
      // Defense in depth — same reasoning as runRecoverApply / Discover.
      if (this.isHistoricalForAction('cleanup')) {
        this.showToast('Run a fresh Cleanup check before deleting — current panel is a snapshot.', 'error');
        return;
      }
      this.cleanupDeleting = true;
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'cleanup',
            mode: 'apply',
            cleanupLabels: labels,
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.showToast('Delete failed: ' + msg, 'error');
          return;
        }
        const data = await resp.json();
        const deleted = (data.applied && data.applied.tagsDeleted) || [];
        if (deleted.length === 0) {
          this.showToast('Nothing to delete', 'error');
          return;
        }
        const deletedSet = new Set(deleted);
        // Prune in-memory result + selection so UI updates immediately.
        if (this.cleanupResults && this.cleanupResults.totals && Array.isArray(this.cleanupResults.totals.tagsToDelete)) {
          this.cleanupResults.totals.tagsToDelete = this.cleanupResults.totals.tagsToDelete.filter(c => !deletedSet.has(c.label));
        }
        const nextSel = { ...this.cleanupSelected };
        for (const l of deleted) delete nextSel[l];
        this.cleanupSelected = nextSel;
        this.showToast('Deleted ' + deleted.length + ' tag' + (deleted.length === 1 ? '' : 's'), 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + (e.message || 'unknown'), 'error');
      } finally {
        this.cleanupDeleting = false;
      }
    },

    // Cleanup-only path used by Quick fix-all when 'cleanup' is on but 'tag'
    // is off. Runs a fresh cleanup-apply against current Radarr state — no
    // tag-pass deltas to factor in. Shows a toast with the count.
    async runCleanupApplyAll() {
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'cleanup',
            mode: 'apply',
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.scanError = msg || ('HTTP ' + resp.status);
          return;
        }
        const data = await resp.json();
        const deleted = (data.applied && data.applied.tagsDeleted) || [];
        this.showToast('Cleanup: ' + deleted.length + ' tag' + (deleted.length === 1 ? '' : 's') + ' deleted', 'success');
      } catch (e) {
        this.scanError = e.message || 'Cleanup failed';
      }
    },
  };
}
