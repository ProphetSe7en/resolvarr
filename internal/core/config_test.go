package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigStore_authDefaults_freshInstall asserts that the default
// Radarr/Sonarr-parity auth policy lands when there is no resolvarr.json
// on disk yet: Forms auth, LAN bypass, 30-day sessions, no trusted
// proxies, no trusted networks. A user coming off a clean VOLUME
// mount sees the app behave exactly as Radarr/Sonarr do out of the
// box — login when reached from outside the LAN, open on the LAN.
func TestConfigStore_authDefaults_freshInstall(t *testing.T) {
	dir := t.TempDir()
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	got := s.Get()
	if got.Authentication != "forms" {
		t.Errorf("Authentication: got %q, want forms", got.Authentication)
	}
	if got.AuthenticationRequired != "disabled_for_local_addresses" {
		t.Errorf("AuthenticationRequired: got %q, want disabled_for_local_addresses", got.AuthenticationRequired)
	}
	if got.SessionTTLDays != 30 {
		t.Errorf("SessionTTLDays: got %d, want 30", got.SessionTTLDays)
	}
	if got.TrustedProxies != "" {
		t.Errorf("TrustedProxies: got %q, want empty", got.TrustedProxies)
	}
	if got.TrustedNetworks != "" {
		t.Errorf("TrustedNetworks: got %q, want empty", got.TrustedNetworks)
	}
}

// TestConfigStore_authDefaults_legacyFile covers the upgrade path:
// an existing resolvarr.json from before the security release has no
// authentication fields at all. Load must fill them in with the same
// defaults as a fresh install, so existing users aren't locked out.
func TestConfigStore_authDefaults_legacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	legacy := []byte(`{
  "instances": [{"id":"abc","name":"Radarr","type":"radarr","iconVariant":"standard","url":"http://radarr:7878","apiKey":"k"}],
  "discord": {"enabled": false, "webhookUrl": ""},
  "display": {"uiScale": "1.1"},
  "releaseGroups": []
}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}

	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load legacy file: %v", err)
	}
	got := s.Get()
	if got.Authentication != "forms" {
		t.Errorf("Authentication: got %q, want forms", got.Authentication)
	}
	if got.AuthenticationRequired != "disabled_for_local_addresses" {
		t.Errorf("AuthenticationRequired: got %q, want disabled_for_local_addresses", got.AuthenticationRequired)
	}
	if got.SessionTTLDays != 30 {
		t.Errorf("SessionTTLDays: got %d, want 30", got.SessionTTLDays)
	}
	if len(got.Instances) != 1 || got.Instances[0].Name != "Radarr" {
		t.Errorf("existing instances lost across load: %+v", got.Instances)
	}
	// Display and Filters survived the default-fill too — a future
	// refactor of Load()'s default logic could regress these without
	// this check.
	if got.Display.UIScale != "1.1" {
		t.Errorf("Display.UIScale lost: got %q, want 1.1", got.Display.UIScale)
	}
	if !filtersInitialized(got.Filters.Radarr) {
		t.Error("Filters.Radarr regressed — defaults not filled for a legacy config")
	}
	if !filtersInitialized(got.Filters.Sonarr) {
		t.Error("Filters.Sonarr regressed — defaults not filled for a legacy config")
	}
}

// TestConfigStore_legacyFiltersMigrateToRadarr confirms that a config
// file written before the per-Arr-type filter split — with a flat
// FilterConfig under "filters" — loads into Filters.Radarr, and that
// Sonarr gets DefaultFilterConfig on the side. Matches the
// FilterSet.UnmarshalJSON + Load() interaction.
func TestConfigStore_legacyFiltersMigrateToRadarr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	// Legacy filter block — user had deliberately disabled TrueHD.
	legacy := []byte(`{
  "filters": {
    "Quality": true,
    "MAWebDL": true,
    "PlayWebDL": true,
    "Audio": true,
    "TrueHD": false,
    "TrueHDAtmos": true,
    "DTSX": true,
    "DTSHDMA": true
  }
}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	// Legacy values landed in Radarr with user's TrueHD=false intact.
	if got.Filters.Radarr.TrueHD {
		t.Errorf("Radarr.TrueHD: got true, want false (user's legacy choice must survive)")
	}
	if !got.Filters.Radarr.TrueHDAtmos {
		t.Errorf("Radarr.TrueHDAtmos: got false, want true")
	}
	// Sonarr was absent from legacy file — Load should have filled it
	// with DefaultFilterConfig (all on).
	if !got.Filters.Sonarr.TrueHD || !got.Filters.Sonarr.TrueHDAtmos {
		t.Errorf("Sonarr got user's Radarr values; should be defaults: %+v", got.Filters.Sonarr)
	}
}

