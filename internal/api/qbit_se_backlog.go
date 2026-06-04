package api

// qbit_se_backlog.go — backlog-fix preview/apply for qBit S/E tagging.
// Wizard step 3c "Run backlog fix" button calls these endpoints to
// retroactively tag torrents that were grabbed before the rule
// existed (or before the user's current Episode / Season / Unmatched
// toggles were enabled).
//
// Architecture: NOT a webhook adapter. User-triggered batch operation
// that walks the qBit instance's torrents, classifies each torrent
// name via engine.DetermineQbitTag (three-rule first-match-wins),
// and computes the single proposed tag per torrent. Preview returns
// the action plan; apply runs the same scan + actually adds the tag.
//
// Sonarr-only (validator gates the parent WebhookFnQbitSeTag function
// for "sonarr" appType, and the API endpoints reject non-Sonarr
// rules — defence in depth).
//
// Why parse from name vs Sonarr lookup: simpler + faster. Most qBit
// torrent names are the indexer release-title which carries S0XE0Y
// tokens. Lookup-via-Sonarr-history would require iterating every
// series + per-series history walk for downloadId matches — O(N
// torrents × M series × per-series-history) which is impractical for
// libraries with hundreds of series. Parser-from-name is O(N
// torrents) with cheap regex per torrent. Trade-off documented in
// dev/qbit-se-plan.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/qbit"
)

// qbitSeBacklogScanRequest is the body shape for the backlog-fix
// preview/apply endpoints. Frontend sends rule ID; backend resolves
// QbitSe criteria + qBit instance from the rule. Optional
// CategoryFilter narrows the qBit walk to a specific category (when
// the user maintains Sonarr-grabbed torrents in a dedicated category
// like "sonarr" or "tv-shows"). Empty = consider all torrents.
//
// SelectedHashes (apply pass only) — when non-empty, only torrents
// whose hash appears in this set are eligible for the AddTags call.
// The full preview is still returned so the UI can show the user the
// complete plan with the selected subset highlighted as Applied. An
// empty / omitted SelectedHashes means "apply to every taggable
// item" (legacy preview-then-apply-all behaviour). Preview pass
// ignores this field.
type qbitSeBacklogScanRequest struct {
	RuleID          string   `json:"ruleId"`
	CategoryFilter  string   `json:"categoryFilter,omitempty"`
	SelectedHashes  []string `json:"selectedHashes,omitempty"`
}

// qbitSeBacklogPreviewItem is one row in the preview response —
// represents one torrent that COULD be tagged by the apply pass.
//
// ParsedSeason / ParsedEpisodes are populated when the torrent name
// carries an S/E token (purely informational — the apply path uses
// ProposedTag from the engine classifier). Movies / music / oddly-
// named TV will have ParsedSeason=0 + nil ParsedEpisodes but may
// still have a ProposedTag (the Unmatched bucket).
type qbitSeBacklogPreviewItem struct {
	Hash           string   `json:"hash"`
	TorrentName    string   `json:"torrentName"`
	Category       string   `json:"category,omitempty"`
	CurrentTags    []string `json:"currentTags,omitempty"`
	ParsedSeason   int      `json:"parsedSeason,omitempty"`
	ParsedEpisodes []int    `json:"parsedEpisodes,omitempty"`
	ProposedTag    string   `json:"proposedTag,omitempty"`
	AlreadyTagged  bool     `json:"alreadyTagged"`        // true → apply would no-op
	SkipReason     string   `json:"skipReason,omitempty"` // populated when ProposedTag is empty
}

// qbitSeBacklogPreviewResponse summarises the scan result. Totals
// drive the wizard's preview-row "X torrents to tag" / "Y already
// tagged" / "Z skipped" counters.
//
// TotalCategoryFiltered reports how many torrents the qBit instance
// returned that were dropped by the optional CategoryFilter (the
// per-request "limit to category X" knob). When non-zero the UI
// surfaces this so the user understands why TotalScanned can be much
// larger than the visible plan — without it a user with 5000 torrents
// + a "sonarr" category filter would see "Total scanned: 5000" with
// 4800 invisible.
type qbitSeBacklogPreviewResponse struct {
	Items                 []qbitSeBacklogPreviewItem `json:"items"`
	TotalScanned          int                        `json:"totalScanned"`
	TotalTaggable         int                        `json:"totalTaggable"`         // ProposedTag non-empty + !AlreadyTagged
	TotalAlreadyOK        int                        `json:"totalAlreadyOk"`        // AlreadyTagged
	TotalSkipped          int                        `json:"totalSkipped"`          // ProposedTag empty (no rule matched / all rules disabled)
	TotalCategoryFiltered int                        `json:"totalCategoryFiltered"` // dropped by CategoryFilter pre-classification
}

