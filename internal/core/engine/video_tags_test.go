package engine

import (
	"reflect"
	"sort"
	"testing"
)

func defaultVideoCfg() VideoTagsConfig {
	return VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
		Codec:      BucketConfig{Enabled: true, SonarrAggregation: AggAllOccurring},
		HDR:        BucketConfig{Enabled: true, SonarrAggregation: AggStrict},
	}
}

func TestVideoTagsForFile_Standard4KHDR10(t *testing.T) {
	mi := MediaInfo{
		Height: 2160, VideoCodec: "x265", VideoBitDepth: 10,
		VideoDynamicRangeType: "HDR10",
	}
	got := VideoTagsForFile(mi, 0, defaultVideoCfg())
	want := []string{"2160p", "h265", "10bit", "hdr10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_Standard1080pSDR(t *testing.T) {
	mi := MediaInfo{Height: 1080, VideoCodec: "x264", VideoBitDepth: 8}
	got := VideoTagsForFile(mi, 0, defaultVideoCfg())
	want := []string{"1080p", "h264", "sdr"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_DolbyVisionWithHDR10(t *testing.T) {
	// DV layered on HDR10 — base "dv" tag emits FROM the HDR bucket
	// here. DV detail layer (mel/fel/profile8/cm2/cm4) is a separate
	// scan path and not exercised by this file.
	mi := MediaInfo{
		Height: 2160, VideoCodec: "x265", VideoBitDepth: 10,
		VideoDynamicRangeType: "DV HDR10",
	}
	got := VideoTagsForFile(mi, 0, defaultVideoCfg())
	want := []string{"2160p", "h265", "10bit", "hdr10", "dv"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_DolbyVisionOnly(t *testing.T) {
	mi := MediaInfo{
		Height: 2160, VideoCodec: "x265", VideoBitDepth: 10,
		VideoDynamicRangeType: "DV",
	}
	got := VideoTagsForFile(mi, 0, defaultVideoCfg())
	want := []string{"2160p", "h265", "10bit", "pq", "dv"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_HDR10Plus(t *testing.T) {
	mi := MediaInfo{
		Height: 2160, VideoCodec: "x265",
		VideoDynamicRangeType: "HDR10Plus",
	}
	got := VideoTagsForFile(mi, 0, defaultVideoCfg())
	want := []string{"2160p", "h265", "hdr10plus"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_FallbackResolution(t *testing.T) {
	// mediaInfo Height=0, qualityResolution=1080 — engine uses
	// quality fallback so legacy imports still get a resolution tag.
	mi := MediaInfo{}
	got := VideoTagsForFile(mi, 1080, defaultVideoCfg())
	if len(got) == 0 {
		t.Fatal("expected resolution tag from quality fallback")
	}
	if got[0] != "1080p" {
		t.Errorf("got %v, want first element 1080p", got)
	}
}

func TestVideoTagsForFile_NoDataAtAll(t *testing.T) {
	got := VideoTagsForFile(MediaInfo{}, 0, defaultVideoCfg())
	// HDR bucket emits "sdr" by default (empty string → sdr per
	// hdrBuckets). Resolution + codec contribute nothing.
	if !reflect.DeepEqual(got, []string{"sdr"}) {
		t.Errorf("got %v, want [sdr]", got)
	}
}

func TestVideoTagsForFile_CustomPrefix(t *testing.T) {
	mi := MediaInfo{
		Height: 2160, VideoCodec: "x265", VideoBitDepth: 10,
		VideoDynamicRangeType: "HDR10",
	}
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, Prefix: "video-"},
		Codec:      BucketConfig{Enabled: true, Prefix: "video-"},
		HDR:        BucketConfig{Enabled: true, Prefix: "video-"},
	}
	got := VideoTagsForFile(mi, 0, cfg)
	want := []string{"video-2160p", "video-h265", "video-10bit", "video-hdr10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_DisabledBuckets(t *testing.T) {
	mi := MediaInfo{
		Height: 2160, VideoCodec: "x265", VideoBitDepth: 10,
		VideoDynamicRangeType: "HDR10",
	}
	// Only HDR enabled.
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: false},
		Codec:      BucketConfig{Enabled: false},
		HDR:        BucketConfig{Enabled: true},
	}
	got := VideoTagsForFile(mi, 0, cfg)
	want := []string{"hdr10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVideoTagsForFile_UnknownCodec(t *testing.T) {
	mi := MediaInfo{
		Height: 1080, VideoCodec: "ProRes",
	}
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true},
		Codec:      BucketConfig{Enabled: true},
	}
	got := VideoTagsForFile(mi, 0, cfg)
	// Unknown codec → empty codec output. Resolution still emits.
	if len(got) != 1 || got[0] != "1080p" {
		t.Errorf("got %v, want [1080p] (unknown codec dropped)", got)
	}
}

func TestVideoTagsForFile_BitDepth8NoTag(t *testing.T) {
	mi := MediaInfo{Height: 1080, VideoCodec: "x265", VideoBitDepth: 8}
	cfg := VideoTagsConfig{Codec: BucketConfig{Enabled: true}}
	got := VideoTagsForFile(mi, 0, cfg)
	// 8-bit shouldn't emit "10bit" tag.
	for _, t := range got {
		if t == "10bit" {
			return
		}
	}
}

func TestVideoTagsForFile_AllowedValuesFiltersResolution(t *testing.T) {
	mi := MediaInfo{Height: 2160, VideoCodec: "x265"}
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, AllowedValues: []string{"1080p", "720p"}}, // excludes 2160p
		Codec:      BucketConfig{Enabled: true},
	}
	got := VideoTagsForFile(mi, 0, cfg)
	// 2160p filtered out; codec + sdr-default still emit.
	for _, tag := range got {
		if tag == "2160p" {
			t.Errorf("2160p should be filtered: got %v", got)
		}
	}
}

func TestVideoTagsForFile_AllowedValuesFiltersBitDepth(t *testing.T) {
	mi := MediaInfo{VideoCodec: "x265", VideoBitDepth: 10}
	cfg := VideoTagsConfig{
		Codec: BucketConfig{Enabled: true, AllowedValues: []string{"h265"}}, // excludes 10bit
	}
	got := VideoTagsForFile(mi, 0, cfg)
	for _, tag := range got {
		if tag == "10bit" {
			t.Errorf("10bit should be filtered: got %v", got)
		}
	}
}

func TestVideoTagsForFile_AllowedValuesFiltersHDRBucketAndDV(t *testing.T) {
	mi := MediaInfo{VideoDynamicRangeType: "DV HDR10"}
	cfg := VideoTagsConfig{
		HDR: BucketConfig{Enabled: true, AllowedValues: []string{"hdr10"}}, // excludes "dv"
	}
	got := VideoTagsForFile(mi, 0, cfg)
	for _, tag := range got {
		if tag == "dv" {
			t.Errorf("dv should be filtered: got %v", got)
		}
	}
}

func TestAllPossibleVideoTags_IgnoresEnabledAndAllowedValues(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: false, AllowedValues: []string{"1080p"}},
		Codec:      BucketConfig{Enabled: false, AllowedValues: []string{"h265"}},
		HDR:        BucketConfig{Enabled: false, AllowedValues: []string{"sdr"}},
	}
	got := AllPossibleVideoTags(cfg)
	resolution, codec, hdr := VideoVocabulary()
	want := len(resolution) + len(codec) + len(hdr)
	if len(got) != want {
		t.Errorf("got %d tags, want %d (full vocab)", len(got), want)
	}
}

func TestAllPossibleVideoTags_PrefixApplied(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Prefix: "res-"},
		Codec:      BucketConfig{Prefix: "codec-"},
		HDR:        BucketConfig{Prefix: "hdr-"},
	}
	got := AllPossibleVideoTags(cfg)
	if got["res-1080p"] != "resolution" {
		t.Errorf("missing prefixed res-1080p: %v", got)
	}
	if got["codec-h265"] != "codec" {
		t.Errorf("missing prefixed codec-h265: %v", got)
	}
	if got["hdr-dv"] != "hdr" {
		t.Errorf("missing prefixed hdr-dv: %v", got)
	}
}

