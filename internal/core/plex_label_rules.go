package core

import (
	"fmt"
	"strings"
	"time"
)

// plex_label_rules.go — the Plex label-sync engine-input type +
// shared inline config.
//
// There is no persisted standalone "Plex label rule". PlexLabelRule is
// the resolved shape the engine (runPlexLabelSync) consumes; every
// trigger context (one-off run, schedule, QFA, webhook) carries a
// PlexLabelSyncConfig and synthesizes a PlexLabelRule per call via
// AsPlexLabelRule. All paths produce the same PlexLabelRuleRun so the
// Activity tab renders them uniformly.

// PlexLabelRule is the resolved input the engine consumes for one sync
// pass: ONE Arr instance + a label whitelist + ONE Plex target +
// library list. Synthesized per-call from a PlexLabelSyncConfig, never
// persisted on its own.
//
// Labels is the WHITELIST of Arr tag-names this pass manages on the
// Plex side. Only labels listed here are touched on Plex — manual
// Plex labels outside the whitelist are preserved.
//
// Targets is ONE PlexLabelTarget today; modelled as a slice so the
// single-Plex-per-pass constraint can be relaxed later without a
// schema migration.
type PlexLabelRule struct {
	InstanceID string `json:"instanceId"` // → Config.Instances[id] (the Arr source)
	AppType    string `json:"appType"`    // "radarr" | "sonarr" — denormalised from the linked instance for fast filter

	// Labels is the case-insensitive whitelist of Arr tag-names the rule
	// manages on Plex. Stored verbatim (case + whitespace as user typed);
	// engine compares case-insensitive at match time.
	Labels []string `json:"labels"`

	// LabelDisplay overrides the per-label display string written to
	// Plex. Key is the Arr tag-name as it appears in Labels[]; value
	// is the Plex-side display string (any case, spaces, punctuation
	// — Plex is permissive, unlike Radarr's lowercase-kebab tag
	// validator which enforces `^[a-z0-9-]+$`). Empty value or missing
	// key falls back to the Arr tag-name verbatim. Lets users render
	// "Atmos" on Plex even when Radarr forces the tag to "atmos".
	LabelDisplay map[string]string `json:"labelDisplay,omitempty"`

	// Targets — exactly one entry today. Slice-typed for future
	// multi-Plex-per-rule relaxation without a schema change.
	Targets []PlexLabelTarget `json:"targets"`

	// TargetTypes — which Plex metadata set(s) the rule writes to.
	// Multi-select: can contain "label" and/or "collection". Empty
	// is treated as ["label"] (backward-compatible default for rules
	// saved before this field existed).
	//
	//   "label"      — Plex Labels (Label[]). Lightweight tags,
	//                  filterable in the UI.
	//   "collection" — Plex Collections (Collection[]). Shown as
	//                  grouped views in Plex Web.
	//
	// When both are selected the engine runs two passes — one per
	// target type — and the result modal aggregates counters
	// across both. Per-item PerLabel rows carry a Target field so
	// the details list shows which side each change is for.
	TargetTypes []string `json:"targetTypes,omitempty"`

	// RunMode — Plex-side write behaviour. "apply" performs the
	// add/remove calls; "preview" computes the diff but skips the
	// writes (dry-run mode).
	//
	// On webhook + scheduled triggers, defaults to "apply" (empty
	// string treated as apply). One-off wizard runs pass the user's
	// radio choice through.
	RunMode string `json:"runMode,omitempty"`
}

// PlexLabelTarget pairs ONE Plex instance with the specific libraries
// on it to scope the rule's writes. User picks libraries from the
// PlexInstance.Libraries cache; engine validates each LibraryKey
// against the live library list at fire-time (in case the user
// deleted libraries in Plex between save + fire).
type PlexLabelTarget struct {
	PlexInstanceID string   `json:"plexInstanceId"`
	LibraryKeys    []string `json:"libraryKeys"`
}

