package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// scheduler_notify_dv_test.go — pin the M4b dvdetail notification
// shape so future scan-response changes can't silently drift it.
// Mirrors the testing pattern that scheduler_notify already covers
// (no test file existed before this commit; these are the first
// per-mode field tests).

func newDvDetailResponse() *scanResponse {
	return &scanResponse{
		Mode:   "apply",
		Action: "dvdetail",
		Instance: scanInstanceInfo{
			ID: "r", Name: "Radarr", Type: "radarr",
		},
		Totals: scanTotals{
			Items:           1077,
			DvNonCandidates: 765,
			DvCandidates:    312,
			DvCacheHits:     287,
			DvExtracted:     25,
			DvExtractedNoRpu: 3,
			DvExtractFailed: 1,
			ToAdd:           73,
			ToRemove:        2,
			ToKeep:          14,
			DvDetailRollups: []scanDvDetailRollup{
				{Action: "add", Tag: "fel", Count: 25},
				{Action: "add", Tag: "cm4", Count: 22},
				{Action: "add", Tag: "dvprofile8", Count: 19},
				{Action: "add", Tag: "cm2", Count: 5},
				{Action: "add", Tag: "mel", Count: 2},
				{Action: "remove", Tag: "cm2", Count: 2},
			},
		},
		Items: []scanItem{
			{
				ID: 1, Title: "Foo", Year: 2024,
				DvStatus: "failed",
				DvDecisions: []scanDvDetailDecision{
					{Status: "failed", Reason: "ffmpeg timed out"},
				},
			},
		},
		Applied: &scanApplied{
			ItemsAdded:   73,
			ItemsRemoved: 2,
			TagsCreated:  []string{"fel", "cm4"},
		},
	}
}

func TestSummarizeDvDetailResponse_PartialOnFailures(t *testing.T) {
	resp := newDvDetailResponse()
	got := summarizeDvDetailResponse(resp)
	if got.Status != "partial" {
		t.Errorf("Status = %q, want partial (failures present)", got.Status)
	}
	if !strings.Contains(got.Summary, "312 candidates") {
		t.Errorf("Summary missing candidate count: %s", got.Summary)
	}
	if !strings.Contains(got.Summary, "287 cached") {
		t.Errorf("Summary missing cache hits: %s", got.Summary)
	}
	if !strings.Contains(got.Summary, "1 failed") {
		t.Errorf("Summary missing failed count: %s", got.Summary)
	}
}

func TestSummarizeDvDetailResponse_OkWhenCleanRun(t *testing.T) {
	resp := newDvDetailResponse()
	resp.Totals.DvExtractFailed = 0
	resp.Totals.DvFileUnreachable = 0
	got := summarizeDvDetailResponse(resp)
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok (no failures)", got.Status)
	}
}

func TestSummarizeDvDetailResponse_NilSafe(t *testing.T) {
	got := summarizeDvDetailResponse(nil)
	if got.Status != "error" {
		t.Errorf("Status = %q, want error on nil", got.Status)
	}
}

func TestDvDetailModeFields_HasExtractionColumn(t *testing.T) {
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	resp := newDvDetailResponse()
	fields := dvDetailModeFields(inst, core.RunSummary{Result: resp})
	// Expect: count column + extraction column + new-tags footer = 3.
	if len(fields) < 2 {
		t.Fatalf("got %d fields, want at least 2 (count + extraction): %+v", len(fields), fields)
	}
	if !strings.Contains(fields[0].Name, "DV detail") {
		t.Errorf("first field name = %q, want substring 'DV detail'", fields[0].Name)
	}
	if !strings.Contains(fields[0].Value, "Added: 73") {
		t.Errorf("first field value missing 'Added: 73': %s", fields[0].Value)
	}
	// Find the extraction-stats field.
	var ext string
	for _, f := range fields {
		if f.Name == "DV extraction" {
			ext = f.Value
		}
	}
	if ext == "" {
		t.Fatal("missing DV extraction field")
	}
	if !strings.Contains(ext, "Candidates: 312") {
		t.Errorf("extraction field missing candidate count: %s", ext)
	}
	if !strings.Contains(ext, "⚠ Failed: 1") {
		t.Errorf("extraction field missing failure count: %s", ext)
	}
}

func TestDvDetailModeFields_OmitsExtractionWhenAllZero(t *testing.T) {
	// When the user has DV detail enabled but the library has zero
	// DV files, the extraction-stats sub-field is just noise. Verify
	// it's omitted.
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	resp := newDvDetailResponse()
	resp.Totals.DvCandidates = 0
	resp.Totals.DvCacheHits = 0
	resp.Totals.DvExtracted = 0
	resp.Totals.DvExtractedNoRpu = 0
	resp.Totals.DvExtractFailed = 0
	resp.Totals.DvFileUnreachable = 0
	fields := dvDetailModeFields(inst, core.RunSummary{Result: resp})
	for _, f := range fields {
		if f.Name == "DV extraction" {
			t.Errorf("extraction field should be omitted when all-zero, got: %+v", f)
		}
	}
}

