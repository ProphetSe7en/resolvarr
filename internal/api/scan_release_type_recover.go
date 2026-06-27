package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/qbit"
)

// scan_release_type_recover.go — Sonarr-only "Recover release type" scan,
// preview stage (Stage A). For every episode file whose stored releaseType
// is unknown, it reads the episode's grab history and runs the durable
// cascade (engine.DecideReleaseTypeRecovery: Tier 1 stored field, Tier 3
// sourceTitle + release-group match) to determine the true type. Returns
// the candidates as a preview only — no write happens here.
//
// Tier 2 (qBit content verification, opt-in via ReleaseTypeQbitInstanceID)
// is layered in here over the engine verdict; Apply (ManualImport) lands in
// Stage C. See docs/resolvarr/release-type-recovery-design.md.

func (s *Server) handleScanReleaseTypeRecover(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runReleaseTypeRecover(ctx, cfg, inst, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "release-type-recover", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "release-type-recover", inst, req, resp, "")
	s.dumpScanJSON("release-type-recover", resp)
	writeJSON(w, resp)
}

func (s *Server) runReleaseTypeRecover(ctx context.Context, cfg core.Config, inst *core.Instance, req scanRunRequest) (*scanResponse, *apiError) {
	client := s.arrClientFor(inst)
	series, err := client.ListItems(ctx, "sonarr")
	if err != nil {
		return nil, newAPIError(502, "arr list series: "+err.Error())
	}

	// Apply can scope to specific series so the client re-imports
	// series-by-series (progress + cancel) without each batch re-scanning
	// the whole library.
	if req.Mode == "apply" && len(req.ReleaseTypeApplySeriesIds) > 0 {
		want := make(map[int]bool, len(req.ReleaseTypeApplySeriesIds))
		for _, id := range req.ReleaseTypeApplySeriesIds {
			want[id] = true
		}
		filtered := series[:0]
		for _, ser := range series {
			if want[ser.ID] {
				filtered = append(filtered, ser)
			}
		}
		series = filtered
	}

	// Optional Tier 2: confirm verdicts against the chosen qBittorrent
	// instance by exact byte-size match. Wired up only when the user picks
	// an instance; a bad pick is a hard error (they explicitly asked for it),
	// but per-series/per-torrent qBit hiccups degrade gracefully to the grab
	// cascade.
	var verifier *releaseTypeQbitVerifier
	if qid := strings.TrimSpace(req.ReleaseTypeQbitInstanceID); qid != "" {
		qinst := findQbitInstanceByID(cfg, qid)
		if qinst == nil {
			return nil, newAPIError(400, "releaseTypeQbitInstanceId not found in config")
		}
		qc, qerr := qbit.New(qbit.Config{
			URL:          qinst.URL,
			Username:     qinst.Username,
			Password:     qinst.Password,
			TrustedCerts: qinst.TrustedCerts,
		})
		if qerr != nil {
			return nil, newAPIError(502, "qbit client init: "+qerr.Error())
		}
		verifier = &releaseTypeQbitVerifier{ctx: ctx, client: qc, files: map[string][]engine.TorrentFileView{}}
	}

	resp := &scanResponse{
		Mode:   req.Mode,
		Action: "release-type-recover",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{Items: len(series)},
	}

	// Apply (Stage C): re-import the selected files via ManualImport. Empty
	// selection in apply mode means every applicable candidate.
	isApply := req.Mode == "apply"
	applyFilter := make(map[int]bool, len(req.ReleaseTypeApplyItems))
	for _, id := range req.ReleaseTypeApplyItems {
		applyFilter[id] = true
	}

	for _, ser := range series {
		epfiles, lerr := client.ListEpisodefiles(ctx, ser.ID)
		if lerr != nil {
			continue // one unreachable series shouldn't sink the preview
		}

		// Only files Sonarr never classified (unknown/empty) are recovery
		// candidates. Series with none skip the history fetch entirely.
		var todo []arr.EpisodeFile
		for _, ef := range epfiles {
			if engine.NormaliseReleaseType(ef.ReleaseType) == "" {
				todo = append(todo, ef)
			}
		}
		if len(todo) == 0 {
			continue
		}

		// Join episode IDs back to each file. Sonarr's /episodefile endpoint
		// returns episodes:null, so without this the per-file history filter
		// degrades to sourceTitle matching — which a season-pack grab (named
		// "Show.S01", no episode number) can never satisfy, so packs would be
		// missed entirely and every match would be a single. Mirrors the
		// recover-Sonarr join. Non-fatal: degrades to the old behaviour.
		if eps, eErr := client.ListEpisodes(ctx, ser.ID); eErr == nil {
			byFile := make(map[int][]arr.EpisodeRef, len(eps))
			for _, ep := range eps {
				if ep.EpisodeFileID > 0 {
					byFile[ep.EpisodeFileID] = append(byFile[ep.EpisodeFileID], arr.EpisodeRef{ID: ep.ID})
				}
			}
			for i := range todo {
				if refs := byFile[todo[i].ID]; len(refs) > 0 {
					todo[i].Episodes = refs
				}
			}
		}

		history, herr := client.ListHistoryForSeries(ctx, ser.ID)
		if herr != nil {
			continue // without history there is nothing to determine the type from
		}

		// qBit torrent views for this series are fetched once, lazily, only
		// when the series actually has a candidate (so series with nothing to
		// recover never touch qBit).
		var seriesViews []engine.QbitTorrentView
		viewsReady := false

		for _, ef := range todo {
			grabs := grabsForEpisodeFile(history, ef)
			resp.Totals.ReleaseTypeRecoverChecked++
			if len(grabs) == 0 {
				continue
			}
			v := engine.DecideReleaseTypeRecovery(engine.ReleaseTypeRecoverInput{
				CurrentType:  ef.ReleaseType,
				ReleaseGroup: ef.ReleaseGroup,
				Grabs:        grabs,
			})
			if !v.IsCandidate {
				continue
			}
			grabEvidence := make([]scanReleaseTypeGrab, 0, len(v.Evidence))
			for _, ev := range v.Evidence {
				grabEvidence = append(grabEvidence, scanReleaseTypeGrab{
					SourceTitle:    ev.SourceTitle,
					ReleaseGroup:   ev.ReleaseGroup,
					StoredType:     ev.StoredType,
					ImpliedType:    ev.ImpliedType,
					GroupMatch:     ev.GroupMatch,
					UsedInDecision: ev.UsedInDecision,
				})
			}
			item := scanReleaseTypeRecoverItem{
				SeriesID:      ser.ID,
				SeriesTitle:   ser.Title,
				Year:          ser.Year,
				EpisodeFileID: ef.ID,
				SeasonNumber:  ef.SeasonNumber,
				EpisodeLabel:  epLabelRe.FindString(ef.RelativePath),
				RelativePath:  ef.RelativePath,
				CurrentType:   ef.ReleaseType,
				RecoveredType: v.RecoveredType,
				Confidence:    v.Confidence,
				Source:        v.Source,
				GroupMatched:  v.GroupMatched,
				Reason:        v.Reason,
				Explanation:   v.Explanation,
				Grabs:         grabEvidence,
			}

			// Tier 2: confirm against qBit by exact byte-size match. Ground
			// truth — overrides the grab cascade when a torrent is found.
			if verifier != nil {
				if !viewsReady {
					seriesViews = verifier.viewsForSeries(ser.Title)
					viewsReady = true
				}
				applyQbitVerification(&item, v.RecoveredType, ef.Size, seriesViews)
			}

			// Stage C: apply the corrected type via ManualImport. Preview
			// leaves Status "would-fix". The server re-derives the verdict
			// above, so a tampered client can't force a wrong type, and
			// Unconfirmed is never written (a single could really be a pack).
			item.Status = "would-fix"
			if isApply {
				switch {
				case len(applyFilter) > 0 && !applyFilter[ef.ID]:
					item.Status = "skipped"
				case item.Confidence == engine.ReleaseTypeConfUnconfirmed:
					item.Status = "skipped"
					item.Error = "unconfirmed, confirm with qBittorrent before applying"
				default:
					epIDs := episodeIDsOf(ef)
					raw, gErr := client.GetEpisodefile(ctx, ef.ID)
					if gErr != nil {
						item.Status = "fix-failed"
						item.Error = "fetch episodefile: " + gErr.Error()
						resp.Totals.ReleaseTypeRecoverFixFailed++
					} else if aErr := client.SetEpisodeFileReleaseType(ctx, raw, epIDs, item.RecoveredType); aErr != nil {
						item.Status = "fix-failed"
						item.Error = aErr.Error()
						resp.Totals.ReleaseTypeRecoverFixFailed++
					} else {
						item.Status = "fixed"
						resp.Totals.ReleaseTypeRecoverFixed++
					}
				}
			}

			resp.ReleaseTypeRecover = append(resp.ReleaseTypeRecover, item)
			resp.Totals.ReleaseTypeRecoverCandidates++
			if item.QbitConfirmed {
				resp.Totals.ReleaseTypeRecoverQbitConfirmed++
			}
			switch item.Confidence {
			case engine.ReleaseTypeConfHigh:
				resp.Totals.ReleaseTypeRecoverHigh++
			case engine.ReleaseTypeConfMedium:
				resp.Totals.ReleaseTypeRecoverMedium++
			default:
				resp.Totals.ReleaseTypeRecoverUnconfirmed++
			}
		}
	}

	// Stable order: series title, then season, then episode label.
	sort.Slice(resp.ReleaseTypeRecover, func(i, j int) bool {
		a, b := resp.ReleaseTypeRecover[i], resp.ReleaseTypeRecover[j]
		if at, bt := strings.ToLower(a.SeriesTitle), strings.ToLower(b.SeriesTitle); at != bt {
			return at < bt
		}
		if a.SeasonNumber != b.SeasonNumber {
			return a.SeasonNumber < b.SeasonNumber
		}
		return a.EpisodeLabel < b.EpisodeLabel
	})

	return resp, nil
}

