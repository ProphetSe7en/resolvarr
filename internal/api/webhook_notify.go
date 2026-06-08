// webhook_notify.go — M-Webhook notifications framework.
//
// Produces Discord/Gotify/NTFY/Pushover/Apprise embeds when a webhook
// rule fires. Mirrors bash tagarr_import.sh's smart-skip + dynamic-
// fields pattern (line 1346-1459 for Import, 423-518 for Grab), with
// resolvarr-only extensions for the post-bash functions (qBit Category
// Fix, per-bucket strip-on-delete, multi-agent routing).
//
// Architecture rule: the embed builders NEVER re-call engine
// helpers. Every plain-language value comes from the typed Detail
// payload that the adapter already populated based on the engine's
// decision. Builders read state, never derive it.
//
// This file owns the FOUNDATION:
//   - per-function Detail struct types (consumed by builders in #5)
//   - composeTitle: bash-parity combo-title builder
//   - pickColor: 5-color palette (orange/gold/green/blue/red)
//   - eventLabel: plain-language label for the universal "Event" field
//
// Per-function section builders + dispatcher wiring + UI live in
// follow-up tasks (#5 / #7 / #8).

package api

import (
	"encoding/json"
	"strings"

	"resolvarr/internal/core"
)

// Embed accent colors — mirror bash tagarr_import.sh's three colors
// + extend for the qBit-side and destructive flows resolvarr added
// past bash parity.
//
//	🟠 Orange — tag-related actions (Tag-Q-R, Auto-tags, combos)
//	🟡 Gold   — only Discover fired (new release-group added to config)
//	🟢 Green  — only Recover fired (release-group backfilled from history)
//	🔵 Blue   — qBit-side actions (Grab Rename, qBit S/E, qBit Category Fix)
//	🔴 Red    — destructive cleanup (file-delete strip)
//
// Mixed outcomes default to the highest-priority color. Tag actions
// win over qBit-side (a Download event firing both Tag-RG + Grab
// Rename is "primarily a tag event"); qBit-side wins over Discover
// (a Grab event with both qBit S/E + Discover is qBit-side); Delete
// has its own dedicated color regardless of co-occurring actions.
const (
	embedColorTagged   = 0xFFA500 // Orange — matches bash tagarr_import.sh primary color
	embedColorDiscover = 0xFFD700 // Gold   — bash tagarr_import.sh Discover-only color
	embedColorRecover  = 0x2ECC71 // Green  — bash tagarr_import.sh Recover-only color
	embedColorQbitSide = 0x3498DB // Blue   — resolvarr-only (no qBit-side in bash)
	embedColorDelete   = 0xE74C3C // Red    — resolvarr-only (destructive cleanup)
)

// TagDetail is the typed payload for WebhookFnTagReleaseGroups results.
// Populated by the Tag-RG adapter; consumed by the embed builder's
// "Tagged in / Quality tag" section.
//
// Type collides by name with `arr.TagDetail` — both live in different
// packages and used unambiguously from api/. Search by qualified name
// to find the right one.
type TagDetail struct {
	// Tag is the release-group tag (or filter-only tag) the engine
	// applied or removed. Empty when the function was a no-op.
	Tag string

	// Added / Removed list the tags actually changed on the Arr item.
	// Most fires touch zero or one tag; Both being non-empty happens
	// when an upgrade removed an old tag AND added a new one in the
	// same event.
	Added   []string
	Removed []string

	// Primary is the primary Arr instance's display name (e.g.
	// "Radarr Main") — populated by the adapter from
	// Config.Instances[rule.InstanceID].Name. Empty falls back to a
	// generic "primary Arr" label in the embed; adapters should
	// always populate when the instance is known.
	Primary string

	// Mirrored indicates whether SyncToSecondary fired alongside this
	// Tag decision, mirroring the same tag-change to the secondary Arr.
	// SecondaryName is the secondary instance's display name when set
	// (replaces the older SyncTarget field — same semantics).
	Mirrored      bool
	SecondaryName string
}

