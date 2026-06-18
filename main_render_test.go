package main

import (
	"io/fs"
	"strings"
	"testing"
)

// TestRenderIndex verifies the html/template migration: index.html + partials
// parse and execute, every {{template}} action runs (no leftover markers), each
// partial is inlined, and the index body is present and non-trivial.
func TestRenderIndex(t *testing.T) {
	sub, err := fs.Sub(staticFiles, "ui/static")
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderIndex(sub, indexData{Version: "test"})
	if err != nil {
		t.Fatalf("renderIndex failed: %v", err)
	}
	s := string(out)

	for _, leftover := range []string{"{{template", "{{ template", "#include"} {
		if strings.Contains(s, leftover) {
			t.Errorf("leftover %q in rendered output (template not executed)", leftover)
		}
	}
	// Each partial inlined (a token unique to that partial).
	for _, want := range []string{
		"dismissRecoverResults", // partials/recover-result-panel.html
		"dismissTagResults",     // partials/tag-result-panel.html
		"cleanupResults",        // partials/cleanup-result-panel.html
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered output missing partial token %q", want)
		}
	}
	// Index body present.
	if !strings.Contains(s, "profileByTagWizard") {
		t.Error("rendered output missing index body (profileByTagWizard)")
	}
	if len(out) < 200_000 {
		t.Errorf("rendered output suspiciously small: %d bytes", len(out))
	}
}
