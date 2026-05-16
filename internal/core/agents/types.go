package agents

import (
	"io"
	"net/http"
	"strings"
)

// Agent describes one configured notification provider instance.
// Users may create multiple Agent entries, including multiple entries of the
// same Type (e.g. two Discord webhooks for different channels). Agents are
// persisted in the config's AutoSync.NotificationAgents slice.
type Agent struct {
	ID      string `json:"id"`      // stable unique identifier for updates/deletes
	Name    string `json:"name"`    // user-defined label, e.g. "Discord #alerts"
	Type    string `json:"type"`    // registered provider type, e.g. "discord" | "gotify" | "pushover"
	Enabled bool   `json:"enabled"` // false keeps config saved but skips delivery
	Events  Events `json:"events"`  // event-class subscription flags (OnImport / OnGrab / OnFileDelete / OnScheduleSuccess / OnScheduleFailure)
	Config  Config `json:"config"`  // provider-specific credentials and options

	// Functions is the per-agent subscription whitelist for the
	// M-Webhook notification framework. Each entry is a
	// core.WebhookFunction constant string ("tagReleaseGroups",
	// "tagAudio", "grabRename", etc.). Empty list means "all
	// functions" (the simplest opt-in path: just enable the agent,
	// no further config needed). Non-empty list = whitelist; the
	// notification dispatcher filters per-rule fire results to
	// functions in this list before building the embed for THIS
	// agent — each agent sees only its subscribed functions.
	//
	// Layered with Events: Events gates on event-class (Import vs
	// Grab vs Delete), Functions gates on which specific function
	// inside that event class produces the embed. An agent with
	// `OnImport=true, Functions=["tagAudio"]` only renders Tag-Audio
	// changes on Import events, ignoring everything else.
	//
	// Validation of constant values lives in
	// core.ValidateNotificationAgent — the agents package
	// intentionally doesn't know the webhook function vocabulary
	// (would create a layering cycle since constants live in core).
	Functions []string `json:"functions,omitempty"`
}

// Events controls which application events trigger notifications for an agent.
// Each flag corresponds to a distinct event category in Clonarr's lifecycle.
// When a flag is false the agent is silently skipped for that event type.
// Events controls which application events trigger notifications for an
// agent. Tagarr's domain is scheduled runs (today) and webhook events
// (when the Radarr Connect receiver lands). Each event has an explicit
// per-agent toggle so a user can route different events to different
// agents (e.g. errors → Pushover, summaries → Discord).
type Events struct {
	OnScheduleSuccess bool `json:"onScheduleSuccess"` // scheduled run finished without error (tagarr scope)
	OnScheduleFailure bool `json:"onScheduleFailure"` // scheduled run errored (tagarr scope)
	// Webhook events (deferred to a later session — Radarr Connect receiver):
	OnImport     bool `json:"onImport,omitempty"`     // Import / Upgrade / ManualImport / DownloadMovie
	OnGrab       bool `json:"onGrab,omitempty"`       // grab event (release indexed + sent to download client)
	OnUpgrade    bool `json:"onUpgrade,omitempty"`    // upgrade-specific subset of import (movie file replaced)
	OnFileDelete bool `json:"onFileDelete,omitempty"` // movieFile / episodeFile deletion event
	// Legacy clonarr fields kept on the struct so the agents/ tree stays
	// drift-compatible. Always false in tagarr; ignored by tagarr providers.
	OnSyncSuccess bool `json:"onSyncSuccess,omitempty"`
	OnSyncFailure bool `json:"onSyncFailure,omitempty"`
	OnCleanup     bool `json:"onCleanup,omitempty"`
	OnRepoUpdate  bool `json:"onRepoUpdate,omitempty"`
	OnChangelog   bool `json:"onChangelog,omitempty"`
}

