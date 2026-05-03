package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// groupRequest is the create/update payload for a release group. The
// ID field is read-only (server-assigned on create, taken from the URL
// on update). Type routes the group to Radarr or Sonarr — a Radarr
// scanner only consults type="radarr" groups, and vice-versa.
type groupRequest struct {
	Search  string `json:"search"`
	Tag     string `json:"tag"`
	Display string `json:"display"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	// Enabled is a pointer so we can distinguish "caller didn't send the
	// field" (nil → default to true for create, preserve for update) from
	// "caller explicitly set enabled:false" (false pointer → disable).
	// The Enabled toggle in the Groups list posts a focused payload that
	// only carries this field flipped; the Edit modal posts the full set.
	Enabled *bool `json:"enabled,omitempty"`
}

// reTagName enforces the "lowercase, no spaces" convention from the bash
// sample config so tags stay predictable in Arr profiles.
var reTagName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (req *groupRequest) normalize() {
	req.Search = strings.TrimSpace(req.Search)
	req.Tag = strings.TrimSpace(strings.ToLower(req.Tag))
	req.Display = strings.TrimSpace(req.Display)
	req.Mode = strings.TrimSpace(strings.ToLower(req.Mode))
	req.Type = strings.TrimSpace(strings.ToLower(req.Type))
	if req.Type == "" {
		// Legacy-compatibility default — an older UI that submits
		// without a type field lands on "radarr", matching what Load()
		// does for pre-split resolvarr.json entries.
		req.Type = "radarr"
	}
}

func (req *groupRequest) validate() error {
	if req.Search == "" {
		return errText("search string is required")
	}
	if req.Tag == "" {
		return errText("tag name is required")
	}
	if !reTagName.MatchString(req.Tag) {
		return errText("tag name must be lowercase letters, digits, underscores, or dashes")
	}
	if req.Display == "" {
		return errText("display name is required")
	}
	switch req.Mode {
	case "simple", "filtered":
		// OK
	default:
		return errText(`mode must be "simple" or "filtered"`)
	}
	switch req.Type {
	case "radarr", "sonarr":
		// OK
	default:
		return errText(`type must be "radarr" or "sonarr"`)
	}
	return nil
}

// handleListGroups returns the current release-group list sorted by
// display name (case-insensitive) — the order the UI renders by default.
// Always returns a JSON array (never null) so the Alpine template doesn't
// need to guard against .length on a null value.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	out := make([]core.ReleaseGroup, len(cfg.ReleaseGroups))
	copy(out, cfg.ReleaseGroups)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Display) < strings.ToLower(out[j].Display)
	})
	writeJSON(w, out)
}

// handleAddGroup appends a new release group. Tag names must be unique
// within the list — duplicate tag names would cause ambiguous matches at
// scan time.
func (s *Server) handleAddGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	req.normalize()
	if err := req.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	for _, g := range s.App.Config.Get().ReleaseGroups {
		if strings.EqualFold(g.Tag, req.Tag) {
			writeError(w, 409, "tag name already exists")
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	newGroup := core.ReleaseGroup{
		ID:      genID(),
		Search:  req.Search,
		Tag:     req.Tag,
		Display: req.Display,
		Mode:    req.Mode,
		Type:    req.Type,
		Enabled: enabled,
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		c.ReleaseGroups = append(c.ReleaseGroups, newGroup)
	}); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, newGroup)
}

// handleUpdateGroup replaces an existing group's fields. The ID is taken
// from the URL; the tag name must remain unique among OTHER groups.
func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req groupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	req.normalize()
	if err := req.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	current := s.App.Config.Get().ReleaseGroups
	idx := -1
	for i, g := range current {
		if g.ID == id {
			idx = i
			continue
		}
		if strings.EqualFold(g.Tag, req.Tag) {
			writeError(w, 409, "tag name already used by another group")
			return
		}
	}
	if idx < 0 {
		writeError(w, 404, "group not found")
		return
	}

	// Preserve existing Enabled state when the caller doesn't send the field.
	// The Edit modal always sends all fields; the row-inline enable toggle
	// posts just `enabled` to flip it without touching anything else. Both
	// paths hit this handler, so fallback-to-current is the safe behavior.
	enabled := current[idx].Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	updated := core.ReleaseGroup{
		ID:      id,
		Search:  req.Search,
		Tag:     req.Tag,
		Display: req.Display,
		Mode:    req.Mode,
		Type:    req.Type,
		Enabled: enabled,
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.ReleaseGroups {
			if c.ReleaseGroups[i].ID == id {
				c.ReleaseGroups[i] = updated
				return
			}
		}
	}); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, updated)
}

// handleDeleteGroup removes a group. The tag itself is NOT deleted from
// Arr — users clean up tags via the Tags screen if they want.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	found := false
	if err := s.App.Config.Update(func(c *core.Config) {
		filtered := c.ReleaseGroups[:0]
		for _, g := range c.ReleaseGroups {
			if g.ID == id {
				found = true
				continue
			}
			filtered = append(filtered, g)
		}
		c.ReleaseGroups = filtered
	}); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !found {
		writeError(w, 404, "group not found")
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// handleGetFilters returns the two per-Arr-type FilterConfig blocks.
// Shape: {"radarr": {...8 toggles}, "sonarr": {...8 toggles}}. The
// two sides are edited independently — tag decisions for a Radarr
// instance consult Filters.Radarr, Sonarr instance consults
// Filters.Sonarr.
func (s *Server) handleGetFilters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.App.Config.Get().Filters)
}

// handleUpdateFilters replaces the entire FilterSet. The UI always
// submits both halves, even when only one changed, so one endpoint
// handles both. Legacy flat bodies (pre-split) are rejected — the UI
// is expected to migrate to the new shape alongside this release.
func (s *Server) handleUpdateFilters(w http.ResponseWriter, r *http.Request) {
	var req core.FilterSet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) { c.Filters = req }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, req)
}

// validSonarrAggregations is the closed set of strategies the engine
// accepts. parseAggregation in core/config.go silently maps unknown
// values to AggAllOccurring; we reject them at the API boundary so
// clients can't write garbage into the persisted config.
var validSonarrAggregations = map[string]bool{
	"all-occurring": true,
	"strict":        true,
	"highest":       true,
	"":              true, // empty = use bucket default (filled by Load)
}

// validateBucket enforces prefix + aggregation + allowed-values rules
// for one TagBucket. knownValues is the closed vocab map for THIS
// bucket — caller passes the right set per category so a "h265"
// value can't slip into the audio bucket and vice versa.
func validateBucket(name string, b core.TagBucket, knownValues map[string]bool) error {
	if !core.ExtraTagPrefixValid.MatchString(b.Prefix) {
		return fmt.Errorf("%s prefix has invalid characters — Radarr only allows a-z, 0-9, and `-`", name)
	}
	if !validSonarrAggregations[b.SonarrAggregation] {
		return fmt.Errorf("%s sonarrAggregation must be one of: all-occurring, strict, highest", name)
	}
	for _, v := range b.AllowedValues {
		if !knownValues[v] {
			return fmt.Errorf("%s allowedValues contains unknown value: %s", name, v)
		}
	}
	return nil
}

// audioBucketKnown / videoBucketKnowns are package-level vocab maps
// built once from engine.AudioVocabulary / VideoVocabulary so each
// PUT validation is an O(1) lookup. Single source of truth — adding
// a vocab value to the engine ships it to the API on the next build.
var (
	audioBucketKnown      = buildAudioBucketKnown()
	videoResolutionKnown  = buildVideoResolutionKnown()
	videoCodecKnown       = buildVideoCodecKnown()
	videoHDRKnown         = buildVideoHDRKnown()
)

func buildAudioBucketKnown() map[string]bool {
	codecs, channels, flags := engine.AudioVocabulary()
	out := make(map[string]bool)
	for _, set := range [][]string{codecs, channels, flags} {
		for _, v := range set {
			out[v] = true
		}
	}
	return out
}
func buildVideoResolutionKnown() map[string]bool {
	res, _, _ := engine.VideoVocabulary()
	out := make(map[string]bool)
	for _, v := range res {
		out[v] = true
	}
	return out
}
func buildVideoCodecKnown() map[string]bool {
	_, codec, _ := engine.VideoVocabulary()
	out := make(map[string]bool)
	for _, v := range codec {
		out[v] = true
	}
	return out
}
func buildVideoHDRKnown() map[string]bool {
	_, _, hdr := engine.VideoVocabulary()
	out := make(map[string]bool)
	for _, v := range hdr {
		out[v] = true
	}
	return out
}

// audioTagsConfigResponse augments AudioTagsConfig with the closed
// vocab list so the UI can render the per-value checkbox matrix
// without hardcoding values. Same shape pattern as dvDetailConfigResponse.
type audioTagsConfigResponse struct {
	Config        core.AudioTagsConfig `json:"config"`
	AudioCodecs   []string             `json:"audioCodecs"`
	AudioChannels []string             `json:"audioChannels"`
	AudioFlags    []string             `json:"audioFlags"`
}

// videoTagsConfigResponse — same idea for the three video buckets.
type videoTagsConfigResponse struct {
	Config     core.VideoTagsConfig `json:"config"`
	Resolution []string             `json:"resolution"`
	Codec      []string             `json:"codec"`
	HDR        []string             `json:"hdr"`
}

func (s *Server) handleGetAudioTags(w http.ResponseWriter, r *http.Request) {
	codecs, channels, flags := engine.AudioVocabulary()
	writeJSON(w, audioTagsConfigResponse{
		Config:        s.App.Config.Get().AudioTags,
		AudioCodecs:   codecs,
		AudioChannels: channels,
		AudioFlags:    flags,
	})
}

func (s *Server) handleGetVideoTags(w http.ResponseWriter, r *http.Request) {
	res, codec, hdr := engine.VideoVocabulary()
	writeJSON(w, videoTagsConfigResponse{
		Config:     s.App.Config.Get().VideoTags,
		Resolution: res,
		Codec:      codec,
		HDR:        hdr,
	})
}

func (s *Server) handleUpdateAudioTags(w http.ResponseWriter, r *http.Request) {
	var req core.AudioTagsConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if err := validateAudioTagsConfig(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) { c.AudioTags = req }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, req)
}

func (s *Server) handleUpdateVideoTags(w http.ResponseWriter, r *http.Request) {
	var req core.VideoTagsConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if err := validateVideoTagsConfig(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) { c.VideoTags = req }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, req)
}

// validateAudioTagsConfig enforces prefix + aggregation + allowed-
// values rules on an AudioTagsConfig payload. Used by the global
// PUT handler + the per-request overlay path on /api/scan/run.
func validateAudioTagsConfig(cfg core.AudioTagsConfig) error {
	return validateBucket("audio", cfg.Audio, audioBucketKnown)
}

// validateVideoTagsConfig enforces the same rules across the three
// video buckets.
func validateVideoTagsConfig(cfg core.VideoTagsConfig) error {
	if err := validateBucket("resolution", cfg.Resolution, videoResolutionKnown); err != nil {
		return err
	}
	if err := validateBucket("codec", cfg.Codec, videoCodecKnown); err != nil {
		return err
	}
	if err := validateBucket("hdr", cfg.HDR, videoHDRKnown); err != nil {
		return err
	}
	return nil
}
