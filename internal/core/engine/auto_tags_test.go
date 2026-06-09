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
		name              string
		width             int
		videoResolution   string
		mediaInfoHeight   int
		qualityResolution int
		want              string
	}{
		// --- Webhook-payload path (Width int set directly) ---
		// Connect webhook events ship width + height as separate ints
		// with NO resolution string. Real-world payload samples cover
		// the strict-height failure mode: cinematic-cropped releases
		// where the file height is below the canonical tier (1600 for
		// a 4K 2.40:1 cut; 800 for a 1080p 2.40:1 cut). The strict
		// h>=tier bucketing that shipped before this dropped them
		// one tier too low; width-based bucketing pins them correctly.
		{"webhook 4K cinematic 2.40:1 (Hokum 2160p)", 3840, "", 1600, 0, "2160p"},
		{"webhook 4K 1.85:1 (Horizon)", 3840, "", 2076, 0, "2160p"},
		{"webhook 1080p cinematic 2.40:1 (Hokum h264)", 1920, "", 800, 0, "1080p"},
		{"webhook 4K 16:9", 3840, "", 2160, 0, "2160p"},
		{"webhook DCI 4K", 4096, "", 2160, 0, "2160p"},
		{"webhook QHD 1440p", 2560, "", 1440, 0, "1440p"},
		{"webhook 720p 2.40:1", 1280, "", 536, 0, "720p"},
		{"webhook 480p NTSC", 720, "", 480, 0, "480p"},

		// Sub-canonical widths — real WEB encodes sit a few pixels under
		// the canonical width, so the tier bound must be permissive (the
		// lower edge of the tier, not the exact canonical value). A
		// strict `w >= 1920` dropped these one tier too low.
		{"webhook 1080p ATVP 1918 wide (Cape Fear S01E01)", 1918, "", 816, 0, "1080p"},
		{"webhook 1080p 1916 wide", 1916, "", 1036, 0, "1080p"},
		{"webhook 4K scope 3838 wide", 3838, "", 1602, 0, "2160p"},
		{"webhook 720p 1278 wide", 1278, "", 692, 0, "720p"},

		// File-truth wins over Radarr's release-name-derived quality
		// bucket. If quality.resolution says 2160 but the file is
		// 1920x1080, the tag reflects the file — gives the user a
		// visible signal that Arr's classification is wrong.
		{"webhook file-truth vs Arr misclassification", 1920, "", 1080, 2160, "1080p"},

		// --- API-path (VideoResolution "WxH" string, no Width int) ---
		// GET /api/v3/movie + /api/v3/episodefile return only the
		// resolution string; we parse out width from it.
		{"API 4K 16:9", 0, "3840x2160", 0, 0, "2160p"},
		{"API 4K cinematic 2.40:1", 0, "3840x1600", 0, 0, "2160p"},
		{"API 1080p 16:9", 0, "1920x1080", 0, 0, "1080p"},
		{"API 1080p cinematic", 0, "1920x800", 0, 0, "1080p"},
		{"API 1080p ATVP 1918x816 (Cape Fear S01E01)", 0, "1918x816", 0, 0, "1080p"},

		// --- Height-fallback path (no width, no resolution string) ---
		// Permissive thresholds: canonical heights are the UPPER bound
		// of the tier, cinematic crops sit below. "h > lower-canonical"
		// catches them; the strict "h >= tier" check that shipped
		// before this lands one tier too low for nearly all theatrical
		// movies.
		{"fallback height 2160", 0, "", 2160, 0, "2160p"},
		{"fallback height 1608 cinematic 4K", 0, "", 1608, 0, "2160p"},
		{"fallback height 1440 exact QHD", 0, "", 1440, 0, "1440p"},
		{"fallback height 1080 exact FHD", 0, "", 1080, 0, "1080p"},
		{"fallback height 802 cinematic 1080p", 0, "", 802, 0, "1080p"},
		{"fallback height 720 exact", 0, "", 720, 0, "720p"},
		{"fallback height 480 exact", 0, "", 480, 0, "480p"},

		// --- Last-resort quality.resolution fallback ---
		// Pre-mediaInfo Radarr imports left everything empty;
		// quality.resolution was populated regardless.
		{"quality fallback 2160", 0, "", 0, 2160, "2160p"},
		{"quality fallback 1080", 0, "", 0, 1080, "1080p"},
		{"all empty", 0, "", 0, 0, ""},

		// --- Malformed videoResolution falls through to height ---
		{"malformed videoResolution falls to height", 0, "1080p", 1080, 0, "1080p"},
		{"empty parts string falls to height", 0, "x", 1080, 0, "1080p"},
		{"non-numeric width falls to height", 0, "axb", 1080, 0, "1080p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolutionBucket(tc.width, tc.videoResolution, tc.mediaInfoHeight, tc.qualityResolution)
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
		audioCodec   string
		features     string
		relativePath string
		sceneName    string
		want         bool
	}{
		// AudioCodec match — webhook-payload path. Real captures:
		// Horizon (DD+ Atmos) and the TrueHD Atmos 7.1 4K samples.
		{"audiocodec_eac3_atmos", "EAC3 Atmos", "", "", "", true},
		{"audiocodec_truehd_atmos", "TrueHD Atmos", "", "", "", true},
		{"audiocodec_lower_atmos", "eac3 atmos", "", "", "", true},

		// AudioAdditionalFeatures match — API-path. Modern Radarr
		// populates this when MediaInfo detected Atmos at import time.
		{"feature_match_capital", "", "Atmos", "", "", true},
		{"feature_match_lower", "", "atmos", "", "", true},
		{"feature_match_compound", "", "Dolby Atmos / DD+", "", "", true},

		// Filename fallback when both codec + features are blank.
		{"filename_token_uhd_release",
			"", "", "Movie.2024.UHD.BluRay.2160p.HDR10.TrueHD.Atmos.7.1.x265-FLUX.mkv", "",
			true},
		{"scenename_token",
			"", "", "", "Movie.2024.UHD.BluRay.TrueHD.Atmos.7.1.x265-FLUX",
			true},
		{"filename_lowercase",
			"", "", "movie.2024.webdl.atmos.5.1-ntb.mkv", "",
			true},

		// Negative paths.
		{"empty_all", "", "", "", "", false},
		{"audiocodec_negative", "EAC3", "", "", "", false},
		{"features_negative", "", "DTS:X", "", "", false},
		{"no_atmos_token_in_filename",
			"", "", "Movie.2024.WEB-DL.DDP.5.1.x264-NTb.mkv", "Movie.2024.WEB-DL.DDP.5.1-NTb",
			false},
		// Substring guard — "atmospheric" in a movie title shouldn't trigger.
		{"substring_false_positive_guard",
			"", "", "Atmospheric.Pressure.2024.WEB-DL.DDP.5.1-NTb.mkv", "",
			false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasAtmos(tc.audioCodec, tc.features, tc.relativePath, tc.sceneName)
			if got != tc.want {
				t.Errorf("hasAtmos(%q, %q, %q, %q) = %v, want %v",
					tc.audioCodec, tc.features, tc.relativePath, tc.sceneName, got, tc.want)
			}
		})
	}
}

