package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"resolvarr/internal/core"
)

// fakeQbit spins up an httptest.Server that mocks qBit's preferences
// API for these tests. Returns the URL + helpers to assert what was
// posted. autorunProgram + autorunEnabled are mutated by the SET
// handler so tests can verify state-after.
type fakeQbit struct {
	mu              sync.Mutex
	server          *httptest.Server
	autorunProgram  string
	autorunEnabled  bool
	failGet         bool
	failSet         bool
	getCalls        atomic.Int32
	setCalls        atomic.Int32
	lastSetProgram  string
	lastSetEnabled  bool
}

func newFakeQbit(t *testing.T) *fakeQbit {
	t.Helper()
	f := &fakeQbit{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/preferences":
			f.getCalls.Add(1)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failGet {
				w.WriteHeader(403)
				_, _ = w.Write([]byte("qui blocked GET"))
				return
			}
			fmt.Fprintf(w, `{
				"autorun_on_torrent_added_enabled": %t,
				"autorun_on_torrent_added_program": %q
			}`, f.autorunEnabled, f.autorunProgram)
		case "/api/v2/app/setPreferences":
			f.setCalls.Add(1)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failSet {
				w.WriteHeader(403)
				_, _ = w.Write([]byte("qui blocked SET"))
				return
			}
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(400)
				return
			}
			var payload struct {
				AutorunOnTorrentAddedEnabled bool   `json:"autorun_on_torrent_added_enabled"`
				AutorunOnTorrentAddedProgram string `json:"autorun_on_torrent_added_program"`
			}
			if err := json.Unmarshal([]byte(r.Form.Get("json")), &payload); err != nil {
				w.WriteHeader(400)
				return
			}
			f.autorunProgram = payload.AutorunOnTorrentAddedProgram
			f.autorunEnabled = payload.AutorunOnTorrentAddedEnabled
			f.lastSetProgram = payload.AutorunOnTorrentAddedProgram
			f.lastSetEnabled = payload.AutorunOnTorrentAddedEnabled
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// newWebhookConfigTestServer wires a Server backed by a real
// ConfigStore + a single qBit instance pointing at the fakeQbit.
func newWebhookConfigTestServer(t *testing.T, fq *fakeQbit) (*Server, *core.ConfigStore, string) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	const instID = "qbit-1"
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances = append(c.QbitInstances, core.QbitInstance{
			ID:            instID,
			Name:          "test",
			URL:           fq.server.URL,
			WebhookSecret: "preset-secret-for-test",
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &Server{App: &core.App{Config: store}}, store, instID
}

// ---- GET /webhook ---------------------------------------------------

func TestQbitWebhookConfig_GET_ReturnsCurlAndState(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = ""
	s, _, instID := newWebhookConfigTestServer(t, fq)

	req := httptest.NewRequest(http.MethodGet, "/api/qbit-instances/"+instID+"/webhook", nil)
	req.SetPathValue("id", instID)
	req.Host = "resolvarr.test:6075"
	rr := httptest.NewRecorder()
	s.handleQbitWebhookConfig(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp qbitWebhookConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Secret != "preset-secret-for-test" {
		t.Errorf("Secret = %q, want preset-secret-for-test (plaintext on this endpoint)", resp.Secret)
	}
	if !strings.Contains(resp.WebhookURL, "/api/qbit/torrent-added/"+instID) {
		t.Errorf("WebhookURL = %q, missing instance path", resp.WebhookURL)
	}
	if !strings.Contains(resp.CurlCommand, "X-API-Key: preset-secret-for-test") {
		t.Errorf("CurlCommand missing X-API-Key header: %s", resp.CurlCommand)
	}
	if !strings.Contains(resp.CurlCommand, `infoHash=%I`) {
		t.Errorf("CurlCommand missing %%I placeholder: %s", resp.CurlCommand)
	}
	if resp.QbitState == nil {
		t.Fatal("QbitState should be present (fakeQbit GET succeeds)")
	}
	if resp.QbitState.ConfiguredByUs || resp.QbitState.ThirdPartyContent {
		t.Errorf("empty autorun should not flag configured/third-party: %+v", resp.QbitState)
	}
}

func TestQbitWebhookConfig_GET_DetectsThirdPartyContent(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = `/scripts/cross-seed-notify.sh "%L" "%I"`
	fq.autorunEnabled = true
	s, _, instID := newWebhookConfigTestServer(t, fq)

	req := httptest.NewRequest(http.MethodGet, "/api/qbit-instances/"+instID+"/webhook", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitWebhookConfig(rr, req)

	var resp qbitWebhookConfigResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.QbitState.ThirdPartyContent != true {
		t.Errorf("ThirdPartyContent = false, want true (program is non-empty + not ours)")
	}
	if resp.QbitState.ConfiguredByUs {
		t.Errorf("ConfiguredByUs = true, want false")
	}
}

func TestQbitWebhookConfig_GET_DetectsConfiguredByUs(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = `curl -fsS -X POST "http://resolvarr:6075/api/qbit/torrent-added/qbit-1" -H "X-API-Key: x" --data-urlencode "infoHash=%I"`
	s, _, instID := newWebhookConfigTestServer(t, fq)

	req := httptest.NewRequest(http.MethodGet, "/api/qbit-instances/"+instID+"/webhook", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitWebhookConfig(rr, req)

	var resp qbitWebhookConfigResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.QbitState.ConfiguredByUs {
		t.Errorf("ConfiguredByUs = false, want true (program contains our path prefix)")
	}
	if resp.QbitState.ThirdPartyContent {
		t.Errorf("ThirdPartyContent = true, want false")
	}
}

func TestQbitWebhookConfig_GET_QbitFetchFailureStillReturnsCurl(t *testing.T) {
	fq := newFakeQbit(t)
	fq.failGet = true
	s, _, instID := newWebhookConfigTestServer(t, fq)

	req := httptest.NewRequest(http.MethodGet, "/api/qbit-instances/"+instID+"/webhook", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitWebhookConfig(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d (should be 200 even on qBit fetch failure)", rr.Code)
	}
	var resp qbitWebhookConfigResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.CurlCommand == "" {
		t.Errorf("CurlCommand empty — should still be returned even when qBit GET fails")
	}
	if resp.QbitState == nil || resp.QbitState.FetchError == "" {
		t.Errorf("QbitState.FetchError should surface the qBit failure: %+v", resp.QbitState)
	}
}

// ---- POST /configure ------------------------------------------------

func TestQbitConfigure_EmptyAutorunSetsOurs(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = ""
	s, store, instID := newWebhookConfigTestServer(t, fq)

	body := strings.NewReader(`{"mode":"append"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/configure", body)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitConfigureWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(fq.lastSetProgram, "/api/qbit/torrent-added/"+instID) {
		t.Errorf("qBit autorun = %q, missing our path", fq.lastSetProgram)
	}
	if !fq.lastSetEnabled {
		t.Errorf("qBit autorun should be enabled=true")
	}
	cfg := store.Get()
	if !cfg.QbitInstances[0].WebhookConfiguredInQbit {
		t.Errorf("WebhookConfiguredInQbit = false, want true after Configure")
	}
	if cfg.QbitInstances[0].PreviousAutorunBackup != "" {
		t.Errorf("PreviousAutorunBackup = %q, want empty (no third-party content to back up)", cfg.QbitInstances[0].PreviousAutorunBackup)
	}
}

func TestQbitConfigure_AppendPreservesThirdParty(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = `/scripts/notify.sh "%I"`
	s, store, instID := newWebhookConfigTestServer(t, fq)

	body := strings.NewReader(`{"mode":"append"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/configure", body)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitConfigureWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.HasPrefix(fq.lastSetProgram, `/scripts/notify.sh`) {
		t.Errorf("Append mode lost original prefix: %q", fq.lastSetProgram)
	}
	if !strings.Contains(fq.lastSetProgram, "; ") {
		t.Errorf("Append should use ; separator, got: %q", fq.lastSetProgram)
	}
	if !strings.Contains(fq.lastSetProgram, "/api/qbit/torrent-added/"+instID) {
		t.Errorf("Append missing our curl: %q", fq.lastSetProgram)
	}
	cfg := store.Get()
	if cfg.QbitInstances[0].PreviousAutorunBackup != `/scripts/notify.sh "%I"` {
		t.Errorf("Backup = %q, want original third-party content", cfg.QbitInstances[0].PreviousAutorunBackup)
	}
}

func TestQbitConfigure_ReplaceBacksUpThirdParty(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = `/scripts/notify.sh "%I"`
	s, store, instID := newWebhookConfigTestServer(t, fq)

	body := strings.NewReader(`{"mode":"replace"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/configure", body)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitConfigureWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(fq.lastSetProgram, "/scripts/notify.sh") {
		t.Errorf("Replace mode should NOT preserve original: %q", fq.lastSetProgram)
	}
	cfg := store.Get()
	if cfg.QbitInstances[0].PreviousAutorunBackup != `/scripts/notify.sh "%I"` {
		t.Errorf("Replace must back up old value: got %q", cfg.QbitInstances[0].PreviousAutorunBackup)
	}
}

func TestQbitConfigure_AlreadyOursIdempotent(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = `curl -X POST "http://resolvarr:6075/api/qbit/torrent-added/qbit-1" -H "X-API-Key: old"`
	s, store, instID := newWebhookConfigTestServer(t, fq)

	body := strings.NewReader(`{"mode":"append"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/configure", body)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitConfigureWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	// Idempotent path overwrites with current-secret curl — secret may
	// have rotated since the existing field was written. PreviousAutorun
	// Backup must NOT be set (no third-party content was overwritten).
	cfg := store.Get()
	if cfg.QbitInstances[0].PreviousAutorunBackup != "" {
		t.Errorf("Backup should not be set on idempotent re-Configure: %q", cfg.QbitInstances[0].PreviousAutorunBackup)
	}
}

// TestQbitConfigure_IdempotentPreservesSurroundingScripts — when the
// user previously ran Configure-Append on a setup that had a third-
// party script (notify.sh) and the result was "notify.sh; <ours>",
// clicking Configure AGAIN must preserve notify.sh + only swap our
// line in-place. Earlier v1 of Slice 4 had a blanket-overwrite bug
// that nuked surrounding content on re-configure.
func TestQbitConfigure_IdempotentPreservesSurroundingScripts(t *testing.T) {
	fq := newFakeQbit(t)
	// Stage: qBit already has our line bracketed by user scripts.
	fq.autorunProgram = `/scripts/notify.sh "%I"; curl -fsS -X POST "http://resolvarr:6075/api/qbit/torrent-added/qbit-1" -H "X-API-Key: previous-secret"; /scripts/log.sh "%N"`
	s, _, instID := newWebhookConfigTestServer(t, fq)

	body := strings.NewReader(`{"mode":"append"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/configure", body)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitConfigureWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.HasPrefix(fq.lastSetProgram, `/scripts/notify.sh`) {
		t.Errorf("re-Configure lost leading script: %q", fq.lastSetProgram)
	}
	if !strings.HasSuffix(fq.lastSetProgram, `/scripts/log.sh "%N"`) {
		t.Errorf("re-Configure lost trailing script: %q", fq.lastSetProgram)
	}
	if strings.Contains(fq.lastSetProgram, "previous-secret") {
		t.Errorf("re-Configure left old secret in qBit: %q", fq.lastSetProgram)
	}
	if !strings.Contains(fq.lastSetProgram, "X-API-Key: preset-secret-for-test") {
		t.Errorf("re-Configure missing current-secret curl: %q", fq.lastSetProgram)
	}
}

func TestQbitConfigure_QbitFailureNotPersisted(t *testing.T) {
	fq := newFakeQbit(t)
	fq.failSet = true
	s, store, instID := newWebhookConfigTestServer(t, fq)

	body := strings.NewReader(`{"mode":"append"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/configure", body)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitConfigureWebhook(rr, req)

	if rr.Code != 502 {
		t.Errorf("status = %d, want 502 on qBit SET failure", rr.Code)
	}
	cfg := store.Get()
	if cfg.QbitInstances[0].WebhookConfiguredInQbit {
		t.Errorf("WebhookConfiguredInQbit = true after qBit failure — must stay false")
	}
}

// ---- POST /rotate-secret -------------------------------------------

func TestQbitRotateSecret_NotConfiguredJustRotatesLocal(t *testing.T) {
	fq := newFakeQbit(t)
	s, store, instID := newWebhookConfigTestServer(t, fq)

	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/rotate-secret", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitRotateWebhookSecret(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["secret"] == "preset-secret-for-test" {
		t.Errorf("secret unchanged after rotate: %v", resp["secret"])
	}
	cfg := store.Get()
	if cfg.QbitInstances[0].WebhookSecret == "preset-secret-for-test" {
		t.Errorf("stored secret unchanged after rotate: %q", cfg.QbitInstances[0].WebhookSecret)
	}
	// qBit was never auto-configured → no SET should fire.
	if fq.setCalls.Load() != 0 {
		t.Errorf("rotate fired qBit SET=%d times, want 0 (not auto-configured)", fq.setCalls.Load())
	}
}

func TestQbitRotateSecret_ConfiguredAlsoUpdatesQbit(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = `/scripts/notify.sh; curl -X POST "http://resolvarr:6075/api/qbit/torrent-added/qbit-1" -H "X-API-Key: old-secret"`
	s, store, instID := newWebhookConfigTestServer(t, fq)
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances[0].WebhookConfiguredInQbit = true
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/rotate-secret", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitRotateWebhookSecret(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if fq.setCalls.Load() != 1 {
		t.Errorf("rotate didn't push to qBit: setCalls=%d", fq.setCalls.Load())
	}
	// Surrounding /scripts/notify.sh must survive the surgical replace.
	if !strings.HasPrefix(fq.lastSetProgram, `/scripts/notify.sh`) {
		t.Errorf("rotate lost surrounding script: %q", fq.lastSetProgram)
	}
	if strings.Contains(fq.lastSetProgram, "old-secret") {
		t.Errorf("rotate left old secret in qBit autorun: %q", fq.lastSetProgram)
	}
}

// ---- POST /test ----------------------------------------------------

func TestQbitTestEndpoint_Synthetic200(t *testing.T) {
	fq := newFakeQbit(t)
	s, _, instID := newWebhookConfigTestServer(t, fq)

	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/test", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTestWebhookEndpoint(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Errorf("body = %q, want success", rr.Body.String())
	}
}

// ---- POST /reset ---------------------------------------------------

func TestQbitReset_RestoresBackup(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = "ours-curl-here"
	fq.autorunEnabled = true
	s, store, instID := newWebhookConfigTestServer(t, fq)
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances[0].WebhookConfiguredInQbit = true
		c.QbitInstances[0].PreviousAutorunBackup = `/scripts/old-script.sh "%I"`
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/reset", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitResetWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if fq.lastSetProgram != `/scripts/old-script.sh "%I"` {
		t.Errorf("Reset didn't restore backup: qBit autorun = %q", fq.lastSetProgram)
	}
	if !fq.lastSetEnabled {
		t.Errorf("Reset of non-empty backup should keep enabled=true")
	}
	cfg := store.Get()
	if cfg.QbitInstances[0].WebhookConfiguredInQbit {
		t.Errorf("WebhookConfiguredInQbit not cleared after Reset")
	}
	if cfg.QbitInstances[0].PreviousAutorunBackup != "" {
		t.Errorf("PreviousAutorunBackup not cleared after Reset: %q", cfg.QbitInstances[0].PreviousAutorunBackup)
	}
}

func TestQbitReset_NoBackupClearsAndDisables(t *testing.T) {
	fq := newFakeQbit(t)
	fq.autorunProgram = "ours-curl-here"
	fq.autorunEnabled = true
	s, _, instID := newWebhookConfigTestServer(t, fq)
	// No backup set — instance was Configured on an originally-empty field.

	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/"+instID+"/webhook/reset", nil)
	req.SetPathValue("id", instID)
	rr := httptest.NewRecorder()
	s.handleQbitResetWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if fq.lastSetProgram != "" {
		t.Errorf("Reset with empty backup should clear program, got %q", fq.lastSetProgram)
	}
	if fq.lastSetEnabled {
		t.Errorf("Reset with empty backup should disable autorun")
	}
}

// ---- helpers -------------------------------------------------------

func TestReplaceResolvarrLine_PreservesSurroundingScripts(t *testing.T) {
	current := `/scripts/notify.sh; curl -X POST "/api/qbit/torrent-added/x" -H "X-API-Key: old"; /scripts/log.sh`
	newCurl := `curl -X POST "/api/qbit/torrent-added/x" -H "X-API-Key: new"`
	got := replaceResolvarrLine(current, newCurl)
	want := `/scripts/notify.sh; curl -X POST "/api/qbit/torrent-added/x" -H "X-API-Key: new"; /scripts/log.sh`
	if got != want {
		t.Errorf("replaceResolvarrLine:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestReplaceResolvarrLine_NoMatchReturnsUnchanged(t *testing.T) {
	current := `/scripts/notify.sh "%I"`
	got := replaceResolvarrLine(current, "doesnt-matter")
	if got != current {
		t.Errorf("no-match replace: got %q, want unchanged %q", got, current)
	}
}

func TestBuildQbitCurlCommand_ContainsPlaceholders(t *testing.T) {
	got := buildQbitCurlCommand("http://resolvarr:6075/api/qbit/torrent-added/x", "secret")
	if !strings.Contains(got, "%I") {
		t.Errorf("missing %%I placeholder: %s", got)
	}
	if !strings.Contains(got, "%N") {
		t.Errorf("missing %%N placeholder: %s", got)
	}
	if !strings.Contains(got, "%L") {
		t.Errorf("missing %%L placeholder: %s", got)
	}
	if !strings.Contains(got, "X-API-Key: secret") {
		t.Errorf("missing or wrong X-API-Key: %s", got)
	}
}
