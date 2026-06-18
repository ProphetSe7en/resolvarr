// resolvarr UI — audio + video tags module
//
// Extra-tags (M4): the Audio and Video tagging helpers + per-value allow-list
// state. Composed into the Alpine root via { ...appAudioVideoTags() } in app();
// methods/props use `this` = the Alpine component (bound on spread), so peer
// calls (this.apiFetch, this.showToast, ...) work exactly as before.
function appAudioVideoTags() {
  return {
    // --- Extra tags (M4) ---
    // anyExtraTagsBucketEnabled gates the Run scan button and the Quick
    // fix-all "Apply extra tags" checkbox. A run with no buckets enabled
    // ---- Audio + Video tags (M4 split) ------------------------------
    // Two parallel sets of helpers — the buckets they operate on differ
    // (Audio = single bucket; Video = three buckets) but the per-value
    // allow-list semantics are identical. Each side has its own scan
    // entrypoint so results land on the originating sub-tab.
    //
    // Empty allowedValues = "all allowed" (engine convention). The
    // UI's checkbox flow auto-disables a bucket if the user un-checks
    // every value (correct way to "tag nothing"); re-enabling starts
    // fresh with all-allowed.

    autoTagPrefixRule: /^[a-z0-9-]*$/,
    // bucketLabelRule mirrors Radarr's strict tag-label regex used by
    // the backend's validateLabelsMap. NON-empty + a-z 0-9 hyphen only.
    bucketLabelRule: /^[a-z0-9-]+$/,

    // Bucket label override helpers ------------------------------------
    // Sparse map on the bucket — keyed by canonical engine value
    // (e.g. "dvprofile8", "truehd", "2160p"), valued by user-chosen
    // replacement. Empty/missing entry means "use engine default".
    //
    // The bucket Prefix still applies on top — override replaces the
    // value portion only. To drop the prefix for an override, leave
    // the bucket Prefix empty (separate per-bucket setting).
    bucketLabelValue(bucket, value) {
      if (!bucket || !bucket.labels) return '';
      const v = bucket.labels[value];
      return (typeof v === 'string') ? v : '';
    },
    setBucketLabel(bucket, value, label, save) {
      if (!bucket) return;
      if (!bucket.labels || typeof bucket.labels !== 'object') bucket.labels = {};
      const trimmed = (label || '').trim();
      if (trimmed === '') {
        delete bucket.labels[value];
      } else {
        bucket.labels[value] = trimmed;
      }
      if (typeof save === 'function') save();
    },
    bucketLabelValid(bucket, value) {
      const v = this.bucketLabelValue(bucket, value);
      if (v === '') return true; // empty = use default, valid
      return this.bucketLabelRule.test(v);
    },
    // Returns true when any label in the bucket collides with another
    // (two keys → same override value). Used to flag the customise-
    // labels section header so users see the error before save.
    bucketHasLabelCollision(bucket) {
      if (!bucket || !bucket.labels) return false;
      const seen = {};
      for (const [k, v] of Object.entries(bucket.labels)) {
        if (!v) continue;
        if (seen[v]) return true;
        seen[v] = k;
      }
      return false;
    },
    // Returns the number of currently-configured overrides — drives
    // the "(N customised)" badge on the section header.
    bucketLabelCount(bucket) {
      if (!bucket || !bucket.labels) return 0;
      return Object.values(bucket.labels).filter(v => v && v.trim() !== '').length;
    },
    // ruleEditorLabelError scans every bucket on the currently-edited
    // rule snapshot for invalid override characters or intra-bucket
    // label collisions. Returns the first user-facing error string or
    // empty when everything's clean. Called from the schedule + webhook
    // save handlers so users see "Fix … on the Audio step" inline
    // instead of round-tripping to a backend 400 toast.
    ruleEditorLabelError() {
      const r = this.editingRule;
      if (!r) return '';
      const buckets = [];
      if (r.audioTags && r.audioTags.audio) buckets.push({ b: r.audioTags.audio, vocab: this.audioFullVocab(),       step: 'Audio' });
      if (r.videoTags) {
        if (r.videoTags.resolution) buckets.push({ b: r.videoTags.resolution, vocab: this.videoVocab.resolution, step: 'Video → Resolution' });
        if (r.videoTags.codec)      buckets.push({ b: r.videoTags.codec,      vocab: this.videoVocab.codec,      step: 'Video → Codec' });
        if (r.videoTags.hdr)        buckets.push({ b: r.videoTags.hdr,        vocab: this.videoVocab.hdr,        step: 'Video → HDR' });
      }
      if (r.dvDetail) buckets.push({ b: r.dvDetail, vocab: this.dvDetailVocab || [], step: 'DV detail' });
      for (const { b, vocab, step } of buckets) {
        for (const v of vocab) {
          if (!this.bucketLabelValid(b, v)) {
            return 'Fix invalid label override on the ' + step + ' step (allowed: a-z, 0-9, hyphen)';
          }
        }
        if (this.bucketHasLabelCollision(b)) {
          return 'Two label overrides on the ' + step + ' step map to the same value — pick distinct labels';
        }
      }
      return '';
    },

    // Audio --------------------------------------------------------
    anyAudioTagsBucketEnabled() {
      return !!(this.audioTags && this.audioTags.audio && this.audioTags.audio.enabled);
    },
    audioTagPrefixInvalid() {
      const p = (this.audioTags.audio && this.audioTags.audio.prefix) || '';
      return !this.autoTagPrefixRule.test(p);
    },
    // Two-mode semantics (SelectMode):
    //   "" / "all" (default) → empty allowedValues means "all allowed".
    //                          Tick state: empty list shows everything checked.
    //   "select"             → exact list. Empty means "tag nothing".
    //                          Tick state: explicit per-value checks.
    // Matches engine.BucketConfig.allowed() — see Go side for the truth-source.
    audioTagValueChecked(value) {
      const b = this.audioTags.audio;
      const av = b.allowedValues;
      if (b.selectMode !== 'select' && (!av || av.length === 0)) return true;
      return !!av && av.includes(value);
    },
    toggleAudioTagValue(value, fullVocab) {
      const bucket = this.audioTags.audio;
      // First per-value click in legacy "all-allowed" mode flips the bucket
      // into explicit-select mode using fullVocab as the starting set, then
      // toggles the clicked value off. Subsequent clicks are pure add/remove.
      if (bucket.selectMode !== 'select') {
        if (!bucket.allowedValues || bucket.allowedValues.length === 0) {
          bucket.allowedValues = [...fullVocab];
        }
        bucket.selectMode = 'select';
      }
      let av = bucket.allowedValues || [];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      // If user re-checked back to the full set, normalise to "all" mode so
      // future vocab additions automatically apply.
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        bucket.selectMode = '';
        bucket.allowedValues = [];
      } else {
        bucket.allowedValues = av;
      }
      this.saveAudioTags();
    },
    selectAllAudioValues() {
      const bucket = this.audioTags.audio;
      bucket.selectMode = '';
      bucket.allowedValues = [];
      this.saveAudioTags();
    },
    selectNoneAudioValues() {
      const bucket = this.audioTags.audio;
      bucket.selectMode = 'select';
      bucket.allowedValues = [];
      this.saveAudioTags();
    },
    // Combined audio vocabulary across the three sub-categories — used
    // by toggleAudioTagValue when it needs the full canonical list to
    // expand "empty = all" into an explicit slice.
    audioFullVocab() {
      return [...this.audioVocab.codecs, ...this.audioVocab.channels, ...this.audioVocab.flags];
    },

    async loadAudioTags() {
      try {
        const r = await this.apiFetch('/api/audio-tags');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config && data.config.audio) {
          const src = data.config.audio;
          const dst = this.audioTags.audio;
          dst.enabled = !!src.enabled;
          dst.prefix = src.prefix || '';
          dst.sonarrAggregation = src.sonarrAggregation || 'all-occurring';
          dst.allowedValues = Array.isArray(src.allowedValues) ? src.allowedValues : [];
          dst.selectMode = src.selectMode || '';
          dst.labels = (src.labels && typeof src.labels === 'object') ? { ...src.labels } : {};
          this.audioTags.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.audioCodecs))   this.audioVocab.codecs   = data.audioCodecs;
        if (Array.isArray(data && data.audioChannels)) this.audioVocab.channels = data.audioChannels;
        if (Array.isArray(data && data.audioFlags))    this.audioVocab.flags    = data.audioFlags;
      } catch (_) {
        // Silent — config-load is best-effort.
      }
    },

    // NOTE: runAction + overlayFromRule + buildSnapshotOverlay + the
    // scan-local per-action memory (_perActionState*) moved to
    // js/modules/run.js, composed via { ...appRunModule() } in app()'s
    // return. See docs/resolvarr/frontend-restructure-plan.md (Stage 4).

    async saveAudioTags() {
      if (this.audioTagPrefixInvalid()) {
        this.showToast('Audio prefix has invalid characters — tag names only allow a-z, 0-9, and -', 'error');
        return;
      }
      try {
        const payload = {
          audio: this.audioTags.audio,
          removeOrphanedTags: !!this.audioTags.removeOrphanedTags,
        };
        const r = await this.apiFetch('/api/audio-tags', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },

    async runAudioTagsScan(mode = 'preview', instanceIdOverride = '') {
      // instanceIdOverride lets Apply-now target a specific instance
      // (used by confirmAudioTagsApply when target=both produced two
      // variants — each variant's instance gets its own apply pass).
      // Empty string falls back to the page-level scanInstanceId for
      // legacy single-instance flows.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) { this.showToast('Pick an instance first', 'error'); return; }
      if (!this.anyAudioTagsBucketEnabled()) { this.showToast('Enable Audio bucket first', 'error'); return; }
      // Modal-close + state-reset only on the first pass when the
      // caller didn't pass an override. Apply-now-against-variants
      // does multiple successive runs and must NOT close the modal
      // between them or reset historical state.
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('audio');
        this.autoTagRowExpanded = {};
        this.scanResults.audioTags = null;
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'audiotags') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      this.scanError = '';
      try {
        // runAction builds + POSTs uniformly. buildSnapshotOverlay carries
        // the SAME config the preview ran with (the rule's overlay), not
        // the global Library-scan config.
        this.scanResults.audioTags = await this.runAction({
          instanceId: targetInstanceId, action: 'audiotags', mode,
          overlay: this.buildSnapshotOverlay(this.qfaDetailAudio),
        });
        if (mode === 'apply' && this.scanResults.audioTags.applied) {
          const a = this.scanResults.audioTags.applied;
          this.showToast('Audio tags applied: ' + a.itemsAdded + ' added, ' + a.itemsRemoved + ' removed', 'success');
        }
        // Open detail modal automatically — same popup pattern as the
        // other phases. History row click + QFA chain phase click also
        // route through viewPhaseDetails, so all surfaces converge.
        this.viewPhaseDetails({ phase: 'audiotags', response: this.scanResults.audioTags });
      } catch (e) {
        this.scanError = e.message || 'Audio-tags scan failed';
      } finally {
        this.scanLoading = false;
      }
    },

    audioTagsPendingChangeCount() {
      const r = this.scanResults && this.scanResults.audioTags;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },
    // Apply-now tooltip helper. Renders a context-aware hint per
    // action, for buttons in the result panels' top + bottom strips.
    // Three states:
    //   1. Historical snapshot   → "Run a fresh preview" warning
    //   2. target=both was used  → "Writes to <pri> + <sec>" clarifier
    //   3. Single instance       → empty (no tooltip needed)
    // Action arg: 'tag' | 'audiotags' | 'videotags' | 'dvdetail' | 'recover'.
    // Recover is intentionally scope-narrowing: each instance has its
    // own per-row selection set (vs. audio/video/dv's "every decision
    // applies"), so apply hits only the currently-viewed variant.
    applyNowTooltip(action) {
      if (this.isHistoricalForAction(action)) {
        const labels = { tag: 'Tag', audiotags: 'Audio', videotags: 'Video', dvdetail: 'DV', recover: 'Recover' };
        return 'Run a fresh ' + (labels[action] || 'scan') + ' preview before applying — this is a saved snapshot, not live data.';
      }
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        if (action === 'recover') {
          // Recover's per-row selection is variant-specific — applies
          // only to the instance whose result is currently displayed.
          // Switch variant to apply on the other one. This differs
          // from audio/video/dv which fan-out across every variant.
          return 'Applies to the currently-viewed instance only. Switch variant above to apply on the other one — Recover selections are per-instance.';
        }
        const names = variants.map(v => v.label || (v.instanceId === this.scanInstanceId ? 'Primary' : 'Secondary')).join(' + ');
        return 'Writes to ' + names + '.';
      }
      return '';
    },
    // Same helper but for the in-modal info banner. Empty on
    // single-instance runs so the banner doesn't render.
    applyNowMultiInstanceLabel() {
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length <= 1) return '';
      const names = variants.map(v => v.label || (v.instanceId === this.scanInstanceId ? 'Primary' : 'Secondary')).join(' + ');
      return 'Writes to ' + names + '.';
    },
    openAudioTagsApplyConfirm() {
      // Don't gate on count — when the user picks Apply mode without
      // running Preview first, the count is 0 (no scanResults yet)
      // but they explicitly chose Apply: they want to scan + write in
      // one step. Modal copy adapts via the count check. Same pattern
      // openDvDetailApplyConfirm uses.
      this.showAudioTagsApplyConfirm = true;
    },
    async confirmAudioTagsApply() {
      this.showAudioTagsApplyConfirm = false;
      // Re-entry guard — see confirmScanApply for rationale.
      if (this.scanLoading) return;
      // When the wizard ran with target=both the preview produced two
      // variants (one per instance). Apply must hit BOTH — that's what
      // the user picked. Variant switcher is for reading the result,
      // not for narrowing apply scope. If the user wanted apply to one
      // instance only, they'd have picked primary or secondary in
      // step 1. Single-variant case falls through to legacy single-
      // instance flow against scanInstanceId.
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        const totals = { added: 0, removed: 0 };
        for (const v of variants) {
          await this.runAudioTagsScan('apply', v.instanceId);
          if (this.scanError) break;
          // Refresh the variant entry's response so the pill switcher
          // shows post-apply state (mode chip flips Preview → Applied,
          // counts reflect what was written) instead of the stale
          // preview response. Without this, clicking a pill after
          // fan-out re-displays the original preview which is
          // confusing and looks like the apply didn't take.
          if (this.scanResults.audioTags) {
            v.response = this.scanResults.audioTags;
          }
          const a = this.scanResults.audioTags && this.scanResults.audioTags.applied;
          if (a) {
            totals.added += a.itemsAdded || 0;
            totals.removed += a.itemsRemoved || 0;
          }
        }
        if (!this.scanError) {
          this.showToast('Audio tags applied across ' + variants.length + ' instances: ' + totals.added + ' added, ' + totals.removed + ' removed', 'success');
        }
      } else {
        await this.runAudioTagsScan('apply');
      }
    },
    audioTagMoviesFor(tag, action) {
      const r = this.scanResults && this.scanResults.audioTags;
      if (!r || !r.items) return [];
      const out = [];
      for (const item of r.items) {
        if (!item.autoDecisions) continue;
        for (const d of item.autoDecisions) {
          if (d.tag === tag && d.action === action) { out.push(item); break; }
        }
      }
      return out;
    },

    // Video --------------------------------------------------------
    anyVideoTagsBucketEnabled() {
      const v = this.videoTags;
      return !!(v.resolution.enabled || v.codec.enabled || v.hdr.enabled);
    },
    videoTagPrefixInvalid(bucketKey) {
      const p = (this.videoTags[bucketKey] && this.videoTags[bucketKey].prefix) || '';
      return !this.autoTagPrefixRule.test(p);
    },
    anyVideoTagPrefixInvalid() {
      return ['resolution', 'codec', 'hdr'].some(b => this.videoTagPrefixInvalid(b));
    },
    // Same two-mode select semantics as audioTagValueChecked — see
    // that helper's header comment for the full SelectMode rationale.
    videoTagValueChecked(bucketKey, value) {
      const b = this.videoTags[bucketKey];
      if (!b) return true;
      const av = b.allowedValues;
      if (b.selectMode !== 'select' && (!av || av.length === 0)) return true;
      return !!av && av.includes(value);
    },
    toggleVideoTagValue(bucketKey, value, fullVocab) {
      const bucket = this.videoTags[bucketKey];
      if (bucket.selectMode !== 'select') {
        if (!bucket.allowedValues || bucket.allowedValues.length === 0) {
          bucket.allowedValues = [...fullVocab];
        }
        bucket.selectMode = 'select';
      }
      let av = bucket.allowedValues || [];
      if (av.includes(value)) av = av.filter(v => v !== value);
      else                    av = [...av, value];
      if (av.length === fullVocab.length && fullVocab.every(v => av.includes(v))) {
        bucket.selectMode = '';
        bucket.allowedValues = [];
      } else {
        bucket.allowedValues = av;
      }
      this.saveVideoTags();
    },
    selectAllVideoValues(bucketKey) {
      const bucket = this.videoTags[bucketKey];
      bucket.selectMode = '';
      bucket.allowedValues = [];
      this.saveVideoTags();
    },
    selectNoneVideoValues(bucketKey) {
      const bucket = this.videoTags[bucketKey];
      bucket.selectMode = 'select';
      bucket.allowedValues = [];
      this.saveVideoTags();
    },

    async loadVideoTags() {
      try {
        const r = await this.apiFetch('/api/video-tags');
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.config) {
          const merge = (dst, src) => {
            if (!src) return;
            dst.enabled = !!src.enabled;
            dst.prefix = src.prefix || '';
            dst.sonarrAggregation = src.sonarrAggregation || dst.sonarrAggregation;
            dst.allowedValues = Array.isArray(src.allowedValues) ? src.allowedValues : [];
            dst.selectMode = src.selectMode || '';
            dst.labels = (src.labels && typeof src.labels === 'object') ? { ...src.labels } : {};
          };
          merge(this.videoTags.resolution, data.config.resolution);
          merge(this.videoTags.codec,      data.config.codec);
          merge(this.videoTags.hdr,        data.config.hdr);
          this.videoTags.removeOrphanedTags = !!data.config.removeOrphanedTags;
        }
        if (Array.isArray(data && data.resolution)) this.videoVocab.resolution = data.resolution;
        if (Array.isArray(data && data.codec))      this.videoVocab.codec      = data.codec;
        if (Array.isArray(data && data.hdr))        this.videoVocab.hdr        = data.hdr;
      } catch (_) {}
    },

    async saveVideoTags() {
      const invalid = ['resolution', 'codec', 'hdr'].filter(b => this.videoTagPrefixInvalid(b));
      if (invalid.length > 0) {
        this.showToast(invalid[0] + ' prefix has invalid characters — tag names only allow a-z, 0-9, and -', 'error');
        return;
      }
      try {
        const payload = {
          resolution: this.videoTags.resolution,
          codec:      this.videoTags.codec,
          hdr:        this.videoTags.hdr,
          removeOrphanedTags: !!this.videoTags.removeOrphanedTags,
        };
        const r = await this.apiFetch('/api/video-tags', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error || 'HTTP ' + r.status);
        }
      } catch (e) {
        this.showToast('Save failed: ' + e.message, 'error');
      }
    },

    async runVideoTagsScan(mode = 'preview', instanceIdOverride = '') {
      // instanceIdOverride: same target=both fan-out as runAudioTagsScan.
      const targetInstanceId = instanceIdOverride || this.scanInstanceId;
      if (!targetInstanceId) { this.showToast('Pick an instance first', 'error'); return; }
      if (!this.anyVideoTagsBucketEnabled()) { this.showToast('Enable at least one Video bucket first', 'error'); return; }
      const isFirstPass = !instanceIdOverride;
      if (isFirstPass) {
        this.closeAllResultModals('video');
        this.autoTagRowExpanded = {};
        this.scanResults.videoTags = null;
        if (this.historicalRunInfo && this.historicalRunInfo.kind === 'videotags') {
          this.historicalRunInfo = null;
        }
      }
      this.scanLoading = true;
      this.scanError = '';
      try {
        // runAction builds + POSTs uniformly with the run's overlay, not global.
        this.scanResults.videoTags = await this.runAction({
          instanceId: targetInstanceId, action: 'videotags', mode,
          overlay: this.buildSnapshotOverlay(this.qfaDetailVideo),
        });
        if (mode === 'apply' && this.scanResults.videoTags.applied) {
          const a = this.scanResults.videoTags.applied;
          this.showToast('Video tags applied: ' + a.itemsAdded + ' added, ' + a.itemsRemoved + ' removed', 'success');
        }
        this.viewPhaseDetails({ phase: 'videotags', response: this.scanResults.videoTags });
      } catch (e) {
        this.scanError = e.message || 'Video-tags scan failed';
      } finally {
        this.scanLoading = false;
      }
    },

    videoTagsPendingChangeCount() {
      const r = this.scanResults && this.scanResults.videoTags;
      if (!r || !r.totals) return 0;
      return (r.totals.toAdd || 0) + (r.totals.toRemove || 0);
    },
    openVideoTagsApplyConfirm() {
      // Don't gate on count — same reasoning as openAudioTagsApplyConfirm.
      // Modal copy adapts to the no-preview case via the count check.
      this.showVideoTagsApplyConfirm = true;
    },
    async confirmVideoTagsApply() {
      this.showVideoTagsApplyConfirm = false;
      // Re-entry guard — see confirmScanApply for rationale.
      if (this.scanLoading) return;
      // Same target=both fan-out as confirmAudioTagsApply. See its
      // header comment for the design rationale.
      const variants = (this.qfaDetailVariants || []).filter(v => v && v.instanceId);
      if (variants.length > 1) {
        const totals = { added: 0, removed: 0 };
        for (const v of variants) {
          await this.runVideoTagsScan('apply', v.instanceId);
          if (this.scanError) break;
          // Refresh variant response to apply state — see
          // confirmAudioTagsApply for rationale.
          if (this.scanResults.videoTags) {
            v.response = this.scanResults.videoTags;
          }
          const a = this.scanResults.videoTags && this.scanResults.videoTags.applied;
          if (a) {
            totals.added += a.itemsAdded || 0;
            totals.removed += a.itemsRemoved || 0;
          }
        }
        if (!this.scanError) {
          this.showToast('Video tags applied across ' + variants.length + ' instances: ' + totals.added + ' added, ' + totals.removed + ' removed', 'success');
        }
      } else {
        await this.runVideoTagsScan('apply');
      }
    },
    videoTagMoviesFor(tag, action) {
      const r = this.scanResults && this.scanResults.videoTags;
      if (!r || !r.items) return [];
      const out = [];
      for (const item of r.items) {
        if (!item.autoDecisions) continue;
        for (const d of item.autoDecisions) {
          if (d.tag === tag && d.action === action) { out.push(item); break; }
        }
      }
      return out;
    },

    // Shared per-row drill-down toggle (audio / video / dv all use the
    // same autoTagRowExpanded state).
    toggleAutoTagRow(tag) {
      this.autoTagRowExpanded = { ...this.autoTagRowExpanded, [tag]: !this.autoTagRowExpanded[tag] };
    },
  };
}
