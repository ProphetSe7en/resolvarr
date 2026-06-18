// resolvarr UI — qBit Category Fix helpers module
//
// Composed into the Alpine root via { ...appQbitCategoryFix() } in app(); methods use
// this = the Alpine component (bound on spread), so peer calls work as before.
function appQbitCategoryFix() {
  return {
    // ---- qBit Category Fix helpers ----
    //
    // Loads the Arr's download-client list on demand so the user picks
    // a download client by name (not ID) and the pre/post category
    // names auto-populate from Sonarr/Radarr's own config. Cached
    // backend-side for 5min; forceRefresh=true invalidates that cache
    // so a user who just edited a category in Sonarr/Radarr UI can
    // see the change immediately.

    arrDownloadClients: [],
    arrDownloadClientsLoading: false,
    arrDownloadClientsError: '',

    async loadArrDownloadClients(arrInstanceId, forceRefresh) {
      if (!arrInstanceId) return;
      this.arrDownloadClientsLoading = true;
      this.arrDownloadClientsError = '';
      try {
        const qs = forceRefresh ? '?refresh=1' : '';
        const r = await this.apiFetch('/api/instances/' + arrInstanceId + '/download-clients' + qs);
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const data = await r.json();
        // Filter to qBit implementations only — other download-client
        // types (sabnzbd, nzbget, deluge) don't use the qBit category
        // pre/post model.
        this.arrDownloadClients = (Array.isArray(data) ? data : [])
          .filter(c => c.implementation && c.implementation.toLowerCase().includes('qbittorrent'));
        // Auto-select when there's exactly one + nothing picked yet,
        // so the typical single-qBit user lands with a working form
        // immediately on entering the step.
        const cf = this.editingRule && this.editingRule.qbitCategoryFix;
        if (cf && this.arrDownloadClients.length === 1 && !cf.arrDownloadClientId) {
          cf.arrDownloadClientId = this.arrDownloadClients[0].id;
          this.autoFillQbitCategories();
        }
      } catch (e) {
        this.arrDownloadClientsError = 'Failed to load download clients: ' + (e.message || e);
        this.arrDownloadClients = [];
      } finally {
        this.arrDownloadClientsLoading = false;
      }
    },

    // Pulls the pre/post category names out of the picked download
    // client and copies them onto the rule's snapshot fields. Called
    // on @change of the select + on auto-select.
    autoFillQbitCategories() {
      const cf = this.editingRule && this.editingRule.qbitCategoryFix;
      if (!cf || !cf.arrDownloadClientId) return;
      const dc = this.arrDownloadClients.find(c => c.id === cf.arrDownloadClientId);
      if (!dc) return;
      cf.preImportCategorySnapshot = dc.qbitPreCat || '';
      cf.postImportCategorySnapshot = dc.qbitPostCat || '';
    },

    // Inline-warning string for the qBit Category Fix step. Empty
    // string means the form is fillable / advancable; non-empty
    // returns a short message the template renders + the Next button
    // uses to gate advance.
    qbitCategoryFixWarning() {
      const cf = this.editingRule && this.editingRule.qbitCategoryFix;
      if (!cf) return '';
      if (!cf.qbitInstanceId) return 'Pick a qBit instance.';
      if (!cf.arrDownloadClientId) return 'Pick a download client from Sonarr/Radarr.';
      if (!cf.preImportCategorySnapshot || !cf.postImportCategorySnapshot) {
        return 'The selected download client doesn\'t have both Pre-import and Post-import categories configured. Set them in Sonarr/Radarr → Settings → Download Clients first.';
      }
      if (cf.preImportCategorySnapshot === cf.postImportCategorySnapshot) {
        return 'Pre-import and post-import categories must differ.';
      }
      return '';
    },

    // True when none of the three classifier rules (Episode / Season /
    // Unmatched) is enabled — backend would reject save with "must
    // enable at least one of episodeEnabled / seasonEnabled /
    // unmatchedEnabled". Surfaces inline on Step 3c so the user catches
    // it before clicking Save / Next.
    qbitSeNoRuleEnabled() {
      const q = (this.editingRule && this.editingRule.qbitSe) || {};
      return !q.episodeEnabled && !q.seasonEnabled && !q.unmatchedEnabled;
    },

    // Tag-name regex check — mirrors backend's reTagName pattern
    // (^[a-z0-9][a-z0-9_-]*$). Case-insensitive on input because
    // Radarr's API is strict (all-lowercase) but qBit accepts any
    // casing; backend lowercases the user's input before validating.
    // Empty / whitespace returns true (the validator backfills the
    // default name on save when the field is blank).
    qbitSeTagNameValid(name) {
      const trimmed = String(name || '').trim();
      if (trimmed === '') return true;
      return /^[a-z0-9][a-z0-9_-]*$/i.test(trimmed);
    },
  };
}
