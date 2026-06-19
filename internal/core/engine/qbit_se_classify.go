package engine

// qbit_se_classify.go — content-aware Season/Episode classification.
//
// The name-only classifier (DetermineQbitTag) can't tell a single episode
// with absolute numbering from a season pack, can't tell a movie from a
// series, and skips packs whose torrent name carries no S/E token. This
// adds a content check: reconcile the name verdict against the torrent's
// actual file list (count of non-sample video files + per-file episode
// markers), which catches single episodes with absolute numbering, movies,
// and packs whose name carries no S/E token.
//
// Level B "floor" — uses only intrinsic data (release name + files), so it
// works regardless of how the user organises qBit (no dependency on
// category, save path, the instance, or the Arr). Phase 2 layers an
// optional Arr import-category booster on top.

import (
	"fmt"
	"regexp"
	"strings"
)

// TorrentFileView is the engine-facing subset of a qBit torrent's file
// list. Mirrors qbit.TorrentFile to keep the engine independent of the
// qbit package (same pattern as QbitSeRulesView). Name is the path
// relative to the torrent root (e.g. "Season 03/show.s03e01.mkv"), so a
// "Sample/" prefix is visible here.
type TorrentFileView struct {
	Name string
	Size int64
}

// SeClass is the content-derived classification of a torrent.
type SeClass int

const (
	// SeUnmatched: no usable S/E signal — skip. Movies land here.
	SeUnmatched SeClass = iota
	SeEpisode
	SeSeason
)

// ClassifyResult pairs the class with a human-readable reason for the
// preview "why" column (mirrors recover/reconcile reason columns).
// VideoFiles is the count of non-sample video files the decision was based
// on — the meaningful "how many episodes/parts" number, excluding subtitles
// / .nfo / samples (so callers can display it instead of the raw file total).
type ClassifyResult struct {
	Class      SeClass
	Reason     string
	VideoFiles int
}

// ContentHint is an OPTIONAL environmental signal about whether a torrent
// is a movie or a series, derived (by the caller) from the Arr
// import-category map. It is never required: HintUnknown keeps the
// name+files floor. A movie hint is authoritative (skip — never S/E tag a
// movie); a series hint rules out a movie, which lets a single name-less
// file be classified as an episode (the Level C promotion) safely.
type ContentHint int

const (
	HintUnknown ContentHint = iota // no env signal — name+files floor
	HintMovie                      // category resolves to a movie → skip
	HintSeries                     // category resolves to a series → confirm + unlock Level C
)

// sampleSizeThreshold: a video file below this AND far smaller than the
// largest video is treated as a sample/extra. Conservative — large
// enough to exclude trailers/featurettes, small enough to keep short
// episodes. Tunable on tester feedback.
const sampleSizeThreshold = 150 << 20 // 150 MiB

var (
	videoExts = map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".m2ts": true, ".ts": true,
		".wmv": true, ".mov": true, ".m4v": true, ".mpg": true, ".mpeg": true,
		".flv": true, ".webm": true,
	}
	// sampleNameRE matches a "sample" token bounded by non-letters so
	// "sample.mkv" / "Sample/clip.mkv" hit but "Resample.mkv" doesn't.
	sampleNameRE = regexp.MustCompile(`(?i)(^|[^a-z])sample([^a-z]|$)`)
	// fileEpisodeRE flags a per-file episode marker, used to confirm a
	// name-less torrent is a season pack (case #4). Conservative to dodge
	// codec/resolution false hits: SxxExx, a bare Exx, "Episode NN", or
	// the anime " - NN" form (a space/dot/underscore-delimited dash + a
	// 1-3 digit number that is itself token-bounded). It deliberately does
	// NOT match a bare 3-digit run like the "264" in "H.264".
	fileEpisodeRE = regexp.MustCompile(`(?i)(\bS\d{1,3}E\d{1,3}\b|\bE\d{1,3}\b|(?:^|[ _.])(?:ep|episode)[ _.]?\d{1,4}\b|[ _.]-[ _.]?\d{1,3}(?:[ _.(\[]|$))`)
)

// isVideoFile reports whether the path has a known video container ext.
func isVideoFile(name string) bool {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return false
	}
	return videoExts[strings.ToLower(name[dot:])]
}

// videoFileStats counts non-sample video files and how many carry an
// episode marker. Sample exclusion (by name/folder and by a tiny-and-
// much-smaller-than-largest size backstop) NEVER reduces the count to
// zero — if every video looks like a sample, count them all rather than
// drop the torrent.
func videoFileStats(files []TorrentFileView) (videoCount, episodeNumbered int) {
	var vids []TorrentFileView
	var maxSize int64
	for _, f := range files {
		if !isVideoFile(f.Name) {
			continue
		}
		vids = append(vids, f)
		if f.Size > maxSize {
			maxSize = f.Size
		}
	}
	if len(vids) == 0 {
		return 0, 0
	}

	kept := make([]TorrentFileView, 0, len(vids))
	for _, v := range vids {
		if sampleNameRE.MatchString(v.Name) {
			continue
		}
		// Size backstop: tiny AND under 10% of the largest video → a
		// trailer/featurette/extra, not a real episode.
		if v.Size > 0 && v.Size < sampleSizeThreshold && maxSize > 0 && v.Size*10 < maxSize {
			continue
		}
		kept = append(kept, v)
	}
	if len(kept) == 0 { // never let the sample filter zero out the torrent
		kept = vids
	}

	for _, v := range kept {
		if fileEpisodeRE.MatchString(v.Name) {
			episodeNumbered++
		}
	}
	return len(kept), episodeNumbered
}

