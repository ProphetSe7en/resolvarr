// Package engine holds the tagging decision logic, ported verbatim from the
// tagarr.sh / tagarr_import.sh bash scripts. Every regex in this package is a
// byte-for-byte copy of its bash counterpart — see filter_test.go for the
// regression suite.
//
// The engine is pure: no HTTP, no config file parsing, no globals. Callers
// build a FilterConfig and a Movie, then ask the engine to decide.
package engine

import "regexp"

// FilterConfig mirrors the bash ENABLE_* flags 1:1. Each field maps directly
// to one option in tagarr.conf / tagarr_import.conf.
type FilterConfig struct {
	// Quality master switch. When false, CheckQuality always returns true.
	Quality bool
	// MAWebDL accepts releases from the Movies Anywhere WEB-DL source.
	MAWebDL bool
	// PlayWebDL accepts releases from the Google Play WEB-DL source.
	PlayWebDL bool

	// Audio master switch. When false, CheckAudio always returns true.
	Audio bool
	// TrueHD accepts Dolby TrueHD audio (non-Atmos).
	TrueHD bool
	// TrueHDAtmos accepts Dolby TrueHD with Atmos.
	TrueHDAtmos bool
	// DTSX accepts DTS:X.
	DTSX bool
	// DTSHDMA accepts DTS-HD Master Audio.
	DTSHDMA bool
}

// DefaultFilterConfig returns the configuration equivalent to an unedited
// tagarr.conf.sample — every filter enabled.
func DefaultFilterConfig() FilterConfig {
	return FilterConfig{
		Quality:     true,
		MAWebDL:     true,
		PlayWebDL:   true,
		Audio:       true,
		TrueHD:      true,
		TrueHDAtmos: true,
		DTSX:        true,
		DTSHDMA:     true,
	}
}

// Regex patterns — byte-for-byte ports of the grep -Ei patterns in
// tagarr.sh:526-590 and tagarr_import.sh:703-770. The (?i) prefix mirrors
// grep's -i flag. Word boundaries (\b) behave identically to grep -E on the
// ASCII-only filenames these match against.
var (
	reMAWebDL     = regexp.MustCompile(`(?i)\bma(\]?\s*\[?|[._-])web([-.]?dl)?`)
	rePlayWebDL   = regexp.MustCompile(`(?i)\bplay(\]?\s*\[?|[._-])web([-.]?dl)?`)
	reAudioReject = regexp.MustCompile(`(?i)\b(upmix|encode|transcode|lossy|converted|re-?encode)\b`)
	reTrueHD      = regexp.MustCompile(`(?i)\btruehd\b`)
	reAtmos       = regexp.MustCompile(`(?i)\batmos\b`)
	reDTSX        = regexp.MustCompile(`(?i)\bdts[._-]?x\b`)
	reDTSHDMA     = regexp.MustCompile(`(?i)\bdts[._ -]?hd[._ -]?ma\b`)
)

// CheckQuality mirrors tagarr.sh::check_quality_match.
//
// Returns true when the release should be accepted on quality grounds. When
// Quality is false the function short-circuits to true (all releases pass).
// Otherwise at least one enabled source pattern must match.
func CheckQuality(cfg FilterConfig, text string) bool {
	if !cfg.Quality {
		return true
	}
	if cfg.MAWebDL && reMAWebDL.MatchString(text) {
		return true
	}
	if cfg.PlayWebDL && rePlayWebDL.MatchString(text) {
		return true
	}
	return false
}

// CheckAudio mirrors tagarr.sh::check_audio_match.
//
// Returns true when the release should be accepted on audio grounds. When
// Audio is false the function short-circuits to true. Upmix/transcode
// markers force rejection before any codec check. TrueHD branches on Atmos
// presence so the TrueHD and TrueHDAtmos flags can be toggled independently.
func CheckAudio(cfg FilterConfig, text string) bool {
	if !cfg.Audio {
		return true
	}

	// Reject modified audio first. An upmixed or transcoded release fails
	// even if it claims a lossless codec.
	if reAudioReject.MatchString(text) {
		return false
	}

	// TrueHD branch — Atmos flavor requires TrueHDAtmos, non-Atmos requires
	// TrueHD. If neither flag is on, fall through to the DTS checks.
	if (cfg.TrueHDAtmos || cfg.TrueHD) && reTrueHD.MatchString(text) {
		if reAtmos.MatchString(text) {
			if cfg.TrueHDAtmos {
				return true
			}
		} else {
			if cfg.TrueHD {
				return true
			}
		}
	}

	if cfg.DTSX && reDTSX.MatchString(text) {
		return true
	}
	if cfg.DTSHDMA && reDTSHDMA.MatchString(text) {
		return true
	}
	return false
}
