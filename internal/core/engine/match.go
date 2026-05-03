package engine

import (
	"regexp"
	"strings"
)

// MatchLocation names the field where a release-group match was found.
// Empty string means no match. The three non-empty values are kept as
// exact strings because tagarr.sh:842-849 emits them verbatim into
// debug output and Discord notifications — breaking wire-format
// compatibility would surprise users who grep their logs.
const (
	MatchLocationReleaseGroup = "releaseGroup field"
	MatchLocationSceneName    = "sceneName"
	MatchLocationRelativePath = "relativePath"
)

// MatchReleaseGroup checks whether the configured search string matches
// any of the three Arr fields, in strict priority order:
//
//  1. releaseGroup  (Arr's movieFile.releaseGroup — the metadata field)
//  2. sceneName     (Arr's movieFile.sceneName — the upload name)
//  3. relativePath  (Arr's movieFile.relativePath — on-disk filename)
//
// Ported from tagarr.sh:841-850. First match in the priority order
// wins; subsequent fields are not consulted. All inputs must be
// lowercased by the caller (bash achieves this via jq ascii_downcase
// at tagarr.sh:804-806); this function does not lowercase again.
//
// The word boundary (\b) around the search term is load-bearing: it's
// what prevents "sic" from matching inside "jurassic", "play" inside
// "gameplay", or "flux" inside "influx". Regex metacharacters in the
// search string are escaped with regexp.QuoteMeta — a user who put
// "26.2" as a search term won't accidentally match "26X2" or similar
// via a literal-dot-as-any-char confusion.
//
// Returns (true, location) on match or (false, "") otherwise. An empty
// search string never matches.
func MatchReleaseGroup(releaseGroup, sceneName, relativePath, search string) (bool, string) {
	search = strings.TrimSpace(search)
	if search == "" {
		return false, ""
	}
	pattern := `\b` + regexp.QuoteMeta(search) + `\b`
	re, err := regexp.Compile(pattern)
	if err != nil {
		// Unreachable under QuoteMeta; keep for defence in depth so a
		// future refactor that loosens QuoteMeta doesn't silently flip
		// the match to true on a bad pattern.
		return false, ""
	}
	if releaseGroup != "" && re.MatchString(releaseGroup) {
		return true, MatchLocationReleaseGroup
	}
	if sceneName != "" && re.MatchString(sceneName) {
		return true, MatchLocationSceneName
	}
	if relativePath != "" && re.MatchString(relativePath) {
		return true, MatchLocationRelativePath
	}
	return false, ""
}
