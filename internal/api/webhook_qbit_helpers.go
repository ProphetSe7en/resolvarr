package api

// webhook_qbit_helpers.go — shared qBit-domain helpers used by the
// Grab Rename adapter, the qBit S/E adapter, and the qBit S/E
// backlog-fix endpoints. Lifted out of webhook_grab_rename.go so the
// cross-file coupling is explicit (sister adapters reach into a
// shared helper set, not into each other's internals).

import (
	"context"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/qbit"
)

// findQbitInstanceByID is the qBit equivalent of findInstanceByID.
// Returns nil when the configured QbitInstanceID isn't in
// cfg.QbitInstances (e.g. user deleted the qBit instance after saving
// the rule). Adapter treats nil as a hard error — rule's qBit binding
// is required by validator at save-time.
//
// Returns *core.QbitInstance pointing into the per-receive cfg
// snapshot (deep-copied by ConfigStore.Get); safe to read without
// further locking.
func findQbitInstanceByID(cfg core.Config, id string) *core.QbitInstance {
	if id == "" {
		return nil
	}
	for i := range cfg.QbitInstances {
		if cfg.QbitInstances[i].ID == id {
			return &cfg.QbitInstances[i]
		}
	}
	return nil
}

// waitForTorrent retries GetTorrent with backoff because qBit may
// finish indexing the just-added torrent a few seconds after Arr's
// /torrents/add returns. Mirrors bash tagarr_import.sh:217-225 retry
// loop. Total upper bound ~16s before give-up; first hit exits early.
//
// Each retry checks ctx; a cancelled receiver context aborts cleanly.
func waitForTorrent(ctx context.Context, client *qbit.Client, hash string) (qbit.Torrent, bool, error) {
	delays := []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second, 5 * time.Second, 5 * time.Second}
	for _, d := range delays {
		if d > 0 {
			select {
			case <-ctx.Done():
				return qbit.Torrent{}, false, ctx.Err()
			case <-time.After(d):
			}
		}
		t, found, err := client.GetTorrent(ctx, hash)
		if err != nil {
			return qbit.Torrent{}, false, err
		}
		if found {
			return t, true, nil
		}
	}
	return qbit.Torrent{}, false, nil
}
