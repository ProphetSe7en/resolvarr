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
	"path"
	"regexp"
	"sort"
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

	// RenameTarget gate — defence-in-depth against pre-validator-saved
	// rules still on disk where target was set to "file" / "both" (the
	// save-time validator at webhook_rules.go:268-270 rejects those now,
	// but a config from before that gate landed could still slip
	// through). Default empty → "torrent". Any other value short-
	// circuits BEFORE qBit calls so the dispatcher logs the reason
	// without generating API traffic.
	renameTarget := strings.TrimSpace(criteria.RenameTarget)
	if renameTarget == "" {
		renameTarget = core.GrabRenameTargetTorrent
	}
	// "file" target renames each episode file inside the torrent — wired
	// for Sonarr only (it exists to fix season-pack per-file scoring,
	// where Sonarr parses each file by its own name). "both" + Radarr
	// file rename aren't wired yet.
	filesMode := renameTarget == core.GrabRenameTargetFile && strings.EqualFold(rule.AppType, "sonarr")
	if renameTarget != core.GrabRenameTargetTorrent && !filesMode {
		return functionResult{
			Function: core.WebhookFnGrabRename, OK: false,
			Summary: fmt.Sprintf("rename target %q not supported (use 'torrent', or 'file' on a Sonarr rule for season packs)", renameTarget),
		}
	}

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
	// Season-pack files mode (Sonarr): rename each episode file inside
	// the torrent. The torrent must exist (waitForTorrent above confirmed
	// it); the per-file work runs off ListTorrentFiles, not the name.
	if filesMode {
		return s.dispatchGrabRenameFiles(ctx, client, hash, grabTitle, rg, criteria, qbitInst.Name)
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
			Summary: fmt.Sprintf("skipped (target equals current after normalisation; from=%q; triggers fired: %s)", currentName, strings.Join(reasons, ", ")),
		}
	}

	// Apply rename.
	if err := client.RenameTorrent(ctx, hash, target); err != nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "qbit rename", Err: err}
	}
	// Summary includes both from + to so the History modal shows the
	// exact comparison qBit returned — no need to query qBit live to
	// understand why a given rename fired. Per-trigger diagnostics
	// (parser output, missing tokens) live inside reasons[].
	groupRecovered, tokensRecovered := summariseGrabRenameRecovery(reasons, rg)
	return functionResult{
		Function: core.WebhookFnGrabRename, OK: true, Changed: true,
		Summary: fmt.Sprintf("renamed %q → %q (triggers: %s)", currentName, target, strings.Join(reasons, ", ")),
		Detail: GrabRenameDetail{
			From:            currentName,
			To:              target,
			Triggers:        reasons,
			QbitInstance:    qbitInst.Name,
			GroupRecovered:  groupRecovered,
			TokensRecovered: tokensRecovered,
		},
	}
}

