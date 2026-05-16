package engine

import "testing"

// Tests for per-bucket Labels overrides — pins the contract that:
//   - emit applies user labels in place of engine defaults
//   - AllPossible / Emittable cleanup-bounds follow the configured
//     vocabulary (so renamed values become orphans we no longer touch)
//   - the bucket Prefix still applies on top of an override
//   - falsy override values (missing key, empty string) fall through
//     to the engine default

func TestBucketLabel_OverrideTakesPrecedence(t *testing.T) {
	b := BucketConfig{Labels: map[string]string{"dvprofile8": "profile8"}}
	if got := b.label("dvprofile8"); got != "profile8" {
		t.Errorf("label(dvprofile8) = %q, want profile8", got)
	}
}

func TestBucketLabel_MissingKeyReturnsDefault(t *testing.T) {
	b := BucketConfig{Labels: map[string]string{"truehd": "premium"}}
	if got := b.label("dts-x"); got != "dts-x" {
		t.Errorf("label(dts-x) = %q, want dts-x (no override)", got)
	}
}

func TestBucketLabel_EmptyValueReturnsDefault(t *testing.T) {
	b := BucketConfig{Labels: map[string]string{"truehd": ""}}
	if got := b.label("truehd"); got != "truehd" {
		t.Errorf("label(truehd) = %q, want truehd (empty override skipped)", got)
	}
}

func TestBucketLabel_NilLabelsReturnsDefault(t *testing.T) {
	b := BucketConfig{}
	if got := b.label("h265"); got != "h265" {
		t.Errorf("label(h265) = %q, want h265 (nil Labels)", got)
	}
}

func TestAudioTagsForFile_AppliesOverride(t *testing.T) {
	cfg := AudioTagsConfig{Audio: BucketConfig{
		Enabled: true,
		Prefix:  "audio-",
		Labels:  map[string]string{"truehd": "premium"},
	}}
	got := AudioTagsForFile(MediaInfo{AudioCodec: "TrueHD", AudioChannels: 5.1}, cfg)
	wantCodec := "audio-premium"
	wantChannels := "audio-5-1" // not overridden
	foundCodec, foundChannels := false, false
	for _, tag := range got {
		switch tag {
		case wantCodec:
			foundCodec = true
		case wantChannels:
			foundChannels = true
		}
	}
	if !foundCodec {
		t.Errorf("expected %q in %v", wantCodec, got)
	}
	if !foundChannels {
		t.Errorf("expected default %q in %v (unrelated value should not be affected)", wantChannels, got)
	}
}

func TestVideoTagsForFile_AppliesOverride(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, Prefix: "video-", Labels: map[string]string{"2160p": "uhd"}},
		HDR:        BucketConfig{Enabled: true, Prefix: "video-"}, // no override
	}
	got := VideoTagsForFile(MediaInfo{Height: 2160, VideoDynamicRangeType: "DV HDR10"}, 0, cfg)
	wantRes := "video-uhd"
	wantHDR := "video-hdr10"
	wantDV := "video-dv"
	gotMap := map[string]bool{}
	for _, t := range got {
		gotMap[t] = true
	}
	if !gotMap[wantRes] {
		t.Errorf("expected %q in %v", wantRes, got)
	}
	if !gotMap[wantHDR] {
		t.Errorf("expected %q in %v (HDR not overridden)", wantHDR, got)
	}
	if !gotMap[wantDV] {
		t.Errorf("expected %q in %v (dv not overridden)", wantDV, got)
	}
}

func TestAllPossibleAudioTags_FollowsConfiguredVocab(t *testing.T) {
	cfg := AudioTagsConfig{Audio: BucketConfig{
		Prefix: "audio-",
		Labels: map[string]string{"truehd": "premium", "5-1": "surround"},
	}}
	got := AllPossibleAudioTags(cfg)
	if _, ok := got["audio-premium"]; !ok {
		t.Errorf("missing override audio-premium in AllPossible: %v", got)
	}
	if _, ok := got["audio-surround"]; !ok {
		t.Errorf("missing override audio-surround in AllPossible: %v", got)
	}
	// The engine-default labels for OVERRIDDEN values must NOT appear —
	// that's the cleanup contract: after rename, the OLD label is no
	// longer "ours".
	if _, ok := got["audio-truehd"]; ok {
		t.Errorf("engine-default audio-truehd leaked into AllPossible after rename: %v", got)
	}
	if _, ok := got["audio-5-1"]; ok {
		t.Errorf("engine-default audio-5-1 leaked into AllPossible after rename: %v", got)
	}
	// Non-overridden defaults still appear.
	if _, ok := got["audio-atmos"]; !ok {
		t.Errorf("missing non-overridden audio-atmos: %v", got)
	}
}

func TestEmittableVideoTags_FollowsConfiguredVocab(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{
			Enabled: true,
			Prefix:  "video-",
			Labels:  map[string]string{"2160p": "uhd", "1080p": "fhd"},
		},
	}
	got := EmittableVideoTags(cfg)
	if _, ok := got["video-uhd"]; !ok {
		t.Errorf("missing override video-uhd in Emittable: %v", got)
	}
	if _, ok := got["video-fhd"]; !ok {
		t.Errorf("missing override video-fhd in Emittable: %v", got)
	}
	if _, ok := got["video-2160p"]; ok {
		t.Errorf("engine-default video-2160p leaked into Emittable after rename: %v", got)
	}
	if _, ok := got["video-1080p"]; ok {
		t.Errorf("engine-default video-1080p leaked into Emittable after rename: %v", got)
	}
}

func TestEmitDvDetailTags_AppliesOverride(t *testing.T) {
	cfg := DvDetailConfig{
		Enabled: true,
		Labels:  map[string]string{"dvprofile8": "profile8"},
	}
	detail := DvDetail{Profile: 8}
	got := EmitDvDetailTags(detail, cfg)
	found := false
	for _, tag := range got {
		if tag == "profile8" {
			found = true
		}
		if tag == "dvprofile8" {
			t.Errorf("engine-default dvprofile8 leaked after rename: %v", got)
		}
	}
	if !found {
		t.Errorf("expected override profile8 in %v", got)
	}
}

func TestAllPossibleDvDetailTags_FollowsConfiguredVocab(t *testing.T) {
	cfg := DvDetailConfig{
		Prefix: "dv-",
		Labels: map[string]string{"dvprofile8": "profile8"},
	}
	got := AllPossibleDvDetailTags(cfg)
	if _, ok := got["dv-profile8"]; !ok {
		t.Errorf("missing override dv-profile8 in AllPossible: %v", got)
	}
	if _, ok := got["dv-dvprofile8"]; ok {
		t.Errorf("engine-default dv-dvprofile8 leaked after rename: %v", got)
	}
	// Untouched vocabulary still appears with its default label.
	if _, ok := got["dv-mel"]; !ok {
		t.Errorf("missing default dv-mel: %v", got)
	}
}

func TestEmitNoDvTag_AppliesOverride(t *testing.T) {
	cfg := DvDetailConfig{
		Enabled: true,
		Labels:  map[string]string{"no-dv": "not-dolby-vision"},
	}
	got := EmitNoDvTag(cfg)
	if len(got) != 1 || got[0] != "not-dolby-vision" {
		t.Errorf("EmitNoDvTag = %v, want [not-dolby-vision]", got)
	}
}
