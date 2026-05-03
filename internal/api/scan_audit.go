// Audit logging for /api/scan/run handlers. Each handler calls auditScan
// once after the response is built, with action + mode + counts so the
// runs.log file carries one structured line per scan operation. Schedule
// runs go through the same helper from the scheduler runner with
// source="schedule:<id>".
//
// Format target: human-grep-friendly key=val pairs, fixed-order so users
// can eyeball columns. "items=" carries the per-action universe count
// (movies for Radarr, series for Sonarr) so a tail across mixed-instance
// runs reads consistently:
//   scan-tag: caller=adhoc instance=radarr type=radarr mode=preview items=1077 add=462 remove=5 keep=200 nofile=12
//   scan-tag: caller=adhoc instance=radarr type=radarr mode=apply items=1077 add=462 remove=5 keep=200 created=4
//   scan-discover: caller=adhoc instance=radarr type=radarr mode=preview items=1077 discovered=15 written=10
//   scan-recover: caller=adhoc instance=sonarr-name type=sonarr mode=preview items=152 affected=12 wouldfix=8 nohistory=2
//   scan-cleanup: caller=adhoc instance=radarr type=radarr mode=apply items=0 unused=3 deleted=3

package api

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"resolvarr/internal/core"
)

// auditScan emits one audit line summarising a scan-run. Caller passes
// the call-context (adhoc / schedule:<id>), the action, the responding
// instance, and the response itself; the helper picks fields per action.
//
// Line shape: `LEVEL  scan-<action>: caller=<context> instance=<name>
// type=<radarr|sonarr> mode=<preview|apply> ...`. Action leads as the
// log-source so a tail naturally reads "scan-tag" at the action column.
//
// errMsg may be empty (success) or a non-empty error string. On error
// the line is tagged ERROR and counts are omitted (resp may be nil).
func (s *Server) auditScan(source, action string, inst *core.Instance, req scanRunRequest, resp *scanResponse, errMsg string) {
	if s.App == nil || s.App.RunLog == nil {
		return
	}
	logSource := "scan-" + action
	instanceName := ""
	instanceType := ""
	if inst != nil {
		instanceName = inst.Name
		instanceType = inst.Type
	}
	fields := []string{
		"caller=" + kvEscape(source),
		"instance=" + kvEscape(instanceName),
		"type=" + instanceType,
		"mode=" + req.Mode,
	}
	if errMsg != "" {
		fields = append(fields, "ERROR", "err="+kvEscape(errMsg))
		s.App.RunLog.Audit(logSource, "", fields...)
		return
	}
	if resp == nil {
		s.App.RunLog.Audit(logSource, "", fields...)
		return
	}
	// "items" rather than "movies" — the same Totals.Items field carries
	// movie count for Radarr and series count for Sonarr; the audit line
	// stays neutral so a tail across mixed instances reads consistently.
	fields = append(fields, "items="+itoa(resp.Totals.Items))

	switch action {
	case "tag":
		fields = append(fields,
			"add="+itoa(resp.Totals.ToAdd),
			"remove="+itoa(resp.Totals.ToRemove),
			"keep="+itoa(resp.Totals.ToKeep),
			"nofile="+itoa(resp.Totals.NoFile),
		)
		if resp.Applied != nil {
			fields = append(fields,
				"created="+itoa(len(resp.Applied.TagsCreated)),
				"itemsadded="+itoa(resp.Applied.ItemsAdded),
				"itemsremoved="+itoa(resp.Applied.ItemsRemoved),
			)
			if len(resp.Applied.TagsDeleted) > 0 {
				fields = append(fields, "tagsdeleted="+itoa(len(resp.Applied.TagsDeleted)))
			}
			if resp.Applied.Secondary != nil {
				fields = append(fields,
					"secondary="+kvEscape(resp.Applied.Secondary.InstanceName),
					"sec_created="+itoa(len(resp.Applied.Secondary.TagsCreated)),
					"sec_added="+itoa(resp.Applied.Secondary.ItemsAdded),
					"sec_removed="+itoa(resp.Applied.Secondary.ItemsRemoved),
				)
			}
		}
	case "discover":
		fields = append(fields, "discovered="+itoa(resp.Totals.Discovered))
		if resp.Applied != nil {
			fields = append(fields, "written="+itoa(len(resp.Applied.DiscoverAdded)))
		}
	case "recover":
		fields = append(fields,
			"affected="+itoa(resp.Totals.RecoverAffected),
			"wouldfix="+itoa(resp.Totals.RecoverWouldFix),
			"flagged="+itoa(resp.Totals.RecoverFlagged),
			"nohistory="+itoa(resp.Totals.RecoverNoHistory),
			"norlsgroup="+itoa(resp.Totals.RecoverNoGroup),
			"failedverify="+itoa(resp.Totals.RecoverFailedVerify),
		)
		if req.Mode == "apply" {
			fields = append(fields,
				"fixed="+itoa(resp.Totals.RecoverFixed),
				"fixfailed="+itoa(resp.Totals.RecoverFixFailed),
				"renamefailed="+itoa(resp.Totals.RecoverRenameFailed),
			)
		}
	case "cleanup":
		fields = append(fields, "unused="+itoa(len(resp.Totals.TagsToDelete)))
		if resp.Applied != nil {
			fields = append(fields, "deleted="+itoa(len(resp.Applied.TagsDeleted)))
		}
	case "audiotags", "videotags":
		fields = append(fields,
			"add="+itoa(resp.Totals.ToAdd),
			"remove="+itoa(resp.Totals.ToRemove),
			"keep="+itoa(resp.Totals.ToKeep),
		)
		if resp.Totals.MissingMediaInfo > 0 {
			fields = append(fields, "missing_mediainfo="+itoa(resp.Totals.MissingMediaInfo))
		}
		if resp.Applied != nil {
			fields = append(fields,
				"created="+itoa(len(resp.Applied.TagsCreated)),
				"itemsadded="+itoa(resp.Applied.ItemsAdded),
				"itemsremoved="+itoa(resp.Applied.ItemsRemoved),
			)
		}
	case "dvdetail":
		// DV detail's pipeline has more interesting counters than the
		// other auto-tag scans — extraction is slow and externally
		// gated (ffmpeg+dovi_tool), so per-bucket cache + extraction
		// stats are what users want when troubleshooting low coverage.
		fields = append(fields,
			"add="+itoa(resp.Totals.ToAdd),
			"remove="+itoa(resp.Totals.ToRemove),
			"keep="+itoa(resp.Totals.ToKeep),
			"dv_candidates="+itoa(resp.Totals.DvCandidates),
			"dv_noncandidates="+itoa(resp.Totals.DvNonCandidates),
			"dv_cache_hits="+itoa(resp.Totals.DvCacheHits),
			"dv_extracted="+itoa(resp.Totals.DvExtracted),
			"dv_extracted_norpu="+itoa(resp.Totals.DvExtractedNoRpu),
			"dv_extract_failed="+itoa(resp.Totals.DvExtractFailed),
		)
		if resp.Applied != nil {
			fields = append(fields,
				"created="+itoa(len(resp.Applied.TagsCreated)),
				"itemsadded="+itoa(resp.Applied.ItemsAdded),
				"itemsremoved="+itoa(resp.Applied.ItemsRemoved),
			)
		}
	}
	s.App.RunLog.Audit(logSource, "", fields...)
}

