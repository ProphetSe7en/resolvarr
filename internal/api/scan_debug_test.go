package api

import (
	"reflect"
	"testing"

	"resolvarr/internal/core"
)

// scan_debug_test.go locks in the dev-only scanResponse.Debug builders.
// The Debug block is what makes "which config actually ran" visible on
// dev builds (config-source overlay vs global + the resolved per-bucket
// SelectMode / AllowedValues). It rides on the scan-*.json dump too, so
// historical runs stay self-describing. These tests guard the shape so a
// future bucket/field change can't silently blank the strip. See
// docs/resolvarr/ui-section-map.md.

func TestAudioScanDebug_SonarrCarriesAggregation(t *testing.T) {
	cfg := core.Config{}
	cfg.AudioTags.Audio = core.TagBucket{
		Enabled:           true,
		SelectMode:        "select",
		AllowedValues:     []string{"2-0", "atmos"},
		SonarrAggregation: "all-occurring",
	}
	req := scanRunRequest{debugConfigSource: "overlay"}

	got := audioScanDebug(cfg, req, "sonarr")
	want := &scanDebug{
		ConfigSource: "overlay",
		Buckets: []scanDebugBucket{{
			Name:          "audio",
			Enabled:       true,
			SelectMode:    "select",
			AllowedValues: []string{"2-0", "atmos"},
			Aggregation:   "all-occurring",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audioScanDebug(sonarr) = %+v, want %+v", got, want)
	}
}

func TestAudioScanDebug_RadarrOmitsAggregation(t *testing.T) {
	cfg := core.Config{}
	cfg.AudioTags.Audio = core.TagBucket{
		Enabled:           true,
		SelectMode:        "",
		SonarrAggregation: "all-occurring", // present in config but Radarr must not surface it
	}
	got := audioScanDebug(cfg, scanRunRequest{debugConfigSource: "global"}, "radarr")
	if got.ConfigSource != "global" {
		t.Errorf("ConfigSource = %q, want global", got.ConfigSource)
	}
	if len(got.Buckets) != 1 || got.Buckets[0].Aggregation != "" {
		t.Errorf("Radarr bucket should omit Aggregation, got %+v", got.Buckets)
	}
}

func TestVideoScanDebug_ThreeBucketsInOrder(t *testing.T) {
	cfg := core.Config{}
	cfg.VideoTags.Resolution = core.TagBucket{Enabled: true, SelectMode: "select", AllowedValues: []string{"2160p"}}
	cfg.VideoTags.Codec = core.TagBucket{Enabled: false}
	cfg.VideoTags.HDR = core.TagBucket{Enabled: true}

	got := videoScanDebug(cfg, scanRunRequest{debugConfigSource: "overlay"}, "radarr")
	if len(got.Buckets) != 3 {
		t.Fatalf("want 3 buckets, got %d", len(got.Buckets))
	}
	names := []string{got.Buckets[0].Name, got.Buckets[1].Name, got.Buckets[2].Name}
	if !reflect.DeepEqual(names, []string{"resolution", "codec", "hdr"}) {
		t.Errorf("bucket order = %v, want [resolution codec hdr]", names)
	}
}

func TestTagScanDebug_ConfigSourceOnly(t *testing.T) {
	got := tagScanDebug(scanRunRequest{debugConfigSource: "overlay"})
	if got.ConfigSource != "overlay" {
		t.Errorf("ConfigSource = %q, want overlay", got.ConfigSource)
	}
	if len(got.Buckets) != 0 {
		t.Errorf("tag debug should have no buckets, got %+v", got.Buckets)
	}
}

func TestDvScanDebug_FlatVocabNoAggregation(t *testing.T) {
	cfg := core.Config{}
	cfg.DvDetail = core.DvDetailConfig{
		Enabled:       true,
		SelectMode:    "select",
		AllowedValues: []string{"dvprofile8", "cm4"},
	}
	got := dvScanDebug(cfg, scanRunRequest{debugConfigSource: "overlay"})
	if len(got.Buckets) != 1 || got.Buckets[0].Name != "dvdetail" {
		t.Fatalf("want single dvdetail bucket, got %+v", got.Buckets)
	}
	if got.Buckets[0].Aggregation != "" {
		t.Errorf("DV bucket must not carry aggregation, got %q", got.Buckets[0].Aggregation)
	}
	if !reflect.DeepEqual(got.Buckets[0].AllowedValues, []string{"dvprofile8", "cm4"}) {
		t.Errorf("AllowedValues = %v", got.Buckets[0].AllowedValues)
	}
}