// AudioDetail is the typed payload for WebhookFnTagAudio results.
// Populated by the Tag-Audio adapter; consumed by the bundled
// Auto-tags embed section.
type AudioDetail struct {
	Added   []string
	Removed []string

	// PlainSummary is the user-facing description rendered in the
	// embed field value (e.g. "TrueHD Atmos 7.1" / "DTS-HD MA 5.1").
	// Adapter formats this once from the underlying mediaInfo so the
	// builder never has to re-derive.
	PlainSummary string
}

// VideoDetail mirrors AudioDetail for WebhookFnTagVideo.
type VideoDetail struct {
	Added        []string
	Removed      []string
	PlainSummary string // e.g. "4K · HEVC · HDR" / "1080p · H.264"
}

// DvDetail mirrors AudioDetail for WebhookFnTagDvDetail.
//
// Type collides by name with `engine.DvDetail` — both live in
// different packages and used unambiguously from api/. Search by
// qualified name to find the right one.
type DvDetail struct {
	Added        []string
	Removed      []string
	PlainSummary string // e.g. "Dolby Vision · Profile 7 · Layer 7.1"
}

// PlexSyncDetail is the typed payload for WebhookFnPlexLabelSync
// results. Surfaces per-label counters from the per-item engine fire
// so the notification can render "+12 labels, -3 labels on Movies" or
// "Movies: +FEL, MEL — Movies 4K: −Atmos" depending on what changed.
//
// Added / Removed are keyed by display label (post LabelDisplay
// override) — same keys as agents see on the Plex Web side. Empty
// maps when nothing actually changed (Changed=false on the parent
// functionResult; section builder skips entirely).
//
// TargetTypes mirrors the rule's pick ("label" / "collection" / both)
// so the section can say "as Plex labels" vs "as Plex collections" vs
// "as Plex labels and collections".
//
// PlexInstanceName surfaces in the section header so users with
// multiple Plex servers can tell which one the changes applied to.
type PlexSyncDetail struct {
	Added            map[string]int
	Removed          map[string]int
	TargetTypes      []string // "label" / "collection"
	PlexInstanceName string   // human-readable name from PlexInstance.Name
	// Libraries are the distinct Plex library titles a label was
	// written to this fire. Multi-library rules touch more than one;
	// the notification lists them so the user can confirm every
	// selected library was tagged, not just the first.
	Libraries []string
}

// DiscoverDetail is the typed payload for WebhookFnDiscover results.
// Populated when a new release-group landed in Config.ReleaseGroups.
type DiscoverDetail struct {
	NewGroup    string // the release-group ID that was added
	AutoEnabled bool   // true = added as Enabled (active); false = added as disabled (commented for manual review)
}

// RecoverDetail is the typed payload for WebhookFnRecover results.
// Populated when the engine backfilled a missing release-group from
// the Arr's grab history.
type RecoverDetail struct {
	RecoveredGroup string // the release-group the engine recovered
	Source         string // short description of where it came from (e.g. "grab history")
}

// GrabRenameDetail is the typed payload for WebhookFnGrabRename
// results. Populated when the engine renamed a torrent in qBittorrent.
// Mirrors the fields bash tagarr_import.sh:452-489 surfaces in its
// Discord embed.
type GrabRenameDetail struct {
	From           string   // torrent name before rename
	To             string   // torrent name after rename (release name from the indexer)
	Triggers       []string // raw trigger labels from the engine — debug-grade, surfaced in History only
	SceneCFChanged bool     // true = the rename changed scene-CF matching (worth flagging in the embed)
	QbitInstance   string   // qBit instance display name where the rename happened

	// GroupRecovered is the release-group token that was missing on
	// the torrent and is now restored by the rename — populated only
	// when the missing-release-group trigger detected a diff. Empty
	// when the rename was driven solely by other triggers (audio /
	// source / movie-version / scene / custom). Mirrors bash
	// tagarr_import.sh's "Release Group Recovered" field shown only
	// when group_recovered=true.
	GroupRecovered string

	// TokensRecovered is the user-friendly list of OTHER tokens the
	// rename restored (movie-version, source, audio, custom). Empty
	// when only the release-group token was recovered. Mirrors bash
	// tagarr_import.sh's "Tokens Recovered" field — collapsed list
	// of token labels without the engine's diagnostic prefix.
	TokensRecovered []string
}

