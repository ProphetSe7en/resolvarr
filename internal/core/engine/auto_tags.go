package engine

import (
	"strconv"
	"strings"
)

// auto_tags.go — shared helpers for the M4 informative auto-tagging
// flows: Audio tags (mediaInfo audio bucket) + Video tags (mediaInfo
// resolution / codec / HDR + DV detail). Each flow has its own config
// + scan handler + UI sub-tab; this file owns the pure
// mediaInfo-to-label mappers + the per-bucket configuration shape +
// the Sonarr per-episode → per-series aggregation strategy.
//
// Sources consulted (semantic reference, no code copied):
//
//   - HDR-bucket detection patterns identified by mvanbaak (original)
//     and jpalenz77 (TRaSH Discord, dv-hdr_tagarr.sh v2.0.0 2025-12-21).
//     Re-implemented from scratch in Go; the regex priority chain in
//     hdrBuckets() matches the algorithm but no code was ported.
//   - Aggregation strategy "all-occurring" pattern observed in onedr0p
//     home-ops (tag-resolution.sh, tag-codecs.sh — WTFPL).
//
// The engine helpers here are pure functions — no I/O, no globals.
// Library scan and the future webhook receiver call AudioTagsForFile /
// VideoTagsForFile with the same inputs and get the same tag set.

// MediaInfo mirrors arr.MediaInfo fields the engine needs. Defined here
// so engine has no dependency on the arr package — callers translate
// arr.MediaInfo → engine.MediaInfo at the handler boundary.
type MediaInfo struct {
	// Width / VideoResolution / Height — three competing signals for
	// resolutionBucket. Width is the canonical per-tier dimension
	// (4K=3840, 1440p=2560, 1080p=1920, 720p=1280, 480p=720) and is
	// immune to letterbox / cinematic crops. The two Arr surfaces
	// populate these differently:
	//  - API GET responses set VideoResolution ("3840x1600") only;
	//    Width + Height are absent.
	//  - Connect webhook payloads set Width + Height ints; the
	//    VideoResolution string is absent.
	// resolutionBucket reads in this order: Width int > parse Width
	// from VideoResolution > permissive Height bucket >
	// quality.resolution last-resort.
	Width                   int
	VideoResolution         string
	Height                  int     // Pixel height. Letterbox-cropped on webhook payloads; canonical only on 16:9 content.
	VideoCodec              string  // "x264" | "x265" | "AV1" | etc
	VideoBitDepth           int     // 8 or 10
	VideoDynamicRangeType   string  // "" | "HDR10" | "HDR10Plus" | "DV" | "DV HDR10" | "PQ"
	AudioCodec              string  // "TrueHD" | "DTS-X" | "AC3" | etc
	AudioChannels           float64 // 2.0 | 5.1 | 7.1
	AudioAdditionalFeatures string  // contains "Atmos" sometimes
	// RelativePath + SceneName let detection helpers (notably hasAtmos)
	// fall back on filename tokens when MediaInfo fields are blank.
	// Old Radarr imports + Atmos-in-EAC3 streams sometimes leave
	// AudioAdditionalFeatures empty even when the file IS Atmos.
	RelativePath string // e.g., "Movie.2024.UHD.BluRay.TrueHD.Atmos.7.1.x265-FLUX.mkv"
	SceneName    string // original release name when imported via Radarr
}

