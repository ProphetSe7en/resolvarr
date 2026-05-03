package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// dvtools.go — M4b API surface for the Dolby Vision config CRUD plus
// a thin /api/tools/dv/status reader.
//
//   - GET  /api/dv-detail              → DvDetailConfig + closed vocab
//   - PUT  /api/dv-detail              → write DvDetailConfig (validated)
//   - GET  /api/tools/dv/status        → are ffmpeg + dovi_tool present
//                                         on $PATH (or /config/tools as legacy)
//
// Install + Uninstall handlers are gone — DV tools (ffmpeg +
// dovi_tool) ship baked into the image as of v0.3.5 via the
// Dockerfile dv-tools stage. No env var, no install step. Status
// here is kept as a defensive health check: it reports whether
// the binaries resolved at $PATH so the DV detail tab can surface
// a "Tools unreachable" notice if a future image build is broken
// (shouldn't happen in normal CI but cheap to defend against).

// dvDetailConfigResponse augments the persisted DvDetailConfig with
// the closed-vocab list so the UI can render the per-value checkbox
// matrix without hardcoding the 5 values. Keeps frontend + engine in
// lock-step — adding a new vocab value (engine vocab + parser) ships
// it to the UI on the next config GET, no separate frontend release.
type dvDetailConfigResponse struct {
	Config     core.DvDetailConfig `json:"config"`
	Vocabulary []string            `json:"vocabulary"`
}

// handleGetDvDetail returns the persisted DvDetail config plus the
// closed vocabulary. UI seeds the Library scan → DV detail sub-tab
// + the per-value AllowedValues checkbox matrix from this response.
func (s *Server) handleGetDvDetail(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	writeJSON(w, dvDetailConfigResponse{
		Config:     cfg.DvDetail,
		Vocabulary: engine.DvDetailVocabulary(),
	})
}

// handleUpdateDvDetail replaces the DvDetail config wholesale.
// Validates Prefix against Radarr's ^[a-z0-9-]*$ rule (empty allowed
// — bare-value tags) and AllowedValues against the closed vocab
// (rejects unknown values so a malformed POST can't write dead state
// into the persisted config — same defence-in-depth as the
// extra-tags handler does).
func (s *Server) handleUpdateDvDetail(w http.ResponseWriter, r *http.Request) {
	var req core.DvDetailConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if err := validateDvDetailConfig(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) { c.DvDetail = req }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, req)
}

// dvDetailKnownValues is the closed-vocab lookup, hoisted to package
// scope so request handlers don't rebuild it per-call. Same pattern
// as validBucketAllowedValues in groups.go — single allocation,
// every PUT shares it. The slice from engine.DvDetailVocabulary()
// is constant; building this map once at init is safe.
var dvDetailKnownValues = func() map[string]bool {
	out := make(map[string]bool)
	for _, v := range engine.DvDetailVocabulary() {
		out[v] = true
	}
	return out
}()

// validateDvDetailConfig enforces prefix-format + closed-vocab rules
// on a DvDetailConfig payload. STRICT — AllowedValues must already
// be canonical (lowercase, no whitespace) to pass. The same rule as
// handleUpdateExtraTags applies here: hand-editing the JSON to send
// "MEL" or " mel " is rejected with a clear error rather than silently
// normalised, because:
//
//   - the UI emits canonical lowercase from its checkbox flow already
//   - normalising in validation but persisting raw produced silent
//     bugs (case mismatch in cleanup vocab lookups → "checkbox does
//     nothing" surface)
//   - rejecting at the boundary keeps the persisted shape clean
//
// Drift sentinel against handleUpdateExtraTags's strict `known[v]`
// pattern — both surfaces validate AllowedValues identically.
func validateDvDetailConfig(cfg core.DvDetailConfig) error {
	if !core.ExtraTagPrefixValid.MatchString(cfg.Prefix) {
		return fmt.Errorf("prefix has invalid characters — Radarr only allows a-z, 0-9, and `-`")
	}
	for _, v := range cfg.AllowedValues {
		if !dvDetailKnownValues[v] {
			return fmt.Errorf("allowedValues contains unknown value: %q (must be one of: mel, fel, dvprofile8, cm2, cm4, no-dv)", v)
		}
	}
	return nil
}

// handleDvToolsStatus reports tools state — does dovi_tool / ffmpeg
// resolve at runtime (legacy /config/tools/ → $PATH). The UI uses
// this for a small "Tools ready" indicator on the DV detail tab,
// or "Tools unreachable" if the image build is somehow broken.
// Tools are baked into the image so the unhealthy branch should
// never fire in normal deployments.
func (s *Server) handleDvToolsStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, s.DvTools.Status(ctx))
}
