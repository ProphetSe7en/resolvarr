// Package engine — recover.go
//
// Release-group recovery from grab history. Byte-for-byte port of two
// helpers from `scripts/tagarr/tagarr_recover.sh`:
//
//   ParseReleaseGroupFromFilename — bash extract_group_from_filename
//                                    (tagarr_recover.sh:284-318)
//   FindImportedGrabGroup         — bash find_imported_grab_group
//                                    (tagarr_recover.sh:343-446)
//
// Both functions are PURE (no I/O, no globals): they take inputs and return
// a verdict. The handler in internal/api/scan.go composes them with the
// arr.Client to fetch movie list + history and applies the result.
//
// Strict contract: handler does NO recovery decisions. If a future
// contributor finds themselves walking history events or parsing filenames
// in scan.go, they're writing it in the wrong place — extend recover.go
// (and add a test) instead.
package engine

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// Filename extraction
// ============================================================================

// filenameResolutionRE matches resolution tokens like 1080p, 2160p, 720i.
// Bash equivalent: `[[ "$lower" =~ ^[0-9]+(p|i)$ ]]`. The leading-digits +
// trailing-p/i form is anchored so only pure-resolution tokens hit; a
// release-group name happening to end in `p` (e.g., "MyGroup") doesn't
// match because it has letter prefix.
var filenameResolutionRE = regexp.MustCompile(`^[0-9]+[pi]$`)

// filenameRejectExact is the lowercase-token blacklist from bash
// extract_group_from_filename. Only an EXACT match disqualifies a candidate
// (codec/audio fragments left after splitting on the last hyphen).
//
// Source mapping:
//   h264|h265|x264|x265|hevc|avc|vc1|remux  → codecs
//   dl|hd                                    → tail of WEB-DL / DTS-HD splits
var filenameRejectExact = map[string]bool{
	"h264":  true,
	"h265":  true,
	"x264":  true,
	"x265":  true,
	"hevc":  true,
	"avc":   true,
	"vc1":   true,
	"remux": true,
	"dl":    true,
	"hd":    true,
}

// FilenameRejectReason categorises why a candidate was rejected. Surfaced
// to the UI as a tooltip so users understand "Movie.2024.h265.mkv → no
// group" instead of seeing a silent skip and assuming the engine missed it.
//
// Values match what we expose to JSON; keep stable.
type FilenameRejectReason string

const (
	FilenameRejectNoHyphen     FilenameRejectReason = "no-hyphen"      // base has no '-' at all
	FilenameRejectEmpty        FilenameRejectReason = "empty"          // text after last '-' is empty
	FilenameRejectMultiToken   FilenameRejectReason = "multi-token"    // candidate contains '.' or ' ' (multi-fragment leftover)
	FilenameRejectCodec        FilenameRejectReason = "codec"          // h264/h265/etc.
	FilenameRejectSplitFrag    FilenameRejectReason = "split-fragment" // dl/hd from WEB-DL / DTS-HD splits
	FilenameRejectResolution   FilenameRejectReason = "resolution"     // 1080p/2160p
)

