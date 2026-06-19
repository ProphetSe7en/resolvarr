package engine

import "testing"

const (
	mb = int64(1) << 20
	gb = int64(1) << 30
)

// file is a tiny helper to build a TorrentFileView.
func file(name string, size int64) TorrentFileView { return TorrentFileView{Name: name, Size: size} }

// TestClassifyTorrentType_RealCases pins four real-world shapes the
// name-only classifier got wrong. Level B floor — name + files only, no
// category/path/Arr.
func TestClassifyTorrentType_RealCases(t *testing.T) {
	cases := []struct {
		label string
		name  string
		files []TorrentFileView
		want  SeClass
	}{
		{
			// #1 single episode whose name carries a bare season token +
			// absolute episode number. Name says season, 1 file → episode.
			"#1 Dr. Stone S4 - 34 (single episode)",
			"[SubsPlease] Dr. Stone S4 - 34 (1080p) [76CA3878]",
			[]TorrentFileView{file("[SubsPlease] Dr. Stone S4 - 34 (1080p) [76CA3878].mkv", 1400*mb)},
			SeEpisode,
		},
		{
			// #2 a movie. The \b fix stops "DTS5.1" matching "S5", so the
			// name is Unmatched; 1 non-numbered file → skip (not a season).
			"#2 8 Mile (movie, DTS5.1 false S5)",
			"8.Mile.2002.Open.Matte.1080p.WEB-DL.DTS5.1.H.264-spartanec163",
			[]TorrentFileView{file("8.Mile.2002.Open.Matte.1080p.WEB-DL.DTS5.1.H.264-spartanec163.mkv", 8*gb)},
			SeUnmatched,
		},
		{
			// #3 anime, absolute numbering, single file. Floor leaves this
			// Unmatched (movie-vs-episode is ambiguous without an env
			// signal; Phase 2's category booster resolves it).
			"#3 anime - 10 (single file, floor → Unmatched)",
			"[SubsPlease] Saikyou Onmyouji no Isekai Tenseiki - 10 (1080p) [423A7BDE]",
			[]TorrentFileView{file("[SubsPlease] Saikyou Onmyouji no Isekai Tenseiki - 10 (1080p) [423A7BDE].mkv", 1300*mb)},
			SeUnmatched,
		},
		{
			// #4 season pack whose TORRENT name has no S/E, but the FILES
			// inside are episode-numbered → season.
			"#4 Black Lagoon (name-less pack, numbered files)",
			"[Legion] Black Lagoon [BD x264 1080p 10bit FLAC][Dual Audio]",
			[]TorrentFileView{
				file("[Legion] Black Lagoon - 01 [BD x264 1080p 10bit FLAC][Dual Audio].mkv", 1100*mb),
				file("[Legion] Black Lagoon - 02 [BD x264 1080p 10bit FLAC][Dual Audio].mkv", 1100*mb),
				file("[Legion] Black Lagoon - 03 [BD x264 1080p 10bit FLAC][Dual Audio].mkv", 1100*mb),
			},
			SeSeason,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := ClassifyTorrentType(c.name, c.files)
			if got.Class != c.want {
				t.Errorf("ClassifyTorrentType(%q) class = %v (reason %q), want %v",
					c.name, got.Class, got.Reason, c.want)
			}
			if got.Reason == "" {
				t.Errorf("ClassifyTorrentType(%q) returned an empty reason", c.name)
			}
		})
	}
}

