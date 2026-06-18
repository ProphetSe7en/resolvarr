// resolvarr UI — DV scan progress module
//
// Dolby Vision detail scan: progress poll + cancel. Composed into the Alpine
// root via { ...appDvScan() } in app(); methods use `this` = the Alpine
// component (bound on spread), so peer calls work exactly as before.
function appDvScan() {
  return {
    // ---- DV scan progress poll + cancel ----

    startDvScanPoll() {
      this.stopDvScanPoll();
      const tick = async () => {
        try {
          const r = await this.apiFetch('/api/scan/dvdetail/progress');
          if (!r.ok) return;
          const d = await r.json();
          if (d && d.running) {
            this.dvScanProgress = d;
          }
        } catch (_) {
          // Silent — next tick will retry.
        }
      };
      tick(); // immediate first poll so the UI doesn't sit blank
      // 400ms poll — fast enough that the progress bar visibly
      // increments per file on remux sources (per-file extraction is
      // tens of ms, so a 1200ms poll showed 20-file hops which looked
      // broken). Cost: ~25 GET/sec during a scan that already takes
      // ~10 seconds for a 200-file library — negligible.
      this._dvScanPollHandle = setInterval(tick, 400);
    },

    stopDvScanPoll() {
      if (this._dvScanPollHandle) {
        clearInterval(this._dvScanPollHandle);
        this._dvScanPollHandle = null;
      }
    },

    async cancelDvScan() {
      try {
        await this.apiFetch('/api/scan/dvdetail/cancel', { method: 'POST' });
        this.showToast('Cancelling DV scan — partial result will land when current file finishes', 'info');
      } catch (e) {
        this.showToast('Cancel failed: ' + e.message, 'error');
      }
    },

    // Sum of pending tag-changes from the most recent DV detail Preview.
    // Used by the Apply confirm modal so the user sees the actual diff
    // size before committing. Returns 0 when no preview has run yet —
    // the modal then shows a generic warning rather than "0 changes".
    dvDetailPendingChangeCount() {
      const r = this.scanResults && this.scanResults.dvDetail;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },

    openDvDetailApplyConfirm() {
      // Don't gate on count — even with 0 known changes, Apply against
      // a fresh cache walks the library and may discover changes the
      // user hasn't previewed. Modal copy adapts via the count check.
      this.showDvDetailApplyConfirm = true;
    },

    async confirmDvDetailApply() {
      this.showDvDetailApplyConfirm = false;
      // Re-entry guard — see confirmScanApply for rationale.
      // Especially important for DV: target=both fan-out + slow
      // ffmpeg+dovi_tool means a double-fire would queue two long
      // scans against the same instance and snapshot dvBypassCache
      // in inconsistent states.
      if (this.scanLoading) return;
      // Reset Skip cache before Apply so a destructive write doesn't
      // silently inherit a Preview's bypass setting. Snapshot first
      // so the actual run uses whatever the user had ticked.
      const bypass = !!this.dvBypassCache;
      this.dvBypassCache = false;
      // Same target=both fan-out as confirmAudioTagsApply. See its
      // header comment for the design rationale. Both runs honor the
      // same bypassDvCache snapshot so a fresh-cache pass is fresh
      // for both instances.
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        const totals = { added: 0, removed: 0 };
        for (const v of variants) {
          await this.runDvDetailScan('apply', bypass, v.instanceId);
          if (this.scanError) break;
          // Refresh variant response — see confirmAudioTagsApply.
          if (this.scanResults.dvDetail) {
            v.response = this.scanResults.dvDetail;
          }
          const a = this.scanResults.dvDetail && this.scanResults.dvDetail.applied;
          if (a) {
            totals.added += a.itemsAdded || 0;
            totals.removed += a.itemsRemoved || 0;
          }
        }
        if (!this.scanError) {
          this.showToast('DV detail applied across ' + variants.length + ' instances: ' + totals.added + ' added, ' + totals.removed + ' removed', 'success');
        }
      } else {
        await this.runDvDetailScan('apply', bypass);
      }
    },

    // --- Groups ---
    setGroupsSection(section) {
      this.groupsSection = section;
      localStorage.setItem('resolvarr-groups-section', section);
      this.pushNav();
    },

    async loadGroups() {
      this.groupsLoadError = '';
      try {
        const r = await this.apiFetch('/api/groups');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.groups = (await r.json()) || [];
      } catch (e) {
        this.groupsLoadError = e.message;
      }
      // Refresh the In-primary / In-secondary counts whenever the source
      // group list changes (post-load, post-save, post-delete).
      this.loadGroupTagCounts();
    },

    // Returns the currently-selected scan-instance object, or null.
    activeInstance() {
      if (!this.scanInstanceId) return null;
      return this.instances.find(i => i.id === this.scanInstanceId) || null;
    },

    // Library scan App-type pill helpers — same shape as the Tag
    // inventory equivalents (tagsAvailableInstances etc.).
    scanAvailableInstances() {
      return this.instances
        .filter(i => i.type === this.scanAppType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },
    scanAppTypeAvailable(type) {
      return this.instances.some(i => i.type === type);
    },
    // Sub-tab visibility per app-type. Sonarr currently supports only
    // Recover (lives on Run mode) + History. Tag library / Discover /
    // Sub-tab visibility per active app-type.
    //
    // Restructure 2026-05-05: Tag library + Release Groups + Recover
    // (Sonarr) all merged into one "Tag quality releases" sub-tab
    // (internal id 'groups'). Standalone 'tag' and 'recover' sub-tabs
    // are unreachable from sidebar — kept in markup as legacy x-show
    // gates so any existing localStorage value or deep-linked state
    // doesn't 404, but new clicks always route through 'groups'.
    //
    // Sonarr coverage today inside "Tag quality releases": Recover
    // works; Tag run + Discover are stubbed with M-Sonarr badges.
    // Filters + DV detail remain Radarr-only.
    scanSectionVisible(section) {
      // Standalone tag + recover + filters sub-tabs were folded into
      // 'groups' / wizardent during the 2026-05-05 restructure. Hide
      // their sidebar entries on every app type. Filter persistence
      // moves to the wizard; Cleanup moves under Active groups.
      if (section === 'tag' || section === 'recover' || section === 'filters') return false;
      // Plex label sync rules bind to one specific Arr instance — so the
      // sub-tab respects the page-level app-type pill (showing the user
      // only rules + instances of the picked type). Consistent with how
      // every other sub-tab (Tag library / Audio / Video / DV) is gated
      // by scanAppType.
      if (section === 'plex-sync') return true;
      // Profile by tag works the same on both Arr types (quality profile +
      // tags exist on movie and series alike).
      if (section === 'profile-by-tag') return true;
      if (this.scanAppType === 'sonarr') {
        // Sonarr's "Tag quality releases" tab carries the Recover action
        // + stubbed Tag/Discover for naming consistency with Radarr.
        // Filters + DV detail are Radarr-only. Missing episodes is
        // Sonarr-only (Radarr doesn't have the per-episode model).
        return section === 'run'    || section === 'groups' ||
               section === 'audio'  || section === 'video'  ||
               section === 'missing-episodes' ||
               section === 'tba-refresh' ||
               section === 'qbit-se' ||
               section === 'reconcile' ||
               section === 'history';
      }
      // Radarr: every visible section EXCEPT the Sonarr-only ones.
      // (qbit-se = qBit Season/Episode tagging is Sonarr-only too.)
      if (section === 'missing-episodes' || section === 'tba-refresh' || section === 'qbit-se') return false;
      return true;
    },

    setScanAppType(type) {
      if (type !== 'radarr' && type !== 'sonarr') return;
      if (!this.scanAppTypeAvailable(type)) return;
      if (this.scanAppType === type) return;
      this.scanAppType = type;
      localStorage.setItem('resolvarr-scan-app-type', type);
      // Rebind scanInstanceId to the first instance of the new type, or
      // clear it. Switching to a section that's not visible for the new
      // type also gets nudged back to 'run' so the user lands somewhere.
      const first = this.scanAvailableInstances()[0];
      this.scanInstanceId = first ? first.id : '';
      if (this.scanInstanceId) {
        // Reuse the existing onChange handler so groups + cached state
        // refresh consistently with a manual instance pick.
        this.onGroupsInstanceChange();
      }
      if (!this.scanSectionVisible(this.scanSection)) {
        this.setScanSection('run');
      }
      this.pushNav();
    },

    // Returns 'radarr' | 'sonarr' for the active instance, defaulting to
    // 'radarr' so the Active-groups table has a sensible filter while no
    // instance is picked. Drives both the type-filter on the table and the
    // header label ("Showing N Radarr groups").
    activeInstanceType() {
      const inst = this.activeInstance();
      return inst ? inst.type : 'radarr';
    },

    // Find a sibling instance of the same type as the active one, used to
    // populate the "In secondary" column. Returns null when only one
    // instance of this type exists; the column is hidden in that case.
    secondaryInstanceForGroups() {
      const inst = this.activeInstance();
      if (!inst) return null;
      return this.instances.find(i => i.type === inst.type && i.id !== inst.id) || null;
    },

    // Filters the groups list by the active instance's type. Used by both
    // the table render and the count badge in the picker header.
    groupsFilteredByInstanceType() {
      const t = this.activeInstanceType();
      return (this.groups || []).filter(g => (g.type || 'radarr') === t);
    },

    // Look up a group's usage count in either the primary or secondary
    // instance. Returns 0 when the tag doesn't exist on that instance yet
    // (created lazily on first apply).
    groupTagCount(g, which) {
      const map = this.tagsByLabel[which] || {};
      const entry = map[(g.tag || '').toLowerCase()];
      return entry ? entry.count : 0;
    },

    // Look up the tag id for a group on either side. Returns null when
    // the tag hasn't been created on that instance yet (count 0 case).
    groupTagId(g, which) {
      const map = this.tagsByLabel[which] || {};
      const entry = map[(g.tag || '').toLowerCase()];
      return entry ? entry.id : null;
    },

    // Fetches /api/instances/{id}/tags for the active instance (and its
    // same-type sibling, if any), collapsing the response to a
    // label→{id,count} lookup keyed by lowercased label. Errors are
    // swallowed silently — the count column gracefully falls back to 0
    // when a fetch fails. The Tags tab will surface the underlying error
    // if it's a real auth/conn issue. Loading flags drive the "…"
    // placeholder in the table cells.
    async loadGroupTagCounts() {
      const primary = this.activeInstance();
      const secondary = this.secondaryInstanceForGroups();
      // Set loading flags BEFORE clearing the maps so there's no
      // microtask window where Alpine sees both tagsByLabel={} AND
      // loading=false → cells flash to "0" before the "…" placeholder
      // takes over. Order matters: synchronous mutations land in this
      // order in the same render tick.
      if (primary) this.tagCountsLoading.primary = true;
      if (secondary) this.tagCountsLoading.secondary = true;
      this.tagsByLabel = { primary: {}, secondary: {} };
      const fetchFor = async (inst, slot) => {
        if (!inst) return;
        try {
          const r = await this.apiFetch('/api/instances/' + inst.id + '/tags');
          if (!r.ok) return;
          const list = await r.json();
          const map = {};
          for (const t of (list || [])) {
            map[(t.label || '').toLowerCase()] = { id: t.id, count: t.usageCount || 0 };
          }
          this.tagsByLabel[slot] = map;
        } catch (e) {
          // Silent — count column shows 0; Tags tab surfaces real errors.
        } finally {
          this.tagCountsLoading[slot] = false;
        }
      };
      await Promise.all([
        fetchFor(primary, 'primary'),
        fetchFor(secondary, 'secondary'),
      ]);
    },

    // Triggered from the sub-tab picker — re-load counts for the new
    // instance pair. Discover/Cleanup blocks below share scanInstanceId,
    // so they pick up the change automatically; we still clear any stale
    // results that were tied to the old instance to avoid confusion.
    onGroupsInstanceChange() {
      this.clearScanResultsForInstanceChange();
      this.loadGroupTagCounts();
    },

    // Opens the drill-down modal listing every movie/series tagged with
    // this group's tag on the chosen instance. Same data source as the
    // Tag-inventory drill-down (/api/instances/{id}/tag-items), but
    // surfaced from a single click on the count cell. No-ops on count==0
    // (the cell handler short-circuits before getting here).
    async openGroupItems(g, slot) {
      const inst = slot === 'secondary' ? this.secondaryInstanceForGroups() : this.activeInstance();
      const tagId = this.groupTagId(g, slot);
      const count = this.groupTagCount(g, slot);
      if (!inst || !tagId || count === 0) return;
      this.groupItemsTarget = { group: g, slot, instance: inst, tagId, count };
      this.groupItemsList = [];
      this.groupItemsError = '';
      this.groupItemsLoading = true;
      this.showGroupItemsModal = true;
      try {
        const r = await this.apiFetch('/api/instances/' + inst.id + '/tag-items?ids=' + tagId);
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          throw new Error(msg || 'HTTP ' + r.status);
        }
        const data = await r.json();
        const grp = (data || []).find(x => x.tagId === tagId);
        this.groupItemsList = (grp && grp.items) || [];
      } catch (e) {
        this.groupItemsError = e.message || 'Failed to load items';
      } finally {
        this.groupItemsLoading = false;
      }
    },

    closeGroupItems() {
      this.showGroupItemsModal = false;
      this.groupItemsTarget = null;
      this.groupItemsList = [];
      this.groupItemsError = '';
    },

    openGroupModal(g) {
      if (g) {
        this.groupForm = { id: g.id, search: g.search, tag: g.tag, display: g.display, mode: g.mode };
      } else {
        this.groupForm = { id: '', search: '', tag: '', display: '', mode: 'filtered' };
      }
      this.groupFormError = '';
      this.groupFormBusy = false;
      this.showGroupModal = true;
    },

    closeGroupModal() {
      this.showGroupModal = false;
    },

    async saveGroup() {
      this.groupFormError = '';
      // Client-side validation mirrors server-side checks so users see
      // fast feedback before the round-trip.
      const search = (this.groupForm.search || '').trim();
      const tag = (this.groupForm.tag || '').trim().toLowerCase();
      const display = (this.groupForm.display || '').trim();
      if (!search) { this.groupFormError = 'Search string is required.'; return; }
      if (!tag) { this.groupFormError = 'Tag name is required.'; return; }
      if (!/^[a-z0-9][a-z0-9_-]*$/.test(tag)) {
        this.groupFormError = 'Tag name must be lowercase letters, digits, underscores, or dashes.';
        return;
      }
      if (!display) { this.groupFormError = 'Display name is required.'; return; }
      if (this.groupForm.mode !== 'filtered' && this.groupForm.mode !== 'simple') {
        this.groupFormError = 'Pick a mode.';
        return;
      }

      this.groupFormBusy = true;
      try {
        const payload = { search, tag, display, mode: this.groupForm.mode };
        const url = this.groupForm.id ? '/api/groups/' + this.groupForm.id : '/api/groups';
        const method = this.groupForm.id ? 'PUT' : 'POST';
        const r = await this.apiFetch(url, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          throw new Error(err.error || 'HTTP ' + r.status);
        }
        await this.loadGroups();
        this.showGroupModal = false;
        this.showToast(this.groupForm.id ? 'Group updated' : 'Group added', 'success');
      } catch (e) {
        this.groupFormError = e.message;
      } finally {
        this.groupFormBusy = false;
      }
    },

    // deleteGroup opens our own confirm modal rather than window.confirm().
    // The native popup is jarring against the rest of the styled UI and
    // also blocks the event loop, which can race with background polling.
    deleteGroup(g) {
      this.deleteGroupTarget = g;
    },

    async confirmDeleteGroup() {
      const g = this.deleteGroupTarget;
      if (!g) return;
      this.deleteGroupBusy = true;
      try {
        const r = await this.apiFetch('/api/groups/' + g.id, { method: 'DELETE' });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        await this.loadGroups();
        // Surface the actual identity in the toast so the user sees
        // exactly what disappeared — useful when removing several in
        // a row, where a generic "Group removed" gives no confirmation
        // they hit the right one. Show the search-string + tag-label
        // pair (display-name varies per group).
        const label = g.tag ? ` → ${g.tag}` : '';
        this.showToast(`Group removed: ${g.search}${label}`, 'success');
        this.deleteGroupTarget = null;
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      } finally {
        this.deleteGroupBusy = false;
      }
    },

    // setGroupMode sets a group's mode from the segmented control in the
    // list — no need to open the Edit modal for a one-field change. Caller
    // passes the target mode (segmented buttons are disabled when already
    // active, so we don't guard here). Server is the source of truth; we
    // re-load from the list endpoint on success rather than assuming the
    // PUT echo is authoritative.
    async setGroupMode(g, nextMode) {
      if (this.groupTogglingId) return; // one at a time
      if (g.mode === nextMode) return;
      this.groupTogglingId = g.id;
      try {
        const r = await this.apiFetch('/api/groups/' + g.id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            search: g.search,
            tag: g.tag,
            display: g.display,
            mode: nextMode,
            type: g.type,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          throw new Error(err.error || 'HTTP ' + r.status);
        }
        await this.loadGroups();
        this.showToast(g.display + ' → ' + (nextMode === 'filtered' ? 'Filtered' : 'Simple'), 'success');
      } catch (e) {
        this.showToast('Mode change failed: ' + e.message, 'error');
      } finally {
        this.groupTogglingId = null;
      }
    },

    // toggleGroupEnabled flips a group's enabled flag from the list toggle
    // switch. A disabled group stays in the config — all settings are
    // preserved — but every scan mode skips it. Equivalent to commenting
    // out the `#` row in the bash RELEASE_GROUPS array. Uses the Enabled
    // pointer on the backend request so we don't have to resend every
    // other field (Edit modal still does that).
    async toggleGroupEnabled(g) {
      if (this.groupTogglingId) return;
      this.groupTogglingId = g.id;
      const nextEnabled = !g.enabled;
      try {
        const r = await this.apiFetch('/api/groups/' + g.id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            search: g.search,
            tag: g.tag,
            display: g.display,
            mode: g.mode,
            type: g.type,
            enabled: nextEnabled,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          throw new Error(err.error || 'HTTP ' + r.status);
        }
        await this.loadGroups();
        this.showToast(g.display + ' ' + (nextEnabled ? 'enabled' : 'disabled'), 'success');
      } catch (e) {
        this.showToast('Enable toggle failed: ' + e.message, 'error');
      } finally {
        this.groupTogglingId = null;
      }
    },

    // --- Filters ---
    async saveFilters() {
      try {
        // Backend expects per-Arr-type FilterSet shape:
        // {radarr: {Quality, ...}, sonarr: {...}}. The UI only edits
        // the Radarr block today; we round-trip whatever Sonarr block
        // the server returned at load (or default-on if none) so a
        // save-from-UI never wipes the Sonarr-side defaults that
        // Load() backfilled.
        const radarrBlock = {
          Quality: this.filters.quality,
          MAWebDL: this.filters.maWebDL,
          PlayWebDL: this.filters.playWebDL,
          Audio: this.filters.audio,
          TrueHD: this.filters.trueHD,
          TrueHDAtmos: this.filters.trueHDAtmos,
          DTSX: this.filters.dtsX,
          DTSHDMA: this.filters.dtsHDMA,
        };
        const sonarrBlock = this._savedSonarrFilters || {
          Quality: true, MAWebDL: true, PlayWebDL: true, Audio: true,
          TrueHD: true, TrueHDAtmos: true, DTSX: true, DTSHDMA: true,
        };
        const r = await this.apiFetch('/api/filters', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ radarr: radarrBlock, sonarr: sonarrBlock }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },
  };
}
