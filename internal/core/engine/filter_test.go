package engine

import "testing"

// testCase is the tuple used across every suite. Expected "+" means both
// CheckQuality and CheckAudio pass; "-" means at least one fails.
type testCase struct {
	expected string
	filename string
}

// standardTests mirrors test_filters.sh:STANDARD_TESTS verbatim — movie
// titles replaced with anonymous placeholders so this file does not leak
// specific library contents when committed to the public repository. The
// filter-relevant tokens (MA/Play, EAC3/TrueHD/Atmos/DTS, HDR flags,
// release group suffix) are preserved exactly.
var standardTests = []testCase{
	// Dot-naming anchors — the original 7 "Real files" slots
	{"-", "Sample.A.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.h265-FLUX.mkv"},
	{"+", "Sample.B.1995.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv"},
	{"+", "Sample.C.1997.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv"},
	{"+", "Sample.D.1989.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv"},
	{"+", "Sample.E.1999.MA.WEBDL-2160p.DTS-X.7.1.HDR10.h265-FLUX.mkv"},
	{"+", "Sample.F.2019.MA.WEBDL-2160p.DTS-HD.MA.7.1.HDR10.h265-FLUX.mkv"},
	{"-", "Sample.G.2024.AMZN.WEBDL-2160p.DTS-HD.MA.5.1.h265-126811.mkv"},
	// TheFarm — MA
	{"+", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"-", "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-TheFarm.mkv"},
	// TheFarm — Play
	{"+", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"-", "MOVIE.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-TheFarm.mkv"},
	// TheFarm — No source prefix (should fail quality)
	{"-", "MOVIE.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-TheFarm.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-TheFarm.mkv"},
	// FLUX — MA
	{"+", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	// FLUX — Play
	{"+", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "MOVIE.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	// FLUX — No source prefix (should fail quality)
	{"-", "MOVIE.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	// 126811 — MA
	{"+", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-126811.mkv"},
	// Discovery groups (unknown, should pass filters)
	{"+", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-rlsgrp_7.mkv"},
	{"+", "MOVIE.2023.MA.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-rlsgrp_1.mkv"},
	{"-", "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-126811.mkv"},
	// 126811 — Play
	{"+", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"+", "MOVIE.2023.Play.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-126811.mkv"},
	{"-", "MOVIE.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-126811.mkv"},
	// 126811 — No source prefix (should fail quality)
	{"-", "MOVIE.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-126811.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-126811.mkv"},
	{"-", "MOVIE.2023.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-126811.mkv"},
}

// bracketTests mirrors test_filters.sh:BRACKET_TESTS verbatim — the same
// cases rewritten in bracket naming to catch regex gaps where "MA]" or
// "MA][" separators replace the "MA." of standard naming. Titles and
// tmdb IDs are anonymized; separator structure is preserved exactly.
var bracketTests = []testCase{
	// Bracket-naming anchors — the original 7 "Real files" slots
	{"-", "Sample A (2023) {tmdb-100001} - [MA][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][h265]-FLUX.mkv"},
	{"+", "Sample B (1995) {tmdb-100002} - [MA][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][h265]-FLUX.mkv"},
	{"+", "Sample C (1997) {tmdb-100003} - [MA][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][h265]-FLUX.mkv"},
	{"+", "Sample D (1989) {tmdb-100004} - [MA][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][h265]-FLUX.mkv"},
	{"+", "Sample E (1999) {tmdb-100005} - [MA][WEBDL-2160p][DTS-X 7.1][HDR10][h265]-FLUX.mkv"},
	{"+", "Sample F (2019) {tmdb-100006} - [MA][WEBDL-2160p][DTS-HD MA 7.1][HDR10][h265]-FLUX.mkv"},
	{"-", "Sample G (2024) {tmdb-100007} - [AMZN][WEBDL-2160p][DTS-HD MA 5.1][h265]-126811.mkv"},
	// TheFarm — MA (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-TheFarm.mkv"},
	// TheFarm — Play (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-TheFarm.mkv"},
	// TheFarm — No source prefix (bracket, should fail quality)
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-TheFarm.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-TheFarm.mkv"},
	// FLUX — MA (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	// FLUX — Play (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	// FLUX — No source prefix (bracket, should fail quality)
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	// 126811 — MA (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-126811.mkv"},
	// Discovery groups (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-rlsgrp_7.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-rlsgrp_1.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [MA][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-126811.mkv"},
	// 126811 — Play (bracket)
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"+", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-126811.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [Play][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-126811.mkv"},
	// 126811 — No source prefix (bracket, should fail quality)
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][DTS-X 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][TrueHD 7.1][DV HDR10][HEVC]-126811.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][DTS-HD MA 5.1][DV HDR10][HEVC]-126811.mkv"},
	{"-", "MOVIE (2023) {tmdb-99999} - [WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-126811.mkv"},
}

// falsePosTests expands test_filters.sh:FALSE_POS_TESTS with thorough
// negative coverage across every category the engine must reject.
// Every entry expects "-" (fails at least one of quality or audio).
var falsePosTests = []testCase{
	// --- Streaming sources other than MA/Play ---
	// Lossless audio cannot rescue a non-MA/Play source — quality must fail.
	// Dot naming + bracket naming tested for each service.
	{"-", "Sample.H.2023.AMZN.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample I (2023) {tmdb-100008} - [AMZN][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.J.2023.NF.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample K (2023) {tmdb-100009} - [NF][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.L.2023.DSNP.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample M (2023) {tmdb-100010} - [DSNP][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.N.2023.HMAX.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample O (2023) {tmdb-100011} - [HMAX][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.P.2023.MAX.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample Q (2023) {tmdb-100012} - [MAX][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.R.2023.ATVP.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample S (2023) {tmdb-100013} - [ATVP][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.T.2023.HULU.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample U (2023) {tmdb-100014} - [HULU][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.V.2023.PCOK.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample W (2023) {tmdb-100015} - [PCOK][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.X.2023.PMTP.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample Y (2023) {tmdb-100016} - [PMTP][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample.Z.2023.STAN.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample AA (2023) {tmdb-100017} - [STAN][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},

	// --- Non-WEB sources ---
	// BluRay, BRRip, WEBRip (after non-MA source), DVDRip, HDTV all lack the
	// "MA/Play + WEB" combo in the quality regex. Even perfect audio fails.
	{"-", "Sample.AB.2023.BluRay-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AC.2023.BDRemux-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AD.2023.BRRip-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AE.2023.HDTV-1080p.TrueHD.Atmos.7.1.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AF.2023.DVDRip-1080p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv"},
	// WEBRip on its own without MA/Play prefix must fail.
	{"-", "Sample.AG.2023.WEBRip-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},

	// --- Lossy audio with MA/Play source ---
	// Quality passes (MA WEB-DL) but audio must reject these.
	{"-", "Sample.AH.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AI.2023.MA.WEBDL-2160p.DDP.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AJ.2023.MA.WEBDL-2160p.DD+.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AK.2023.MA.WEBDL-2160p.AC3.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AL.2023.MA.WEBDL-2160p.AAC.2.0.HEVC-FLUX.mkv"},
	{"-", "Sample.AM.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AN.2023.Play.WEBDL-2160p.DDP.5.1.HEVC-FLUX.mkv"},
	// Plain DTS (no HD MA, no :X) is not in the allow list.
	{"-", "Sample.AO.2023.MA.WEBDL-2160p.DTS.5.1.HEVC-FLUX.mkv"},
	// Bracket naming equivalents for lossy audio.
	{"-", "Sample AP (2023) {tmdb-100018} - [MA][WEBDL-2160p][EAC3 Atmos 5.1][DV HDR10][HEVC]-FLUX.mkv"},
	{"-", "Sample AQ (2023) {tmdb-100019} - [Play][WEBDL-2160p][DDP 5.1][HEVC]-FLUX.mkv"},
	{"-", "Sample AR (2023) {tmdb-100020} - [MA][WEBDL-2160p][DTS 5.1][HEVC]-FLUX.mkv"},

	// --- Upmix / transcode / re-encode rejection ---
	// Rejection happens before any positive codec check. TrueHD Atmos is
	// present in all of these, but the marker forces audio failure.
	// The audio-rejection regex is \b(upmix|encode|transcode|lossy|
	// converted|re-?encode)\b — exact word forms with word boundaries.
	// Past-tense forms (encoded / transcoded) do NOT match because \b
	// does not fire between the final 'e' and the trailing 'd'. The
	// tokens below use the exact word forms the regex recognizes.
	{"-", "Sample.AS.2023.MA.WEBDL-2160p.TrueHD.Atmos.upmix.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AT.2023.MA.WEBDL-2160p.TrueHD.Atmos.encode.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AU.2023.MA.WEBDL-2160p.TrueHD.Atmos.transcode.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AV.2023.MA.WEBDL-2160p.TrueHD.Atmos.lossy.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AW.2023.MA.WEBDL-2160p.TrueHD.Atmos.converted.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AX.2023.MA.WEBDL-2160p.TrueHD.Atmos.re-encode.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.AY.2023.MA.WEBDL-2160p.TrueHD.Atmos.reencode.7.1.DV.HDR10.HEVC-FLUX.mkv"},

	// --- Word-boundary protection — "ma" substring inside other tokens ---
	// IMAX: ends in MA (letter M before), no \b between M and A inside IMAX.
	{"-", "Sample.AZ.2023.IMAX.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample BA (2023) {tmdb-100021} - [IMAX][WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},
	// MASTER / MAGMA / DRAMA release-group suffixes — "ma" substring but
	// separator/context breaks the MA WEB-DL pattern even when WEB follows.
	{"-", "Sample.BB.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-MASTER.mkv"},
	{"-", "Sample.BC.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-DRAMA.mkv"},
	{"-", "Sample.BD.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-MAGMA.mkv"},
	// "MA" appearing purely inside audio codec name (DTS-HD.MA) with no
	// source prefix must NOT satisfy the MA quality check on its own.
	{"-", "Sample.BE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv"},

	// --- Word-boundary protection — "play" substring inside other tokens ---
	// DISPLAY / PLAYER / PLAYSTATION — the PLAY substring should not match
	// the Play WEB-DL regex because of the required word boundary.
	{"-", "Sample.BF.2023.DISPLAY.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.BG.2023.PLAYER.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},

	// --- Missing source entirely ---
	// WEBDL with no MA/Play prefix must fail quality.
	{"-", "Sample.BH.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample BI (2023) {tmdb-100022} - [WEBDL-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-FLUX.mkv"},

	// --- Combined fail: non-MA source AND lossy audio ---
	// Worst case — both filters should reject for different reasons.
	{"-", "Sample.BJ.2023.AMZN.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv"},
	{"-", "Sample.BK.2023.NF.WEBDL-2160p.DDP.5.1.HEVC-FLUX.mkv"},
	{"-", "Sample.BL.2023.BluRay-2160p.AAC.2.0.HEVC-FLUX.mkv"},

	// --- MA / WEBDL interrupted by another token ---
	// The quality regex is \bma([._-]|\]?\s*\[?)web — it requires ma
	// and web to be joined by a single separator character or a bracket
	// transition. Tokens between ma and web break the match on the
	// filename alone. (Bash classified the real file "+" because it also
	// scanned the concatenated sceneName field, which had a contiguous
	// ma.webdl token.) This case documents filter-only behavior.
	{"-", "Sample.BM.1997.ma.repack2.webdl-2160p.proper.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
	{"-", "Sample.BN.2023.ma.extra.token.webdl-2160p.truehd.atmos.7.1.dv.hdr10.h265-flux.mkv"},
}

// runSuite executes the combined "+/-" expectation mirroring test_filters.sh's
// run_test: both CheckQuality AND CheckAudio must pass for a "+" verdict.
func runSuite(t *testing.T, name string, cfg FilterConfig, cases []testCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(name+"/"+tc.filename, func(t *testing.T) {
			q := CheckQuality(cfg, tc.filename)
			a := CheckAudio(cfg, tc.filename)
			actual := "-"
			if q && a {
				actual = "+"
			}
			if actual != tc.expected {
				t.Errorf("expected %q got %q (quality=%v audio=%v)", tc.expected, actual, q, a)
			}
		})
	}
}

// TestFiltersDefaultConfig runs all three bash suites under the default
// (every flag enabled) configuration — the regression anchor that must stay
// green for the port to be accepted.
func TestFiltersDefaultConfig(t *testing.T) {
	cfg := DefaultFilterConfig()
	runSuite(t, "standard", cfg, standardTests)
	runSuite(t, "bracket", cfg, bracketTests)
	runSuite(t, "false-positive", cfg, falsePosTests)
}

// TestQualityDisabled confirms the ENABLE_QUALITY_FILTER=false short-circuit.
func TestQualityDisabled(t *testing.T) {
	cfg := DefaultFilterConfig()
	cfg.Quality = false
	// AMZN would fail with Quality on; confirm it passes with Quality off.
	if !CheckQuality(cfg, "MOVIE.2023.AMZN.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv") {
		t.Error("Quality=false should pass any input")
	}
}

// TestAudioDisabled confirms the ENABLE_AUDIO_FILTER=false short-circuit.
func TestAudioDisabled(t *testing.T) {
	cfg := DefaultFilterConfig()
	cfg.Audio = false
	// EAC3 Atmos would fail with Audio on; confirm it passes with Audio off.
	if !CheckAudio(cfg, "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.HEVC-FLUX.mkv") {
		t.Error("Audio=false should pass any input")
	}
	// Upmix should also pass when Audio is fully disabled.
	if !CheckAudio(cfg, "MOVIE.2023.MA.WEBDL.TrueHD.Atmos.upmix.HEVC-FLUX.mkv") {
		t.Error("Audio=false should pass even upmix")
	}
}

// TestOnlyMA confirms toggling MAWebDL on + PlayWebDL off rejects Play
// releases — the granular flag behavior the UI exposes.
func TestOnlyMA(t *testing.T) {
	cfg := DefaultFilterConfig()
	cfg.PlayWebDL = false
	if !CheckQuality(cfg, "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv") {
		t.Error("MA should pass when MAWebDL is on")
	}
	if CheckQuality(cfg, "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv") {
		t.Error("Play should fail when PlayWebDL is off")
	}
}

// TestOnlyPlay is the mirror of TestOnlyMA.
func TestOnlyPlay(t *testing.T) {
	cfg := DefaultFilterConfig()
	cfg.MAWebDL = false
	if CheckQuality(cfg, "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv") {
		t.Error("MA should fail when MAWebDL is off")
	}
	if !CheckQuality(cfg, "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv") {
		t.Error("Play should pass when PlayWebDL is on")
	}
}

// TestTrueHDBranching confirms Atmos and non-Atmos TrueHD can be toggled
// independently — the exact branching in tagarr.sh:562-576.
func TestTrueHDBranching(t *testing.T) {
	atmosFile := "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC-FLUX.mkv"
	plainFile := "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.HEVC-FLUX.mkv"

	// Atmos on, non-Atmos off: Atmos passes, plain TrueHD fails.
	cfg := DefaultFilterConfig()
	cfg.TrueHD = false
	cfg.DTSX = false
	cfg.DTSHDMA = false
	if !CheckAudio(cfg, atmosFile) {
		t.Error("TrueHD Atmos should pass when TrueHDAtmos=true")
	}
	if CheckAudio(cfg, plainFile) {
		t.Error("Plain TrueHD should fail when TrueHD=false")
	}

	// Non-Atmos on, Atmos off: plain TrueHD passes, Atmos TrueHD fails.
	cfg = DefaultFilterConfig()
	cfg.TrueHDAtmos = false
	cfg.DTSX = false
	cfg.DTSHDMA = false
	if !CheckAudio(cfg, plainFile) {
		t.Error("Plain TrueHD should pass when TrueHD=true")
	}
	if CheckAudio(cfg, atmosFile) {
		t.Error("TrueHD Atmos should fail when TrueHDAtmos=false")
	}
}

// TestUpmixRejection walks the rejection terms from the audio regex.
func TestUpmixRejection(t *testing.T) {
	cfg := DefaultFilterConfig()
	for _, term := range []string{"upmix", "encode", "transcode", "lossy", "converted", "re-encode", "reencode"} {
		filename := "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.HEVC.-" + term + "-FLUX.mkv"
		if CheckAudio(cfg, filename) {
			t.Errorf("term %q should trigger audio rejection", term)
		}
	}
}
