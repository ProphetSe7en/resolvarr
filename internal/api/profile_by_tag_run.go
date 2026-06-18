package api

// profile_by_tag_run.go — one-off Library-scan run for Profile by tag.
//
//   GET  /api/instances/{id}/quality-profiles   picker source
//   POST /api/profile-by-tag/run                 preview / apply
//
// Phase 1: scan one-off only (Radarr + Sonarr). Nothing is persisted — the
// rules are supplied inline. QFA / Schedule / Webhook contexts come in later
// phases and will reuse engine.PlanProfileMoves + the apply path here.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// profileRunTimeout bounds a full library walk + a few editor PUTs.
const profileRunTimeout = 5 * time.Minute

// handleListQualityProfiles — GET /api/instances/{id}/quality-profiles.
// Powers the profile picker in the Profile-by-tag wizard. Mirrors handleListTags.
func (s *Server) handleListQualityProfiles(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	profiles, err := client.ListQualityProfiles(ctx, inst.Type)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, profiles)
}

// profileByTagMove is one applied/previewed profile change for the result panel.
type profileByTagMove struct {
	ItemID      int    `json:"itemId"`
	Title       string `json:"title"`
	Year        int    `json:"year,omitempty"`
	FromProfile string `json:"fromProfile"`
	ToProfile   string `json:"toProfile"`
	ToProfileID int    `json:"toProfileId"`
	MatchedRule string `json:"matchedRule"`
	Applied     bool   `json:"applied"`
}

// profileByTagConflict is an item matching rules that disagree on the profile.
type profileByTagConflict struct {
	ItemID     int      `json:"itemId"`
	Title      string   `json:"title"`
	Year       int      `json:"year,omitempty"`
	Candidates []string `json:"candidates"`
}

// profileByTagRun is the result payload (also embedded in the history dump).
type profileByTagRun struct {
	RunMode    string                 `json:"runMode"`
	ItemsTotal int                    `json:"itemsTotal"`
	Moves      []profileByTagMove     `json:"moves"`
	Conflicts  []profileByTagConflict `json:"conflicts"`
	Status     string                 `json:"status"`
	Error      string                 `json:"error,omitempty"`
}