// TestClassifyTorrentType_FileReconciliation covers the name-vs-files
// reconciliation rows beyond the four real cases.
func TestClassifyTorrentType_FileReconciliation(t *testing.T) {
	cases := []struct {
		label string
		name  string
		files []TorrentFileView
		want  SeClass
	}{
		{
			"name episode but many files → season pack",
			"Show.S01E01.1080p.WEB-DL-FLUX",
			[]TorrentFileView{
				file("Show.S01E01.1080p.mkv", 2*gb),
				file("Show.S01E02.1080p.mkv", 2*gb),
				file("Show.S01E03.1080p.mkv", 2*gb),
			},
			SeSeason,
		},
		{
			"single multi-episode file stays episode",
			"Show.S01E01E02.1080p-FLUX",
			[]TorrentFileView{file("Show.S01E01E02.1080p.mkv", 2*gb)},
			SeEpisode,
		},
		{
			"real season pack with SxxExx files",
			"Show.S03.1080p.WEB-DL-FLUX",
			[]TorrentFileView{
				file("Show.S03E01.1080p.mkv", 2*gb),
				file("Show.S03E02.1080p.mkv", 2*gb),
			},
			SeSeason,
		},
		{
			"movie + small extra (size-excluded) stays unmatched",
			"Some.Movie.2019.1080p.BluRay.x264-GRP",
			[]TorrentFileView{
				file("Some.Movie.2019.1080p.BluRay.x264-GRP.mkv", 10*gb),
				file("extras/trailer.mkv", 60*mb),
			},
			SeUnmatched,
		},
		{
			"movie + large non-numbered featurette stays unmatched (numbered guard)",
			"Some.Movie.2019.1080p.BluRay.x264-GRP",
			[]TorrentFileView{
				file("Some.Movie.2019.1080p.BluRay.x264-GRP.mkv", 10*gb),
				file("Featurettes/behind.the.scenes.mkv", 900*mb),
			},
			SeUnmatched,
		},
		{
			"multi-season pack S01-S03 stays season",
			"Show.S01-S03.Complete.1080p.WEB-DL-FLUX",
			[]TorrentFileView{
				file("Show.S01E01.1080p.mkv", 2*gb),
				file("Show.S02E01.1080p.mkv", 2*gb),
				file("Show.S03E01.1080p.mkv", 2*gb),
			},
			SeSeason,
		},
		{
			"season name but only non-video files → falls back to name",
			"Show.S01.Complete.1080p.WEB-DL-FLUX",
			[]TorrentFileView{
				file("Show.S01.nfo", 4*mb),
				file("subs/Show.S01E01.srt", 1*mb),
			},
			SeSeason,
		},
		{
			"name-less pack with a sample mixed in → season (sample dropped)",
			"[Legion] Black Lagoon [BD x264 1080p 10bit FLAC][Dual Audio]",
			[]TorrentFileView{
				file("[Legion] Black Lagoon - 01 [BD 1080p].mkv", 1100*mb),
				file("[Legion] Black Lagoon - 02 [BD 1080p].mkv", 1100*mb),
				file("Sample/black-lagoon-sample.mkv", 25*mb),
			},
			SeSeason,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := ClassifyTorrentType(c.name, c.files); got.Class != c.want {
				t.Errorf("ClassifyTorrentType(%q) = %v (%q), want %v", c.name, got.Class, got.Reason, c.want)
			}
		})
	}
}

// TestClassifyTorrentType_SampleExclusion proves a sample file does not
// inflate the count, and that exclusion never zeroes the count out.
func TestClassifyTorrentType_SampleExclusion(t *testing.T) {
	// Episode + sample.mkv → still one real video → Episode (not Season).
	r := ClassifyTorrentType("Show.S03E05.1080p-FLUX", []TorrentFileView{
		file("Show.S03E05.1080p.mkv", 2*gb),
		file("Sample/show-sample.mkv", 30*mb),
	})
	if r.Class != SeEpisode {
		t.Errorf("episode+sample: got %v (%q), want Episode", r.Class, r.Reason)
	}

	// All files look like samples → never reduce to zero; fall back to
	// counting them so a name-less multi-sample-named pack still classifies.
	n, _ := videoFileStats([]TorrentFileView{
		file("sample-01.mkv", 40*mb),
		file("sample-02.mkv", 40*mb),
	})
	if n == 0 {
		t.Errorf("videoFileStats reduced an all-sample set to 0; must keep them")
	}
}