// BucketConfig captures one bucket's toggle + prefix + per-value
// allow-list. SonarrAggregation is unused for Radarr (per-file
// tagging — no aggregation needed); for Sonarr it controls how
// per-episode tag sets collapse to a series-level set. See
// AggregateForSeries below.
//
// AllowedValues controls WHICH bucket values get emitted. nil/empty
// means "all values allowed" (matches the original behaviour); a
// non-empty slice restricts emission to listed values only — values
// outside the list are skipped at emit-time.
//
// Important: AllowedValues only filters what gets EMITTED. The cleanup
// safety-bound (AllPossible*Tags) still returns the full bucket
// vocabulary so the scan handler knows that "1080p" is one of OUR
// tags even when the user just disabled it — that lets the next Apply
// remove it from movies that previously had it. Without this, disabled
// values would silently leak as orphans.
type BucketConfig struct {
	Enabled           bool
	Prefix            string
	SonarrAggregation AggregationStrategy
	AllowedValues     []string
	// SelectMode disambiguates an empty AllowedValues list:
	//   ""       (or "all")    — empty means "all allowed" (legacy default,
	//                            backward-compatible with configs predating
	//                            the explicit-none toggle)
	//   "select"               — the AllowedValues list is exact: empty
	//                            means "tag nothing from this bucket"
	// Lets the UI offer a Select-none button without disabling the bucket
	// outright (the prior workaround) — empty + select-mode is a valid
	// "tag nothing yet, but bucket stays on" state.
	SelectMode string
	// Labels is a sparse override map from canonical engine value to the
	// user-chosen replacement value. Keys must be in the bucket's
	// canonical vocabulary (vocabAudioCodecs / vocabResolution / etc).
	// A missing or empty value means "use the engine default".
	//
	// Override scope is the value portion only — the bucket Prefix still
	// applies on top. So Prefix="dv-" + Labels["dvprofile8"]="profile8"
	// emits "dv-profile8". A user who wants no prefix at all leaves
	// Prefix empty (it's a separate per-bucket setting).
	//
	// Cleanup safety: AllPossible / Emittable + emit all resolve through
	// label(), so the configured vocabulary IS the cleanup scope. After
	// a rename, the OLD label is no longer "ours" — orphans from before
	// the rename stay on the items untouched. Documented in CHANGELOG
	// and the bucket-config UI hint.
	Labels map[string]string
}

// label returns the emit value for a canonical bucket value. If the user
// supplied an override in Labels, that override is returned (caller still
// applies Prefix). Empty / missing override → engine default.
func (b BucketConfig) label(value string) string {
	if v, ok := b.Labels[value]; ok && v != "" {
		return v
	}
	return value
}

// allowed returns true when value passes the bucket's per-value filter.
// Two modes:
//   - SelectMode != "select": back-compat. Empty/nil AllowedValues means
//     "all allowed"; a non-empty slice means "only these listed values pass".
//   - SelectMode == "select": exact list. Empty list means "tag nothing".
//
// Linear scan is fine — typical bucket vocabulary is 5-12 entries.
func (b BucketConfig) allowed(value string) bool {
	if b.SelectMode != "select" && len(b.AllowedValues) == 0 {
		return true
	}
	for _, v := range b.AllowedValues {
		if v == value {
			return true
		}
	}
	return false
}

// AggregationStrategy controls Sonarr per-episode → per-series collapse.
type AggregationStrategy int

const (
	// AggAllOccurring tags with EVERY value that appears in ≥1 episode.
	// Mixed-quality series get multiple tags. Default for Resolution /
	// Codec / Audio-codec — supports "any episodes match X" filtering.
	AggAllOccurring AggregationStrategy = iota

	// AggStrict tags only when ALL episodes match the same value. No
	// tag if mixed. Default for HDR bucket — mixed HDR is unusual and
	// usually a partial-upgrade state; strict means "fully converted".
	AggStrict

	// AggHighest tags with the highest-grade value present (resolution:
	// 2160 > 1080; codec: av1 > h265 > h264; audio-channels: 7.1 > 5.1).
	// Single tag, "series-is-X-capable" semantics. Default for
	// Audio-channels — mixed 5.1+7.1 series → "7.1 capable".
	AggHighest
)

// parseResolutionWxH parses Radarr/Sonarr's mediaInfo.videoResolution
// string ("3840x2160" / "1920x800") into width + height ints. Returns
// 0, 0 on empty / malformed input so the caller falls back gracefully
// to other signals. Case-insensitive on the "x" separator; tolerates
// leading/trailing whitespace.
func parseResolutionWxH(s string) (width, height int) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, 0
	}
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return 0, 0
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w < 0 || h < 0 {
		return 0, 0
	}
	return w, h
}