// auditSource returns "adhoc" for direct /api/scan/run calls (the
// AuditSource field is empty by JSON default) and the explicit label
// for scheduler-fired runs ("schedule:<id>").
func (req scanRunRequest) auditSource() string {
	if req.AuditSource == "" {
		return "adhoc"
	}
	return req.AuditSource
}

// errMsgOf is the apiError → string convenience helper used by audit
// callers that need an empty string on success and the message on
// failure. Avoids a 3-line if at every call site.
func errMsgOf(e *apiError) string {
	if e == nil {
		return ""
	}
	return e.Message
}

// dumpScanJSON writes the full scanResponse to a timestamped file
// under /config/logs/scan-{action}-YYYYMMDD-HHMMSS.json so users have
// a deep-diff-able artifact per adhoc run (schedule runs already get
// a per-run .log + .json via the scheduler). Returns the absolute
// path (or "" on failure). Best-effort — a failure to write
// surfaces to stderr but doesn't block the API response.
//
// Retention follows runs.log's KeepDays config (default 14) — see
// pruneOldArchives in core/runlog.go which now sweeps scan-*.json
// alongside dated runs-YYYYMMDD.log files.
func (s *Server) dumpScanJSON(action string, resp *scanResponse) string {
	if resp == nil {
		return ""
	}
	dir := "/config/logs"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "tagarr: scan dump mkdir: %v\n", err)
		return ""
	}
	ts := time.Now().Format("20060102-150405")
	path := fmt.Sprintf("%s/scan-%s-%s.json", dir, action, ts)
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tagarr: scan dump marshal: %v\n", err)
		return ""
	}
	// Atomic write: stage to .tmp then rename. Avoids handleScanHistory
	// reading a half-written file and silently dropping its preview
	// fields when the process gets killed mid-write.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tagarr: scan dump write: %v\n", err)
		return ""
	}
	if err := os.Rename(tmp, path); err != nil {
		fmt.Fprintf(os.Stderr, "tagarr: scan dump rename: %v\n", err)
		_ = os.Remove(tmp)
		return ""
	}
	return path
}

// itoa is a local fmt.Sprintf("%d", n) shortcut to keep the audit-line
// builder readable. Hot path is microseconds either way.
func itoa(n int) string { return fmt.Sprintf("%d", n) }

// kvEscape replaces space, quote, and '=' in field values with '_' so
// the line stays grep-able as space-separated key=val pairs. '=' would
// otherwise produce ambiguous parses (e.g. an Arr error like
// "X-Api-Key=invalid" landing in the err= field). Audit lines aren't
// structured JSON — they're meant to be eyeballed.
func kvEscape(s string) string {
	if s == "" {
		return "-"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '"' || c == '\n' || c == '\t' || c == '=' {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
