// resolvarr UI — webhook-subsystem (extracted from app.js, Stage 4 split).
// Composed via { ...appWebhookSubsystem() } in app(); methods bind `this` to the Alpine component.
function appWebhookSubsystem() {
  return {
    // ===== Webhook subsystem (M-Webhook foundation, logging-only today) =====

    // Loads the per-instance webhook configs for every Arr instance.
    // Called when the Webhooks page mounts + after wizard completion
    // / token rotation. Failures are logged but don't block the UI —
    // an instance whose GET fails just shows "not configured" and
    // works the next time the page reloads.
    //
    // Fetches in parallel via Promise.all — serialised awaits stack
    // latency on slow networks (5 instances × 50ms = 250ms wall).
    async loadWebhookSetupPage() {
      const insts = this.instances || [];
      // Defensive: if the user picked an instance for the activity tab
      // and that instance was deleted under Settings → Instances, the
      // dropdown selected-id stays dangling. Reset before re-render.
      if (this.webhookActivityInstanceId &&
          !insts.find(i => i.id === this.webhookActivityInstanceId)) {
        this.webhookActivityInstanceId = '';
      }
      const results = await Promise.all(insts.map(async inst => {
        try {
          const r = await this.apiFetch('/api/instances/' + inst.id + '/webhook');
          if (r.ok) {
            const d = await r.json();
            return [inst.id, d];
          }
        } catch (e) {
          // Tolerate per-instance failure — leave the entry unset
          // and let the UI render "not configured" for that one.
        }
        return null;
      }));
      const next = {};
      for (const r of results) {
        if (r) next[r[0]] = r[1];
      }
      // Whole-object replacement so Alpine v3's reactivity proxy
      // sees the change on EVERY key — nested-key writes don't
      // always trigger re-render when the key wasn't tracked yet
      // (the same trap that bit the Extra-tags toggle on M4).
      this.webhookConfigs = next;
      // Prefetch event lists for every CONFIGURED instance so the
      // Setup-tab "Last received" label reflects reality. Only the
      // ones with a token are worth fetching (untokenized rows can't
      // have received anything). Runs in parallel — slowest one
      // doesn't block the others.
      for (const inst of insts) {
        const cfg = next[inst.id];
        if (cfg && cfg.token) this.prefetchWebhookEventsForLabel(inst.id);
      }
      // Auto-pick the first instance OF THE ACTIVE app-type for the
      // Recent activity sub-tab if the user hasn't picked one yet —
      // saves a click on first visit. Honours the webhookAppType pill
      // so a user on the Sonarr pill auto-lands on Sonarr-1 even when
      // Radarr-1 is first in the global instance list.
      if (!this.webhookActivityInstanceId && insts.length > 0) {
        const t = this.webhookAppType || 'radarr';
        const first = insts.find(i => i.type === t) || insts[0];
        this.webhookActivityInstanceId = first.id;
        this.loadWebhookEvents(first.id);
      }
    },

    // Fetches recent events for one instance and caches them. Writes
    // to webhookEvents via spread-assign so Alpine reliably re-renders
    // when adding a new instanceId key (nested-key writes don't always
    // trigger updates in Alpine v3 — same trap class as Extra-tags).
    //
    // Also (re)starts the SSE stream for the picked instance so future
    // events arrive in real-time without further GET calls. Stream
    // disconnect happens when the user picks another instance, leaves
    // the Webhooks page, or closes the tab.
    async loadWebhookEvents(instanceId) {
      if (!instanceId) {
        this.stopWebhookEventStream();
        return;
      }
      this.webhookEventsLoading = true;
      try {
        let events = [];
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events');
        if (r.ok) {
          const d = await r.json();
          events = Array.isArray(d) ? d : [];
        }
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: events };
      } catch (e) {
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: [] };
      } finally {
        this.webhookEventsLoading = false;
      }
      this.startWebhookEventStream(instanceId);
    },

    // Lightweight events-only fetch (no SSE, no loading flag) used by
    // the Setup tab to populate webhookLastReceivedLabel for every
    // configured instance. Without this the label says "Never received"
    // for any instance the user hasn't opened in the Activity dropdown
    // yet, even when the server-side ring buffer has events. Failures
    // are silent — the label just stays at "Never" until the next
    // refresh.
    async prefetchWebhookEventsForLabel(instanceId) {
      if (!instanceId) return;
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events');
        if (!r.ok) return;
        const d = await r.json();
        const events = Array.isArray(d) ? d : [];
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: events };
      } catch (e) {
        // silent — label falls back to "Never received"
      }
    },

    // SSE-driven live updates for the Recent activity panel.
    // Replaces a polling alternative — the receiver knows when an
    // event arrives, so it pushes; browser doesn't have to ask.
    //
    // Owner of `_webhookEventSource` is this Alpine component. Only
    // one stream is alive at a time; switching instances closes the
    // old one. Browser's EventSource auto-reconnects on transient
    // network drop; we don't add a manual reconnect loop.
    startWebhookEventStream(instanceId) {
      this.stopWebhookEventStream();
      if (!instanceId) return;
      if (typeof EventSource === 'undefined') {
        // Old browser — no SSE support. Recent activity still
        // works via the GET endpoint + the ↻ button. No-op here.
        return;
      }
      try {
        const url = '/api/instances/' + instanceId + '/webhook/events/stream';
        const es = new EventSource(url);
        es.addEventListener('webhook', (msgEvent) => {
          this._handleWebhookSseEvent(instanceId, msgEvent.data);
        });
        // No reconnect spam — EventSource handles transient drops
        // itself. We only log the FIRST onerror so dev consoles
        // aren't full of "EventSource failed" reconnect noise.
        es.onerror = () => {
          // Browser will auto-reconnect; no action needed.
        };
        this._webhookEventSource = es;
        this._webhookEventSourceInstanceId = instanceId;
      } catch (e) {
        // Mostly defensive — `new EventSource` only throws on
        // a malformed URL, which we control. Swallow + leave the
        // panel in poll-via-↻ mode.
      }
      // Start the 10-second polling safety-net regardless of SSE
      // outcome — proxies sometimes silently buffer SSE responses, or
      // EventSource fails to reconnect after long idle. Polling makes
      // sure the panel reflects new events even when push is broken.
      this.startWebhookEventPolling(instanceId);
    },

    stopWebhookEventStream() {
      if (this._webhookEventSource) {
        try { this._webhookEventSource.close(); } catch (e) { /* ignore */ }
      }
      this._webhookEventSource = null;
      this._webhookEventSourceInstanceId = null;
      // Stop polling fallback too — paired lifecycle with the SSE stream.
      if (this._webhookEventPollHandle) {
        clearInterval(this._webhookEventPollHandle);
        this._webhookEventPollHandle = null;
      }
    },

    // startWebhookEventPolling is a safety-net fallback that runs
    // alongside the SSE stream. SSE is the primary push channel, but
    // it's fragile in real deployments — reverse proxies (SWAG /
    // nginx) sometimes buffer event-stream responses, or drop the
    // connection silently after the heartbeat window. Without a poll,
    // the user has to click ↻ to see new events.
    //
    // 10-second poll is cheap (the GET endpoint returns a JSON list
    // capped at 100 entries) and small enough that it doesn't fight
    // SSE — when SSE works, the poll just re-fetches the same list
    // we already have. The Activity panel re-renders against the new
    // list (Alpine spread-assign on webhookEvents).
    //
    // Stops cleanly via stopWebhookEventStream — paired lifecycle.
    startWebhookEventPolling(instanceId) {
      if (this._webhookEventPollHandle) {
        clearInterval(this._webhookEventPollHandle);
      }
      if (!instanceId) return;
      this._webhookEventPollHandle = setInterval(async () => {
        // Stale-tab guard: if the user navigated away the instance
        // dropdown will have changed; skip in that case.
        if (instanceId !== this._webhookEventSourceInstanceId) return;
        try {
          const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events', {
            headers: { 'X-Skip-Login-Redirect': '1' },
          });
          if (r.ok) {
            const d = await r.json();
            if (Array.isArray(d)) {
              this.webhookEvents = { ...this.webhookEvents, [instanceId]: d };
            }
          }
        } catch (e) {
          // Network blip — next tick will retry. No user-facing error.
        }
        // Piggyback: refresh the rules list too. Each rule's History
        // field carries its per-fire summaries — without this call,
        // the per-rule History modal stays stale until the user
        // manually clicks Reload. Cheap GET (small JSON, capped at
        // a handful of rules per instance).
        try {
          await this.loadWebhookRules();
        } catch (e) {
          // loadWebhookRules already swallows errors silently.
        }
      }, 10000); // 10s
    },

    // Handler for one SSE 'webhook' event payload. Prepends the new
    // event to the in-memory list (newest-first). Trims to the
    // server-side cap (100) so the display matches the persisted
    // ring on disk. Spread-assign on webhookEvents to dodge the
    // Alpine v3 nested-key reactivity trap that caught us before.
    _handleWebhookSseEvent(instanceId, payload) {
      // Stale-stream guard: if the user already switched instances
      // between event-arrival and this handler firing, drop the
      // event. The new stream will deliver future events.
      if (instanceId !== this._webhookEventSourceInstanceId) return;
      let ev;
      try { ev = JSON.parse(payload); }
      catch (e) { return; }
      if (!ev || typeof ev !== 'object') return;
      // Defence-in-depth: server stream is per-instance, so
      // ev.instanceId should always equal instanceId. Drop
      // mismatches rather than silently land an event on the
      // wrong instance's panel.
      if (ev.instanceId && ev.instanceId !== instanceId) return;
      const cur = this.webhookEvents[instanceId] || [];
      const next = [ev].concat(cur).slice(0, 100);
      this.webhookEvents = { ...this.webhookEvents, [instanceId]: next };
    },

    // "Last: 2 min ago" / "Never received" status pill text. Pulls
    // from the per-instance event cache, falls back to "Never" when
    // the cache is empty (server-side ring would be empty too in
    // that case).
    webhookLastReceivedLabel(instanceId) {
      const events = this.webhookEvents[instanceId];
      if (!events || events.length === 0) return 'Never received an event';
      const newest = events[0]; // events are newest-first
      if (!newest || !newest.receivedAt) return '';
      try {
        const d = new Date(newest.receivedAt);
        const ageMs = Date.now() - d.getTime();
        if (ageMs < 60 * 1000) return 'Last: just now';
        if (ageMs < 60 * 60 * 1000) return 'Last: ' + Math.floor(ageMs / 60000) + ' min ago';
        if (ageMs < 24 * 60 * 60 * 1000) return 'Last: ' + Math.floor(ageMs / 3600000) + ' h ago';
        return 'Last: ' + Math.floor(ageMs / 86400000) + ' d ago';
      } catch (e) {
        return '';
      }
    },

    // Copy text to clipboard with a legacy fallback. The modern
    // navigator.clipboard API only works in secure contexts (HTTPS
    // or localhost) — most users run resolvarr on a LAN HTTP URL,
    // which fails the SecureContext check. The textarea +
    // execCommand('copy') path still works there. Returns true on
    // success.
    async copyToClipboard(text) {
      if (!text) return false;
      if (navigator.clipboard && window.isSecureContext) {
        try {
          await navigator.clipboard.writeText(text);
          return true;
        } catch (e) {
          // fall through to legacy
        }
      }
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      // Off-screen but in the layout so focus + select work in every
      // browser. position:fixed avoids triggering page scroll on
      // .focus() in iOS Safari.
      ta.style.position = 'fixed';
      ta.style.top = '0';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      let ok = false;
      try {
        ok = document.execCommand('copy');
      } catch (e) {
        ok = false;
      }
      document.body.removeChild(ta);
      return ok;
    },

    async copyWebhookUrl(instanceId) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.url) return;
      const ok = await this.copyToClipboard(cfg.url);
      this.showToast(
        ok ? 'Webhook URL copied' : 'Copy failed — your browser blocked clipboard access',
        ok ? 'success' : 'error',
      );
    },

    // Confirm + execute token rotation. New URL means the user has
    // to update Sonarr/Radarr's Connect entry; warn explicitly so
    // they don't lose the previous URL accidentally.
    async confirmRegenerateWebhookToken(instanceId, name) {
      const cfg = this.webhookConfigs[instanceId];
      const wasConfigured = !!(cfg && cfg.token);
      const arrName = (this.instances.find(i => i.id === instanceId) || {}).type === 'sonarr' ? 'Sonarr' : 'Radarr';
      // Rotation also rotates the Secret (Phase 2 Slice A) — surface
      // that in the confirm message so users with Require-signature on
      // know they'll need to re-paste the new Secret into Arr's
      // Connect password field too.
      const msg = wasConfigured
        ? 'The current URL will stop working immediately. You\'ll need to paste BOTH the new URL and the new Secret into ' + arrName + ' → Settings → Connect → your Webhook (Secret goes in the password field).'
        : 'A unique webhook URL + Secret will be generated for ' + name + '.';
      const title = wasConfigured
        ? 'Generate a new webhook URL AND new Secret for "' + name + '"?'
        : 'Generate webhook URL for "' + name + '"?';
      if (!await this.confirmDialog({
        title:       title,
        message:     msg,
        confirmText: wasConfigured ? 'Rotate' : 'Generate',
        kind:        wasConfigured ? 'warning' : 'default',
      })) return;
      this.regenerateWebhookToken(instanceId);
    },

    async regenerateWebhookToken(instanceId) {
      try {
        // No body — preserve LoggingEnabled. Backend reads it as
        // *bool and now distinguishes "field omitted" (preserve)
        // from "field present and false" (explicit disable). Old
        // behaviour silently flipped logging off on rotate.
        //
        // Backend also preserves RequireSignature across rotation,
        // so users in strict mode stay in strict mode — but their
        // Sonarr/Radarr config still has the OLD Secret in the
        // password field, so events will start 401-ing until they
        // re-paste. Toast surfaces this; the Setup-tab card's
        // Require-signature toggle is still visible for an emergency
        // downgrade to grace mode if the user wants events flowing
        // while they go update Arr's config.
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/rotate', {
          method: 'POST',
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: d };
        if (d.requireSignature) {
          this.showToast(
            'New URL + Secret generated. Require-signature is ON — re-paste the new Secret into your Webhook password field in Sonarr/Radarr or events will be rejected.',
            'success',
          );
        } else {
          this.showToast('New webhook URL + Secret generated', 'success');
        }
      } catch (e) {
        this.showToast('Rotate failed: ' + e.message, 'error');
      }
    },

    // Confirm + delete a configured webhook. Clears the token so the
    // receiver path returns 404 for the previous URL, reverts the
    // row to "not configured" + drops the persisted Connect entry's
    // ability to reach us. Recent activity events are NOT wiped —
    // they're keyed by instance ID, not token, and the user has a
    // separate Clear log button if they want both.
    async confirmDeleteWebhook(instanceId, name) {
      const arr = (this.instances.find(i => i.id === instanceId) || {}).type === 'sonarr' ? 'Sonarr' : 'Radarr';
      const msg = 'The current URL will stop working immediately. To reconfigure later you\'ll need to:\n' +
        '  1. Generate a new webhook here.\n' +
        '  2. Update the URL in ' + arr + ' → Settings → Connect, or remove the Connect entry there.';
      if (!await this.confirmDialog({
        title:       'Remove the webhook for "' + name + '"?',
        message:     msg,
        confirmText: 'Remove',
        kind:        'danger',
      })) return;
      this.deleteWebhook(instanceId);
    },

    async deleteWebhook(instanceId) {
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook', {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        // Reflect the cleared state locally — empty the entry rather
        // than dropping the key, so x-text on the row sees a defined
        // (but empty) config and renders the "not configured" branch.
        this.webhookConfigs = {
          ...this.webhookConfigs,
          [instanceId]: { token: '', url: '', loggingEnabled: false },
        };
        this.showToast('Webhook removed', 'success');
      } catch (e) {
        this.showToast('Delete failed: ' + e.message, 'error');
      }
    },

    // Toggle the LoggingEnabled flag for an existing webhook config.
    // Optimistic update — the checkbox flips immediately, server
    // confirms in the background. On failure we revert. Writes use
    // spread-assign on the parent object so Alpine reliably re-renders.
    async toggleWebhookLogging(instanceId, enabled) {
      const cfg = this.webhookConfigs[instanceId];
      if (!cfg || !cfg.token) return;
      const prev = cfg.loggingEnabled;
      this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, loggingEnabled: enabled } };
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/logging', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ loggingEnabled: enabled }),
        });
        if (!r.ok) {
          const d = await r.json();
          throw new Error(d.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        // Revert
        this.webhookConfigs = { ...this.webhookConfigs, [instanceId]: { ...cfg, loggingEnabled: prev } };
        this.showToast('Logging toggle failed: ' + e.message, 'error');
      }
    },

    // Confirm + clear the per-instance log. Idempotent on the
    // server but the confirm step matches the rest of the
    // destructive-action UX (delete tag, delete instance, etc.).
    async confirmClearWebhookEvents(instanceId) {
      if (!instanceId) return;
      const events = this.webhookEvents[instanceId] || [];
      if (events.length === 0) return;
      const inst = this.instances.find(i => i.id === instanceId);
      const name = inst ? inst.name : 'this instance';
      if (!await this.confirmDialog({
        title:       'Clear logged events?',
        message:     'Clear ' + events.length + ' logged event(s) for "' + name + '". The events are also removed from the on-disk log file.',
        confirmText: 'Clear',
        kind:        'danger',
      })) return;
      this.clearWebhookEventsApi(instanceId);
    },

    async clearWebhookEventsApi(instanceId) {
      try {
        const r = await this.apiFetch('/api/instances/' + instanceId + '/webhook/events', {
          method: 'DELETE',
        });
        if (!r.ok) {
          const d = await r.json();
          throw new Error(d.error || 'HTTP ' + r.status);
        }
        this.webhookEvents = { ...this.webhookEvents, [instanceId]: [] };
        this.showToast('Log cleared', 'success');
      } catch (e) {
        this.showToast('Clear failed: ' + e.message, 'error');
      }
    },

    // connectEventLabel maps the raw Connect event-type strings
    // (what the payload calls them) to the labels Sonarr/Radarr show
    // in their own UI — so the user sees the same word here that
    // they ticked in the Arr's Connect settings. Most notable: the
    // payload says "Download" for what the Arr UI calls "Import".
    //
    // Unknown event types pass through verbatim so future Arr
    // additions don't crash the UI. Also handles resolvarr's own
    // custom event types (qbit:torrentAdded).
    connectEventLabel(rawType) {
      if (!rawType) return '(unknown event)';
      switch (rawType) {
        case 'Grab':                return 'Grab';
        case 'Download':            return 'Import';
        case 'Upgrade':             return 'Upgrade';
        case 'Rename':              return 'Rename';
        case 'MovieAdded':          return 'Movie added';
        case 'MovieDelete':         return 'Movie deleted';
        case 'MovieFileDelete':     return 'Movie file deleted';
        case 'SeriesAdd':           return 'Series added';
        case 'SeriesDelete':        return 'Series deleted';
        case 'EpisodeFileDelete':   return 'Episode file deleted';
        case 'HealthIssue':         return 'Health issue';
        case 'HealthRestored':      return 'Health restored';
        case 'ApplicationUpdate':   return 'Application update';
        case 'ManualInteractionRequired': return 'Manual interaction required';
        case 'Test':                return 'Test';
        case 'qbit:torrentAdded':   return 'qBit torrent added';
        default:                    return rawType;
      }
    },

    // Build [label, rawType, count] triples from the per-instance event
    // cache, sorted by count desc. Drives the filter chip row on the
    // Recent activity sub-tab. Label is the user-facing version (so
    // "Download" appears as "Import" in chips); rawType is what the
    // filter compares against to keep the filter logic correct even
    // when labels collide between Arr versions.
    webhookEventTypeCounts(instanceId) {
      const events = this.webhookEvents[instanceId] || [];
      const counts = new Map();
      for (const ev of events) {
        const t = ev.eventType || '(unknown)';
        counts.set(t, (counts.get(t) || 0) + 1);
      }
      return Array.from(counts.entries()).sort((a, b) => b[1] - a[1]);
    },

    webhookEventsFiltered() {
      const events = this.webhookEvents[this.webhookActivityInstanceId] || [];
      let out = events;
      const q = (this.webhookActivitySearch || '').trim().toLowerCase();
      if (q) {
        out = out.filter(ev =>
          ((ev.title || '').toLowerCase().includes(q)) ||
          ((ev.subtitle || '').toLowerCase().includes(q)));
      }
      if (this.webhookEventFilter !== 'all') {
        out = out.filter(ev => ev.eventType === this.webhookEventFilter);
      }
      if (this.webhookOutcomeFilter && this.webhookOutcomeFilter !== 'all') {
        out = out.filter(ev => this.eventOutcomeFilterMatch(ev));
      }
      if ((this.webhookContentShapeFilter || []).length > 0) {
        out = out.filter(ev => this.eventContentShapeFilterMatch(ev));
      }
      return out;
    },

    // Toggle the expand-to-see-JSON state on a single event card.
    // Spread-assign so first-touch keys land cleanly on Alpine v3's
    // proxy (object-key writes are the same trap class that bit
    // webhookEvents / webhookConfigs).
    toggleWebhookEventExpanded(eventId) {
      const cur = !!this.webhookEventExpanded[eventId];
      this.webhookEventExpanded = { ...this.webhookEventExpanded, [eventId]: !cur };
    },

    // Pretty-print the raw event JSON for the expand-to-see view.
    // Falls back to showing the raw string when JSON.parse fails
    // (the receiver stamps unparseable bodies through with
    // eventType="(unparseable)"; pre-pretty-printing them as
    // raw text is the right behaviour).
    formatWebhookRaw(raw) {
      if (!raw) return '';
      // raw is a JSON value (object/array/etc.) decoded by Alpine
      // from the JSON response — stringify it pretty.
      try {
        return JSON.stringify(raw, null, 2);
      } catch (e) {
        return String(raw);
      }
    },

  };
}