// grabsForEpisodeFile reduces a series' history to the grab events for one
// episode file (reusing the recover flow's season-pack-aware per-file
// filter), mapped to the engine's minimal grab shape.
func grabsForEpisodeFile(history []arr.HistoryRecord, ef arr.EpisodeFile) []engine.ReleaseTypeGrab {
	evs := filterHistoryForEpisodefile(history, ef)
	var grabs []engine.ReleaseTypeGrab
	for _, h := range evs {
		if h.EventType != "grabbed" {
			continue
		}
		grabs = append(grabs, engine.ReleaseTypeGrab{
			SourceTitle:  h.SourceTitle,
			ReleaseGroup: h.ReleaseGroup(),
			FieldType:    h.GrabReleaseType(),
		})
	}
	return grabs
}

// episodeIDsOf returns the episode IDs joined onto a file (the /episode
// pass in the scan loop populates ef.Episodes; /episodefile alone returns
// episodes:null). ManualImport needs these to re-import the file.
func episodeIDsOf(ef arr.EpisodeFile) []int {
	ids := make([]int, 0, len(ef.Episodes))
	for _, e := range ef.Episodes {
		if e.ID > 0 {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// maxQbitTorrentsPerSeries caps how many torrents we fetch a file list for
// per series. The title pre-filter normally narrows this to a handful (the
// pack + its cross-seeds); the cap is a backstop against a too-common title
// pre-matching a large slice of the library and ballooning the scan.
const maxQbitTorrentsPerSeries = 80

// releaseTypeQbitVerifier holds the live qBit client for Tier 2 and caches
// the torrent list (fetched once) plus per-hash file lists across the scan.
type releaseTypeQbitVerifier struct {
	ctx      context.Context
	client   *qbit.Client
	torrents []qbit.Torrent
	loaded   bool
	listErr  bool                                // true once ListTorrents failed; stop retrying
	files    map[string][]engine.TorrentFileView // per-hash file cache
}

// viewsForSeries returns the qBit torrents whose name plausibly belongs to
// the series (normalised-title substring), each with its file list. The
// title match only BOUNDS which torrents we fetch files for; the actual
// confirmation is the exact byte-size match in the engine. Best-effort:
// any qBit error degrades to "no views" so the grab cascade still stands.
func (v *releaseTypeQbitVerifier) viewsForSeries(title string) []engine.QbitTorrentView {
	if !v.loaded && !v.listErr {
		ts, err := v.client.ListTorrents(v.ctx, "")
		if err != nil {
			v.listErr = true
		} else {
			v.torrents = ts
		}
		v.loaded = true
	}
	key := normaliseReleaseTitleKey(title)
	if len(key) < 3 || len(v.torrents) == 0 {
		return nil
	}
	var views []engine.QbitTorrentView
	for _, t := range v.torrents {
		if !strings.Contains(normaliseReleaseTitleKey(t.Name), key) {
			continue
		}
		files, ok := v.files[t.Hash]
		if !ok {
			raw, err := v.client.ListTorrentFiles(v.ctx, t.Hash)
			if err != nil {
				v.files[t.Hash] = nil // negative-cache: don't refetch this scan
				continue
			}
			files = make([]engine.TorrentFileView, 0, len(raw))
			for _, f := range raw {
				files = append(files, engine.TorrentFileView{Name: f.Name, Size: f.Size})
			}
			v.files[t.Hash] = files
		}
		if files == nil {
			continue
		}
		views = append(views, engine.QbitTorrentView{Hash: t.Hash, Name: t.Name, Files: files})
		if len(views) >= maxQbitTorrentsPerSeries {
			break
		}
	}
	return views
}

// normaliseReleaseTitleKey lowercases a title and drops everything but
// letters/digits, so "Whiskey Cavalier" and "Whiskey.Cavalier.2019.S01..."
// share the substring "whiskeycavalier".
func normaliseReleaseTitleKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyQbitVerification runs the Tier-2 content match for one candidate and
// rewrites its verdict in place. A byte-size match is ground truth: it sets
// the type from the matched torrent's video-file count, raises confidence to
// High, and notes if it corrected the grab-history guess. No match leaves the
// grab verdict intact with a short note.
func applyQbitVerification(item *scanReleaseTypeRecoverItem, grabType string, fileSize int64, views []engine.QbitTorrentView) {
	item.QbitChecked = true
	m := engine.MatchReleaseTypeByContent(fileSize, views)
	if !m.Matched {
		item.QbitNote = "Not found in the selected qBittorrent, so this stays on the grab-history verdict (the torrent may have been removed)."
		return
	}
	item.QbitConfirmed = true
	item.QbitTorrentName = m.TorrentName
	item.QbitVideoFiles = m.VideoFiles
	corrected := m.Type != grabType
	item.RecoveredType = m.Type
	item.Confidence = engine.ReleaseTypeConfHigh
	item.Source = "qbit"

	files := "1 video file"
	if m.VideoFiles != 1 {
		files = strconv.Itoa(m.VideoFiles) + " video files"
	}
	if corrected {
		item.Reason = "confirmed by qBittorrent (" + files + ")"
		item.Explanation = "The file on disk matches a torrent in qBittorrent by exact byte size. That torrent holds " + files + ", so this is a " + releaseTypeDisplay(m.Type) + ". qBittorrent is the actual download, so this overrides the grab-history guess of " + releaseTypeDisplay(grabType) + "."
	} else {
		item.Reason = "confirmed by qBittorrent (" + files + ")"
		item.Explanation = "The file on disk matches a torrent in qBittorrent by exact byte size, and that torrent holds " + files + ", confirming " + releaseTypeDisplay(m.Type) + ". That's why it's High."
	}
}

// releaseTypeDisplay renders a release-type constant for user-facing copy.
func releaseTypeDisplay(t string) string {
	switch t {
	case engine.ReleaseTypeSeasonPack:
		return "Season Pack"
	case engine.ReleaseTypeSingleEpisode:
		return "Single Episode"
	case engine.ReleaseTypeMultiEpisode:
		return "Multi-Episode"
	default:
		return "Unknown"
	}
}