// PlexLabelRuleRun summarises one rule fire. Shape modelled on
// WebhookRuleRun / JobRun so the Activity tab renderer can treat
// all three uniformly.
//
// Status semantics:
//   - "ok"      every targeted library scanned + all writes succeeded
//   - "partial" one or more per-item errors (Plex 4xx, match miss
//               where the user expected a hit, etc.) — diff still
//               applied to matched items
//   - "error"   couldn't fire at all (Plex unreachable, Arr instance
//               missing, no libraries matched, etc.)
//
// Added + Removed are per-label counts so the row can render "4k: +12,
// atmos: +3 / -1" without unpacking the full PerLabel slice. PerLabel
// holds the detailed (label, action, item-title, item-year) list for
// the result-modal drilldown.
type PlexLabelRuleRun struct {
	StartedAt   time.Time         `json:"startedAt"`
	DurationMs  int64             `json:"durationMs"`
	Status      string            `json:"status"`
	Trigger     string            `json:"trigger"`              // "scheduled" | "webhook" | "manual"
	RunMode     string            `json:"runMode,omitempty"`    // "apply" | "preview"
	ItemsTotal  int               `json:"itemsTotal"`           // Plex items scanned (across all target libraries)
	Matched     int               `json:"matched"`              // matched to an Arr media via 4-tier fallback
	Unmatched   int               `json:"unmatched"`            // no match (graceful — logged + skipped)
	Added       map[string]int    `json:"added,omitempty"`      // label → count of items label was added to
	Removed     map[string]int    `json:"removed,omitempty"`    // label → count of items label was removed from
	// InSync counts the per-label items where the whitelist label is
	// already correctly applied on both sides (Arr has the tag AND
	// Plex has the label). Lets the result modal show "FEL: +60 add,
	// 0 remove, 33 in sync" so users can verify against their known
	// totals without doing the math themselves. Key matches the
	// DISPLAY label (same key space as Added + Removed).
	InSync      map[string]int    `json:"inSync,omitempty"`
	PerLabel    []PlexLabelChange     `json:"perLabel,omitempty"`   // ordered list for the result-modal
	Errors      []string          `json:"errors,omitempty"`     // aggregated per-item / per-library errors (capped)
	Summary     string            `json:"summary"`              // one-line for activity row
	// Changed — true when at least one label was actually added or
	// removed. Drives the "Made changes" filter default in Recent
	// Activity, same as WebhookRuleRun.Changed. False on preview-mode
	// runs even when the diff is non-empty (no state mutated).
	Changed bool `json:"changed,omitempty"`
}

// PlexLabelSyncConfig is the shared inline config for a Plex label-sync
// run, carried by every trigger context that can fire one:
//
//   - WebhookRule.PlexLabelSync   — per-event sync on Connect events
//   - ScheduledJob.PlexSync       — cron-driven sync
//   - QFA Plex-sync step          — one-shot run-all dispatcher
//   - one-off run form            — Tag Library / Plex label sync tab
//
// It carries everything the engine needs minus the rule-level identity
// (name, enabled, history). The Arr instanceID + appType are supplied
// by the surrounding context (parent webhook rule, schedule, or the
// one-off request) and passed to AsPlexLabelRule at engine-call time.
//
// There is no standalone persisted "Plex label rule": persistence
// lives only on the QFA / Schedule / Webhook objects that embed this
// config, mirroring how every other Tag Library feature works.
type PlexLabelSyncConfig struct {
	// PlexInstanceID picks the Plex Media Server to write to.
	PlexInstanceID string `json:"plexInstanceId"`
	// LibraryKeys scopes the writes to specific Plex libraries on
	// the picked Plex instance. Validator + engine gate by Arr-side
	// type (Radarr → movie libs, Sonarr → show libs).
	LibraryKeys []string `json:"libraryKeys"`
	// Labels is the case-insensitive whitelist of Arr tag-names this
	// rule manages on Plex. Same semantics as PlexLabelRule.Labels.
	Labels []string `json:"labels"`
	// LabelDisplay overrides the per-label display string written to
	// Plex. Same semantics as PlexLabelRule.LabelDisplay.
	LabelDisplay map[string]string `json:"labelDisplay,omitempty"`
	// TargetTypes — "label" and/or "collection". Empty defaults to
	// "label" via the EffectiveTargetTypes helper.
	TargetTypes []string `json:"targetTypes,omitempty"`
}

// EffectiveTargetTypes mirrors PlexLabelRule's method so engine code
// that takes either type can use the same fallback default.
func (c *PlexLabelSyncConfig) EffectiveTargetTypes() []string {
	if c == nil || len(c.TargetTypes) == 0 {
		return []string{"label"}
	}
	return c.TargetTypes
}

// DisplayLabel mirrors PlexLabelRule's method — lets engine code
// look up per-tag display overrides without caring whether the
// config came from a standalone PlexLabelRule or an inline
// PlexLabelSyncConfig.
func (c *PlexLabelSyncConfig) DisplayLabel(arrTag string) string {
	if c == nil || c.LabelDisplay == nil {
		return arrTag
	}
	if v, ok := c.LabelDisplay[arrTag]; ok {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}
	return arrTag
}

