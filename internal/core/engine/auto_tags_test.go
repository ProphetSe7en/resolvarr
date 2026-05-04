package engine

import (
	"reflect"
	"sort"
	"testing"
)

// auto_tags_test.go — tests for shared helpers (mediaInfo bucket
// mappers + Sonarr aggregation). Per-config emit/AllPossible/
// Emittable tests live in audio_tags_test.go + video_tags_test.go.

func TestResolutionBucket(t *testing.T) {
	cases := []struct {
		name             string
		mediaInfoHeight  int
		qualityResolution int
		want             string
	}{
		{"4K mediaInfo", 2160, 0, "2160p"},
		{"4K with quality fallback", 2160, 2160, "2160p"},
		{"1440p mediaInfo wins over 1080p quality", 1440, 1080, "1440p"},
		{"1080p mediaInfo", 1080, 0, "1080p"},
		{"720p mediaInfo", 720, 0, "720p"},
		{"480p mediaInfo", 480, 0, "480p"},
		{"sd from low height", 240, 0, "sd"},
		{"missing mediaInfo, quality 1080", 0, 1080, "1080p"},
		{"missing mediaInfo, quality 0", 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolutionBucket(tc.mediaInfoHeight, tc.qualityResolution)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodecBucket(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"x265", "h265"}, {"hevc", "h265"}, {"H.265", "h265"}, {"h265", "h265"},
		{"x264", "h264"}, {"AVC", "h264"}, {"H.264", "h264"}, {"h264", "h264"},
		{"AV1", "av1"},
		{"XviD", "mpeg4"}, {"DivX", "mpeg4"}, {"MPEG-4", "mpeg4"},
		{"MPEG-2", "mpeg2"}, {"mpeg2", "mpeg2"},
		{"VC-1", "vc1"}, {"vc1", "vc1"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := codecBucket(tc.in)
			if got != tc.want {
				t.Errorf("codecBucket(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAudioCodecBucket(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"TrueHD", "truehd"}, {"TrueHD Atmos", "truehd"},
		{"DTS-X", "dts-x"}, {"DTS:X", "dts-x"},
		{"DTS-HD MA", "dts-hd-ma"}, {"DTS-HD-MA", "dts-hd-ma"},
		{"DTS-HD HRA", "dts-hd-hra"},
		{"DTS-ES", "dts-es"},
		{"DTS", "dts"},
		{"E-AC-3", "eac3"}, {"EAC3", "eac3"},
		{"AC3", "ac3"}, {"Dolby Digital", "ac3"},
		{"AAC", "aac"},
		{"FLAC", "flac"},
		{"PCM", "pcm"},
		{"Opus", "opus"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := audioCodecBucket(tc.in)
			if got != tc.want {
				t.Errorf("audioCodecBucket(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAudioChannelsBucket(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{8.0, "7-1"}, {7.1, "7-1"}, {7.0, "7-1"},
		{5.1, "5-1"}, {5.0, "5-1"},
		{4.0, "4-0"},
		{2.1, "2-0"}, {2.0, "2-0"},
		{1.0, "mono"},
		{0.0, ""},
	}
	for _, tc := range cases {
		got := audioChannelsBucket(tc.in)
		if got != tc.want {
			t.Errorf("audioChannelsBucket(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHasAtmos(t *testing.T) {
	cases := []struct {
		name         string
		features     string
		relativePath string
		sceneName    string
		want         bool
	}{
		// Authoritative source: audioAdditionalFeatures match wins.
		{"feature_match_capital", "Atmos", "", "", true},
		{"feature_match_lower", "atmos", "", "", true},
		{"feature_match_compound", "Dolby Atmos / DD+", "", "", true},

		// Filename fallback when features blank.
		{"filename_token_uhd_release",
			"", "Movie.2024.UHD.BluRay.2160p.HDR10.TrueHD.Atmos.7.1.x265-FLUX.mkv", "",
			true},
		{"scenename_token",
			"", "", "Movie.2024.UHD.BluRay.TrueHD.Atmos.7.1.x265-FLUX",
			true},
		{"filename_lowercase",
			"", "movie.2024.webdl.atmos.5.1-ntb.mkv", "",
			true},

		// Negative paths.
		{"empty_all", "", "", "", false},
		{"features_negative", "DTS:X", "", "", false},
		{"no_atmos_token_in_filename",
			"", "Movie.2024.WEB-DL.DDP.5.1.x264-NTb.mkv", "Movie.2024.WEB-DL.DDP.5.1-NTb",
			false},
		// Substring guard — "atmospheric" in a movie title shouldn't trigger.
		{"substring_false_positive_guard",
			"", "Atmospheric.Pressure.2024.WEB-DL.DDP.5.1-NTb.mkv", "",
			false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasAtmos(tc.features, tc.relativePath, tc.sceneName)
			if got != tc.want {
				t.Errorf("hasAtmos(%q, %q, %q) = %v, want %v",
					tc.features, tc.relativePath, tc.sceneName, got, tc.want)
			}
		})
	}
}

func TestHdrBuckets(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{"sdr"}},
		{"HDR10", []string{"hdr10"}},
		{"HDR10Plus", []string{"hdr10plus"}},
		{"HDR10+", []string{"hdr10plus"}},
		{"DV", []string{"pq", "dv"}},
		{"DV HDR10", []string{"hdr10", "dv"}},
		{"DV/HDR10", []string{"hdr10", "dv"}},
		{"PQ", []string{"pq"}},
		{"WeirdValue", []string{"sdr"}},
	}
	for _, tc := range cases {
		got := hdrBuckets(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("hdrBuckets(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHdrBuckets_StandalonePQ(t *testing.T) {
	// "PQ" alone — bucket is pq, no DV.
	got := hdrBuckets("PQ")
	if len(got) != 1 || got[0] != "pq" {
		t.Errorf("got %v, want [pq]", got)
	}
}

func TestHdrBuckets_NoFalsePositiveOnSubstring(t *testing.T) {
	// "WeirDValue" contains "dv" as a substring but isn't a token.
	// Token-based detection must NOT trigger DV.
	got := hdrBuckets("WeirDValue")
	if len(got) != 1 || got[0] != "sdr" {
		t.Errorf("got %v, want [sdr] (no false-DV from substring)", got)
	}
	// Same for an isolated word that contains "dv" as letters.
	got = hdrBuckets("Adventure")
	if len(got) != 1 || got[0] != "sdr" {
		t.Errorf("got %v, want [sdr] (no false-DV from 'Adventure')", got)
	}
}

func TestHdrBuckets_RealRadarrInputsRoundTrip(t *testing.T) {
	// Every Radarr-known videoDynamicRangeType string must produce
	// a clean bucket result.
	cases := map[string][]string{
		"":               {"sdr"},
		"HDR10":          {"hdr10"},
		"HDR10Plus":      {"hdr10plus"},
		"PQ":             {"pq"},
		"HLG":            {"sdr"}, // HLG isn't in our explicit set; falls to sdr
		"DV":             {"pq", "dv"},
		"DV HDR10":       {"hdr10", "dv"},
		"DV HDR10Plus":   {"hdr10plus", "dv"},
		"DV HLG":         {"pq", "dv"}, // HLG variant lumped with pq base
		"DV SDR":         {"pq", "dv"}, // bizarre but theoretical Radarr label
	}
	for in, want := range cases {
		got := hdrBuckets(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("hdrBuckets(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAggregateForSeries_AllOccurring(t *testing.T) {
	perEp := [][]string{
		{"1080p", "h264"},
		{"1080p", "h265", "10bit"},
		{"2160p", "h265", "10bit"},
	}
	got := AggregateForSeries(perEp, AggAllOccurring)
	want := []string{"1080p", "h264", "h265", "10bit", "2160p"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAggregateForSeries_Strict(t *testing.T) {
	// Strict: only tags appearing in EVERY episode.
	perEp := [][]string{
		{"hdr10", "h265"},
		{"hdr10", "h265"},
		{"hdr10", "h265"},
	}
	got := AggregateForSeries(perEp, AggStrict)
	want := []string{"hdr10", "h265"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Mixed → strict drops everything.
	perEp = [][]string{
		{"hdr10"},
		{"sdr"},
	}
	got = AggregateForSeries(perEp, AggStrict)
	if len(got) != 0 {
		t.Errorf("strict on mixed = %v, want empty", got)
	}
}

func TestAggregateForSeries_HighestResolution(t *testing.T) {
	perEp := [][]string{
		{"720p"},
		{"1080p"},
		{"2160p"},
	}
	got := AggregateForSeries(perEp, AggHighest)
	if len(got) != 1 || got[0] != "2160p" {
		t.Errorf("got %v, want [2160p]", got)
	}
}

func TestAggregateForSeries_HighestAudioChannels(t *testing.T) {
	perEp := [][]string{
		{"5-1"},
		{"7-1"},
		{"5-1"},
	}
	got := AggregateForSeries(perEp, AggHighest)
	if len(got) != 1 || got[0] != "7-1" {
		t.Errorf("got %v, want [7-1]", got)
	}
}

func TestAggregateForSeries_Empty(t *testing.T) {
	got := AggregateForSeries(nil, AggAllOccurring)
	if got != nil {
		t.Errorf("empty input produced %v, want nil", got)
	}
}

func TestAggregateForSeries_HighestPreservesUnknownTags(t *testing.T) {
	// Unranked tags (e.g. user-added custom labels) must pass
	// through alongside the ranked highest. The function is
	// documented as defensive — never silently drop user input.
	perEp := [][]string{
		{"1080p", "custom-tag"},
		{"2160p", "custom-tag", "another"},
	}
	got := AggregateForSeries(perEp, AggHighest)
	sort.Strings(got)
	want := []string{"2160p", "another", "custom-tag"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want (sorted) %v", got, want)
	}
}

// ----- AggregateAudioForSeries -----

func TestAggregateAudioForSeries_Disabled(t *testing.T) {
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: false}}
	eps := []EpisodeInput{{Info: MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1}}}
	got := AggregateAudioForSeries(eps, cfg)
	if got != nil {
		t.Errorf("disabled bucket emitted %v, want nil", got)
	}
}

func TestAggregateAudioForSeries_Empty(t *testing.T) {
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring}}
	got := AggregateAudioForSeries(nil, cfg)
	if got != nil {
		t.Errorf("empty input emitted %v, want nil", got)
	}
}

func TestAggregateAudioForSeries_AllOccurringMixed(t *testing.T) {
	// Mixed series: S1 in 5.1 EAC3, S2 in 7.1 TrueHD-Atmos.
	// All-occurring → every codec / channel / atmos that appears
	// in ≥1 episode lands on the series.
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring}}
	eps := []EpisodeInput{
		{Info: MediaInfo{AudioCodec: "EAC3", AudioChannels: 5.1}},
		{Info: MediaInfo{AudioCodec: "EAC3", AudioChannels: 5.1}},
		{Info: MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1, AudioAdditionalFeatures: "Atmos"}},
	}
	got := AggregateAudioForSeries(eps, cfg)
	sort.Strings(got)
	want := []string{"5-1", "7-1", "atmos", "eac3", "truehd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAggregateAudioForSeries_StrictDropsMixed(t *testing.T) {
	// Strict: only tags every episode shares survive. Codec differs
	// here so it drops out; channels match so 5-1 survives.
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, SonarrAggregation: AggStrict}}
	eps := []EpisodeInput{
		{Info: MediaInfo{AudioCodec: "EAC3", AudioChannels: 5.1}},
		{Info: MediaInfo{AudioCodec: "TrueHD", AudioChannels: 5.1}},
	}
	got := AggregateAudioForSeries(eps, cfg)
	if len(got) != 1 || got[0] != "5-1" {
		t.Errorf("got %v, want [5-1]", got)
	}
}

func TestAggregateAudioForSeries_HighestChannels(t *testing.T) {
	// Audio bucket carries ONE strategy across codec/channels/atmos.
	// AggHighest only ranks channels (5-1<7-1) — audio codecs are
	// unranked in tagRank so they pass through as unknowns. Result:
	// ONE highest channel tag + every codec/flag that appeared.
	// Documenting the actual behaviour so a future tagRank extension
	// catches the test as it tightens.
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, SonarrAggregation: AggHighest}}
	eps := []EpisodeInput{
		{Info: MediaInfo{AudioCodec: "EAC3", AudioChannels: 5.1}},
		{Info: MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1}},
	}
	got := AggregateAudioForSeries(eps, cfg)
	sort.Strings(got)
	want := []string{"7-1", "eac3", "truehd"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAggregateAudioForSeries_AppliesPrefix(t *testing.T) {
	// Prefix on the bucket survives aggregation.
	cfg := AudioTagsConfig{Audio: BucketConfig{Enabled: true, Prefix: "audio-", SonarrAggregation: AggAllOccurring}}
	eps := []EpisodeInput{{Info: MediaInfo{AudioCodec: "EAC3", AudioChannels: 5.1}}}
	got := AggregateAudioForSeries(eps, cfg)
	sort.Strings(got)
	want := []string{"audio-5-1", "audio-eac3"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ----- AggregateVideoForSeries -----

func TestAggregateVideoForSeries_Empty(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
	}
	got := AggregateVideoForSeries(nil, cfg)
	if got != nil {
		t.Errorf("empty input emitted %v, want nil", got)
	}
}

func TestAggregateVideoForSeries_PerBucketStrategy(t *testing.T) {
	// Default-style config: Resolution all-occurring, Codec all-occurring,
	// HDR strict. Mixed-resolution + mixed-HDR series — resolution
	// emits both tags, HDR emits nothing (strict drops mixed).
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
		Codec:      BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
		HDR:        BucketConfig{Enabled: true, SonarrAggregation: AggStrict},
	}
	eps := []EpisodeInput{
		{Info: MediaInfo{Height: 1080, VideoCodec: "x264", VideoDynamicRangeType: "HDR10"}},
		{Info: MediaInfo{Height: 2160, VideoCodec: "x265", VideoDynamicRangeType: ""}}, // SDR
	}
	got := AggregateVideoForSeries(eps, cfg)
	sort.Strings(got)
	// Resolution all-occurring: 1080p + 2160p
	// Codec all-occurring: h264 + h265
	// HDR strict: hdr10 in ep1, sdr in ep2 → nothing survives
	want := []string{"1080p", "2160p", "h264", "h265"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAggregateVideoForSeries_QualityResolutionFallback(t *testing.T) {
	// Legacy episode has empty mediaInfo but
	// quality.quality.resolution=1080. Engine should fall back per
	// VideoTagsForFile's documented behaviour.
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
	}
	eps := []EpisodeInput{
		{Info: MediaInfo{}, QualityResolution: 1080},
		{Info: MediaInfo{Height: 2160}},
	}
	got := AggregateVideoForSeries(eps, cfg)
	sort.Strings(got)
	want := []string{"1080p", "2160p"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAggregateVideoForSeries_HDRStrictFullyHDR(t *testing.T) {
	// Every episode HDR10 → strict keeps it.
	cfg := VideoTagsConfig{
		HDR: BucketConfig{Enabled: true, SonarrAggregation: AggStrict},
	}
	eps := []EpisodeInput{
		{Info: MediaInfo{VideoDynamicRangeType: "HDR10"}},
		{Info: MediaInfo{VideoDynamicRangeType: "HDR10"}},
	}
	got := AggregateVideoForSeries(eps, cfg)
	if len(got) != 1 || got[0] != "hdr10" {
		t.Errorf("got %v, want [hdr10]", got)
	}
}

func TestAggregateVideoForSeries_DisabledBucketSkipped(t *testing.T) {
	// Resolution disabled — only codec emits.
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: false, SonarrAggregation: AggAllOccurring},
		Codec:      BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
	}
	eps := []EpisodeInput{
		{Info: MediaInfo{Height: 1080, VideoCodec: "x264"}},
	}
	got := AggregateVideoForSeries(eps, cfg)
	if len(got) != 1 || got[0] != "h264" {
		t.Errorf("got %v, want [h264]", got)
	}
}

func TestAggregateVideoForSeries_MixedResolutionHighest(t *testing.T) {
	// Resolution highest → just the top tag.
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, SonarrAggregation: AggHighest},
	}
	eps := []EpisodeInput{
		{Info: MediaInfo{Height: 720}},
		{Info: MediaInfo{Height: 1080}},
		{Info: MediaInfo{Height: 2160}},
	}
	got := AggregateVideoForSeries(eps, cfg)
	if len(got) != 1 || got[0] != "2160p" {
		t.Errorf("got %v, want [2160p]", got)
	}
}

func TestAggregateVideoForSeries_HDRHighestMultiTagPerEpisode(t *testing.T) {
	// hdrBuckets emits 1-2 tags per episode (e.g. "DV HDR10" → ["hdr10","dv"]).
	// AggHighest must pick the single rank-best across ALL episodes' tags.
	// Sentinel test: tagRank["dv"]=5, tagRank["hdr10plus"]=4 — even when one
	// episode has BOTH hdr10+dv and another has hdr10plus, the rank-5 dv
	// wins over rank-4 hdr10plus. Tightens guarantee on the multi-tag
	// AggHighest path so a future tagRank reshuffle catches the regression.
	cfg := VideoTagsConfig{
		HDR: BucketConfig{Enabled: true, SonarrAggregation: AggHighest},
	}
	eps := []EpisodeInput{
		{Info: MediaInfo{VideoDynamicRangeType: "DV HDR10"}},  // → ["hdr10","dv"]
		{Info: MediaInfo{VideoDynamicRangeType: "HDR10Plus"}}, // → ["hdr10plus"]
	}
	got := AggregateVideoForSeries(eps, cfg)
	if len(got) != 1 || got[0] != "dv" {
		t.Errorf("got %v, want [dv]", got)
	}
}

func TestAggregateAudioForSeries_SelectModeEmptyTagsNothing(t *testing.T) {
	// SelectMode="select" with empty AllowedValues = explicit "tag
	// nothing from this bucket". Engine must return empty even when
	// the audio bucket is enabled and episodes have populated mediaInfo.
	// Wraps the BucketConfig.allowed contract — without this gate every
	// per-episode pass would emit nothing, so the aggregate would also
	// be empty, but a regression in allowed() would silently flip
	// behaviour.
	cfg := AudioTagsConfig{Audio: BucketConfig{
		Enabled:           true,
		SonarrAggregation: AggAllOccurring,
		SelectMode:        "select",
		AllowedValues:     nil,
	}}
	eps := []EpisodeInput{
		{Info: MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1, AudioAdditionalFeatures: "Atmos"}},
	}
	got := AggregateAudioForSeries(eps, cfg)
	if got != nil {
		t.Errorf("SelectMode=select+empty AllowedValues emitted %v, want nil", got)
	}
}
