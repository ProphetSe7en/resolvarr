package api

// webhook_grab_rename.go — Grab Rename adapter for the M-Webhook
// dispatcher. Fires on Connect Grab events and renames the qBit
// torrent display name to a parser-friendly form so Radarr/Sonarr's
// import-time scoring sees the full indexer release-title (with all
// CF-relevant tokens) rather than the tracker-stripped torrent name.
//
// Architectural rule 1 (engine-only decisions): every token-match
// goes through engine.* helpers (DiffMissingMovieVersions /
// DiffMissingSources / DiffMissingAudio / MatchCustomTokens /
// IsSceneNamingPattern / IsKnownSceneGroup / ParseReleaseGroupFromFilename
// / ParseReleaseGroupTolerant / NormalizeRgSegment). NO inline
// substring/regex matching in this file.
//
// Architectural rule 2 (single-item scope): one Grab event = one
// torrent. GetTorrent + RenameTorrent are O(1) round-trips against
// qBit. NEVER walk all torrents, NEVER fan out across the qBit
// library.
//
// v1: torrent display rename only. File rename (task #8b) lands if
// torrent rename proves insufficient on real-world testing.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/qbit"
)

// grabEventPayload — Connect Grab event shape. Sonarr/Radarr both POST
// a `release` block; Sonarr also includes `episodes[]` and `series`
// where Radarr has `movie`. The adapter reads release.releaseTitle +
// release.releaseGroup + downloadId; movie/series IDs are unused
// (rename is qBit-side only — Arr-side IDs aren't needed).
type grabEventPayload struct {
	Release struct {
		ReleaseTitle  string `json:"releaseTitle"`
		ReleaseGroup  string `json:"releaseGroup"`
		Indexer       string `json:"indexer,omitempty"`
		Size          int64  `json:"size,omitempty"`
	} `json:"release"`
	DownloadID     string `json:"downloadId,omitempty"`
	DownloadClient string `json:"downloadClient,omitempty"`

	// Movie carried for log-context (Radarr Grab).
	Movie *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Year  int    `json:"year,omitempty"`
	} `json:"movie,omitempty"`

	// Series + Episodes (Sonarr Grab). Episodes is the per-grab
	// episode list — single-element for one episode, multi-element
	// for multi-ep releases (S01E05E06) or season packs (Sonarr emits
	// each episode's id/numbers). The qBit S/E tag adapter consumes
	// Episodes; Grab Rename ignores them.
	Series *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	} `json:"series,omitempty"`
	Episodes []struct {
		ID            int `json:"id"`
		EpisodeNumber int `json:"episodeNumber"`
		SeasonNumber  int `json:"seasonNumber"`
	} `json:"episodes,omitempty"`
}

// dispatchGrabRename runs the single-torrent rename flow on Connect
// Grab events. Idempotent (qBit returns 200 on no-op rename; we
// pre-check by computing target via NormalizeRgSegment + skipping
// when current == target).
//
// Trigger model: at least one enabled trigger must yield a diff
// between current torrent name and grab title (or TriggerAlways=true)
// for rename to fire. Triggers are OR'd; a single matching trigger
// is enough.
//
// Returns OK=true with descriptive summary on every clean path
// (skip-due-to-* / rename-applied). OK=false only on actual failures
// (qBit unreachable, malformed payload, missing qBit-instance config).
func (s *Server) dispatchGrabRename(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventGrab) {
		return functionResult{Function: core.WebhookFnGrabRename, OK: true, Summary: "skipped (not a Grab event)"}
	}
	if rule.GrabRename == nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "rule has GrabRename function but no criteria struct"}
	}
	criteria := rule.GrabRename

	var payload grabEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "decode payload failed", Err: err}
	}

	grabTitle := strings.TrimSpace(payload.Release.ReleaseTitle)
	if grabTitle == "" {
		return functionResult{Function: core.WebhookFnGrabRename, OK: true, Summary: "skipped (no release.releaseTitle on event)"}
	}
	hash := strings.TrimSpace(payload.DownloadID)
	if hash == "" {
		return functionResult{Function: core.WebhookFnGrabRename, OK: true, Summary: "skipped (no downloadId on event — manual grab?)"}
	}

	// Resolve releaseGroup. Primary: Arr's pre-parsed release.releaseGroup.
	// Fallback: parse the indexer release-title via ParseReleaseGroupTolerant
	// (catches the Rango/Matilda failure mode where Arr's parser bombed
	// on " - <RG>" but the title still has the rg in extractable form).
	rg := strings.TrimSpace(payload.Release.ReleaseGroup)
	if rg == "" {
		if extracted, ok := engine.ParseReleaseGroupTolerant(grabTitle); ok {
			rg = extracted
		}
	}

	// Group blocklist — never rename for these RG names. Case-insensitive.
	if rg != "" {
		rgLower := strings.ToLower(rg)
		for _, blocked := range criteria.GroupBlocklist {
			if strings.ToLower(strings.TrimSpace(blocked)) == rgLower {
				return functionResult{
					Function: core.WebhookFnGrabRename, OK: true,
					Summary: fmt.Sprintf("skipped (release group %q is on the blocklist)", rg),
				}
			}
		}
	}

	// Resolve qBit instance.
	qbitInst := findQbitInstanceByID(cfg, criteria.QbitInstanceID)
	if qbitInst == nil {
		return functionResult{
			Function: core.WebhookFnGrabRename, OK: false,
			Summary: fmt.Sprintf("qbit instance %q not found in config", criteria.QbitInstanceID),
		}
	}
	client, err := qbit.New(qbit.Config{
		URL:          qbitInst.URL,
		Username:     qbitInst.Username,
		Password:     qbitInst.Password,
		TrustedCerts: qbitInst.TrustedCerts,
	})
	if err != nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "qbit client init", Err: err}
	}

	// Fetch current torrent. qBit may not have indexed yet (race with
	// /torrents/add). Mirror bash's retry-with-backoff (line 217-225).
	current, found, err := waitForTorrent(ctx, client, hash)
	if err != nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "qbit GetTorrent", Err: err}
	}
	if !found {
		return functionResult{
			Function: core.WebhookFnGrabRename, OK: true,
			Summary: fmt.Sprintf("skipped (torrent hash %s not in qbit after retries — already removed?)", hash),
		}
	}
	currentName := current.Name
	if currentName == grabTitle {
		return functionResult{Function: core.WebhookFnGrabRename, OK: true, Summary: "skipped (torrent name already equals grab title)"}
	}

	// Trigger evaluation — collect reasons; rename fires when ≥1 trigger
	// has a diff (or TriggerAlways=true).
	reasons := evaluateGrabRenameTriggers(currentName, grabTitle, rg, criteria)
	if criteria.TriggerAlways && len(reasons) == 0 {
		reasons = append(reasons, "always-rename")
	}
	if len(reasons) == 0 {
		return functionResult{Function: core.WebhookFnGrabRename, OK: true, Summary: "skipped (no enabled trigger detected a diff)"}
	}

	// Build parser-friendly target. NormalizeRgSegment handles both
	// the Rango " - SumVision" → "-SumVision" case AND the bash
	// trailing-junk strip ("-126811 x ATM05" → "-126811").
	target := engine.NormalizeRgSegment(grabTitle, rg)
	if target == currentName {
		return functionResult{
			Function: core.WebhookFnGrabRename, OK: true,
			Summary: fmt.Sprintf("skipped (target equals current after normalisation; triggers fired: %s)", strings.Join(reasons, ", ")),
		}
	}

	// Apply rename.
	if err := client.RenameTorrent(ctx, hash, target); err != nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "qbit rename", Err: err}
	}
	return functionResult{
		Function: core.WebhookFnGrabRename, OK: true,
		Summary: fmt.Sprintf("renamed → %q (triggers: %s)", target, strings.Join(reasons, ", ")),
	}
}

