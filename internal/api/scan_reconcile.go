package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_reconcile.go — reconcile-stuck-downloads action.
//
// "Fix stuck qBit category after import" (webhook) only ever fires on an
// import event, so it can't help a download that NEVER imported (e.g. a
// lower-tier release that lost the race to a better one grabbed seconds
// later — Your Friends & Neighbors S02E08: BLOOM 1675 stuck behind the
// imported Kitsune 1775). Those sit in the download client forever with
// the pre-import category.
//
// This sweep reads the Arr queue (the authoritative list of grabbed-but-
// not-imported downloads, with the stuck release's CF score + the target
// id + the download hash in one call), decides per item whether it's
// redundant (the target already has a file scoring >= the stuck release)
// or needs attention, and — on apply, for the rows the user selected —
// changes the qBit category of the redundant ones so the user's cleanup
// on that category can remove them. Never deletes the torrent.
//
// Engine decision: engine.ClassifyStuckDownload. Identification + qBit
// action live here.

func (s *Server) handleScanReconcile(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runReconcile(ctx, cfg, inst, appType, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "reconcile", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "reconcile", inst, req, resp, "")
	s.dumpScanJSON("reconcile", resp)
	writeJSON(w, resp)
}

// handleReconcileQbitCategories returns the Arr's qBittorrent download-
// client pre/post-import categories so the reconcile config modal can
// pre-fill the target (post-import) category before a scan runs.
func (s *Server) handleReconcileQbitCategories(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	pre, post, err := s.arrClientFor(inst).QbitImportCategories(ctx)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]string{"preImport": pre, "postImport": post})
}

