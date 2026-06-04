package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"resolvarr/internal/core"
)

func TestSplitQbitTags(t *testing.T) {
	cases := map[string][]string{
		"":                  nil,
		"   ":               nil,
		"a":                 {"a"},
		"a,b,c":             {"a", "b", "c"},
		" a , b , c ":       {"a", "b", "c"},
		"a,,b":              {"a", "b"},
		"S01,S01E05,custom": {"S01", "S01E05", "custom"},
	}
	for in, want := range cases {
		got := splitQbitTags(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("splitQbitTags(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestHasAllTags(t *testing.T) {
	cases := []struct {
		name    string
		current []string
		wanted  []string
		want    bool
	}{
		{"both present, exact case",
			[]string{"S01", "S01E05"}, []string{"S01", "S01E05"}, true},
		{"both present, mixed case",
			[]string{"s01", "S01e05"}, []string{"S01", "s01E05"}, true},
		{"current has extra tags — still all wanted present",
			[]string{"S01", "S01E05", "favorite"}, []string{"S01"}, true},
		{"missing one",
			[]string{"S01"}, []string{"S01", "S01E05"}, false},
		{"current empty",
			nil, []string{"S01"}, false},
		{"wanted empty — vacuously true",
			[]string{"x"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasAllTags(c.current, c.wanted); got != c.want {
				t.Errorf("hasAllTags(%v, %v) = %v, want %v", c.current, c.wanted, got, c.want)
			}
		})
	}
}

// fakeQbitServer stands in for a real qBittorrent Web API. Exposes a
// fixed list of torrents from /api/v2/torrents/info and records each
// /api/v2/torrents/addTags call so the test can assert the apply
// loop only touched the hashes in the SelectedHashes filter. Login
// is short-circuited via empty creds (Username == Password == "")
// — the real Client skips the /auth/login call in that mode, which
// matches the LocalHostAuth-disabled qBit setup.
type fakeQbitServer struct {
	srv      *httptest.Server
	addCalls []addTagsCall
	mu       sync.Mutex
}

type addTagsCall struct {
	Hashes []string
	Tags   []string
}

func newFakeQbitServer(t *testing.T, listJSON string) *fakeQbitServer {
	t.Helper()
	f := &fakeQbitServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listJSON))
	})
	mux.HandleFunc("/api/v2/torrents/addTags", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		f.mu.Lock()
		f.addCalls = append(f.addCalls, addTagsCall{
			Hashes: strings.Split(vals.Get("hashes"), "|"),
			Tags:   strings.Split(vals.Get("tags"), ","),
		})
		f.mu.Unlock()
		w.WriteHeader(200)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeQbitServer) calls() []addTagsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]addTagsCall, len(f.addCalls))
	copy(out, f.addCalls)
	return out
}