// evaluateGrabRenameTriggers walks each enabled trigger and returns a
// label-list of triggers that detected a diff. Pure function — testable
// without qBit involvement.
//
// Evaluated in canonical order (matches what the user toggles in the
// wizard): missing-rg → movie-version → source → audio → scene →
// custom-tokens. Order doesn't affect rename outcome (any trigger is
// enough); it affects summary readability.
func evaluateGrabRenameTriggers(currentName, grabTitle, rg string, c *core.GrabRenameCriteria) []string {
	if c == nil {
		return nil
	}
	var reasons []string

	// Missing release group — uses Radarr's strict filename parser to
	// answer "would Radarr extract rg from the current name?" If parser
	// returns rg → no diff. If returns different value or empty → diff.
	if c.TriggerOnMissingReleaseGroup && rg != "" {
		got, ok, _ := engine.ParseReleaseGroupFromFilename(currentName)
		if !ok || !strings.EqualFold(got, rg) {
			reasons = append(reasons, "missing-release-group")
		}
	}

	if c.TriggerOnMovieVersionMismatch {
		if missing := engine.DiffMissingMovieVersions(currentName, grabTitle); len(missing) > 0 {
			reasons = append(reasons, "movie-version: "+strings.Join(missing, "/"))
		}
	}

	if c.TriggerOnSourceMismatch {
		if missing := engine.DiffMissingSources(currentName, grabTitle); len(missing) > 0 {
			reasons = append(reasons, "source: "+strings.Join(missing, "/"))
		}
	}

	if c.TriggerOnAudioMismatch {
		if missing := engine.DiffMissingAudio(currentName, grabTitle); len(missing) > 0 {
			reasons = append(reasons, "audio: "+strings.Join(missing, "/"))
		}
	}

	// Scene mismatch — nuanced. Fire when current looks scene-stripped
	// AND rg is NOT a known scene group (legit scene releases keep
	// their name). Replaces bash's blanket ExcludeSceneReleases skip.
	if c.TriggerOnSceneMismatch {
		if engine.IsSceneNamingPattern(currentName) && !engine.IsKnownSceneGroup(rg) {
			reasons = append(reasons, "scene-stripped (rg not a known scene group)")
		}
	}

	// Custom tokens — compile fresh per fire. Validator at save-time
	// guarantees compilable, but defence-in-depth: skip uncompilable
	// entries silently (rule's other triggers still fire).
	if len(c.CustomTokens) > 0 {
		compiled := compileCustomTokens(c.CustomTokens)
		if missing := engine.MatchCustomTokens(currentName, grabTitle, compiled); len(missing) > 0 {
			reasons = append(reasons, "custom: "+strings.Join(missing, "/"))
		}
	}

	return reasons
}

// compileCustomTokens converts the rule's user-supplied "Label:regex"
// pairs into compiled regex form for engine.MatchCustomTokens. Skip-
// uncompilable behaviour matches the validator's "fail-soft on
// runtime-bad-regex" semantics — rule's other triggers still get
// evaluated.
func compileCustomTokens(tokens []core.GrabRenameCustomToken) []engine.CompiledCustomToken {
	out := make([]engine.CompiledCustomToken, 0, len(tokens))
	for _, t := range tokens {
		re, err := regexp.Compile("(?i)" + t.Regex)
		if err != nil {
			continue
		}
		out = append(out, engine.CompiledCustomToken{Label: t.Label, Pattern: re})
	}
	return out
}