func (s *Server) runReconcile(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) (*scanResponse, *apiError) {
	client := s.arrClientFor(inst)

	queue, err := client.ListQueue(ctx)
	if err != nil {
		return nil, newAPIError(502, "arr list queue: "+err.Error())
	}

	resp := &scanResponse{
		Mode:   req.Mode,
		Action: "reconcile",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
	}

	// Category hints from the Arr's qBittorrent download client, so the UI
	// pre-fills the target (post-import) category. Best-effort — failure
	// just leaves the field for the user to type.
	if pre, post, cerr := client.QbitImportCategories(ctx); cerr == nil {
		resp.ReconcilePreCategory = pre
		resp.ReconcilePostCategory = post
	}

	isApply := req.Mode == "apply"
	applySel := make(map[string]bool, len(req.ReconcileApplyItems))
	for _, id := range req.ReconcileApplyItems {
		if t := strings.TrimSpace(id); t != "" {
			applySel[t] = true
		}
	}

	// Resolve the download clients once for apply. The user picks one or
	// more (qBit instance, target category) pairs per run — no hidden
	// global state. Several clients are supported because one Arr's
	// queue mixes downloads from every client attached to it.
	var qclients *reconcileClients
	if isApply {
		targets := reconcileTargetsFromRequest(req)
		if len(targets) == 0 {
			return nil, newAPIError(400, "reconcileTargets (or reconcileQbitInstanceId + reconcilePostCategory) is required to apply")
		}
		rcs, rerr := newReconcileClients(cfg, targets)
		if rerr != nil {
			return nil, rerr
		}
		qclients = rcs
	}

	// Per-target caches (a queue can hold many items for the same
	// series/movie — fetch each target's data + history once).
	var radarrItems map[int]arr.Item           // bulk library map — un-narrowed runs only
	radarrOne := map[int]arr.Item{}            // per-id cache — narrowed runs
	seriesEpFileID := map[int]map[int]int{}    // seriesId -> episodeId -> episodeFileId
	seriesEpFileScore := map[int]map[int]int{} // seriesId -> episodeFileId -> cfScore
	seriesHistory := map[int][]arr.HistoryRecord{}
	movieHistory := map[int][]arr.HistoryRecord{}
	manualImport := map[string][]arr.ManualImportItem{} // downloadId -> per-file import evaluation
	// One category change per downloadId — a season pack is N queue rows
	// sharing one hash. Value is the row detail of the change so the
	// follow-up rows report the same client + category rather than a
	// bare "recategorised" with no context.
	categoryDone := map[string]reconcileClient{}

	for _, q := range queue {
		// Stuck = the download finished but Arr hasn't imported it.
		if !strings.EqualFold(q.Status, "completed") {
			continue
		}
		if q.TrackedDownloadState != "importPending" && q.TrackedDownloadState != "importBlocked" {
			continue
		}
		// Optional narrowing to one movie / series. The webhook adapter
		// fires per import event and by default only wants the losers of
		// THAT race; the Library scan page never narrows.
		if req.ReconcileOnlyMovieID > 0 && q.MovieID != req.ReconcileOnlyMovieID {
			continue
		}
		if req.ReconcileOnlySeriesID > 0 && q.SeriesID != req.ReconcileOnlySeriesID {
			continue
		}
		resp.Totals.ReconcileStuck++

		row := scanReconcileItem{
			DownloadID:   strings.TrimSpace(q.DownloadID),
			Title:        q.Title,
			AppType:      appType,
			TrackedState: q.TrackedDownloadState,
			StuckScore:   q.CustomFormatScore,
		}
		if len(q.StatusMessages) > 0 {
			row.StatusMessage = q.StatusMessages[0].Title
		}

		var targets []engine.StuckTarget

		switch appType {
		case "radarr":
			row.MovieID = q.MovieID
			// Narrowed runs (the webhook adapter) fetch just the one
			// movie. Reading the whole library here would break
			// Architectural rule 2 in webhook_adapters.go — a 24-episode
			// season pack would turn into 24 full-library reads, the
			// exact scaling trap that rule exists to prevent.
			//
			// Un-narrowed runs (Library scan page, reconcile schedules)
			// keep the single bulk read: that is the deliberate batch
			// model, and one list beats N per-movie fetches there.
			var (
				it    arr.Item
				found bool
			)
			if req.ReconcileOnlyMovieID > 0 {
				if cached, hit := radarrOne[q.MovieID]; hit {
					it, found = cached, true
				} else if fetched, ferr := client.GetItem(ctx, "radarr", q.MovieID); ferr == nil {
					radarrOne[q.MovieID] = fetched
					it, found = fetched, true
				}
				// A movie that vanished between the queue read and now
				// just loses its label; the row still gets classified.
			} else {
				if radarrItems == nil {
					items, lerr := client.ListItems(ctx, "radarr")
					if lerr != nil {
						return nil, newAPIError(502, "arr list movies: "+lerr.Error())
					}
					radarrItems = make(map[int]arr.Item, len(items))
					for _, x := range items {
						radarrItems[x.ID] = x
					}
				}
				it, found = radarrItems[q.MovieID]
			}
			if found {
				row.TargetLabel = it.Title
				if it.MovieFile != nil {
					row.HasFile = true
					// Radarr's /movie list embeds movieFile but omits its
					// customFormatScore — fetch the moviefile record for the
					// real score (Sonarr's /episodefile list carries it, so
					// the Sonarr path reads it inline).
					if mfs, mferr := client.MovieFilesForMovie(ctx, q.MovieID); mferr == nil && len(mfs) > 0 {
						row.ImportedScore = mfs[0].CustomFormatScore
					}
				}
				targets = append(targets, engine.StuckTarget{HasFile: row.HasFile, ImportedScore: row.ImportedScore})
			}
			if q.MovieID > 0 {
				if _, done := movieHistory[q.MovieID]; !done {
					h, _ := client.ListHistoryForMovie(ctx, q.MovieID)
					movieHistory[q.MovieID] = h
				}
			}
			row.StuckGrabDate, row.ImportedGrabDate = reconcileGrabDates(movieHistory[q.MovieID], row.DownloadID, 0)

		case "sonarr":
			row.SeriesID = q.SeriesID
			row.EpisodeID = q.EpisodeID
			if _, done := seriesEpFileID[q.SeriesID]; !done {
				epm := map[int]int{}
				if eps, eerr := client.ListEpisodes(ctx, q.SeriesID); eerr == nil {
					for _, e := range eps {
						epm[e.ID] = e.EpisodeFileID
					}
				}
				seriesEpFileID[q.SeriesID] = epm
				scm := map[int]int{}
				if efs, ferr := client.ListEpisodefiles(ctx, q.SeriesID); ferr == nil {
					for _, ef := range efs {
						scm[ef.ID] = ef.CustomFormatScore
					}
				}
				seriesEpFileScore[q.SeriesID] = scm
			}
			if fileID := seriesEpFileID[q.SeriesID][q.EpisodeID]; fileID > 0 {
				row.HasFile = true
				row.ImportedScore = seriesEpFileScore[q.SeriesID][fileID]
			}
			targets = append(targets, engine.StuckTarget{HasFile: row.HasFile, ImportedScore: row.ImportedScore})
			if _, done := seriesHistory[q.SeriesID]; !done {
				h, _ := client.ListHistoryForSeries(ctx, q.SeriesID)
				seriesHistory[q.SeriesID] = h
			}
			row.StuckGrabDate, row.ImportedGrabDate = reconcileGrabDates(seriesHistory[q.SeriesID], row.DownloadID, q.EpisodeID)
		}

		// Import evaluation (best-effort): the per-file CF score the queued
		// release gets at import + the rejection reason. One manualimport
		// call per downloadId (a pack shares one). For Sonarr, match the
		// item to this row's episode; for Radarr there's a single item.
		if row.DownloadID != "" {
			if _, done := manualImport[row.DownloadID]; !done {
				items, _ := client.ManualImportForDownload(ctx, row.DownloadID)
				manualImport[row.DownloadID] = items
			}
			var chosen *arr.ManualImportItem
			items := manualImport[row.DownloadID]
			if appType == "sonarr" {
				for i := range items {
					for _, e := range items[i].Episodes {
						if e.ID == row.EpisodeID {
							chosen = &items[i]
							break
						}
					}
					if chosen != nil {
						break
					}
				}
			} else if len(items) > 0 {
				chosen = &items[0]
			}
			if chosen != nil {
				row.ImportScore = chosen.CustomFormatScore
				row.ImportScoreKnown = true
				if len(chosen.Rejections) > 0 {
					row.Rejection = chosen.Rejections[0].Reason
				}
			}
		}

		// Classify on the IMPORT score when we have it — that's the score
		// the Arr actually compares against the existing file (the grab/
		// release-title score is often inflated vs what the file scores on
		// disk: The Menu grabbed at 4601 but imports at 2231 < existing
		// 3850, so it's redundant even though its grab score looks higher).
		// Fall back to the grab score when the import evaluation wasn't
		// available.
		effectiveScore := row.StuckScore
		if row.ImportScoreKnown {
			effectiveScore = row.ImportScore
		}
		verdict := engine.ClassifyStuckDownload(effectiveScore, targets)
		row.Status = string(verdict)
		if verdict == engine.StuckRedundant {
			resp.Totals.ReconcileRedundant++
		} else {
			resp.Totals.ReconcileNeedsAttention++
		}

		// Apply: an explicit per-row selection acts on that row whatever
		// its verdict (the user's call — they can move a needs-attention
		// one too). "Apply all" from the modal (no selection) stays
		// conservative: redundant only.
		shouldAct := verdict == engine.StuckRedundant
		if len(applySel) > 0 {
			shouldAct = applySel[row.DownloadID]
		}
		if isApply && shouldAct {
			if done, already := categoryDone[row.DownloadID]; already {
				row.Status = "recategorised"
				row.QbitInstanceName = done.name
				row.QbitCategory = done.category
			} else if target, ok := qclients.clientFor(ctx, row.DownloadID); !ok {
				// Nobody owns this hash. Most often the user already
				// removed the torrent by hand, or the download lives in
				// a client they didn't tick for this run — either way,
				// say so instead of failing against an arbitrary client.
				row.Status = "failed"
				row.Error = "not found in any selected download client (" + strings.Join(qclients.names(), ", ") + ")"
				resp.Totals.ReconcileFailed++
			} else if err := target.client.SetTorrentCategory(ctx, row.DownloadID, target.category); err != nil {
				row.Status = "failed"
				row.Error = err.Error()
				row.QbitInstanceName = target.name
				resp.Totals.ReconcileFailed++
			} else {
				categoryDone[row.DownloadID] = target
				row.Status = "recategorised"
				row.QbitInstanceName = target.name
				row.QbitCategory = target.category
				resp.Totals.ReconcileRecategorised++
			}
		}

		resp.Reconcile = append(resp.Reconcile, row)
	}

	return resp, nil
}

