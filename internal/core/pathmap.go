package core

import (
	"sort"
	"strings"
)

// TranslatePath maps an Arr-API-reported path onto a path reachable
// from inside the tagarr container, using this instance's
// PathMappings. Pass-through (returns input unchanged) when no
// mapping matches OR when PathMappings is empty.
//
// See TranslatePathWithMappings for the matching rules.
func (i *Instance) TranslatePath(apiPath string) string {
	if i == nil {
		return apiPath
	}
	return TranslatePathWithMappings(apiPath, i.PathMappings)
}

// TranslatePathWithMappings is the pure version: same semantics,
// independent of Instance. Exported so callers that only have a
// `[]PathMapping` (e.g. the path-mapping panel UI's preview-as-
// you-type field) can use it directly.
//
// Matching rules (matches the analysis-doc behaviour):
//
//  1. Mappings are sorted by descending From length so a more-
//     specific entry like "/movies/4k" wins over a less-specific
//     entry like "/movies".
//
//  2. A mapping matches when apiPath equals From OR starts with
//     From + "/". This avoids the surface-level bug where "/movies"
//     would falsely match "/moviesextra" (no trailing slash check).
//
//  3. The first matching mapping replaces its From prefix with To.
//     Trailing-slash handling on From is normalised — both "/movies"
//     and "/movies/" map identically. To-side keeps whatever
//     trailing-slash the user typed (rare to want one, never
//     harmful, but we don't second-guess the user).
//
//  4. Empty From or empty To skip the mapping silently — entries
//     with one half blank are user errors during edit. Validation
//     belongs in the API handler, not here.
//
//  5. From="/" trims to empty after step 1 and gets skipped — a
//     mapping that "translates everything" is rejected silently.
//     If a future user wants this, the API handler should validate
//     and return a clear error instead of letting it slide here.
//
//  6. Relative input paths (no leading slash) won't match any
//     absolute mapping by construction — they fall through
//     unchanged. Mappings should only be configured for absolute
//     paths; relative inputs are pass-through.
//
// Empty PathMappings → return apiPath unchanged. Empty apiPath →
// also unchanged (defensive; the loop would reach the same
// conclusion but the early return reads more clearly).
func TranslatePathWithMappings(apiPath string, mappings []PathMapping) string {
	if apiPath == "" || len(mappings) == 0 {
		return apiPath
	}

	// Build a sorted-by-descending-From-length copy so stable
	// longest-prefix-first matching works without mutating the
	// caller's slice.
	prepared := make([]PathMapping, 0, len(mappings))
	for _, m := range mappings {
		from := strings.TrimRight(m.From, "/")
		if from == "" || m.To == "" {
			continue
		}
		prepared = append(prepared, PathMapping{From: from, To: m.To})
	}
	sort.SliceStable(prepared, func(a, b int) bool {
		return len(prepared[a].From) > len(prepared[b].From)
	})

	for _, m := range prepared {
		if apiPath == m.From {
			return m.To
		}
		if strings.HasPrefix(apiPath, m.From+"/") {
			// Replace just the prefix; preserve the suffix
			// (including its leading slash via the +"/" anchor we
			// matched on).
			return m.To + apiPath[len(m.From):]
		}
	}
	return apiPath
}
