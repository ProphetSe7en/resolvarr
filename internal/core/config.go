package core

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"resolvarr/internal/core/agents"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/utils"
)

// Instance is a single Radarr/Sonarr endpoint.
type Instance struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`        // "radarr" or "sonarr"
	IconVariant string `json:"iconVariant"` // "standard" or "4k"
	URL         string `json:"url"`
	APIKey      string `json:"apiKey"`

	// PathMappings translates paths reported by Arr's API into paths
	// reachable from inside the tagarr container. Empty for the common
	// "aligned mounts" Unraid setup where Radarr and tagarr both see
	// `/data/media/...` — pass-through with no translation. Used by
	// the M4b DV-detail extraction path which needs to open the file
	// directly (the bash script ran inside the Radarr container so it
	// got the alignment for free).
	//
	// Applied longest-prefix-first so nested mappings work. Empty +
	// nil are treated identically by every consumer; omitempty keeps
	// older configs from gaining a noisy `"pathMappings": null` line
	// on their next save.
	PathMappings []PathMapping `json:"pathMappings,omitempty"`

	// Webhook holds the per-instance Connect-webhook config. Empty
	// struct means webhook isn't set up yet. The Token is the
	// per-instance shared secret baked into the webhook URL the user
	// pastes into Sonarr/Radarr — base64url-encoded crypto/rand
	// 32 bytes. Other fields are user-toggleable function flags.
	// Today only LoggingEnabled is wired; subsequent releases add
	// per-function flags (release-group tag on import, DV detail
	// on import, etc.).
	Webhook WebhookConfig `json:"webhook,omitempty"`
}

// WebhookConfig is the per-Arr-instance webhook subscription state.
// Token-empty == webhook not configured. Per-function flags default
// false; the configuration wizard flips them on per the user's picks.
//
// Migration story for Secret + RequireSignature: existing WebhookConfigs
// decoded from disk before this field landed get Secret="" + Require-
// Signature=false (the zero values), keeping legacy webhook URLs
// working without any user action. The user generates Secret on the
// next Configure-webhook click (the rotate handler now stamps it
// alongside Token); the require-signature toggle stays off until the
// user explicitly opts in after pasting the Secret into Sonarr/Radarr's
// Connect config and verifying a Test event arrives.
type WebhookConfig struct {
	Token string `json:"token,omitempty"` // base64url-encoded random — empty when not configured
	// Secret is the shared password Sonarr/Radarr's Webhook config
	// sends in the Authorization: Basic <base64(user:pass)> header.
	// Sonarr/Radarr Webhook implementation does NOT support custom
	// HMAC headers — it does support HTTP Basic auth on outgoing
	// webhook calls. We therefore encode the shared secret as the
	// password field of Basic auth and validate it server-side with
	// a constant-time compare.
	//
	// Generated alongside Token at Configure-webhook time. The user
	// pastes this as the password field in Sonarr/Radarr → Settings
	// → Connect → Edit Webhook → password. Any non-empty username
	// works (e.g. "resolvarr") — only the password is checked.
	//
	// Empty Secret + RequireSignature=true is INVALID — the validator
	// on handleWebhookSetRequireSignature rejects the combination so
	// the receiver never has to fail-close at fire-time on a config
	// that can't be satisfied. Empty Secret + RequireSignature=false
	// is the legacy unsigned mode — the receiver accepts but logs
	// a warning to the ring-buffer so the user sees it in Recent
	// activity.
	Secret string `json:"secret,omitempty"`
	// RequireSignature gates strict-mode enforcement. Default false
	// for backwards compatibility (existing webhook URLs configured
	// before this change keep working without paste-the-secret-into-
	// Sonarr-Connect ceremony). User flips it on per-instance once
	// they've pasted the Secret into Sonarr/Radarr's Connect config
	// and verified a Test event arrives.
	RequireSignature bool `json:"requireSignature,omitempty"`
	LoggingEnabled   bool `json:"loggingEnabled,omitempty"` // when true, every received event is appended to the in-memory + on-disk ring
}

// PathMapping is a single from→to prefix translation. The "from"
// side is what the Arr API reports (e.g. "/movies"); the "to" side
// is the matching path inside the tagarr container (e.g.
// "/data/media/movies"). Absolute paths recommended on both sides;
// relative inputs fall through unchanged because the translator
// only matches absolute prefixes. Forward slashes only — the
// container runs on Linux so OS-portability isn't a goal.
type PathMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// DiscordConfig is the Discord webhook settings.
type DiscordConfig struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhookUrl"`
}

// LoggingConfig controls the runs.log under /config/logs/. Audit lines
// (one per scan-run summary) are always written. Debug lines (per Arr
// HTTP call) are gated on Debug; toggling at runtime takes effect on
// the next call without restart. KeepDays bounds the rotated file
// retention — older daily files are pruned at log-open time.
type LoggingConfig struct {
	Debug    bool `json:"debug"`
	KeepDays int  `json:"keepDays"` // default 14, clamped 1–90
}

// DisplayConfig is UI-appearance settings persisted server-side
// so they follow the user across devices.
//
// TimeFormat controls how the frontend renders timestamps:
//   - ""     | "auto" — use the locale derived from container TZ
//                       (or LANG when set). Norwegian TZ → 24h, US TZ → 12h.
//   - "24h"           — force 24-hour clock regardless of locale
//   - "12h"           — force 12-hour AM/PM clock regardless of locale
//
// Date order still follows the auto-detected locale (DD/MM vs MM/DD vs
// YYYY-MM-DD); only the clock portion is overridden when 24h/12h is set.
type DisplayConfig struct {
	UIScale    string `json:"uiScale"`              // "1", "1.1", "1.2" — Compact / Default / Large
	TimeFormat string `json:"timeFormat,omitempty"` // "" / "auto" / "24h" / "12h"
}

// ReleaseGroup is one entry from the bash RELEASE_GROUPS array,
// reshaped into structured JSON. The fields map onto the colon-
// delimited tuple "search:tag:display:mode" that tagarr.sh reads, plus
// a Type field that routes the group to Radarr or Sonarr — movies and
// TV generally need separate group lists because the good groups
// differ (FLUX for 4K movies, NTb for 1080p TV, etc.).
type ReleaseGroup struct {
	ID      string `json:"id"`
	Search  string `json:"search"`  // text searched in releaseGroup / sceneName / relativePath
	Tag     string `json:"tag"`     // tag label applied in Arr (lowercase, no spaces)
	Display string `json:"display"` // human-readable name for logs and Discord
	Mode    string `json:"mode"`    // "filtered" — require quality+audio | "simple" — tag every match
	Type    string `json:"type"`    // "radarr" | "sonarr" — legacy configs default to "radarr" in Load()
	// Enabled is the bash-equivalent of prefixing the array entry with `#`.
	// A disabled group stays in the config (so the user keeps their settings)
	// but is skipped by every scan mode — Tag doesn't evaluate it, Discover
	// doesn't treat its search string as "already covered", Recover ignores
	// it when parsing grab titles. Unmarshal defaults to true when the field
	// is missing (legacy configs + manual JSON edits that omit it) so an
	// upgrade can never silently flip groups off.
	Enabled bool `json:"enabled"`
}

// UnmarshalJSON defaults Enabled to true when the field is missing from the
// serialised JSON — the plain-bool default of false would silently disable
// every group on an upgrade from a config that predates the Enabled field.
// A user who explicitly sets `"enabled": false` still gets a disabled group:
// the pre-fill is only honoured when json.Unmarshal doesn't touch the field.
func (rg *ReleaseGroup) UnmarshalJSON(data []byte) error {
	type raw ReleaseGroup
	tmp := raw{Enabled: true}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*rg = ReleaseGroup(tmp)
	return nil
}

// TagBucket is the persisted shape of one auto-tag bucket
// (Resolution / Codec / Audio / HDR). Mirror of engine.BucketConfig
// but with JSON tags for persistence; kept in core so handlers don't
// need to translate when loading/saving.
//
// AllowedValues is the per-value allow-list. nil/empty = all values
// allowed; non-empty restricts emission to listed values only.
// Cleanup safety-bound is independent — see engine.AllPossible*Tags.
type TagBucket struct {
	Enabled           bool     `json:"enabled"`
	Prefix            string   `json:"prefix"`
	SonarrAggregation string   `json:"sonarrAggregation,omitempty"` // "all-occurring" | "strict" | "highest"
	AllowedValues     []string `json:"allowedValues,omitempty"`     // nil/empty = all (when SelectMode != "select")
	SelectMode        string   `json:"selectMode,omitempty"`        // "" or "all" (default — empty=all-allowed) | "select" (exact list, empty=tag nothing)
	// Labels is a sparse per-value override. Keys are canonical engine
	// values from the bucket vocabulary; values are user-chosen
	// replacement labels (subject to Radarr's ^[a-z0-9-]+$ validator
	// at save-time). Missing/empty value = use engine default.
	//
	// Prefix still applies on top of the override — Prefix="dv-" +
	// Labels["dvprofile8"]="profile8" emits "dv-profile8". To drop
	// the prefix for an override, set the bucket Prefix to empty.
	//
	// Cleanup follows the CURRENT label vocabulary. After a rename,
	// tags with the old label become orphans we no longer touch —
	// users clean those up manually in Tag inventory. Documented in
	// the UI hint next to the per-value label inputs.
	Labels map[string]string `json:"labels,omitempty"`
}

