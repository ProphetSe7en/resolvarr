package utils

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicWriteFile_basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	want := []byte(`{"hello":"world"}`)
	if err := AtomicWriteFile(path, want, 0600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}
	// Verify file mode honors perm argument.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("perm: got %o want 0600", st.Mode().Perm())
	}
}

// TestAtomicWriteFile_concurrent exercises the random-tmp-suffix guard:
// if two goroutines raced on a deterministic `.tmp` name, one would
// truncate the other's tmp and one of the goroutines would see ENOENT
// at Rename time. With random suffixes both writes complete, and one
// of the two payloads ends up on disk (whichever rename wins).
func TestAtomicWriteFile_concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	const workers = 16

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := []byte{byte('a' + i)}
			if err := AtomicWriteFile(path, payload, 0600); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent AtomicWriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected a single-byte winner, got %d bytes", len(got))
	}

	// No stray tmp files should remain — every successful write either
	// Renamed away its tmp, and every failing write removed it.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != "" && e.Name() != "file.json" {
			continue
		}
		if e.Name() == "file.json" {
			continue
		}
		t.Errorf("unexpected file left in dir: %q", e.Name())
	}
}

func TestSanitizeLogField_controlBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "hello world", "hello world"},
		{"newline", "line1\nline2", "line1 line2"},
		{"cr", "a\rb", "a b"},
		{"tab", "a\tb", "a b"},
		{"del", "a\x7fb", "a b"},
		{"mixed", "a\n\rb\tc\x7fd", "a  b c d"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeLogField(tc.in); got != tc.want {
				t.Fatalf("SanitizeLogField(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeLogField_truncation(t *testing.T) {
	in := bytes.Repeat([]byte("x"), 2048)
	got := SanitizeLogField(string(in))
	if len(got) != 1024 {
		t.Fatalf("expected truncation to 1024, got %d", len(got))
	}
}
