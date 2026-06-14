package engine

import (
	"reflect"
	"sort"
	"testing"
)

func defaultAudioCfg() AudioTagsConfig {
	return AudioTagsConfig{
		Audio: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
	}
}

func TestAudioTagsForFile_TruehdAtmos5_1(t *testing.T) {
	mi := MediaInfo{
		AudioCodec:              "TrueHD",
		AudioChannels:           5.1,
		AudioAdditionalFeatures: "Atmos",
	}
	got := AudioTagsForFile(mi, defaultAudioCfg())
	want := []string{"truehd", "5-1", "atmos"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAudioTagsForFile_AAC2_0(t *testing.T) {
	mi := MediaInfo{AudioCodec: "AAC", AudioChannels: 2.0}
	got := AudioTagsForFile(mi, defaultAudioCfg())
	want := []string{"aac", "2-0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAudioTagsForFile_DisabledReturnsNil(t *testing.T) {
	mi := MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1}
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: false}}
	got := AudioTagsForFile(mi, cfg)
	if got != nil {
		t.Errorf("got %v, want nil (bucket disabled)", got)
	}
}

func TestAudioTagsForFile_NoDataReturnsNil(t *testing.T) {
	got := AudioTagsForFile(MediaInfo{}, defaultAudioCfg())
	if got != nil {
		t.Errorf("got %v, want nil (no audio fields)", got)
	}
}

func TestAudioTagsForFile_CustomPrefix(t *testing.T) {
	mi := MediaInfo{AudioCodec: "DTS-X", AudioChannels: 7.1, AudioAdditionalFeatures: "Atmos"}
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, Prefix: "audio-"}}
	got := AudioTagsForFile(mi, cfg)
	want := []string{"audio-dts-x", "audio-7-1", "audio-atmos"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAudioTagsForFile_AllowedValuesFiltersAtmos(t *testing.T) {
	// User wants codec + channels but not the atmos flag.
	mi := MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1, AudioAdditionalFeatures: "Atmos"}
	cfg := AudioTagsConfig{
		Audio: BucketConfig{
			Enabled:       true,
			AllowedValues: []string{"truehd", "7-1"}, // excludes "atmos"
		},
	}
	got := AudioTagsForFile(mi, cfg)
	want := []string{"truehd", "7-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAudioTagsForFile_AllowedValuesEmptyMeansAll(t *testing.T) {
	// Pre-filter back-compat: empty slice = all allowed.
	mi := MediaInfo{AudioCodec: "AAC", AudioChannels: 2.0}
	cfg := AudioTagsConfig{
		Audio: BucketConfig{Enabled: true, AllowedValues: []string{}},
	}
	got := AudioTagsForFile(mi, cfg)
	want := []string{"aac", "2-0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAllPossibleAudioTags_EnabledIgnoresAllowedValues_DisabledEmpty(t *testing.T) {
	// Orphan-removal bound: an ENABLED bucket contributes its whole vocab
	// (ignoring AllowedValues, so unchecked values stay removable); a
	// DISABLED bucket contributes nothing (hands-off — orphan removal must
	// not strip tags from a dimension the user switched off).
	disabled := AllPossibleAudioTags(AudioTagsConfig{
		Audio: BucketConfig{Enabled: false, AllowedValues: []string{"truehd"}},
	})
	if len(disabled) != 0 {
		t.Errorf("disabled audio bucket: got %d tags, want 0", len(disabled))
	}
	enabled := AllPossibleAudioTags(AudioTagsConfig{
		Audio: BucketConfig{Enabled: true, AllowedValues: []string{"truehd"}},
	})
	codecs, channels, flags := AudioVocabulary()
	want := len(codecs) + len(channels) + len(flags)
	if len(enabled) != want {
		t.Errorf("enabled audio bucket: got %d tags, want %d (full vocab)", len(enabled), want)
	}
	for _, v := range codecs {
		if enabled[v] != "audio" {
			t.Errorf("missing %q in enabled safety-bound", v)
		}
	}
}

func TestAllPossibleAudioTags_PrefixApplied(t *testing.T) {
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, Prefix: "audio-"}}
	got := AllPossibleAudioTags(cfg)
	if got["audio-truehd"] != "audio" {
		t.Errorf("missing prefixed key audio-truehd: %v", got)
	}
	if _, exists := got["truehd"]; exists {
		t.Errorf("bare key 'truehd' should not exist with prefix")
	}
}

func TestEmittableAudioTags_DisabledIsEmpty(t *testing.T) {
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: false}}
	got := EmittableAudioTags(cfg)
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0 when disabled", len(got))
	}
}

func TestEmittableAudioTags_AllowedValuesNarrows(t *testing.T) {
	cfg := AudioTagsConfig{
		Audio: BucketConfig{Enabled: true, AllowedValues: []string{"truehd", "atmos"}},
	}
	got := EmittableAudioTags(cfg)
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2", len(got))
	}
	if got["truehd"] != "audio" || got["atmos"] != "audio" {
		t.Errorf("missing expected entry: %v", got)
	}
	if _, exists := got["7-1"]; exists {
		t.Error("filtered-out value should not appear")
	}
}

func TestAudioVocabulary_ReturnsCopy(t *testing.T) {
	codecs, _, _ := AudioVocabulary()
	codecs[0] = "MUTATED"
	codecs2, _, _ := AudioVocabulary()
	if codecs2[0] == "MUTATED" {
		t.Error("AudioVocabulary returned a shared backing array")
	}
}

func TestAudioVocabulary_AllValuesRadarrCompatible(t *testing.T) {
	codecs, channels, flags := AudioVocabulary()
	all := append(append([]string{}, codecs...), channels...)
	all = append(all, flags...)
	sort.Strings(all)
	for _, v := range all {
		for _, c := range v {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				t.Errorf("vocab %q contains char %q — fails Radarr ^[a-z0-9-]+$", v, c)
			}
		}
	}
}