// AudioTagsConfig governs informative auto-tagging from the
// audio stream of Radarr/Sonarr's mediaInfo. Single bucket because
// codecs / channels / atmos all share one toggle + one prefix in
// the UI — they're conceptually "everything you'd say about the
// audio stream".
//
// RemoveOrphanedTags scopes to audio labels only — toggling has no
// effect on Video tags or DV detail cleanup. Off by default
// (destructive cleanup is opt-in).
//
// StripOnFileDelete: when true, the webhook handler strips this
// bucket's managed audio tags from the affected item on
// MovieFileDelete / EpisodeFileDelete / *ForUpgrade events. Off by
// default — opt-in per bucket so users can choose granularly which
// file-property tag families should auto-clean. Scope is the SINGLE
// instance that received the delete event; never mirrored cross-
// instance because audio mediaInfo is per-file. Tag-RG / filter-only
// tag strip on delete is a separate, automatic flow (driven by
// fnTagReleaseGroups on the rule) — see WebhookRule docs.
type AudioTagsConfig struct {
	Audio              TagBucket `json:"audio"`
	RemoveOrphanedTags bool      `json:"removeOrphanedTags,omitempty"`
	StripOnFileDelete  bool      `json:"stripOnFileDelete,omitempty"`
}

// VideoTagsConfig governs informative auto-tagging from the video
// stream of Radarr/Sonarr's mediaInfo. Three buckets because users
// commonly want different namespacing or selective emission per
// category.
//
// The base "dv" tag emits from the HDR bucket here. The DV detail
// layer (mel/fel/dvprofile8/cm2/cm4) is a separate scan path that
// requires opt-in tools install — see DvDetailConfig.
//
// RemoveOrphanedTags scopes to video labels only.
//
// StripOnFileDelete: when true, the webhook handler strips this
// bucket's managed video tags (resolution + codec + HDR) from the
// affected item on file-delete events. Same scope rules as
// AudioTagsConfig.StripOnFileDelete — see that doc-comment for the
// full invariant.
type VideoTagsConfig struct {
	Resolution         TagBucket `json:"resolution"`
	Codec              TagBucket `json:"codec"`
	HDR                TagBucket `json:"hdr"`
	RemoveOrphanedTags bool      `json:"removeOrphanedTags,omitempty"`
	StripOnFileDelete  bool      `json:"stripOnFileDelete,omitempty"`
}

// DvDetailConfig governs M4b Dolby Vision detail tagging. Distinct
// from VideoTags because the underlying flow is fundamentally
// different:
//
//   - Audio/Video tags read Radarr's pre-computed mediaInfo
//     (microseconds per file).
//   - DV detail shells out to ffmpeg + dovi_tool to extract the RPU
//     summary. Per-file cost is fast on remux sources (tens of ms),
//     but cumulative across hundreds of files plus fork+exec + I/O
//     overhead is enough to want a cache. Requires opt-in tools
//     install, persists results to dv-cache.json, runs as its own
//     scan action ("dvdetail") and lives in its own UI sub-section.
//
// The vocabulary is a closed set of 5 values: "mel", "fel",
// "dvprofile8", "cm2", "cm4" (canonical list lives in
// engine.DvDetailVocabulary()). One flat AllowedValues list across
// all 5. The base "dv" tag (whether the file IS DV) belongs to
// VideoTagsConfig.HDR; DV detail only contributes the additional
// profile/CM-version layer on top.
//
// Prefix is optional and validated against Radarr's ^[a-z0-9-]+$
// rule. Default empty = bare values matching the bash convention.
//
// RemoveOrphanedTags scopes to DV-detail labels only. Off by default
// (destructive cleanup is opt-in).
//
// The struct serialises into the persisted JSON even when zero-
// valued — Go's encoding/json doesn't honour omitempty on struct
// types. A fresh-install config will therefore show
// `"dvDetail":{"enabled":false}` on disk; that's expected.
type DvDetailConfig struct {
	Enabled            bool              `json:"enabled"`
	Prefix             string            `json:"prefix,omitempty"`
	AllowedValues      []string          `json:"allowedValues,omitempty"`      // nil/empty = all 5 values allowed (when SelectMode != "select")
	SelectMode         string            `json:"selectMode,omitempty"`         // "" or "all" (default) | "select" (exact list)
	RemoveOrphanedTags bool              `json:"removeOrphanedTags,omitempty"` // off by default — opt-in destructive cleanup
	StripOnFileDelete  bool              `json:"stripOnFileDelete,omitempty"`  // strip DV-detail tags from the item on file-delete events; same scope rules as AudioTagsConfig.StripOnFileDelete
	Labels             map[string]string `json:"labels,omitempty"`             // per-value override (canonical → user label); see TagBucket.Labels for full semantics
}

// Config is the top-level persisted config.
//
// Authentication credentials (username, bcrypt password hash, API key
// hash) live separately in /config/auth.json and are managed by the
// internal/auth package. The fields below are policy only — which
// auth mode is active, who bypasses it, which proxies to trust — so
// this file can be exported or shared for diagnostics without
// leaking secrets.
type Config struct {
	Instances     []Instance      `json:"instances"`
	Discord       DiscordConfig   `json:"discord"` // legacy single-Discord config — migrated to NotificationAgents on Load and persisted as empty thereafter; kept on the struct so the migration path remains idempotent across restarts
	Display       DisplayConfig   `json:"display"`
	ReleaseGroups []ReleaseGroup  `json:"releaseGroups"`
	Filters       FilterSet       `json:"filters"`
	AudioTags     AudioTagsConfig `json:"audioTags"`           // M4 Audio tags — informative auto-tags from audio mediaInfo
	VideoTags     VideoTagsConfig `json:"videoTags"`           // M4 Video tags — resolution / codec / HDR from mediaInfo (rask, ingen install)
	DvDetail      DvDetailConfig  `json:"dvDetail"`            // M4b DV detail — opt-in Dolby Vision profile/CM tags, requires ffmpeg+dovi_tool
	Schedules     []ScheduledJob  `json:"schedules,omitempty"` // saved Scan workflows — cron + options snapshots
	WebhookRules  []WebhookRule   `json:"webhookRules,omitempty"` // saved M-Webhook rules — fired by Connect events on the per-instance webhook URL

	// NotificationAgents is the multi-agent config replacing the legacy
	// flat Discord field. Each entry is one provider configuration
	// (Discord webhook, Gotify, NTFY, Pushover, Apprise) with its own
	// Events flags + credentials. Migrated once from Config.Discord on
	// first Load() of an older config; subsequent loads see this slice
	// populated and skip the migration.
	NotificationAgents []NotificationAgent `json:"notificationAgents,omitempty"`

	// (NotificationDefaults retired in 7.4e — agents own their filter
	// via agents.Agent.Events + .Functions; no per-rule-type preset
	// is needed. Legacy on-disk configs decode cleanly: unknown JSON
	// fields are ignored by encoding/json, and re-saves drop the key.)

	// Logging controls the run-log file under /config/logs/. Audit lines
	// are always written; debug lines (per Arr API call) only when enabled.
	// KeepDays drives the rotation prune — older daily files get deleted.
	Logging LoggingConfig `json:"logging,omitempty"`

	// Authentication — matches Radarr/Sonarr Security panel model.
	Authentication         string `json:"authentication,omitempty"`         // "forms" (default) | "basic" | "none"
	AuthenticationRequired string `json:"authenticationRequired,omitempty"` // "enabled" | "disabled_for_local_addresses" (default)
	TrustedProxies         string `json:"trustedProxies,omitempty"`         // comma-separated IPs — reverse-proxy deployments
	TrustedNetworks        string `json:"trustedNetworks,omitempty"`        // comma-separated IPs/CIDRs for local-bypass; empty = Radarr-parity default
	SessionTTLDays         int    `json:"sessionTtlDays,omitempty"`         // default 30

	// RecoverExclusions holds per-instance "skip these in the next
	// Recover scan" lists. Keyed by instance ID. User flags faulty
	// movies / series / seasons that aren't fixable (no grab history,
	// permanently missing from indexer, manual import they don't want
	// re-imported, etc.). The Recover scan filters them out before the
	// per-item history walk — saves API calls + scan time.
	//
	// Restored to the scan list via the "Show excluded" panel + per-row
	// Include-again button.
	RecoverExclusions map[string]RecoverExclusion `json:"recoverExclusions,omitempty"`

	// QbitInstances is a user-named list of qBittorrent instances. Kept
	// standalone (not 1:1 paired with Arr instances) so a single qBit
	// can be referenced from multiple Arr webhook configs without
	// duplicating credentials. The pairing happens per-Arr inside
	// WebhookConfig.QbitInstanceID — see internal/api/qbit_handlers.go.
	//
	// Backlog scan (Service B) targets a QbitInstance directly without
	// going through any Arr instance.
	QbitInstances []QbitInstance `json:"qbitInstances,omitempty"`
}

