package core

import (
	"fmt"
	"strings"
	"time"
)

// plex_label_rules.go — saved-rule type for the Plex label-sync
// feature. Architectural twin of webhook_rules.go's WebhookRule and
// jobs.go's ScheduledJob: server-managed ID, per-rule history capped
// at PlexLabelHistoryCap, label-whitelist + library-target list.
//
// Triggers: scheduled job, one-off wizard run, AND webhook function
// on Connect events. All three paths instantiate the same rule and
// produce the same PlexLabelRuleRun shape so the Activity tab can
// render them uniformly.

// PlexLabelHistoryCap matches WebhookRule / ScheduledJob (7) for
// consistency in the Activity tab. Rolling window — oldest entry
// drops when the cap is exceeded.
const PlexLabelHistoryCap = 7

// PlexLabelRule is one configured Arr-tag → Plex-label sync mapping.
//
// One Arr instance + a label whitelist + ONE Plex instance + library
// list. Users with multiple Plex servers create multiple rules.
//
// Labels is the WHITELIST of Arr tag-names this rule manages on the
// Plex side. Only labels listed here are touched on Plex — manual
// Plex labels outside the whitelist are preserved. Empty list =
// no-op rule (validator rejects).
//
// Targets is ONE PlexLabelTarget today; modelled as a slice so the
// single-Plex-per-rule constraint can be relaxed later without a
// schema migration. Validator enforces len(Targets) == 1.
//
// History capped at PlexLabelHistoryCap, same pattern as WebhookRule.
type PlexLabelRule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	InstanceID string `json:"instanceId"` // → Config.Instances[id] (the Arr source)
	AppType    string `json:"appType"`    // "radarr" | "sonarr" — denormalised from the linked instance for fast filter

	// Labels is the case-insensitive whitelist of Arr tag-names the rule
	// manages on Plex. Stored verbatim (case + whitespace as user typed);
	// engine compares case-insensitive at match time.
	Labels []string `json:"labels"`

	// Targets — exactly one entry today. Slice-typed for future
	// multi-Plex-per-rule relaxation without a schema change.
	Targets []PlexLabelTarget `json:"targets"`

	// RunMode — Plex-side write behaviour. "apply" performs the
	// add/remove calls; "preview" computes the diff but skips the
	// writes (dry-run mode).
	//
	// On webhook + scheduled triggers, defaults to "apply" (empty
	// string treated as apply). One-off wizard runs pass the user's
	// radio choice through.
	RunMode string `json:"runMode,omitempty"`

	// History — last PlexLabelHistoryCap runs. Rolling window.
	History []PlexLabelRuleRun `json:"history,omitempty"`
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
	PerLabel    []PlexLabelChange     `json:"perLabel,omitempty"`   // ordered list for the result-modal
	Errors      []string          `json:"errors,omitempty"`     // aggregated per-item / per-library errors (capped)
	Summary     string            `json:"summary"`              // one-line for activity row
	// Changed — true when at least one label was actually added or
	// removed. Drives the "Made changes" filter default in Recent
	// Activity, same as WebhookRuleRun.Changed. False on preview-mode
	// runs even when the diff is non-empty (no state mutated).
	Changed bool `json:"changed,omitempty"`
}

// PlexLabelChange is one (item × label × action) tuple captured by the
// engine. Action is "add" or "remove". Year may be zero for items
// where Plex didn't report a year (rare; usually older shows).
type PlexLabelChange struct {
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Label  string `json:"label"`
	Action string `json:"action"`
}