// bucketByWidth maps a pixel width to a tier label. Canonical per-tier
// widths: 4K=3840, 1440p=2560, 1080p=1920, 720p=1280, 480p=720. Width
// is the cleanest bucketing axis because letterbox / cinematic crops
// shrink HEIGHT, not WIDTH (a 3840x1608 4K cinema cut still has w=3840).
//
// Thresholds are the LOWER bound of each tier (roughly the midpoint to
// the tier below), NOT the canonical width. Real-world WEB encodes sit a
// few pixels under canonical: ATVP ships 1080p at 1918, DCI 4K is 4096,
// 720p sources land at 1278. A strict `w >= 1920` check dropped a 1918-
// wide 1080p one tier to 720p. The permissive bounds catch the real
// spread while staying well clear of the next tier down.
//
// Returns "" for non-positive width so callers can distinguish "no
// signal" from a deliberate "sd" bucket; callers gate on `w > 0`
// before invoking, but the guard makes the helper self-defensive.
func bucketByWidth(w int) string {
	if w <= 0 {
		return ""
	}
	switch {
	case w >= 3200:
		return "2160p"
	case w >= 2240:
		return "1440p"
	case w >= 1600:
		return "1080p"
	case w >= 1000:
		return "720p"
	case w >= 600:
		return "480p"
	}
	return "sd"
}

// resolutionBucket maps a file's reported dimensions to a tier label.
// Truth hierarchy (file-truth first, Radarr-classification last):
//
//  1. Width int — webhook payloads set this directly. Bucket on width.
//  2. Width parsed from VideoResolution "WxH" string — API GET path.
//  3. Permissive Height bucket — fallback when neither width source
//     resolved. Canonical heights are the UPPER bound of the tier
//     (1080p tops out at 1080), so the strict "h >= tier" check used
//     to drop letterbox-cropped files one tier too low (a 1920x800
//     1080p cinema cut tagged as 720p). Permissive uses
//     "h > lower-tier-canonical" instead.
//  4. quality.resolution int — last resort for pre-mediaInfo legacy
//     imports where Arr never populated mediaInfo at all.
//
// File truth wins over Radarr's release-name-derived quality bucket on
// purpose: differences between the tag and the quality profile become
// a visible signal for misclassifications the user can hunt down.
func resolutionBucket(width int, videoResolution string, mediaInfoHeight, qualityResolution int) string {
	// 1. Width int — webhook-payload path.
	if width > 0 {
		return bucketByWidth(width)
	}
	// 2. Parse width from the "WxH" string — API-path.
	if w, _ := parseResolutionWxH(videoResolution); w > 0 {
		return bucketByWidth(w)
	}
	// 3. Height with permissive thresholds — letterbox-safe fallback.
	h := mediaInfoHeight
	if h == 0 {
		h = qualityResolution
	}
	switch {
	case h > 1440:
		return "2160p"
	case h > 1080:
		return "1440p"
	case h > 720:
		return "1080p"
	case h > 480:
		return "720p"
	case h > 0:
		return "480p"
	}
	return ""
}

// codecBucket normalizes Radarr's videoCodec string into a stable label.
// Unknown codecs return "" so the caller emits no tag.
func codecBucket(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case v == "":
		return ""
	case strings.Contains(v, "x265"), strings.Contains(v, "hevc"), strings.Contains(v, "h265"), strings.Contains(v, "h.265"):
		return "h265"
	case strings.Contains(v, "x264"), strings.Contains(v, "avc"), strings.Contains(v, "h264"), strings.Contains(v, "h.264"):
		return "h264"
	case strings.Contains(v, "av1"):
		return "av1"
	case strings.Contains(v, "xvid"), strings.Contains(v, "divx"), strings.Contains(v, "mpeg-4"), strings.Contains(v, "mpeg4"):
		return "mpeg4"
	case strings.Contains(v, "mpeg-2"), strings.Contains(v, "mpeg2"):
		return "mpeg2"
	case strings.Contains(v, "vc-1"), strings.Contains(v, "vc1"):
		return "vc1"
	}
	return ""
}