// QbitInstance is one user-configured qBittorrent connection.
// User picks the Name (any string they want — "qbt-main",
// "qbt-remux", whatever fits their setup); ID is auto-generated
// on create and stable thereafter so cross-references in
// WebhookConfig.QbitInstanceID survive name edits.
//
// URL accepts direct (http://192.168.1.100:8080) AND
// reverse-proxy-fronted forms (https://qbit.example.com/qbit) —
// the qbit client treats it as a base URL and joins API paths
// onto it preserving any subpath. TrustedCerts skips TLS
// verification when on, off by default.
type QbitInstance struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	TrustedCerts bool   `json:"trustedCerts,omitempty"`

	// WebhookSecret is the per-instance shared secret stamped into the
	// X-API-Key header on the qBit-side "torrent added" webhook. qBit's
	// "Run external program on torrent added" curls our endpoint and
	// includes this value; we constant-time-compare to authenticate.
	// Generated on Create (32-byte base64url-encoded crypto/rand);
	// preserved on Update; backfilled on first save for instances that
	// existed before this field landed.
	WebhookSecret string `json:"webhookSecret,omitempty"`

	// PreviousAutorunBackup stores the contents of qBit's
	// "Run external program on torrent added" field BEFORE we wrote
	// our curl into it. Lets the user undo our auto-config via a
	// "Restore previous autorun" button. Empty string means either
	// (a) we never auto-configured, or (b) qBit's field was empty
	// when we configured (so there's nothing to restore).
	PreviousAutorunBackup string `json:"previousAutorunBackup,omitempty"`

	// WebhookConfiguredInQbit is true when we successfully wrote our
	// curl into qBit's autorun field via the auto-config endpoint.
	// Drives UI state (shows "Configured automatically" badge + Reset
	// button) AND tells the rotate-secret flow it needs to also
	// update qBit's autorun field with the new key.
	WebhookConfiguredInQbit bool `json:"webhookConfiguredInQbit,omitempty"`

	// WebhookCallbackURL is the override URL qBit uses to reach
	// resolvarr. Empty (default) means "use the URL the user's browser
	// reached resolvarr at" — built from r.Host on each Configure/Show
	// command/curl-build call. Set when qBit is on a different network
	// than the browser and needs a different address (e.g.
	// http://resolvarr:6075 when qBit and resolvarr share a Docker
	// network and the user accesses resolvarr via the host's LAN IP).
	//
	// Persisted on Configure-success so rotate-secret keeps using the
	// same URL and the next modal-open hydrates the override. User can
	// clear it via the modal to fall back to r.Host detection.
	//
	// Format constraint: must be a valid http:// or https:// URL with
	// host. No trailing path — the endpoint path is appended by the
	// builder. Validated at save-time in the configure handler.
	WebhookCallbackURL string `json:"webhookCallbackUrl,omitempty"`
}

// RecoverExclusion is the per-instance skip list. Movies (Radarr) is
// a flat ID list; Series (Sonarr) maps seriesId → []seasonNumbers
// where an empty / nil seasons slice means "skip the whole series"
// and a populated slice means "skip only these seasons" (other
// seasons of the same series stay in the scan).
//
// Naming: the front-end calls this "Exclude" / "Show excluded" /
// "Include again". Stored as RecoverExclusion to keep the JSON key
// stable across UI rewords.
type RecoverExclusion struct {
	Movies []int         `json:"movies,omitempty"` // Radarr movie IDs
	Series map[int][]int `json:"series,omitempty"` // Sonarr seriesId → seasonNumbers ([] = whole series)
}

// IsMovieExcluded checks if the given Radarr movie ID is on the
// exclusion list. Linear scan is fine — exclusion lists are typically
// 0..few-dozen entries.
func (e RecoverExclusion) IsMovieExcluded(movieID int) bool {
	for _, id := range e.Movies {
		if id == movieID {
			return true
		}
	}
	return false
}

// IsSeriesFullyExcluded returns true when the whole series is on the
// skip list (seasons slice empty / nil). Per-season exclusions return
// false here — caller should also check IsSeasonExcluded for the
// specific season number.
func (e RecoverExclusion) IsSeriesFullyExcluded(seriesID int) bool {
	if e.Series == nil {
		return false
	}
	seasons, ok := e.Series[seriesID]
	return ok && len(seasons) == 0
}

// IsSeasonExcluded returns true when this exact (series, season)
// pair is excluded. Returns true when the WHOLE series is excluded
// too — convenience wrapper so the scan loop can call one helper
// per (series, season).
func (e RecoverExclusion) IsSeasonExcluded(seriesID, seasonNumber int) bool {
	if e.Series == nil {
		return false
	}
	seasons, ok := e.Series[seriesID]
	if !ok {
		return false
	}
	if len(seasons) == 0 {
		return true // whole-series exclusion catches every season
	}
	for _, s := range seasons {
		if s == seasonNumber {
			return true
		}
	}
	return false
}

// AddMovie / RemoveMovie / AddSeries / RemoveSeries / AddSeason /
// RemoveSeason are pure-function mutators. ConfigStore.Update wraps
// the caller in the lock + persistence; these are the bit-level diff.
func (e *RecoverExclusion) AddMovie(id int) {
	for _, m := range e.Movies {
		if m == id {
			return
		}
	}
	e.Movies = append(e.Movies, id)
}

func (e *RecoverExclusion) RemoveMovie(id int) {
	out := e.Movies[:0]
	for _, m := range e.Movies {
		if m != id {
			out = append(out, m)
		}
	}
	e.Movies = out
}

// AddSeries marks an entire series as excluded — replaces any
// per-season entries for that series with the empty-slice "whole
// series" sentinel.
func (e *RecoverExclusion) AddSeries(seriesID int) {
	if e.Series == nil {
		e.Series = make(map[int][]int)
	}
	e.Series[seriesID] = []int{}
}

func (e *RecoverExclusion) RemoveSeries(seriesID int) {
	if e.Series == nil {
		return
	}
	delete(e.Series, seriesID)
}

// AddSeason adds a per-season exclusion. If the series is currently
// whole-excluded (empty slice), the call is a no-op — whole-series
// already covers this season and adding the explicit number would
// downgrade the meaning.
func (e *RecoverExclusion) AddSeason(seriesID, seasonNumber int) {
	if e.Series == nil {
		e.Series = make(map[int][]int)
	}
	cur, ok := e.Series[seriesID]
	if ok && len(cur) == 0 {
		return // whole series already excluded
	}
	for _, s := range cur {
		if s == seasonNumber {
			return // already there
		}
	}
	e.Series[seriesID] = append(cur, seasonNumber)
}

