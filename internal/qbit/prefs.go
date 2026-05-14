package qbit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// qbitPrefs decodes ONLY the "Run external program on torrent added"
// fields out of qBit's full preferences blob. The full response is
// hundreds of keys covering every Settings panel — restricting our
// struct to what we actually need keeps the decode cheap, our
// in-memory footprint small, and our code resilient to qBit version
// drift (new keys we don't care about don't break us).
//
// Field names match qBit 4.5+. Earlier versions (4.4.x and older) had
// a single autorun_program / autorun_enabled pair shared between
// "on add" and "on complete" — those are NOT supported by these
// helpers; document min version 4.5 in the user-facing UI.
type qbitPrefs struct {
	AutorunOnTorrentAddedEnabled bool   `json:"autorun_on_torrent_added_enabled"`
	AutorunOnTorrentAddedProgram string `json:"autorun_on_torrent_added_program"`
}

// GetAutorunOnAdded fetches qBit's "Run external program on torrent
// added" settings (Settings → Downloads section). Returns the program
// string + the enabled flag.
//
// Used by the M-qBit-add webhook config endpoints to:
//   - Detect whether a hook is already configured
//   - Distinguish ours from a third-party script (string-match on the
//     resolvarr URL prefix)
//   - Capture the existing value for PreviousAutorunBackup before we
//     overwrite it with our own curl
//
// Empty program + enabled=false is the qBit default (autorun feature
// not configured at all). Empty program + enabled=true is unusual but
// valid — qBit accepts the toggle independent of the field content.
//
// API: GET /api/v2/app/preferences. Returns the full preferences blob;
// we decode only the two keys we care about. qBit 4.5+ required.
func (c *Client) GetAutorunOnAdded(ctx context.Context) (program string, enabled bool, err error) {
	resp, err := c.Do(ctx, "GET", "/api/v2/app/preferences", nil, nil)
	if err != nil {
		return "", false, fmt.Errorf("getAutorunOnAdded: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		snippet := strings.TrimSpace(string(bodyBytes))
		if snippet == "" {
			return "", false, fmt.Errorf("getAutorunOnAdded HTTP %d", resp.StatusCode)
		}
		return "", false, fmt.Errorf("getAutorunOnAdded HTTP %d: %s", resp.StatusCode, snippet)
	}
	// Cap the read — full preferences blob is ~5 KB on typical qBit
	// installs, 64 KB headroom for future-proofing.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", false, fmt.Errorf("getAutorunOnAdded read body: %w", err)
	}
	var prefs qbitPrefs
	if err := json.Unmarshal(bodyBytes, &prefs); err != nil {
		return "", false, fmt.Errorf("getAutorunOnAdded decode: %w", err)
	}
	return prefs.AutorunOnTorrentAddedProgram, prefs.AutorunOnTorrentAddedEnabled, nil
}

// SetAutorunOnAdded writes qBit's "Run external program on torrent
// added" settings via setPreferences. Both the program string and
// the enabled flag are sent in one call so the caller can't end up
// with a partial state (e.g. enabled=true + program="" because the
// program write succeeded and the toggle write didn't).
//
// API: POST /api/v2/app/setPreferences with form-encoded
// `json={"key":"value",...}`. qBit accepts a partial preferences
// object — keys not in the JSON are left unchanged. Returns 200 OK
// on success regardless of whether values actually changed.
//
// Caller is responsible for backing up the previous value (read via
// GetAutorunOnAdded) into QbitInstance.PreviousAutorunBackup BEFORE
// calling this — Reset depends on having that snapshot.
func (c *Client) SetAutorunOnAdded(ctx context.Context, program string, enabled bool) error {
	payload, err := json.Marshal(qbitPrefs{
		AutorunOnTorrentAddedEnabled: enabled,
		AutorunOnTorrentAddedProgram: program,
	})
	if err != nil {
		// json.Marshal of a fixed-shape struct can't realistically
		// fail; treat as defensive.
		return fmt.Errorf("setAutorunOnAdded marshal: %w", err)
	}
	form := url.Values{}
	form.Set("json", string(payload))
	body := strings.NewReader(form.Encode())
	resp, err := c.Do(ctx, "POST", "/api/v2/app/setPreferences", body, func(h http.Header) {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if err != nil {
		return fmt.Errorf("setAutorunOnAdded: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("setAutorunOnAdded HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("setAutorunOnAdded HTTP %d: %s", resp.StatusCode, snippet)
}