// TestQbitSeBacklog_SelectedHashesGatesApply — verifies that when the
// apply request supplies SelectedHashes, only torrents in that
// allowlist receive AddTags calls (untaggable / unselected torrents
// are still included in the preview Items[] but skipped by the apply
// loop). This is the per-row checkbox path the UI relies on; without
// the gate apply would tag every taggable item regardless of UI
// selection.
func TestQbitSeBacklog_SelectedHashesGatesApply(t *testing.T) {
	// Three torrents:
	//   AAA — Episode (S01E05) → would be tagged "Episode"
	//   BBB — Episode (S02E01) → would be tagged "Episode"
	//   CCC — Season  (S03)    → would be tagged "Season"
	listJSON := `[
		{"hash":"aaa111","name":"Show.Name.S01E05.Title.1080p.WEB-DL.x264-GROUP","category":"sonarr","tags":""},
		{"hash":"bbb222","name":"Other.Show.S02E01.1080p.WEB-DL.x264-OTHER","category":"sonarr","tags":""},
		{"hash":"ccc333","name":"Third.Show.S03.COMPLETE.1080p.WEB-DL.x264-FOO","category":"sonarr","tags":""}
	]`
	fake := newFakeQbitServer(t, listJSON)

	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(cfg *core.Config) {
		cfg.QbitInstances = []core.QbitInstance{{
			ID:   "qbt1",
			Name: "qbt-main",
			URL:  fake.srv.URL,
			// Empty creds: skips /auth/login in the real Client (matches
			// qBit's LocalHostAuth-disabled setup).
		}}
		cfg.WebhookRules = []core.WebhookRule{{
			ID:         "rule-se",
			Name:       "Sonarr S/E backlog",
			Enabled:    true,
			InstanceID: "sonarr1",
			AppType:    "sonarr",
			Functions:  []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID:   "qbt1",
				EpisodeEnabled:   true,
				EpisodeTag:       "Episode",
				SeasonEnabled:    true,
				SeasonTag:        "Season",
				UnmatchedEnabled: false,
			},
		}}
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	s := &Server{App: &core.App{Config: store}}

	// Apply with SelectedHashes that only includes AAA + CCC. BBB
	// should remain in the preview Items[] but get NO AddTags call.
	resp, apiErr := s.runQbitSeBacklogScan(context.Background(), qbitSeBacklogScanRequest{
		RuleID:         "rule-se",
		SelectedHashes: []string{"aaa111", "ccc333"},
	}, true)
	if apiErr != nil {
		t.Fatalf("runQbitSeBacklogScan: %+v", apiErr)
	}
	applyResp, ok := resp.(qbitSeBacklogApplyResponse)
	if !ok {
		t.Fatalf("expected qbitSeBacklogApplyResponse, got %T", resp)
	}
	if applyResp.Applied != 2 {
		t.Errorf("Applied = %d, want 2 (AAA + CCC; BBB excluded by SelectedHashes)", applyResp.Applied)
	}
	if applyResp.Failed != 0 {
		t.Errorf("Failed = %d, want 0; errors=%v", applyResp.Failed, applyResp.Errors)
	}
	if len(applyResp.Items) != 3 {
		t.Errorf("Items length = %d, want 3 (preview includes every torrent regardless of selection)", len(applyResp.Items))
	}

	// Verify exactly which hashes hit the qBit AddTags endpoint —
	// the BBB torrent must NOT appear.
	calls := fake.calls()
	var hitHashes []string
	for _, c := range calls {
		hitHashes = append(hitHashes, c.Hashes...)
	}
	sort.Strings(hitHashes)
	want := []string{"aaa111", "ccc333"}
	if !reflect.DeepEqual(hitHashes, want) {
		t.Errorf("AddTags hit hashes = %v, want %v (BBB must be skipped)", hitHashes, want)
	}
}

// TestQbitSeBacklog_EmptySelectedHashesAppliesAll — legacy callers
// that omit SelectedHashes (or pass an empty slice) must still see
// the apply-all-taggable behaviour. Belt-and-braces against an
// accidental gate inversion that would silently no-op every legacy
// apply.
func TestQbitSeBacklog_EmptySelectedHashesAppliesAll(t *testing.T) {
	listJSON := `[
		{"hash":"aaa111","name":"Show.Name.S01E05.Title.1080p.WEB-DL.x264-GROUP","category":"sonarr","tags":""},
		{"hash":"bbb222","name":"Other.Show.S02E01.1080p.WEB-DL.x264-OTHER","category":"sonarr","tags":""}
	]`
	fake := newFakeQbitServer(t, listJSON)

	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(cfg *core.Config) {
		cfg.QbitInstances = []core.QbitInstance{{ID: "qbt1", Name: "qbt-main", URL: fake.srv.URL}}
		cfg.WebhookRules = []core.WebhookRule{{
			ID:         "rule-se",
			Name:       "Sonarr S/E backlog",
			Enabled:    true,
			InstanceID: "sonarr1",
			AppType:    "sonarr",
			Functions:  []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: "qbt1",
				EpisodeEnabled: true,
				EpisodeTag:     "Episode",
			},
		}}
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	s := &Server{App: &core.App{Config: store}}
	resp, apiErr := s.runQbitSeBacklogScan(context.Background(), qbitSeBacklogScanRequest{
		RuleID: "rule-se",
		// SelectedHashes intentionally omitted.
	}, true)
	if apiErr != nil {
		t.Fatalf("runQbitSeBacklogScan: %+v", apiErr)
	}
	applyResp, ok := resp.(qbitSeBacklogApplyResponse)
	if !ok {
		t.Fatalf("expected qbitSeBacklogApplyResponse, got %T", resp)
	}
	if applyResp.Applied != 2 {
		t.Errorf("Applied = %d, want 2 (apply-all when SelectedHashes is empty)", applyResp.Applied)
	}
}