// audioCodecBucket normalizes the audio codec into a stable label.
// Order matters: more-specific patterns (DTS-X, DTS-HD MA) before
// generic DTS so substring matching doesn't downgrade them.
func audioCodecBucket(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case v == "":
		return ""
	case strings.Contains(v, "truehd"):
		return "truehd"
	case strings.Contains(v, "dts-x"), strings.Contains(v, "dts:x"):
		return "dts-x"
	case strings.Contains(v, "dts-hd ma"), strings.Contains(v, "dts-hd-ma"):
		return "dts-hd-ma"
	case strings.Contains(v, "dts-hd hra"), strings.Contains(v, "dts-hd-hra"):
		return "dts-hd-hra"
	case strings.Contains(v, "dts-es"):
		return "dts-es"
	case strings.Contains(v, "dts"):
		return "dts"
	case strings.Contains(v, "eac3"), strings.Contains(v, "e-ac-3"):
		return "eac3"
	case strings.Contains(v, "ac3"), strings.Contains(v, "dolby digital"):
		return "ac3"
	case strings.Contains(v, "aac"):
		return "aac"
	case strings.Contains(v, "flac"):
		return "flac"
	case strings.Contains(v, "pcm"):
		return "pcm"
	case strings.Contains(v, "opus"):
		return "opus"
	}
	return ""
}

// audioChannelsBucket maps the float channel count to a bucket. Most
// content is 2.0 / 5.1 / 7.1; we don't try to discriminate above 7.1.
//
// Returned values use hyphen separators (5-1, 7-1) instead of "5.1" /
// "7.1" because Radarr's tag validation rejects everything outside
// `^[a-z0-9-]+$`.
func audioChannelsBucket(channels float64) string {
	switch {
	case channels >= 7.0:
		return "7-1"
	case channels >= 5.0:
		return "5-1"
	case channels >= 4.0:
		return "4-0"
	case channels >= 2.0:
		return "2-0"
	case channels >= 1.0:
		return "mono"
	}
	return ""
}

// is10Bit returns true when the file is 10-bit (or higher). Reads
// VideoBitDepth directly when present (API path); falls back to a
// whitelist of HDR rangeType tokens on webhook payloads which omit
// videoBitDepth. All listed HDR variants are 10-bit (or higher) by
// spec — 8-bit HDR doesn't ship in consumer media.
//
// Whitelist (not blacklist) on purpose: if Arr starts emitting a new
// or unknown rangeType label, we'd rather miss a niche 10-bit tag
// than false-positive on something we don't know. "DV SDR" is
// included via the dv substring — it's still 10-bit (the SDR variant
// of Dolby Vision profile 8.4 uses 10-bit BL).
func is10Bit(videoBitDepth int, videoDynamicRangeType string) bool {
	if videoBitDepth == 10 {
		return true
	}
	drt := strings.ToLower(strings.TrimSpace(videoDynamicRangeType))
	if drt == "" {
		return false
	}
	for _, tok := range tenBitRangeTypes {
		if strings.Contains(drt, tok) {
			return true
		}
	}
	return false
}

// tenBitRangeTypes — substrings matched (case-insensitive) inside
// VideoDynamicRangeType to infer 10-bit. Order doesn't matter; any
// match wins. Sourced from Radarr/Sonarr's documented rangeType
// strings ("HDR10", "HDR10Plus", "PQ", "HLG", "DV", "DV HDR10",
// "DV HDR10Plus", "DV HLG", "DV SDR" — all 10-bit by spec).
var tenBitRangeTypes = []string{"hdr10", "hdr10plus", "dv", "hlg", "pq"}

