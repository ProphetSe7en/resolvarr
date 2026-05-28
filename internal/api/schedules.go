package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// schedules.go — CRUD + RunNow handlers for ScheduledJob entries.
// Persistence goes through ConfigStore.Update; cron loop registration
// goes through s.Scheduler.Reload after every mutation.
//
// Validation gates: cron expression parses, mode in JobMode set,
// instance exists, options match mode (e.g. CombinedModes only set
// when Mode == JobModeCombined).

// scheduleRequest is the POST/PUT body. Mirrors core.ScheduledJob with
// History and ID stripped — those are server-managed, not client input.
//
// Per-rule snapshots (Filters / AudioTags / VideoTags / DvDetail /
// ReleaseGroupIDs) are optional in the wire shape so old clients
// posting without them keep working: missing fields fall through
// to the on-Load migration which backfills from globals. New
// clients (the rule-editor wizard) always send all snapshots.
type scheduleRequest struct {
	Name            string                       `json:"name"`
	Mode            core.JobMode                 `json:"mode"`
	InstanceID      string                       `json:"instanceId"`
	Cron            string                       `json:"cron"`
	Enabled         bool                         `json:"enabled"`
	Options         core.JobOptions              `json:"options"`
	Filters         *engine.FilterConfig         `json:"filters,omitempty"`
	AudioTags       *core.AudioTagsConfig        `json:"audioTags,omitempty"`
	VideoTags       *core.VideoTagsConfig        `json:"videoTags,omitempty"`
	DvDetail        *core.DvDetailConfig         `json:"dvDetail,omitempty"`
	MissingEpisodes *core.MissingEpisodesConfig  `json:"missingEpisodes,omitempty"`
	PlexSync        *core.PlexLabelSyncConfig    `json:"plexSync,omitempty"`
	ReleaseGroupIDs []string                     `json:"releaseGroupIds,omitempty"`
}