// TestQbitSeBacklog_SelectedHashesCaseInsensitive — the user-side hash
// allowlist is normalised via strings.ToLower at gate-build time. The
// regression we're guarding against: a UI cache holding uppercase
// hashes (some qBit endpoints / clients hand out uppercased hex)
// shouldn't silently drop every selection. Both lowercase and
// uppercase variants must hit the same fake-qBit-returned (lowercase)
// hash and produce an AddTags call.
func TestQbitSeBacklog_SelectedHashesCaseInsensitive(t *testing.T) {
	listJSON := `[
		{"hash":"aaa111","name":"Show.Name.S01E05.Title.1080p.WEB-DL.x264-GROUP","category":"sonarr","tags":""},
		{"hash":"bbb222","name":"Other.Show.S02E01.1080p.WEB-DL.x264-OTHER","category":"sonarr","tags":""},
		{"hash":"ccc333","name":"Third.Show.S03.COMPLETE.1080p.WEB-DL.x264-FOO","category":"sonarr","tags":""}
	]`
	fake := newFakeQbitServer(t, listJSON)

	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(cfg *core.Config) {
		cfg.QbitInstances = []core.QbitInstance{{ID: "qbt1", Name: "qbt-main", URL: fake.srv.URL}}
		cfg.WebhookRules = []core.WebhookRule{{
			ID:         "rule-se",
			Name:       "Sonarr S/E backlog",
			Enabled:    true,
			InstanceID: "sonarr1",
			AppType:    "sonarr",
			Functions:  []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: "qbt1",
				EpisodeEnabled: true,
				EpisodeTag:     "Episode",
				SeasonEnabled:  true,
				SeasonTag:      "Season",
			},
		}}
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	s := &Server{App: &core.App{Config: store}}
	// AAA111 (uppercase) + ccc333 (lowercase). Fake qBit returns both
	// hashes lowercased — gate must match both.
	resp, apiErr := s.runQbitSeBacklogScan(context.Background(), qbitSeBacklogScanRequest{
		RuleID:         "rule-se",
		SelectedHashes: []string{"AAA111", "ccc333"},
	}, true)
	if apiErr != nil {
		t.Fatalf("runQbitSeBacklogScan: %+v", apiErr)
	}
	applyResp, ok := resp.(qbitSeBacklogApplyResponse)
	if !ok {
		t.Fatalf("expected qbitSeBacklogApplyResponse, got %T", resp)
	}
	if applyResp.Applied != 2 {
		t.Errorf("Applied = %d, want 2 (case-insensitive hash match against lowercase fake-qBit hashes); errors=%v", applyResp.Applied, applyResp.Errors)
	}
	calls := fake.calls()
	var hitHashes []string
	for _, c := range calls {
		hitHashes = append(hitHashes, c.Hashes...)
	}
	sort.Strings(hitHashes)
	want := []string{"aaa111", "ccc333"}
	if !reflect.DeepEqual(hitHashes, want) {
		t.Errorf("AddTags hit hashes = %v, want %v (uppercase AAA111 must collapse to lowercase aaa111)", hitHashes, want)
	}
}