// hasAtmos checks three signals in order of authority:
//
//  1. AudioCodec string — Connect webhook payloads omit
//     audioAdditionalFeatures entirely; instead they bake Atmos into
//     the codec string itself ("EAC3 Atmos", "TrueHD Atmos"). API GET
//     responses also surface it here when present.
//  2. AudioAdditionalFeatures string — API-path dedicated field.
//     Modern Radarr/Sonarr writes "Atmos" here when MediaInfo detected
//     it at import time. Absent on webhook events.
//  3. Filename-token check on relativePath + sceneName — last-resort
//     fallback for legacy imports where neither of the above are set,
//     and for webhook releases whose audioCodec lacks the Atmos suffix
//     even though the file is Atmos. Token-based (vs substring) avoids
//     false positives — "atmos" must appear as its own word, separated
//     by . - _ or space. Won't match a movie literally titled "Atmos"
//     because the title rarely sits adjacent to the same delimiters as
//     a release-tag.
func hasAtmos(audioCodec, audioAdditionalFeatures, relativePath, sceneName string) bool {
	if strings.Contains(strings.ToLower(audioCodec), "atmos") {
		return true
	}
	if strings.Contains(strings.ToLower(audioAdditionalFeatures), "atmos") {
		return true
	}
	return hasAtmosFilenameToken(relativePath) || hasAtmosFilenameToken(sceneName)
}

func hasAtmosFilenameToken(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, t := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == ' '
	}) {
		if t == "atmos" {
			return true
		}
	}
	return false
}

// hdrBuckets returns 0..2 tags from videoDynamicRangeType. Returns
// the basic HDR bucket (sdr/pq/hdr10/hdr10plus) AND a parallel "dv"
// flag when DV is signaled — DV is its own dimension, can co-exist
// with HDR10. Examples:
//
//	""             → ["sdr"]
//	"HDR10"        → ["hdr10"]
//	"HDR10Plus"    → ["hdr10plus"]
//	"DV"           → ["pq", "dv"]      (DV implies HDR baseline)
//	"DV HDR10"     → ["hdr10", "dv"]
//	"PQ"           → ["pq"]
//	"WeirdValue"   → ["sdr"]
//
// DV detection is token-based (not substring) to avoid matching "dv"
// inside arbitrary strings like "WeirDValue".
func hdrBuckets(raw string) []string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return []string{"sdr"}
	}
	lower := strings.ToLower(v)

	hasDV := false
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '/'
	}) {
		switch token {
		case "dv", "dolby", "dolbyvision":
			hasDV = true
		}
		if hasDV {
			break
		}
	}

	var bucket string
	switch {
	case strings.Contains(lower, "hdr10plus"), strings.Contains(lower, "hdr10+"):
		bucket = "hdr10plus"
	case strings.Contains(lower, "hdr10"):
		bucket = "hdr10"
	case strings.HasPrefix(lower, "hdr"), strings.Contains(lower, "pq"):
		bucket = "pq"
	case hasDV:
		bucket = "pq"
	default:
		bucket = "sdr"
	}

	out := []string{bucket}
	if hasDV {
		out = append(out, "dv")
	}
	return out
}

// EpisodeInput pairs one episode's mediaInfo with the
// quality.quality.resolution fallback so the Aggregate*ForSeries
// helpers don't need to take parallel slices. Quality.Quality
// .Resolution is more reliable than mediaInfo.Height on legacy Sonarr
// imports (onedr0p's tag-resolution.sh ships this assumption); we keep
// it as the explicit fallback path here.
type EpisodeInput struct {
	Info              MediaInfo
	QualityResolution int
}

