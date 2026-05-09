package api

import (
	"context"
	"strings"
	"testing"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_tag_test.go — focused unit tests for the tag-mode handler. Like
// scan_dv_detail_test.go, these cover the early-out paths that don't
// need a working Arr client (per-movie integration testing requires
// httptest fronting a fake Radarr — out of scope for this slice).

// TestRunTagFilterOnly_ConflictWithGroupReturns409 — the conflict
// guard at the top of runTagFilterOnly fires before any Arr call. A
// filter-only tag whose name matches an existing per-group rule's
// Tag for the same instance type must produce HTTP 409 with a
// message naming the colliding group, so the user knows what to
// rename or remove.
func TestRunTagFilterOnly_ConflictWithGroupReturns409(t *testing.T) {
	cfg := core.Config{
		ReleaseGroups: []core.ReleaseGroup{
			{
				ID:      "g1",
				Type:    "radarr",
				Search:  "flux",
				Tag:     "lossless-web",
				Display: "FLUX",
				Mode:    "filtered",
				Enabled: true,
			},
		},
	}
	s := minimalServer(cfg)
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	req := scanRunRequest{
		InstanceID:    "r",
		Mode:          "preview",
		Action:        "tag",
		TagSource:     "filter-only",
		FilterOnlyTag: "lossless-web",
	}
	_, apiErr := s.runTagFilterOnly(context.Background(), cfg, inst, "radarr", engine.FilterConfig{}, req)
	if apiErr == nil {
		t.Fatal("expected apiError, got nil")
	}
	if apiErr.Status != 409 {
		t.Errorf("status = %d, want 409", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message, "FLUX") {
		t.Errorf("message = %q, want to mention colliding group %q", apiErr.Message, "FLUX")
	}
}

// TestRunTagFilterOnly_DisabledGroupStillBlocks — disabled groups
// still hold their tag-name reservation, since flipping Enabled back
// on would re-introduce the conflict. The conflict guard must fire
// regardless of group's Enabled state.
func TestRunTagFilterOnly_DisabledGroupStillBlocks(t *testing.T) {
	cfg := core.Config{
		ReleaseGroups: []core.ReleaseGroup{
			{
				ID:      "g1",
				Type:    "radarr",
				Search:  "flux",
				Tag:     "lossless-web",
				Display: "FLUX",
				Mode:    "filtered",
				Enabled: false, // disabled but still reserves the name
			},
		},
	}
	s := minimalServer(cfg)
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	req := scanRunRequest{
		InstanceID:    "r",
		Mode:          "preview",
		Action:        "tag",
		TagSource:     "filter-only",
		FilterOnlyTag: "lossless-web",
	}
	_, apiErr := s.runTagFilterOnly(context.Background(), cfg, inst, "radarr", engine.FilterConfig{}, req)
	if apiErr == nil || apiErr.Status != 409 {
		t.Fatalf("expected 409, got %+v", apiErr)
	}
}

// Note on cross-type isolation: a Sonarr group named "lossless-web"
// must NOT block a Radarr filter-only rule with the same tag (each
// Arr instance has its own tag inventory). The conflict guard
// enforces this via the `g.Type != appType` skip in the loop —
// asserted by code-review on runTagFilterOnly's filter loop, not
// here, because reaching past the guard requires a working Arr
// backend (httptest fake) that the api package's tests don't
// build today.

// TestReTagName_AcceptsLosslessWebDefault — the default filter-only
// tag must pass the same Radarr-strict regex used to validate
// per-group Tag names, otherwise users would get a confusing 502
// from Arr when they hit Apply on a freshly-defaulted rule.
func TestReTagName_AcceptsLosslessWebDefault(t *testing.T) {
	if !reTagName.MatchString("lossless-web") {
		t.Error("default filter-only tag 'lossless-web' rejected by reTagName")
	}
}

// TestReTagName_RejectsCommonInvalidTags — the validator surfaces
// in the dispatcher's filter-only branch (returns 400). Spot-check
// patterns the user might naively try. Underscore is allowed by the
// regex (mirrors per-group Tag validation in groups.go), so it's
// NOT in the reject set even though Radarr's stricter validator
// would reject it for some operations.
func TestReTagName_RejectsCommonInvalidTags(t *testing.T) {
	rejects := []string{
		"Lossless-Web",  // uppercase
		"lossless web",  // space
		"lossless.web",  // dot
		"-leading-dash", // leading non-alphanum
		"premium!",      // punctuation
	}
	for _, label := range rejects {
		if reTagName.MatchString(label) {
			t.Errorf("regex unexpectedly accepted %q (should reject)", label)
		}
	}
	// Spot-check accepted variants for parity with per-group rules.
	accepts := []string{
		"lossless-web",
		"lossless_web", // underscore is OK in the per-group regex
		"5-1",          // numeric leading + dash (channel-notation pattern)
		"flux",
		"premium",
	}
	for _, label := range accepts {
		if !reTagName.MatchString(label) {
			t.Errorf("regex unexpectedly rejected %q (should accept)", label)
		}
	}
}
