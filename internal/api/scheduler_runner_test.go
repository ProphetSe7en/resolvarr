package api

import (
	"testing"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// applyRuleOverlay is the load-bearing helper that schedules and the
// adhoc /api/scan/run quickfix path both use to overlay rule-level
// snapshots onto the global cfg. Wrong behaviour here = wrong tags
// applied, so the contract gets pinned with table tests.

func TestApplyRuleOverlay_NilPassThrough(t *testing.T) {
	cfg := buildOverlayTestCfg()
	out := applyRuleOverlay(cfg, nil, nil, nil, nil, nil, "radarr", nil)

	if out.Filters.Radarr.Quality != true {
		t.Errorf("Radarr Filters mutated when overlay nil: got Quality=%v, want true", out.Filters.Radarr.Quality)
	}
	if !out.VideoTags.Resolution.Enabled {
		t.Error("VideoTags mutated when overlay nil: expected Resolution.Enabled to stay true")
	}
	if !out.AudioTags.Audio.Enabled {
		t.Error("AudioTags mutated when overlay nil: expected Audio.Enabled to stay true")
	}
	if len(out.ReleaseGroups) != 3 {
		t.Errorf("ReleaseGroups mutated when overlay nil: got %d, want 3", len(out.ReleaseGroups))
	}
}

func TestApplyRuleOverlay_FiltersRoutedByAppType(t *testing.T) {
	cfg := buildOverlayTestCfg()
	overlay := &engine.FilterConfig{Quality: false, Audio: true}

	radarr := applyRuleOverlay(cfg, overlay, nil, nil, nil, nil, "radarr", nil)
	if radarr.Filters.Radarr.Quality != false {
		t.Error("Radarr filters not overlaid for radarr appType")
	}
	if radarr.Filters.Sonarr.Quality != true {
		t.Error("Sonarr filters mutated when appType=radarr — should stay untouched")
	}

	sonarr := applyRuleOverlay(cfg, overlay, nil, nil, nil, nil, "sonarr", nil)
	if sonarr.Filters.Sonarr.Quality != false {
		t.Error("Sonarr filters not overlaid for sonarr appType")
	}
	if sonarr.Filters.Radarr.Quality != true {
		t.Error("Radarr filters mutated when appType=sonarr — should stay untouched")
	}
}

func TestApplyRuleOverlay_AudioTagsWholesaleReplace(t *testing.T) {
	cfg := buildOverlayTestCfg()
	overlay := &core.AudioTagsConfig{
		Audio: core.TagBucket{Enabled: false, Prefix: "audio-"},
	}
	out := applyRuleOverlay(cfg, nil, overlay, nil, nil, nil, "radarr", nil)

	if out.AudioTags.Audio.Enabled {
		t.Error("Audio.Enabled not replaced — expected false from overlay")
	}
	if out.AudioTags.Audio.Prefix != "audio-" {
		t.Errorf("Audio.Prefix not replaced: got %q, want audio-", out.AudioTags.Audio.Prefix)
	}
}

func TestApplyRuleOverlay_VideoTagsWholesaleReplace(t *testing.T) {
	cfg := buildOverlayTestCfg()
	overlay := &core.VideoTagsConfig{
		Resolution: core.TagBucket{Enabled: false},
		Codec:      core.TagBucket{Enabled: true, Prefix: "v-"},
	}
	out := applyRuleOverlay(cfg, nil, nil, overlay, nil, nil, "radarr", nil)

	if out.VideoTags.Resolution.Enabled {
		t.Error("Resolution.Enabled not replaced — expected false from overlay")
	}
	if out.VideoTags.Codec.Prefix != "v-" {
		t.Errorf("Codec.Prefix not replaced: got %q, want v-", out.VideoTags.Codec.Prefix)
	}
}

func TestApplyRuleOverlay_DvDetailWholesaleReplace(t *testing.T) {
	cfg := buildOverlayTestCfg()
	overlay := &core.DvDetailConfig{Enabled: true, Prefix: "dv-"}
	out := applyRuleOverlay(cfg, nil, nil, nil, overlay, nil, "radarr", nil)

	if !out.DvDetail.Enabled {
		t.Error("DvDetail.Enabled not replaced — expected true from overlay")
	}
	if out.DvDetail.Prefix != "dv-" {
		t.Errorf("DvDetail.Prefix not replaced: got %q, want dv-", out.DvDetail.Prefix)
	}
}

func TestApplyRuleOverlay_RGSubsetByID(t *testing.T) {
	cfg := buildOverlayTestCfg()
	out := applyRuleOverlay(cfg, nil, nil, nil, nil, []string{"g1", "g3"}, "radarr", nil)

	if len(out.ReleaseGroups) != 2 {
		t.Fatalf("expected 2 groups in subset, got %d", len(out.ReleaseGroups))
	}
	got := map[string]bool{}
	for _, g := range out.ReleaseGroups {
		got[g.ID] = true
	}
	if !got["g1"] || !got["g3"] {
		t.Errorf("subset wrong: got %v, want g1+g3", got)
	}
	if got["g2"] {
		t.Error("g2 leaked into subset")
	}
}

func TestApplyRuleOverlay_EmptyRGSliceTreatedAsNil(t *testing.T) {
	cfg := buildOverlayTestCfg()
	out := applyRuleOverlay(cfg, nil, nil, nil, nil, []string{}, "radarr", nil)

	if len(out.ReleaseGroups) != 3 {
		t.Errorf("empty []string{} should be treated as nil (preserve all RGs); got %d, want 3", len(out.ReleaseGroups))
	}
}

func TestApplyRuleOverlay_RGUnknownIDsDroppedSilently(t *testing.T) {
	cfg := buildOverlayTestCfg()
	out := applyRuleOverlay(cfg, nil, nil, nil, nil, []string{"g1", "ghost-id"}, "radarr", nil)

	if len(out.ReleaseGroups) != 1 {
		t.Errorf("expected 1 group (only g1 exists), got %d", len(out.ReleaseGroups))
	}
	if len(out.ReleaseGroups) == 1 && out.ReleaseGroups[0].ID != "g1" {
		t.Errorf("subset survivor wrong: got %q, want g1", out.ReleaseGroups[0].ID)
	}
}

func TestApplyRuleOverlay_DoesNotMutateCallerCfg(t *testing.T) {
	cfg := buildOverlayTestCfg()
	overlay := &engine.FilterConfig{Quality: false}
	_ = applyRuleOverlay(cfg, overlay, nil, nil, nil, []string{"g1"}, "radarr", nil)

	if cfg.Filters.Radarr.Quality != true {
		t.Error("caller's cfg.Filters.Radarr was mutated — overlay must work on a copy")
	}
	if len(cfg.ReleaseGroups) != 3 {
		t.Errorf("caller's cfg.ReleaseGroups was mutated: got %d, want 3", len(cfg.ReleaseGroups))
	}
}

// buildOverlayTestCfg returns a fixture config with non-default
// Filters, populated AudioTags + VideoTags, and 3 release groups
// split across types so the appType routing can be observed.
func buildOverlayTestCfg() core.Config {
	return core.Config{
		Filters: core.FilterSet{
			Radarr: engine.FilterConfig{Quality: true, Audio: true},
			Sonarr: engine.FilterConfig{Quality: true, Audio: false},
		},
		AudioTags: core.AudioTagsConfig{
			Audio: core.TagBucket{Enabled: true, Prefix: ""},
		},
		VideoTags: core.VideoTagsConfig{
			Resolution: core.TagBucket{Enabled: true, Prefix: ""},
			Codec:      core.TagBucket{Enabled: true, Prefix: ""},
		},
		ReleaseGroups: []core.ReleaseGroup{
			{ID: "g1", Tag: "flux", Type: "radarr", Enabled: true, Mode: "filtered"},
			{ID: "g2", Tag: "ntb", Type: "radarr", Enabled: true, Mode: "simple"},
			{ID: "g3", Tag: "tepes", Type: "sonarr", Enabled: true, Mode: "filtered"},
		},
	}
}