// ValidatePlexLabelSyncConfig rejects malformed inline-config
// at save-time. Validator parallel to ValidatePlexLabelRule for the
// Plex-side parts (the parent webhook rule already handles instance +
// appType validation, so this only re-checks what's specific to the
// Plex side).
//
// appType is the parent webhook rule's app-type — used for the
// library-type filter (Radarr→movie / Sonarr→show).
func ValidatePlexLabelSyncConfig(c *PlexLabelSyncConfig, plexes []PlexInstance, appType string) error {
	if c == nil {
		return fmt.Errorf("Plex sync config is required when the Plex sync function is enabled")
	}
	c.PlexInstanceID = strings.TrimSpace(c.PlexInstanceID)
	if c.PlexInstanceID == "" {
		return fmt.Errorf("Plex instance is required")
	}
	var plex *PlexInstance
	for i := range plexes {
		if plexes[i].ID == c.PlexInstanceID {
			plex = &plexes[i]
			break
		}
	}
	if plex == nil {
		return fmt.Errorf("Plex instance %q not found", c.PlexInstanceID)
	}
	if len(c.LibraryKeys) == 0 {
		return fmt.Errorf("at least one Plex library is required")
	}
	if len(c.Labels) == 0 {
		return fmt.Errorf("at least one label is required")
	}
	// Dedupe + trim labels (same as standalone PlexLabelRule).
	seenLabels := make(map[string]struct{}, len(c.Labels))
	for i, lbl := range c.Labels {
		trimmed := strings.TrimSpace(lbl)
		if trimmed == "" {
			return fmt.Errorf("labels[%d] cannot be empty", i)
		}
		key := strings.ToLower(trimmed)
		if _, dup := seenLabels[key]; dup {
			return fmt.Errorf("label %q is listed more than once", trimmed)
		}
		seenLabels[key] = struct{}{}
		c.Labels[i] = trimmed
	}
	// Library-key dedupe + cache-existence + type filter.
	cached := make(map[string]string, len(plex.Libraries))
	for _, lib := range plex.Libraries {
		cached[lib.Key] = lib.Type
	}
	wantType := plexLibraryTypeForApp(appType)
	seenKeys := make(map[string]struct{}, len(c.LibraryKeys))
	for _, key := range c.LibraryKeys {
		if _, dup := seenKeys[key]; dup {
			return fmt.Errorf("Plex library key %q is listed more than once", key)
		}
		seenKeys[key] = struct{}{}
		libType, ok := cached[key]
		if !ok {
			return fmt.Errorf("Plex library key %q not in cache — refresh libraries on the Plex instance and pick again", key)
		}
		if wantType != "" && libType != wantType {
			return fmt.Errorf("library %q is type %q but rule uses %s (need %s libraries)",
				key, libType, appType, wantType)
		}
	}
	// LabelDisplay cleanup — same in-place mutation as
	// ValidatePlexLabelRule. Map mutations survive the pass-by-value
	// boundary because maps are reference types.
	if len(c.LabelDisplay) > 0 {
		labelSet := make(map[string]struct{}, len(c.Labels))
		for _, l := range c.Labels {
			labelSet[l] = struct{}{}
		}
		for k, v := range c.LabelDisplay {
			trimmedV := strings.TrimSpace(v)
			if trimmedV == "" || trimmedV == k {
				delete(c.LabelDisplay, k)
				continue
			}
			if _, ok := labelSet[k]; !ok {
				delete(c.LabelDisplay, k)
				continue
			}
			if trimmedV != v {
				c.LabelDisplay[k] = trimmedV
			}
		}
	}
	// Target-types validation. Same allowlist as standalone rule.
	if len(c.TargetTypes) > 0 {
		seenTargets := make(map[string]struct{}, len(c.TargetTypes))
		for _, t := range c.TargetTypes {
			if t != "label" && t != "collection" {
				return fmt.Errorf(`targetTypes entries must be "label" or "collection"; got %q`, t)
			}
			if _, dup := seenTargets[t]; dup {
				return fmt.Errorf("targetTypes %q is listed more than once", t)
			}
			seenTargets[t] = struct{}{}
		}
	}
	return nil
}