// RemoveSeason removes a specific season from the per-season list.
// If the series was whole-excluded (empty slice sentinel) this call
// is a no-op — to un-exclude a whole series, callers use RemoveSeries.
// If removing the last per-season entry leaves the slice empty, the
// map entry is deleted entirely so the series is fully back in scope.
func (e *RecoverExclusion) RemoveSeason(seriesID, seasonNumber int) {
	if e.Series == nil {
		return
	}
	cur, ok := e.Series[seriesID]
	if !ok {
		return
	}
	if len(cur) == 0 {
		return // whole-series — caller should use RemoveSeries
	}
	out := cur[:0]
	for _, s := range cur {
		if s != seasonNumber {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		delete(e.Series, seriesID)
	} else {
		e.Series[seriesID] = out
	}
}

// NotificationAgent is the config shape of one notification provider entry.
// Type alias for agents.Agent — canonical definition + provider logic live
// in the agents sub-package; the alias keeps core code in domain language.
type NotificationAgent = agents.Agent

// AgentEvents controls which application events trigger notifications.
// Type alias for agents.Events.
type AgentEvents = agents.Events

// NotificationConfig is the union-struct of all provider credentials.
// Type alias for agents.Config; each provider uses only its own fields.
type NotificationConfig = agents.Config

// (NotificationDefaults type retired in 7.4e along with the per-rule
// NotifyAgents whitelist it pre-filled. Under option A — power-to-
// the-agent model — agents own their filter via Events + Functions
// config; no per-rule-type preset is meaningful.)

// ConfigStore loads and saves Config to disk with a mutex.
type ConfigStore struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func NewConfigStore(dir string) *ConfigStore {
	return &ConfigStore{path: filepath.Join(dir, "resolvarr.json")}
}

// filtersInitialized distinguishes an unconfigured filter block from one
// the user has deliberately cleared. A fresh install with no resolvarr.json
// gets DefaultFilterConfig (all on); an older resolvarr.json that predates
// the filters field gets the same defaults so existing deployments keep
// matching their bash-era behavior.
func filtersInitialized(f engine.FilterConfig) bool {
	return f.Quality || f.MAWebDL || f.PlayWebDL ||
		f.Audio || f.TrueHD || f.TrueHDAtmos || f.DTSX || f.DTSHDMA
}

// ExtraTagPrefixValid is the regex used to validate the per-bucket
// prefix string. Matches Radarr's tag-label validator `^[a-z0-9-]+$`
// but allows empty (the TRaSH bare-value default). Sonarr is more
// permissive but we enforce the strictest rule so configs written
// today still work when Sonarr support lands.
//
// Exported so the api package can reuse the same regex for input
// validation — no copy-paste drift between persistence-layer
// migration and request validation.
var ExtraTagPrefixValid = regexp.MustCompile(`^[a-z0-9-]*$`)

// clearInvalidPrefix is the shared helper used by every fill*Defaults
// — clears the prefix back to "" when it doesn't match Radarr's tag-
// label rule. Returns true when a value was actually cleared, so the
// caller can decide whether the cleanup needs to be persisted.
func clearInvalidPrefix(p *string) bool {
	if !ExtraTagPrefixValid.MatchString(*p) {
		*p = ""
		return true
	}
	return false
}

// fillAudioTagsDefaults validates the Audio bucket prefix + backfills
// the SonarrAggregation default. The bucket itself stays disabled on
// a fresh install — Audio tagging is opt-in.
//
// Returns true when migration touched anything.
func fillAudioTagsDefaults(c *AudioTagsConfig) bool {
	migrated := clearInvalidPrefix(&c.Audio.Prefix)
	if c.Audio.SonarrAggregation == "" {
		c.Audio.SonarrAggregation = "all-occurring"
	}
	return migrated
}

// fillVideoTagsDefaults validates the three video-bucket prefixes
// + backfills SonarrAggregation defaults: all-occurring for
// Resolution + Codec, strict for HDR (mixed HDR is unusual and
// usually a partial-upgrade state).
//
// Returns true when migration touched anything.
func fillVideoTagsDefaults(c *VideoTagsConfig) bool {
	migrated := false
	if clearInvalidPrefix(&c.Resolution.Prefix) {
		migrated = true
	}
	if clearInvalidPrefix(&c.Codec.Prefix) {
		migrated = true
	}
	if clearInvalidPrefix(&c.HDR.Prefix) {
		migrated = true
	}
	if c.Resolution.SonarrAggregation == "" {
		c.Resolution.SonarrAggregation = "all-occurring"
	}
	if c.Codec.SonarrAggregation == "" {
		c.Codec.SonarrAggregation = "all-occurring"
	}
	if c.HDR.SonarrAggregation == "" {
		c.HDR.SonarrAggregation = "strict"
	}
	return migrated
}

// fillDvDetailDefaults validates the DvDetail prefix — invalid
// prefixes get cleared so on-disk state always satisfies Radarr's
// `^[a-z0-9-]+$` tag rule.
//
// Enabled stays false by default; the user opts in by installing
// the tools + flipping the toggle. AllowedValues stays nil by
// default (all 5 values allowed). No SonarrAggregation field —
// DV detail is per-file extraction so no aggregation across
// episodes is meaningful (Sonarr support is deferred anyway).
//
// Returns true when an invalid prefix was cleared (caller persists
// so the on-disk JSON reflects the migration).
func fillDvDetailDefaults(c *DvDetailConfig) bool {
	migrated := false
	if !ExtraTagPrefixValid.MatchString(c.Prefix) {
		c.Prefix = ""
		migrated = true
	}
	return migrated
}

// DvDetailToEngine converts the persisted DvDetailConfig into the
// engine-side mirror struct. Same role as AudioTagsToEngine /
// VideoTagsToEngine — keeps engine helpers I/O-free + import-clean
// (no dependency on the core package). AllowedValues is deep-copied
// to defend against future engine helpers that mutate.
func DvDetailToEngine(c DvDetailConfig) engine.DvDetailConfig {
	return engine.DvDetailConfig{
		Enabled:       c.Enabled,
		Prefix:        c.Prefix,
		AllowedValues: append([]string(nil), c.AllowedValues...),
		SelectMode:    c.SelectMode,
		Labels:        copyLabels(c.Labels),
	}
}

// bucketToEngine is the shared TagBucket → engine.BucketConfig
// adapter. AllowedValues is deep-copied: engine code never mutates
// today, but pairing this with ConfigStore.Get's deep-copy means a
// future helper that sorts/dedupes/appends can't reach back into
// the persisted config.
func bucketToEngine(b TagBucket) engine.BucketConfig {
	return engine.BucketConfig{
		Enabled:           b.Enabled,
		Prefix:            b.Prefix,
		SonarrAggregation: parseAggregation(b.SonarrAggregation),
		AllowedValues:     append([]string(nil), b.AllowedValues...),
		SelectMode:        b.SelectMode,
		Labels:            copyLabels(b.Labels),
	}
}

// copyLabels deep-copies a Labels map so a downstream engine helper
// can't reach back into the persisted bucket. Returns nil for a nil
// or empty input (preserves the "no overrides" sentinel).
func copyLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// AudioTagsToEngine converts the persisted AudioTagsConfig into the
// pure-function engine config the helpers in core/engine accept.
// Mirror of VideoTagsToEngine + DvDetailToEngine.
func AudioTagsToEngine(c AudioTagsConfig) engine.AudioTagsConfig {
	return engine.AudioTagsConfig{
		Audio: bucketToEngine(c.Audio),
	}
}

// VideoTagsToEngine converts the persisted VideoTagsConfig into the
// pure-function engine config.
func VideoTagsToEngine(c VideoTagsConfig) engine.VideoTagsConfig {
	return engine.VideoTagsConfig{
		Resolution: bucketToEngine(c.Resolution),
		Codec:      bucketToEngine(c.Codec),
		HDR:        bucketToEngine(c.HDR),
	}
}

func parseAggregation(s string) engine.AggregationStrategy {
	switch s {
	case "strict":
		return engine.AggStrict
	case "highest":
		return engine.AggHighest
	default:
		return engine.AggAllOccurring
	}
}

func (s *ConfigStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.cfg = defaultConfig()
			return nil
		}
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	if s.cfg.Display.UIScale == "" {
		s.cfg.Display.UIScale = "1.1"
	}
	if s.cfg.Instances == nil {
		s.cfg.Instances = []Instance{}
	}
	if s.cfg.ReleaseGroups == nil {
		s.cfg.ReleaseGroups = []ReleaseGroup{}
	}
	// Legacy release groups had no Type field — default to "radarr"
	// because that's what tagarr.sh (the only tagging script before
	// this release) was built for. Sonarr tagging is new to the
	// container and must be opted into per-group going forward.
	for i := range s.cfg.ReleaseGroups {
		if s.cfg.ReleaseGroups[i].Type == "" {
			s.cfg.ReleaseGroups[i].Type = "radarr"
		}
	}
	// Both Radarr and Sonarr filter blocks default to the sample config
	// (every filter on). FilterSet.UnmarshalJSON already handled the
	// legacy flat → Radarr migration; here we just fill in whichever
	// side is still zero-valued.
	if !filtersInitialized(s.cfg.Filters.Radarr) {
		s.cfg.Filters.Radarr = engine.DefaultFilterConfig()
	}
	if !filtersInitialized(s.cfg.Filters.Sonarr) {
		s.cfg.Filters.Sonarr = engine.DefaultFilterConfig()
	}
	// Fill in Extra-tags defaults + migrate any pre-existing prefix that
	// violates Radarr's tag-label rule. Returns true when a migration
	// happened — caller persists so the on-disk JSON reflects the change.
	audioTagsMigrated := fillAudioTagsDefaults(&s.cfg.AudioTags)
	videoTagsMigrated := fillVideoTagsDefaults(&s.cfg.VideoTags)
	dvDetailMigrated := fillDvDetailDefaults(&s.cfg.DvDetail)
	// Auth-policy defaults for configs predating the security release.
	// Matches Radarr/Sonarr ship-defaults: Forms auth, LAN bypass on,
	// no trusted proxies, 30-day sessions. Users on the local network
	// see the app work exactly as before the upgrade; anyone behind a
	// reverse proxy hits the setup wizard on first access.
	if s.cfg.Authentication == "" {
		s.cfg.Authentication = "forms"
	}
	if s.cfg.AuthenticationRequired == "" {
		s.cfg.AuthenticationRequired = "disabled_for_local_addresses"
	}
	if s.cfg.SessionTTLDays == 0 {
		s.cfg.SessionTTLDays = 30
	}
	// Belt-and-suspenders with auth.ValidateConfig (baseline T38).
	// `sessionTtlDays * 24h` overflows int64 past ~10^14 days, and int32
	// on 32-bit ARM overflows much sooner. A user handediting 999999 or
	// pasting a malformed value shouldn't be able to break the auth
	// subsystem before validation even runs. 365 days matches the auth
	// package's own cap and is longer than any homelab deployment needs
	// between password rotations.
	if s.cfg.SessionTTLDays < 0 || s.cfg.SessionTTLDays > 365 {
		s.cfg.SessionTTLDays = 30
	}
	// Logging defaults — KeepDays clamped 1–90, default 14.
	if s.cfg.Logging.KeepDays == 0 {
		s.cfg.Logging.KeepDays = 14
	}
	if s.cfg.Logging.KeepDays < 1 || s.cfg.Logging.KeepDays > 90 {
		s.cfg.Logging.KeepDays = 14
	}
	// One-time migration: legacy Config.Discord{Enabled, WebhookURL}
	// → NotificationAgents[0]={type:discord, ...}. Idempotent: skipped
	// when NotificationAgents already has entries OR Discord URL is empty.
	// Persists immediately so the next Load sees the new shape.
	discordMigrated := s.migrateLegacyDiscord()

	// Per-rule schedule config migration: existing schedules created
	// before the rule model landed have nil Filters/ExtraTags/
	// ReleaseGroupIDs. Snapshot the current global config into each
	// schedule so they become self-contained going forward. Idempotent
	// — schedules with non-nil fields are left alone.
	schedulesMigrated := s.migrateSchedulesToRules()

	// Legacy WebhookFnFileDeleteClean → per-bucket StripOnFileDelete
	// opt-in (C5 of the M-webhook delete-semantics refactor).
	// Idempotent — rules already on the new shape are skipped.
	fileDeleteCleanMigrated := s.migrateLegacyFileDeleteClean()

	// QbitInstance.WebhookSecret backfill (Slice 1 of M-qBit-add).
	// Existing instances saved before the field landed have empty
	// secret; stamp one at startup so the dedicated webhook-config
	// endpoint works on day 1 without forcing a manual edit-and-save.
	// Idempotent — entries with a non-empty secret are skipped.
	qbitSecretsMigrated := s.migrateBackfillQbitWebhookSecrets()

	// Persist if any migration touched anything. Best-effort — if
	// the write fails, the migration runs again next start (all are
	// idempotent). Surface the write failure in logs so a read-only
	// mount or full disk doesn't go silently unobserved.
	if discordMigrated || audioTagsMigrated || videoTagsMigrated || dvDetailMigrated || schedulesMigrated || fileDeleteCleanMigrated || qbitSecretsMigrated {
		if err := s.saveLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "tagarr: config migration save failed (will retry on next Load): %v\n", err)
		}
	}

	// Defensive scan for notification agents whose persisted credentials
	// match the literal mask placeholder. This shouldn't happen post the
	// ConfigStore.Get deep-copy fix in commit 26b76fd, but pre-fix
	// corruptions can persist on disk. Surface them at startup so the
	// user knows to re-enter the credential before the next scheduled
	// fire surfaces an opaque upstream API error.
	for _, a := range s.cfg.NotificationAgents {
		c := a.Config
		corrupted := false
		switch a.Type {
		case "discord":
			corrupted = c.DiscordWebhook == agents.MaskedDiscordWebhook ||
				c.DiscordWebhookUpdates == agents.MaskedDiscordWebhook
		case "gotify":
			corrupted = c.GotifyToken == agents.MaskedToken
		case "ntfy":
			corrupted = c.NtfyToken == agents.MaskedToken
		case "pushover":
			corrupted = c.PushoverUserKey == agents.MaskedToken ||
				c.PushoverAppToken == agents.MaskedToken
		case "apprise":
			if c.AppriseToken == agents.MaskedToken {
				corrupted = true
			}
			for _, u := range c.AppriseURLs {
				if u == agents.MaskedToken {
					corrupted = true
					break
				}
			}
		}
		if corrupted {
			fmt.Fprintf(os.Stderr, "tagarr: notification agent %q (%s) has masked-placeholder credentials in storage — re-enter the real credential via Settings → Notifications. Notifications from this agent will fail until fixed.\n", a.Name, a.Type)
		}
	}

	// Migrate legacy GrabRenameCriteria fields → new TriggerOn* flags
	// for webhook rules saved before the trigger-based model landed.
	// Idempotent — MigrateLegacyTriggerFlags is a no-op when a rule
	// already has any new-style flag set.
	for i := range s.cfg.WebhookRules {
		if s.cfg.WebhookRules[i].GrabRename != nil {
			s.cfg.WebhookRules[i].GrabRename.MigrateLegacyTriggerFlags()
		}
		// QbitSe legacy flags → three-rule first-match-wins model.
		// Idempotent — a rule already on the new shape short-circuits.
		if s.cfg.WebhookRules[i].QbitSe != nil {
			s.cfg.WebhookRules[i].QbitSe.MigrateLegacyQbitSeFlags()
		}
	}

	return nil
}

