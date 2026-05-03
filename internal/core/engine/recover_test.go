package engine

import (
	"testing"
	"time"
)

// ============================================================================
// ParseReleaseGroupFromFilename
// ============================================================================

func TestParseReleaseGroupFromFilename_BashHeaderExamples(t *testing.T) {
	// Mirrors the docstring examples in tagarr_recover.sh:280-283. Expected
	// outcomes are byte-for-byte from the bash comments — these are the
	// canonical test vectors for filename extraction.
	cases := []struct {
		filename string
		want     string
		ok       bool
	}{
		{"Movie.Name.2024.WEB-DL.h265-MyGroup.mkv", "MyGroup", true},
		{"Movie Name 2024 WEB-DL h265-MyGroup.mkv", "MyGroup", true},
		{"Movie.Name.2024.WEBDL-2160p.DTS-HD.MA.7.1.h265.mkv", "", false},
		{"Movie.Name.2024.WEB-DL.DTS-HD.MA.7.1.H.265.mkv", "", false},
	}
	for _, c := range cases {
		t.Run(c.filename, func(t *testing.T) {
			got, ok, _ := ParseReleaseGroupFromFilename(c.filename)
			if ok != c.ok || got != c.want {
				t.Fatalf("got (%q, %t), want (%q, %t)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParseReleaseGroupFromFilename_RejectionReasons(t *testing.T) {
	// Each filter clause exercised independently. The reason field is a
	// container-side improvement over bash (which silently rejected) so
	// the UI can surface "filtered: looks like resolution token".
	cases := []struct {
		filename string
		want     string
		ok       bool
		reason   FilenameRejectReason
	}{
		// no-hyphen
		{"Movie.mkv", "", false, FilenameRejectNoHyphen},
		{"", "", false, FilenameRejectNoHyphen},
		// empty (trailing hyphen)
		{"Movie-.mkv", "", false, FilenameRejectEmpty},
		// multi-token (dot) — candidate after last hyphen contains a dot.
		// Example: WEBDL-DTS-HD.MA.7.1.h265.mkv → strip .mkv →
		// last '-' splits at "DTS-HD", candidate = "HD.MA.7.1.h265"
		{"Movie.Name.2024.WEB-DL.DTS-HD.MA.7.1.h265.mkv", "", false, FilenameRejectMultiToken},
		// multi-token (space)
		{"Movie-Group With Spaces.mkv", "", false, FilenameRejectMultiToken},
		// codec rejections
		{"Movie-h264.mkv", "", false, FilenameRejectCodec},
		{"Movie-h265.mkv", "", false, FilenameRejectCodec},
		{"Movie-x264.mkv", "", false, FilenameRejectCodec},
		{"Movie-x265.mkv", "", false, FilenameRejectCodec},
		{"Movie-hevc.mkv", "", false, FilenameRejectCodec},
		{"Movie-avc.mkv", "", false, FilenameRejectCodec},
		{"Movie-vc1.mkv", "", false, FilenameRejectCodec},
		{"Movie-remux.mkv", "", false, FilenameRejectCodec},
		// case-insensitive on rejection set
		{"Movie-H265.mkv", "", false, FilenameRejectCodec},
		{"Movie-REMUX.mkv", "", false, FilenameRejectCodec},
		// split-fragment (dl/hd remnants)
		{"Movie-dl.mkv", "", false, FilenameRejectSplitFrag},
		{"Movie-hd.mkv", "", false, FilenameRejectSplitFrag},
		{"Movie-DL.mkv", "", false, FilenameRejectSplitFrag},
		// resolution
		{"Movie-1080p.mkv", "", false, FilenameRejectResolution},
		{"Movie-2160p.mkv", "", false, FilenameRejectResolution},
		{"Movie-720i.mkv", "", false, FilenameRejectResolution},
		// happy paths — ensure we don't false-reject realistic groups
		{"Movie-FLUX.mkv", "FLUX", true, ""},
		{"Movie-TheFarm.mkv", "TheFarm", true, ""},
		{"Movie-126811.mkv", "126811", true, ""}, // numeric group from a real release
		{"Movie-Group_with_underscore.mkv", "Group_with_underscore", true, ""},
		// path with directory prefix
		{"/data/movies/Movie/Movie.2024-FLUX.mkv", "FLUX", true, ""},
	}
	for _, c := range cases {
		t.Run(c.filename, func(t *testing.T) {
			got, ok, reason := ParseReleaseGroupFromFilename(c.filename)
			if ok != c.ok || got != c.want {
				t.Fatalf("got (%q, %t), want (%q, %t)", got, ok, c.want, c.ok)
			}
			if !ok && reason != c.reason {
				t.Fatalf("rejection reason: got %q, want %q", reason, c.reason)
			}
		})
	}
}

// ============================================================================
// FindImportedGrabGroup
// ============================================================================

// fixtureDate builds a monotonically-decreasing UTC time. i=0 is newest.
func fixtureDate(i int) time.Time {
	return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Hour)
}

func TestFindImportedGrabGroup_DownloadIDMatch(t *testing.T) {
	// Strategy A: grab and import share a downloadId. Newest event-pair wins.
	history := []HistoryRecord{
		{
			EventType:   HistoryEventMovieFileImported,
			Date:        fixtureDate(0),
			DownloadID:  "dl-newest",
			SourceTitle: "Movie.2024.WEB-DL.h265-FLUX",
		},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			DownloadID:   "dl-newest",
			SourceTitle:  "Movie.2024.WEB-DL.h265-FLUX",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "Movie", 2024)
	if status != RecoverFound || got != "FLUX" {
		t.Fatalf("got (%q, %v), want (FLUX, RecoverFound)", got, status)
	}
}

func TestFindImportedGrabGroup_TitleYearFallback(t *testing.T) {
	// Strategy B: downloadIds are missing on either side. title+year lock.
	history := []HistoryRecord{
		{
			EventType:   HistoryEventMovieFileImported,
			Date:        fixtureDate(0),
			SourceTitle: "Some Movie 2024 WEB-DL h265-FLUX",
		},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			SourceTitle:  "Some Movie 2024 WEB-DL h265-FLUX",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "Some Movie", 2024)
	if status != RecoverFound || got != "FLUX" {
		t.Fatalf("got (%q, %v), want (FLUX, RecoverFound)", got, status)
	}
}

func TestFindImportedGrabGroup_TitleYearFallback_LeadingArticleStripped(t *testing.T) {
	// "The Movie" → strip "the ", first word "movie".
	history := []HistoryRecord{
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0)},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			SourceTitle:  "Movie 2024 WEB-DL-FLUX",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "The Movie", 2024)
	if status != RecoverFound || got != "FLUX" {
		t.Fatalf("got (%q, %v), want (FLUX, RecoverFound)", got, status)
	}
}

func TestFindImportedGrabGroup_NoImport(t *testing.T) {
	// Only grabs in history — no import event ever fired. Bash: rc=1.
	history := []HistoryRecord{
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(0),
			SourceTitle:  "Movie 2024",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "Movie", 2024)
	if status != RecoverNoVerified || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", RecoverNoVerified)", got, status)
	}
}