// QbitSeDetail is the typed payload for WebhookFnQbitSeTag results.
//
// Invariant: `Tag` is required for the section to render. The
// builder skips the entire section when Tag is empty — Classification
// or QbitInstance alone don't form a meaningful "S/E classification
// happened" embed. If a future adapter wants to surface "engine could
// not classify" as its own embed, model it differently rather than
// populating QbitSeDetail with an empty Tag.
type QbitSeDetail struct {
	Tag            string // the qBit tag applied (e.g. "Episode" / "Season" / "Unmatched")
	Classification string // human-readable classification ("Episode" / "Season pack" / "Unmatched")
	QbitInstance   string // qBit instance display name
}

// QbitCategoryFixDetail is the typed payload for
// WebhookFnQbitCategoryFix results.
type QbitCategoryFixDetail struct {
	PreCat       string // category the torrent was sitting in pre-fix (pre-import category)
	PostCat      string // category the torrent ended up in post-fix (post-import category)
	QbitInstance string // qBit instance display name
	// SkipReason is non-empty when the function was a no-op and the
	// embed should explain why (e.g. "Arr did its job — torrent
	// already on post-import category").
	SkipReason string
}

// SyncDetail is the typed payload for WebhookFnSyncToSecondary
// results. Carries the secondary instance's display name so
// composeFields can fold it into the TagDetail's Mirrored +
// SecondaryName fields when both Tag-RG and Sync fire on the same
// event. Sync doesn't get its own embed section — "Tagged in:
// primary · secondary" is the user-visible surface.
type SyncDetail struct {
	SecondaryName string // resolved secondary instance display name
}

// FileDeleteDetail is the typed payload for the per-bucket
// strip-on-delete + auto-strip Tag-RG bundle that fires on
// MovieFileDelete / EpisodeFileDelete events. Both sub-flows
// collapse into one struct because the bash mental-model is "the
// file went away → clean up its tags" — one event, one outcome.
type FileDeleteDetail struct {
	// PerBucket lists the tags removed from each Auto-tag bucket
	// (Audio / Video / DV). Empty bucket = no strip-on-delete for that
	// bucket. Keys are bucket names; values are the tags removed.
	PerBucket map[string][]string

	// TagRgRemoved is the release-group tag (or filter-only tag)
	// removed by the dispatcher's auto-strip flow. Empty when the
	// rule didn't have Tag-RG enabled.
	TagRgRemoved string

	// Primary is the primary Arr instance's display name (the Arr
	// the file was deleted from) — populated by the adapter from
	// Config.Instances[rule.InstanceID].Name. Empty falls back to a
	// generic "primary Arr" label in the embed.
	Primary string

	// MirroredSecondary indicates whether SyncToSecondary fired
	// alongside the delete-cleanup, mirroring the strip to the
	// secondary Arr.
	MirroredSecondary bool

	// SecondaryName is the secondary instance's display name when
	// MirroredSecondary is true. Folded into the "Cleaned in" instance
	// list rather than its own separate field — symmetric with how
	// TagDetail renders Mirrored as part of "Tagged in".
	SecondaryName string
}

