package core

import (
	"crypto/rand"
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
type AudioTagsConfig struct {
	Audio              TagBucket `json:"audio"`
	RemoveOrphanedTags bool      `json:"removeOrphanedTags,omitempty"`
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
type VideoTagsConfig struct {
	Resolution         TagBucket `json:"resolution"`
	Codec              TagBucket `json:"codec"`
	HDR                TagBucket `json:"hdr"`
	RemoveOrphanedTags bool      `json:"removeOrphanedTags,omitempty"`
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
	Enabled            bool     `json:"enabled"`
	Prefix             string   `json:"prefix,omitempty"`
	AllowedValues      []string `json:"allowedValues,omitempty"`      // nil/empty = all 5 values allowed (when SelectMode != "select")
	SelectMode         string   `json:"selectMode,omitempty"`         // "" or "all" (default) | "select" (exact list)
	RemoveOrphanedTags bool     `json:"removeOrphanedTags,omitempty"` // off by default — opt-in destructive cleanup
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

	// NotificationAgents is the multi-agent config replacing the legacy
	// flat Discord field. Each entry is one provider configuration
	// (Discord webhook, Gotify, NTFY, Pushover, Apprise) with its own
	// Events flags + credentials. Migrated once from Config.Discord on
	// first Load() of an older config; subsequent loads see this slice
	// populated and skip the migration.
	NotificationAgents []NotificationAgent `json:"notificationAgents,omitempty"`

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
	}
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

	// Persist if any migration touched anything. Best-effort — if
	// the write fails, the migration runs again next start (all are
	// idempotent). Surface the write failure in logs so a read-only
	// mount or full disk doesn't go silently unobserved.
	if discordMigrated || audioTagsMigrated || videoTagsMigrated || dvDetailMigrated || schedulesMigrated {
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
			sched.AudioTags = &at
			migrated = true
		}
		if sched.VideoTags == nil {
			vt := s.cfg.VideoTags
			vt.Resolution.AllowedValues = append([]string(nil), s.cfg.VideoTags.Resolution.AllowedValues...)
			vt.Codec.AllowedValues = append([]string(nil), s.cfg.VideoTags.Codec.AllowedValues...)
			vt.HDR.AllowedValues = append([]string(nil), s.cfg.VideoTags.HDR.AllowedValues...)
			sched.VideoTags = &vt
			migrated = true
		}
		if sched.DvDetail == nil {
			dd := s.cfg.DvDetail
			dd.AllowedValues = append([]string(nil), s.cfg.DvDetail.AllowedValues...)
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
				out.Schedules[i].AudioTags = &at
			}
			if j.VideoTags != nil {
				vt := *j.VideoTags
				vt.Resolution.AllowedValues = append([]string(nil), j.VideoTags.Resolution.AllowedValues...)
				vt.Codec.AllowedValues = append([]string(nil), j.VideoTags.Codec.AllowedValues...)
				vt.HDR.AllowedValues = append([]string(nil), j.VideoTags.HDR.AllowedValues...)
				out.Schedules[i].VideoTags = &vt
			}
			if j.DvDetail != nil {
				dd := *j.DvDetail
				dd.AllowedValues = append([]string(nil), j.DvDetail.AllowedValues...)
				out.Schedules[i].DvDetail = &dd
			}
			if j.ReleaseGroupIDs != nil {
				out.Schedules[i].ReleaseGroupIDs = append([]string(nil), j.ReleaseGroupIDs...)
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
	// Deep-copy AllowedValues slices on every auto-tag bucket — same
	// header-aliasing class as NotificationAgents.AppriseURLs above.
	// A caller mutating the returned slice must not corrupt the store.
	out.AudioTags.Audio.AllowedValues = append([]string(nil), s.cfg.AudioTags.Audio.AllowedValues...)
	out.VideoTags.Resolution.AllowedValues = append([]string(nil), s.cfg.VideoTags.Resolution.AllowedValues...)
	out.VideoTags.Codec.AllowedValues = append([]string(nil), s.cfg.VideoTags.Codec.AllowedValues...)
	out.VideoTags.HDR.AllowedValues = append([]string(nil), s.cfg.VideoTags.HDR.AllowedValues...)
	out.DvDetail.AllowedValues = append([]string(nil), s.cfg.DvDetail.AllowedValues...)
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
		Authentication:         "forms",
		AuthenticationRequired: "disabled_for_local_addresses",
		SessionTTLDays:         30,
	}
	fillAudioTagsDefaults(&cfg.AudioTags)
	fillVideoTagsDefaults(&cfg.VideoTags)
	fillDvDetailDefaults(&cfg.DvDetail)
	return cfg
}
