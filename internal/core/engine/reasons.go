package engine

import "regexp"

// Extra regexes used only for human-readable detail strings on pass/
// fail. These do NOT drive any tag decision — they only label the
// decision for Discord / debug output. Patterns are verbatim from
// tagarr.sh:876-923.
//
// Looser quality regexes (no "-dl" suffix) are used here because the
// detail detection is lenient: once we've already decided PASS/FAIL
// via the strict CheckQuality regex, the detail regex just needs to
// identify which source family matched. Missing "-dl" still reads as
// "MA WEB-DL" in human terms.
var (
	reMALoose     = regexp.MustCompile(`(?i)\bma(\]?\s*\[?|[._-])web`)
	rePlayLoose   = regexp.MustCompile(`(?i)\bplay(\]?\s*\[?|[._-])web`)
	reAMZNLoose   = regexp.MustCompile(`(?i)\bamzn(\]?\s*\[?|[._-])web`)
	reNFLoose     = regexp.MustCompile(`(?i)\bnf(\]?\s*\[?|[._-])web`)
	rePlainWeb    = regexp.MustCompile(`(?i)\bweb`)
	reTrueHDAtmos = regexp.MustCompile(`(?i)\btruehd\b.*\batmos\b|\batmos\b.*\btruehd\b`)
	// reEAC3OrDDP: catches EAC3, DD+, and DDP (Dolby Digital Plus
	// — same codec, different scene-name spelling). Trailing \b is
	// dropped so DDP5.1 / EAC3.5.1 forms match too — bash regex
	// `\beac3\b|\bdd\+` missed every channel-suffixed variant which
	// covered most real-world DD+ releases. Bumped over bash parity
	// for honesty's sake; bash never surfaced the gap because it
	// required ENABLE_AUDIO_FILTER=true so the lossy labeling path
	// was dead code there.
	reEAC3OrDDP = regexp.MustCompile(`(?i)\beac3|\bdd\+|\bddp`)
	// reAAC: same trailing-\b relaxation so AAC2.0, AAC5.1 etc.
	// match. Bash had `\baac\b` which only matched the bare "AAC"
	// token without channel suffix.
	reAAC = regexp.MustCompile(`(?i)\baac`)
	// reAC3: kept as-is. AC3.5.1 already matches because the dot
	// before the channels is a word-break.
	reAC3 = regexp.MustCompile(`(?i)\bac3\b`)
)

// QualityDetailPass identifies which source family produced a quality
// PASS so downstream display can say "MA WEB-DL" instead of just "OK".
// Ported from tagarr.sh:876-882. MA is checked before Play because the
// bash order is MA first.
//
// On fall-through (no MA/Play matched) we delegate to QualityDetailFail
// so the label reflects the file's ACTUAL source (AMZN / NF / plain
// WEB-DL / no WEB-DL) rather than a useless "Unknown". This matters
// when the Quality filter is OFF — every release passes regardless of
// source, and the user wants the result panel to show the real source
// family for each row, not a misleading "Unknown" placeholder.
func QualityDetailPass(text string) string {
	if reMALoose.MatchString(text) {
		return "MA WEB-DL"
	}
	if rePlayLoose.MatchString(text) {
		return "Play WEB-DL"
	}
	return QualityDetailFail(text)
}

// QualityDetailFail identifies the most likely reason a quality check
// failed — AMZN-sourced, Netflix-sourced, plain WEB-DL without a
// premium prefix, or no WEB-DL at all. Ported from tagarr.sh:884-894.
// Order matters: AMZN / NF are checked before the generic "plain WEB"
// fallback, otherwise every AMZN release would read "Plain WEB-DL".
func QualityDetailFail(text string) string {
	if reAMZNLoose.MatchString(text) {
		return "AMZN (not MA/Play)"
	}
	if reNFLoose.MatchString(text) {
		return "Netflix (not MA/Play)"
	}
	if rePlainWeb.MatchString(text) {
		return "Plain WEB-DL (no MA/Play prefix)"
	}
	return "No WEB-DL source"
}

// AudioDetailPass identifies which codec family produced an audio PASS.
// Ported from tagarr.sh:901-911. Order is critical here:
//  1. TrueHD Atmos — most specific (needs BOTH truehd and atmos tokens)
//  2. DTS:X        — next most specific
//  3. TrueHD       — without atmos
//  4. DTS-HD MA    — least specific lossless marker
// Checking TrueHD before "TrueHD Atmos" would mislabel every Atmos
// release as plain TrueHD.
//
// On fall-through (none of the four lossless codecs matched) we
// delegate to AudioDetailFail so the label honestly identifies what's
// in the file (EAC3/DD+ / AAC / AC3 / no recognised codec) rather
// than the misleading "Lossless audio" generic. The misleading label
// was bash-parity dead code (bash always runs with ENABLE_AUDIO_FILTER
// = true so fall-through never fires); resolvarr lets users disable
// the audio filter, which exposed every passing row to that
// fall-through and wrongly labelled lossy releases as "Lossless".
func AudioDetailPass(text string) string {
	if reTrueHDAtmos.MatchString(text) {
		return "TrueHD Atmos"
	}
	if reDTSX.MatchString(text) {
		return "DTS-X"
	}
	if reTrueHD.MatchString(text) {
		return "TrueHD"
	}
	if reDTSHDMA.MatchString(text) {
		return "DTS-HD.MA"
	}
	return AudioDetailFail(text)
}

// AudioDetailFail labels the most likely rejection reason — lossy
// codec family. Ported from tagarr.sh:913-923. "EAC3/DD+" is one
// category because Arr and the scene use the names interchangeably.
func AudioDetailFail(text string) string {
	if reEAC3OrDDP.MatchString(text) {
		return "EAC3/DD+ (lossy)"
	}
	if reAAC.MatchString(text) {
		return "AAC (lossy)"
	}
	if reAC3.MatchString(text) {
		return "AC3 (lossy)"
	}
	return "No lossless audio"
}