// TestConfigStore_legacyReleaseGroupTypeMigration confirms that
// release-group entries without a type field land on "radarr" after
// Load(), matching the pre-split convention that tagarr was
// Radarr-only.
func TestConfigStore_legacyReleaseGroupTypeMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	legacy := []byte(`{
  "releaseGroups": [
    {"id":"a","search":"flux","tag":"flux","display":"FLUX","mode":"filtered"},
    {"id":"b","search":"farm","tag":"farm","display":"TheFarm","mode":"simple"}
  ]
}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if len(got.ReleaseGroups) != 2 {
		t.Fatalf("ReleaseGroups: got %d, want 2", len(got.ReleaseGroups))
	}
	for _, g := range got.ReleaseGroups {
		if g.Type != "radarr" {
			t.Errorf("ReleaseGroup %q: Type=%q, want radarr (legacy default)", g.Tag, g.Type)
		}
	}
}

// TestConfigStore_sessionTTLClamp asserts that Load() clamps out-of-
// range SessionTTLDays values (negative, overflow-risk large) back to
// the 30-day default. Belt-and-suspenders with auth.ValidateConfig —
// see baseline T38. 365 is the hard upper limit; anything over flips
// to the default. Negative values (byte-flipped writes, malformed
// hand-edits) also flip to the default.
func TestConfigStore_sessionTTLClamp(t *testing.T) {
	cases := []struct {
		name  string
		value int
		want  int
	}{
		{"negative", -5, 30},
		{"overflow-risk", 999999, 30},
		{"upper-bound", 365, 365},
		{"inside-range", 90, 90},
		{"one", 1, 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "resolvarr.json")
			payload := []byte(`{"sessionTtlDays": ` + itoa(tc.value) + `}`)
			if err := os.WriteFile(path, payload, 0600); err != nil {
				t.Fatal(err)
			}
			s := NewConfigStore(dir)
			if err := s.Load(); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := s.Get().SessionTTLDays; got != tc.want {
				t.Errorf("SessionTTLDays: got %d, want %d", got, tc.want)
			}
		})
	}
}

// itoa exists so the table above reads naturally without pulling in
// strconv just for test fixtures.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// TestConfigStore_authDefaults_userOverrides asserts that when a user
// HAS deliberately set a non-default value (e.g. "basic" auth or
// sessionTtlDays=7), Load must NOT clobber it with the default. The
// empty-string / zero-value defaults only fire when the field is
// absent or cleared, never when the user picked a legal alternative.
func TestConfigStore_authDefaults_userOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	persisted := []byte(`{
  "authentication": "basic",
  "authenticationRequired": "enabled",
  "sessionTtlDays": 7,
  "trustedProxies": "172.17.0.1",
  "trustedNetworks": "192.168.1.0/24"
}`)
	if err := os.WriteFile(path, persisted, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if got.Authentication != "basic" {
		t.Errorf("Authentication: got %q, want basic (user value must survive)", got.Authentication)
	}
	if got.AuthenticationRequired != "enabled" {
		t.Errorf("AuthenticationRequired: got %q, want enabled", got.AuthenticationRequired)
	}
	if got.SessionTTLDays != 7 {
		t.Errorf("SessionTTLDays: got %d, want 7 (user value must survive)", got.SessionTTLDays)
	}
	if got.TrustedProxies != "172.17.0.1" {
		t.Errorf("TrustedProxies: got %q, want 172.17.0.1", got.TrustedProxies)
	}
	if got.TrustedNetworks != "192.168.1.0/24" {
		t.Errorf("TrustedNetworks: got %q, want 192.168.1.0/24", got.TrustedNetworks)
	}
}

// TestDvDetailDefaults_FreshInstall pins that the M4b DvDetail config
// lands disabled by default with no allow-list and no prefix. Users
// have to consciously install the tools + flip Enabled before any
// DV-detail work happens.
func TestDvDetailDefaults_FreshInstall(t *testing.T) {
	dir := t.TempDir()
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if got.DvDetail.Enabled {
		t.Error("DvDetail.Enabled = true on fresh install — must be opt-in")
	}
	if got.DvDetail.Prefix != "" {
		t.Errorf("DvDetail.Prefix = %q, want empty (TRaSH bare-value default)", got.DvDetail.Prefix)
	}
	if len(got.DvDetail.AllowedValues) != 0 {
		t.Errorf("DvDetail.AllowedValues = %v, want nil/empty (all 5 vocab values)", got.DvDetail.AllowedValues)
	}
	if got.DvDetail.RemoveOrphanedTags {
		t.Error("DvDetail.RemoveOrphanedTags = true on fresh install — destructive cleanup must be opt-in")
	}
}

// TestDvDetailMigration_BadPrefixCleared covers the same prefix-
// migration as ExtraTags: a saved config with an invalid prefix
// (e.g. "dv:" from an earlier dev iteration that pre-dates Radarr's
// strict tag-label rule) gets cleared on Load and persisted clean.
func TestDvDetailMigration_BadPrefixCleared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	bad := []byte(`{
	  "instances": [],
	  "releaseGroups": [],
	  "dvDetail": {"enabled": true, "prefix": "dv:profile5"}
	}`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if got.DvDetail.Prefix != "" {
		t.Errorf("Prefix = %q, want cleared (Radarr ^[a-z0-9-]+$ rule)", got.DvDetail.Prefix)
	}
	// Enabled flag survives — only the bad prefix gets reset.
	if !got.DvDetail.Enabled {
		t.Error("Enabled flag was incorrectly cleared by prefix migration")
	}
}

// TestConfigStore_GetDeepCopiesQbitInstances regression-pins the
// QbitInstances slice deep-copy added per Agent 3 review #2.
// Without it, concurrent ConfigStore.Update doing a delete-by-shift
// would mutate the backing array under in-flight readers
// (findQbitInstanceByID returns *QbitInstance pointers into the
// snapshot).
func TestConfigStore_GetDeepCopiesQbitInstances(t *testing.T) {
	s := &ConfigStore{}
	s.cfg.QbitInstances = []QbitInstance{
		{ID: "q1", Name: "Main", URL: "http://example.com:8080"},
		{ID: "q2", Name: "Backup", URL: "http://example.com:8081"},
	}
	got := s.Get()
	if len(got.QbitInstances) != 2 {
		t.Fatalf("unexpected QbitInstances: %+v", got.QbitInstances)
	}
	got.QbitInstances[0].Name = "HACKED"
	if s.cfg.QbitInstances[0].Name != "Main" {
		t.Errorf("store mutated via returned slice: %q", s.cfg.QbitInstances[0].Name)
	}
}

// TestConfigStore_GetDeepCopiesDvDetailAllowedValues regression-pins
// the same header-aliasing class the AppriseURLs / ExtraTags fixes
// addressed. A caller mutating the returned AllowedValues slice
// must NOT see its mutation reflected back in the store.
func TestConfigStore_GetDeepCopiesDvDetailAllowedValues(t *testing.T) {
	s := &ConfigStore{}
	s.cfg.DvDetail.AllowedValues = []string{"mel", "fel"}
	got := s.Get()
	if len(got.DvDetail.AllowedValues) != 2 {
		t.Fatalf("unexpected AllowedValues: %+v", got.DvDetail.AllowedValues)
	}
	got.DvDetail.AllowedValues[0] = "HACKED"
	if s.cfg.DvDetail.AllowedValues[0] != "mel" {
		t.Errorf("store mutated via returned slice: %q", s.cfg.DvDetail.AllowedValues[0])
	}
}

// TestConfigStore_FilterOnlyDefaultsBackfill — a schedule loaded
// with TagSource="filter-only" but missing FilterOnlyTag must get
// the canonical default ("lossless-web") backfilled at Load. Belt
// + braces against a UI bug or hand-edit leaving the rule in a
// state where the engine would refuse to run.
func TestConfigStore_FilterOnlyDefaultsBackfill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	legacy := []byte(`{
  "instances": [{"id":"r","name":"Radarr","type":"radarr","iconVariant":"standard","url":"http://radarr:7878","apiKey":"k"}],
  "schedules": [{
    "id": "s1",
    "name": "Filter-only nightly",
    "mode": "tag",
    "instanceId": "r",
    "cron": "0 3 * * *",
    "enabled": true,
    "options": {
      "runMode": "apply",
      "tagSource": "filter-only"
    }
  }]
}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if len(got.Schedules) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(got.Schedules))
	}
	if got.Schedules[0].Options.TagSource != "filter-only" {
		t.Errorf("TagSource preserved? got %q, want filter-only", got.Schedules[0].Options.TagSource)
	}
	if got.Schedules[0].Options.FilterOnlyTag != "lossless-web" {
		t.Errorf("FilterOnlyTag default not applied: got %q, want lossless-web", got.Schedules[0].Options.FilterOnlyTag)
	}
}