// qbitSeBacklogApplyResponse summarises an apply pass. Items mirror
// preview; Applied count matches the items where adapter actually
// called qBit AddTags. Errors are per-item — one bad torrent
// doesn't abort the rest of the scan.
type qbitSeBacklogApplyResponse struct {
	Items   []qbitSeBacklogPreviewItem `json:"items"`
	Applied int                        `json:"applied"`
	Failed  int                        `json:"failed"`
	Errors  []string                   `json:"errors,omitempty"`
}

// handleQbitSeBacklogPreview — POST /api/webhook-rules/qbit-se-backlog/preview
//
// Returns the action plan without writing anything to qBit. Used by
// the wizard step 3c preview-modal to show the user what the apply
// pass would do.
func (s *Server) handleQbitSeBacklogPreview(w http.ResponseWriter, r *http.Request) {
	var req qbitSeBacklogScanRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webhookRuleRequestBodyMaxBytes)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	resp, apiErr := s.runQbitSeBacklogScan(r.Context(), req, false)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, resp)
}

// handleQbitSeBacklogApply — POST /api/webhook-rules/qbit-se-backlog/apply
//
// Runs the same scan + actually adds tags via qBit AddTags. Each
// item's apply attempt is independent; per-item errors are recorded
// but don't abort siblings.
func (s *Server) handleQbitSeBacklogApply(w http.ResponseWriter, r *http.Request) {
	var req qbitSeBacklogScanRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webhookRuleRequestBodyMaxBytes)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	resp, apiErr := s.runQbitSeBacklogScan(r.Context(), req, true)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, resp)
}

// runQbitSeBacklogScan is the webhook-rule-scoped entry point: resolve
// the rule + its QbitSe criteria from req.RuleID, then run the shared
// scan. Used by the per-rule "Backlog scan" button on the Webhooks page.
func (s *Server) runQbitSeBacklogScan(ctx context.Context, req qbitSeBacklogScanRequest, apply bool) (any, *apiError) {
	cfg := s.App.Config.Get()
	rule := findWebhookRuleByID(cfg, req.RuleID)
	if rule == nil {
		return nil, newAPIError(404, "webhook rule not found")
	}
	if rule.AppType != "sonarr" {
		return nil, newAPIError(400, "qbit-se backlog requires a Sonarr rule")
	}
	return s.runQbitSeScanWithRules(ctx, cfg, rule.QbitSe, req.CategoryFilter, req.SelectedHashes, apply)
}

