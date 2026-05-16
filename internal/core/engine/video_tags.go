package engine

// video_tags.go — Video-tags engine helpers (M4 video split). Reads
// Radarr/Sonarr's mediaInfo.height / videoCodec / videoBitDepth /
// videoDynamicRangeType and emits informative auto-tags for the
// video stream. Pure functions, no I/O.
//
// Vocabulary covers three buckets:
//   - resolution: 2160p / 1440p / 1080p / 720p / 480p / sd
//   - codec:      h265 / h264 / av1 / 10bit / mpeg4 / mpeg2 / vc1
//   - hdr:        sdr / pq / hdr10 / hdr10plus / dv
//
// Three buckets each with its own toggle + prefix because users
// commonly want different namespacing or selective emission per
// category (e.g. "tag every codec but not resolution").
//
// The base "dv" tag emits from THIS file (HDR bucket). The DV
// detail layer (mel/fel/dvprofile8/cm2/cm4) lives in the separate
// dvdetail flow that requires opt-in tools install — see
// dv_summary.go + scan_dv_detail.go.

// vocabResolution / vocabCodec / vocabHDR are the canonical value
// lists. Single source of truth — VideoVocabulary returns them and
// AllPossibleVideoTags / VideoTagsForFile both iterate them.
var (
	vocabResolution = []string{"2160p", "1440p", "1080p", "720p", "480p", "sd"}
	vocabCodec      = []string{"h265", "h264", "av1", "10bit", "mpeg4", "mpeg2", "vc1"}
	vocabHDR        = []string{"sdr", "pq", "hdr10", "hdr10plus", "dv"}
)

// VideoVocabulary returns the three canonical bucket vocabularies.
// UI uses this for the per-value allow-list checkbox matrix; API
// uses it for input validation.
//
// Returns slice copies so callers can't mutate the package-level
// state.
func VideoVocabulary() (resolution, codec, hdr []string) {
	cp := func(s []string) []string { return append([]string(nil), s...) }
	return cp(vocabResolution), cp(vocabCodec), cp(vocabHDR)
}

// VideoTagsConfig is the engine-side config the Video-tags scan
// emits against. Mirror of core.VideoTagsConfig.
type VideoTagsConfig struct {
	Resolution BucketConfig
	Codec      BucketConfig
	HDR        BucketConfig
}

// VideoTagsForFile returns the video-related tag labels for one
// file's mediaInfo. qualityResolution is the integer from
// movieFile.quality.quality.resolution — used as a fallback when
// mediaInfo is nil or has Height == 0.
//
// Pure function; no side effects. Empty slice when every bucket is
// disabled, mediaInfo + qualityResolution are both empty, or all
// values are filtered out.
func VideoTagsForFile(mi MediaInfo, qualityResolution int, cfg VideoTagsConfig) []string {
	var out []string

	if cfg.Resolution.Enabled {
		if tag := resolutionBucket(mi.Height, qualityResolution); tag != "" && cfg.Resolution.allowed(tag) {
			out = append(out, cfg.Resolution.Prefix+cfg.Resolution.label(tag))
		}
	}

	if cfg.Codec.Enabled {
		if tag := codecBucket(mi.VideoCodec); tag != "" && cfg.Codec.allowed(tag) {
			out = append(out, cfg.Codec.Prefix+cfg.Codec.label(tag))
		}
		if mi.VideoBitDepth == 10 && cfg.Codec.allowed("10bit") {
			out = append(out, cfg.Codec.Prefix+cfg.Codec.label("10bit"))
		}
	}

	if cfg.HDR.Enabled {
		for _, tag := range hdrBuckets(mi.VideoDynamicRangeType) {
			if cfg.HDR.allowed(tag) {
				out = append(out, cfg.HDR.Prefix+cfg.HDR.label(tag))
			}
		}
	}

	return out
}

// AllPossibleVideoTags returns the universe of video tags this
// configuration could ever emit, regardless of Enabled or
// AllowedValues. Cleanup safety-bound.
func AllPossibleVideoTags(cfg VideoTagsConfig) map[string]string {
	out := make(map[string]string)
	emit := func(b BucketConfig, bucket string, values []string) {
		for _, v := range values {
			out[b.Prefix+b.label(v)] = bucket
		}
	}
	emit(cfg.Resolution, "resolution", vocabResolution)
	emit(cfg.Codec, "codec", vocabCodec)
	emit(cfg.HDR, "hdr", vocabHDR)
	return out
}

// EmittableVideoTags returns only the labels this configuration
// would emit RIGHT NOW given Enabled + AllowedValues. Companion
// to AllPossibleVideoTags.
func EmittableVideoTags(cfg VideoTagsConfig) map[string]string {
	out := make(map[string]string)
	emitIf := func(b BucketConfig, bucket string, values []string) {
		if !b.Enabled {
			return
		}
		for _, v := range values {
			if !b.allowed(v) {
				continue
			}
			out[b.Prefix+b.label(v)] = bucket
		}
	}
	emitIf(cfg.Resolution, "resolution", vocabResolution)
	emitIf(cfg.Codec, "codec", vocabCodec)
	emitIf(cfg.HDR, "hdr", vocabHDR)
	return out
}