// validate enforces the schedule contract before persistence. Returns
// nil on success, an apiError on any rule violation.
func (req *scheduleRequest) validate(cfg core.Config) *apiError {
	if strings.TrimSpace(req.Name) == "" {
		return newAPIError(400, "name is required")
	}
	if !core.ValidJobMode(req.Mode) {
		return newAPIError(400, "mode must be tag, discover, recover, audiotags, videotags, dvdetail, or combined")
	}
	// Empty cron is a valid "manual run only" sentinel — the scheduler
	// already skips empty-cron rules in Reload (see scheduler.go:155).
	// Run-now via /api/schedules/{id}/run still fires regardless of
	// cron presence. Non-empty values must still parse.
	if strings.TrimSpace(req.Cron) != "" {
		if err := core.ValidateCron(req.Cron); err != nil {
			return newAPIError(400, err.Error())
		}
	}
	// Instance must exist.
	found := false
	for _, i := range cfg.Instances {
		if i.ID == req.InstanceID {
			found = true
			break
		}
	}
	if !found {
		return newAPIError(400, "instanceId not found")
	}
	// CombinedModes is only meaningful when Mode == Combined.
	if req.Mode == core.JobModeCombined && len(req.Options.CombinedModes) == 0 {
		return newAPIError(400, "combined mode requires options.combinedModes (one or more of discover/recover/tag/audiotags/videotags/dvdetail/missingepisodes)")
	}
	if req.Mode != core.JobModeCombined && len(req.Options.CombinedModes) > 0 {
		return newAPIError(400, "options.combinedModes is only meaningful when mode = combined")
	}
	for _, m := range req.Options.CombinedModes {
		if !core.ValidJobMode(m) || m == core.JobModeCombined {
			return newAPIError(400, "options.combinedModes entries must be one of discover/recover/tag/audiotags/videotags/dvdetail/missingepisodes")
		}
	}
	if req.Options.RunMode != "" && req.Options.RunMode != "preview" && req.Options.RunMode != "apply" {
		return newAPIError(400, "options.runMode must be preview or apply")
	}
	// Tag-source validation — mirrors handleScanRun's filter-only path
	// at scan.go so a schedule POST/PUT can't persist garbage that
	// would only get cleaned up at next process restart via Config.Load
	// migration. Validates closed enum + filter-only's tag-name shape
	// against the same regex Active groups use.
	switch req.Options.TagSource {
	case "", "active", "discover", "filter-only":
		// OK
	default:
		return newAPIError(400, `options.tagSource must be "" / "active" / "discover" / "filter-only"`)
	}
	if req.Options.TagSource == "filter-only" {
		if req.Options.FilterOnlyTag == "" {
			return newAPIError(400, "options.filterOnlyTag is required when tagSource is filter-only")
		}
		if !reTagName.MatchString(req.Options.FilterOnlyTag) {
			return newAPIError(400, "options.filterOnlyTag must be lowercase letters, digits, underscores, or dashes")
		}
	}

	// MissingEpisodes snapshot bounds — mirrors the per-handler
	// validation in scan_missing_episodes.go so a schedule POST/PUT
	// can't persist garbage that would only get clamped at fire-time.
	// Sonarr-only enforced via the instance type check above (we
	// already resolved + validated the instance against appType).
	if req.MissingEpisodes != nil {
		me := req.MissingEpisodes
		if me.ThresholdPercent < 0 || me.ThresholdPercent > 100 {
			return newAPIError(400, "missingEpisodes.thresholdPercent must be between 0 and 100")
		}
		if me.BufferHours < 0 || me.BufferHours > 672 {
			return newAPIError(400, "missingEpisodes.bufferHours must be between 0 and 672 (4 weeks)")
		}
		if me.TagName != "" && !reTagName.MatchString(me.TagName) {
			return newAPIError(400, "missingEpisodes.tagName must be lowercase letters, digits, underscores, or dashes")
		}
		// Phase only fires on Sonarr — defend at save-time so a
		// Radarr rule can't persist a no-op snapshot.
		instType := ""
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == req.InstanceID {
				instType = cfg.Instances[i].Type
				break
			}
		}
		if instType != "" && instType != "sonarr" {
			return newAPIError(400, "missingEpisodes is Sonarr-only — pick a Sonarr instance")
		}
	}

	// Per-schedule auto-tag snapshots — validate identically to the
	// global PUT handlers. Without this, an overlay can persist a
	// snapshot the global handlers would have rejected (bad prefix,
	// unknown allowed-value, malformed Labels override). The Labels
	// feature added 2026-05-16 surfaces this gap by letting users
	// configure per-value renames — the overlay path must enforce the
	// same closed-vocab + collision + regex rules as the global.
	if req.AudioTags != nil {
		if err := validateAudioTagsConfig(*req.AudioTags); err != nil {
			return newAPIError(400, "audioTags: "+err.Error())
		}
	}
	if req.VideoTags != nil {
		if err := validateVideoTagsConfig(*req.VideoTags); err != nil {
			return newAPIError(400, "videoTags: "+err.Error())
		}
	}
	if req.DvDetail != nil {
		if err := validateDvDetailConfig(*req.DvDetail); err != nil {
			return newAPIError(400, "dvDetail: "+err.Error())
		}
	}
	// PlexSync snapshot — validate against the shared config validator
	// (label/library dedupe, cached-key existence, Radarr→movie /
	// Sonarr→show library-type filter). Needs the resolved instance
	// type, which the instance-exists check above already confirmed.
	if req.PlexSync != nil {
		instType := ""
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == req.InstanceID {
				instType = cfg.Instances[i].Type
				break
			}
		}
		if err := core.ValidatePlexLabelSyncConfig(req.PlexSync, cfg.PlexInstances, instType); err != nil {
			return newAPIError(400, "plexSync: "+err.Error())
		}
	}
	return nil
}

// handleListSchedules — GET /api/schedules.
// Returns the full list with history. Auth-gated like every other
// /api endpoint.
func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	out := cfg.Schedules
	if out == nil {
		out = []core.ScheduledJob{}
	}
	writeJSON(w, out)
}