// ValidatePlexLabelRule rejects malformed rules at save-time. Returns
// a user-facing error on the first violation (alphabetical order
// where it doesn't matter).
//
// existing is the rest of the rule list (for name-uniqueness checks);
// ignoreID = the ID being updated (so a PUT of the same rule doesn't
// trip its own name).
func ValidatePlexLabelRule(rule PlexLabelRule, instances []Instance, plexes []PlexInstance, existing []PlexLabelRule, ignoreID string) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}
	rule.InstanceID = strings.TrimSpace(rule.InstanceID)
	if rule.InstanceID == "" {
		return fmt.Errorf("Arr instance is required")
	}

	// Arr instance + appType validation. AppType must match the
	// referenced instance's URL-prefix discriminator so the engine
	// can pick the right client without a runtime probe.
	var srcInstance *Instance
	for i := range instances {
		if instances[i].ID == rule.InstanceID {
			srcInstance = &instances[i]
			break
		}
	}
	if srcInstance == nil {
		return fmt.Errorf("Arr instance %q not found", rule.InstanceID)
	}
	if rule.AppType != "radarr" && rule.AppType != "sonarr" {
		return fmt.Errorf(`appType must be "radarr" or "sonarr"`)
	}
	if rule.AppType != srcInstance.Type {
		return fmt.Errorf("appType %q doesn't match instance type %q", rule.AppType, srcInstance.Type)
	}

	if len(rule.Labels) == 0 {
		return fmt.Errorf("at least one label is required")
	}
	// Reject duplicates (case-insensitive) rather than silently dedupe
	// so the UI surfaces a clear error message. Duplicate labels would
	// make the engine iterate the same label N times per item — a
	// correctness foot-gun rather than a no-op.
	seenLabels := make(map[string]struct{}, len(rule.Labels))
	for i, lbl := range rule.Labels {
		trimmed := strings.TrimSpace(lbl)
		if trimmed == "" {
			return fmt.Errorf("labels[%d] cannot be empty", i)
		}
		key := strings.ToLower(trimmed)
		if _, dup := seenLabels[key]; dup {
			return fmt.Errorf("label %q is listed more than once", trimmed)
		}
		seenLabels[key] = struct{}{}
		rule.Labels[i] = trimmed
	}

	if len(rule.Targets) != 1 {
		return fmt.Errorf("exactly one Plex target is required")
	}
	tgt := rule.Targets[0]
	if strings.TrimSpace(tgt.PlexInstanceID) == "" {
		return fmt.Errorf("Plex instance is required")
	}
	var plex *PlexInstance
	for i := range plexes {
		if plexes[i].ID == tgt.PlexInstanceID {
			plex = &plexes[i]
			break
		}
	}
	if plex == nil {
		return fmt.Errorf("Plex instance %q not found", tgt.PlexInstanceID)
	}
	if len(tgt.LibraryKeys) == 0 {
		return fmt.Errorf("at least one Plex library is required")
	}
	// Library keys must reference cached libraries from the picked
	// Plex instance — caller refreshes the cache via /fetch-libraries
	// before opening the picker. Engine re-validates at fire-time too
	// in case Plex's library list changed in the meantime.
	cached := make(map[string]string, len(plex.Libraries))
	for _, lib := range plex.Libraries {
		cached[lib.Key] = lib.Type
	}
	wantType := plexLibraryTypeForApp(rule.AppType)
	seenKeys := make(map[string]struct{}, len(tgt.LibraryKeys))
	for _, key := range tgt.LibraryKeys {
		if _, dup := seenKeys[key]; dup {
			return fmt.Errorf("Plex library key %q is listed more than once", key)
		}
		seenKeys[key] = struct{}{}
		libType, ok := cached[key]
		if !ok {
			return fmt.Errorf("Plex library key %q not in cache — refresh libraries on the Plex instance and pick again", key)
		}
		// Radarr → movie libraries only, Sonarr → show libraries only.
		// Catches mis-click when the UI filter is bypassed (raw API
		// call, copy-paste, etc.).
		if wantType != "" && libType != wantType {
			return fmt.Errorf("library %q is type %q but rule uses %s (need %s libraries)",
				key, libType, rule.AppType, wantType)
		}
	}

	// Run-mode default + validation.
	if rule.RunMode == "" {
		rule.RunMode = "apply"
	}
	if rule.RunMode != "apply" && rule.RunMode != "preview" {
		return fmt.Errorf(`runMode must be "apply" or "preview"`)
	}

	// Name uniqueness (case-insensitive) across the rule set.
	lower := strings.ToLower(rule.Name)
	for _, other := range existing {
		if other.ID == ignoreID {
			continue
		}
		if strings.ToLower(other.Name) == lower {
			return fmt.Errorf("name %q is already used by another Plex label rule", rule.Name)
		}
	}
	return nil
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
