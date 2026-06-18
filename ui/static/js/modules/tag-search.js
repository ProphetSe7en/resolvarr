// resolvarr UI — tag search (tag inventory) module
//
// Tag search Mode B: query items by tag combinations. Composed into the Alpine
// root via { ...appTagSearch() } in app(); methods use `this` = the Alpine
// component (bound on spread), so peer calls work exactly as before.
function appTagSearch() {
  return {
    // ---- Tag search (Mode B — query items by tag combinations) ----

    // Map of bucket-name → set of tag-name strings, derived from the
    // currently-loaded ExtraTags / AudioTags / VideoTags / DvDetail config.
    // Used by the bucket: macro in the DSL.
    tagSearchBuckets() {
      const out = {};
      const expand = (vocab, prefix) => {
        const set = new Set();
        for (const v of (vocab || [])) {
          set.add(v);
          if (prefix) set.add(prefix + v);
        }
        return set;
      };
      // Audio sub-buckets share one prefix; expose individual sub-buckets
      // and a combined 'audio' alias for convenience.
      const audioPrefix = (this.audioTags && this.audioTags.audio && this.audioTags.audio.prefix) || '';
      const audioCodec    = expand(this.audioVocab && this.audioVocab.codecs,   audioPrefix);
      const audioChannels = expand(this.audioVocab && this.audioVocab.channels, audioPrefix);
      const audioFlags    = expand(this.audioVocab && this.audioVocab.flags,    audioPrefix);
      out['audio-codec']    = audioCodec;
      out['audio-channels'] = audioChannels;
      out['audio-flags']    = audioFlags;
      out['audio'] = new Set([...audioCodec, ...audioChannels, ...audioFlags]);
      // Video sub-buckets — each has its own prefix in config.
      const vt = this.videoTags || {};
      out['resolution'] = expand(this.videoVocab && this.videoVocab.resolution, vt.resolution && vt.resolution.prefix);
      out['codec']      = expand(this.videoVocab && this.videoVocab.codec,      vt.codec      && vt.codec.prefix);
      out['hdr']        = expand(this.videoVocab && this.videoVocab.hdr,        vt.hdr        && vt.hdr.prefix);
      // DV detail.
      const dvPrefix = (this.dvDetail && this.dvDetail.prefix) || '';
      out['dv-detail'] = expand(this.dvDetailVocab, dvPrefix);
      return out;
    },

    // Tokenise a query into a flat token stream. Tokens:
    //   {kind: 'and' | 'or' | 'not' | 'lparen' | 'rparen'}
    //   {kind: 'term', value: 'tagname', wildcard: bool, bucket: 'name'?}
    // Implicit AND is inserted later during the parse phase.
    tagSearchTokenise(input) {
      const tokens = [];
      const re = /\s*([()]|[^\s()]+)/g;
      let m;
      while ((m = re.exec(input)) !== null) {
        const raw = m[1];
        if (raw === '(') { tokens.push({ kind: 'lparen' }); continue; }
        if (raw === ')') { tokens.push({ kind: 'rparen' }); continue; }
        const lower = raw.toLowerCase();
        if (lower === 'and') { tokens.push({ kind: 'and' }); continue; }
        if (lower === 'or')  { tokens.push({ kind: 'or'  }); continue; }
        if (lower === 'not') { tokens.push({ kind: 'not' }); continue; }
        if (raw.startsWith('-') && raw.length > 1) {
          tokens.push({ kind: 'not' });
          tokens.push(this.tagSearchTermToken(raw.slice(1)));
          continue;
        }
        tokens.push(this.tagSearchTermToken(raw));
      }
      return tokens;
    },

    tagSearchTermToken(raw) {
      const lower = raw.toLowerCase();
      if (lower.startsWith('bucket:')) {
        return { kind: 'term', bucket: lower.slice('bucket:'.length), value: '', wildcard: false };
      }
      if (lower.endsWith('*')) {
        return { kind: 'term', value: lower.slice(0, -1), wildcard: true };
      }
      return { kind: 'term', value: lower, wildcard: false };
    },

    // Parse the token stream into an AST. Recursive-descent with implicit
    // AND. AST nodes: {op:'and'|'or', left, right} | {op:'not', child} |
    // {op:'term', value, wildcard, bucket?}. Throws on syntax errors.
    // Validates bucket: macros against the live bucket-name set so users
    // get an immediate error instead of a silent zero-match.
    tagSearchParse(input, validBuckets) {
      const tokens = this.tagSearchTokenise(input);
      if (tokens.length === 0) return null;
      // Reject empty-prefix wildcards ("*" alone) and bucket: with no name —
      // both produce match-everything-or-nothing surprises.
      for (const t of tokens) {
        if (t.kind !== 'term') continue;
        if (t.bucket !== undefined) {
          if (!t.bucket) throw new Error("'bucket:' needs a name (e.g. bucket:dv-detail)");
          if (validBuckets && !validBuckets.has(t.bucket)) {
            const list = validBuckets ? [...validBuckets].sort().join(', ') : '';
            throw new Error("unknown bucket '" + t.bucket + "' — valid: " + list);
          }
        } else if (t.wildcard && !t.value) {
          throw new Error("'*' on its own matches every tag — narrow it (e.g. rg-*)");
        }
      }
      let pos = 0;
      const peek = () => tokens[pos];
      const consume = (kind) => {
        if (!peek() || peek().kind !== kind) throw new Error('expected ' + kind);
        return tokens[pos++];
      };
      // expr := orExpr
      // orExpr := andExpr (OR andExpr)*
      // andExpr := unary (AND unary | unary)*    -- implicit AND when no operator
      // unary := NOT unary | atom
      // atom := term | '(' expr ')'
      const parseAtom = () => {
        const t = peek();
        if (!t) throw new Error('unexpected end of input');
        if (t.kind === 'lparen') {
          consume('lparen');
          const inner = parseOr();
          if (!peek() || peek().kind !== 'rparen') throw new Error("missing ')'");
          consume('rparen');
          return inner;
        }
        if (t.kind === 'term') {
          pos++;
          return { op: 'term', value: t.value, wildcard: !!t.wildcard, bucket: t.bucket };
        }
        throw new Error("unexpected token '" + (t.kind) + "'");
      };
      const parseUnary = () => {
        if (peek() && peek().kind === 'not') {
          consume('not');
          return { op: 'not', child: parseUnary() };
        }
        return parseAtom();
      };
      const parseAnd = () => {
        let left = parseUnary();
        while (peek() && peek().kind !== 'or' && peek().kind !== 'rparen') {
          if (peek().kind === 'and') consume('and'); // explicit AND
          // implicit AND: just continue parsing the next unary
          const right = parseUnary();
          left = { op: 'and', left, right };
        }
        return left;
      };
      const parseOr = () => {
        let left = parseAnd();
        while (peek() && peek().kind === 'or') {
          consume('or');
          const right = parseAnd();
          left = { op: 'or', left, right };
        }
        return left;
      };
      const result = parseOr();
      if (pos !== tokens.length) throw new Error("unexpected trailing input");
      return result;
    },

    // Evaluate the AST against an item. itemTagLabels is a Set of
    // lowercase tag-label strings the item carries. allTagLabels is the
    // full inventory (for wildcard expansion); buckets is the bucket map.
    tagSearchEval(node, itemTagLabels, allTagLabels, buckets) {
      if (!node) return true;
      if (node.op === 'and') {
        return this.tagSearchEval(node.left, itemTagLabels, allTagLabels, buckets)
            && this.tagSearchEval(node.right, itemTagLabels, allTagLabels, buckets);
      }
      if (node.op === 'or') {
        return this.tagSearchEval(node.left, itemTagLabels, allTagLabels, buckets)
            || this.tagSearchEval(node.right, itemTagLabels, allTagLabels, buckets);
      }
      if (node.op === 'not') {
        return !this.tagSearchEval(node.child, itemTagLabels, allTagLabels, buckets);
      }
      if (node.op === 'term') {
        // Bucket macro — match if the item carries any tag from the bucket.
        if (node.bucket) {
          const set = buckets[node.bucket];
          if (!set) return false; // unknown bucket → no match
          for (const tag of itemTagLabels) {
            if (set.has(tag)) return true;
          }
          return false;
        }
        // Wildcard — match if any item-tag starts with the prefix.
        if (node.wildcard) {
          const prefix = node.value;
          for (const tag of itemTagLabels) {
            if (tag.startsWith(prefix)) return true;
          }
          return false;
        }
        // Exact (case-insensitive) match.
        return itemTagLabels.has(node.value);
      }
      return false;
    },

    // Returns an array of label strings the term matches against the
    // current inventory — used to highlight matching tag chips on the
    // result rows. For NOT nodes, returns the negative set's labels too
    // so the user can see WHY a row matched ("doesn't have any of …").
    // Keeps it simple: returns positive matches only, NOT-branches are
    // skipped for highlight purposes.
    tagSearchHighlightSet(node, allTagLabels, buckets) {
      const out = new Set();
      const walk = (n, negated) => {
        if (!n) return;
        if (n.op === 'and' || n.op === 'or') {
          walk(n.left, negated);
          walk(n.right, negated);
          return;
        }
        if (n.op === 'not') {
          walk(n.child, !negated);
          return;
        }
        if (n.op === 'term' && !negated) {
          if (n.bucket) {
            const set = buckets[n.bucket];
            if (set) for (const t of set) out.add(t);
            return;
          }
          if (n.wildcard) {
            for (const t of allTagLabels) if (t.startsWith(n.value)) out.add(t);
            return;
          }
          out.add(n.value);
        }
      };
      walk(node, false);
      return out;
    },

    // Compute results for the current query — returns {items, highlight,
    // matchedTotal, libraryTotal} or null if query is empty.
    // Memoised: the function is invoked from inside x-for chip loops, so
    // a 2000-movie library would otherwise pay a full scan per chip per
    // render. Cache key combines query + instance + cacheVersion so any
    // state change that affects results busts the memo.
    tagSearchResults() {
      const q = this.tagSearchQuery.trim();
      if (!q) return null;
      const cache = this.tagSearchCache[this.tagsInstanceId];
      if (!cache) return null;
      const buckets = this.tagSearchBuckets();
      const validBucketNames = new Set(Object.keys(buckets));
      const key = q + '|' + this.tagsInstanceId + '|' + this.tagSearchCacheVersion;
      if (key === this._tagSearchResultsKey && this._tagSearchResultsValue) {
        return this._tagSearchResultsValue;
      }
      let ast;
      try {
        ast = this.tagSearchParse(q, validBucketNames);
        this.tagSearchParseError = '';
      } catch (e) {
        this.tagSearchParseError = e.message;
        const errOut = { items: [], highlight: new Set(), matchedTotal: 0, libraryTotal: cache.items.length };
        this._tagSearchResultsKey = key;
        this._tagSearchResultsValue = errOut;
        return errOut;
      }
      const idToLabel = new Map();
      const allLabels = new Set();
      for (const t of cache.tags) {
        const lbl = t.label.toLowerCase();
        idToLabel.set(t.id, lbl);
        allLabels.add(lbl);
      }
      const matched = [];
      for (const it of cache.items) {
        const labelSet = new Set();
        for (const tid of it.tags) {
          const lbl = idToLabel.get(tid);
          if (lbl) labelSet.add(lbl);
        }
        if (this.tagSearchEval(ast, labelSet, allLabels, buckets)) {
          matched.push({
            ...it,
            tagLabels: [...labelSet].sort(),
          });
        }
      }
      const result = {
        items: matched,
        highlight: this.tagSearchHighlightSet(ast, allLabels, buckets),
        matchedTotal: matched.length,
        libraryTotal: cache.items.length,
      };
      this._tagSearchResultsKey = key;
      this._tagSearchResultsValue = result;
      return result;
    },

    async loadTagSearchInventory(force = false) {
      // Snapshot the instance id at call time. If the user switches the
      // dropdown before the fetch resolves, the response belongs to the
      // PREVIOUS instance and must not be written to the new instance's
      // cache slot. The snapshot also drives the URL so the request is
      // never built against a stale id mid-flight.
      const inst = this.tagsInstanceId;
      if (!inst) return;
      if (!force && this.tagSearchCache[inst]) return;
      this.tagSearchLoading = true;
      this.tagSearchError = '';
      try {
        const r = await this.apiFetch(`/api/instances/${inst}/items-with-tags`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        // Stale-response guard: if instance switched during fetch, drop
        // the result on the floor — the new instance's load (if any)
        // will populate the right cache slot on its own.
        if (inst !== this.tagsInstanceId) return;
        this.tagSearchCache[inst] = {
          tags: d.tags || [],
          items: d.items || [],
        };
        this.tagSearchCacheVersion++;
      } catch (e) {
        if (inst === this.tagsInstanceId) {
          this.tagSearchError = 'Could not load library: ' + e.message;
        }
      } finally {
        if (inst === this.tagsInstanceId) {
          this.tagSearchLoading = false;
        }
      }
    },

    // Called when the user types in the search field. Loads inventory
    // lazily on first non-empty query.
    onTagSearchInput() {
      this.tagSearchExpanded = {};
      if (this.tagSearchQuery.trim() && !this.tagSearchCache[this.tagsInstanceId]) {
        this.loadTagSearchInventory();
      }
    },

    clearTagSearch() {
      this.tagSearchQuery = '';
      this.tagSearchParseError = '';
      this.tagSearchExpanded = {};
    },

    toggleTagSearchRow(itemId) {
      this.tagSearchExpanded = { ...this.tagSearchExpanded, [itemId]: !this.tagSearchExpanded[itemId] };
    },

    // Per-row expand toggle for the tag inventory drill-down. Mirrors the
    // search-results pattern so both views feel the same; rows start
    // collapsed so the drill-down doesn't dump every field on screen.
    toggleTagItemRow(itemId) {
      this.tagItemExpanded = { ...this.tagItemExpanded, [itemId]: !this.tagItemExpanded[itemId] };
    },

    currentInstanceTypeLabel() {
      const t = this.currentInstanceType();
      if (!t) return 'the instance';
      return t.charAt(0).toUpperCase() + t.slice(1);
    },

    // itemLabel returns "movie"/"movies" for Radarr, "series"/"series" for Sonarr.
    itemLabel(n) {
      if (this.currentInstanceType() === 'radarr') return n === 1 ? 'movie' : 'movies';
      if (this.currentInstanceType() === 'sonarr') return 'series';
      return n === 1 ? 'item' : 'items';
    },

    usageColumnLabel() {
      if (this.currentInstanceType() === 'radarr') return 'Movies';
      if (this.currentInstanceType() === 'sonarr') return 'Series';
      return 'Used by';
    },

    setSort(col) {
      if (this.tagsSort === col) {
        this.tagsSortDir = this.tagsSortDir === 'asc' ? 'desc' : 'asc';
      } else {
        this.tagsSort = col;
        // Sensible defaults: label A→Z, usage most-first.
        this.tagsSortDir = col === 'label' ? 'asc' : 'desc';
      }
    },

    sortedTags() {
      const dir = this.tagsSortDir === 'asc' ? 1 : -1;
      const out = [...this.tags];
      if (this.tagsSort === 'label') {
        out.sort((a, b) => a.label.localeCompare(b.label) * dir);
      } else {
        out.sort((a, b) => {
          const d = (a.usageCount - b.usageCount) * dir;
          return d !== 0 ? d : a.label.localeCompare(b.label);
        });
      }
      return out;
    },

    deleteTargetsTotalUsage() {
      return this.deleteTargets.reduce((s, t) => s + (t.usageCount || 0), 0);
    },

    async loadTags() {
      if (!this.tagsInstanceId) return;
      this.tagsLoading = true;
      this.tagsLoadError = '';
      this.tagsSelected = new Set();
      // Reset drill-down state — tag IDs may not be the same on a different
      // instance, and a Reload should re-fetch any expanded sections so the
      // items list reflects current state (just-applied tags, etc).
      this.tagExpanded = {};
      this.tagItems = {};
      this.tagItemsLoading = {};
      this.tagItemsError = {};
      // Per-row expanded state must reset too — the next instance may have
      // an item with the same internal ID as one the user expanded here,
      // and Alpine would re-open the wrong row with the new instance's data.
      this.tagItemExpanded = {};
      // Same reasoning for compare — selected tag IDs may not exist on the
      // newly-loaded instance, and stale results would be misleading.
      this.compareOpen = false;
      this.compareResults = null;
      this.compareError = '';
      this.compareExpanded = { both: false, onlyA: false, onlyB: false };
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.tags = d || [];
      } catch (e) {
        this.tagsLoadError = e.message;
        this.tags = [];
      } finally {
        this.tagsLoading = false;
      }
    },

    // Returns instances of the same Arr type as the current Tag inventory
    // instance, excluding it. Drives the cross-instance picker dropdown.
    compareCrossInstanceCandidates() {
      const t = this.currentInstanceType();
      if (!t) return [];
      return this.instances
        .filter(i => i.type === t && i.id !== this.tagsInstanceId)
        .sort((a, b) => a.name.localeCompare(b.name));
    },

    // Compare button enables when:
    //   - 2 tags selected from current instance and no cross-instance target
    //     (original same-instance flow)
    //   - 1 tag selected and a cross-instance target picked
    //     (cross-instance flow — same-name match on the other instance)
    compareCanRun() {
      if (this.compareLoading) return false;
      if (this.compareCrossInstanceTarget) return this.tagsSelected.size === 1;
      return this.tagsSelected.size === 2;
    },

    compareDisabledTooltip() {
      if (this.compareLoading) return 'Comparing…';
      if (this.compareCrossInstanceTarget) {
        if (this.tagsSelected.size === 0) return 'Pick the tag you want to compare across instances';
        if (this.tagsSelected.size > 1)   return 'Cross-instance compare uses one tag — uncheck the extras';
        return 'Compare this tag against the same name on the other instance';
      }
      if (this.tagsSelected.size === 0) return 'Pick 2 tags to compare, or pick another instance to compare across';
      if (this.tagsSelected.size === 1) return 'Pick a 2nd tag, or pick another instance to compare across';
      if (this.tagsSelected.size > 2)   return 'Same-instance compare uses exactly 2 tags — uncheck the extras';
      return 'Compare the two selected tags';
    },

    // Compare two tags — fetch items for both via tag-items, compute set
    // differences in-browser. The endpoint already supports multi-id query
    // (?ids=A,B) so this is one round-trip. Sorted by title for stable
    // diffs across runs (parity testing leans on this — a moved item would
    // otherwise look like a regression even when the set is identical).
    //
    // The two tag IDs come from the toolbar's `tagsSelected` Set in the
    // order JavaScript chose to iterate — Sets preserve insertion order,
    // so A is whichever the user clicked first. Acceptable: which is "A"
    // vs "B" doesn't change the math, only the column labels.
    async runCompare(idA, idB) {
      if (!idA || !idB || idA === idB) return;
      // Snapshot the inputs so a late-arriving response doesn't overwrite
      // state that the user has since cleared (closeCompare, app-type
      // switch, or instance switch). Same pattern as loadTagSearchInventory.
      const instSnap = this.tagsInstanceId;
      const idASnap = idA, idBSnap = idB;
      const stale = () => this.tagsInstanceId !== instSnap
                       || !this.compareOpen
                       || !this.tagsSelected.has(idASnap)
                       || !this.tagsSelected.has(idBSnap);
      this.compareLoading = true;
      this.compareError = '';
      this.compareResults = null;
      try {
        const r = await this.apiFetch(`/api/instances/${instSnap}/tag-items?ids=${idA},${idB}`);
        if (stale()) return;
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.compareError = msg || ('HTTP ' + r.status);
          return;
        }
        const data = await r.json();
        const a = (data || []).find(g => g.tagId === idA) || { label: '', items: [] };
        const b = (data || []).find(g => g.tagId === idB) || { label: '', items: [] };
        const aIds = new Set(a.items.map(it => it.id));
        const bIds = new Set(b.items.map(it => it.id));
        const both = a.items.filter(it => bIds.has(it.id));
        const onlyA = a.items.filter(it => !bIds.has(it.id));
        const onlyB = b.items.filter(it => !aIds.has(it.id));
        if (stale()) return;
        const sortFn = (x, y) => x.title.localeCompare(y.title);
        const instName = (this.instances.find(i => i.id === instSnap) || {}).name || '';
        this.compareResults = {
          a: { label: a.label, instanceName: instName, items: [...a.items].sort(sortFn) },
          b: { label: b.label, instanceName: instName, items: [...b.items].sort(sortFn) },
          both: both.sort(sortFn),
          onlyA: onlyA.sort(sortFn),
          onlyB: onlyB.sort(sortFn),
          crossInstance: false,
          joinKey: 'id',
        };
        // Auto-expand the categories with content so the user sees something
        // immediately instead of having to click each card.
        this.compareExpanded = {
          both: both.length > 0,
          onlyA: onlyA.length > 0,
          onlyB: onlyB.length > 0,
        };
      } catch (e) {
        if (!stale()) this.compareError = e.message || 'Compare failed';
      } finally {
        if (this.tagsInstanceId === instSnap) this.compareLoading = false;
      }
    },

    // Toggle the drill-down expander on a tag row. First expand triggers a
    // lazy fetch via /api/instances/{id}/tag-items?ids=<tagId>; collapse
    // keeps the cached items so a re-expand is instant.
    async toggleTagExpanded(tagID) {
      const next = { ...this.tagExpanded };
      if (next[tagID]) {
        delete next[tagID];
        this.tagExpanded = next;
        return;
      }
      next[tagID] = true;
      this.tagExpanded = next;

      // Already cached? Skip the fetch.
      if (this.tagItems[tagID]) return;

      this.tagItemsLoading = { ...this.tagItemsLoading, [tagID]: true };
      this.tagItemsError = { ...this.tagItemsError, [tagID]: '' };
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tag-items?ids=${tagID}`);
        if (!r.ok) {
          const body = await r.text();
          let msg = body;
          try { msg = JSON.parse(body).error || body; } catch {}
          this.tagItemsError = { ...this.tagItemsError, [tagID]: msg || ('HTTP ' + r.status) };
          return;
        }
        const data = await r.json();
        // Endpoint returns [{tagId, label, items}] — flatten to the matched group.
        const group = (data || []).find(g => g.tagId === tagID);
        this.tagItems = { ...this.tagItems, [tagID]: (group && group.items) || [] };
      } catch (e) {
        this.tagItemsError = { ...this.tagItemsError, [tagID]: e.message || 'unknown' };
      } finally {
        this.tagItemsLoading = { ...this.tagItemsLoading, [tagID]: false };
      }
    },

    toggleOneTag(id, checked) {
      const next = new Set(this.tagsSelected);
      if (checked) next.add(id); else next.delete(id);
      this.tagsSelected = next;
      // Selection changed — close any open compare so results never lag
      // behind the selection. User re-clicks Compare to re-run with the
      // new pair.
      this.closeCompare();
    },

    toggleAllTags(checked) {
      this.tagsSelected = checked ? new Set(this.tags.map(t => t.id)) : new Set();
      this.closeCompare();
    },

    // Tags that are safe to delete without manual triage —
    // 0 movies/series attached AND no non-item references
    // (Lists, Custom Formats, Notifications, Indexers, etc.)
    // Reuses `nonItemUsage` keyed off the API response for
    // each tag, same data the delete-confirm pre-check uses.
    _isUnusedSafelyDeletable(t) {
      if (!t) return false;
      if ((t.usageCount || 0) > 0) return false;
      const u = t.nonItemUsage || {};
      return Object.keys(u).length === 0;
    },

    unusedSafelyDeletableCount() {
      return this.tags.reduce((n, t) => n + (this._isUnusedSafelyDeletable(t) ? 1 : 0), 0);
    },

    // Bulk-select every tag that meets the both-criteria gate
    // above. Replaces the current selection rather than merging
    // into it — the button's clear intent is "give me a clean
    // selection of safe-to-delete orphans". Closes any open
    // compare panel since selection changed.
    selectUnusedTags() {
      const next = new Set();
      for (const t of this.tags) {
        if (this._isUnusedSafelyDeletable(t)) next.add(t.id);
      }
      this.tagsSelected = next;
      this.closeCompare();
    },

    // Compare panel toggle. Dispatches to one of two flows based on the
    // selection + cross-instance picker state. Re-clicking when the panel
    // is already open closes it (toggle behaviour).
    async toggleCompare() {
      if (this.compareOpen) {
        this.closeCompare();
        return;
      }
      if (!this.compareCanRun()) return;
      this.compareOpen = true;
      if (this.compareCrossInstanceTarget) {
        const [tagId] = [...this.tagsSelected];
        await this.runCrossInstanceCompare(tagId, this.compareCrossInstanceTarget);
      } else {
        const [idA, idB] = [...this.tagsSelected];
        await this.runCompare(idA, idB);
      }
    },

    closeCompare() {
      this.compareOpen = false;
      this.compareResults = null;
      this.compareError = '';
      this.compareExpanded = { both: false, onlyA: false, onlyB: false };
    },

    // Cross-instance compare. Resolves the same tag NAME on the target
    // instance, fetches both sides' tag-items, and joins on tmdbId for
    // Radarr or tvdbId for Sonarr (instance-local item IDs are useless
    // across instances). Items missing the join key are excluded from
    // both/onlyA/onlyB and reported in `unjoinable` so users know why a
    // count looks low. Same-name match is the v1 scope; comparing across
    // different tag NAMES is a follow-up.
    async runCrossInstanceCompare(tagAId, instanceBId) {
      // Snapshot the inputs — three sequential awaits below means a wide
      // race window. If the user closes the panel, switches instance,
      // changes the cross-instance target, or clears the tag selection
      // mid-flight, this run's results are discarded on the floor.
      const instSnap = this.tagsInstanceId;
      const targetSnap = instanceBId;
      const tagSnap = tagAId;
      const stale = () => this.tagsInstanceId !== instSnap
                       || this.compareCrossInstanceTarget !== targetSnap
                       || !this.compareOpen
                       || !this.tagsSelected.has(tagSnap);
      this.compareLoading = true;
      this.compareError = '';
      this.compareResults = null;
      try {
        const instA = this.instances.find(i => i.id === instSnap);
        const instB = this.instances.find(i => i.id === instanceBId);
        if (!instA || !instB || instA.id === instB.id) {
          if (!stale()) this.compareError = 'Instance not found';
          return;
        }
        if (instA.type !== instB.type) {
          if (!stale()) this.compareError = 'Cross-Arr-type compare (Radarr vs Sonarr) is not supported — different content types';
          return;
        }
        const joinKey = instA.type === 'sonarr' ? 'tvdbId' : 'tmdbId';

        // Side A — fetch the chosen tag's items on the current instance.
        const tagA = this.tags.find(t => t.id === tagSnap);
        if (!tagA) { if (!stale()) this.compareError = 'Selected tag not found on this instance'; return; }
        // Side A and B's tag-list can run in parallel — neither depends
        // on the other. Side B's tag-items still has to wait for B's
        // tags to identify the matching tag id.
        const [aResp, tagsResp] = await Promise.all([
          this.apiFetch(`/api/instances/${instSnap}/tag-items?ids=${tagSnap}`),
          this.apiFetch(`/api/instances/${targetSnap}/tags`),
        ]);
        if (stale()) return;
        if (!aResp.ok)    { this.compareError = 'Failed to load tag on side A: HTTP ' + aResp.status; return; }
        if (!tagsResp.ok) { this.compareError = 'Failed to load tags on side B: HTTP ' + tagsResp.status; return; }
        const aData = await aResp.json();
        const aGroup = (aData || []).find(g => g.tagId === tagSnap) || { label: tagA.label, items: [] };
        const bTags = await tagsResp.json();
        const tagB = (bTags || []).find(t => t.label.toLowerCase() === tagA.label.toLowerCase());
        let bGroup = { label: tagA.label, items: [] };
        if (tagB) {
          const bResp = await this.apiFetch(`/api/instances/${targetSnap}/tag-items?ids=${tagB.id}`);
          if (stale()) return;
          if (!bResp.ok) { this.compareError = 'Failed to load tag on side B: HTTP ' + bResp.status; return; }
          const bData = await bResp.json();
          bGroup = (bData || []).find(g => g.tagId === tagB.id) || bGroup;
        }

        // Join by tmdbId/tvdbId. Items missing the key are unjoinable —
        // reported separately so users can see why counts may be lower
        // than expected (rare, but happens with stub-only entries).
        const aById = new Map();
        const bById = new Map();
        let aUnjoinable = 0, bUnjoinable = 0;
        for (const it of aGroup.items) {
          const k = it[joinKey];
          if (!k) { aUnjoinable++; continue; }
          aById.set(k, it);
        }
        for (const it of bGroup.items) {
          const k = it[joinKey];
          if (!k) { bUnjoinable++; continue; }
          bById.set(k, it);
        }
        const both = [], onlyA = [], onlyB = [];
        for (const [k, aIt] of aById) {
          if (bById.has(k)) {
            const bIt = bById.get(k);
            // Use side-A item as the row identity; carry side-B reference
            // so the result UI can render both file contexts if useful.
            both.push({ ...aIt, _b: bIt, _joinKey: k });
          } else {
            onlyA.push({ ...aIt, _joinKey: k });
          }
        }
        for (const [k, bIt] of bById) {
          if (!aById.has(k)) onlyB.push({ ...bIt, _joinKey: k });
        }
        if (stale()) return;
        const sortFn = (x, y) => x.title.localeCompare(y.title);
        this.compareResults = {
          a: { label: aGroup.label, instanceName: instA.name, items: [...aGroup.items].sort(sortFn) },
          b: { label: bGroup.label, instanceName: instB.name, items: [...bGroup.items].sort(sortFn) },
          both: both.sort(sortFn),
          onlyA: onlyA.sort(sortFn),
          onlyB: onlyB.sort(sortFn),
          crossInstance: true,
          joinKey,
          tagBExists: !!tagB,
          aUnjoinable, bUnjoinable,
        };
        this.compareExpanded = {
          both: both.length > 0,
          onlyA: onlyA.length > 0,
          onlyB: onlyB.length > 0,
        };
      } catch (e) {
        if (!stale()) this.compareError = e.message || 'Cross-instance compare failed';
      } finally {
        if (this.tagsInstanceId === instSnap && this.compareCrossInstanceTarget === targetSnap) {
          this.compareLoading = false;
        }
      }
    },

    // ===== Tag label validation (per-app-type) =====
    //
    // Source of truth for what each Arr's POST /api/v3/tag will accept:
    //
    // RADARR (TagController.cs in Radarr/Radarr):
    //   .Matches("^[a-z0-9-]+$", RegexOptions.IgnoreCase)
    //   .WithMessage("Allowed characters a-z, 0-9 and -")
    // Then TagService.Add lowercases the label via ToLowerInvariant before
    // insert. So uppercase is accepted by the validator but stored as
    // lowercase. Periods, colons, underscores, spaces, unicode, emoji
    // → 400. No length cap in source.
    //
    // SONARR (TagController.cs in Sonarr/Sonarr):
    //   No validator at all. Accepts anything non-empty. TagService.Add
    //   also lowercases via ToLowerInvariant before insert. Spaces,
    //   periods, unicode, emoji all accepted; just stored lowercase.
    //
    // Implication for our preview: always lowercase before send (matches
    // what the server will store + sidesteps a case-sensitive
    // FindByLabel dedup race in both Arrs). Block Radarr-illegal chars
    // up-front so the user sees the reason, not just a 400.

    tagLabelNormalize(s) {
      return (s || '').trim().toLowerCase();
    },

    // Live keystroke sanitiser bound to the rename inputs.
    //   Radarr: validator is `^[a-z0-9-]+$` IgnoreCase + server-side
    //     ToLowerInvariant. We strip anything outside that set as the
    //     user types — uppercase, spaces, periods, colons, underscores,
    //     unicode, emoji all refuse to land in the input.
    //   Sonarr: validator accepts any non-empty string but TagService
    //     still lowercases via ToLowerInvariant on insert. We lowercase
    //     keystrokes too so the input shows what will actually be
    //     stored — spaces, periods, accented chars all pass through.
    // Both rules give a WYSIWYG preview without trailing "will be
    // saved as X" warnings.
    sanitizeTagInput(value, appType) {
      const v = (value || '').toLowerCase();
      if (appType === 'radarr') {
        return v.replace(/[^a-z0-9-]/g, '');
      }
      return v;
    },

    // Returns { valid, reason, message, normalized } where reason is one
    // of '' | 'empty' | 'radarr-chars' | 'too-long'. message is a
    // user-facing string the modals display next to the input.
    tagLabelValidate(s, appType) {
      const normalized = this.tagLabelNormalize(s);
      if (!normalized) {
        return { valid: false, reason: 'empty', message: 'Tag name cannot be empty.', normalized };
      }
      // Defensive cap — neither Arr enforces one in source code, but
      // SQLite's TEXT column has practical limits and a 200-char tag
      // is not a real use case. Higher than the longest released
      // group (~30) plus margin for prefixes / decorators.
      if (normalized.length > 200) {
        return { valid: false, reason: 'too-long', message: 'Tag name is too long (200-char cap).', normalized };
      }
      if (appType === 'radarr') {
        if (!/^[a-z0-9-]+$/.test(normalized)) {
          // Pull out the offending characters so the message is concrete.
          const bad = [...new Set(normalized.replace(/[a-z0-9-]/g, '').split(''))]
            .map(c => c === ' ' ? '"space"' : '"' + c + '"')
            .join(', ');
          return {
            valid: false,
            reason: 'radarr-chars',
            message: 'Radarr only accepts a-z, 0-9 and hyphens. Remove: ' + bad + '.',
            normalized,
          };
        }
      }
      // Sonarr — anything non-empty after lowercasing.
      return { valid: true, reason: '', message: '', normalized };
    },

    // Single-rename helpers — return validation state for the current
    // input + active instance. The modal binds these for the input
    // hint, preview row, and submit-button gate.
    renameValidation() {
      return this.tagLabelValidate(this.renameNewLabel, this.currentInstanceType());
    },
    renameNormalized() {
      return this.renameValidation().normalized;
    },

    openRenameTag(t) {
      this.renameTarget = { id: t.id, label: t.label, usageCount: t.usageCount };
      this.renameNewLabel = t.label;
      this.renameKeepOldDefinition = false;
      this.renameError = '';
      this.renameBusy = false;
      this.renamePreview = [];
      this.showRenameModal = true;
      this.loadRenamePreview();
    },

    async loadRenamePreview() {
      this.renamePreviewLoading = true;
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tag-items?ids=${this.renameTarget.id}`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.renamePreview = (d && d[0] && d[0].items) ? d[0].items : [];
      } catch (e) {
        this.renamePreview = [];
        this.renameError = 'Preview failed: ' + e.message;
      } finally {
        this.renamePreviewLoading = false;
      }
    },

    // Returns the existing tag that the new label would merge into, or null.
    // Comparison uses the normalized (trim+lowercase) form because that's
    // what the server will store — "MyTag" submitted against existing
    // "mytag" is a merge, not a fresh rename.
    renameMergeTarget() {
      const newLabel = this.renameNormalized();
      if (!newLabel || newLabel === this.renameTarget.label.toLowerCase()) return null;
      return this.tags.find(t => t.label.toLowerCase() === newLabel && t.id !== this.renameTarget.id) || null;
    },

    async submitRename() {
      const v = this.renameValidation();
      if (!v.valid) return;
      // Send the normalized form. Both Arrs lowercase server-side before
      // insert; sending pre-lowercased avoids a case-sensitive
      // FindByLabel dedup race where "MyTag" submitted against existing
      // DB row "mytag" hits an insert + UNIQUE-constraint failure
      // instead of finding the merge candidate.
      const newLabel = v.normalized;
      if (newLabel === this.renameTarget.label.toLowerCase()) {
        this.showRenameModal = false;
        return;
      }
      this.renameBusy = true;
      this.renameError = '';
      try {
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags/rename`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            oldId: this.renameTarget.id,
            newLabel,
            keepOldDefinition: this.renameKeepOldDefinition,
          }),
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.showRenameModal = false;
        const label = this.itemLabel(d.movedCount || 0);
        const verb = d.merged ? 'Merged into' : 'Renamed to';
        this.showToast(`${verb} "${newLabel}" (${d.movedCount || 0} ${label} moved)`, 'success');
        await this.loadTags();
      } catch (e) {
        this.renameError = e.message;
      } finally {
        this.renameBusy = false;
      }
    },

    openBatchRename() {
      if (this.tagsSelected.size < 2) return;
      this.batchRenameTargets = this.tags
        .filter(t => this.tagsSelected.has(t.id))
        .map(t => ({ id: t.id, label: t.label, usageCount: t.usageCount }));
      this.batchRenameMode = 'suffix';
      this.batchRenamePrefix = '';
      this.batchRenameSuffix = '';
      this.batchRenameFind = '';
      this.batchRenameReplace = '';
      this.batchRenameKeepOldDefinition = false;
      this.batchRenameBusy = false;
      this.batchRenameError = '';
      this.batchRenameProgress = '';
      this.showBatchRenameModal = true;
    },

    // Apply the active template to one label and return the resulting label.
    // Empty inputs collapse to no-op (returns the original label unchanged).
    batchRenameApplyTemplate(label) {
      if (this.batchRenameMode === 'prefix') {
        const p = this.batchRenamePrefix.trim();
        return p ? p + label : label;
      }
      if (this.batchRenameMode === 'suffix') {
        const s = this.batchRenameSuffix.trim();
        return s ? label + s : label;
      }
      if (this.batchRenameMode === 'replace') {
        const f = this.batchRenameFind;
        if (!f) return label;
        // Plain substring replace-all (no regex) — escape for safety
        const escaped = f.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        return label.replace(new RegExp(escaped, 'g'), this.batchRenameReplace);
      }
      return label;
    },

    // Live preview rows — one per selected tag, with the resulting label and
    // a status that drives the UI (chip + apply-disabled gate). Statuses:
    //   ok                — clean rename, no collision
    //   merge-existing    — new label matches an existing tag NOT in selection (will merge via single-rename handler)
    //   merge-batch       — two selected tags resolve to the same new label (would clobber each other)
    //   unchanged         — template produced the same label (skipped on apply)
    //   invalid           — new label fails the [a-z0-9-]+ rule or is empty
    batchRenamePreview() {
      const rows = [];
      const appType = this.currentInstanceType();
      const selectedIds = new Set(this.batchRenameTargets.map(t => t.id));
      // Tally new labels within the batch to detect dupes. Key by the
      // normalized (lowercase) form because the server will lowercase
      // on write — "Foo" + "foo" produced by the template both end up
      // as "foo" and would collide.
      const newLabelCounts = new Map();
      for (const t of this.batchRenameTargets) {
        const raw = this.batchRenameApplyTemplate(t.label);
        const v = this.tagLabelValidate(raw, appType);
        if (v.valid) {
          newLabelCounts.set(v.normalized, (newLabelCounts.get(v.normalized) || 0) + 1);
        }
      }
      for (const t of this.batchRenameTargets) {
        const raw = this.batchRenameApplyTemplate(t.label);
        const v = this.tagLabelValidate(raw, appType);
        // Display column shows the normalized form (what server
        // stores) — keeps preview honest. Falls back to the raw
        // input for empty/invalid cases so the user sees what
        // they actually typed alongside the error chip.
        const newLabel = v.valid ? v.normalized : raw;
        let status = 'ok';
        let mergeTarget = null;
        let invalidMessage = '';
        if (!v.valid) {
          status = 'invalid';
          invalidMessage = v.message;
        } else if (v.normalized === t.label.toLowerCase()) {
          status = 'unchanged';
        } else if (newLabelCounts.get(v.normalized) > 1) {
          status = 'merge-batch';
        } else {
          // Look for existing tag (outside the selection) that matches.
          const existing = this.tags.find(
            x => x.label.toLowerCase() === v.normalized && !selectedIds.has(x.id)
          );
          if (existing) {
            status = 'merge-existing';
            mergeTarget = existing;
          }
        }
        rows.push({ id: t.id, oldLabel: t.label, newLabel, status, mergeTarget, invalidMessage });
      }
      return rows;
    },

    batchRenameApplyDisabled() {
      const rows = this.batchRenamePreview();
      // Block on any hard error. Unchanged rows are silently skipped on apply.
      if (rows.some(r => r.status === 'invalid' || r.status === 'merge-batch')) return true;
      // Need at least one row that would actually change.
      if (!rows.some(r => r.status === 'ok' || r.status === 'merge-existing')) return true;
      return false;
    },

    batchRenameStatusChip(status) {
      switch (status) {
        case 'ok': return { text: 'Rename', color: 'var(--accent-green)', bg: '#0e3318' };
        case 'merge-existing': return { text: 'Merge into existing', color: 'var(--accent-orange)', bg: '#2a2414' };
        case 'merge-batch': return { text: 'Conflicts within batch', color: 'var(--accent-red)', bg: '#3d0e0a' };
        case 'unchanged': return { text: 'No change — skipped', color: 'var(--text-secondary)', bg: 'var(--bg-card)' };
        case 'invalid': return { text: 'Invalid name', color: 'var(--accent-red)', bg: '#3d0e0a' };
        default: return { text: status, color: 'var(--text-secondary)', bg: 'var(--bg-card)' };
      }
    },

    async submitBatchRename() {
      if (this.batchRenameApplyDisabled()) return;
      const rows = this.batchRenamePreview().filter(r => r.status === 'ok' || r.status === 'merge-existing');
      this.batchRenameBusy = true;
      this.batchRenameError = '';
      let renamed = 0;
      let merged = 0;
      let movedTotal = 0;
      const failures = [];
      for (let i = 0; i < rows.length; i++) {
        const row = rows[i];
        this.batchRenameProgress = `${i + 1} of ${rows.length} — ${row.oldLabel} → ${row.newLabel}`;
        try {
          const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags/rename`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              oldId: row.id,
              newLabel: row.newLabel,
              keepOldDefinition: this.batchRenameKeepOldDefinition,
            }),
          });
          const d = await r.json();
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          if (d.merged) merged++; else renamed++;
          movedTotal += d.movedCount || 0;
        } catch (e) {
          failures.push(`${row.oldLabel}: ${e.message}`);
        }
      }
      this.batchRenameBusy = false;
      this.batchRenameProgress = '';
      if (failures.length > 0) {
        this.batchRenameError = `${failures.length} of ${rows.length} renames failed:\n` + failures.join('\n');
        // Refresh anyway so partial success is reflected.
        await this.loadTags();
        return;
      }
      this.showBatchRenameModal = false;
      const parts = [];
      if (renamed > 0) parts.push(`${renamed} renamed`);
      if (merged > 0) parts.push(`${merged} merged`);
      const itemWord = this.itemLabel(movedTotal);
      this.showToast(`${parts.join(' · ')} (${movedTotal} ${itemWord} moved)`, 'success');
      this.tagsSelected = new Set();
      await this.loadTags();
    },

    openDeleteTag(t) {
      this.deleteTargets = [{ id: t.id, label: t.label, usageCount: t.usageCount, nonItemUsage: t.nonItemUsage || {} }];
      this.deleteKeepDefinition = false;
      this.deleteError = '';
      this.deleteProgress = '';
      this.deleteBusy = false;
      this.deletePreviewGroups = [];
      this.showDeleteModal = true;
      this.loadDeletePreview();
    },

    // deleteBlockedTargets returns the subset of deleteTargets that
    // have non-item references (Lists, Custom Formats, Notifications,
    // etc.). Radarr/Sonarr refuse to delete those tags — surfacing
    // them in the modal lets the user fix before clicking Delete and
    // getting a long cryptic API error.
    deleteBlockedTargets() {
      return (this.deleteTargets || []).filter(t => {
        const u = t.nonItemUsage || {};
        return Object.keys(u).length > 0;
      });
    },
    // Pretty label for the modal: "2 Lists, 1 Custom Format" etc.
    deleteBlockedSummary(target) {
      const u = (target && target.nonItemUsage) || {};
      const parts = [];
      for (const k of Object.keys(u)) {
        parts.push(u[k] + ' ' + k);
      }
      return parts.join(', ');
    },

    openBulkDelete() {
      if (this.tagsSelected.size === 0) return;
      this.deleteTargets = this.tags
        .filter(t => this.tagsSelected.has(t.id))
        .map(t => ({ id: t.id, label: t.label, usageCount: t.usageCount, nonItemUsage: t.nonItemUsage || {} }));
      this.deleteKeepDefinition = false;
      this.deleteError = '';
      this.deleteProgress = '';
      this.deleteBusy = false;
      this.deletePreviewGroups = [];
      this.showDeleteModal = true;
      this.loadDeletePreview();
    },

    async loadDeletePreview() {
      this.deletePreviewLoading = true;
      try {
        const ids = this.deleteTargets.map(t => t.id).join(',');
        const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tag-items?ids=${ids}`);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
        this.deletePreviewGroups = d || [];
      } catch (e) {
        this.deletePreviewGroups = [];
        this.deleteError = 'Preview failed: ' + e.message;
      } finally {
        this.deletePreviewLoading = false;
      }
    },

    // Flatten preview groups to rows: [{tagId, tagLabel, itemId, title}]
    deletePreviewRows() {
      const rows = [];
      for (const g of this.deletePreviewGroups) {
        for (const it of (g.items || [])) {
          rows.push({ tagId: g.tagId, tagLabel: g.label, itemId: it.id, title: it.title });
        }
      }
      return rows;
    },

    async submitDelete() {
      this.deleteBusy = true;
      this.deleteError = '';
      const keep = this.deleteKeepDefinition ? '?keepDefinition=true' : '';
      let done = 0, failed = 0, itemsRemoved = 0;
      for (const t of this.deleteTargets) {
        this.deleteProgress = `Deleting ${t.label}… (${done + failed + 1}/${this.deleteTargets.length})`;
        try {
          const r = await this.apiFetch(`/api/instances/${this.tagsInstanceId}/tags/${t.id}${keep}`, { method: 'DELETE' });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
          done++;
          itemsRemoved += d.removedFrom || 0;
        } catch (e) {
          failed++;
          this.deleteError = `${t.label}: ${e.message}`;
        }
      }
      this.deleteBusy = false;
      this.deleteProgress = '';
      if (failed === 0) {
        this.showDeleteModal = false;
        const action = this.deleteKeepDefinition ? 'Cleared' : 'Deleted';
        const itemWord = this.itemLabel(itemsRemoved);
        this.showToast(`${action} ${done} tag${done === 1 ? '' : 's'} (${itemsRemoved} ${itemWord} affected)`, 'success');
      }
      await this.loadTags();
    },

    // --- Schedules (M3d) ---

    // Loads /api/schedules into this.schedules. Called on Scan tab init
    // and from the Reload button. Soft-fail: error stays in
    // schedulesError so the table reads as empty without trapping the
    // user.
    async loadSchedules() {
      this.schedulesLoading = true;
      this.schedulesError = '';
      try {
        const r = await this.apiFetch('/api/schedules');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.schedules = (await r.json()) || [];
        this.seedLastSeenRuns();
      } catch (e) {
        this.schedules = [];
        this.schedulesError = 'Load failed: ' + (e.message || 'unknown');
      } finally {
        this.schedulesLoading = false;
      }
    },

    // After a fresh load (initial or manual Reload), seed the last-seen
    // map from current state. Without this, the first poll after a
    // page open would treat every existing history entry as "new" and
    // spam toasts. Only entries newer than what's already in the map
    // are flagged on subsequent polls.
    seedLastSeenRuns() {
      for (const sj of this.schedules) {
        const last = (sj.history || [])[(sj.history || []).length - 1];
        if (last) this.lastSeenScheduleRuns[sj.id] = last.startedAt;
      }
    },

    // Called from the background poll (every ~30s while Scan -> Run is
    // visible) and at any other moment we want to repaint without
    // showing a "loading" spinner. Same fetch path as loadSchedules
    // but quiet: no error toast, no schedulesLoading flag, just a
    // diff-and-toast pass.
    async pollSchedulesForFires() {
      try {
        const r = await this.apiFetch('/api/schedules');
        if (!r.ok) return;
        const fresh = (await r.json()) || [];
        // For every schedule, see if its latest history entry is
        // newer than what we last saw. Toast + remember the new
        // startedAt so the same fire isn't announced twice.
        for (const sj of fresh) {
          const last = (sj.history || [])[(sj.history || []).length - 1];
          if (!last) continue;
          const prev = this.lastSeenScheduleRuns[sj.id];
          if (prev !== last.startedAt) {
            // Skip the very first observation per page-load (handled
            // by seedLastSeenRuns) — only fires NEW since last poll
            // get a toast.
            if (prev !== undefined) {
              const tone = last.status === 'ok' ? 'success'
                         : last.status === 'partial' ? 'error'
                         : last.status === 'error' ? 'error'
                         : 'success';
              const summary = last.summary ? ' — ' + last.summary : '';
              this.showToast(`Schedule "${sj.name}" finished${summary}`, tone);
            }
            this.lastSeenScheduleRuns[sj.id] = last.startedAt;
          }
        }
        this.schedules = fresh;
      } catch {
        // Network blip — try again next tick. No user-facing surface.
      }
    },

    // Starts the 30s poll while the Run sub-tab is visible. Idempotent
    // — safe to call multiple times; existing handle is reused.
    startSchedulePoll() {
      if (this.schedulePollHandle) return;
      this.schedulePollHandle = setInterval(() => this.pollSchedulesForFires(), 30000);
    },

    stopSchedulePoll() {
      if (this.schedulePollHandle) {
        clearInterval(this.schedulePollHandle);
        this.schedulePollHandle = null;
      }
    },
  };
}
