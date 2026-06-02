package arr

import (
	"encoding/json"
	"testing"
)

// TestMediaInfo_UnmarshalWebhookShape verifies the Connect-webhook
// payload shape decodes correctly. Webhook payloads ship width + height
// as separate ints, with NO "resolution" string. Real samples lifted
// from production resolvarr's webhook-events.json:
//   - Hokum 2160p HDR10+: width=3840, height=1600 (cinematic 2.40:1 4K)
//   - Hokum 1080p h264:   width=1920, height=800
//   - Horizon Saga 1:     width=3840, height=2076 (4K 1.85:1, just under
//     the canonical 2160 height — strict "h >= 2160" rejected it as
//     2160p before this fix)
//
// If anyone renames the json tags on Width / Height the silent decode-
// drop returns and every webhook-driven Tag Video fire reverts to the
// height-permissive fallback — losing the cleanest signal.
func TestMediaInfo_UnmarshalWebhookShape(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantW, wantH int
	}{
		{"Hokum 2160p HDR10+ webhook payload", `{"width":3840,"height":1600,"videoCodec":"h265"}`, 3840, 1600},
		{"Hokum 1080p h264 webhook payload", `{"width":1920,"height":800,"videoCodec":"h264"}`, 1920, 800},
		{"Horizon 4K 1.85:1 webhook payload", `{"width":3840,"height":2076,"videoCodec":"h265"}`, 3840, 2076},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mi MediaInfo
			if err := json.Unmarshal([]byte(tc.raw), &mi); err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if mi.Width != tc.wantW || mi.Height != tc.wantH {
				t.Errorf("Width=%d Height=%d, want %d/%d", mi.Width, mi.Height, tc.wantW, tc.wantH)
			}
		})
	}
}

// TestMediaInfo_UnmarshalResolutionField pins the JSON key for the
// "WxH" video-resolution string. Verified against live Radarr 6.x and
// Sonarr API responses: the field name is bare "resolution", NOT
// "videoResolution". If anyone renames the json tag the silent
// decode-drop returns — every file would fall through to the Height-
// permissive fallback path and the width-based bucket logic the user
// added this for stops working.
func TestMediaInfo_UnmarshalResolutionField(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		// Real Radarr 6.x sample (4K).
		{"radarr 4K", `{"resolution":"3840x2160","height":2160}`, "3840x2160"},
		// Real Sonarr sample (1080p 2:1 episode).
		{"sonarr 1080p 2:1", `{"resolution":"1920x960","height":960}`, "1920x960"},
		// Cinematic 4K crop — the case the bug fix targets.
		{"4K cinematic crop", `{"resolution":"3840x1600","height":1600}`, "3840x1600"},
		// Legacy / pre-mediaInfo imports: field absent. Height stays the
		// only signal; engine falls back to permissive height thresholds.
		{"field absent", `{"height":1080}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mi MediaInfo
			if err := json.Unmarshal([]byte(tc.raw), &mi); err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if mi.VideoResolution != tc.want {
				t.Errorf("VideoResolution = %q, want %q", mi.VideoResolution, tc.want)
			}
		})
	}
}

// TestMediaInfo_RejectsWrongJSONKey is a negative test that fails loud
// if someone "fixes" the json tag back to videoResolution (or any other
// name). Belt-and-braces with the positive test above.
func TestMediaInfo_RejectsWrongJSONKey(t *testing.T) {
	// Payload uses videoResolution — what the field WOULD be named if
	// someone followed the camelCase pattern the other mediaInfo fields
	// use. Both Arr APIs return bare "resolution" though, so this
	// payload must produce an empty VideoResolution.
	raw := []byte(`{"videoResolution":"3840x2160","height":2160}`)
	var mi MediaInfo
	if err := json.Unmarshal(raw, &mi); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if mi.VideoResolution != "" {
		t.Errorf(`VideoResolution = %q after decoding a "videoResolution"-keyed payload; want "" so this test fails if the json tag drifts`, mi.VideoResolution)
	}
}