// TestQbitSeBacklog_SelectedHashesUnknownDropped — a SelectedHashes
// entry that doesn't appear in the qBit listing must be silently
// dropped (not surfaced as an error). Hashes in the allowlist that
// aren't known to qBit are common when the user previewed, then the
// torrent got deleted between preview and apply — this is fine, no
// half-success error needed.
func TestQbitSeBacklog_SelectedHashesUnknownDropped(t *testing.T) {
	listJSON := `[
		{"hash":"aaa111","name":"Show.Name.S01E05.Title.1080p.WEB-DL.x264-GROUP","category":"sonarr","tags":""},
		{"hash":"bbb222","name":"Other.Show.S02E01.1080p.WEB-DL.x264-OTHER","category":"sonarr","tags":""}
	]`
	fake := newFakeQbitServer(t, listJSON)

	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(cfg *core.Config) {
		cfg.QbitInstances = []core.QbitInstance{{ID: "qbt1", Name: "qbt-main", URL: fake.srv.URL}}
		cfg.WebhookRules = []core.WebhookRule{{
			ID:         "rule-se",
			Name:       "Sonarr S/E backlog",
			Enabled:    true,
			InstanceID: "sonarr1",
			AppType:    "sonarr",
			Functions:  []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: "qbt1",
				EpisodeEnabled: true,
				EpisodeTag:     "Episode",
			},
		}}
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	s := &Server{App: &core.App{Config: store}}
	// aaa111 is known, deadbeef is not. Apply should tag aaa111 and
	// silently drop deadbeef without erroring.
	resp, apiErr := s.runQbitSeBacklogScan(context.Background(), qbitSeBacklogScanRequest{
		RuleID:         "rule-se",
		SelectedHashes: []string{"aaa111", "deadbeef"},
	}, true)
	if apiErr != nil {
		t.Fatalf("runQbitSeBacklogScan: %+v", apiErr)
	}
	applyResp, ok := resp.(qbitSeBacklogApplyResponse)
	if !ok {
		t.Fatalf("expected qbitSeBacklogApplyResponse, got %T", resp)
	}
	if applyResp.Applied != 1 {
		t.Errorf("Applied = %d, want 1 (only aaa111 is in qBit; deadbeef silently dropped)", applyResp.Applied)
	}
	if applyResp.Failed != 0 {
		t.Errorf("Failed = %d, want 0 (unknown selected hashes are not errors); errors=%v", applyResp.Failed, applyResp.Errors)
	}
	if len(applyResp.Errors) != 0 {
		t.Errorf("Errors = %v, want empty (unknown hashes do not raise errors)", applyResp.Errors)
	}
	calls := fake.calls()
	var hitHashes []string
	for _, c := range calls {
		hitHashes = append(hitHashes, c.Hashes...)
	}
	if !reflect.DeepEqual(hitHashes, []string{"aaa111"}) {
		t.Errorf("AddTags hit hashes = %v, want [aaa111] (deadbeef must not have triggered an AddTags call)", hitHashes)
	}
}

// TestValidateQbitSeConfig locks the shared validator extracted from the
// webhook rule validator (now also used by the one-off run endpoint).
func TestValidateQbitSeConfig(t *testing.T) {
	cfg := core.Config{QbitInstances: []core.QbitInstance{{ID: "qbt1", Name: "main"}}}
	cases := []struct {
		name    string
		in      *core.QbitSeRules
		wantErr bool
		check   func(t *testing.T, qse *core.QbitSeRules)
	}{
		{"nil rejected", nil, true, nil},
		{"all disabled rejected", &core.QbitSeRules{QbitInstanceID: "qbt1"}, true, nil},
		{"missing instance rejected", &core.QbitSeRules{EpisodeEnabled: true}, true, nil},
		{"unknown instance rejected", &core.QbitSeRules{EpisodeEnabled: true, QbitInstanceID: "nope"}, true, nil},
		{"invalid tag char rejected", &core.QbitSeRules{QbitInstanceID: "qbt1", EpisodeEnabled: true, EpisodeTag: "bad tag!"}, true, nil},
		{"blank tags default in place", &core.QbitSeRules{QbitInstanceID: "qbt1", EpisodeEnabled: true, SeasonEnabled: true, UnmatchedEnabled: true}, false,
			func(t *testing.T, qse *core.QbitSeRules) {
				if qse.EpisodeTag != "Episode" || qse.SeasonTag != "Season" || qse.UnmatchedTag != "Unmatched" {
					t.Errorf("defaults not applied: %+v", qse)
				}
			}},
		{"valid custom tag trimmed", &core.QbitSeRules{QbitInstanceID: "qbt1", EpisodeEnabled: true, EpisodeTag: "  ep-1  "}, false,
			func(t *testing.T, qse *core.QbitSeRules) {
				if qse.EpisodeTag != "ep-1" {
					t.Errorf("EpisodeTag = %q, want trimmed \"ep-1\"", qse.EpisodeTag)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateQbitSeConfig(c.in, cfg)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.check != nil {
				c.check(t, c.in)
			}
		})
	}
}