func TestFindImportedGrabGroup_VerifiedEmpty(t *testing.T) {
	// Strategy A succeeded in matching the grab to the import, but the grab
	// itself has no releaseGroup. Bash: rc=2 → "no-rls-group" bucket.
	history := []HistoryRecord{
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0), DownloadID: "dl-1"},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			DownloadID:   "dl-1",
			SourceTitle:  "Movie 2024 WEB-DL",
			ReleaseGroup: "",
		},
	}
	got, status := FindImportedGrabGroup(history, "Movie", 2024)
	if status != RecoverVerifiedEmpty || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", RecoverVerifiedEmpty)", got, status)
	}
}

func TestFindImportedGrabGroup_OlderGrabIgnoredWhenDLIDMismatch(t *testing.T) {
	// An older grab matches an OLDER import — it shouldn't satisfy the
	// newest import. Bash: dl-id mismatch → continue.
	history := []HistoryRecord{
		// Newest import (dl-NEW) — has no matching grab
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0), DownloadID: "dl-NEW"},
		// Older grab + import pair — dl-OLD. Grab has releaseGroup but
		// it belongs to the previous file, not the current one.
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(1), DownloadID: "dl-OLD"},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(2),
			DownloadID:   "dl-OLD",
			SourceTitle:  "Movie 2024 WEB-DL-OLDGROUP",
			ReleaseGroup: "OLDGROUP",
		},
	}
	got, status := FindImportedGrabGroup(history, "Movie", 2024)
	if status != RecoverNoVerified || got != "" {
		t.Fatalf("older grab leaked through: got (%q, %v)", got, status)
	}
}

func TestFindImportedGrabGroup_TitleOnlyMatch_YearInvalid(t *testing.T) {
	// Year invalid (=0) — verification falls back to title-only.
	history := []HistoryRecord{
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0)},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			SourceTitle:  "MovieName whatever WEB-DL-FLUX",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "MovieName", 0)
	if status != RecoverFound || got != "FLUX" {
		t.Fatalf("got (%q, %v), want (FLUX, RecoverFound)", got, status)
	}
}