// Config holds credentials and options for all supported providers.
// This is a union struct — each provider uses only the fields relevant to its
// own Type. Fields are omitempty so unrelated providers do not bloat the JSON
// persisted to resolvarr.json.
//
// When adding a new provider, append its fields here with an omitempty tag and
// a grouping comment. Then implement MaskConfig and PreserveConfig in the
// provider to handle credential round-trips with the UI.
type Config struct {
	// Discord — webhook URLs for embed-based notifications.
	DiscordWebhook        string `json:"discordWebhook,omitempty"`        // primary webhook (sync, cleanup, errors)
	DiscordWebhookUpdates string `json:"discordWebhookUpdates,omitempty"` // optional separate channel for repo/changelog events

	// Gotify — self-hosted push notification server.
	GotifyURL              string `json:"gotifyUrl,omitempty"`              // base server URL (e.g. https://gotify.example.com)
	GotifyToken            string `json:"gotifyToken,omitempty"`            // application token for message submission
	GotifyPriorityCritical bool   `json:"gotifyPriorityCritical,omitempty"` // enable delivery for SeverityCritical
	GotifyPriorityWarning  bool   `json:"gotifyPriorityWarning,omitempty"`  // enable delivery for SeverityWarning
	GotifyPriorityInfo     bool   `json:"gotifyPriorityInfo,omitempty"`     // enable delivery for SeverityInfo
	GotifyCriticalValue    *int   `json:"gotifyCriticalValue,omitempty"`    // Gotify priority int for critical (nil = 0)
	GotifyWarningValue     *int   `json:"gotifyWarningValue,omitempty"`     // Gotify priority int for warning (nil = 0)
	GotifyInfoValue        *int   `json:"gotifyInfoValue,omitempty"`        // Gotify priority int for info (nil = 0)

	// Pushover — third-party push notification service.
	PushoverUserKey  string `json:"pushoverUserKey,omitempty"`  // user/group key from Pushover dashboard
	PushoverAppToken string `json:"pushoverAppToken,omitempty"` // application API token from Pushover dashboard

	// ntfy — simple HTTP push notification service (ntfy.sh or self-hosted).
	NtfyURL              string `json:"ntfyUrl,omitempty"`              // base URL (e.g. https://ntfy.sh, https://ntfy.example.com)
	NtfyTopic            string `json:"ntfyTopic,omitempty"`            // topic name to publish to
	NtfyToken            string `json:"ntfyToken,omitempty"`            // optional bearer token (required only for protected topics)
	NtfyPriorityCritical bool   `json:"ntfyPriorityCritical,omitempty"` // enable delivery for SeverityCritical
	NtfyPriorityWarning  bool   `json:"ntfyPriorityWarning,omitempty"`  // enable delivery for SeverityWarning
	NtfyPriorityInfo     bool   `json:"ntfyPriorityInfo,omitempty"`     // enable delivery for SeverityInfo
	NtfyCriticalValue    *int   `json:"ntfyCriticalValue,omitempty"`    // ntfy priority 1-5 for critical (nil = 5)
	NtfyWarningValue     *int   `json:"ntfyWarningValue,omitempty"`     // ntfy priority 1-5 for warning (nil = 4)
	NtfyInfoValue        *int   `json:"ntfyInfoValue,omitempty"`        // ntfy priority 1-5 for info (nil = 3)

	// Apprise — meta-notifier API server that fans out to many backends.
	// AppriseURL points at an Apprise API server (https://github.com/caronc/apprise-api).
	// AppriseURLs is a list of Apprise notification URLs to fan out to per
	// notification (e.g. ["discord://...", "mailto://..."]).
	// AppriseToken is an optional bearer token for the Apprise API endpoint.
	AppriseURL   string   `json:"appriseUrl,omitempty"`
	AppriseToken string   `json:"appriseToken,omitempty"`
	AppriseURLs  []string `json:"appriseUrls,omitempty"`
}

// TestResult captures the outcome of one provider-specific test channel.
type TestResult struct {
	Label  string `json:"label"`
	Status string `json:"status"`          // "ok" or "error"
	Error  string `json:"error,omitempty"` // set when status == "error"
}

const (
	statusOK    = "ok"
	statusError = "error"
)

// Severity indicates the semantic importance of an outgoing notification.
// Providers may use this to set visual styling (color, priority level) or to
// gate delivery entirely (e.g. Gotify skips severities the user has disabled).
type Severity string

const (
	SeverityInfo     Severity = "info"     // routine success events (auto-sync applied)
	SeverityWarning  Severity = "warning"  // notable but non-critical events (cleanup, changelog)
	SeverityCritical Severity = "critical" // errors requiring user attention (sync failure)
)

// Route indicates which logical channel an agent should use.
// Currently only Discord distinguishes between routes (main webhook vs.
// updates webhook). Providers that do not support multiple channels simply
// ignore this value and deliver to their single endpoint.
type Route string

const (
	RouteDefault Route = "default" // primary channel (sync results, cleanup, errors)
	RouteUpdates Route = "updates" // secondary channel (repo updates, changelog)
)

