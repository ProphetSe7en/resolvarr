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
	// Strip directory prefix and file extension. filepath.Base handles
	// both POSIX and Windows-style separators since Radarr can be running
	// on either. ext-strip via filepath.Ext is the same as bash `${filename%.*}`.
	filename := filepath.Base(relativePath)
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)

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
			if ev.ReleaseGroup == "" {
				return "", RecoverVerifiedEmpty
			}
			return ev.ReleaseGroup, RecoverFound
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
			if ev.ReleaseGroup == "" {
				return "", RecoverVerifiedEmpty
			}
			return ev.ReleaseGroup, RecoverFound
		}
	}

	// Walked all grabs, none verified.
	return "", RecoverNoVerified
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
