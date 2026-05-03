package engine

import "testing"

// productionTests are one representative per unique structural pattern
// observed in the user's live Radarr library on 2026-04-18 (from
// tagarr_discovery_report.log). The original 190 report rows collapsed to
// 36 distinct structures differing in source (ma / play), audio codec and
// channel layout, HDR flag (dv / hdr10 / hdr10plus / pq), codec tag
// (h265 / hevc / x265), and edition modifier (proper / repack / theatrical /
// theatrical.cut / directors.cut / extended / extended.edition / extended.cut /
// final.cut / unrated / imax).
//
// Duplicate rows with the same token structure but different movie years
// or release groups were removed — the filter ignores both, so running
// the same structure five times does not strengthen the regression.
//
// Every structure is expected "+" (passes both CheckQuality and
// CheckAudio) under the default filter configuration. Filenames were
// anonymized with title_NNN placeholders so this file does not leak the
// user's library contents.
//
// Preserved from the original report for each case:
//   - Year context (for realism)
//   - All modifier tokens between year and source
//   - Source prefix (ma / play)
//   - Audio token (truehd, truehd.atmos, dts-x, dts-hd.ma with spacings)
//   - HDR + codec tail (dv.hdr10.h265 etc.)
//   - Release group suffix (public scene naming — not anonymized)
//
// Excluded from the default-config snapshot and placed in falsePosTests:
//
//   - title_053.1997.ma.repack2.webdl-...  — in this filename "ma" and
//     "webdl" are separated by the "repack2" token, so the quality regex
//     \bma([._-]|\]?\s*\[?)web does NOT match on the filename alone.
//     Bash classified this case "+" because it scanned the concatenated
//     string (rel + scene + rg) in which the scene name had a contiguous
//     "ma.webdl" token. Testing the regex against a filename-only input
//     correctly rejects the pattern.
var productionTests = []testCase{
	// Source + audio matrix — the 24 structural variants without edition modifiers
	{"+", "title_001.2024.ma.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10plus.h265-126811.mkv"},
	{"+", "title_002.2018.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-126811.mkv"},
	{"+", "title_003.2023.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.hevc-126811.mkv"},
	{"+", "title_004.2023.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10plus.h265-126811.mkv"},
	{"+", "title_005.2024.ma.webdl-2160p.truehd.7.1.dv.hdr10.hevc-126811.mkv"},
	{"+", "title_006.2022.ma.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10.hevc-126811.mkv"},
	{"+", "title_007.2020.ma.webdl-2160p.truehd.atmos.7.1.h265-12gaugeshotgun.mkv"},
	{"+", "title_008.2023.ma.webdl-2160p.dts-hd.ma.5.1.dv.hdr10.h265-btbn.mkv"},
	{"+", "title_009.2017.ma.webdl-2160p.dts-x.7.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_010.2017.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_011.2019.ma.webdl-2160p.dts-hd.ma.5.1.hdr10.h265-flux.mkv"},
	{"+", "title_012.1990.ma.webdl-2160p.dts-hd.ma.4.0.h265-flux.mkv"},
	{"+", "title_013.2000.ma.webdl-2160p.dts-x.7.1.hdr10.h265-flux.mkv"},
	{"+", "title_014.2024.ma.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_015.2015.ma.webdl-2160p.dts-hd.ma.7.1.hdr10.h265-flux.mkv"},
	{"+", "title_016.2024.ma.webdl-2160p.dts-hd.ma.5.1.dv.hdr10plus.h265-kae.mkv"},
	{"+", "title_017.2019.ma.webdl-2160p.truehd.atmos.7.1.hdr10.h265-thefarm.mkv"},
	{"+", "title_018.2018.ma.webdl-2160p.truehd.atmos.7.1.hdr10plus.h265-thefarm.mkv"},
	{"+", "title_019.2019.ma.webdl-2160p.truehd.atmos.7.1.pq.h265-thefarm.mkv"},
	{"+", "title_020.2017.ma.webdl-2160p.proper.dts-x.7.1.dv.hdr10.h265-vkz.mkv"},
	{"+", "title_021.2023.ma.webdl-2160p.dts-hd.ma.7.1.dv.hdr10.hevc-vox.mkv"},
	{"+", "title_022.2023.ma.webdl-2160p.dts-hd.ma.5.1.dv.hdr10.hevc-vox.mkv"},
	{"+", "title_023.2025.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10plus.hevc-vox.mkv"},
	{"+", "title_024.2018.ma.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10plus.h265-flux.mkv"},

	// Edition modifier variants — these insert tokens BEFORE the ma/play
	// source. The word boundary in \bma must still anchor correctly.
	{"+", "title_025.2019.directors.cut.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-crfw.mkv"},
	{"+", "title_026.2007.theatrical.ma.webdl-2160p.dts-hd.ma.5.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_027.2022.unrated.ma.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_028.2011.extended.cut.ma.webdl-2160p.proper.dts-hd.ma.5.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_029.2012.extended.edition.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_030.2001.extended.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
	{"+", "title_031.2013.theatrical.cut.ma.webdl-2160p.dts-hd.ma.7.1.h265-flux.mkv"},
	{"+", "title_032.2018.theatrical.cut.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-thefarm.mkv"},
	{"+", "title_033.2024.theatrical.cut.ma.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10plus.h265-126811.mkv"},
	{"+", "title_034.1982.final.cut.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-thefarm.mkv"},

	// IMAX placed between year and source — tests that word boundary on
	// \bma still matches when IMAX precedes it.
	{"+", "title_035.1984.2020.imax.ma.webdl-2160p.truehd.atmos.7.1.dv.hdr10plus.h265-thefarm.mkv"},

	// Play source variant — single observed case in production.
	{"+", "title_036.2025.play.webdl-2160p.truehd.atmos.7.1.dv.hdr10plus.x265-thefarm.mkv"},
}

// TestFiltersProductionSnapshot is the real-world regression suite: every
// case is a structural pattern the bash script tagged in production on
// 2026-04-18. If any flips to "-", the Go port has diverged from bash
// semantics and must be fixed before shipping.
func TestFiltersProductionSnapshot(t *testing.T) {
	runSuite(t, "production", DefaultFilterConfig(), productionTests)
}
