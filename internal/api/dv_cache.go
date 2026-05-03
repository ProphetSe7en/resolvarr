package api

import (
	"net/http"

	"resolvarr/internal/core/dvdetect"
)

// handleDvCacheStats returns the in-memory entry count + on-disk
// file size + oldest/newest CachedAt timestamps. Powers the cache
// status line on the DV detail tab.
//
// When DvCache is nil (cache wasn't attached at startup — shouldn't
// happen in normal deployments but covered for defensiveness) we
// still return a valid empty Stats response so the UI renders
// "0 cached files · 0 B" instead of choking. Same shape both branches
// so the UI doesn't need to special-case nil — every caller sees the
// canonical dvdetect.Stats JSON layout.
func (s *Server) handleDvCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.DvCache == nil {
		writeJSON(w, dvdetect.Stats{})
		return
	}
	writeJSON(w, s.DvCache.Stats())
}

// handleDvCacheClear wipes every cached entry and persists an empty
// file. Returns the same Stats shape as the GET so the UI can render
// the post-clear state without a follow-up GET round-trip.
//
// Returns 200 with the cleared stats on success, 500 if the on-disk
// write fails (rare — disk full / permissions). In-memory wipe is
// always applied first so a write-failure leaves the user with a
// fresh in-memory cache + a stale on-disk file (which Load handles
// fine — next Save overwrites). We surface the error so the user
// knows persistence didn't take, not so they re-try the wipe.
func (s *Server) handleDvCacheClear(w http.ResponseWriter, r *http.Request) {
	if s.DvCache == nil {
		writeJSON(w, dvdetect.Stats{})
		return
	}
	if err := s.DvCache.ClearAndSave(); err != nil {
		writeAPIError(w, newAPIError(500, "clear cache: "+err.Error()))
		return
	}
	writeJSON(w, s.DvCache.Stats())
}
