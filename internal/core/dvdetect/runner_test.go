package dvdetect

import (
	"context"
	"errors"
	"testing"
)

func TestRunner_Stub_Success(t *testing.T) {
	r := Runner{
		Stub: func(path string) (string, bool, error) {
			return "Profile: 7.6\nFEL\nCM v4.0\n", true, nil
		},
	}
	d, ok, err := r.Detect(context.Background(), "/fake/movie.mkv")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d.Profile != 7 || d.Layer != "fel" || d.CMVersion != 4 {
		t.Errorf("detail = %+v, want Profile=7 Layer=fel CMVersion=4", d)
	}
}

func TestRunner_Stub_NoRpu(t *testing.T) {
	// "API said DV but stream has no RPU" — stub returns ok=false
	// without an error. Caller treats this as "fall through to no-dv".
	r := Runner{
		Stub: func(path string) (string, bool, error) {
			return "", false, nil
		},
	}
	d, ok, err := r.Detect(context.Background(), "/fake/movie.mkv")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if ok {
		t.Error("expected ok=false (no RPU)")
	}
	if d.Profile != 0 {
		t.Errorf("detail unexpectedly populated: %+v", d)
	}
}

func TestRunner_Stub_ExtractionFailed(t *testing.T) {
	// Hard error path — caller surfaces this as a per-file warning
	// rather than tagging.
	wantErr := errors.New("ffmpeg: invalid input")
	r := Runner{
		Stub: func(path string) (string, bool, error) {
			return "", false, wantErr
		},
	}
	_, _, err := r.Detect(context.Background(), "/fake/movie.mkv")
	if !errors.Is(err, wantErr) {
		t.Errorf("Detect err = %v, want wraps %v", err, wantErr)
	}
}

func TestRunner_NoStubNoBins_ReturnsToolsMissing(t *testing.T) {
	r := Runner{} // no Stub, no DvBin/FfBin
	_, _, err := r.Detect(context.Background(), "/fake/movie.mkv")
	if !errors.Is(err, ErrToolsMissing) {
		t.Errorf("Detect err = %v, want ErrToolsMissing", err)
	}
}

func TestRunner_BinsSetButMissing(t *testing.T) {
	r := Runner{
		DvBin: "/no/such/dovi_tool",
		FfBin: "/no/such/ffmpeg",
	}
	_, _, err := r.Detect(context.Background(), "/fake/movie.mkv")
	if !errors.Is(err, ErrToolsMissing) {
		t.Errorf("Detect err = %v, want ErrToolsMissing", err)
	}
}