// migrateLegacyDiscord moves a non-empty Config.Discord into NotificationAgents
// as a single Discord-type agent on first Load of an older config. Returns
// true when a migration happened (caller persists). Caller must hold s.mu.
func (s *ConfigStore) migrateLegacyDiscord() bool {
	if len(s.cfg.NotificationAgents) > 0 {
		return false
	}
	url := s.cfg.Discord.WebhookURL
	if url == "" {
		return false
	}
	agent := NotificationAgent{
		ID:      generateAgentID(),
		Name:    "Discord",
		Type:    "discord",
		Enabled: s.cfg.Discord.Enabled,
		Events: AgentEvents{
			OnScheduleSuccess: true, // sensible defaults for an existing user
			OnScheduleFailure: true, // who already opted into Discord
		},
		Config: NotificationConfig{
			DiscordWebhook: url,
		},
	}
	s.cfg.NotificationAgents = append(s.cfg.NotificationAgents, agent)
	// Strip the legacy field so the persisted JSON is clean. The struct
	// field stays for one release cycle so Load() can still detect old
	// configs from users who skip a version; subsequent Load() sees an
	// empty Discord{} + populated NotificationAgents and is a no-op.
	s.cfg.Discord = DiscordConfig{}
	return true
}

// migrateSchedulesToRules backfills per-rule config snapshots on
// schedules that predate the rule model. For each schedule with
// nil Filters / AudioTags / VideoTags / DvDetail / ReleaseGroupIDs,
// populate from the current global config so the schedule becomes
// self-contained:
//
//   - Filters: deep-copy of cfg.Filters.{Radarr|Sonarr} per the
//     schedule's instance type. Falls back to Radarr if the instance
//     resolution fails (instance was deleted from the cfg) — better
//     than leaving the schedule un-runnable; user can re-edit.
//   - AudioTags / VideoTags / DvDetail: deep-copy of the matching
//     global config section. Independent value-types so the schedule's
//     snapshot is unaffected by future global edits.
//   - ReleaseGroupIDs: every cfg.ReleaseGroups[].ID where the group's
//     Type matches the schedule's instance type AND Enabled is true.
//     Captures the user's current "active subset" intent.
//
// Returns true when any schedule was migrated (caller persists).
// Idempotent — schedules with already-populated fields are skipped.
// Caller must hold s.mu.
func (s *ConfigStore) migrateSchedulesToRules() bool {
	migrated := false
	for i := range s.cfg.Schedules {
		sched := &s.cfg.Schedules[i]

		// Resolve the instance type for this schedule (Radarr vs Sonarr)
		// so the Filters block picks the right side. Falls back to
		// "radarr" if the instance can't be found — schedule may have
		// been orphaned by a delete; user will re-edit.
		instType := "radarr"
		for _, inst := range s.cfg.Instances {
			if inst.ID == sched.InstanceID {
				instType = inst.Type
				break
			}
		}

		if sched.Filters == nil {
			var src engine.FilterConfig
			switch instType {
			case "sonarr":
				src = s.cfg.Filters.Sonarr
			default:
				src = s.cfg.Filters.Radarr
			}
			f := src // copy
			sched.Filters = &f
			migrated = true
		}

		if sched.AudioTags == nil {
			at := s.cfg.AudioTags
			at.Audio.AllowedValues = append([]string(nil), s.cfg.AudioTags.Audio.AllowedValues...)
			at.Audio.Labels = copyLabels(s.cfg.AudioTags.Audio.Labels)
			sched.AudioTags = &at
			migrated = true
		}
		if sched.VideoTags == nil {
			vt := s.cfg.VideoTags
			vt.Resolution.AllowedValues = append([]string(nil), s.cfg.VideoTags.Resolution.AllowedValues...)
			vt.Resolution.Labels = copyLabels(s.cfg.VideoTags.Resolution.Labels)
			vt.Codec.AllowedValues = append([]string(nil), s.cfg.VideoTags.Codec.AllowedValues...)
			vt.Codec.Labels = copyLabels(s.cfg.VideoTags.Codec.Labels)
			vt.HDR.AllowedValues = append([]string(nil), s.cfg.VideoTags.HDR.AllowedValues...)
			vt.HDR.Labels = copyLabels(s.cfg.VideoTags.HDR.Labels)
			sched.VideoTags = &vt
			migrated = true
		}
		if sched.DvDetail == nil {
			dd := s.cfg.DvDetail
			dd.AllowedValues = append([]string(nil), s.cfg.DvDetail.AllowedValues...)
			dd.Labels = copyLabels(s.cfg.DvDetail.Labels)
			sched.DvDetail = &dd
			migrated = true
		}

		if sched.ReleaseGroupIDs == nil {
			ids := []string{}
			for _, g := range s.cfg.ReleaseGroups {
				if g.Type == instType && g.Enabled {
					ids = append(ids, g.ID)
				}
			}
			sched.ReleaseGroupIDs = ids
			migrated = true
		}

		// Per-bucket target migration. Pre-existing rules carry the
		// boolean AutoTagsRunOnSecondary flag. Translate it once into
		// per-bucket targets and clear the legacy flag so subsequent
		// loads see populated targets and the JSON drops the old key.
		// DV target defaults to 'primary' (DV was always single-instance
		// pre-migration; user can flip to 'both' explicitly post-migration).
		if sched.Options.AudioTagsTarget == "" {
			if sched.Options.AutoTagsRunOnSecondary {
				sched.Options.AudioTagsTarget = JobTargetBoth
			} else {
				sched.Options.AudioTagsTarget = JobTargetPrimary
			}
			migrated = true
		}
		if sched.Options.VideoTagsTarget == "" {
			if sched.Options.AutoTagsRunOnSecondary {
				sched.Options.VideoTagsTarget = JobTargetBoth
			} else {
				sched.Options.VideoTagsTarget = JobTargetPrimary
			}
			migrated = true
		}
		if sched.Options.DvDetailTarget == "" {
			sched.Options.DvDetailTarget = JobTargetPrimary
			migrated = true
		}
		// Clear the legacy flag now that the targets carry the truth.
		// Persisted JSON drops it via omitempty since false is its zero.
		if sched.Options.AutoTagsRunOnSecondary {
			sched.Options.AutoTagsRunOnSecondary = false
			migrated = true
		}

		// Filter-only tag-mode validation. TagSource clamps to known
		// values; FilterOnlyTag gets a sensible default when empty so
		// a UI bug or hand-edit can't leave a filter-only rule unable
		// to run. Default reflects the OOTB filter (MA/Play WEB-DL +
		// lossless audio); user can rename anytime via the wizard.
		switch sched.Options.TagSource {
		case "", "active", "discover", "filter-only":
			// OK
		default:
			sched.Options.TagSource = ""
			migrated = true
		}
		if sched.Options.TagSource == "filter-only" && sched.Options.FilterOnlyTag == "" {
			sched.Options.FilterOnlyTag = "lossless-web"
			migrated = true
		}
	}
	return migrated
}

