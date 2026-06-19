// resolvarr UI — recover-runmode (extracted from app.js, Stage 4 split).
// Composed via { ...appRecoverRunMode() } in app(); methods bind `this` to the Alpine component.
function appRecoverRunMode() {
  return {
    // ====== Recover (Run mode sub-tab) ======
    // Bash tagarr_recover.sh parity. Standalone, not part of Quick fix-all.

    async runRecoverCheck() {
      if (!this.scanInstanceId) {
        this.showToast('Pick an instance first', 'error');
        return;
      }
      // Close any other open modal first; closeAllResultModals(except='recover')
      // also nulls historicalRunInfo so a previous snapshot banner can't carry
      // over into this fresh live run.
      this.closeAllResultModals('recover');
      this.recoverLoading = true;
      this.recoverError = '';
      this.recoverResults = null;
      this.recoverApplySelected = {};
      this.recoverExpanded = {};
      this.recoverSeriesExpanded = {};
      this.recoverSeasonExpanded = {};
      this.recoverFilter = 'all';
      // Standalone Run Recover targets a single instance — drop any
      // wizard-driven variant set so the switcher doesn't render
      // stale primary/secondary pills from an earlier Both run.
      this.qfaDetailVariants = [];
      this.qfaDetailVariantIdx = 0;
      // Fresh recover replaces any historical-run banner that was tied
      // to an earlier replay.
      if (this.historicalRunInfo && this.historicalRunInfo.kind === 'recover') {
        this.historicalRunInfo = null;
      }
      try {
        const resp = await this.apiFetch('/api/scan/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            instanceId: this.scanInstanceId,
            action: 'recover',
            mode: 'preview',
          }),
        });
        if (!resp.ok) {
          const body = await resp.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.recoverError = msg || ('HTTP ' + resp.status);
          return;
        }
        this.recoverResults = await resp.json();
        // Load the per-instance exclusion list so the result panel can
        // show the Show-excluded count + per-row Include-again buttons.
        if (this.recoverResults.instance && this.recoverResults.instance.id) {
          this.loadRecoverExclusions(this.recoverResults.instance.id);
        }
        // Auto-default would-fix rows to selected so a user who just clicks
        // Apply gets every recoverable item — they untoggle the ones they
        // don't trust. Matches bash's "fix all" semantics with the
        // container-side per-row override.
        const sel = {};
        for (const it of (this.recoverResults.recover || [])) {
          if (it.status === 'would-fix') sel[it.id] = true;
        }
        this.recoverApplySelected = sel;
        // Default filter to whichever bucket has the most action so the
        // user lands on something useful — would-fix beats flagged beats all.
        const t = this.recoverResults.totals;
        if (t.recoverWouldFix) this.recoverFilter = 'would-fix';
        else if (t.recoverFlagged) this.recoverFilter = 'flagged';
        else this.recoverFilter = 'all';
      } catch (e) {
        this.recoverError = e.message || 'Recover check failed';
      } finally {
        this.recoverLoading = false;
      }
    },
  };
}