// runQbitSeScanWithRules is the shared preview+apply implementation,
// decoupled from persistence — it takes the QbitSe criteria inline so
// the webhook backlog button, the one-off Tag Library run, and (later)
// QFA/Schedule all feed the same scan. apply=false returns a preview;
// apply=true also calls qBit AddTags for each taggable torrent. rules
// is assumed validated (validateQbitSeConfig); the guards here are
// defence-in-depth for direct callers.
func (s *Server) runQbitSeScanWithRules(ctx context.Context, cfg core.Config, rules *core.QbitSeRules, categoryFilterRaw string, selectedHashes []string, apply bool) (any, *apiError) {
	if rules == nil {
		return nil, newAPIError(400, "no QbitSe criteria")
	}
	if !rules.EpisodeEnabled && !rules.SeasonEnabled && !rules.UnmatchedEnabled {
		return nil, newAPIError(400, "every tag rule disabled — enable Episode, Season, or Unmatched before running")
	}

	// Resolve qBit instance.
	qbitInst := findQbitInstanceByID(cfg, rules.QbitInstanceID)
	if qbitInst == nil {
		return nil, newAPIError(400, "rule's qbit instance not found in config")
	}
	client, err := qbit.New(qbit.Config{
		URL:          qbitInst.URL,
		Username:     qbitInst.Username,
		Password:     qbitInst.Password,
		TrustedCerts: qbitInst.TrustedCerts,
	})
	if err != nil {
		return nil, newAPIError(502, "qbit client init: "+err.Error())
	}

	// Walk qBit's library. ListTorrents accepts a state filter;
	// CategoryFilter is post-fetched here (qBit doesn't have a
	// server-side category filter on /torrents/info).
	torrents, err := client.ListTorrents(ctx, "")
	if err != nil {
		return nil, newAPIError(502, "qbit listTorrents: "+err.Error())
	}

	cfgView := engine.QbitSeRulesView{
		EpisodeEnabled:   rules.EpisodeEnabled,
		EpisodeTag:       rules.EpisodeTag,
		SeasonEnabled:    rules.SeasonEnabled,
		SeasonTag:        rules.SeasonTag,
		UnmatchedEnabled: rules.UnmatchedEnabled,
		UnmatchedTag:     rules.UnmatchedTag,
	}
	categoryFilter := strings.TrimSpace(categoryFilterRaw)

	// Build a hash-allowlist set for the apply pass when the caller
	// passed SelectedHashes. nil set = legacy "apply to all taggable"
	// behaviour. Hashes are compared case-insensitively because qBit
	// returns hashes in lowercase but the UI may have cached an
	// uppercase variant from a different endpoint.
	var selectedSet map[string]bool
	if apply && len(selectedHashes) > 0 {
		selectedSet = make(map[string]bool, len(selectedHashes))
		for _, h := range selectedHashes {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" {
				selectedSet[h] = true
			}
		}
	}

	preview := qbitSeBacklogPreviewResponse{}
	var applyItems []qbitSeBacklogPreviewItem
	var errs []string
	applied := 0
	failed := 0

	for _, t := range torrents {
		preview.TotalScanned++
		if categoryFilter != "" && !strings.EqualFold(t.Category, categoryFilter) {
			preview.TotalCategoryFiltered++
			continue
		}
		item := qbitSeBacklogPreviewItem{
			Hash:        t.Hash,
			TorrentName: t.Name,
			Category:    t.Category,
			CurrentTags: splitQbitTags(t.Tags),
		}
		// Informational parse — purely for UI display ("parsed season X
		// / episodes [...]"). The classification path uses
		// DetermineQbitTag below which is name-based + first-match-wins.
		if seasonNum, eps, ok := engine.ParseSeasonEpisodeFromTitle(t.Name); ok {
			item.ParsedSeason = seasonNum
			item.ParsedEpisodes = eps
		}

		// Classify via engine — single-tag winner per Episode → Season
		// → Unmatched first-match-wins.
		proposed := engine.DetermineQbitTag(t.Name, cfgView)
		if proposed == "" {
			item.SkipReason = "no rule matched (every classifier disabled, or matching rule is disabled)"
			preview.TotalSkipped++
			preview.Items = append(preview.Items, item)
			continue
		}
		item.ProposedTag = proposed

		// AlreadyTagged: the proposed tag is already on the torrent.
		if hasAllTags(item.CurrentTags, []string{proposed}) {
			item.AlreadyTagged = true
			preview.TotalAlreadyOK++
			preview.Items = append(preview.Items, item)
			continue
		}

		preview.TotalTaggable++
		preview.Items = append(preview.Items, item)
		if apply {
			// When the caller supplied a SelectedHashes filter, only
			// apply tags to hashes the user explicitly checked in the
			// preview UI. Otherwise (legacy callers / apply-all path)
			// every taggable item gets applied.
			if selectedSet == nil || selectedSet[strings.ToLower(item.Hash)] {
				applyItems = append(applyItems, item)
			}
		}
	}

	if !apply {
		return preview, nil
	}

	// Apply pass — add tags per item. Per-item errors collected but
	// don't abort siblings. Check ctx between iterations so a
	// cancelled receiver context (browser-tab-close mid-apply)
	// short-circuits cleanly without piling up "context canceled"
	// errors per remaining item.
	for _, item := range applyItems {
		select {
		case <-ctx.Done():
			errs = append(errs, fmt.Sprintf("apply cancelled after %d items: %v", applied+failed, ctx.Err()))
			return qbitSeBacklogApplyResponse{
				Items:   preview.Items,
				Applied: applied,
				Failed:  failed,
				Errors:  errs,
			}, nil
		default:
		}
		if err := client.AddTags(ctx, []string{item.Hash}, []string{item.ProposedTag}); err != nil {
			failed++
			errs = append(errs, fmt.Sprintf("%s: %v", item.TorrentName, err))
			continue
		}
		applied++
	}
	return qbitSeBacklogApplyResponse{
		Items:   preview.Items,
		Applied: applied,
		Failed:  failed,
		Errors:  errs,
	}, nil
}