// TestIs10Bit locks the bit-depth inference: when VideoBitDepth is
// populated (API path) use it directly; when absent (webhook path)
// derive from VideoDynamicRangeType. HDR variants are all 10-bit or
// higher by spec — 8-bit HDR doesn't ship in consumer media.
func TestIs10Bit(t *testing.T) {
	cases := []struct {
		name          string
		videoBitDepth int
		rangeType     string
		want          bool
	}{
		// API path — bit-depth set directly.
		{"explicit 10-bit", 10, "", true},
		{"explicit 8-bit", 8, "", false},
		{"explicit 10-bit with HDR rangeType", 10, "HDR10", true},

		// Webhook path — bit-depth absent, infer from rangeType.
		// Real webhook captures: Horizon (DV HDR10Plus), Hokum (HDR10Plus).
		{"webhook HDR10", 0, "HDR10", true},
		{"webhook HDR10Plus", 0, "HDR10Plus", true},
		{"webhook DV HDR10", 0, "DV HDR10", true},
		{"webhook DV HDR10Plus", 0, "DV HDR10Plus", true},
		{"webhook plain DV", 0, "DV", true},
		{"webhook HLG", 0, "HLG", true},
		{"webhook PQ", 0, "PQ", true},

		// Webhook path with no HDR — SDR file, bit-depth missing.
		// We can't infer 10-bit reliably here; tag is intentionally
		// absent (better to miss a niche SDR 10-bit encode than to
		// false-positive an 8-bit SDR file).
		{"webhook SDR no rangeType", 0, "", false},
		{"webhook explicit SDR rangeType", 0, "SDR", false},
		{"webhook SDR lowercase", 0, "sdr", false},
		{"webhook SDR whitespace", 0, "  ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := is10Bit(tc.videoBitDepth, tc.rangeType)
			if got != tc.want {
				t.Errorf("is10Bit(%d, %q) = %v, want %v",
					tc.videoBitDepth, tc.rangeType, got, tc.want)
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
	// Codec all-occurring: h264 + h265, plus 10bit on ep1 (HDR10 implies
	//   10-bit by spec; is10Bit infers it when VideoBitDepth is absent).
	// HDR strict: hdr10 in ep1, sdr in ep2 → nothing survives
	want := []string{"1080p", "2160p", "10bit", "h264", "h265"}
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