// handleRunProfileByTag — POST /api/profile-by-tag/run.
func (s *Server) handleRunProfileByTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ArrInstanceID string               `json:"arrInstanceId"`
		RunMode       string               `json:"runMode,omitempty"`
		Rules         []engine.ProfileRule `json:"rules"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	runMode := req.RunMode
	if runMode == "" {
		runMode = "preview"
	}
	if runMode != "apply" && runMode != "preview" {
		writeError(w, 400, `runMode must be "apply" or "preview"`)
		return
	}
	if len(req.Rules) == 0 {
		writeError(w, 400, "at least one rule is required")
		return
	}

	cfg := s.App.Config.Get()
	inst := findInstanceByID(cfg, req.ArrInstanceID)
	if inst == nil {
		writeError(w, 404, "Arr instance not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), profileRunTimeout)
	defer cancel()
	client := s.arrClientFor(inst)

	// Profiles — for id->name and to validate each rule's target exists.
	profiles, err := client.ListQualityProfiles(ctx, inst.Type)
	if err != nil {
		writeError(w, 502, "list quality profiles: "+err.Error())
		return
	}
	profileName := make(map[int]string, len(profiles))
	for _, p := range profiles {
		profileName[p.ID] = p.Name
	}
	for i, rule := range req.Rules {
		if len(rule.Conditions) == 0 {
			writeError(w, 400, fmt.Sprintf("rule %d has no conditions", i+1))
			return
		}
		if _, ok := profileName[rule.ProfileID]; !ok {
			writeError(w, 400, fmt.Sprintf("rule %d targets a profile that does not exist on this instance", i+1))
			return
		}
	}

	// Tags — for human-readable rule descriptions in the result.
	tagLabel := map[int]string{}
	if details, terr := client.ListTagDetails(ctx); terr == nil {
		for _, d := range details {
			tagLabel[d.ID] = d.Label
		}
	}

	// Items — the library walk.
	items, err := client.ListItems(ctx, inst.Type)
	if err != nil {
		writeError(w, 502, "list items: "+err.Error())
		return
	}
	engItems := make([]engine.ProfileItem, 0, len(items))
	for _, it := range items {
		engItems = append(engItems, engine.ProfileItem{
			ID:               it.ID,
			Title:            it.Title,
			Year:             it.Year,
			Tags:             it.Tags,
			CurrentProfileID: it.QualityProfileID,
		})
	}

	moves, conflicts := engine.PlanProfileMoves(engItems, req.Rules)

	run := profileByTagRun{RunMode: runMode, ItemsTotal: len(items), Status: "ok"}
	for _, m := range moves {
		run.Moves = append(run.Moves, profileByTagMove{
			ItemID:      m.ItemID,
			Title:       m.Title,
			Year:        m.Year,
			FromProfile: profileNameOr(profileName, m.FromProfileID),
			ToProfile:   profileNameOr(profileName, m.ToProfileID),
			ToProfileID: m.ToProfileID,
			MatchedRule: describeProfileRule(req.Rules[m.MatchedRuleIndex], tagLabel),
		})
	}
	for _, c := range conflicts {
		names := make([]string, 0, len(c.CandidateProfileIDs))
		for _, pid := range c.CandidateProfileIDs {
			names = append(names, profileNameOr(profileName, pid))
		}
		run.Conflicts = append(run.Conflicts, profileByTagConflict{
			ItemID: c.ItemID, Title: c.Title, Year: c.Year, Candidates: names,
		})
	}

	// Apply: batch by target profile via the editor.
	if runMode == "apply" && len(moves) > 0 {
		byProfile := map[int][]int{}
		for _, m := range moves {
			byProfile[m.ToProfileID] = append(byProfile[m.ToProfileID], m.ItemID)
		}
		applied := map[int]bool{}
		for pid, ids := range byProfile {
			if aerr := client.EditorApplyProfile(ctx, inst.Type, ids, pid); aerr != nil {
				run.Status = "error"
				run.Error = fmt.Sprintf("apply profile %s: %v", profileNameOr(profileName, pid), aerr)
				break
			}
			for _, id := range ids {
				applied[id] = true
			}
		}
		for i := range run.Moves {
			run.Moves[i].Applied = applied[run.Moves[i].ItemID]
		}
	}

	s.dumpProfileByTagJSON(run, inst)
	writeJSON(w, run)
}

func profileNameOr(m map[int]string, id int) string {
	if n, ok := m[id]; ok {
		return n
	}
	if id == 0 {
		return "(none)"
	}
	return "profile " + strconv.Itoa(id)
}

// describeProfileRule renders a rule as e.g. "anime AND uhd" / "anime OR cartoon"
// using tag labels (falls back to the raw value when a label is unknown).
func describeProfileRule(rule engine.ProfileRule, tagLabel map[int]string) string {
	var b strings.Builder
	for i, c := range rule.Conditions {
		if i > 0 {
			if strings.EqualFold(strings.TrimSpace(c.Join), "or") {
				b.WriteString(" OR ")
			} else {
				b.WriteString(" AND ")
			}
		}
		if c.Not {
			b.WriteString("NOT ")
		}
		label := c.Value
		if c.Type == "tag" {
			if id, err := strconv.Atoi(strings.TrimSpace(c.Value)); err == nil {
				if l, ok := tagLabel[id]; ok {
					label = l
				}
			}
		}
		b.WriteString(label)
	}
	return b.String()
}

// profileByTagDumpFile mirrors scanResponse's top-level shape so the generic
// History-row preview works without special-casing; the run hangs off .run.
type profileByTagDumpFile struct {
	Mode     string `json:"mode"`
	Instance struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"instance"`
	Totals struct {
		Items int `json:"items"`
	} `json:"totals"`
	Run profileByTagRun `json:"run"`
}

// dumpProfileByTagJSON writes the run to /config/logs/scan-profilebytag-{ts}.json.
func (s *Server) dumpProfileByTagJSON(run profileByTagRun, inst *core.Instance) string {
	if inst == nil {
		return ""
	}
	var dump profileByTagDumpFile
	dump.Mode = run.RunMode
	dump.Instance.ID = inst.ID
	dump.Instance.Name = inst.Name
	dump.Instance.Type = inst.Type
	dump.Totals.Items = run.ItemsTotal
	dump.Run = run

	dir := "/config/logs"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: profilebytag dump mkdir: %v\n", err)
		return ""
	}
	path := fmt.Sprintf("%s/scan-profilebytag-%s.json", dir, time.Now().Format("20060102-150405"))
	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return ""
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return ""
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return ""
	}
	return path
}