// titleDisplayOrder controls the order labels appear in the combo
// title. Independent of canonicalFunctionOrder (which dictates EXECUTION
// order in webhook_dispatch.go) because the natural user-facing order
// puts the headline action first: bash tagarr_import.sh emits
// "Tagged + Discovered + Fixed" (Tag first → Discover → Recover), but
// canonical execution runs Recover → Discover → Tag-RG to backfill
// dependencies. Title order matches the user's mental model of "what
// was the most consequential action?" — tag changes first, qBit-side
// last, with Discover/Recover in the middle as auxiliary context.
//
// Lower number = earlier in the title. Functions absent from this map
// are placed at the end (defensive — a future WebhookFunction added
// without updating this map still produces a sensible title).
var titleDisplayOrder = map[core.WebhookFunction]int{
	core.WebhookFnTagReleaseGroups: 10, // headline: "Tagged" (or "Cleaned up tags" on delete events)
	// All three Auto-tag buckets share order=20 so the "Auto-tagged"
	// label position is stable regardless of which bucket's result
	// arrives first to seed the bucket-collapse dedup.
	core.WebhookFnTagAudio:        20,
	core.WebhookFnTagVideo:        20,
	core.WebhookFnTagDvDetail:     20,
	core.WebhookFnDiscover:        30, // auxiliary context: "Discovered"
	core.WebhookFnRecover:         40, // auxiliary context: "Fixed release group"
	core.WebhookFnGrabRename:      50, // qBit-side
	core.WebhookFnQbitSeTag:       60,
	core.WebhookFnQbitCategoryFix: 70,
	// Plex label sync surfaces AFTER all the Arr-side tagging functions
	// (it's downstream of them) but BEFORE the qBit-side functions —
	// it's the "and the tags propagated to Plex too" half of an
	// import-event story.
	core.WebhookFnPlexLabelSync: 45,
	// SyncToSecondary has no standalone label (titleLabelForFunction
	// returns ""), so this entry is dead today. Kept defensive: if a
	// future change gives Sync its own visible label, the order is
	// already wired and it lands at the tail of the title.
	core.WebhookFnSyncToSecondary: 99,
}

// composeTitle builds the embed title in bash tagarr_import.sh's
// combo-string shape:
//
//	"Tagged + Discovered - Dune: Part Two (2024)"          ← Download event, multi-action
//	"Renamed in qBit - Dune: Part Two (2024)"              ← Grab event, GrabRename only
//	"Cleaned up tags - Old Movie (2019)"                   ← MovieFileDelete event with strip
//	"Episode tagged - The Bear (S03E07)"                   ← qBit S/E on a series Grab
//
// Only results with Changed=true contribute to the title — bash
// tagarr_import.sh's smart-skip rule "Nothing to report" applies per-
// function. A Tag-Q-R that successfully decided "no change needed"
// (OK=true, Changed=false) does not surface in the title.
//
// Rule: notifications must contain only actual changes.
// A delete event that fires its strip-on-delete dispatchers but
// actually strips nothing (no managed tags on the deleted file)
// produces NO embed — same skip-path as a Download event where every
// function was a no-op. The History modal still records the fire so
// the user can audit; Discord just stays quiet.
//
// Multiple Auto-tag buckets (Audio + Video + DV all firing on the
// same import) collapse into a single "Auto-tagged" label rather
// than three separate ones — keeps the title readable.
//
// Labels are ordered by titleDisplayOrder (Tag first, then Discover/
// Recover, then qBit-side), independent of canonicalFunctionOrder
// (which controls execution order in the dispatcher). This way the
// user sees the headline action first regardless of how the engine
// executed.
//
// Returns "" when no function actually changed. The caller treats
// empty as "no embed".
//
// `allowedFunctions` is the per-agent Functions whitelist (from
// agents.Agent.Functions). Empty/nil = no filter; non-empty =
// title combo only includes labels for the filtered subset, so the
// agent's title matches the body sections it will receive.
func composeTitle(event core.WebhookConnectEvent, results []functionResult, itemTitle, itemContext string, allowedFunctions []string) string {
	results = filterResultsByFunctions(results, allowedFunctions)
	type labelEntry struct {
		order int
		text  string
	}
	entries := []labelEntry{}
	seen := map[string]bool{}
	for _, r := range results {
		if !r.Changed {
			continue
		}
		label := titleLabelForFunctionAndEvent(r.Function, event)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		order, ok := titleDisplayOrder[r.Function]
		if !ok {
			order = 1000 // unknown / future function → end
		}
		entries = append(entries, labelEntry{order: order, text: label})
	}

	if len(entries) == 0 {
		return ""
	}

	// Stable sort by display-order. Equal orders preserve append
	// order (slice append is stable). Use a simple insertion sort to
	// avoid pulling sort package for what's always a tiny slice
	// (≤ 9 distinct labels max).
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].order > entries[j].order; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}

	labels := make([]string, 0, len(entries))
	for _, e := range entries {
		labels = append(labels, e.text)
	}

	head := strings.Join(labels, " + ")
	tail := strings.TrimSpace(itemTitle)
	if tail == "" {
		tail = "(unknown title)"
	}
	if ctx := strings.TrimSpace(itemContext); ctx != "" {
		tail = tail + " (" + ctx + ")"
	}
	return head + " - " + tail
}

