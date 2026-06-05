package api

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_recover_sonarr.go — Sonarr equivalent of scan_recover.go's
// runRecoverRadarr. Bash parity: tagarr_recover.sh _process_sonarr.
//
// The Sonarr flow differs from Radarr in three ways that drive the
// per-series → per-episodefile structure here:
//
//   1. Identity: items in the response represent EPISODE FILES, not
//      series. A 200-episode show may have 200 affected files; each
//      gets its own row (with seriesTitle + S01E05 label rolled into
//      the displayed Title).
//
//   2. History: Sonarr's history is keyed by series, not episode-file.
//      One /history/series?seriesId=N call covers every episode in the
//      show. We filter the response client-side per-epfile using either
//      (a) episodeId match against epfile.episodes[].id, or
//      (b) sourceTitle pattern match against the epfile's "S01E05"
//          label parsed out of the relativePath. Bash same.
//
//   3. Patch: PUT /api/v3/episodefile/{id} instead of /movieFile/{id}.
//      Same read-modify-write pattern (full-object PUT preserves every
//      field Sonarr expects). Rename command uses seriesId+files (a
//      slice of episodefile IDs).
//
// Engine layer is unchanged — engine.FindImportedGrabGroup walks any
// HistoryRecord set the same way regardless of Arr type.

// epLabelRe matches the S<season>E<episode>(-E<n>)* pattern in Sonarr
// relativePaths so we can build a human-readable per-episode label
// AND do sourceTitle-fallback filtering on series history. Single
// expression captures the leading S##E## plus any multi-episode
// continuation (S01E05E06 / S01E05-E06). Compiled once.
var epLabelRe = regexp.MustCompile(`(?i)S\d+E\d+(?:[E-]\d+)*`)