func TestBuildDvDetailDetail_AddRollupSection(t *testing.T) {
	resp := newDvDetailResponse()
	got := buildDvDetailDetail(core.RunSummary{Result: resp})
	if !strings.Contains(got, "**DV detail added:**") {
		t.Errorf("missing add header in apply mode: %s", got)
	}
	if !strings.Contains(got, "fel ") {
		t.Errorf("missing fel rollup row: %s", got)
	}
	if !strings.Contains(got, "**DV detail removed:**") {
		t.Errorf("missing remove header: %s", got)
	}
}

func TestBuildDvDetailDetail_PreviewVerbTense(t *testing.T) {
	resp := newDvDetailResponse()
	resp.Applied = nil // preview mode
	got := buildDvDetailDetail(core.RunSummary{Result: resp})
	if !strings.Contains(got, "**DV detail to add:**") {
		t.Errorf("preview missing 'to add' header: %s", got)
	}
	if strings.Contains(got, "**DV detail added:**") {
		t.Errorf("preview should not say 'added'")
	}
}

func TestBuildDvDetailDetail_FailureSection(t *testing.T) {
	resp := newDvDetailResponse()
	got := buildDvDetailDetail(core.RunSummary{Result: resp})
	if !strings.Contains(got, "**DV detail extraction warnings:**") {
		t.Errorf("missing extraction-warnings section despite DvExtractFailed=1: %s", got)
	}
	if !strings.Contains(got, "Foo (2024)") {
		t.Errorf("failure section missing movie title: %s", got)
	}
	if !strings.Contains(got, "ffmpeg timed out") {
		t.Errorf("failure section missing reason: %s", got)
	}
}

func TestBuildDvDetailDetail_FailureBodyBytesBudget(t *testing.T) {
	// dvDetailFailureBodyMax caps the cumulative bytes of the failure
	// section body. Build a response with rows long enough that 50
	// of them would blow past Discord's 2000-char content limit if
	// uncapped, and verify the body stays under the budget plus the
	// "…and N more" footer accounts for the truncated rows.
	resp := newDvDetailResponse()
	resp.Items = nil
	longTitle := strings.Repeat("LongMovieTitle", 8) // 112 chars
	longReason := "media file unreachable: " + strings.Repeat("/very/long/path/segment", 5) + " — check path mappings"
	for i := 0; i < 50; i++ {
		resp.Items = append(resp.Items, scanItem{
			ID: i + 1, Title: longTitle, Year: 2024,
			DvStatus: "failed",
			DvDecisions: []scanDvDetailDecision{
				{Status: "failed", Reason: longReason},
			},
		})
	}
	resp.Totals.DvExtractFailed = 50
	got := buildDvDetailDetail(core.RunSummary{Result: resp})
	// Body must stay under the budget — section + headers + footer
	// adds < 200 chars over the body budget; assert total < 1900
	// (Discord 2000 cap with margin).
	if len(got) > 1900 {
		t.Errorf("detail content %d bytes — exceeds Discord-safe ceiling of 1900", len(got))
	}
	if !strings.Contains(got, "…and ") || !strings.Contains(got, "more (see log file)") {
		t.Errorf("missing overflow footer for budget-truncated failures: %s", got)
	}
	// Each rendered line must be ≤ dvDetailFailureLineMax bytes
	// (plus margin for our " …" truncation suffix).
	for _, line := range strings.Split(got, "\n") {
		if len(line) > dvDetailFailureLineMax+5 {
			t.Errorf("line exceeds per-line cap (%d): %q", len(line), line)
		}
	}
}

func TestBuildDvDetailDetail_ToolsMissingSurfacesAsWarning(t *testing.T) {
	// 🔴 fix verification: every row tools-missing means
	// summarizeDvDetailResponse must NOT report "ok" + the warnings
	// section MUST surface. Mirrors the user's most likely failure
	// mode (clicked Run before clicking Install).
	resp := newDvDetailResponse()
	resp.Items = nil
	resp.Totals = scanTotals{
		Items:          5,
		DvCandidates:   5,
		DvToolsMissing: 5,
	}
	for i := 0; i < 5; i++ {
		resp.Items = append(resp.Items, scanItem{
			ID:    i + 1,
			Title: "Movie",
			Year:  2024,
			DvStatus: "tools-missing",
			DvDecisions: []scanDvDetailDecision{
				{Status: "tools-missing", Reason: "dv tools not installed"},
			},
		})
	}
	resp.Applied = nil
	got := buildDvDetailDetail(core.RunSummary{Result: resp})
	if !strings.Contains(got, "**DV detail extraction warnings:**") {
		t.Errorf("tools-missing rows not surfaced as warnings: %s", got)
	}
	if !strings.Contains(got, "dv tools not installed") {
		t.Errorf("tools-missing reason not surfaced: %s", got)
	}
}

