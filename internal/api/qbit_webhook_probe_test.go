package api

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"
)

func TestBuildTestTorrent(t *testing.T) {
	data, ih := buildTestTorrent("resolvarr-webhook-test-abc123")

	if len(ih) != 40 {
		t.Fatalf("infohash len = %d, want 40 hex chars", len(ih))
	}
	if ih != strings.ToLower(ih) {
		t.Errorf("infohash should be lowercase hex, got %q", ih)
	}
	s := string(data)
	if !strings.HasPrefix(s, "d4:infod") {
		t.Errorf("torrent should start with d4:infod, got %.20q", s)
	}
	if !strings.HasSuffix(s, "ee") {
		t.Errorf("torrent should end with ee (info dict + outer dict)")
	}
	// infohash must equal sha1 of the bencoded info dict (the bytes between
	// the outer "d4:info" prefix and the trailing "e").
	info := s[len("d4:info") : len(s)-1]
	sum := sha1.Sum([]byte(info))
	if got := hex.EncodeToString(sum[:]); got != ih {
		t.Errorf("infohash %q != sha1(info) %q", ih, got)
	}
	// Different names produce different infohashes; same name is stable.
	if _, ih2 := buildTestTorrent("resolvarr-webhook-test-xyz"); ih2 == ih {
		t.Errorf("different names produced the same infohash")
	}
	if _, again := buildTestTorrent("resolvarr-webhook-test-abc123"); again != ih {
		t.Errorf("same name not deterministic")
	}
}

func TestQbitProbeSignal(t *testing.T) {
	s := &Server{}
	ch := s.registerQbitProbe(qbitProbeKey("inst1", "ABCDEF0123"))

	// Receive handler lowercases the hash; the key match must be case-folded.
	if !s.signalQbitProbe("inst1", "abcdef0123") {
		t.Fatalf("signal should find the pending probe (case-insensitive)")
	}
	select {
	case <-ch:
	default:
		t.Errorf("probe channel should be closed after signal")
	}
	// Consumed: a second signal finds nothing.
	if s.signalQbitProbe("inst1", "abcdef0123") {
		t.Errorf("probe should be consumed after the first signal")
	}
	// Wrong instance / unknown hash must not signal.
	s.registerQbitProbe(qbitProbeKey("inst1", "deadbeef"))
	if s.signalQbitProbe("inst2", "deadbeef") {
		t.Errorf("a probe for inst1 must not be signalled by inst2")
	}
}