// handleGetScheduleRunResult — GET /api/schedules/{id}/runs/{startedAt}/result.
// Returns the persisted scan-response JSON for one historical run so the
// frontend can hydrate scanResults.tag / .discover / .extraTags or
// recoverResults (or the {tag, discover, recover, extraTags} bundle
// for combined runs) and replay the same drill-in UI the live
// Run-mode shows.
//
// startedAt is the JobRun.StartedAt as RFC3339Nano (Go's default time.Time
// JSON encoding) URL-encoded. We parse it as time.Time and match against
// JobRun.StartedAt with .Equal — string compare would fail because the
// JSON nano-precision doesn't always round-trip equal to a re-formatted
// stamp on either side. Path-traversal defense: the resolved file must
// live under the scheduler's logDir.
func (s *Server) handleGetScheduleRunResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	startedAtRaw := r.PathValue("startedAt")
	want, err := time.Parse(time.RFC3339Nano, startedAtRaw)
	if err != nil {
		// Try plain RFC3339 as a fallback (older entries serialized
		// without sub-second precision).
		want, err = time.Parse(time.RFC3339, startedAtRaw)
		if err != nil {
			writeError(w, 400, "invalid startedAt: "+err.Error())
			return
		}
	}
	cfg := s.App.Config.Get()
	var sched *core.ScheduledJob
	for i := range cfg.Schedules {
		if cfg.Schedules[i].ID == id {
			sched = &cfg.Schedules[i]
			break
		}
	}
	if sched == nil {
		writeError(w, 404, "schedule not found")
		return
	}
	var run *core.JobRun
	for i := range sched.History {
		if sched.History[i].StartedAt.Equal(want) {
			run = &sched.History[i]
			break
		}
	}
	if run == nil {
		writeError(w, 404, "run not found")
		return
	}
	if run.ResultPath == "" {
		writeError(w, 404, "no result persisted for this run")
		return
	}
	// Path-traversal defense: confirm the file is within the scheduler's
	// log dir. ResultPath was server-generated, but a config tamperer or
	// upgrade-path edge could land an arbitrary path here.
	if s.Scheduler == nil {
		writeError(w, 503, "scheduler not attached")
		return
	}
	logDir := s.Scheduler.LogDir()
	// Canonicalize the path AND resolve any symlinks before the prefix
	// check. Without EvalSymlinks a `foo.json -> /etc/shadow` symlink
	// inside logDir would pass HasPrefix (the link itself lives under
	// logDir) yet open arbitrary content. The parens around the
	// !HasPrefix branch matter — Go's && binds tighter than || so the
	// previous form `err != nil || !HasPrefix(...) && abs != logDir`
	// parsed as `err || (!HasPrefix && != logDir)`, which accepted
	// invalid paths whenever `abs == logDir`. Both fixes land together
	// since the same line is being touched.
	abs, err := filepath.EvalSymlinks(run.ResultPath)
	if err != nil {
		// EvalSymlinks fails if the file is missing — fall through to
		// the os.Open below so the 404 path produces a helpful message.
		abs = run.ResultPath
	}
	abs, absErr := filepath.Abs(abs)
	if absErr != nil || !strings.HasPrefix(abs, logDir+string(filepath.Separator)) {
		writeError(w, 400, "invalid result path")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, 404, "result file missing")
			return
		}
		writeError(w, 500, "open result: "+err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, f); err != nil {
		// Header already sent; nothing better we can do.
		return
	}
}

// handleGetSchedule — GET /api/schedules/{id}.
func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := s.App.Config.Get()
	for _, sj := range cfg.Schedules {
		if sj.ID == id {
			writeJSON(w, sj)
			return
		}
	}
	writeError(w, 404, "schedule not found")
}