// migrateLegacyFileDeleteClean converts webhook rules that still carry
// the legacy WebhookFnFileDeleteClean function into the per-bucket
// StripOnFileDelete opt-in model (C5 of the M-webhook delete-semantics
// refactor). For each rule with the legacy function in Functions:
//
//  1. Materialise nil snapshots (AudioTags / VideoTags / DvDetail) from
//     the current globals — same deep-copy pattern as
//     migrateSchedulesToRules so future global edits don't drift the
//     rule's behaviour.
//  2. Set StripOnFileDelete=true on each of the three bucket snapshots.
//     Sonarr rules get the flag on DvDetail too — the file-delete
//     dispatcher gates on AppType=="radarr", so a Sonarr snapshot with
//     the flag set stays inert.
//  3. Drop WebhookFnFileDeleteClean from r.Functions, preserving
//     relative order of surviving functions.
//
// Returns true when any rule was migrated (caller persists).
// Idempotent — rules without the legacy function are skipped, so
// re-running is a no-op once a deployment has caught up. Caller must
// hold s.mu.
func (s *ConfigStore) migrateLegacyFileDeleteClean() bool {
	migrated := false
	for i := range s.cfg.WebhookRules {
		r := &s.cfg.WebhookRules[i]
		if !r.HasFunction(WebhookFnFileDeleteClean) {
			continue
		}

		// Materialise snapshots if missing — mirrors
		// migrateSchedulesToRules deep-copy semantics so subsequent
		// edits to globals don't drift the migrated rule.
		if r.AudioTags == nil {
			at := s.cfg.AudioTags
			at.Audio.AllowedValues = append([]string(nil), s.cfg.AudioTags.Audio.AllowedValues...)
			at.Audio.Labels = copyLabels(s.cfg.AudioTags.Audio.Labels)
			r.AudioTags = &at
		}
		r.AudioTags.StripOnFileDelete = true

		if r.VideoTags == nil {
			vt := s.cfg.VideoTags
			vt.Resolution.AllowedValues = append([]string(nil), s.cfg.VideoTags.Resolution.AllowedValues...)
			vt.Resolution.Labels = copyLabels(s.cfg.VideoTags.Resolution.Labels)
			vt.Codec.AllowedValues = append([]string(nil), s.cfg.VideoTags.Codec.AllowedValues...)
			vt.Codec.Labels = copyLabels(s.cfg.VideoTags.Codec.Labels)
			vt.HDR.AllowedValues = append([]string(nil), s.cfg.VideoTags.HDR.AllowedValues...)
			vt.HDR.Labels = copyLabels(s.cfg.VideoTags.HDR.Labels)
			r.VideoTags = &vt
		}
		r.VideoTags.StripOnFileDelete = true

		if r.DvDetail == nil {
			dd := s.cfg.DvDetail
			dd.AllowedValues = append([]string(nil), s.cfg.DvDetail.AllowedValues...)
			dd.Labels = copyLabels(s.cfg.DvDetail.Labels)
			r.DvDetail = &dd
		}
		r.DvDetail.StripOnFileDelete = true

		newFns := make([]WebhookFunction, 0, len(r.Functions))
		for _, fn := range r.Functions {
			if fn != WebhookFnFileDeleteClean {
				newFns = append(newFns, fn)
			}
		}
		r.Functions = newFns

		migrated = true
	}
	return migrated
}