// TestConfigStore_FilterOnlyClampsInvalidTagSource — an unknown
// tagSource value clamps to "" (legacy default = use Active list)
// at Load. Defensive against future value renames or hand-edits
// that would otherwise leave the engine in an unhandled-branch
// state.
func TestConfigStore_FilterOnlyClampsInvalidTagSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	legacy := []byte(`{
  "instances": [{"id":"r","name":"Radarr","type":"radarr","iconVariant":"standard","url":"http://radarr:7878","apiKey":"k"}],
  "schedules": [{
    "id": "s1",
    "name": "Bad tagSource",
    "mode": "tag",
    "instanceId": "r",
    "cron": "0 3 * * *",
    "enabled": true,
    "options": {
      "runMode": "apply",
      "tagSource": "garbage-value"
    }
  }]
}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if got.Schedules[0].Options.TagSource != "" {
		t.Errorf("invalid tagSource not clamped: got %q, want \"\"", got.Schedules[0].Options.TagSource)
	}
}

// TestConfigStore_FilterOnlyPreservesUserTag — when the user did
// supply FilterOnlyTag, Load must NOT overwrite it with the default.
// Backfill is a defaulting safety net, not a rename.
func TestConfigStore_FilterOnlyPreservesUserTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvarr.json")
	legacy := []byte(`{
  "instances": [{"id":"r","name":"Radarr","type":"radarr","iconVariant":"standard","url":"http://radarr:7878","apiKey":"k"}],
  "schedules": [{
    "id": "s1",
    "name": "Custom tag",
    "mode": "tag",
    "instanceId": "r",
    "cron": "0 3 * * *",
    "enabled": true,
    "options": {
      "runMode": "apply",
      "tagSource": "filter-only",
      "filterOnlyTag": "premium-bluray"
    }
  }]
}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s.Get()
	if got.Schedules[0].Options.FilterOnlyTag != "premium-bluray" {
		t.Errorf("user FilterOnlyTag clobbered: got %q, want premium-bluray", got.Schedules[0].Options.FilterOnlyTag)
	}
}