// titleLabelForFunctionAndEvent returns the human-readable verb used
// in the combo title. The event type matters because a Tag-Q-R fire
// on a Download means "Tagged" but the same function on a
// MovieFileDelete means "Cleaned up" (the strip-on-delete dispatcher
// REMOVED the tag, not applied it). One label per (function, event-
// class) avoids misleading verbs on the wrong side of the lifecycle.
//
// On delete events, every tag-touching function collapses to a single
// "Cleaned up tags" label (the same way Audio/Video/DV bucket-collapse
// to "Auto-tagged" on import). User mental model is "the file went
// away → tags went away"; the embed body (built in #5) carries the
// detail of WHICH tags.
//
// Empty string means the function never carries a standalone label
// at this event class — e.g. SyncToSecondary always rides alongside
// a Tag/Auto-tag result, so giving it its own label would produce
// ugly "Tagged + Synced" titles when "Tagged" is the right framing.
func titleLabelForFunctionAndEvent(fn core.WebhookFunction, event core.WebhookConnectEvent) string {
	if isDeleteEvent(event) {
		switch fn {
		case core.WebhookFnTagReleaseGroups,
			core.WebhookFnTagAudio,
			core.WebhookFnTagVideo,
			core.WebhookFnTagDvDetail:
			return "Cleaned up tags"
		case core.WebhookFnPlexLabelSync:
			// On delete events Plex sync removes labels/collections
			// for the deleted item. Surface that explicitly.
			return "Cleared from Plex"
		case core.WebhookFnSyncToSecondary:
			return "" // folded into the Cleaned-up label, never solo
		}
		// Other functions (Discover, Recover, GrabRename, qBit*) don't
		// fire on delete events at all per EventsForFunction; if a
		// future change wires one in, fall through to the regular
		// label below so the title isn't silently dropped.
	}
	switch fn {
	case core.WebhookFnTagReleaseGroups:
		return "Tagged"
	case core.WebhookFnDiscover:
		return "Discovered"
	case core.WebhookFnRecover:
		return "Fixed release group"
	case core.WebhookFnTagAudio, core.WebhookFnTagVideo, core.WebhookFnTagDvDetail:
		return "Auto-tagged" // three buckets collapse to one label
	case core.WebhookFnGrabRename:
		return "Renamed in qBit"
	case core.WebhookFnQbitSeTag:
		return "Episode tagged"
	case core.WebhookFnQbitCategoryFix:
		return "Category fixed"
	case core.WebhookFnPlexLabelSync:
		return "Synced to Plex"
	case core.WebhookFnSyncToSecondary:
		return "" // folded into the Tag/Auto-tag label, never solo
	}
	return ""
}