func TestFindImportedGrabGroup_YearOnlyMatch_TitleInvalid(t *testing.T) {
	// Title's first word is < 3 chars (after article strip) — skip title check.
	history := []HistoryRecord{
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0)},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			SourceTitle:  "Whatever 2024 WEB-DL-FLUX",
			ReleaseGroup: "FLUX",
		},
	}
	// Title "X" → first word "X", length < 3 → titleValid=false. Year valid.
	// Year matches → verified.
	got, status := FindImportedGrabGroup(history, "X", 2024)
	if status != RecoverFound || got != "FLUX" {
		t.Fatalf("got (%q, %v), want (FLUX, RecoverFound)", got, status)
	}
}

func TestFindImportedGrabGroup_BothInvalid_RejectsAll(t *testing.T) {
	// Both title and year are invalid (no movie metadata). No grab can
	// pass title+year fallback — must return RecoverNoVerified.
	history := []HistoryRecord{
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0)},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			SourceTitle:  "anything anywhere",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "X", 0)
	if status != RecoverNoVerified || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", RecoverNoVerified)", got, status)
	}
}

func TestFindImportedGrabGroup_YearMatchesAsWordOnly(t *testing.T) {
	// "20240101" should NOT match year 2024 — bash uses `grep -wq` (word
	// boundary). Container uses containsWholeWord helper.
	history := []HistoryRecord{
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0)},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			SourceTitle:  "Some 20240101 unrelated string",
			ReleaseGroup: "FLUX",
		},
	}
	got, status := FindImportedGrabGroup(history, "WrongTitle", 2024)
	if status == RecoverFound {
		t.Fatalf("year matched as substring: got (%q, %v)", got, status)
	}
}

func TestFindImportedGrabGroup_NewestImportPicked(t *testing.T) {
	// Multiple imports in history — newest one drives the lookup. The
	// older import would also have a matching grab, but newer takes
	// priority by definition.
	history := []HistoryRecord{
		// Newest import + matching grab
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(0), DownloadID: "dl-NEW"},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			DownloadID:   "dl-NEW",
			ReleaseGroup: "NEWGROUP",
			SourceTitle:  "Movie 2024",
		},
		// Older import + grab pair
		{EventType: HistoryEventMovieFileImported, Date: fixtureDate(2), DownloadID: "dl-OLD"},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(3),
			DownloadID:   "dl-OLD",
			ReleaseGroup: "OLDGROUP",
			SourceTitle:  "Movie 2024",
		},
	}
	got, status := FindImportedGrabGroup(history, "Movie", 2024)
	if status != RecoverFound || got != "NEWGROUP" {
		t.Fatalf("got (%q, %v), want (NEWGROUP, RecoverFound)", got, status)
	}
}

func TestFindImportedGrabGroup_EmptyHistory(t *testing.T) {
	got, status := FindImportedGrabGroup(nil, "Movie", 2024)
	if status != RecoverNoVerified || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", RecoverNoVerified)", got, status)
	}
}

func TestFindImportedGrabGroup_DownloadFolderImportedTriggersImportCheck(t *testing.T) {
	// Bash treats downloadFolderImported, movieFileImported, and
	// episodeFileImported all as imports. Verify the alternative event
	// types reach the same path.
	history := []HistoryRecord{
		{EventType: HistoryEventDownloadFolderImported, Date: fixtureDate(0), DownloadID: "dl-1"},
		{
			EventType:    HistoryEventGrabbed,
			Date:         fixtureDate(1),
			DownloadID:   "dl-1",
			ReleaseGroup: "FLUX",
			SourceTitle:  "Movie 2024",
		},
	}
	got, status := FindImportedGrabGroup(history, "Movie", 2024)
	if status != RecoverFound || got != "FLUX" {
		t.Fatalf("downloadFolderImported didn't count: got (%q, %v)", got, status)
	}
}

// containsWholeWord helper unit tests — covers the year-matching word boundary.
func TestContainsWholeWord(t *testing.T) {
	cases := []struct {
		s, needle string
		want      bool
	}{
		{"Some Movie 2024 WEB-DL", "2024", true},
		{"Some Movie 20240101 WEB-DL", "2024", false}, // bordered by digit
		{"Movie.2024.WEB-DL", "2024", true},           // bounded by '.'
		{"2024", "2024", true},                        // whole string
		{"2024foo", "2024", false},                    // bordered by letter
		{"foo2024", "2024", false},
		{"", "2024", false},
		{"2024", "", false},
	}
	for _, c := range cases {
		got := containsWholeWord(c.s, c.needle)
		if got != c.want {
			t.Errorf("containsWholeWord(%q, %q) = %t, want %t", c.s, c.needle, got, c.want)
		}
	}
}