// classifyByName runs the name-only regex (with the \b-fixed season
// pattern), returning the raw class with no enable-toggle resolution.
// Same ordering as DetermineQbitTag: Episode → Season → Unmatched.
func classifyByName(name string) SeClass {
	for _, p := range qbitEpisodePatterns {
		if p.MatchString(name) {
			return SeEpisode
		}
	}
	if qbitSeasonPattern.MatchString(name) {
		return SeSeason
	}
	return SeUnmatched
}

// ClassifyTorrentType is the name+files floor (no env signal). Equivalent
// to ClassifyTorrentTypeWithHint(name, files, HintUnknown).
func ClassifyTorrentType(name string, files []TorrentFileView) ClassifyResult {
	return ClassifyTorrentTypeWithHint(name, files, HintUnknown)
}

// ClassifyTorrentTypeWithHint reconciles the name verdict with the
// torrent's file contents, optionally boosted by a movie/series ContentHint
// derived from the Arr import-category map. The reconciliation table:
//
//	name Episode + many video files  → Season  (multi-ep pack mislabeled)
//	name Episode + 1 video file      → Episode
//	name Season  + many video files  → Season
//	name Season  + 1 video file      → Episode (single ep of that season)
//	name Unmatched + >=2 numbered    → Season  (name-less pack, e.g. #4)
//	name Unmatched + otherwise       → Unmatched (movies / ambiguous)
//
// Hints (optional, never required):
//   - HintMovie:  authoritative skip — a movie is never S/E tagged,
//     whatever the name says.
//   - HintSeries: rules out a movie, so a single name-less file is a
//     single episode and any multi-file is a season pack (Level C — fixes
//     absolute-numbered anime singles like case #3).
//
// With no usable file list it falls back to the name verdict.
func ClassifyTorrentTypeWithHint(name string, files []TorrentFileView, hint ContentHint) ClassifyResult {
	n, numbered := videoFileStats(files)
	// res stamps every result with the non-sample video-file count so
	// callers can display "N video files" rather than the raw file total
	// (which includes subtitles / .nfo / samples).
	res := func(c SeClass, reason string) ClassifyResult {
		return ClassifyResult{Class: c, Reason: reason, VideoFiles: n}
	}

	// A movie category is authoritative — never S/E tag a movie, even if
	// the name carries a (possibly false) season token.
	if hint == HintMovie {
		return res(SeUnmatched, "category matches a Radarr (movie) category, so not tagged as a season or episode")
	}

	nameClass := classifyByName(name)

	if n == 0 {
		switch nameClass {
		case SeEpisode:
			return res(SeEpisode, "name has an SxxExx episode token (no file list)")
		case SeSeason:
			return res(SeSeason, "name has a season token (no file list)")
		default:
			// Even a series hint can't split episode from season without a
			// file count, so leave it Unmatched.
			return res(SeUnmatched, "no season or episode token in the name (no file list)")
		}
	}

	switch nameClass {
	case SeEpisode:
		if n >= 2 {
			return res(SeSeason,
				fmt.Sprintf("name has an episode token but the torrent holds %d video files (season pack)", n))
		}
		return res(SeEpisode, "name has an SxxExx episode token, single video file")
	case SeSeason:
		if n >= 2 {
			return res(SeSeason, fmt.Sprintf("name has a season token, %d video files", n))
		}
		return res(SeEpisode,
			"name has a season token but a single video file (single episode of that season)")
	default:
		// Series confirmed by the Arr category → no movie risk, so the
		// numbered-files guard is unnecessary: a single video is one
		// episode (Level C, fixes absolute-numbered anime), many videos are
		// a season pack.
		if hint == HintSeries {
			if n >= 2 {
				return res(SeSeason,
					fmt.Sprintf("category matches a Sonarr (series) category; %d video files, so a season pack", n))
			}
			return res(SeEpisode, "category matches a Sonarr (series) category; single video file, so a single episode")
		}
		// Floor (no hint): require episode-numbered files to call it a
		// pack; otherwise skip. Known floor limitation — a rare multi-file
		// *movie* using the anime "- N" naming could land here as Season;
		// the movie hint above resolves it when the Arr category is known.
		if n >= 2 && numbered >= 2 {
			return res(SeSeason,
				fmt.Sprintf("no season or episode token in the name, but %d episode-numbered video files (season pack)", numbered))
		}
		return res(SeUnmatched, "no season or episode token and not a multi-episode pack")
	}
}

// DetermineQbitTagFromClass maps a content classification to the rule's
// configured tag, honouring each class's Enabled toggle + custom tag name
// (mirrors DetermineQbitTag's resolution). Returns "" when the matched
// class's rule is disabled — same no-fall-through contract as the
// name-only path.
func DetermineQbitTagFromClass(cls SeClass, r QbitSeRulesView) string {
	switch cls {
	case SeEpisode:
		if r.EpisodeEnabled {
			return defaultStr(r.EpisodeTag, "Episode")
		}
	case SeSeason:
		if r.SeasonEnabled {
			return defaultStr(r.SeasonTag, "Season")
		}
	default:
		if r.UnmatchedEnabled {
			return defaultStr(r.UnmatchedTag, "Unmatched")
		}
	}
	return ""
}