// reconcileGrabDates returns (stuckGrabDate, importedGrabDate) as RFC3339
// strings from a target's history. stuck = the newest grab carrying the
// stuck download's hash. imported = the grab behind the file on disk:
// the newest import event (filtered to episodeID when > 0) gives the
// download that produced the current file, then the newest grab with
// that download's hash. Either may be empty when history doesn't carry
// the event (pruned / migrated).
func reconcileGrabDates(history []arr.HistoryRecord, stuckDownloadID string, episodeID int) (stuck string, imported string) {
	if len(history) == 0 || stuckDownloadID == "" {
		return "", ""
	}
	var newestImport *arr.HistoryRecord
	for i := range history {
		h := &history[i]
		if !reconcileIsImport(h.EventType) {
			continue
		}
		if episodeID > 0 && h.EpisodeID != episodeID {
			continue
		}
		if newestImport == nil || h.Date.After(newestImport.Date) {
			newestImport = h
		}
	}
	importDL := ""
	if newestImport != nil {
		importDL = strings.TrimSpace(newestImport.DownloadID)
	}

	var stuckGrab, impGrab *arr.HistoryRecord
	for i := range history {
		h := &history[i]
		if h.EventType != "grabbed" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(h.DownloadID), stuckDownloadID) {
			if stuckGrab == nil || h.Date.After(stuckGrab.Date) {
				stuckGrab = h
			}
		}
		if importDL != "" && strings.EqualFold(strings.TrimSpace(h.DownloadID), importDL) {
			if impGrab == nil || h.Date.After(impGrab.Date) {
				impGrab = h
			}
		}
	}
	if stuckGrab != nil && !stuckGrab.Date.IsZero() {
		stuck = stuckGrab.Date.UTC().Format(time.RFC3339)
	}
	if impGrab != nil && !impGrab.Date.IsZero() {
		imported = impGrab.Date.UTC().Format(time.RFC3339)
	}
	return stuck, imported
}

// reconcileIsImport reports whether an event type is an import event.
func reconcileIsImport(t string) bool {
	switch t {
	case "downloadFolderImported", "movieFileImported", "episodeFileImported":
		return true
	}
	return false
}
