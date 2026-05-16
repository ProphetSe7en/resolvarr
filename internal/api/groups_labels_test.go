package api

import (
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// Tests for validateLabelsMap — the per-bucket Labels override validator
// shared between TagBucket (Audio/Video) and DvDetailConfig. Covers:
//   - unknown bucket-value keys rejected
//   - empty override values rejected (omit the key instead)
//   - invalid characters rejected (Radarr ^[a-z0-9-]+$)
//   - same override value across two keys rejected (collision)
//   - well-formed input accepted

func TestValidateBucket_LabelsAccepts(t *testing.T) {
	b := core.TagBucket{
		Enabled: true,
		Labels:  map[string]string{"truehd": "premium", "5-1": "surround"},
	}
	if err := validateBucket("audio", b, audioBucketKnown); err != nil {
		t.Errorf("expected accept, got %v", err)
	}
}

func TestValidateBucket_LabelsRejectsUnknownKey(t *testing.T) {
	b := core.TagBucket{
		Labels: map[string]string{"never-was-a-value": "premium"},
	}
	err := validateBucket("audio", b, audioBucketKnown)
	if err == nil || !strings.Contains(err.Error(), "not a recognised value") {
		t.Errorf("expected unknown-key error, got %v", err)
	}
}

func TestValidateBucket_LabelsRejectsEmptyValue(t *testing.T) {
	b := core.TagBucket{
		Labels: map[string]string{"truehd": ""},
	}
	err := validateBucket("audio", b, audioBucketKnown)
	if err == nil || !strings.Contains(err.Error(), "empty override") {
		t.Errorf("expected empty-value error, got %v", err)
	}
}

func TestValidateBucket_LabelsRejectsInvalidCharacters(t *testing.T) {
	b := core.TagBucket{
		Labels: map[string]string{"truehd": "Premium Sound"}, // uppercase + space
	}
	err := validateBucket("audio", b, audioBucketKnown)
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("expected invalid-characters error, got %v", err)
	}
}

func TestValidateBucket_LabelsRejectsCollision(t *testing.T) {
	b := core.TagBucket{
		Labels: map[string]string{"truehd": "lossless", "dts-hd-ma": "lossless"},
	}
	err := validateBucket("audio", b, audioBucketKnown)
	if err == nil || !strings.Contains(err.Error(), "both map to") {
		t.Errorf("expected collision error, got %v", err)
	}
}

func TestValidateDvDetailConfig_LabelsAccepts(t *testing.T) {
	cfg := core.DvDetailConfig{
		Enabled: true,
		Labels:  map[string]string{"dvprofile8": "profile8"},
	}
	if err := validateDvDetailConfig(cfg); err != nil {
		t.Errorf("expected accept, got %v", err)
	}
}

func TestValidateDvDetailConfig_LabelsRejectsUnknownKey(t *testing.T) {
	cfg := core.DvDetailConfig{
		Labels: map[string]string{"profile9": "profile9"}, // not a canonical DV value
	}
	err := validateDvDetailConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "not a recognised value") {
		t.Errorf("expected unknown-key error, got %v", err)
	}
}

// Overlay-path validators: schedule + webhook-rule snapshots must
// enforce the same bucket-validation as the global PUT handlers.
// Without these tests the pre-Labels gap (caught by review of cc3b7a58)
// could regress.

func TestScheduleRequest_RejectsAudioTagsBadLabel(t *testing.T) {
	req := &scheduleRequest{
		Name:       "test",
		Mode:       core.JobModeAudioTags,
		InstanceID: "i1",
		AudioTags: &core.AudioTagsConfig{
			Audio: core.TagBucket{
				Enabled: true,
				Labels:  map[string]string{"truehd": "Premium Sound"}, // bad chars
			},
		},
	}
	cfg := core.Config{Instances: []core.Instance{{ID: "i1", Type: "radarr"}}}
	err := req.validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "audioTags") {
		t.Errorf("expected audioTags validation error, got %v", err)
	}
}

func TestScheduleRequest_RejectsVideoTagsBadAllowedValue(t *testing.T) {
	req := &scheduleRequest{
		Name:       "test",
		Mode:       core.JobModeVideoTags,
		InstanceID: "i1",
		VideoTags: &core.VideoTagsConfig{
			Resolution: core.TagBucket{
				Enabled:       true,
				AllowedValues: []string{"never-a-value"},
			},
		},
	}
	cfg := core.Config{Instances: []core.Instance{{ID: "i1", Type: "radarr"}}}
	err := req.validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "videoTags") {
		t.Errorf("expected videoTags validation error, got %v", err)
	}
}

func TestScheduleRequest_RejectsDvDetailLabelCollision(t *testing.T) {
	req := &scheduleRequest{
		Name:       "test",
		Mode:       core.JobModeDvDetail,
		InstanceID: "i1",
		DvDetail: &core.DvDetailConfig{
			Enabled: true,
			Labels:  map[string]string{"mel": "x", "fel": "x"},
		},
	}
	cfg := core.Config{Instances: []core.Instance{{ID: "i1", Type: "radarr"}}}
	err := req.validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "dvDetail") {
		t.Errorf("expected dvDetail validation error, got %v", err)
	}
}

func TestWebhookRuleRequest_RejectsAudioTagsBadLabel(t *testing.T) {
	req := &webhookRuleRequest{
		Name:       "test",
		InstanceID: "i1",
		AppType:    "radarr",
		Functions:  []core.WebhookFunction{core.WebhookFnTagAudio},
		AudioTags: &core.AudioTagsConfig{
			Audio: core.TagBucket{
				Enabled: true,
				Labels:  map[string]string{"truehd": "x", "dts-hd-ma": "x"}, // collision
			},
		},
	}
	cfg := core.Config{Instances: []core.Instance{{ID: "i1", Type: "radarr"}}}
	err := req.validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "audioTags") {
		t.Errorf("expected audioTags validation error, got %v", err)
	}
}