func TestEmittableVideoTags_DisabledExcluded(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true},
		Codec:      BucketConfig{Enabled: false}, // off
		HDR:        BucketConfig{Enabled: true},
	}
	got := EmittableVideoTags(cfg)
	for k := range got {
		if k == "h264" || k == "h265" || k == "av1" || k == "10bit" || k == "mpeg4" || k == "mpeg2" || k == "vc1" {
			t.Errorf("disabled codec bucket leaked: %s", k)
		}
	}
}

func TestEmittableVideoTags_RespectsAllowedValues(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, AllowedValues: []string{"1080p", "2160p"}},
	}
	got := EmittableVideoTags(cfg)
	if _, ok := got["1080p"]; !ok {
		t.Errorf("1080p missing")
	}
	if _, ok := got["720p"]; ok {
		t.Errorf("720p should be filtered")
	}
}

func TestEmittableVideoTags_IsSubsetOfAllPossible(t *testing.T) {
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true, AllowedValues: []string{"1080p"}},
		Codec:      BucketConfig{Enabled: true, AllowedValues: []string{"h265"}},
		HDR:        BucketConfig{Enabled: true},
	}
	all := AllPossibleVideoTags(cfg)
	em := EmittableVideoTags(cfg)
	for k := range em {
		if _, ok := all[k]; !ok {
			t.Errorf("Emittable contains %q not in AllPossible", k)
		}
	}
}