// summariseGrabRenameRecovery turns the engine's raw trigger labels
// into the user-friendly GroupRecovered + TokensRecovered split bash
// tagarr_import.sh surfaces in its embed. Pure string parsing — no
// engine state needed beyond the rg token already on hand.
//
// Mapping rules:
//
//   - "missing-release-group …"  → GroupRecovered = rg
//   - "movie-version: A/B/C"     → TokensRecovered += A, B, C
//   - "source: WEB-DL"           → TokensRecovered += WEB-DL
//   - "audio: TrueHD/Atmos"      → TokensRecovered += TrueHD, Atmos
//   - "scene-stripped …"         → TokensRecovered += "scene"
//   - "custom: Label1/Label2"    → TokensRecovered += Label1, Label2
//   - "always-rename"            → no recovery semantics (rename
//     fired without any specific trigger detecting a diff)
//
// Deduplicates token list — multiple triggers can surface the same
// token in pathological configs and the embed should show it once.
func summariseGrabRenameRecovery(reasons []string, rg string) (groupRecovered string, tokensRecovered []string) {
	seen := map[string]bool{}
	appendToken := func(tok string) {
		t := strings.TrimSpace(tok)
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		tokensRecovered = append(tokensRecovered, t)
	}
	for _, raw := range reasons {
		r := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(r, "missing-release-group"):
			if groupRecovered == "" {
				groupRecovered = strings.TrimSpace(rg)
			}
		case strings.HasPrefix(r, "movie-version: "):
			for _, t := range strings.Split(strings.TrimPrefix(r, "movie-version: "), "/") {
				appendToken(t)
			}
		case strings.HasPrefix(r, "source: "):
			for _, t := range strings.Split(strings.TrimPrefix(r, "source: "), "/") {
				appendToken(t)
			}
		case strings.HasPrefix(r, "audio: "):
			for _, t := range strings.Split(strings.TrimPrefix(r, "audio: "), "/") {
				appendToken(t)
			}
		case strings.HasPrefix(r, "scene-stripped"):
			appendToken("scene")
		case strings.HasPrefix(r, "custom: "):
			for _, t := range strings.Split(strings.TrimPrefix(r, "custom: "), "/") {
				appendToken(t)
			}
		}
	}
	return groupRecovered, tokensRecovered
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
	//
	// Diagnostic suffix lands in the History summary so users can see
	// EXACTLY what the parser extracted vs what the grab payload claimed
	// — without needing live qBit access. Helps debug "rename fired but
	// the name looked fine" cases by surfacing the actual comparison.
	if c.TriggerOnMissingReleaseGroup && rg != "" {
		got, ok, reason := engine.ParseReleaseGroupFromFilename(currentName)
		if !ok {
			reasons = append(reasons, fmt.Sprintf("missing-release-group (parser rejected: %s)", reason))
		} else if !strings.EqualFold(got, rg) {
			reasons = append(reasons, fmt.Sprintf("missing-release-group (parsed=%q expected=%q)", got, rg))
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

// videoFileRE matches the container extensions we rename inside a
// season pack. Non-video files (nfo / sample / subs) are left alone.
var videoFileRE = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|mov|wmv|flv|webm|ts|mpg|mpeg)$`)

// dispatchGrabRenameFiles is the "file" rename target (Sonarr season
// packs). Sonarr scores a season pack per file at import — by each
// file's own name, not the torrent display name — so a scene-stripped
// inner file (e.g. "web" instead of "WEB-DL", missing "NF") scores far
// below the grab and the import can get stuck. This renames each
// episode file to the release title with that file's SxxEyy substituted
// in, so every file parses with the full release tokens.
//
// Per file: parse SxxEyy → build the per-episode title → run the SAME
// triggers the torrent path uses (comparing the file name to the grab
// title) → rename only files where ≥1 trigger fires (or TriggerAlways).
// Files with no SxxEyy, or when the grab title has no matching season
// token, are skipped (never guessed). One file's rename failure doesn't
// abort the rest.
func (s *Server) dispatchGrabRenameFiles(ctx context.Context, client *qbit.Client, hash, grabTitle, rg string, criteria *core.GrabRenameCriteria, qbitInstName string) functionResult {
	files, err := client.ListTorrentFiles(ctx, hash)
	if err != nil {
		return functionResult{Function: core.WebhookFnGrabRename, OK: false, Summary: "qbit list files", Err: err}
	}
	if len(files) == 0 {
		return functionResult{Function: core.WebhookFnGrabRename, OK: true, Summary: "skipped (torrent has no files listed yet)"}
	}

	var renamed, skipped, failed int
	var lastErr error
	var firstFrom, firstTo string
	reasonSet := map[string]bool{}
	// Guard against two files mapping to the same target name (e.g. a
	// pack carrying a dupe of an episode, or a sample that parses to a
	// real SxxEyy). qBit would reject the second with 409; skip it
	// ourselves so the tally stays honest and we don't churn the API.
	usedTargets := map[string]bool{}

	for _, f := range files {
		if !videoFileRE.MatchString(f.Name) {
			continue
		}
		base := path.Base(f.Name)
		token, season, ok := engine.ParseSeasonEpisodeToken(base)
		if !ok {
			skipped++
			continue
		}
		perEp, ok := engine.BuildSeasonPackEpisodeTitle(grabTitle, token, season)
		if !ok {
			skipped++
			continue
		}
		reasons := evaluateGrabRenameTriggers(base, grabTitle, rg, criteria)
		if criteria.TriggerAlways && len(reasons) == 0 {
			reasons = append(reasons, "always-rename")
		}
		if len(reasons) == 0 {
			skipped++
			continue
		}
		newBase := engine.NormalizeRgSegment(perEp, rg) + path.Ext(f.Name)
		newPath := newBase
		if dir := path.Dir(f.Name); dir != "." && dir != "" {
			newPath = dir + "/" + newBase
		}
		if newPath == f.Name {
			skipped++
			continue
		}
		if usedTargets[newPath] {
			// Another file in this pass already took this target name.
			skipped++
			continue
		}
		if err := client.RenameFile(ctx, hash, f.Name, newPath); err != nil {
			failed++
			lastErr = err
			continue
		}
		usedTargets[newPath] = true
		renamed++
		for _, r := range reasons {
			reasonSet[r] = true
		}
		if firstFrom == "" {
			firstFrom, firstTo = base, newBase
		}
	}

	if renamed == 0 {
		if failed > 0 {
			return functionResult{
				Function: core.WebhookFnGrabRename, OK: false,
				Summary: fmt.Sprintf("no files renamed (%d failed)", failed), Err: lastErr,
			}
		}
		return functionResult{
			Function: core.WebhookFnGrabRename, OK: true,
			Summary: fmt.Sprintf("skipped (no episode files needed renaming; %d files examined)", len(files)),
		}
	}

	reasonList := make([]string, 0, len(reasonSet))
	for r := range reasonSet {
		reasonList = append(reasonList, r)
	}
	sort.Strings(reasonList)

	summary := fmt.Sprintf("renamed %d episode file(s) inside the pack", renamed)
	if skipped > 0 {
		summary += fmt.Sprintf(", skipped %d", skipped)
	}
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	summary += fmt.Sprintf(" (triggers: %s)", strings.Join(reasonList, ", "))

	return functionResult{
		Function: core.WebhookFnGrabRename, OK: true, Changed: true,
		Summary: summary,
		Detail: GrabRenameDetail{
			From:         firstFrom,
			To:           firstTo,
			Triggers:     reasonList,
			QbitInstance: qbitInstName,
		},
	}
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

