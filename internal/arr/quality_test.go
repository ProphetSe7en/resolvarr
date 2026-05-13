package arr

import (
	"encoding/json"
	"testing"
)

// TestQuality_UnmarshalStructForm verifies the Arr API shape decodes
// as before: { "quality": { "id": 7, "name": "WEBDL-2160p",
// "resolution": 2160 } }. Locks back-compat with every Arr API call.
func TestQuality_UnmarshalStructForm(t *testing.T) {
	data := []byte(`{"quality":{"id":7,"name":"WEBDL-2160p","resolution":2160}}`)
	var q Quality
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatalf("struct form decode failed: %v", err)
	}
	if q.Quality.ID != 7 {
		t.Errorf("ID = %d, want 7", q.Quality.ID)
	}
	if q.Quality.Name != "WEBDL-2160p" {
		t.Errorf("Name = %q, want WEBDL-2160p", q.Quality.Name)
	}
	if q.Quality.Resolution != 2160 {
		t.Errorf("Resolution = %d, want 2160", q.Quality.Resolution)
	}
}

// TestQuality_UnmarshalStringForm verifies the Webhook Connect shape
// decodes into QualityValue.Name with empty ID/Resolution. This is
// the bug that bit Thor's grab+import — Radarr sends "WEBDL-2160p" as
// a flat string and the old struct-only decoder errored out, killing
// every function in the rule.
func TestQuality_UnmarshalStringForm(t *testing.T) {
	data := []byte(`"WEBDL-2160p"`)
	var q Quality
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatalf("string form decode failed: %v", err)
	}
	if q.Quality.Name != "WEBDL-2160p" {
		t.Errorf("Name = %q, want WEBDL-2160p", q.Quality.Name)
	}
	if q.Quality.ID != 0 {
		t.Errorf("ID = %d, want 0 (string form has no ID)", q.Quality.ID)
	}
	if q.Quality.Resolution != 0 {
		t.Errorf("Resolution = %d, want 0 (string form has no resolution — engine falls back to mediaInfo.height)", q.Quality.Resolution)
	}
}

// TestQuality_UnmarshalEmbedded verifies the realistic case: a
// MovieFile field carrying the webhook payload's flat quality. Without
// the custom unmarshaler this errored out with "cannot unmarshal
// string into Go struct field MovieFile.movieFile.quality of type
// arr.Quality" — the exact error that killed Thor's import.
func TestQuality_UnmarshalEmbedded(t *testing.T) {
	data := []byte(`{
		"id": 7376,
		"relativePath": "Thor.mkv",
		"quality": "WEBDL-2160p",
		"releaseGroup": "APEX",
		"sceneName": "Thor 2011 2160p MA WEB-DL TrueHD 7.1 Atmos HDR H.265-APEX",
		"size": 25804502047
	}`)
	var mf MovieFile
	if err := json.Unmarshal(data, &mf); err != nil {
		t.Fatalf("MovieFile decode failed: %v", err)
	}
	if mf.Quality == nil {
		t.Fatal("Quality is nil — pointer dropped")
	}
	if mf.Quality.Quality.Name != "WEBDL-2160p" {
		t.Errorf("Quality.Name = %q, want WEBDL-2160p", mf.Quality.Quality.Name)
	}
	if mf.ReleaseGroup != "APEX" {
		t.Errorf("ReleaseGroup = %q, want APEX", mf.ReleaseGroup)
	}
}

// TestQuality_UnmarshalNull verifies that null / missing quality is
// safe — callers already handle the zero value.
func TestQuality_UnmarshalNull(t *testing.T) {
	data := []byte(`null`)
	var q Quality
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatalf("null decode failed: %v", err)
	}
	if q.Quality.Name != "" || q.Quality.ID != 0 || q.Quality.Resolution != 0 {
		t.Errorf("null should decode to zero value, got %+v", q)
	}
}