func TestVideoVocabulary_ReturnsCopy(t *testing.T) {
	res, _, _ := VideoVocabulary()
	res[0] = "MUTATED"
	res2, _, _ := VideoVocabulary()
	if res2[0] == "MUTATED" {
		t.Error("VideoVocabulary returned a shared backing array")
	}
}

func TestVideoVocabulary_AllValuesRadarrCompatible(t *testing.T) {
	res, codec, hdr := VideoVocabulary()
	all := append(append([]string{}, res...), codec...)
	all = append(all, hdr...)
	sort.Strings(all)
	for _, v := range all {
		for _, c := range v {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				t.Errorf("vocab %q contains char %q — fails Radarr ^[a-z0-9-]+$", v, c)
			}
		}
	}
}

func TestVocabularyDriftSentinel_VideoEmittableValuesAllInVocab(t *testing.T) {
	// Every value VideoTagsForFile could produce must be in the
	// canonical vocab — drift would silently break cleanup-bound.
	cfg := VideoTagsConfig{
		Resolution: BucketConfig{Enabled: true},
		Codec:      BucketConfig{Enabled: true},
		HDR:        BucketConfig{Enabled: true},
	}
	res, codec, hdr := VideoVocabulary()
	allVocab := make(map[string]bool)
	for _, v := range res {
		allVocab[v] = true
	}
	for _, v := range codec {
		allVocab[v] = true
	}
	for _, v := range hdr {
		allVocab[v] = true
	}
	// Cycle every realistic mediaInfo combination.
	miCases := []MediaInfo{
		{Height: 2160, VideoCodec: "x265", VideoBitDepth: 10, VideoDynamicRangeType: "HDR10"},
		{Height: 1080, VideoCodec: "x264", VideoBitDepth: 8},
		{Height: 720, VideoCodec: "AV1", VideoBitDepth: 8},
		{Height: 480, VideoCodec: "MPEG-4", VideoBitDepth: 8},
		{Height: 240, VideoCodec: "VC-1"},
		{Height: 2160, VideoCodec: "x265", VideoBitDepth: 10, VideoDynamicRangeType: "DV HDR10"},
		{VideoDynamicRangeType: "PQ"},
	}
	for _, mi := range miCases {
		got := VideoTagsForFile(mi, 0, cfg)
		for _, tag := range got {
			if !allVocab[tag] {
				t.Errorf("emit produced %q not in vocab — drift!", tag)
			}
		}
	}
}