// MediaSummary surfaces the bucket-resolved values a single mediaInfo
// blob would produce — used by the Sonarr scan handler's per-episode
// drill-in card so the UI doesn't have to re-implement bucket logic.
// Mirror of the values that drive AudioTagsForFile + VideoTagsForFile;
// kept thin so the wire payload stays compact.
type MediaSummary struct {
	Resolution    string  // "1080p" / "" — resolutionBucket label
	VideoCodec    string  // "h265" / "" — codecBucket label
	HDR           string  // "sdr" / "hdr10" / "dv" / etc — first bucket from hdrBuckets
	VideoBitDepth int     // raw int (8 / 10). Use HasTenBit for the canonical 10-bit signal; VideoBitDepth is unreliable on webhook payloads which omit videoBitDepth.
	HasTenBit     bool    // is10Bit result — true when bitDepth==10 OR rangeType is on the 10-bit-implies whitelist. The signal video_tags.go uses to emit the "10bit" tag, surfaced here so the UI doesn't re-implement the inference.
	AudioCodec    string  // audioCodecBucket label
	AudioChannels string  // audioChannelsBucket label ("7-1" / "5-1" / etc)
	HasAtmos      bool    // hasAtmos result (incl. filename-token fallback)
}

// SummariseMediaInfo collapses one MediaInfo + qualityResolution
// fallback into the bucket strings the UI surfaces in drill-in views.
// Pure function; no I/O. Calls the same bucket helpers the engine's
// emit-side uses so the drill-in copy matches the tag emission.
func SummariseMediaInfo(mi MediaInfo, qualityResolution int) MediaSummary {
	hdr := ""
	if buckets := hdrBuckets(mi.VideoDynamicRangeType); len(buckets) > 0 {
		hdr = buckets[0]
	}
	return MediaSummary{
		Resolution:    resolutionBucket(mi.Width, mi.VideoResolution, mi.Height, qualityResolution),
		VideoCodec:    codecBucket(mi.VideoCodec),
		HDR:           hdr,
		VideoBitDepth: mi.VideoBitDepth,
		HasTenBit:     is10Bit(mi.VideoBitDepth, mi.VideoDynamicRangeType),
		AudioCodec:    audioCodecBucket(mi.AudioCodec),
		AudioChannels: audioChannelsBucket(mi.AudioChannels),
		HasAtmos:      hasAtmos(mi.AudioCodec, mi.AudioAdditionalFeatures, mi.RelativePath, mi.SceneName),
	}
}

// AggregateAudioForSeries emits series-level audio tags from a list
// of per-episode mediaInfo blobs. Audio config carries a SINGLE
// SonarrAggregation strategy that applies across the codec / channels
// / atmos sub-vocabularies (one bucket → one strategy). Caller should
// skip when cfg.Audio.Enabled == false.
//
// Returns the deduped, aggregated tag-list — already prefix-applied.
// Empty input → nil.
func AggregateAudioForSeries(eps []EpisodeInput, cfg AudioTagsConfig) []string {
	if !cfg.Audio.Enabled || len(eps) == 0 {
		return nil
	}
	perEp := make([][]string, 0, len(eps))
	for _, e := range eps {
		perEp = append(perEp, AudioTagsForFile(e.Info, cfg))
	}
	return AggregateForSeries(perEp, cfg.Audio.SonarrAggregation)
}

// AggregateVideoForSeries emits series-level video tags. Each of the
// three sub-buckets (Resolution / Codec / HDR) carries its own
// SonarrAggregation, so we evaluate per-bucket and concat — a series
// can end up with HDR-strict + Resolution-all-occurring at once. The
// HDR-strict default means a series with mixed HDR/SDR episodes won't
// claim "this series is HDR" unless every episode is.
//
// Single-bucket config-clones avoid emitting tags from disabled
// buckets at the per-episode step — keeps the perEp lists clean before
// they hit AggregateForSeries.
func AggregateVideoForSeries(eps []EpisodeInput, cfg VideoTagsConfig) []string {
	if len(eps) == 0 {
		return nil
	}
	var out []string

	bucketRun := func(b BucketConfig, only VideoTagsConfig) {
		if !b.Enabled {
			return
		}
		perEp := make([][]string, 0, len(eps))
		for _, e := range eps {
			perEp = append(perEp, VideoTagsForFile(e.Info, e.QualityResolution, only))
		}
		out = append(out, AggregateForSeries(perEp, b.SonarrAggregation)...)
	}

	bucketRun(cfg.Resolution, VideoTagsConfig{Resolution: cfg.Resolution})
	bucketRun(cfg.Codec, VideoTagsConfig{Codec: cfg.Codec})
	bucketRun(cfg.HDR, VideoTagsConfig{HDR: cfg.HDR})
	return out
}