// migrateBackfillQbitWebhookSecrets stamps a fresh WebhookSecret on
// any QbitInstance loaded with an empty value. Backfill-on-Load (vs
// backfill-on-Update) means the dedicated /webhook endpoint added in
// Slice 4 of M-qBit-add works on day 1 for legacy installs without
// requiring the user to edit-and-save the qBit instance first.
//
// Format mirrors api/webhooks.go generateWebhookSecret: 32 bytes of
// crypto/rand, base64url-encoded (no padding) → 43 chars, same threat
// model as the per-Arr webhook secret. Caller must hold s.mu.
//
// Idempotent — entries with a non-empty secret are skipped, so re-
// running on already-migrated data is a no-op.
func (s *ConfigStore) migrateBackfillQbitWebhookSecrets() bool {
	migrated := false
	for i := range s.cfg.QbitInstances {
		if s.cfg.QbitInstances[i].WebhookSecret != "" {
			continue
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			// Defensive: rand.Read on Linux can't fail under normal
			// conditions, but if it does we skip this entry rather
			// than stamp an empty secret. Next Update will retry
			// via the handler-side generator.
			fmt.Fprintf(os.Stderr, "tagarr: backfill QbitInstance.WebhookSecret rand: %v (skipping %s)\n", err, s.cfg.QbitInstances[i].ID)
			continue
		}
		s.cfg.QbitInstances[i].WebhookSecret = base64.RawURLEncoding.EncodeToString(b)
		migrated = true
	}
	return migrated
}

// generateAgentID is the migration-time ID generator. Notifications
// won't have many entries — short hex is enough. Mirrors the api-side
// genID; kept local here so config.go doesn't depend on the api package.
func generateAgentID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get returns a deep copy of the config so callers can't mutate the
// store's internal slice without holding the lock.
func (s *ConfigStore) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.cfg
	out.Instances = make([]Instance, len(s.cfg.Instances))
	for i, inst := range s.cfg.Instances {
		out.Instances[i] = inst
		// Deep-copy PathMappings []PathMapping — same class of
		// header-aliasing bug as NotificationAgents.AppriseURLs and
		// ExtraTags.*.AllowedValues. Without this copy, the
		// translator's longest-prefix-first sort (which works on a
		// local copy of the slice header but not the backing array
		// elements... wait, the backing array elements ARE the
		// PathMapping structs that get sorted in `prepared`) — to
		// be precise: TranslatePath builds its own `prepared` copy
		// today so the sort itself is safe, but a future caller
		// mutating returned-Instance.PathMappings in place would
		// race the store under a concurrent UI write. Defence in
		// depth, matches the rest of this function.
		if len(inst.PathMappings) > 0 {
			out.Instances[i].PathMappings = append([]PathMapping(nil), inst.PathMappings...)
		}
	}
	out.ReleaseGroups = append([]ReleaseGroup(nil), s.cfg.ReleaseGroups...)
	// Schedules need a deeper copy than a slice append because each
	// ScheduledJob embeds a slice (History) that otherwise shares the
	// backing array with the store's copy. Callers modifying returned
	// History would race the store's lock.
	if len(s.cfg.Schedules) > 0 {
		out.Schedules = make([]ScheduledJob, len(s.cfg.Schedules))
		for i, j := range s.cfg.Schedules {
			out.Schedules[i] = j
			out.Schedules[i].History = append([]JobRun(nil), j.History...)
			// Deep-copy per-rule config snapshots so callers mutating
			// the returned schedule (e.g. validators that strip-then-
			// re-add fields) can't corrupt the store. Same defensive
			// pattern as NotificationAgents.AppriseURLs.
			if j.Filters != nil {
				f := *j.Filters
				out.Schedules[i].Filters = &f
			}
			if j.AudioTags != nil {
				at := *j.AudioTags
				at.Audio.AllowedValues = append([]string(nil), j.AudioTags.Audio.AllowedValues...)
				at.Audio.Labels = copyLabels(j.AudioTags.Audio.Labels)
				out.Schedules[i].AudioTags = &at
			}
			if j.VideoTags != nil {
				vt := *j.VideoTags
				vt.Resolution.AllowedValues = append([]string(nil), j.VideoTags.Resolution.AllowedValues...)
				vt.Resolution.Labels = copyLabels(j.VideoTags.Resolution.Labels)
				vt.Codec.AllowedValues = append([]string(nil), j.VideoTags.Codec.AllowedValues...)
				vt.Codec.Labels = copyLabels(j.VideoTags.Codec.Labels)
				vt.HDR.AllowedValues = append([]string(nil), j.VideoTags.HDR.AllowedValues...)
				vt.HDR.Labels = copyLabels(j.VideoTags.HDR.Labels)
				out.Schedules[i].VideoTags = &vt
			}
			if j.DvDetail != nil {
				dd := *j.DvDetail
				dd.AllowedValues = append([]string(nil), j.DvDetail.AllowedValues...)
				dd.Labels = copyLabels(j.DvDetail.Labels)
				out.Schedules[i].DvDetail = &dd
			}
			if j.ReleaseGroupIDs != nil {
				out.Schedules[i].ReleaseGroupIDs = append([]string(nil), j.ReleaseGroupIDs...)
			}
		}
	}
	// WebhookRules — same deep-copy pattern as Schedules. Each rule
	// embeds a History slice + per-rule snapshot pointers + nested
	// criteria structs (GrabRename / QbitSe). A caller mutating the
	// returned rule's slices would otherwise share backing arrays with
	// the store — same header-aliasing class as Schedules + the
	// NotificationAgents.AppriseURLs incident.
	if len(s.cfg.WebhookRules) > 0 {
		out.WebhookRules = make([]WebhookRule, len(s.cfg.WebhookRules))
		for i, r := range s.cfg.WebhookRules {
			out.WebhookRules[i] = r
			out.WebhookRules[i].Functions = append([]WebhookFunction(nil), r.Functions...)
			out.WebhookRules[i].History = append([]WebhookRuleRun(nil), r.History...)
			if r.Filters != nil {
				f := *r.Filters
				out.WebhookRules[i].Filters = &f
			}
			if r.AudioTags != nil {
				at := *r.AudioTags
				at.Audio.AllowedValues = append([]string(nil), r.AudioTags.Audio.AllowedValues...)
				at.Audio.Labels = copyLabels(r.AudioTags.Audio.Labels)
				out.WebhookRules[i].AudioTags = &at
			}
			if r.VideoTags != nil {
				vt := *r.VideoTags
				vt.Resolution.AllowedValues = append([]string(nil), r.VideoTags.Resolution.AllowedValues...)
				vt.Resolution.Labels = copyLabels(r.VideoTags.Resolution.Labels)
				vt.Codec.AllowedValues = append([]string(nil), r.VideoTags.Codec.AllowedValues...)
				vt.Codec.Labels = copyLabels(r.VideoTags.Codec.Labels)
				vt.HDR.AllowedValues = append([]string(nil), r.VideoTags.HDR.AllowedValues...)
				vt.HDR.Labels = copyLabels(r.VideoTags.HDR.Labels)
				out.WebhookRules[i].VideoTags = &vt
			}
			if r.DvDetail != nil {
				dd := *r.DvDetail
				dd.AllowedValues = append([]string(nil), r.DvDetail.AllowedValues...)
				dd.Labels = copyLabels(r.DvDetail.Labels)
				out.WebhookRules[i].DvDetail = &dd
			}
			if r.ReleaseGroupIDs != nil {
				out.WebhookRules[i].ReleaseGroupIDs = append([]string(nil), r.ReleaseGroupIDs...)
			}
			if r.GrabRename != nil {
				gr := *r.GrabRename
				if r.GrabRename.AppendReleaseGroup != nil {
					b := *r.GrabRename.AppendReleaseGroup
					gr.AppendReleaseGroup = &b
				}
				gr.SourceTokens = append([]string(nil), r.GrabRename.SourceTokens...)
				gr.MovieVersionTokens = append([]string(nil), r.GrabRename.MovieVersionTokens...)
				gr.GroupBlocklist = append([]string(nil), r.GrabRename.GroupBlocklist...)
				gr.CustomTokens = append([]GrabRenameCustomToken(nil), r.GrabRename.CustomTokens...)
				out.WebhookRules[i].GrabRename = &gr
			}
			if r.QbitSe != nil {
				qs := *r.QbitSe
				out.WebhookRules[i].QbitSe = &qs
			}
			if r.QbitCategoryFix != nil {
				qc := *r.QbitCategoryFix
				out.WebhookRules[i].QbitCategoryFix = &qc
			}
			// Per-rule webhook config (Token + Secret) — was previously
			// not deep-copied. Without this clone, masking handlers that
			// mutated Webhook.Token in the returned snapshot would race
			// + persist masked values back through ConfigStore.Update.
			// Closes the class with all the other pointer fields above.
			if r.Webhook != nil {
				w := *r.Webhook
				out.WebhookRules[i].Webhook = &w
			}
		}
	}
	// Deep-copy NotificationAgents — each agent's Config struct embeds
	// AppriseURLs []string. The shallow append-copy at the agent-slice
	// level is enough today (handlers replace .Config wholesale via
	// MaskNotificationAgentConfig rather than mutating fields in place),
	// but copy AppriseURLs explicitly for defence in depth so a future
	// caller that does in-place URL-list mutation can't corrupt the
	// store. Without this copy, masking the returned slice writes back
	// into the store and replaces real webhook URLs / tokens with the
	// "[MASKED]" placeholder strings — silently breaking notifications
	// after the first GET.
	if len(s.cfg.NotificationAgents) > 0 {
		out.NotificationAgents = make([]NotificationAgent, len(s.cfg.NotificationAgents))
		for i, a := range s.cfg.NotificationAgents {
			out.NotificationAgents[i] = a
			if len(a.Config.AppriseURLs) > 0 {
				out.NotificationAgents[i].Config.AppriseURLs = append([]string(nil), a.Config.AppriseURLs...)
			}
		}
	}
	// (NotificationDefaults deep-copy retired in 7.4e along with the
	// struct itself.)
	// Deep-copy QbitInstances. Each entry's struct holds only string +
	// bool fields (no nested slices), so a slice-of-struct copy is
	// enough — callers (qbit_se_backlog, webhook_grab_rename, etc.)
	// take *QbitInstance pointers via findQbitInstanceByID and read
	// URL / Username / Password immutably. Without this copy, a
	// concurrent ConfigStore.Update that does `c.QbitInstances =
	// append(c.QbitInstances[:i], c.QbitInstances[i+1:]...)` (the
	// standard delete-by-shift) would mutate the backing array under
	// the reader, race-detector flag + potentially torn (ptr,len)
	// reads on string fields under tight GC timing.
	if len(s.cfg.QbitInstances) > 0 {
		out.QbitInstances = append([]QbitInstance(nil), s.cfg.QbitInstances...)
	}
	// Deep-copy AllowedValues slices on every auto-tag bucket — same
	// header-aliasing class as NotificationAgents.AppriseURLs above.
	// A caller mutating the returned slice must not corrupt the store.
	out.AudioTags.Audio.AllowedValues = append([]string(nil), s.cfg.AudioTags.Audio.AllowedValues...)
	out.AudioTags.Audio.Labels = copyLabels(s.cfg.AudioTags.Audio.Labels)
	out.VideoTags.Resolution.AllowedValues = append([]string(nil), s.cfg.VideoTags.Resolution.AllowedValues...)
	out.VideoTags.Resolution.Labels = copyLabels(s.cfg.VideoTags.Resolution.Labels)
	out.VideoTags.Codec.AllowedValues = append([]string(nil), s.cfg.VideoTags.Codec.AllowedValues...)
	out.VideoTags.Codec.Labels = copyLabels(s.cfg.VideoTags.Codec.Labels)
	out.VideoTags.HDR.AllowedValues = append([]string(nil), s.cfg.VideoTags.HDR.AllowedValues...)
	out.VideoTags.HDR.Labels = copyLabels(s.cfg.VideoTags.HDR.Labels)
	out.DvDetail.AllowedValues = append([]string(nil), s.cfg.DvDetail.AllowedValues...)
	out.DvDetail.Labels = copyLabels(s.cfg.DvDetail.Labels)
	// Deep-copy RecoverExclusions — outer map AND every per-instance
	// Movies + Series-by-id slice. The Recover scan caches `excl :=
	// cfg.RecoverExclusions[inst.ID]` for the entire scan duration
	// (seconds → minutes on large libraries), so a concurrent
	// POST/DELETE that does the standard filter-in-place pattern
	// (out := e.Movies[:0]) would mutate the very backing array the
	// in-flight scan reads from. Header-aliasing race detector would
	// flag this; deep copy here forecloses the class.
	if len(s.cfg.RecoverExclusions) > 0 {
		out.RecoverExclusions = make(map[string]RecoverExclusion, len(s.cfg.RecoverExclusions))
		for k, v := range s.cfg.RecoverExclusions {
			ne := RecoverExclusion{Movies: append([]int(nil), v.Movies...)}
			if len(v.Series) > 0 {
				ne.Series = make(map[int][]int, len(v.Series))
				for sid, seasons := range v.Series {
					ne.Series[sid] = append([]int(nil), seasons...)
				}
			}
			out.RecoverExclusions[k] = ne
		}
	}
	return out
}