// ParseReleaseGroupFromFilename extracts the release-group from a media
// filename's last-hyphen segment. Bash extract_group_from_filename parity.
//
// Returns (group, true, "") when a valid candidate is found, or
// ("", false, reason) explaining why the candidate (if any) was rejected.
// Empty input → ("", false, "no-hyphen") for simplicity.
//
// Examples:
//   Movie.Name.2024.WEB-DL.h265-MyGroup.mkv           → ("MyGroup", true, "")
//   Movie Name 2024 WEB-DL h265-MyGroup.mkv           → ("MyGroup", true, "")
//   Movie.Name.2024.WEBDL-2160p.DTS-HD.MA.7.1.h265.mkv→ ("", false, "multi-token")
//   Movie.Name.2024.WEB-DL.DTS-HD.MA.7.1.H.265.mkv    → ("", false, "multi-token")
//   Movie-h265.mkv                                     → ("", false, "codec")
//   Movie-1080p.mkv                                    → ("", false, "resolution")
//   Movie.mkv                                          → ("", false, "no-hyphen")
//
// Bash uses bash word-boundary on case-insensitive matching. Go: lowercase
// and exact-match the rejection set; the resolution regex is case-insensitive
// by lowercasing first.
func ParseReleaseGroupFromFilename(relativePath string) (string, bool, FilenameRejectReason) {
	if relativePath == "" {
		return "", false, FilenameRejectNoHyphen
	}
	// Strip directory prefix; use a media-extension-aware strip (NOT
	// filepath.Ext) for the suffix. filepath.Ext walks back to the
	// final dot in the entire string, which for an extensionless
	// torrent name like "Thor.2011.[...].H.265-APEX" wrongly returns
	// ".265-APEX" — eating half the name before we look for the
	// release-group hyphen. The grabRename dispatcher passes qBit's
	// torrent .Name field which is often extensionless (multi-file
	// torrents) or uses dots throughout (single-file scene-style),
	// so we only strip when the suffix is actually a media extension.
	//
	// Matches the strategy the tolerant parser already uses
	// (mediaExtRE) — the strict parser's old assumption that "input
	// always has a real filename" turned out to be false for the
	// webhook grabRename call site.
	filename := filepath.Base(relativePath)
	base := mediaExtRE.ReplaceAllString(filename, "")

	// No hyphen at all → no candidate.
	if !strings.Contains(base, "-") {
		return "", false, FilenameRejectNoHyphen
	}

	// Last-hyphen segment.
	idx := strings.LastIndex(base, "-")
	candidate := base[idx+1:]
	if candidate == "" {
		return "", false, FilenameRejectEmpty
	}

	// Multi-token rejection — bash: `[[ "$candidate" == *.* ]]` and
	// `[[ "$candidate" == *" "* ]]`. A release group is a single word.
	if strings.ContainsAny(candidate, ". ") {
		return "", false, FilenameRejectMultiToken
	}

	// Exact-match blacklist (codec / dl / hd). Always lowercase compare.
	lower := strings.ToLower(candidate)
	if filenameRejectExact[lower] {
		// Distinguish "codec" vs "split-fragment" so the UI tooltip can
		// be accurate. dl/hd are remnants of split delimiters; the rest
		// are codec / format names.
		if lower == "dl" || lower == "hd" {
			return "", false, FilenameRejectSplitFrag
		}
		return "", false, FilenameRejectCodec
	}

	// Resolution: 1080p / 2160p / 720i.
	if filenameResolutionRE.MatchString(lower) {
		return "", false, FilenameRejectResolution
	}

	// Pass — preserve original case (bash echoes "$candidate", not "$lower").
	return candidate, true, ""
}

// filenameYearRE matches a 4-digit year token like "2024" — used by the
// tolerant parser's blacklist to reject "Movie - 2024" patterns where
// the "rg candidate" is actually the year.
var filenameYearRE = regexp.MustCompile(`^(19[0-9]{2}|20[0-9]{2})$`)