func (s *Server) runRecoverSonarr(ctx context.Context, inst *core.Instance, req scanRunRequest) (*scanResponse, *apiError) {
	client := s.arrClientFor(inst)
	series, err := client.ListItems(ctx, "sonarr")
	if err != nil {
		return nil, newAPIError(502, "arr list series: "+err.Error())
	}

	// Per-instance exclusion list — user-flagged series + per-season
	// skips. Whole-series exclusions skip the per-series epfile fetch
	// entirely (saves an API call); per-season exclusions filter
	// affected epfiles after the fetch. List mutates between scans via
	// /api/recover/exclusions endpoints.
	cfg := s.App.Config.Get()
	excl := cfg.RecoverExclusions[inst.ID]

	// itemFilter is series-ID scoped (matches the bash --series flag).
	// applyFilter is episodefile-ID scoped (matches the per-row UI
	// exclude — same field name as Radarr but different identity).
	itemFilter := make(map[int]bool, len(req.RecoverItems))
	for _, id := range req.RecoverItems {
		itemFilter[id] = true
	}
	applyFilter := make(map[int]bool, len(req.RecoverApplyItems))
	for _, id := range req.RecoverApplyItems {
		applyFilter[id] = true
	}

	resp := &scanResponse{
		Mode:   req.Mode,
		Action: "recover",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{
			Items: len(series),
		},
	}
	var applied scanApplied
	hasApply := false

	for _, ser := range series {
		if len(itemFilter) > 0 && !itemFilter[ser.ID] {
			continue
		}
		// Whole-series exclusion: skip the API call too. Per-season
		// exclusions still need the fetch (other seasons in the same
		// series may be in scope), so they filter after the fetch
		// inside the affected loop below.
		if excl.IsSeriesFullyExcluded(ser.ID) {
			continue
		}
		epfiles, lerr := client.ListEpisodefiles(ctx, ser.ID)
		if lerr != nil {
			// Series-level fetch failure → record one fix-failed row so
			// the user sees the series didn't get checked. No retry.
			// RecoverAffected is also bumped so per-status totals
			// reconcile against the affected count in the audit line
			// and the "All affected" filter chip in the UI doesn't drop
			// these rows from its bucket. ID prefixed -seriesID
			// (negated) to keep it from colliding with a real
			// episodefile ID elsewhere in the result list — Alpine
			// :key collisions silently clobber rows on re-render.
			resp.Totals.RecoverAffected++
			resp.Totals.RecoverFixFailed++
			resp.Recover = append(resp.Recover, scanRecoverItem{
				ID:          -ser.ID,
				Title:       ser.Title,
				Year:        ser.Year,
				TvdbID:      ser.TvdbID,
				SeriesID:    ser.ID,
				SeriesTitle: ser.Title,
				Status:      "fix-failed",
				Error:       "fetch episodefiles: " + lerr.Error(),
			})
			continue
		}

		// Affected = epfiles whose releaseGroup is empty / "Unknown",
		// minus any episodes in seasons the user has excluded.
		// IsSeasonExcluded handles both whole-series and per-season
		// exclusions — but the whole-series case was already short-
		// circuited above, so this branch only filters per-season.
		var affected []arr.EpisodeFile
		for _, ef := range epfiles {
			rg := strings.TrimSpace(ef.ReleaseGroup)
			if rg != "" && rg != "Unknown" {
				continue
			}
			if excl.IsSeasonExcluded(ser.ID, ef.SeasonNumber) {
				continue
			}
			affected = append(affected, ef)
		}
		if len(affected) == 0 {
			continue
		}
		resp.Totals.RecoverAffected += len(affected)

		// Join episodefiles back to their episode IDs. Sonarr's
		// /episodefile endpoint returns episodes:null, so without this the
		// per-episode history filter (Strategy A in filterHistoryForEpisode
		// file) never fires and recover falls back to sourceTitle matching
		// — which a season-pack grab (named "Show.S02", no episode number)
		// can never satisfy, surfacing every pack episode as a false
		// "failed-verify". /api/v3/episode carries episodeFileId per episode;
		// build the reverse map and attach the IDs each affected file covers.
		// Non-fatal on error: the per-epfile filter degrades to sourceTitle
		// matching exactly as before.
		if eps, eErr := client.ListEpisodes(ctx, ser.ID); eErr == nil {
			byFile := make(map[int][]arr.EpisodeRef, len(eps))
			for _, ep := range eps {
				if ep.EpisodeFileID > 0 {
					byFile[ep.EpisodeFileID] = append(byFile[ep.EpisodeFileID], arr.EpisodeRef{ID: ep.ID})
				}
			}
			for i := range affected {
				if refs := byFile[affected[i].ID]; len(refs) > 0 {
					affected[i].Episodes = refs
				}
			}
		}

		// Series-level history fetched once and re-used across every
		// affected epfile in this series. Bash same — saves N*M curls.
		history, hErr := client.ListHistoryForSeries(ctx, ser.ID)
		historyOK := hErr == nil

		for _, ef := range affected {
			row := scanRecoverItem{
				ID:           ef.ID, // episodefile ID = unique row identity
				Title:        sonarrEpisodeLabel(ser.Title, ef.SeasonNumber, ef.RelativePath),
				Year:         ser.Year,
				TvdbID:       ser.TvdbID,
				SeriesID:     ser.ID,
				SeriesTitle:  ser.Title,
				SeasonNumber: ef.SeasonNumber,
				MovieFileID:  ef.ID, // reuse the field — represents the epfile
				RelativePath: ef.RelativePath,
				SceneName:    ef.SceneName,
				CurrentGroup: ef.ReleaseGroup,
			}

			// Safety check 1: filename-group flag (same engine call as
			// Radarr — the parser is path-shape-agnostic).
			filenameGroup, hasFilenameGroup, rejectReason := engine.ParseReleaseGroupFromFilename(ef.RelativePath)
			if hasFilenameGroup {
				row.Status = "flagged"
				row.FilenameGroup = filenameGroup
				resp.Totals.RecoverFlagged++
				resp.Recover = append(resp.Recover, row)
				continue
			}
			if rejectReason != "" {
				row.FilenameReject = string(rejectReason)
			}

			if !historyOK {
				row.Status = "fix-failed"
				row.Error = "fetch history: " + hErr.Error()
				resp.Totals.RecoverFixFailed++
				resp.Recover = append(resp.Recover, row)
				continue
			}

			// Filter series history → events relevant to THIS epfile.
			// Strategy mirrors bash _process_sonarr at line 1085-1101:
			// episodeId match first, sourceTitle pattern fallback.
			epHistory := filterHistoryForEpisodefile(history, ef)
			if len(epHistory) == 0 {
				row.Status = "no-history"
				resp.Totals.RecoverNoHistory++
				resp.Recover = append(resp.Recover, row)
				continue
			}

			// Convert arr.HistoryRecord → engine.HistoryRecord at the
			// boundary. Engine has no I/O / no http — keeps the contract.
			engHistory := make([]engine.HistoryRecord, 0, len(epHistory))
			for _, h := range epHistory {
				engHistory = append(engHistory, engine.HistoryRecord{
					EventType:    engine.HistoryEventType(h.EventType),
					Date:         h.Date,
					SourceTitle:  h.SourceTitle,
					DownloadID:   h.DownloadID,
					ReleaseGroup: h.ReleaseGroup(),
				})
			}
			recoveredGroup, status := engine.FindImportedGrabGroup(engHistory, ser.Title, ser.Year)
			switch status {
			case engine.RecoverNoVerified:
				row.Status = "failed-verify"
				resp.Totals.RecoverFailedVerify++
				resp.Recover = append(resp.Recover, row)
				continue
			case engine.RecoverVerifiedEmpty:
				row.Status = "no-rls-group"
				resp.Totals.RecoverNoGroup++
				resp.Recover = append(resp.Recover, row)
				continue
			}

			// status == RecoverFound — populate import-event metadata.
			row.RecoveredGroup = recoveredGroup
			if importEv := newestImportEvent(engHistory); importEv != nil {
				row.ImportSourceTitle = importEv.SourceTitle
				if !importEv.Date.IsZero() {
					row.ImportDate = importEv.Date.UTC().Format(time.RFC3339)
				}
			}

			if req.Mode == "preview" {
				row.Status = "would-fix"
				resp.Totals.RecoverWouldFix++
				resp.Recover = append(resp.Recover, row)
				continue
			}

			// Apply mode. Skip if user excluded this epfile via RecoverApplyItems.
			if len(applyFilter) > 0 && !applyFilter[ef.ID] {
				row.Status = "would-fix"
				resp.Totals.RecoverWouldFix++
				resp.Recover = append(resp.Recover, row)
				continue
			}

			hasApply = true
			raw, gErr := client.GetEpisodefile(ctx, ef.ID)
			if gErr != nil {
				row.Status = "fix-failed"
				row.Error = "fetch episodefile: " + gErr.Error()
				resp.Totals.RecoverFixFailed++
				resp.Recover = append(resp.Recover, row)
				continue
			}
			if pErr := client.UpdateEpisodefileReleaseGroup(ctx, ef.ID, raw, recoveredGroup); pErr != nil {
				row.Status = "fix-failed"
				row.Error = "PUT episodefile: " + pErr.Error()
				resp.Totals.RecoverFixFailed++
				resp.Recover = append(resp.Recover, row)
				continue
			}
			row.Status = "fixed"
			resp.Totals.RecoverFixed++

			if req.RecoverRename {
				if rErr := client.TriggerSonarrRenameFiles(ctx, ser.ID, []int{ef.ID}); rErr != nil {
					resp.Totals.RecoverRenameFailed++
					if row.Error == "" {
						row.Error = "rename: " + rErr.Error()
					}
				} else {
					row.RenameTriggered = true
				}
			}
			resp.Recover = append(resp.Recover, row)
		}
	}

	// Sort by displayed Title (which already includes the S01E05 label)
	// so episodes within a series cluster naturally and series cluster
	// alphabetically. Case-insensitive for cross-locale stability.
	sort.Slice(resp.Recover, func(i, j int) bool {
		return strings.ToLower(resp.Recover[i].Title) < strings.ToLower(resp.Recover[j].Title)
	})

	if hasApply {
		resp.Applied = &applied
	}
	return resp, nil
}

