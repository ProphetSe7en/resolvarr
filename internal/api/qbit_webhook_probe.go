package api

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"resolvarr/internal/qbit"
)

// qbit_webhook_probe.go — the real end-to-end qBit webhook test. Instead of
// pinging resolvarr's own handler, it adds a tiny synthetic test torrent to
// qBit via the API, which makes qBit fire its configured "run on torrent
// added" program back to resolvarr. The receive handler recognises the test
// torrent's infohash, signals the waiting probe, and stops (no tag / notify /
// history). The probe then removes the test torrent. A timeout means qBit
// could not reach resolvarr (wrong URL/secret/network, or the autorun command
// isn't configured).

const qbitProbeTimeout = 20 * time.Second

// buildTestTorrent returns a minimal valid single-file, trackerless .torrent
// plus its v1 infohash (lowercase hex). The name carries a unique marker so
// each probe gets a distinct infohash to correlate on.
func buildTestTorrent(name string) (data []byte, infoHash string) {
	content := []byte("resolvarr-webhook-probe")
	pieceHash := sha1.Sum(content)
	// bencoded info dict, keys in lexical order: length, name, piece length,
	// pieces (20 raw SHA1 bytes of the single piece).
	info := "d" +
		"6:lengthi" + strconv.Itoa(len(content)) + "e" +
		"4:name" + strconv.Itoa(len(name)) + ":" + name +
		"12:piece lengthi16384e" +
		"6:pieces20:" + string(pieceHash[:]) +
		"e"
	ih := sha1.Sum([]byte(info))
	full := "d4:info" + info + "e"
	return []byte(full), hex.EncodeToString(ih[:])
}

func qbitProbeKey(instanceID, infoHash string) string {
	return instanceID + ":" + strings.ToLower(strings.TrimSpace(infoHash))
}

func (s *Server) registerQbitProbe(key string) chan struct{} {
	s.qbitProbeMu.Lock()
	defer s.qbitProbeMu.Unlock()
	if s.qbitProbes == nil {
		s.qbitProbes = map[string]chan struct{}{}
	}
	ch := make(chan struct{})
	s.qbitProbes[key] = ch
	return ch
}

func (s *Server) unregisterQbitProbe(key string) {
	s.qbitProbeMu.Lock()
	defer s.qbitProbeMu.Unlock()
	delete(s.qbitProbes, key)
}

// signalQbitProbe closes the pending probe's channel for (instanceID,
// infoHash) and returns true if one was registered. Called by the receive
// handler so a test torrent's webhook resolves the waiting probe instead of
// being processed as a normal add.
func (s *Server) signalQbitProbe(instanceID, infoHash string) bool {
	key := qbitProbeKey(instanceID, infoHash)
	s.qbitProbeMu.Lock()
	defer s.qbitProbeMu.Unlock()
	ch, ok := s.qbitProbes[key]
	if ok {
		close(ch)
		delete(s.qbitProbes, key)
	}
	return ok
}

type qbitProbeResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// handleQbitWebhookProbe runs the real end-to-end test for one qBit instance.
func (s *Server) handleQbitWebhookProbe(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	cfg := s.App.Config.Get()
	inst := findQbitInstanceByID(cfg, id)
	if inst == nil {
		writeError(w, 404, "qbit instance not found")
		return
	}
	if strings.TrimSpace(inst.WebhookSecret) == "" {
		writeJSON(w, qbitProbeResult{OK: false, Message: "The webhook isn't configured yet. Configure it first, then run the test."})
		return
	}
	client, err := qbit.New(qbit.Config{URL: inst.URL, Username: inst.Username, Password: inst.Password, TrustedCerts: inst.TrustedCerts})
	if err != nil {
		writeJSON(w, qbitProbeResult{OK: false, Message: "Couldn't build a qBittorrent client: " + err.Error()})
		return
	}

	tok := make([]byte, 6)
	_, _ = rand.Read(tok)
	name := "resolvarr-webhook-test-" + hex.EncodeToString(tok)
	data, infoHash := buildTestTorrent(name)
	key := qbitProbeKey(id, infoHash)
	ch := s.registerQbitProbe(key)
	defer s.unregisterQbitProbe(key)

	ctx, cancel := context.WithTimeout(r.Context(), qbitProbeTimeout)
	defer cancel()

	if err := client.AddTorrent(ctx, data, name, "resolvarr-webhook-test"); err != nil {
		writeJSON(w, qbitProbeResult{OK: false, Message: "Couldn't add the test torrent to qBittorrent: " + err.Error()})
		return
	}
	// Always remove the test torrent, even on timeout. Best-effort, own ctx
	// so it still runs after the request ctx is done.
	defer func() {
		delCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = client.DeleteTorrent(delCtx, infoHash, true)
	}()

	select {
	case <-ch:
		writeJSON(w, qbitProbeResult{OK: true, Message: "qBittorrent reached resolvarr. The webhook is configured correctly."})
	case <-ctx.Done():
		writeJSON(w, qbitProbeResult{OK: false, Message: "Added a test torrent, but qBittorrent never called resolvarr back within 20 seconds. Check that the webhook command is set in qBittorrent (Options, Downloads, Run external program on torrent added) and that qBittorrent can reach this URL."})
	}
}