// mediaExtRE strips known media extensions for the tolerant parser.
// filepath.Ext is unsafe here — it walks back to the last "." which
// for a release-title like "DTS-HD MA 5.1 DoVi - SumVision" returns
// ".1 DoVi - SumVision" because the last dot is in "5.1" and there's
// no path separator to bound the search. Strict parser only ever sees
// real filenames so its filepath.Ext call is safe; tolerant parser's
// callers pass both filenames AND release-titles (sourceTitle field on
// history records, indexer release-titles on grab events), so the
// targeted regex catches only ".mkv" / ".mp4" / etc.
var mediaExtRE = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|mov|wmv|flv|webm|ts|mpg|mpeg)$`)

// tolerantParserSourceBlacklist extends the strict parser's reject set
// for the tolerant fallback path. Real release-groups never use these
// names; anything matching here is a source/format token in disguise.
//
// Strict parser doesn't have these because its multi-token-reject
// already catches "Movie.2024.WEB.mkv" (no hyphen before WEB → no rg
// candidate at all). Tolerant parser splits on " - " which can land on
// "Movie - WEB" → candidate="WEB" → without this blacklist would be
// returned as rg.
var tolerantParserSourceBlacklist = map[string]bool{
	"web":     true,
	"webdl":   true,
	"webrip":  true,
	"bluray":  true,
	"bdrip":   true,
	"brrip":   true,
	"dvdrip":  true,
	"dvd":     true,
	"hdtv":    true,
	"hdrip":   true,
	"tv":      true,
	"proper":  true,
	"repack":  true,
	"limited": true,
	"extended": true,
	"unrated": true,
	"theatrical": true,
	"hybrid":  true,
	"imax":    true,
	"remaster": true,
	"remastered": true,
}

// ParseReleaseGroupTolerant is a fallback over ParseReleaseGroupFromFilename
// that handles the " - <RG>" pattern (space-dash-space-rgname) some
// indexers emit. The strict parser rejects this pattern as multi-token
// because the candidate after the last hyphen has a leading space.
// Examples that the strict parser misses but this one catches:
//
//   "Rango 2011 ... DoVi - SumVision"  → "SumVision"
//   "Movie 2024 ... HEVC - GROUPNAME"  → "GROUPNAME"
//
// Algorithm:
//   1. Try the strict parser first (bash-parity, well-tested).
//   2. If strict succeeds → return it.
//   3. If strict rejected with anything other than multi-token →
//      no fallback (rejected for codec / resolution / no-hyphen
//      reasons; the tolerant parser would have rejected too).
//   4. If strict rejected with multi-token AND the input contains " - ",
//      split on " - " and take the trailing segment trimmed. Re-apply
//      the same rejection rules as the strict parser (codec / resolution
//      blacklists + new year blacklist + multi-token reject).
//
// False-positive defenses (caught by the second-pass blacklist):
//
//   "Movie - 2024"               → rejected (year)
//   "Movie - WEB"                → rejected (codec/source)
//   "Movie - Director's Cut"     → rejected (multi-token, contains space)
//   "Movie 2024 - 1080p"         → rejected (resolution)
//   "Movie - 1080p - GROUP"      → "GROUP" via split-on-last; passes
//
// Used by:
//   - Recover adapter's findRecoveryGroupByDownloadID fallback
//     (when grab event's data.releaseGroup is empty but data.sourceTitle
//     contains the rg in " - <RG>" form).
//   - Grab Rename adapter's release-group resolution (when Connect
//     event's release.releaseGroup is empty but release.releaseTitle
//     has the rg in trailing position).
func ParseReleaseGroupTolerant(input string) (string, bool) {
	if rg, ok, _ := ParseReleaseGroupFromFilename(input); ok {
		return rg, true
	}
	// Strict parser failed. Apply tolerant fallback ONLY for the
	// space-dash-space pattern. Strip path + media-extension first.
	// Use mediaExtRE (not filepath.Ext) because callers pass release-
	// titles like "Movie 5.1 - SumVision" where filepath.Ext would
	// return ".1 - SumVision" — see the doc-comment on mediaExtRE.
	base := filepath.Base(input)
	base = mediaExtRE.ReplaceAllString(base, "")

	// Split on " - " (space-dash-space). Take trailing segment.
	idx := strings.LastIndex(base, " - ")
	if idx < 0 {
		return "", false
	}
	candidate := strings.TrimSpace(base[idx+3:])
	if candidate == "" {
		return "", false
	}

	// Same rejection rules as strict parser, plus year blacklist
	// (the strict parser doesn't need this because last-hyphen on
	// "Movie 2024" wouldn't produce "2024" — bash regex catches
	// resolution tokens but not bare years).
	if strings.ContainsAny(candidate, ". ") {
		// Multi-word like "Director's Cut" → reject
		return "", false
	}
	lower := strings.ToLower(candidate)
	if filenameRejectExact[lower] {
		return "", false
	}
	if filenameResolutionRE.MatchString(lower) {
		return "", false
	}
	if filenameYearRE.MatchString(candidate) {
		return "", false
	}
	if tolerantParserSourceBlacklist[lower] {
		return "", false
	}
	return candidate, true
}

// NormalizeRgSegment produces a parser-friendly variant of the indexer
// release-title for use as a qBit rename target. Combines two transforms
// on the trailing rg-segment:
//
//   1. Strip indexer-appended garbage after "-<RG>" (preserves the
//      existing bash fix for "-126811 x ATM05 @HDT18" patterns).
//   2. Normalize " - <RG>" / "- <RG>" / " -<RG>" to "-<RG>" (new — fixes
//      the Rango/Matilda failure mode where Radarr's strict filename
//      parser rejects "- SumVision" as multi-token even though bash's
//      lax presence regex matched).
//
// Single regex handles both. Anchored to end-of-string so middle-of-
// title hyphens are untouched.
//
// Examples (rg = the parsed/extracted release-group):
//
//   "Movie ... -126811 x ATM05 @HDT18", rg="126811"   → "Movie ... -126811"
//   "Rango 2011 ... DoVi - SumVision", rg="SumVision" → "Rango 2011 ... DoVi-SumVision"
//   "Movie ... -FLUX [MEGUSTA]",        rg="FLUX"     → "Movie ... -FLUX"
//   "Movie ... -FLUX",                  rg="FLUX"     → "Movie ... -FLUX" (no-op)
//   "Movie ... DoVi-SumVision",         rg="SumVision"→ "Movie ... DoVi-SumVision" (no-op)
//   "Movie 2024 - SumVision",           rg="SumVision"→ "Movie 2024-SumVision"
//
// Returns input unchanged when rg is empty or the rg-segment isn't at
// the trailing position (no-op semantics; caller decides whether
// rename is needed via separate parser-friendly check).
func NormalizeRgSegment(grabTitle, rg string) string {
	if rg == "" || grabTitle == "" {
		return grabTitle
	}
	// Match: optional whitespace + "-" + optional whitespace + <RG> +
	// optional non-alnum suffix, anchored to end-of-string. The
	// (?:[^a-zA-Z0-9].*)? captures trailing garbage that should be
	// stripped (indexer-appended IDs, brackets, status flags).
	re := regexp.MustCompile(`\s?-\s?` + regexp.QuoteMeta(rg) + `(?:[^a-zA-Z0-9].*)?$`)
	loc := re.FindStringIndex(grabTitle)
	if loc == nil {
		return grabTitle // rg not at trailing position; leave alone
	}
	return grabTitle[:loc[0]] + "-" + rg
}

// ============================================================================
// History walking
// ============================================================================

// HistoryEventType is the subset of Radarr event types we care about.
// Radarr's full enum is bigger (deleted / renamed / etc.) but recovery
// only cares about grabs and imports.
type HistoryEventType string

const (
	HistoryEventGrabbed                 HistoryEventType = "grabbed"
	HistoryEventDownloadFolderImported  HistoryEventType = "downloadFolderImported"
	HistoryEventMovieFileImported       HistoryEventType = "movieFileImported"
	HistoryEventEpisodeFileImported     HistoryEventType = "episodeFileImported"
)

// HistoryRecord is the subset of /api/v3/history/movie events FindImportedGrabGroup
// needs. Constructed by the arr.Client; engine never fetches.
type HistoryRecord struct {
	EventType    HistoryEventType
	Date         time.Time
	SourceTitle  string
	DownloadID   string // "" when Radarr didn't store one
	ReleaseGroup string // from data.releaseGroup OR data.ReleaseGroup; "" when missing
}

// RecoverStatus is the outcome bucket from FindImportedGrabGroup. Mirrors
// the three return codes in bash find_imported_grab_group:
//
//   0 = group recovered (RecoverFound)
//   1 = no verified grab (RecoverNoVerified)  → bash "failed verify" bucket
//   2 = verified grab but empty group (RecoverVerifiedEmpty) → bash "no-rls-group" bucket
//
// The handler in scan.go translates these to per-movie status strings for
// the JSON response and UI bucket-totals.
type RecoverStatus int

const (
	// RecoverFound — a grab was matched to the newest import and carried a
	// non-empty releaseGroup.
	RecoverFound RecoverStatus = iota

	// RecoverNoVerified — no import event in history, OR no grab matched
	// the newest import via downloadId or title+year. Most movies with
	// totally-stale history land here (e.g. manually-imported files).
	RecoverNoVerified

	// RecoverVerifiedEmpty — a grab matched the newest import, but its
	// releaseGroup field was empty too. This means the indexer didn't
	// have the group either — a recover pass can't help; user's only
	// option is a manual edit.
	RecoverVerifiedEmpty
)

// FindImportedGrabGroup mirrors bash find_imported_grab_group. Walks
// history newest→oldest, locates the newest import event, then walks
// again looking for the grab that produced it (downloadId match first,
// then title+year fallback). Returns the recovered releaseGroup + status.
//
// The matching rules:
//
//   Strategy A — downloadId match:
//     If both grab and import have non-empty downloadIds AND they're equal,
//     this grab produced this import. Use its releaseGroup.
//
//   Strategy B — title+year fallback:
//     Used when downloadId is missing on either side. Grabs whose
//     downloadId matches a *different* import are skipped (they belong to
//     older files). Title+year verification:
//       - Strip leading article (the/a/an) from the movie title
//       - First word of the stripped title, lowercased
//       - title-match: source-lowercase contains first-word substring (length ≥ 3)
//       - year-match: source-title contains the year as a whole-word
//       - both valid → require BOTH match
//       - only one valid → require that one to match
//
// The grab walk continues past Strategy B failures — there might be an
// older grab that matches title+year. Bash same.
//
// Sorting: we sort by Date descending. Bash uses `jq sort_by(.date) | reverse`.
// Ties (same timestamp) preserve input order in Go's stable sort.
func FindImportedGrabGroup(history []HistoryRecord, title string, year int) (string, RecoverStatus) {
	if len(history) == 0 {
		return "", RecoverNoVerified
	}

	// Sort newest-first (defensive — caller may not have sorted).
	sorted := make([]HistoryRecord, len(history))
	copy(sorted, history)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Date.After(sorted[j].Date)
	})

	// Step 1: locate newest import event. Capture its downloadId.
	newestImportDLID := ""
	foundImport := false
	for _, ev := range sorted {
		if isImport(ev.EventType) {
			newestImportDLID = ev.DownloadID // empty string is legitimate; treated as "no id"
			foundImport = true
			break
		}
	}
	if !foundImport {
		// No import in history at all (e.g. file was manually moved into
		// the library by the user without going through Radarr's import flow).
		return "", RecoverNoVerified
	}

	// Pre-compute title-fallback inputs once. Bash: lowercase, strip leading
	// article, take first word, require length ≥ 3.
	titleLower := strings.ToLower(title)
	titleStripped := stripLeadingArticle(titleLower)
	titleFirstWord := firstWord(titleStripped)
	titleValid := len(titleFirstWord) >= 3

	yearStr := ""
	yearValid := false
	if year > 0 {
		yearStr = strconv.Itoa(year)
		yearValid = true
	}

	// Step 2: walk grabs in the same newest-first order, look for a match.
	for _, ev := range sorted {
		if ev.EventType != HistoryEventGrabbed {
			continue
		}

		// Strategy A — downloadId match. Strongest signal.
		if ev.DownloadID != "" && newestImportDLID != "" && ev.DownloadID == newestImportDLID {
			if rg := extractGrabReleaseGroup(ev); rg != "" {
				return rg, RecoverFound
			}
			return "", RecoverVerifiedEmpty
		}

		// If this grab's downloadId is non-empty AND points at a *different*
		// import (i.e. != newestImport), skip it — that grab produced an
		// older file. Bash: same gate.
		if ev.DownloadID != "" && newestImportDLID != "" && ev.DownloadID != newestImportDLID {
			continue
		}

		// Strategy B — title+year fallback. At least one of them must be
		// valid (otherwise we'd accept any grab indiscriminately).
		yearMatch := false
		titleMatch := false
		if yearValid && containsWholeWord(ev.SourceTitle, yearStr) {
			yearMatch = true
		}
		if titleValid && strings.Contains(strings.ToLower(ev.SourceTitle), titleFirstWord) {
			titleMatch = true
		}

		verified := false
		if yearValid && titleValid {
			verified = yearMatch && titleMatch
		} else if yearValid || titleValid {
			verified = yearMatch || titleMatch
		}
		// Both invalid → verified stays false (no grab can match).

		if verified {
			if rg := extractGrabReleaseGroup(ev); rg != "" {
				return rg, RecoverFound
			}
			return "", RecoverVerifiedEmpty
		}
	}

	// Walked all grabs, none verified.
	return "", RecoverNoVerified
}

// extractGrabReleaseGroup pulls the release-group from a Grab history
// record with two fallback layers:
//
//  1. ev.ReleaseGroup — Arr's pre-parsed value (data.releaseGroup OR
//     data.ReleaseGroup; coalesce already done by the arr.HistoryRecord
//     adapter at fetch-time).
//  2. ParseReleaseGroupTolerant(ev.SourceTitle) — when (1) is empty
//     because Arr's parser bombed on " - <RG>" patterns (Rango/Matilda
//     class). The indexer release-title still has rg in extractable
//     form via the tolerant parser.
//
// Returns "" only when both layers fail. Mirrors the same fallback
// pattern findRecoveryGroupByDownloadID uses on the webhook side —
// single-source-of-truth for "trust Arr's parse first, fall back to
// our own when missing".
func extractGrabReleaseGroup(ev HistoryRecord) string {
	// ResolveReleaseGroup trusts Arr's pre-parsed value by default but
	// overrides it from the source-title's trailing "-RG" when Arr's
	// value is an obvious mis-parse (empty, or the leading non-Latin
	// bracket Radarr took as the group, or non-ASCII garbage). Same
	// resolution grab-rename uses — so Recover stops applying e.g.
	// "<non-Latin>" and recovers the real "UBWEB" from the name.
	return ResolveReleaseGroup(ev.ReleaseGroup, ev.SourceTitle)
}

// isImport — bash: `case "$event_type" in
//   downloadFolderImported|movieFileImported|episodeFileImported)`.
// EpisodeFileImported is included for forward-compat when Sonarr lands —
// engine helper is shared.
func isImport(t HistoryEventType) bool {
	switch t {
	case HistoryEventDownloadFolderImported,
		HistoryEventMovieFileImported,
		HistoryEventEpisodeFileImported:
		return true
	}
	return false
}