// sonarrEpisodeLabel formats the per-row Title as "Series Title — S01E05".
// Falls back to the season-only label "Series Title — S01" when the
// relativePath doesn't yield an SxxExx token (mid-process renames or
// non-standard naming). Mirrors the bash item_label format so log
// audits cross-reference cleanly.
func sonarrEpisodeLabel(seriesTitle string, season int, relativePath string) string {
	tag := strings.ToUpper(epLabelRe.FindString(relativePath))
	if tag == "" {
		tag = fmt.Sprintf("S%02d", season)
	}
	return seriesTitle + " — " + tag
}

// filterHistoryForEpisodefile narrows series-level history down to
// events that belong to this specific epfile. Two-strategy filter
// matches bash _process_sonarr's jq pipeline:
//
//   Strategy A — episodeId match. Each history event has an
//   episodeId; if it's in epfile.episodes[].id, keep the event.
//   Strongest signal (Sonarr links the event to the episode directly).
//
//   Strategy B — sourceTitle pattern fallback. When Strategy A returns
//   nothing (older history events without episodeId, or hand-imported
//   files), try matching by the epfile's S01E05 label appearing in
//   the event's sourceTitle. Lossy — multi-episode releases like
//   "S01E05E06" only match one of their files, but that's a
//   correctness-over-completeness trade bash made.
//
// Returns an empty slice when neither strategy yields anything; the
// caller treats that as "no-history" status.
func filterHistoryForEpisodefile(history []arr.HistoryRecord, ef arr.EpisodeFile) []arr.HistoryRecord {
	// Strategy A: episodeId match.
	if len(ef.Episodes) > 0 {
		want := make(map[int]bool, len(ef.Episodes))
		for _, e := range ef.Episodes {
			want[e.ID] = true
		}
		var out []arr.HistoryRecord
		for _, h := range history {
			if h.EpisodeID > 0 && want[h.EpisodeID] {
				out = append(out, h)
			}
		}
		if len(out) > 0 {
			// Re-attach grab + import events the episodeId filter dropped
			// because Sonarr tagged them to a sibling episode of the same
			// download (season packs) — see attachDownloadIDLinkedEvents.
			return attachDownloadIDLinkedEvents(out, history, want)
		}
	}
	// Strategy B: sourceTitle pattern fallback.
	label := strings.ToLower(epLabelRe.FindString(ef.RelativePath))
	if label == "" {
		return nil
	}
	var out []arr.HistoryRecord
	for _, h := range history {
		if strings.Contains(strings.ToLower(h.SourceTitle), label) {
			out = append(out, h)
		}
	}
	return out
}

