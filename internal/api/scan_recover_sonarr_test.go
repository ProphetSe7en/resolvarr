package api

import (
	"testing"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core/engine"
)

// TestFilterHistoryReattachesSeasonPackGrab locks in the season-pack
// recover fix. A season pack is grabbed once as a single season-level
// event (no per-episode episodeId) and imported as N episode files. The
// per-episode episodeId filter used to drop that grab, leaving each
// episode's import with no grab to verify against, so every episode in
// the pack falsely reported "failed-verify". filterHistoryForEpisodefile
// now re-attaches the grab that shares the import's downloadId, restoring
// the import+grab pairing FindImportedGrabGroup needs (the same pairing
// the Radarr/bash path has naturally, since it never narrows per-movie
// history).
func TestFilterHistoryReattachesSeasonPackGrab(t *testing.T) {
	grabDate := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	importDate := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)

	packGrab := arr.HistoryRecord{
		EventType:   "grabbed",
		Date:        grabDate,
		SourceTitle: "Legion.S02.1080p.WEB-DL.x264-FLUX",
		DownloadID:  "DL1",
		EpisodeID:   0, // season-level grab: no per-episode episodeId
	}
	packGrab.Data.ReleaseGroupLower = "FLUX"

	history := []arr.HistoryRecord{
		packGrab,
		{
			EventType:   "episodeFileImported",
			Date:        importDate,
			SourceTitle: "Legion.S02E05.1080p.WEB-DL.x264-FLUX",
			DownloadID:  "DL1", // same download as the pack grab
			EpisodeID:   505,
		},
	}
	ef := arr.EpisodeFile{
		RelativePath: "Season 02/Legion.S02E05.1080p.WEB-DL.x264.mkv",
		Episodes:     []arr.EpisodeRef{{ID: 505}},
	}

	filtered := filterHistoryForEpisodefile(history, ef)

	// The pack grab must be re-attached — the episodeId filter alone drops
	// it (its episodeId is 0, not 505).
	hasGrab := false
	for _, h := range filtered {
		if h.EventType == "grabbed" && h.DownloadID == "DL1" {
			hasGrab = true
		}
	}
	if !hasGrab {
		t.Fatalf("season-pack grab not re-attached to episode history; filtered=%+v", filtered)
	}

	// End-to-end: the verifier recovers the group via the downloadId match.
	eng := make([]engine.HistoryRecord, 0, len(filtered))
	for _, h := range filtered {
		eng = append(eng, engine.HistoryRecord{
			EventType:    engine.HistoryEventType(h.EventType),
			Date:         h.Date,
			SourceTitle:  h.SourceTitle,
			DownloadID:   h.DownloadID,
			ReleaseGroup: h.ReleaseGroup(),
		})
	}
	rg, status := engine.FindImportedGrabGroup(eng, "Legion", 2017)
	if status != engine.RecoverFound || rg != "FLUX" {
		t.Fatalf("recover = (%q, %v), want (\"FLUX\", RecoverFound)", rg, status)
	}
}

// TestFilterHistoryReattachesSeasonPackImport locks in the real Legion S02
// case. Sonarr recorded the season-pack import as a SINGLE
// downloadFolderImported event tagged only to the pack's first episode,
// while every episode got its own per-episode grab. Recovering any episode
// other than the first dropped that lone import (its episodeId belongs to
// E01, not this episode), leaving the episode with a grab but no import, so
// FindImportedGrabGroup's "find newest import" step failed and reported a
// false "failed-verify" for every episode after the first. The downloadId
// re-attach now pulls the sibling import back in.
func TestFilterHistoryReattachesSeasonPackImport(t *testing.T) {
	const dl = "4A186DD73358"
	const pack = "Legion.S02.1080p.AMZN.WEB-DL.DDP5.1.H.264-DEFLATE"

	e01Grab := arr.HistoryRecord{EventType: "grabbed", SourceTitle: pack, DownloadID: dl, EpisodeID: 2171}
	e01Grab.Data.ReleaseGroupLower = "DEFLATE"
	e05Grab := arr.HistoryRecord{EventType: "grabbed", SourceTitle: pack, DownloadID: dl, EpisodeID: 2175}
	e05Grab.Data.ReleaseGroupLower = "DEFLATE"

	history := []arr.HistoryRecord{
		e01Grab,
		e05Grab,
		// The whole pack imported as one event tagged to E01 only.
		{EventType: "downloadFolderImported", SourceTitle: pack, DownloadID: dl, EpisodeID: 2171},
	}
	// Recover E05 (episodeId 2175) — it has a grab but no import of its own.
	ef := arr.EpisodeFile{
		RelativePath: "Season 02/Legion.S02E05.1080p.WEB-DL.x264.mkv",
		Episodes:     []arr.EpisodeRef{{ID: 2175}},
	}

	filtered := filterHistoryForEpisodefile(history, ef)

	// The sibling import (tagged to E01) must be re-attached so the verifier
	// has an import event to anchor on.
	hasImport := false
	for _, h := range filtered {
		if h.EventType == "downloadFolderImported" && h.DownloadID == dl {
			hasImport = true
		}
	}
	if !hasImport {
		t.Fatalf("season-pack import (tagged to sibling episode) not re-attached; filtered=%+v", filtered)
	}

	eng := make([]engine.HistoryRecord, 0, len(filtered))
	for _, h := range filtered {
		eng = append(eng, engine.HistoryRecord{
			EventType:    engine.HistoryEventType(h.EventType),
			Date:         h.Date,
			SourceTitle:  h.SourceTitle,
			DownloadID:   h.DownloadID,
			ReleaseGroup: h.ReleaseGroup(),
		})
	}
	rg, status := engine.FindImportedGrabGroup(eng, "Legion", 2017)
	if status != engine.RecoverFound || rg != "DEFLATE" {
		t.Fatalf("recover = (%q, %v), want (\"DEFLATE\", RecoverFound)", rg, status)
	}
}

// TestFilterHistoryNoCrossContamination confirms the re-attach only pulls
// grabs sharing the episode's own import downloadId — a different pack's
// grab (other downloadId) must not bleed in.
func TestFilterHistoryNoCrossContamination(t *testing.T) {
	history := []arr.HistoryRecord{
		{EventType: "grabbed", SourceTitle: "Other.Pack-OTHER", DownloadID: "DL2", EpisodeID: 0},
		{EventType: "episodeFileImported", SourceTitle: "Legion.S02E05-FLUX", DownloadID: "DL1", EpisodeID: 505},
	}
	ef := arr.EpisodeFile{RelativePath: "Legion.S02E05.mkv", Episodes: []arr.EpisodeRef{{ID: 505}}}

	filtered := filterHistoryForEpisodefile(history, ef)
	for _, h := range filtered {
		if h.DownloadID == "DL2" {
			t.Fatalf("unrelated grab (DL2) leaked into episode history; filtered=%+v", filtered)
		}
	}
}
