package engine

// audio_tags.go — Audio-tags engine helpers (M4 audio split). Reads
// Radarr/Sonarr's mediaInfo.audioCodec / audioChannels /
// audioAdditionalFeatures and emits informative auto-tags for the
// audio stream. Pure functions, no I/O.
//
// Vocabulary covers three sub-categories under one bucket:
//   - codecs:   truehd / dts-x / dts-hd-ma / dts-hd-hra / dts-es /
//               dts / eac3 / ac3 / aac / flac / pcm / opus
//   - channels: 7-1 / 5-1 / 4-0 / 2-0 / mono
//   - flags:    atmos
//
// Single bucket because all three share one toggle + one prefix in
// the UI — they're conceptually "everything you'd say about the
// audio stream". Three sub-vocabularies for the per-value allow-
// list checkbox grouping.

// vocabAudioCodecs / vocabAudioChannels / vocabAudioFlags are the
// canonical value lists. Single source of truth — AudioVocabulary
// returns them and AllPossibleAudioTags / AudioTagsForFile both
// iterate them.
var (
	vocabAudioCodecs   = []string{"truehd", "dts-x", "dts-hd-ma", "dts-hd-hra", "dts-es", "dts", "eac3", "ac3", "aac", "flac", "pcm", "opus"}
	vocabAudioChannels = []string{"7-1", "5-1", "4-0", "2-0", "mono"}
	vocabAudioFlags    = []string{"atmos"}
)

// AudioVocabulary returns the three canonical sub-vocabularies the
// Audio bucket can emit. UI uses this for the per-value allow-list
// checkbox matrix; API uses it for input validation.
//
// Returns slice copies so callers can't mutate the package-level
// state.
func AudioVocabulary() (codecs, channels, flags []string) {
	cp := func(s []string) []string { return append([]string(nil), s...) }
	return cp(vocabAudioCodecs), cp(vocabAudioChannels), cp(vocabAudioFlags)
}

// AudioTagsConfig is the engine-side config the Audio-tags scan
// emits against. Mirror of core.AudioTagsConfig — the persistent
// shape with JSON tags lives in core/config.go; this is its
// engine-facing twin so engine never imports core.
type AudioTagsConfig struct {
	Audio BucketConfig
}

// AudioTagsForFile returns the audio-related tag labels for one
// file's mediaInfo. Pure function; no side effects. Empty slice
// when the bucket is disabled, audio fields are missing, or every
// value is filtered out.
func AudioTagsForFile(mi MediaInfo, cfg AudioTagsConfig) []string {
	if !cfg.Audio.Enabled {
		return nil
	}
	var out []string
	if tag := audioCodecBucket(mi.AudioCodec); tag != "" && cfg.Audio.allowed(tag) {
		out = append(out, cfg.Audio.Prefix+cfg.Audio.label(tag))
	}
	if tag := audioChannelsBucket(mi.AudioChannels); tag != "" && cfg.Audio.allowed(tag) {
		out = append(out, cfg.Audio.Prefix+cfg.Audio.label(tag))
	}
	if hasAtmos(mi.AudioCodec, mi.AudioAdditionalFeatures, mi.RelativePath, mi.SceneName) && cfg.Audio.allowed("atmos") {
		out = append(out, cfg.Audio.Prefix+cfg.Audio.label("atmos"))
	}
	return out
}

// AllPossibleAudioTags returns the universe of audio tags this
// configuration could ever emit, regardless of Enabled or
// AllowedValues. Cleanup safety-bound — see the parallel
// AllPossibleVideoTags / AllPossibleDvDetailTags for the reasoning.
//
// Map shape: prefixed-tag → "audio" (single bucket name).
func AllPossibleAudioTags(cfg AudioTagsConfig) map[string]string {
	out := make(map[string]string)
	// A disabled bucket is hands-off: nothing enters the managed set, so
	// Remove orphaned tags never strips audio tags the user switched off.
	if !cfg.Audio.Enabled {
		return out
	}
	emit := func(values []string) {
		for _, v := range values {
			out[cfg.Audio.Prefix+cfg.Audio.label(v)] = "audio"
		}
	}
	emit(vocabAudioCodecs)
	emit(vocabAudioChannels)
	emit(vocabAudioFlags)
	return out
}

// EmittableAudioTags returns only the labels this configuration
// would emit RIGHT NOW given Enabled + AllowedValues. Companion to
// AllPossibleAudioTags; used as the cleanup bound when the user
// opts OUT of orphan removal.
func EmittableAudioTags(cfg AudioTagsConfig) map[string]string {
	out := make(map[string]string)
	if !cfg.Audio.Enabled {
		return out
	}
	emit := func(values []string) {
		for _, v := range values {
			if !cfg.Audio.allowed(v) {
				continue
			}
			out[cfg.Audio.Prefix+cfg.Audio.label(v)] = "audio"
		}
	}
	emit(vocabAudioCodecs)
	emit(vocabAudioChannels)
	emit(vocabAudioFlags)
	return out
}