// pickColor selects the embed accent color based on which function
// outcomes actually changed state + the event type. File-delete
// events always get red; other events use the highest-priority
// outcome color from results where Changed=true.
//
// Priority order (tag-related > qBit-side > Discover > Recover) is
// deliberate: a Download firing both Tag-RG and Discover is
// "primarily a tag event" from the user's perspective (Discover is
// auxiliary context); a Grab firing GrabRename + qBit S/E is
// primarily qBit-side. The color tells the user the dominant action
// at a glance without having to read the fields.
//
// Filters on Changed (not OK) so a successful no-op (Tag-Q-R decided
// "already correct") doesn't pull the color into orange — if NOTHING
// changed, the embed shouldn't fire in the first place (the caller
// gates on Changed too), and if it does fire, the color falls
// through to the safe default.
//
// `allowedFunctions` is the per-agent Functions whitelist (from
// agents.Agent.Functions). Empty/nil = no filter; non-empty = color
// is picked from the filtered subset only, so the agent's color
// matches the sections it will receive.
func pickColor(event core.WebhookConnectEvent, results []functionResult, allowedFunctions []string) int {
	results = filterResultsByFunctions(results, allowedFunctions)
	if isDeleteEvent(event) {
		return embedColorDelete
	}

	var sawTag, sawQbit, sawDiscover, sawRecover bool
	for _, r := range results {
		if !r.Changed {
			continue
		}
		switch r.Function {
		case core.WebhookFnTagReleaseGroups,
			core.WebhookFnTagAudio,
			core.WebhookFnTagVideo,
			core.WebhookFnTagDvDetail,
			core.WebhookFnSyncToSecondary,
			core.WebhookFnPlexLabelSync:
			// Plex sync is the downstream half of a tag-event chain —
			// the user perceives it as "the tag changes propagated" so
			// it pulls the embed color into the tag bucket alongside
			// the Arr-side mutations.
			sawTag = true
		case core.WebhookFnGrabRename,
			core.WebhookFnQbitSeTag,
			core.WebhookFnQbitCategoryFix:
			sawQbit = true
		case core.WebhookFnDiscover:
			sawDiscover = true
		case core.WebhookFnRecover:
			sawRecover = true
		}
	}

	switch {
	case sawTag:
		return embedColorTagged
	case sawQbit:
		return embedColorQbitSide
	case sawDiscover:
		return embedColorDiscover
	case sawRecover:
		return embedColorRecover
	}
	// Safe default: orange (matches bash). Reached when no result had
	// Changed=true (or the filter eliminated every changed result for
	// this agent) — embed body would be empty too, so the caller
	// short-circuits dispatch via composeTitle's empty-return path.
	// The color value only matters in the rare debug / hand-test case
	// where a caller bypasses the short-circuit.
	return embedColorTagged
}

// (composeFooterSuffix deleted 2026-05-24 — rule name now lives in
// the embed body's "Rule" field instead of crammed onto the footer
// line. The footer is reserved for "Resolvarr {version} by
// ProphetSe7en" + the locale-aware embed timestamp on the right.
// See appendRuleSection in webhook_notify_sections.go.)

// posterImage is one entry in a Radarr/Sonarr Connect payload's
// images[] array. CoverType discriminates the image kind ("poster",
// "fanart", "banner", "clearart", …); we filter to "poster" only.
//
// RemoteURL is the upstream metadata CDN URL (TMDb / TheTVDB) which
// is preferred for embed thumbnails — Discord can fetch it directly
// without the user's Arr needing to be reachable from Discord's
// image proxy. URL is the Arr's own proxy URL ("/MediaCover/{id}/..."
// relative to Arr base), used as fallback when remoteUrl is missing.
type posterImage struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
	URL       string `json:"url"`
}

// posterPayload is a surgical decode target for poster-URL extraction.
// Only the images[] arrays under the Radarr movie / Sonarr series are
// pulled out; the rest of the Connect body is left untouched. Keeps
// extractPosterURL allocation-light + decoupled from the full
// connectEventEnvelope schema so future schema additions don't ripple
// through.
type posterPayload struct {
	Movie *struct {
		Images []posterImage `json:"images"`
	} `json:"movie,omitempty"`
	Series *struct {
		Images []posterImage `json:"images"`
	} `json:"series,omitempty"`
}