// Update applies mutator to a copy of the config and persists the result.
// The mutator sees a pointer it can modify freely.
func (s *ConfigStore) Update(mutator func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutator(&s.cfg)
	return s.saveLocked()
}

// AddNotificationAgent persists a new agent and returns the stored copy
// (server-assigned ID + normalized fields). The caller passes a partial
// agent without an ID; we generate one. Multiple agents of the same
// type are allowed (e.g. two Discord channels for sync vs alerts).
func (s *ConfigStore) AddNotificationAgent(agent NotificationAgent) (NotificationAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Always overwrite — clients can't pin an ID. Stops a malicious or
	// buggy client from creating a duplicate-ID agent that becomes
	// unreachable through GET-by-id but still loads + fires from the
	// dispatcher.
	agent.ID = generateAgentID()
	s.cfg.NotificationAgents = append(s.cfg.NotificationAgents, agent)
	if err := s.saveLocked(); err != nil {
		// Roll back the in-memory append so the next read doesn't see
		// an entry that isn't on disk.
		s.cfg.NotificationAgents = s.cfg.NotificationAgents[:len(s.cfg.NotificationAgents)-1]
		return NotificationAgent{}, err
	}
	return agent, nil
}

// GetNotificationAgent looks up an agent by ID. Returns the stored copy
// (with REAL credentials, not masked) and ok=true on hit, zero+false
// on miss. Caller is responsible for masking before sending to clients.
func (s *ConfigStore) GetNotificationAgent(id string) (NotificationAgent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.cfg.NotificationAgents {
		if a.ID == id {
			return a, true
		}
	}
	return NotificationAgent{}, false
}

// UpdateNotificationAgent replaces the agent at id with the supplied
// data, preserving the ID. Returns the stored copy or an error if the
// ID isn't found.
func (s *ConfigStore) UpdateNotificationAgent(id string, agent NotificationAgent) (NotificationAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.cfg.NotificationAgents {
		if a.ID == id {
			agent.ID = id // pin the ID so callers can't move it
			prev := s.cfg.NotificationAgents[i]
			s.cfg.NotificationAgents[i] = agent
			if err := s.saveLocked(); err != nil {
				s.cfg.NotificationAgents[i] = prev
				return NotificationAgent{}, err
			}
			return agent, nil
		}
	}
	return NotificationAgent{}, fmt.Errorf("notification agent %q not found", id)
}

// DeleteNotificationAgent removes the agent at id. Returns nil on
// success or an error if the ID isn't found.
func (s *ConfigStore) DeleteNotificationAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.cfg.NotificationAgents {
		if a.ID == id {
			// Snapshot by COPY before mutating — `append(s[:i], s[i+1:]...)`
			// shifts elements left in the shared backing array, so a
			// header-only snapshot would not survive a saveLocked
			// failure (the array slots are corrupted: deleted entry
			// gone, last slot duplicated). Element-by-element copy
			// gives a true backup we can restore to.
			prev := append([]NotificationAgent(nil), s.cfg.NotificationAgents...)
			s.cfg.NotificationAgents = append(s.cfg.NotificationAgents[:i], s.cfg.NotificationAgents[i+1:]...)
			if err := s.saveLocked(); err != nil {
				s.cfg.NotificationAgents = prev
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("notification agent %q not found", id)
}

func (s *ConfigStore) saveLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// 0600 because resolvarr.json contains Radarr/Sonarr API keys and the
	// Discord webhook URL (itself a bearer token). Atomic write with a
	// random tmp-suffix — see baseline T71.
	return utils.AtomicWriteFile(s.path, data, 0600)
}

func defaultConfig() Config {
	cfg := Config{
		Instances:     []Instance{},
		Discord:       DiscordConfig{},
		Display:       DisplayConfig{UIScale: "1.1"},
		ReleaseGroups: []ReleaseGroup{},
		Filters: FilterSet{
			Radarr: engine.DefaultFilterConfig(),
			Sonarr: engine.DefaultFilterConfig(),
		},
		Schedules:              []ScheduledJob{},
		WebhookRules:           []WebhookRule{},
		Authentication:         "forms",
		AuthenticationRequired: "disabled_for_local_addresses",
		SessionTTLDays:         30,
	}
	fillAudioTagsDefaults(&cfg.AudioTags)
	fillVideoTagsDefaults(&cfg.VideoTags)
	fillDvDetailDefaults(&cfg.DvDetail)
	return cfg
}
