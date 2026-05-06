package engine

import "testing"

// TestAudioDetailPass_FallThroughHonestyForLossy locks the fix for the
// "G-Force shows Lossless audio for DDP5.1" bug. When AudioDetailPass
// finds no recognised lossless codec it now delegates to AudioDetailFail
// so the label reflects what's actually in the file (EAC3/DD+ / AAC /
// AC3 / no recognised codec) instead of the misleading "Lossless audio"
// generic. Matters when the audio filter is OFF — every release passes
// regardless of codec, and the result panel needs to report the real
// audio family, not a polite lie.
func TestAudioDetailPass_FallThroughHonestyForLossy(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		// Real-world G-Force example from the bug report.
		{
			name:     "G-Force DDP5.1 (lossy DD+)",
			input:    "g-force.2009.1080p.ma.web-dl.ddp5.1.h.264-azkars",
			expected: "EAC3/DD+ (lossy)",
		},
		{
			name:     "EAC3 token alone",
			input:    "movie.2024.web-dl.eac3.5.1.x264-rg",
			expected: "EAC3/DD+ (lossy)",
		},
		{
			name:     "AAC stereo",
			input:    "movie.2024.web-dl.aac2.0.x264-rg",
			expected: "AAC (lossy)",
		},
		{
			name:     "AC3 5.1",
			input:    "movie.2024.web-dl.ac3.5.1.x264-rg",
			expected: "AC3 (lossy)",
		},
		{
			name:     "no recognised audio codec",
			input:    "movie.2024.web-dl.x264-rg",
			expected: "No lossless audio",
		},
		// Lossless codecs still take precedence (regression guard).
		{
			name:     "TrueHD Atmos still labels correctly",
			input:    "movie.2024.bluray.truehd.atmos.7.1-rg",
			expected: "TrueHD Atmos",
		},
		{
			name:     "DTS:X still labels correctly",
			input:    "movie.2024.bluray.dts-x.7.1-rg",
			expected: "DTS-X",
		},
		{
			name:     "DTS-HD MA still labels correctly",
			input:    "movie.2024.bluray.dts-hd.ma.5.1-rg",
			expected: "DTS-HD.MA",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AudioDetailPass(c.input)
			if got != c.expected {
				t.Errorf("AudioDetailPass(%q) = %q, want %q", c.input, got, c.expected)
			}
		})
	}
}

// TestQualityDetailPass_FallThroughHonestyForOtherSources locks the
// matching fix on the quality side: when neither MA nor Play matches,
// fall through to QualityDetailFail so AMZN / Netflix / plain WEB / no
// WEB-DL show up in the label instead of the useless "Unknown".
func TestQualityDetailPass_FallThroughHonestyForOtherSources(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "AMZN release falls through to AMZN label",
			input:    "movie.2024.amzn.web-dl.x264-ntb",
			expected: "AMZN (not MA/Play)",
		},
		{
			name:     "Netflix release",
			input:    "movie.2024.nf.web-dl.x264-ntb",
			expected: "Netflix (not MA/Play)",
		},
		{
			name:     "plain WEB-DL with no premium prefix",
			input:    "movie.2024.web-dl.x264-rg",
			expected: "Plain WEB-DL (no MA/Play prefix)",
		},
		{
			name:     "BluRay rip — no WEB token at all",
			input:    "movie.2024.bluray.x264-rg",
			expected: "No WEB-DL source",
		},
		// Premium prefixes still take precedence (regression guard).
		{
			name:     "MA WEB-DL still labels correctly",
			input:    "movie.2024.ma.web-dl.x264-rg",
			expected: "MA WEB-DL",
		},
		{
			name:     "Play WEB-DL still labels correctly",
			input:    "movie.2024.play-webdl.x264-rg",
			expected: "Play WEB-DL",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := QualityDetailPass(c.input)
			if got != c.expected {
				t.Errorf("QualityDetailPass(%q) = %q, want %q", c.input, got, c.expected)
			}
		})
	}
}