// handleCreateSchedule — POST /api/schedules.
// Server assigns the ID; client supplies the rest.
func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	cfg := s.App.Config.Get()
	if apiErr := req.validate(cfg); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sj := core.ScheduledJob{
		ID:              genID(),
		Name:            strings.TrimSpace(req.Name),
		Mode:            req.Mode,
		InstanceID:      req.InstanceID,
		Cron:            strings.TrimSpace(req.Cron),
		Enabled:         req.Enabled,
		Options:         req.Options,
		Filters:         req.Filters,
		AudioTags:       req.AudioTags,
		VideoTags:       req.VideoTags,
		DvDetail:        req.DvDetail,
		MissingEpisodes: req.MissingEpisodes,
		PlexSync:        req.PlexSync,
		ReleaseGroupIDs: req.ReleaseGroupIDs,
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		c.Schedules = append(c.Schedules, sj)
	}); err != nil {
		writeError(w, 500, "save schedule: "+err.Error())
		return
	}
	if s.Scheduler != nil {
		s.Scheduler.Reload()
	}
	writeJSON(w, sj)
}

// handleUpdateSchedule — PUT /api/schedules/{id}.
// Replaces the schedule's editable fields; History is preserved.
func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	cfg := s.App.Config.Get()
	if apiErr := req.validate(cfg); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	found := false
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Schedules {
			if c.Schedules[i].ID != id {
				continue
			}
			found = true
			c.Schedules[i].Name = strings.TrimSpace(req.Name)
			c.Schedules[i].Mode = req.Mode
			c.Schedules[i].InstanceID = req.InstanceID
			c.Schedules[i].Cron = strings.TrimSpace(req.Cron)
			c.Schedules[i].Enabled = req.Enabled
			c.Schedules[i].Options = req.Options
			// Per-rule snapshots: if the client sent them we replace
			// wholesale (this is the path the wizard always takes). If
			// the client sent nil for one of them we leave the existing
			// snapshot in place rather than wiping it — protects the
			// rule from a partial PUT (e.g. a future Basics-only quick-
			// edit) blowing away the user's filter/RG/extra-tag config.
			if req.Filters != nil {
				c.Schedules[i].Filters = req.Filters
			}
			if req.AudioTags != nil {
				c.Schedules[i].AudioTags = req.AudioTags
			}
			if req.VideoTags != nil {
				c.Schedules[i].VideoTags = req.VideoTags
			}
			if req.DvDetail != nil {
				c.Schedules[i].DvDetail = req.DvDetail
			}
			if req.MissingEpisodes != nil {
				c.Schedules[i].MissingEpisodes = req.MissingEpisodes
			}
			if req.PlexSync != nil {
				c.Schedules[i].PlexSync = req.PlexSync
			}
			if req.ReleaseGroupIDs != nil {
				c.Schedules[i].ReleaseGroupIDs = req.ReleaseGroupIDs
			}
			// History intentionally untouched.
			return
		}
	}); err != nil {
		writeError(w, 500, "save schedule: "+err.Error())
		return
	}
	if !found {
		writeError(w, 404, "schedule not found")
		return
	}
	if s.Scheduler != nil {
		s.Scheduler.Reload()
	}
	// Echo the persisted state back.
	cfg = s.App.Config.Get()
	for _, sj := range cfg.Schedules {
		if sj.ID == id {
			writeJSON(w, sj)
			return
		}
	}
	writeError(w, 500, "post-update read failed")
}

// handleDeleteSchedule — DELETE /api/schedules/{id}.
func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	if err := s.App.Config.Update(func(c *core.Config) {
		out := c.Schedules[:0]
		for _, sj := range c.Schedules {
			if sj.ID == id {
				found = true
				continue
			}
			out = append(out, sj)
		}
		c.Schedules = out
	}); err != nil {
		writeError(w, 500, "delete schedule: "+err.Error())
		return
	}
	if !found {
		writeError(w, 404, "schedule not found")
		return
	}
	if s.Scheduler != nil {
		s.Scheduler.Reload()
	}
	w.WriteHeader(204)
}

// handleRunSchedule — POST /api/schedules/{id}/run. Fires the schedule
// once via the same code path the cron loop uses. Returns 202 Accepted
// because the run completes asynchronously — the result will appear in
// the schedule's history once it finishes.
func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil {
		writeError(w, 503, "scheduler not initialized")
		return
	}
	id := r.PathValue("id")
	if err := s.Scheduler.RunNow(id); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
}