// extractPosterURL pulls the poster image URL from a Connect-event
// body. Radarr: reads `.movie.images[]`. Sonarr: reads
// `.series.images[]`. Picks the first entry with `coverType=="poster"`,
// preferring `remoteUrl` (TMDb / TheTVDB CDN) over `url` (the Arr's
// own MediaCover proxy).
//
// Only `http://` or `https://` URLs are accepted. This filter does
// triple duty:
//
//  1. Security — blocks `javascript:` / `data:` URLs that a
//     compromised TMDb/TheTVDB entry could inject. Discord would
//     refuse to render them anyway, but defence-in-depth costs us
//     nothing.
//  2. Correctness — blocks relative paths like `/MediaCover/1/poster.jpg`
//     that the Arr emits for `.url`. Discord can't fetch a relative
//     path and would silently 404 the thumbnail. Skipping unresolved
//     relative URLs lets the function return "" → no thumbnail →
//     embed renders cleanly without a broken-image icon.
//  3. Forward-compat — if a future Arr release introduces a new
//     URL scheme (file:// for local caching?), it's transparently
//     rejected until we audit + opt in.
//
// Returns "" on any of: empty body, malformed JSON, missing images
// array, no poster-typed image, non-http(s) URL, appType not
// "radarr"/"sonarr". The caller treats empty as "no thumbnail" —
// agents.Payload.ThumbnailURL empty omits the embed thumbnail
// entirely (verified in agents/discord.go).
//
// No API-fetch fallback (bash tagarr_import.sh fetches /movie/{id}
// when the payload lacks images). Radarr/Sonarr Connect payloads on
// Grab + Download + MovieFileDelete events DO carry images by
// default; an API fetch would add HTTP dependency + auth surface for
// no measurable gain. If real-world testing surfaces payloads
// without images, add the fallback then.
func extractPosterURL(body []byte, appType string) string {
	if len(body) == 0 {
		return ""
	}
	var p posterPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	var images []posterImage
	switch strings.ToLower(strings.TrimSpace(appType)) {
	case "radarr":
		if p.Movie != nil {
			images = p.Movie.Images
		}
	case "sonarr":
		if p.Series != nil {
			images = p.Series.Images
		}
	default:
		return ""
	}
	for _, img := range images {
		if !strings.EqualFold(strings.TrimSpace(img.CoverType), "poster") {
			continue
		}
		if u := acceptHTTPURL(img.RemoteURL); u != "" {
			return u
		}
		if u := acceptHTTPURL(img.URL); u != "" {
			return u
		}
	}
	return ""
}

// acceptHTTPURL trims its input and returns it only when it parses
// as an http:// or https:// URL. Everything else (relative paths,
// javascript:, data:, file:, empty) returns "". Used by
// extractPosterURL to gate the embed thumbnail URL — see the docs
// there for the rationale.
func acceptHTTPURL(raw string) string {
	u := strings.TrimSpace(raw)
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}

// isDeleteEvent returns true for the four delete-event variants the
// per-bucket strip-on-delete + auto-strip Tag-RG flows fire on.
func isDeleteEvent(event core.WebhookConnectEvent) bool {
	switch event {
	case core.WebhookEventMovieFileDelete,
		core.WebhookEventMovieFileDeleteForUpgrade,
		core.WebhookEventEpisodeFileDelete,
		core.WebhookEventEpisodeFileDeleteForUpgrade:
		return true
	}
	return false
}

// eventLabel maps a Connect event-type to the short plain-language
// label rendered in the embed's "Event" field. Mirrors bash
// tagarr_import.sh's exposure of $EVENT_TYPE but with friendlier
// wording (bash exposes the literal "MovieFileDeleteForUpgrade";
// we render "Upgraded").
//
// Returns "" for unknown / unhandled events — caller suppresses the
// Event field rather than emitting a literal "Unknown".
func eventLabel(event core.WebhookConnectEvent) string {
	switch event {
	case core.WebhookEventGrab:
		return "Grab"
	case core.WebhookEventDownload:
		return "Import"
	case core.WebhookEventMovieFileDelete:
		return "File deleted"
	case core.WebhookEventEpisodeFileDelete:
		return "Episode deleted"
	case core.WebhookEventMovieFileDeleteForUpgrade,
		core.WebhookEventEpisodeFileDeleteForUpgrade:
		return "Upgraded"
	}
	return ""
}
