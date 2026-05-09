package api

// webhook_qbit_se.go — qBit Season/Episode tagging adapter for the
// M-Webhook dispatcher. Sonarr-only per WebhookFunctionAppliesTo.
// Fires on Connect Grab events and tags the qBit torrent with
// S/E patterns (S01 / S01E05 / S01E05E06) so backlog seeding by
// season/episode becomes trivial in qBit's tag dropdown.
//
// Architectural rule 1: tag-format decisions come from
// engine.QbitSeasonEpisodeTags (pure function). NO inline format
// strings.
//
// Architectural rule 2: one Grab event = one torrent. GetTorrent +
// AddTags are O(1) round-trips. NEVER walk the qBit library.
//
// Container-only feature; bash tagarr_import_sonarr.sh has no S/E
// tagging. Backlog-fix flow (preview/apply on existing torrents)
// lives in commit 3 as a separate API endpoint, not an adapter.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/qbit"
)

// dispatchQbitSeTag fires on Sonarr Grab events. Reads release
// metadata (seasonNumber via episodes[]) + computes the tag list
// via the engine helper, then adds the tag(s) to the qBit torrent
// identified by downloadId.
//
// Skip conditions (clean OK=true skip, no qBit write):
//   - Not a Grab event.
//   - Rule's QbitSe criteria struct missing (validator should have
//     caught this at save-time; defence in depth).
//   - Empty downloadId or empty episodes[] (no S/E info to tag).
//   - Episodes span multiple seasons (anomalous; skip rather than
//     tag wrongly — Sonarr typically grabs one season per torrent).
//   - Rule's TagSeason + TagEpisode both off (engine returns empty
//     tag list → nothing to apply).
//   - qBit torrent not found in qBit's library after retries.
//
// Idempotency: qBit's /addTags is no-op when tag already on
// torrent, so Connect retries are free.
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
	if len(payload.Episodes) == 0 {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: true, Summary: "skipped (no episodes[] on event payload)"}
	}

	// Resolve season + episode list. All episodes must belong to the
	// same season for tag-format to be meaningful — Sonarr typically
	// emits per-season grabs, but a multi-season torrent (rare —
	// usually a manually-imported pack) would produce ambiguous tags.
	seasonNum := payload.Episodes[0].SeasonNumber
	epNums := make([]int, 0, len(payload.Episodes))
	for _, e := range payload.Episodes {
		if e.SeasonNumber != seasonNum {
			return functionResult{
				Function: core.WebhookFnQbitSeTag, OK: true,
				Summary: "skipped (episodes span multiple seasons — ambiguous tag)",
			}
		}
		epNums = append(epNums, e.EpisodeNumber)
	}

	// Build tag list via engine helper. totalEps left at 0 (heuristic
	// fallback) — querying Sonarr for series statistics would add a
	// network round-trip per fire; the ≥10-eps heuristic catches
	// genuine season packs cheaply.
	tags := engine.QbitSeasonEpisodeTags(seasonNum, epNums, 0, engine.QbitSeRulesView{
		TagSeason:  rules.TagSeason,
		TagEpisode: rules.TagEpisode,
	})
	if len(tags) == 0 {
		return functionResult{
			Function: core.WebhookFnQbitSeTag, OK: true,
			Summary: "skipped (no tag formats enabled — toggle TagSeason or TagEpisode)",
		}
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

	// Wait for the torrent to appear in qBit (retry-with-backoff —
	// reuse the same helper Grab Rename uses).
	_, found, err := waitForTorrent(ctx, client, hash)
	if err != nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "qbit GetTorrent", Err: err}
	}
	if !found {
		return functionResult{
			Function: core.WebhookFnQbitSeTag, OK: true,
			Summary: fmt.Sprintf("skipped (torrent hash %s not in qbit after retries)", hash),
		}
	}

	if err := client.AddTags(ctx, []string{hash}, tags); err != nil {
		return functionResult{Function: core.WebhookFnQbitSeTag, OK: false, Summary: "qbit addTags", Err: err}
	}
	return functionResult{
		Function: core.WebhookFnQbitSeTag, OK: true,
		Summary: fmt.Sprintf("tagged %s with [%s]", hash, strings.Join(tags, ", ")),
	}
}