// AsPlexLabelRule synthesizes a PlexLabelRule from the inline
// webhook config so the existing engine entry points
// (runPlexLabelSync / runPlexLabelSyncForItem) can fire against it
// without a parallel implementation. Fields that don't apply to the
// webhook flow (Name, ID, Enabled, History, RunMode) are zero-valued;
// engine doesn't read them on the per-item path.
//
// instanceID + appType come from the parent WebhookRule so the
// synthesized rule reads as if it were a standalone rule bound to
// the same Arr.
func (c *PlexLabelSyncConfig) AsPlexLabelRule(instanceID, appType string) PlexLabelRule {
	if c == nil {
		return PlexLabelRule{}
	}
	return PlexLabelRule{
		InstanceID:   instanceID,
		AppType:      appType,
		Labels:       append([]string(nil), c.Labels...),
		LabelDisplay: cloneLabelDisplay(c.LabelDisplay),
		Targets: []PlexLabelTarget{
			{
				PlexInstanceID: c.PlexInstanceID,
				LibraryKeys:    append([]string(nil), c.LibraryKeys...),
			},
		},
		TargetTypes: append([]string(nil), c.TargetTypes...),
		// Webhook-driven flows always apply — the rule's stored
		// runMode is meaningless here (no UI to flip preview/apply
		// per webhook event). The adapter overrides runMode at
		// engine-call time anyway.
		RunMode: "apply",
	}
}

func cloneLabelDisplay(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// EffectiveTargetTypes returns the canonical list of Plex target
// types this rule writes to. Empty / missing TargetTypes defaults
// to ["label"] for backward compatibility with rules saved before
// the field landed.
func (r *PlexLabelRule) EffectiveTargetTypes() []string {
	if len(r.TargetTypes) == 0 {
		return []string{"label"}
	}
	return r.TargetTypes
}

// DisplayLabel returns the Plex-side display string for an Arr tag
// name. Looks up LabelDisplay first (key matches Labels verbatim);
// falls back to the Arr tag-name when no override is set or the
// override is empty/whitespace.
//
// Used by the engine when writing labels to Plex AND by the UI's
// "Manages: …" line on rule cards.
func (r *PlexLabelRule) DisplayLabel(arrTag string) string {
	if r.LabelDisplay == nil {
		return arrTag
	}
	if v, ok := r.LabelDisplay[arrTag]; ok {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}
	return arrTag
}

// PlexLabelChange is one (item × label × action) tuple captured by the
// engine. Action is "add" or "remove". Year may be zero for items
// where Plex didn't report a year (rare; usually older shows).
type PlexLabelChange struct {
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Label  string `json:"label"`
	Action string `json:"action"`
	// Target is the Plex-side metadata array this change writes to:
	// "label" or "collection". Lets the per-item detail row in the
	// result modal show "Add FEL as label on Abigail" vs "Add FEL
	// as collection on Bee Movie" when a rule targets both.
	Target string `json:"target,omitempty"`
}

// PlexLabelRunErrorCap + PlexLabelRunPerLabelCap bound the slices on
// pathologically large runs so the on-disk JSON shape (and the
// /api/plex-label-rules response payload) stays reasonable. A
// completely-broken run shouldn't be allowed to write a 50 MB history
// entry — we record up to the cap, then emit one cut-off marker.
const (
	PlexLabelRunErrorCap    = 50
	PlexLabelRunPerLabelCap = 500
)

// AppendError adds an error string to the run's Errors slice, capped
// at PlexLabelRunErrorCap entries (the last one becomes a cut-off
// marker so the reader knows there were more).
func (r *PlexLabelRuleRun) AppendError(s string) {
	if len(r.Errors) >= PlexLabelRunErrorCap {
		r.Errors[PlexLabelRunErrorCap-1] = "(more errors omitted — run hit the error cap)"
		return
	}
	r.Errors = append(r.Errors, s)
}

// AppendPerLabel adds a change to the PerLabel slice, capped at
// PlexLabelRunPerLabelCap. Counts in Added/Removed maps stay
// authoritative even after the detail list caps out.
func (r *PlexLabelRuleRun) AppendPerLabel(c PlexLabelChange) {
	if len(r.PerLabel) >= PlexLabelRunPerLabelCap {
		return
	}
	r.PerLabel = append(r.PerLabel, c)
}

// plexLibraryTypeForApp returns the Plex library-type that a rule of
// the given Arr type can target.
func plexLibraryTypeForApp(appType string) string {
	switch appType {
	case "radarr":
		return "movie"
	case "sonarr":
		return "show"
	}
	return ""
}
