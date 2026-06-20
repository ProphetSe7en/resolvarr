// resolvarr UI — webhook-config-wizard (extracted from app.js, Stage 4 split).
// Composed via { ...appWebhookConfigWizard() } in app(); methods bind `this` to the Alpine component.
function appWebhookConfigWizard() {
  return {
    // ===== Webhook configuration wizard =====
    //
    // Two-step (Choices → Summary), Arr-type-first layout matching
    // the QFA / Tag / Discover wizards. Today's only function pick
    // is "Enable logging"; later releases add per-function checkboxes
    // (release-group tag on import, DV detail on import, qBit S/E
    // tag on grab, etc.) that drive the Step-2 summary's "Connect
    // events to enable" list.

    openWebhookWizard() {
      // Seed Arr-type from the page-level pill (webhookAppType).
      // User picks the type out front via the pills, so the wizard
      // mirrors that choice rather than re-deriving from instance
      // pool. Wizard's internal Arr-type radios still let the user
      // flip mid-configuration, mostly for users who haven't seen
      // the pill yet.
      const insts = this.instances || [];
      let appType = this.webhookAppType || 'radarr';
      // Defence: if the picked app-type has no instances (shouldn't
      // happen since the pill is :disabled in that state, but
      // keyboard navigation could still trigger), fall back to
      // whichever type does have one.
      const hasPicked = insts.some(i => i.type === appType);
      if (!hasPicked) {
        const hasRadarr = insts.some(i => i.type === 'radarr');
        const hasSonarr = insts.some(i => i.type === 'sonarr');
        if (hasRadarr) appType = 'radarr';
        else if (hasSonarr) appType = 'sonarr';
      }
      // Seed instance: last-used remembered (when still in pool of
      // the picked appType — note: remembered may be from a different
      // type, in which case we ignore it) → first-of-type.
      const remembered = this.recallWizardInstance('webhook', insts.filter(i => i.type === appType));
      const firstOfType = insts.find(i => i.type === appType);
      const seedId = remembered || (firstOfType ? firstOfType.id : '');
      this.webhookWizard = {
        open: true,
        step: 0,
        appType,
        instanceId: seedId,
        fnLogging: true,
        busy: false,
        generatedUrl: '',
        generatedSecret: '',
        generatedLoggingEnabled: false,
        requireSignature: false,
      };
    },

    closeWebhookWizard() {
      if (this.webhookWizard.busy) return;
      this.webhookWizard.open = false;
    },

    webhookWizardVisibleSteps() {
      return ['Choices', 'Summary'];
    },

    // Reactive instance list for the Step 1 picker — filtered to
    // the chosen Arr type, sorted by name.
    webhookWizardInstancesForType() {
      return (this.instances || [])
        .filter(i => i.type === this.webhookWizard.appType)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // When the user flips the Arr-type radio, re-seed instanceId
    // to the first instance of the new type (or empty when none).
    webhookWizardSetAppType(type) {
      this.webhookWizard.appType = type;
      const list = this.webhookWizardInstancesForType();
      this.webhookWizard.instanceId = list.length > 0 ? list[0].id : '';
    },

    webhookWizardCanAdvance() {
      const cur = this.webhookWizardVisibleSteps()[this.webhookWizard.step];
      if (cur === 'Choices') {
        // Hard gate: must have an instance picked + at least one
        // function ticked (today: logging is the only function so
        // this collapses to "fnLogging must be true").
        if (!this.webhookWizard.instanceId) return false;
        if (!this.webhookWizard.fnLogging) return false;
        return true;
      }
      return true;
    },

    webhookWizardPrev() {
      if (this.webhookWizard.step > 0) this.webhookWizard.step--;
    },

    // Advance from Choices → Summary. This is where we actually
    // mutate config: persist the function picks (Logging) and,
    // if the instance has no token yet, generate one. Existing
    // tokens are preserved so a user re-running the wizard for an
    // already-configured instance doesn't accidentally rotate.
    async webhookWizardNext() {
      if (!this.webhookWizardCanAdvance()) return;
      const cur = this.webhookWizardVisibleSteps()[this.webhookWizard.step];
      if (cur === 'Choices') {
        await this.webhookWizardCommit();
        return;
      }
      // No further forward steps today; Summary is terminal (footer
      // shows Close instead of Next).
    },

    async webhookWizardCommit() {
      const { instanceId, fnLogging } = this.webhookWizard;
      // Remember the picked instance for next time the wizard opens.
      if (instanceId) this.rememberWizardInstance('webhook', instanceId);
      this.webhookWizard.busy = true;
      try {
        // If instance already has a token, just update the logging
        // flag. Otherwise rotate to generate one. Both calls return
        // a JSON body containing { token, url, loggingEnabled }.
        const existing = this.webhookConfigs[instanceId];
        let result;
        if (existing && existing.token) {
          // Toggle logging only — token preserved. Then re-fetch the
          // full config so URL stays computed against the current
          // request host.
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/logging', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ loggingEnabled: !!fnLogging }),
          });
          if (!r.ok) {
            const d = await r.json();
            throw new Error(d.error || 'HTTP ' + r.status);
          }
          // Re-fetch to get the URL (the logging endpoint doesn't
          // return it). On failure surface the error rather than
          // silently advancing with the cached URL — that URL was
          // computed against a previous request's host header and
          // could be stale if the user moved the container behind
          // a different reverse proxy.
          const g = await this.apiFetch('/api/instances/' + instanceId + '/webhook');
          if (!g.ok) {
            const d = await g.json().catch(() => ({}));
            throw new Error(d.error || 'fetch URL: HTTP ' + g.status);
          }
          result = await g.json();
        } else {
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ loggingEnabled: !!fnLogging }),
          });
          const d = await r.json();
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          result = d;
        }
        // Mirror into the page-level cache via spread-assign so the
        // Setup sub-tab sees the new state without a manual refresh
        // and Alpine reliably re-renders even on first-write to a
        // previously-untracked instance key.
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: result };
        this.webhookWizard.generatedUrl = result.url || '';
        // Phase 2 Slice A: rotate response includes the new Secret +
        // preserved RequireSignature flag. handleWebhookSetLogging's
        // response doesn't carry these, but we re-fetched via GET
        // /webhook above when the token already existed, so the
        // result has them in both branches.
        this.webhookWizard.generatedSecret = result.secret || '';
        this.webhookWizard.requireSignature = !!result.requireSignature;
        this.webhookWizard.generatedLoggingEnabled = !!result.loggingEnabled;
        this.webhookWizard.step = 1;
      } catch (e) {
        this.showToast('Webhook setup failed: ' + e.message, 'error');
      } finally {
        this.webhookWizard.busy = false;
      }
    },

    // Connect-events-to-enable matrix for the Summary step. Today
    // logging is everything-mode — Sonarr/Radarr should toggle ALL
    // event types so resolvarr can capture them. When functions
    // land, this list will whittle to only the events the picked
    // functions need.
    webhookWizardArrEvents() {
      const isSonarr = this.webhookWizard.appType === 'sonarr';
      // Matches Sonarr's / Radarr's actual checkbox labels in their
      // Connect → Webhook config form so users can match them
      // 1:1 in the Summary step.
      if (isSonarr) {
        return [
          'On Test (verify connectivity)',
          'On Grab',
          'On Import (Download)',
          'On Upgrade (Download — upgrade)',
          'On Episode File Delete',
          'On Series Add',
          'On Series Delete',
          'On Health Issue (optional)',
          'On Health Restored (optional)',
          'On Application Update (optional)',
        ];
      }
      return [
        'On Test (verify connectivity)',
        'On Grab',
        'On Import',
        'On Upgrade',
        'On Rename',
        'On Movie Added',
        'On Movie Delete',
        'On Movie File Delete',
        'On Health Issue (optional)',
        'On Health Restored (optional)',
        'On Application Update (optional)',
      ];
    },

    // Copy the wizard's generated URL.
    async copyWebhookWizardUrl() {
      if (!this.webhookWizard.generatedUrl) return;
      const ok = await this.copyToClipboard(this.webhookWizard.generatedUrl);
      this.showToast(
        ok ? 'URL copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Copy the wizard's generated Secret. Same UX pattern as
    // copyWebhookWizardUrl — toast on success/failure. The Secret is
    // the shared password the user pastes into Sonarr/Radarr → Connect
    // → Webhook → password. Phase 2 Slice A.
    async copyWebhookWizardSecret() {
      if (!this.webhookWizard.generatedSecret) return;
      const ok = await this.copyToClipboard(this.webhookWizard.generatedSecret);
      this.showToast(
        ok ? 'Secret copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Toggle the Require-signature flag from within the wizard's
    // Summary step. Optimistic update with revert-on-failure, same
    // pattern as toggleWebhookLogging. Backend validator rejects
    // {enabled:true} with 400 when stored Secret is empty — we
    // shouldn't hit that here because the wizard reached Summary by
    // generating both Token + Secret, but we surface the error
    // through the toast just in case. Phase 2 Slice B.
    async toggleWebhookWizardRequireSignature(enabled) {
      const instanceId = this.webhookWizard.instanceId;
      if (!instanceId) return;
      const prev = this.webhookWizard.requireSignature;
      this.webhookWizard.requireSignature = enabled;
      try {
        const r = await this.apiFetch(
          '/api/instances/' + instanceId + '/webhook/require-signature',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled }),
          },
        );
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Mirror the new flag into the page-level cache so the
        // Setup-tab card pill stays in sync without a refresh.
        const cur = this.webhookConfigs[instanceId] || {};
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { ...cur, requireSignature: enabled },
        };
        this.showToast(
          enabled
            ? 'Require signature on — events without the Secret will be rejected'
            : 'Require signature off — events without the Secret pass with a warning',
          'success',
        );
      } catch (e) {
        // Revert optimistic update.
        this.webhookWizard.requireSignature = prev;
        this.showToast('Require-signature toggle failed: ' + e.message, 'error');
      }
    },

    // Per-instance Require-signature toggle from the Setup tab's
    // instance card. Same backend endpoint; this just lives outside
    // the wizard so users can flip strict mode anytime. Phase 2
    // Slice B.
    async toggleWebhookRequireSignature(instanceId, enabled) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.token) return;
      const prev = !!cfg.requireSignature;
      this.webhookConfigs = {
        ...this.webhookConfigs,
        [instanceId]: { ...cfg, requireSignature: enabled },
      };
      try {
        const r = await this.apiFetch(
          '/api/instances/' + instanceId + '/webhook/require-signature',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled }),
          },
        );
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        // Revert.
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { ...cfg, requireSignature: prev },
        };
        this.showToast('Require-signature toggle failed: ' + e.message, 'error');
      }
    },

    // formatBytes renders a byte count in B / KB / MB / GB with one
    // decimal place. Used for the DV cache file-size display. KiB-style
    // 1024-base for "what the OS reports" matches du/ls expectations.
    formatBytes(n) {
      if (n == null || isNaN(n)) return '0 B';
      if (n < 1024) return n + ' B';
      if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
      if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
      return (n / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    },

    // dateFormatOptions builds the Intl.DateTimeFormat options object
    // shared by formatDate + scheduleNextFires + scheduleNextRun so
    // every timestamp in the UI honours the same Settings → Display
    // preference. Only sets hour12 when the user explicitly picks one
    // — passing undefined hour12 + a locale lets Intl pick the
    // locale-native default, which is what the "auto" choice means.
    dateFormatOptions() {
      const opts = {};
      if (this.serverTimezone) opts.timeZone = this.serverTimezone;
      if (this.timeFormat === '24h') opts.hour12 = false;
      else if (this.timeFormat === '12h') opts.hour12 = true;
      return opts;
    },

    // highlightMatchTokens wraps tokens that drove the filter pass in
    // CSS-class spans so the user can see at a glance WHY a movie
    // qualified. Three classes (palette + style live in components.css):
    //   - .tok-rg      release group (blue, matched as `-<RG>` or bare)
    //   - .tok-quality quality-filter tokens (MA WEB-DL, Play WEB-DL)
    //   - .tok-audio   audio-filter tokens (TrueHD, Atmos, DTS-X, DTS-HD MA)
    //
    // Order matters: longer patterns first so DTS-HD MA isn't partially
    // matched by DTS alone. Word boundaries via \b avoid coloring tokens
    // mid-word (e.g. "DTSomething" wouldn't match \bDTS\b).
    //
    // We HTML-escape the input first so user-controlled scene/path strings
    // can't inject markup; the wrapping <span> tags are added on top of
    // the escaped text. Search term is escaped for regex too — release
    // groups like `126811` are safe but `[GROUP]` would otherwise blow up.
    //
    // Note: the spans use class= rather than inline style so the design
    // tokens (palette, dotted-underline vs filled-bg, etc.) live in CSS.
    // One stylesheet update changes every panel that calls this helper.
    highlightMatchTokens(text, releaseGroup, opts) {
      if (!text) return '';
      const options = opts || {};
      // Escape HTML
      let html = String(text)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');

      const wrap = (re, cls) => {
        html = html.replace(re, (m) => `<span class="${cls}">${m}</span>`);
      };

      // Quality + audio tokens are filter-decision context — only useful
      // where the user is checking whether a release passes the engine's
      // filter set (scan results, discover samples). Compare-tags and the
      // tag-inventory drill-down don't care about filter passage; they're
      // listing tag membership. opts.releaseGroupOnly suppresses these
      // wraps so those views stay clean (just the blue tag-label highlight).
      if (!options.releaseGroupOnly) {
        // Audio tokens — longest first so DTS-HD MA isn't pre-eaten.
        wrap(/\bDTS[._\-\s]?HD[._\-\s]?MA\b/gi, 'tok-audio');
        wrap(/\bDTS[._\-\s]?X\b/gi, 'tok-audio');
        wrap(/\bAtmos\b/gi, 'tok-audio');
        wrap(/\bTrueHD\b/gi, 'tok-audio');

        // Quality tokens — bash check_quality_match uses these regexes:
        //   MA WEB-DL  : \bma(\]?\s*\[?|[._-])web([-.]?dl)?
        //   Play WEB-DL: \bplay(\]?\s*\[?|[._-])web([-.]?dl)?
        wrap(/\bMA([._\-\s]|\][\s_]?\[?)WEB(?:[._\-]?DL)?\b/gi, 'tok-quality');
        wrap(/\bPlay([._\-\s]|\][\s_]?\[?)WEB(?:[._\-]?DL)?\b/gi, 'tok-quality');
      }

      // Release group — match `-<RG>` (the typical "filename-FLUX" form)
      // AND bare `<RG>` (the case where the value IS the release group,
      // e.g. the releaseGroup: field on its own). Optional dash prefix
      // + word boundaries keeps it from matching mid-token. Escape
      // regex metacharacters in the group name to be safe.
      //
      // releaseGroup may be a string (single RG, the typical case) or an
      // array (used by compare-tags where two tag labels both want
      // highlighting in the same file-context block).
      const rgs = Array.isArray(releaseGroup) ? releaseGroup : (releaseGroup ? [releaseGroup] : []);
      for (const rg of rgs) {
        if (!rg) continue;
        const rgEsc = rg.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        wrap(new RegExp('-?\\b' + rgEsc + '\\b', 'gi'), 'tok-rg');
      }

      return html;
    },

    // Returns the subset of scanResult.items that match the current filter
    // chip. 'all' returns everything; the action filters return items that
    // contain at least one decision with the matching action. 'nofile'
    // returns items with no decisions (which indicates the backend skipped
    // them because MovieFile was nil).
    filteredScanItems() {
      // Legacy filter — kept for the no-file flow + filter-counts. The
      // group-first display uses decisionsByGroup() instead, which only
      // covers items that have decisions (no-file items by definition
      // don't appear under any group).
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter === 'all') return r.items;
      if (this.scanFilter === 'nofile') return r.items.filter(i => !i.decisions || i.decisions.length === 0);
      return r.items.filter(item => (item.decisions || []).some(d => d.action === this.scanFilter));
    },

    // No-file items shown only when filter is "nofile" — they don't fit
    // any release-group bucket since they have no decisions.
    noFileScanItems() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter !== 'nofile') return [];
      return r.items.filter(i => !i.decisions || i.decisions.length === 0);
    },

    // Missing-in-secondary items (sync mode only). One row per primary
    // movie that has no matching TmdbID in secondary — `secondaryAction`
    // is identical across all groups for the same item (the movie is
    // either present in secondary or not), so we dedupe by movie ID.
    missingScanItems() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      if (this.scanFilter !== 'missing') return [];
      const seen = new Set();
      const out = [];
      for (const item of r.items) {
        if (!item.decisions) continue;
        const isMissing = item.decisions.some(d => d.secondaryAction === 'missing');
        if (isMissing && !seen.has(item.id)) {
          seen.add(item.id);
          out.push(item);
        }
      }
      return out;
    },

    // Counts per action across the tag result. Respects scanInstanceFilter:
    //   'primary'   → only primary action counted
    //   'secondary' → only secondary action counted (secondary has no nofile concept)
    //   'both'      → counts EITHER side that matches (so a movie matching primary
    //                 'add' AND secondary 'add' is counted twice — that's
    //                 intentional, both are real actions to apply).
    // Also respects orphan-pass-only contributions to secondary remove counts.
    // Switches the visible-instance filter and re-picks the status chip
    // if the new filter would orphan it (e.g. scanFilter='missing' is
    // a secondary-only concept, scanFilter='nofile' is a primary-only
    // concept — when scanInstanceFilter flips, the chip can become
    // incompatible). Re-pick keeps the user on a valid chip without
    // forcing them to click again.
    setScanInstanceFilter(next) {
      this.scanInstanceFilter = next;
      const f = this.scanFilter;
      // Hidden-chip cases per index.html @x-show conditions:
      //   No file: hidden when filter=secondary
      //   Missing: hidden when filter=primary
      if ((f === 'nofile' && next === 'secondary') || (f === 'missing' && next === 'primary')) {
        this.scanFilter = this.pickDefaultScanFilter();
      }
    },

    // Pick the most useful default filter chip based on the response.
    // Action buckets (add/remove) win when they have anything, since the
    // user typically came to act on changes. When all action buckets are
    // empty (common: QFA against already-synced library, returned 0/0),
    // fall back to whichever non-action bucket has the most items so the
    // user lands on data instead of an empty filter. Honors the active
    // scanInstanceFilter so the counts reflect what's currently visible.
    pickDefaultScanFilter() {
      const c = this.scanFilterCounts();
      if (c.add > 0) return 'add';
      if (c.remove > 0) return 'remove';
      // No actionable change. Pick the largest of keep / nofile / missing.
      const r = this.scanResults.tag;
      const missing = (r && r.totals && r.totals.secondaryMissing) || 0;
      const candidates = [
        { name: 'keep',    count: c.keep },
        { name: 'nofile',  count: c.nofile },
        { name: 'missing', count: missing },
      ];
      candidates.sort((a, b) => b.count - a.count);
      return candidates[0].count > 0 ? candidates[0].name : 'add';
    },

    scanFilterCounts() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return { add: 0, remove: 0, keep: 0, nofile: 0 };
      const fil = this.scanInstanceFilter;
      let add = 0, remove = 0, keep = 0, nofile = 0;
      for (const item of r.items) {
        if (!item.decisions || item.decisions.length === 0) {
          if (fil !== 'secondary') nofile++;
          continue;
        }
        for (const d of item.decisions) {
          if (fil === 'primary' || fil === 'both') {
            if (d.action === 'add') add++;
            else if (d.action === 'remove') remove++;
            else if (d.action === 'keep') keep++;
          }
          if (fil === 'secondary' || fil === 'both') {
            if (d.secondaryAction === 'add') add++;
            else if (d.secondaryAction === 'remove') remove++;
            else if (d.secondaryAction === 'keep') keep++;
          }
        }
      }
      return { add, remove, keep, nofile };
    },

    // Reorganize per-movie items into per-group buckets. Each group row
    // collapses N decisions across the library into one expandable entry,
    // matching Discover's UI pattern. Filtered to actions matching the
    // current scanFilter (or all actions when scanFilter is "all").
    //
    // Each bucket carries:
    //   group: { id, tag, display }
    //   items: [{ ...item fields, action, matched, matchLocation,
    //             quality, qualityDetail, audio, audioDetail, reason }]
    //   totals: { add, remove, keep, skip }  (full per-group totals,
    //                                          unfiltered — for the chip)
    //
    // Sort: groups by tag-label alphabetical. Within a group, items by
    // movie title.
    decisionsByGroup() {
      const r = this.scanResults.tag;
      if (!r || !r.items) return [];
      const filter = this.scanFilter;
      const instFil = this.scanInstanceFilter;
      // No-file rows handled in noFileScanItems(), missing handled separately.
      if (filter === 'nofile' || filter === 'missing') return [];
      const buckets = new Map();
      for (const item of r.items) {
        if (!item.decisions || item.decisions.length === 0) continue;
        for (const d of item.decisions) {
          let b = buckets.get(d.groupId);
          if (!b) {
            b = {
              group: { id: d.groupId, tag: d.groupTag, display: d.groupDisplay },
              items: [],
              totals: { add: 0, remove: 0, keep: 0, skip: 0 },
            };
            buckets.set(d.groupId, b);
          }
          b.totals[d.action] = (b.totals[d.action] || 0) + 1;
          // Decide whether this (item, decision) matches the chosen filter,
          // taking instance-filter into account. For 'both', either side
          // matching qualifies the row for inclusion.
          let matchesFilter = false;
          if (instFil === 'primary' || instFil === 'both') {
            if (d.action === filter) matchesFilter = true;
          }
          if (!matchesFilter && (instFil === 'secondary' || instFil === 'both')) {
            if (d.secondaryAction === filter) matchesFilter = true;
          }
          if (matchesFilter) {
            b.items.push({
              ...item,
              action: d.action,
              matched: d.matched,
              matchLocation: d.matchLocation,
              quality: d.quality,
              qualityDetail: d.qualityDetail,
              audio: d.audio,
              audioDetail: d.audioDetail,
              reason: d.reason,
              secondaryAction: d.secondaryAction,
              secondaryHasTag: d.secondaryHasTag,
            });
          }
        }
      }
      // Drop groups with no items after the filter — keeps the screen
      // tight when the user picks "Add" and most groups have only Keeps.
      const out = [];
      for (const b of buckets.values()) {
        if (b.items.length === 0) continue;
        b.items.sort((a, c) => a.title.localeCompare(c.title, undefined, { sensitivity: 'base' }));
        out.push(b);
      }
      out.sort((a, b) => a.group.tag.localeCompare(b.group.tag, undefined, { sensitivity: 'base' }));
      return out;
    },

    toggleScanRowExpanded(itemId) {
      const next = { ...this.scanRowExpanded };
      if (next[itemId]) delete next[itemId];
      else next[itemId] = true;
      this.scanRowExpanded = next;
    },

    toggleScanGroupExpanded(groupId) {
      const next = { ...this.scanGroupExpanded };
      if (next[groupId]) delete next[groupId];
      else next[groupId] = true;
      this.scanGroupExpanded = next;
    },

    iconUrl(type, variant) {
      if (type === 'radarr') return variant === '4k' ? '/icons/radarr4kNew.png' : '/icons/radarrNew.png';
      return variant === '4k' ? '/icons/sonarr4k.png' : '/icons/sonarr.png';
    },

    async refreshAllStatus() {
      for (const inst of this.instances) {
        // Don't overwrite a 'testing' that the user just triggered
        if (this.instStatus[inst.id] === 'testing') continue;
        this.testInstance(inst, true);
      }
    },

    async loadConfig() {
      try {
        const r = await this.apiFetch('/api/config');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const cfg = await r.json();
        this.instances = cfg.instances || [];
        // Reconcile picker state — if the previously-chosen cross-instance
        // compare target was deleted (or had its type changed) since the
        // last config load, clear it so the user doesn't see stale UI or
        // hit "Instance not found" on the next Compare click. Same pattern
        // already applied to tagsInstanceId in init() at app.js:627.
        if (this.compareCrossInstanceTarget) {
          const t = this.currentInstanceType();
          const stillThere = this.instances.find(
            i => i.id === this.compareCrossInstanceTarget && i.type === t && i.id !== this.tagsInstanceId
          );
          if (!stillThere) this.compareCrossInstanceTarget = '';
        }
        this.discord = cfg.discord || { enabled: false, webhookUrl: '' };
        this.uiScale = (cfg.display && cfg.display.uiScale) || '1.1';
        this.timeFormat = (cfg.display && cfg.display.timeFormat) || 'auto';
        this.groups = cfg.releaseGroups || [];
        // FilterSet has per-Arr-type structure (cfg.filters.radarr +
        // cfg.filters.sonarr). The Filters sub-tab only renders one set
        // today — UI-side mirroring of per-type filters lands with the
        // M-Sonarr per-episode walker. Until then we read/write the
        // Radarr block, which matches the active scan type for every
        // shipped feature (Radarr-only handlers).
        const f = (cfg.filters && cfg.filters.radarr) || {};
        this.filters = {
          quality: f.Quality !== false,
          maWebDL: f.MAWebDL !== false,
          playWebDL: f.PlayWebDL !== false,
          audio: f.Audio !== false,
          trueHD: f.TrueHD !== false,
          trueHDAtmos: f.TrueHDAtmos !== false,
          dtsX: f.DTSX !== false,
          dtsHDMA: f.DTSHDMA !== false,
        };
        // Snapshot the Sonarr block so we can round-trip it through
        // saveFilters without the user's checkbox toggles wiping the
        // Sonarr-side defaults that Load() backfilled.
        this._savedSonarrFilters = (cfg.filters && cfg.filters.sonarr) || null;
        // Audio + Video tags load from their own endpoints (each ships
        // {config, ...vocab} together so the UI can render the closed-
        // vocab checkbox matrix without hardcoding values).
        await this.loadAudioTags();
        await this.loadVideoTags();
        await this.loadDvDetail();
        // Best-effort tools status — ignore errors so a 503 (DV not wired
        // in dev mode) doesn't trip the toast banner.
        try { await this.loadDvToolsStatus(); } catch (_) {}
        try { await this.loadDvCacheStats(); } catch (_) {}
      } catch (e) {
        this.showToast('Load failed: ' + e.message, 'error');
      }
    },

    // --- DV detail (M4b) ---
    async loadDvDetail() {
      try {
        // GET — CSRF doesn't gate it but use apiFetch for consistency.
        const r = await this.apiFetch('/api/dv-detail');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config) {
          this.dvDetail.enabled = !!data.config.enabled;
          this.dvDetail.prefix = data.config.prefix || '';
          this.dvDetail.allowedValues = Array.isArray(data.config.allowedValues) ? data.config.allowedValues : [];
          this.dvDetail.selectMode = data.config.selectMode || '';
          this.dvDetail.labels = (data.config.labels && typeof data.config.labels === 'object') ? { ...data.config.labels } : {};
          this.dvDetail.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.vocabulary)) {
          this.dvDetailVocab = data.vocabulary;
        }
      } catch (e) {
        // Silent — config-load is best-effort. saveDvDetail will surface
        // network errors if the user actually tries to write.
      }
    },

    async saveDvDetail() {
      // Same pattern as saveExtraTags: PUT the full object (server treats
      // it as wholesale replacement). Validation errors come back as 400
      // with a clear message — surface as a toast.
      // apiFetch (not raw fetch) — CSRF middleware rejects PUT without
      // the X-CSRF-Token header, same trap that bit the DV-tools handlers.
      try {
        const r = await this.apiFetch('/api/dv-detail', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            enabled: !!this.dvDetail.enabled,
            prefix: this.dvDetail.prefix || '',
            allowedValues: this.dvDetail.allowedValues,
            selectMode: this.dvDetail.selectMode || '',
            labels: this.dvDetail.labels || {},
            removeOrphanedTags: !!this.dvDetail.removeOrphanedTags,
          }),
        });
        if (!r.ok) {
          const err = await r.json().catch(() => ({}));
          this.showToast('DV detail save failed: ' + (err.error || r.status), 'error');
          return;
        }
      } catch (e) {
        this.showToast('DV detail save failed: ' + e.message, 'error');
      }
    },

    // toggleDvDetailValue uses the same two-mode semantics as the audio
    // and video bucket toggles — see audioTagValueChecked for the full
    // SelectMode rationale. Default mode treats empty=all; first per-
    // value click switches to "select" mode using the full vocab as
    // baseline so the unchecked one disappears as expected.
    toggleDvDetailValue(value) {
      const fullVocab = [...this.dvDetailVocab];
      if (this.dvDetail.selectMode !== 'select') {
        if (!this.dvDetail.allowedValues || this.dvDetail.allowedValues.length === 0) {
          this.dvDetail.allowedValues = [...fullVocab];
        }
        this.dvDetail.selectMode = 'select';
      }
      const av = this.dvDetail.allowedValues || [];
      const idx = av.indexOf(value);
      if (idx >= 0) av.splice(idx, 1);
      else          av.push(value);
      const order = (v) => this.dvDetailVocab.indexOf(v);
      av.sort((a, b) => order(a) - order(b));
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        this.dvDetail.selectMode = '';
        this.dvDetail.allowedValues = [];
      } else {
        this.dvDetail.allowedValues = av;
      }
      this.saveDvDetail();
    },
    selectAllDvDetailValues() {
      this.dvDetail.selectMode = '';
      this.dvDetail.allowedValues = [];
      this.saveDvDetail();
    },
    selectNoneDvDetailValues() {
      this.dvDetail.selectMode = 'select';
      this.dvDetail.allowedValues = [];
      this.saveDvDetail();
    },

    isDvDetailValueAllowed(value) {
      const av = this.dvDetail.allowedValues || [];
      if (this.dvDetail.selectMode !== 'select' && av.length === 0) return true;
      return av.includes(value);
    },

    async loadDvToolsStatus() {
      try {
        // GET — CSRF doesn't gate it but use apiFetch for consistency
        // with the rest of the codebase + any future auth wiring.
        const r = await this.apiFetch('/api/tools/dv/status');
        if (r.status === 503) {
          // AttachDV not called server-side — dev/test build, not a
          // production deployment. Banner stays in default
          // {installed:false} state which shows the [Install] CTA;
          // the user clicking Install will get a 503 from the install
          // endpoint with a clear "DV tools not configured" message.
          // Console.warn so a dev running locally sees the wiring gap.
          // eslint-disable-next-line no-console
          console.warn('DV tools status endpoint returned 503 — AttachDV likely not wired in main; install button will fail with 503 too.');
          return;
        }
        if (!r.ok) return;
        this.dvTools = await r.json();
      } catch (e) {
        // Silent on poll failure; banner just stays as-is.
      }
    },

    // installDvTools / uninstallDvTools removed — DV tools (ffmpeg +
    // dovi_tool) ship baked into the image as of v0.3.5 via the
    // Dockerfile dv-tools stage. No env var, no install step.
    // loadDvToolsStatus stays as a defensive health check for the
    // "Tools unreachable" UI branch (only fires if the image build
    // is somehow broken — should never happen in normal CI).

    // --- DV cache panel (Library scan → DV detail tab) ---
    // GET /api/dv-cache/stats. Server returns zero-valued struct when
    // DvCache is nil (defensive — shouldn't happen in normal deploys
    // but covered). UI renders "No files cached yet" copy in that case.
    async loadDvCacheStats() {
      try {
        const r = await this.apiFetch('/api/dv-cache/stats');
        if (!r.ok) return;
        this.dvCacheStats = await r.json();
      } catch (_) {
        // Silent — panel just shows the empty-state copy.
      }
    },
    // Open the confirm modal. Stats are already loaded; if not, we
    // still open the modal so the user can see "0 cached files" and
    // the Clear button is naturally disabled.
    openClearDvCacheConfirm() {
      this.showClearDvCacheConfirm = true;
    },
    // Fire the DELETE. Server wipes in-memory + persists empty file
    // and returns the post-clear stats so we don't need a follow-up
    // GET. Toast on success/failure.
    async confirmClearDvCache() {
      if (this.clearingDvCache) return;
      this.clearingDvCache = true;
      try {
        const r = await this.apiFetch('/api/dv-cache', { method: 'DELETE' });
        if (!r.ok) {
          let msg = 'HTTP ' + r.status;
          try { msg = (await r.json()).error || msg; } catch (_) {}
          throw new Error(msg);
        }
        this.dvCacheStats = await r.json();
        this.showToast('DV cache cleared — next scan will re-extract from scratch', 'success');
        this.showClearDvCacheConfirm = false;
      } catch (e) {
        this.showToast('Clear DV cache failed: ' + e.message, 'error');
      } finally {
        this.clearingDvCache = false;
      }
    },

    async runDvDetailScan(mode, bypassOverride, instanceIdOverride = '') {
      // instanceIdOverride: same target=both fan-out as runAudioTagsScan.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) return;
      // Defensive re-entry guard — same pattern as runQuickFixChain.
      // The Run-button gates on scanLoading at the UI layer; this
      // is the function-level safety net.
      if (this.scanLoading) return;
      // bypassOverride lets confirmDvDetailApply pass the snapshot it
      // captured before resetting dvBypassCache. Falsy → fall back to
      // the live state (Preview path).
      const bypassDvCache = bypassOverride !== undefined ? !!bypassOverride : !!this.dvBypassCache;
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('dv');
        this.scanResults.dvDetail = null;
        this.scanError = '';
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'dvdetail') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      // Reset progress + start polling. The scan is slow enough
      // (~1-3s per file × N files) that the user needs both a
      // current-file label and a way to cancel. Poll runs every 1.2s
      // — frequent enough to feel live, slow enough that polling
      // never bottlenecks anything.
      this.dvScanProgress = { running: true, total: 0, processed: 0, currentTitle: '' };
      this.startDvScanPoll();
      try {
        // runAction builds + POSTs uniformly with the run's overlay, not
        // global. On failure it throws; the catch below sets scanError
        // (which confirmDvDetailApply's target=both fan-out reads to
        // short-circuit) + surfaces the toast.
        this.scanResults.dvDetail = await this.runAction({
          instanceId: targetInstanceId, action: 'dvdetail', mode,
          overlay: this.buildSnapshotOverlay(this.qfaDetailDv),
          options: { bypassDvCache },
        });
        const totals = (this.scanResults.dvDetail && this.scanResults.dvDetail.totals) || {};
        const summary = (totals.dvCandidates || 0) + ' candidates · ' +
                        (totals.toAdd || 0) + ' add · ' +
                        (totals.toRemove || 0) + ' remove · ' +
                        (totals.toKeep || 0) + ' keep';
        this.showToast((mode === 'apply' ? 'DV detail applied — ' : 'DV detail preview ready — ') + summary, 'success');
        this.viewPhaseDetails({ phase: 'dvdetail', response: this.scanResults.dvDetail });
      } catch (e) {
        this.scanError = e.message || 'DV detail scan failed';
        this.showToast('DV detail scan failed: ' + e.message, 'error');
      } finally {
        this.scanLoading = false;
        this.stopDvScanPoll();
        this.dvScanProgress = null;
        // Refresh the History tab's scan-list if the user is sitting
        // on it so the just-dumped scan appears without manual reload.
        // No-op when they're elsewhere; the loadScanHistory tick on
        // History-tab landing will pick it up.
        if (this.scanSection === 'history') this.loadScanHistory();
        // Refresh the cache panel — a cache-active scan populates new
        // entries; a bypass scan changes nothing on disk but loadDvCacheStats
        // is cheap enough to run regardless.
        try { await this.loadDvCacheStats(); } catch (_) {}
      }
    },



  };
}
