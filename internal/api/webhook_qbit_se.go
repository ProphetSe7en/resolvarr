package api

// webhook_qbit_se.go — qBit tagging adapter for the M-Webhook
// dispatcher. Three-rule first-match-wins model (Episode → Season →
// Unmatched), direct port of the community Python reference
// qbittorrent_auto_tagger.py. Sonarr-only per WebhookFunctionAppliesTo.
//
// Fires on Connect Grab events; classifies the release title via
// engine.DetermineQbitTag and adds ONE tag to the qBit torrent
// identified by downloadId. The classifier looks at the torrent name
// (release.releaseTitle on the Connect payload) — episodes[] is not
// consulted because the patterns are name-based, mirroring the Python
// script's behaviour exactly.
//
// Architectural rule 1: tag decisions come from engine.DetermineQbitTag
// (pure function). NO inline classification logic in the adapter.
//
// Architectural rule 2: one Grab event = one torrent. GetTorrent +
// AddTags are O(1) round-trips. NEVER walk the qBit library.
//
// Container-only feature with bash-script-parity semantics — replaces
// the user's existing qbittorrent_auto_tagger.py deployment.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/qbit"
)

// dispatchQbitSeTag fires on Sonarr Grab events. Reads release title
// from the Connect payload + classifies via the engine helper, then
// adds ONE tag (the winner of Episode → Season → Unmatched first-
// match-wins) to the qBit torrent identified by downloadId.
//
// Skip conditions (clean OK=true skip, no qBit write):
//   - Not a Grab event.
//   - Rule's QbitSe criteria struct missing (validator should have
//     caught this at save-time; defence in depth).
//   - Empty downloadId (manual grab without a download client).
//   - Empty release title (anomalous — Sonarr always emits one).
//   - Engine returned no tag (every rule disabled, OR the matched
//     rule is the disabled one — see DetermineQbitTag's no-fall-
//     through behaviour).
//   - qBit torrent not found in qBit's library after retries.
//
// Idempotency: qBit's /addTags is no-op when tag already on torrent,
// so Connect retries are free.
func (s *Server) dispatchQbitSeTag(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventGrab) {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: true, Summary: "skipped (not a Grab event)"}
	}
	if rule.QbitSe == nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "rule has QbitSeTag function but no QbitSe criteria struct"}
	}
	rules := rule.QbitSe

	var payload grabEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "decode payload failed", Err: err}
	}

	hash := strings.TrimSpace(payload.DownloadID)
	if hash == "" {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: true, Summary: "skipped (no downloadId on event — manual grab?)"}
	}

	// Source torrent name for classification — release.releaseTitle is
	// the indexer-provided release title that qBit also uses as the
	// torrent display name post-grab. Classification reconciles this name
	// with the qBit file list below (content-aware, same as the on-add
	// hook + backlog scan); episodes[] isn't used because the file count
	// distinguishes a season pack from a multi-episode file, which the
	// per-grab episode list can't.
	torrentName := strings.TrimSpace(payload.Release.ReleaseTitle)
	if torrentName == "" {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: true, Summary: "skipped (no release.releaseTitle on event payload)"}
	}

	view := engine.QbitSeRulesView{
		EpisodeEnabled:   rules.EpisodeEnabled,
		EpisodeTag:       rules.EpisodeTag,
		SeasonEnabled:    rules.SeasonEnabled,
		SeasonTag:        rules.SeasonTag,
		UnmatchedEnabled: rules.UnmatchedEnabled,
		UnmatchedTag:     rules.UnmatchedTag,
	}

	// Resolve qBit instance + client.
	qbitInst := findQbitInstanceByID(cfg, rules.QbitInstanceID)
	if qbitInst == nil {
		return functionResult{
			Function: core.WebhookFnQbitSeTag, OK: false,
			Summary: fmt.Sprintf("qbit instance %q not found in config", rules.QbitInstanceID),
		}
	}
	client, err := qbit.New(qbit.Config{
		URL:          qbitInst.URL,
		Username:     qbitInst.Username,
		Password:     qbitInst.Password,
		TrustedCerts: qbitInst.TrustedCerts,
	})
	if err != nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "qbit client init", Err: err}
	}

	// Wait for the torrent to appear in qBit (retry-with-backoff — reuse
	// the same helper Grab Rename uses). A wait error is NOT surfaced yet:
	// a no-tag torrent should skip cleanly even when qBit is unreachable,
	// so the error is only reported below once we know there's a tag.
	torrent, found, waitErr := waitForTorrent(ctx, client, hash)

	// Content-aware classification. This is a Sonarr Grab, so the torrent
	// is definitively a series (HintSeries — rules out movie, unlocks the
	// single-file episode promotion). Reconcile against the qBit file list
	// so it agrees with the on-add hook + backlog scan (same classifier).
	// The file list may not be ready yet at grab time (e.g. a magnet whose
	// metadata hasn't resolved), or unavailable if the wait failed — then
	// it falls back to name-only.
	var fileViews []engine.TorrentFileView
	if found {
		if files, ferr := client.ListTorrentFiles(ctx, hash); ferr == nil {
			fileViews = make([]engine.TorrentFileView, 0, len(files))
			for _, f := range files {
				fileViews = append(fileViews, engine.TorrentFileView{Name: f.Name, Size: f.Size})
			}
		}
	}
	res := engine.ClassifyTorrentTypeWithHint(torrentName, fileViews, engine.HintSeries)
	tag := engine.DetermineQbitTagFromClass(res.Class, view)
	if tag == "" {
		return functionResult{
			Function: core.WebhookFnQbitSeTag, OK: true,
			Summary: "skipped (no rule matched — toggle Episode / Season / Unmatched)",
		}
	}
	// There is a tag to apply — now surface any qBit reachability problem.
	if waitErr != nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "qbit GetTorrent", Err: waitErr}
	}
	if !found {
		return functionResult{
			Function: core.WebhookFnQbitSeTag, OK: true,
			Summary: fmt.Sprintf("skipped (torrent hash %s not in qbit after retries)", hash),
		}
	}

	// Already tagged → no state change, so Changed stays false and the
	// notification dispatcher (which fires only on actual changes)
	// correctly stays silent. AddTags is idempotent on the qBit side too.
	if qbitHasTag(torrent.Tags, tag) {
		return functionResult{
			Function: core.WebhookFnQbitSeTag, OK: true,
			Summary: fmt.Sprintf("already tagged %s with %q", hash, tag),
		}
	}

	if err := client.AddTags(ctx, []string{hash}, []string{tag}); err != nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "qbit addTags", Err: err}
	}
	// Changed=true so the grab event actually notifies. A newly applied
	// Season/Episode tag is a real change worth surfacing.
	return functionResult{
		Function: core.WebhookFnQbitSeTag, OK: true, Changed: true,
		Summary: fmt.Sprintf("tagged %s with %q", hash, tag),
	}
}

// qbitHasTag reports whether tag is already present in a qBit torrent's
// comma-separated Tags string (case-insensitive, whitespace-trimmed).
func qbitHasTag(tagsCSV, tag string) bool {
	for _, t := range strings.Split(tagsCSV, ",") {
		if strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(tag)) {
			return true
		}
	}
	return false
}
