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
		features string
		want     bool
	}{
		{"Atmos", true}, {"atmos", true},
		{"Dolby Atmos / DD+", true},
		{"", false},
		{"DTS:X", false},
	}
	for _, tc := range cases {
		got := hasAtmos(tc.features)
		if got != tc.want {
			t.Errorf("hasAtmos(%q) = %v, want %v", tc.features, got, tc.want)
		}
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