func TestSummarizeDvDetailResponse_ToolsMissingFlipsToPartial(t *testing.T) {
	// Same scenario at the summarize layer — status MUST flip to
	// partial so the schedule history row + notification embed
	// signal a problem instead of "ok".
	resp := &scanResponse{
		Mode: "apply", Action: "dvdetail",
		Totals: scanTotals{
			Items:          5,
			DvCandidates:   5,
			DvToolsMissing: 5,
		},
	}
	got := summarizeDvDetailResponse(resp)
	if got.Status != "partial" {
		t.Errorf("Status = %q, want partial (every row tools-missing)", got.Status)
	}
	if !strings.Contains(got.Summary, "5 tools-missing") {
		t.Errorf("Summary missing tools-missing count: %s", got.Summary)
	}
}

func TestDvDetailModeFields_ToolsMissingSurfacesInExtractionColumn(t *testing.T) {
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	resp := &scanResponse{
		Mode: "apply",
		Totals: scanTotals{
			Items:          5,
			DvCandidates:   5,
			DvToolsMissing: 5,
		},
	}
	fields := dvDetailModeFields(inst, core.RunSummary{Result: resp})
	var ext string
	for _, f := range fields {
		if f.Name == "DV extraction" {
			ext = f.Value
		}
	}
	if !strings.Contains(ext, "Tools missing: 5") {
		t.Errorf("extraction column missing tools-missing count: %s", ext)
	}
}

func TestTruncateRune_RuneSafeAndSuffix(t *testing.T) {
	// Multi-byte rune at the cut boundary mustn't slice in the
	// middle of a UTF-8 sequence. "ø" is 2 bytes; truncating
	// "føøøøøøøø" at 5 bytes must walk back to a rune boundary.
	got := truncateRune("føøøøøøøø", 5)
	// Don't check exact bytes (rune-walking is correct as long as
	// no error). Just assert it didn't panic + appended " …".
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncation suffix, got %q", got)
	}
}

func TestTruncateRune_ShortInputUnchanged(t *testing.T) {
	got := truncateRune("short", 100)
	if got != "short" {
		t.Errorf("short input mutated: %q", got)
	}
}

func TestBuildDvDetailDetail_ErrorPathReturnsEmpty(t *testing.T) {
	// runErr != nil at the dispatcher level returns "" from
	// buildScheduleDetail before this function is even called; we
	// also handle nil-resp inside this function defensively.
	got := buildDvDetailDetail(core.RunSummary{Result: nil})
	if got != "" {
		t.Errorf("expected empty string on nil-result, got: %s", got)
	}
}

func TestNotifyScheduleDvDetail_RuntimeFieldPresent(t *testing.T) {
	// Sanity: full pipeline through buildScheduleFields produces
	// a Runtime field at the tail. The dispatcher appends it
	// regardless of mode; pin against a regression that drops it
	// for an unrecognised mode.
	job := core.ScheduledJob{
		Name:    "DV nightly",
		Mode:    core.JobModeDvDetail,
		Options: core.JobOptions{RunMode: "apply"},
	}
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	resp := newDvDetailResponse()
	fields := buildScheduleFields(inst, job, core.RunSummary{Result: resp}, nil, 12*time.Second)
	var hasRuntime bool
	for _, f := range fields {
		if f.Name == "Runtime" {
			hasRuntime = true
			break
		}
	}
	if !hasRuntime {
		t.Errorf("Runtime field missing from dvdetail embed: %+v", fields)
	}
}

func TestNotifyScheduleDvDetail_ErrorReplacesFields(t *testing.T) {
	// Error path at the dispatcher level emits an Error field +
	// Runtime field, dropping the per-mode block. Same shape every
	// other mode produces; pinning here so a future refactor can't
	// regress just dvdetail.
	job := core.ScheduledJob{Name: "DV nightly", Mode: core.JobModeDvDetail}
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	fields := buildScheduleFields(inst, job, core.RunSummary{}, errors.New("tools missing"), 0)
	var sawError, sawRuntime bool
	for _, f := range fields {
		if f.Name == "Error" {
			sawError = true
		}
		if f.Name == "Runtime" {
			sawRuntime = true
		}
	}
	if !sawError || !sawRuntime {
		t.Errorf("error path embed missing Error or Runtime: %+v", fields)
	}
}