// attachDownloadIDLinkedEvents re-includes grab and import events from the
// full series history whose downloadId matches a downloadId already on one
// of the episode's matched events, but which the per-episode episodeId
// filter (Strategy A) dropped because Sonarr tagged them to a sibling
// episode of the same download.
//
// A season pack breaks per-episode narrowing in two ways, both of which
// surface as a false "failed-verify" for the affected episodes even though
// the file plainly came from the pack:
//
//   - the grab may be a single season-level event with no per-episode
//     episodeId — the filter drops it, leaving the import with no grab to
//     verify against; and
//   - the import may be a single downloadFolderImported event tagged only
//     to the pack's FIRST episode — the filter drops it for every other
//     episode, leaving those episodes with a grab but no import, so the
//     verifier's "find newest import" step (FindImportedGrabGroup) fails.
//
// The real Legion S02 case was the second: each episode had its own grab
// but the lone import event was tagged to E01, so E02-E11 reported
// failed-verify. Re-attaching every grab/import event sharing the episode's
// downloadId restores the import+grab pairing the verifier needs — the same
// complete-history view the Radarr/bash path has naturally, since it never
// narrows per-movie history. Events already pulled in by Strategy A (their
// own episodeId matched) are skipped so they aren't duplicated.
func attachDownloadIDLinkedEvents(matched, full []arr.HistoryRecord, matchedEpisodeIDs map[int]bool) []arr.HistoryRecord {
	wantDL := make(map[string]bool)
	for _, h := range matched {
		if dl := strings.TrimSpace(h.DownloadID); dl != "" {
			wantDL[dl] = true
		}
	}
	if len(wantDL) == 0 {
		return matched
	}
	for _, h := range full {
		if !isGrabOrImportEvent(h.EventType) {
			continue
		}
		// Already pulled in by Strategy A (its own episodeId matched).
		if h.EpisodeID > 0 && matchedEpisodeIDs[h.EpisodeID] {
			continue
		}
		dl := strings.TrimSpace(h.DownloadID)
		if dl == "" || !wantDL[dl] {
			continue
		}
		matched = append(matched, h)
	}
	return matched
}

// isGrabOrImportEvent reports whether an event type is a grab or one of the
// import-confirmation events FindImportedGrabGroup anchors on. Mirrors the
// engine's HistoryEvent* constants (grabbed / downloadFolderImported /
// movieFileImported / episodeFileImported).
func isGrabOrImportEvent(t string) bool {
	switch t {
	case "grabbed",
		"downloadFolderImported",
		"movieFileImported",
		"episodeFileImported":
		return true
	default:
		return false
	}
}
