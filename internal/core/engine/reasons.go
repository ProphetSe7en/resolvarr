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
	reEAC3OrDDP   = regexp.MustCompile(`(?i)\beac3\b|\bdd\+`)
	reAAC         = regexp.MustCompile(`(?i)\baac\b`)
	reAC3         = regexp.MustCompile(`(?i)\bac3\b`)
)

// QualityDetailPass identifies which source family produced a quality
// PASS so downstream display can say "MA WEB-DL" instead of just "OK".
// Ported from tagarr.sh:876-882. MA is checked before Play because the
// bash order is MA first.
func QualityDetailPass(text string) string {
	if reMALoose.MatchString(text) {
		return "MA WEB-DL"
	}
	if rePlayLoose.MatchString(text) {
		return "Play WEB-DL"
	}
	return "Unknown"
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
	return "Lossless audio"
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