// Payload is the provider-agnostic message contract for outbound notifications.
// A single Payload is created by the caller (autosync, cleanup, repo-update)
// and dispatched to every matching agent. Providers may use TypeMessages for
// per-platform formatting overrides (e.g. Gotify needs markdown bullets while
// Discord uses embed descriptions).
//
// Payload is an internal Go contract — NEVER JSON-serialised to disk or
// transmitted over the API. Fields therefore intentionally have no JSON
// tags. Adding `json:"…"` tags here would suggest persistence semantics
// that don't exist.
type Payload struct {
	Title        string            // short title, e.g. "Clonarr: Auto-Sync Applied"
	Message      string            // default provider message body (markdown)
	TypeMessages map[string]string // optional per-provider body override keyed by provider type (e.g. {"gotify": "..."})
	Color        int               // embed accent color (hex int) for providers that support it (Discord)
	Severity     Severity          // semantic importance — providers may map to priority levels or colors
	Route        Route             // logical delivery channel — multi-channel providers use this to pick an endpoint

	// Fields are optional structured key/value pairs rendered as a fields-
	// grid by providers that support it (Discord). Other providers may
	// flatten Fields into Message text or ignore them — Discord renders
	// inline fields as side-by-side columns so e.g. Primary/Secondary
	// totals stack visually like the bash tagarr.sh embed.
	//
	// Callers either set Fields (rich) OR Message (plain) — providers
	// that handle Fields prefer them over Message when both are present.
	Fields []PayloadField

	// Detail is an optional follow-up message body sent AFTER the main
	// embed (Discord) or appended to Message (other providers). Used to
	// surface long lists that don't fit in an embed — bash tagarr.sh
	// sends a separate "**Tagged Movies:**\n```\n...\n```" message after
	// the summary embed. Discord auto-chunks this at 1800 chars per
	// message (Discord's 2000-char content limit minus markdown overhead).
	// Pre-formatted with markdown (callers wrap in code blocks etc).
	Detail string

	// ThumbnailURL is rendered as the small top-right poster image in
	// the Discord embed (Discord's `thumbnail.url` slot). Used by the
	// webhook-fire notifications to surface the movie/series poster
	// from the Connect payload's `images[].remoteUrl`. Empty string
	// renders no thumbnail. Providers without thumbnail support ignore
	// this field.
	ThumbnailURL string

	// FooterSuffix appends to the default provider footer text with
	// " · " as separator. The default footer is
	// "Resolvarr {version} by ProphetSe7en"; setting FooterSuffix to
	// "rule: Tag 4K imports" yields
	// "Resolvarr v0.6.4 by ProphetSe7en · rule: Tag 4K imports".
	//
	// Append (rather than replace) so the caller never has to know
	// the version string — version + by-line stay automatic, the
	// suffix is purely the per-payload context (rule name, scheduler
	// job name, …). Empty string keeps the plain default footer.
	FooterSuffix string
}

// PayloadField is one cell in a Discord embed's fields-grid. Inline
// fields stack horizontally (up to ~3 per row at typical client width);
// non-inline fields take their own full row. Bash tagarr.sh uses inline
// for Primary/Secondary stat columns and full-row for Runtime.
type PayloadField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// messageFor returns the provider-specific message override when present.
func (p Payload) messageFor(agentType string) string {
	if len(p.TypeMessages) == 0 {
		return p.Message
	}
	if msg, ok := p.TypeMessages[strings.ToLower(strings.TrimSpace(agentType))]; ok && msg != "" {
		return msg
	}
	return p.Message
}

// severityOrDefault returns SeverityInfo when payload severity is unset.
func (p Payload) severityOrDefault() Severity {
	if p.Severity == "" {
		return SeverityInfo
	}
	return p.Severity
}

// routeOrDefault returns RouteDefault when payload route is unset.
func (p Payload) routeOrDefault() Route {
	if p.Route == "" {
		return RouteDefault
	}
	return p.Route
}

// HTTPPoster is the HTTP capability required by notification providers.
// Both [Runtime.NotifyClient] and [Runtime.SafeClient] satisfy this interface
// (Go's *http.Client implements both Post and Do).
// Using an interface rather than *http.Client allows test doubles and SSRF-safe wrappers.
type HTTPPoster interface {
	Post(url, contentType string, body io.Reader) (*http.Response, error)
	Do(req *http.Request) (*http.Response, error)
}

// Runtime bundles process-scoped dependencies injected into providers at
// dispatch time. Providers must never construct their own HTTP clients;
// the caller decides which client is appropriate based on trust level.
type Runtime struct {
	Version      string     // application version string, used in provider message footers
	NotifyClient HTTPPoster // standard HTTP client for trusted first-party destinations (e.g. Gotify on LAN)
	SafeClient   HTTPPoster // SSRF-protected HTTP client for untrusted user-supplied URLs (e.g. Discord webhooks)
}