// TestClassifyByName_WordBoundaryFix proves the \b fix: the bare-Sxx
// season pattern no longer matches inside an audio token (DTS5.1 → S5),
// while legitimate season tokens still match.
func TestClassifyByName_WordBoundaryFix(t *testing.T) {
	cases := []struct {
		name string
		want SeClass
	}{
		{"8.Mile.2002.Open.Matte.1080p.WEB-DL.DTS5.1.H.264-spartanec163", SeUnmatched}, // the fix
		{"Show.S01.Complete.1080p.WEB-DL-FLUX", SeSeason},                              // legit bare season
		{"Show.Season.1.Complete.WEB-DL-FLUX", SeSeason},                               // worded season
		{"Show.S01E05.1080p-FLUX", SeEpisode},                                          // episode wins
		{"Movie.Title.2019.1080p.BluRay.x264-GRP", SeUnmatched},                        // plain movie
	}
	for _, c := range cases {
		if got := classifyByName(c.name); got != c.want {
			t.Errorf("classifyByName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestClassifyTorrentTypeWithHint covers the Phase 2 Arr-category booster:
// a movie hint always skips, a series hint unlocks the single-file episode
// promotion (Level C) for name-less torrents like case #3.
func TestClassifyTorrentTypeWithHint(t *testing.T) {
	cases := []struct {
		label string
		name  string
		files []TorrentFileView
		hint  ContentHint
		want  SeClass
	}{
		{
			"movie hint skips even with a real season token in the name",
			"Show.S01.Complete.1080p-FLUX",
			[]TorrentFileView{file("Show.S01.Complete.mkv", 8*gb)},
			HintMovie, SeUnmatched,
		},
		{
			"movie hint skips a plain movie",
			"Some.Movie.2019.1080p.BluRay.x264-GRP",
			[]TorrentFileView{file("Some.Movie.2019.1080p.BluRay.x264-GRP.mkv", 10*gb)},
			HintMovie, SeUnmatched,
		},
		{
			// #3 fixed: series confirmed → single name-less file = episode.
			"series hint promotes a name-less single file to episode (#3)",
			"[SubsPlease] Saikyou Onmyouji no Isekai Tenseiki - 10 (1080p) [423A7BDE]",
			[]TorrentFileView{file("[SubsPlease] Saikyou Onmyouji no Isekai Tenseiki - 10 (1080p) [423A7BDE].mkv", 1300*mb)},
			HintSeries, SeEpisode,
		},
		{
			"series hint makes a name-less multi-file a season even without numbered markers",
			"Some.Anime.Batch [BD 1080p]",
			[]TorrentFileView{
				file("Some.Anime.Batch/disc1.mkv", 2*gb),
				file("Some.Anime.Batch/disc2.mkv", 2*gb),
			},
			HintSeries, SeSeason,
		},
		{
			// HintUnknown == the floor: #3 stays Unmatched.
			"unknown hint = floor (anime single stays Unmatched)",
			"[SubsPlease] Saikyou Onmyouji no Isekai Tenseiki - 10 (1080p) [423A7BDE]",
			[]TorrentFileView{file("[SubsPlease] Saikyou Onmyouji no Isekai Tenseiki - 10 (1080p) [423A7BDE].mkv", 1300*mb)},
			HintUnknown, SeUnmatched,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := ClassifyTorrentTypeWithHint(c.name, c.files, c.hint); got.Class != c.want {
				t.Errorf("ClassifyTorrentTypeWithHint(%q, hint=%v) = %v (%q), want %v",
					c.name, c.hint, got.Class, got.Reason, c.want)
			}
		})
	}
}

// TestClassifyResult_VideoFileCount: the reported VideoFiles count is the
// meaningful episode/part count — it excludes .nfo, subtitles, and samples
// (the "48 files" Kristiania case was 24 .mkv + 24 .nfo → should read 24).
func TestClassifyResult_VideoFileCount(t *testing.T) {
	r := ClassifyTorrentType("Some.Show.Pack.No.SE.Token", []TorrentFileView{
		file("Show/Show.S01E01.mkv", 600*mb),
		file("Show/Show.S01E02.mkv", 600*mb),
		file("Show/Show.S01E03.mkv", 600*mb),
		file("Show/Show.S01E01.nfo", 4*1024),
		file("Show/Show.S01E02.nfo", 4*1024),
		file("Show/Show.S01E01.en.srt", 60*1024),
		file("Sample/sample.mkv", 20*mb),
	})
	if r.VideoFiles != 3 {
		t.Errorf("VideoFiles = %d, want 3 (nfo/srt/sample excluded)", r.VideoFiles)
	}
	if r.Class != SeSeason {
		t.Errorf("class = %v, want Season (3 episode-numbered videos)", r.Class)
	}
}

// TestClassifyTorrentType_NoFileList falls back to the name verdict when
// the file list is empty (e.g. magnet metadata not yet resolved).
func TestClassifyTorrentType_NoFileList(t *testing.T) {
	cases := []struct {
		name string
		want SeClass
	}{
		{"Show.S01E05-FLUX", SeEpisode},
		{"Show.S01.Complete-FLUX", SeSeason},
		{"Movie.2019.1080p-GRP", SeUnmatched},
	}
	for _, c := range cases {
		if got := ClassifyTorrentType(c.name, nil); got.Class != c.want {
			t.Errorf("ClassifyTorrentType(%q, nil) = %v (%q), want %v", c.name, got.Class, got.Reason, c.want)
		}
	}
}

// TestDetermineQbitTagFromClass mirrors DetermineQbitTag's enable-toggle +
// custom-name resolution, including the no-fall-through-when-disabled rule.
func TestDetermineQbitTagFromClass(t *testing.T) {
	allOn := QbitSeRulesView{
		EpisodeEnabled: true, EpisodeTag: "Episode",
		SeasonEnabled: true, SeasonTag: "Season",
		UnmatchedEnabled: true, UnmatchedTag: "Unmatched",
	}
	custom := QbitSeRulesView{
		EpisodeEnabled: true, EpisodeTag: "ep",
		SeasonEnabled: true, SeasonTag: "sn",
		UnmatchedEnabled: true, UnmatchedTag: "other",
	}
	epDisabled := allOn
	epDisabled.EpisodeEnabled = false

	cases := []struct {
		label string
		cls   SeClass
		rules QbitSeRulesView
		want  string
	}{
		{"episode all-on", SeEpisode, allOn, "Episode"},
		{"season all-on", SeSeason, allOn, "Season"},
		{"unmatched all-on", SeUnmatched, allOn, "Unmatched"},
		{"episode custom name", SeEpisode, custom, "ep"},
		{"season custom name", SeSeason, custom, "sn"},
		{"episode disabled → no tag", SeEpisode, epDisabled, ""},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := DetermineQbitTagFromClass(c.cls, c.rules); got != c.want {
				t.Errorf("DetermineQbitTagFromClass(%v) = %q, want %q", c.cls, got, c.want)
			}
		})
	}
}