// validateQbitSeConfig validates + canonicalises a QbitSe criteria
// block. Shared by the webhook-rule validator and the one-off run
// endpoint so every context enforces identical rules. Trims + defaults
// each enabled tag name in place and rejects unknown qBit instances.
func validateQbitSeConfig(qse *core.QbitSeRules, cfg core.Config) *apiError {
	if qse == nil {
		return newAPIError(400, "qbitSe rules required when qbitSeTag function is enabled")
	}
	if !qse.EpisodeEnabled && !qse.SeasonEnabled && !qse.UnmatchedEnabled {
		return newAPIError(400, "qbitSe must enable at least one of episodeEnabled / seasonEnabled / unmatchedEnabled")
	}
	if qse.QbitInstanceID == "" {
		return newAPIError(400, "qbitSe.qbitInstanceId is required")
	}
	if !qbitInstanceExists(cfg, qse.QbitInstanceID) {
		return newAPIError(400, "qbitSe.qbitInstanceId not found")
	}
	if qse.EpisodeEnabled {
		qse.EpisodeTag = strings.TrimSpace(qse.EpisodeTag)
		if qse.EpisodeTag == "" {
			qse.EpisodeTag = "Episode"
		}
		if !reTagName.MatchString(strings.ToLower(qse.EpisodeTag)) {
			return newAPIError(400, "qbitSe.episodeTag must be letters, digits, underscores, or dashes")
		}
	}
	if qse.SeasonEnabled {
		qse.SeasonTag = strings.TrimSpace(qse.SeasonTag)
		if qse.SeasonTag == "" {
			qse.SeasonTag = "Season"
		}
		if !reTagName.MatchString(strings.ToLower(qse.SeasonTag)) {
			return newAPIError(400, "qbitSe.seasonTag must be letters, digits, underscores, or dashes")
		}
	}
	if qse.UnmatchedEnabled {
		qse.UnmatchedTag = strings.TrimSpace(qse.UnmatchedTag)
		if qse.UnmatchedTag == "" {
			qse.UnmatchedTag = "Unmatched"
		}
		if !reTagName.MatchString(strings.ToLower(qse.UnmatchedTag)) {
			return newAPIError(400, "qbitSe.unmatchedTag must be letters, digits, underscores, or dashes")
		}
	}
	return nil
}

// qbitSeRunRequest is the body for the one-off qBit S/E run (Tag
// Library sub-tab; QFA/Schedule reuse the same inline config later).
// Unlike the webhook backlog request it carries the QbitSe criteria
// inline rather than a rule ID, matching the one-off model: the Tag
// Library surface owns its config and nothing is saved.
type qbitSeRunRequest struct {
	QbitSe         *core.QbitSeRules `json:"qbitSe"`
	CategoryFilter string            `json:"categoryFilter,omitempty"`
	SelectedHashes []string          `json:"selectedHashes,omitempty"`
}

// handleQbitSeRunPreview — POST /api/qbit-se/run/preview
// One-off preview from inline QbitSe config (no saved rule).
func (s *Server) handleQbitSeRunPreview(w http.ResponseWriter, r *http.Request) {
	var req qbitSeRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webhookRuleRequestBodyMaxBytes)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	if e := validateQbitSeConfig(req.QbitSe, cfg); e != nil {
		writeAPIError(w, e)
		return
	}
	resp, apiErr := s.runQbitSeScanWithRules(r.Context(), cfg, req.QbitSe, req.CategoryFilter, nil, false)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, resp)
}

// handleQbitSeRunApply — POST /api/qbit-se/run/apply
// One-off apply from inline QbitSe config. SelectedHashes narrows the
// apply to the checked subset (empty = every taggable torrent).
func (s *Server) handleQbitSeRunApply(w http.ResponseWriter, r *http.Request) {
	var req qbitSeRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webhookRuleRequestBodyMaxBytes)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	if e := validateQbitSeConfig(req.QbitSe, cfg); e != nil {
		writeAPIError(w, e)
		return
	}
	resp, apiErr := s.runQbitSeScanWithRules(r.Context(), cfg, req.QbitSe, req.CategoryFilter, req.SelectedHashes, true)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, resp)
}

// findWebhookRuleByID is a small helper used by the backlog endpoints.
// Returns nil when the rule isn't found in cfg.WebhookRules.
func findWebhookRuleByID(cfg core.Config, id string) *core.WebhookRule {
	for i := range cfg.WebhookRules {
		if cfg.WebhookRules[i].ID == id {
			return &cfg.WebhookRules[i]
		}
	}
	return nil
}

// splitQbitTags converts qBit's comma-separated tag string into a
// slice. Empty input returns empty slice.
func splitQbitTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// hasAllTags returns true when every wanted tag is present in current
// (case-insensitive). Used by preview to decide AlreadyTagged.
//
// Case-insensitive is intentional: qBit's tag matching is case-
// insensitive even though it preserves user-typed casing in storage.
// User-applied "s01" prevents adapter from creating a separate "S01"
// duplicate (and vice-versa). When a real diff exists (proposed tag
// completely absent), apply still adds with the adapter's
// canonical "S01" / "S01E05" casing.
func hasAllTags(current, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	currentLower := make(map[string]bool, len(current))
	for _, c := range current {
		currentLower[strings.ToLower(c)] = true
	}
	for _, w := range wanted {
		if !currentLower[strings.ToLower(w)] {
			return false
		}
	}
	return true
}