// AggregateForSeries collapses a per-episode tag set into a series-
// level set per the chosen strategy. Used by the Sonarr scan path —
// Radarr doesn't aggregate (per-file tagging) and calls *TagsForFile
// directly. perEpisode is a slice of per-file tag-lists; the function
// returns the deduped/merged series-level list.
func AggregateForSeries(perEpisode [][]string, strategy AggregationStrategy) []string {
	if len(perEpisode) == 0 {
		return nil
	}
	switch strategy {
	case AggAllOccurring:
		return aggAllOccurring(perEpisode)
	case AggStrict:
		return aggStrict(perEpisode)
	case AggHighest:
		return aggHighest(perEpisode)
	}
	return nil
}

func aggAllOccurring(perEpisode [][]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, ep := range perEpisode {
		for _, tag := range ep {
			if _, ok := seen[tag]; !ok {
				seen[tag] = struct{}{}
				out = append(out, tag)
			}
		}
	}
	return out
}

func aggStrict(perEpisode [][]string) []string {
	if len(perEpisode) == 0 {
		return nil
	}
	candidates := make(map[string]struct{}, len(perEpisode[0]))
	for _, tag := range perEpisode[0] {
		candidates[tag] = struct{}{}
	}
	for i := 1; i < len(perEpisode); i++ {
		epTags := make(map[string]struct{}, len(perEpisode[i]))
		for _, tag := range perEpisode[i] {
			epTags[tag] = struct{}{}
		}
		for tag := range candidates {
			if _, ok := epTags[tag]; !ok {
				delete(candidates, tag)
			}
		}
	}
	var out []string
	for _, tag := range perEpisode[0] {
		if _, ok := candidates[tag]; ok {
			out = append(out, tag)
		}
	}
	return out
}

// aggHighest returns the single highest-grade tag per category, plus
// any tags the caller passed that aren't ranked. Pass single-bucket
// data only — see comment on tagRank below.
func aggHighest(perEpisode [][]string) []string {
	var bestTag string
	bestRank := -1
	unknownOrder := []string{}
	unknownSeen := make(map[string]struct{})

	for _, ep := range perEpisode {
		for _, tag := range ep {
			rank, ok := tagRank[tag]
			if !ok {
				if _, dup := unknownSeen[tag]; !dup {
					unknownSeen[tag] = struct{}{}
					unknownOrder = append(unknownOrder, tag)
				}
				continue
			}
			if rank > bestRank {
				bestRank = rank
				bestTag = tag
			}
		}
	}
	out := make([]string, 0, 1+len(unknownOrder))
	if bestTag != "" {
		out = append(out, bestTag)
	}
	out = append(out, unknownOrder...)
	return out
}

// tagRank is the highest-grade ordering used by AggHighest. Keys are
// the bucket labels WITHOUT prefix.
var tagRank = map[string]int{
	"sd":    1,
	"480p":  2,
	"720p":  3,
	"1080p": 4,
	"1440p": 5,
	"2160p": 6,
	"mpeg2": 1,
	"vc1":   1,
	"mpeg4": 2,
	"h264":  3,
	"h265":  4,
	"av1":   5,
	"mono":  1,
	"2-0":   2,
	"4-0":   3,
	"5-1":   4,
	"7-1":   5,
	"sdr":       1,
	"pq":        2,
	"hdr10":     3,
	"hdr10plus": 4,
	"dv":        5,
}