// stripLeadingArticle drops a leading "the "/"a "/"an " from a lowercase
// title. Bash: `sed -E 's/^(the|a|an) //'`.
func stripLeadingArticle(lowerTitle string) string {
	for _, prefix := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lowerTitle, prefix) {
			return lowerTitle[len(prefix):]
		}
	}
	return lowerTitle
}

// firstWord returns the first whitespace-delimited token of s. Bash awk
// '{print $1}'. Empty string in → empty string out.
func firstWord(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}

// containsWholeWord checks if s contains needle as a whole word (bounded by
// non-word characters or string ends). Bash: `grep -wq "$needle"`. Word
// boundary in grep is [^A-Za-z0-9_]; we match that.
//
// Used for year matching only — sourceTitle of "Movie 2024 WEB-DL" matches
// year 2024 as a word, but "20240101" wouldn't (would-match as substring,
// won't as word).
func containsWholeWord(s, needle string) bool {
	if needle == "" {
		return false
	}
	// Walk match positions and check character before/after for word-boundary.
	idx := 0
	for {
		pos := strings.Index(s[idx:], needle)
		if pos < 0 {
			return false
		}
		start := idx + pos
		end := start + len(needle)
		// Boundary check: char before AND char after must not be word-char.
		// (Word-char = [A-Za-z0-9_] per POSIX `\w`.)
		if (start == 0 || !isWordChar(s[start-1])) && (end == len(s) || !isWordChar(s[end])) {
			return true
		}
		idx = end
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}
