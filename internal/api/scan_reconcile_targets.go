package api

// scan_reconcile_targets.go — the download clients a stuck-downloads
// apply acts on. One Sonarr/Radarr queue mixes downloads from all of its
// qBittorrent clients, so each stuck hash is looked up in every
// configured client and moved with that client's own category. Matching
// is by hash, not by client name.

import (
	"context"
	"fmt"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/qbit"
)

// reconcileClient is one download client of an apply run.
type reconcileClient struct {
	name     string
	category string
	client   *qbit.Client
}

// reconcileClients holds the run's clients and a per-run hash lookup
// cache, so a season pack's rows (one hash) are looked up once.
type reconcileClients struct {
	entries  []reconcileClient
	resolved map[string]resolvedHash
}

// resolvedHash is a cached lookup: the entry holding the torrent (-1 =
// none) and its category at the time.
type resolvedHash struct {
	idx      int
	category string
}

// reconcileTargetsFromRequest merges ReconcileTargets with the legacy
// single-client pair, which is added only when it names a client the
// list doesn't cover (the UI sends both). A client repeated inside
// ReconcileTargets is kept so newReconcileClients can reject it.
func reconcileTargetsFromRequest(req scanRunRequest) []scanReconcileTarget {
	out := make([]scanReconcileTarget, 0, len(req.ReconcileTargets)+1)
	seen := map[string]bool{}
	for _, t := range req.ReconcileTargets {
		id := strings.TrimSpace(t.QbitInstanceID)
		if id == "" {
			continue
		}
		seen[id] = true
		out = append(out, scanReconcileTarget{QbitInstanceID: id, PostCategory: strings.TrimSpace(t.PostCategory)})
	}
	if id := strings.TrimSpace(req.ReconcileQbitInstanceID); id != "" && !seen[id] {
		out = append(out, scanReconcileTarget{QbitInstanceID: id, PostCategory: strings.TrimSpace(req.ReconcilePostCategory)})
	}
	return out
}

// newReconcileClients builds a qBit client per target. Any unusable
// target fails the run, so a client is never silently skipped.
func newReconcileClients(cfg core.Config, targets []scanReconcileTarget) (*reconcileClients, *apiError) {
	if len(targets) == 0 {
		return nil, newAPIError(400, "pick at least one qBittorrent client + target category to apply")
	}
	rc := &reconcileClients{resolved: map[string]resolvedHash{}}
	seen := map[string]bool{}
	for _, t := range targets {
		if seen[t.QbitInstanceID] {
			return nil, newAPIError(400, fmt.Sprintf("qBittorrent instance %q is listed twice — one target category per client", t.QbitInstanceID))
		}
		seen[t.QbitInstanceID] = true
		if t.PostCategory == "" {
			return nil, newAPIError(400, "a target category is required for every qBittorrent client")
		}
		inst := findQbitInstanceByID(cfg, t.QbitInstanceID)
		if inst == nil {
			return nil, newAPIError(400, fmt.Sprintf("qBittorrent instance %q not found in config", t.QbitInstanceID))
		}
		c, err := qbit.New(qbit.Config{URL: inst.URL, Username: inst.Username, Password: inst.Password, TrustedCerts: inst.TrustedCerts})
		if err != nil {
			return nil, newAPIError(502, "qbit client init ("+inst.Name+"): "+err.Error())
		}
		rc.entries = append(rc.entries, reconcileClient{name: inst.Name, category: t.PostCategory, client: c})
	}
	return rc, nil
}

// clientFor finds the client holding hash and the torrent's current
// category. Every client is asked, even when there is only one: qBit
// accepts setCategory for unknown hashes, so without the lookup a removed
// torrent would be reported as moved.
//
// found=false with a nil error means no client has it. An error means the
// answer can't be trusted: a client couldn't be asked, or the torrent is
// in more than one client (cross-seeding) and moving either copy could
// hit the wrong one.
func (rc *reconcileClients) clientFor(ctx context.Context, hash string) (entry reconcileClient, currentCategory string, found bool, err error) {
	key := strings.ToLower(strings.TrimSpace(hash))
	if key == "" {
		return reconcileClient{}, "", false, nil
	}
	if r, ok := rc.resolved[key]; ok {
		if r.idx < 0 {
			return reconcileClient{}, "", false, nil
		}
		return rc.entries[r.idx], r.category, true, nil
	}
	owner, ownerCategory := -1, ""
	var holders []string
	var probeErr error
	for i := range rc.entries {
		t, ok, gerr := rc.entries[i].client.GetTorrent(ctx, hash)
		if gerr != nil {
			if probeErr == nil {
				probeErr = fmt.Errorf("could not check download client %s: %w", rc.entries[i].name, gerr)
			}
			continue
		}
		if ok {
			if owner < 0 {
				owner, ownerCategory = i, t.Category
			}
			holders = append(holders, rc.entries[i].name)
		}
	}
	switch {
	case len(holders) > 1:
		return reconcileClient{}, "", false, fmt.Errorf("the same torrent is in several download clients (%s); move it by hand", strings.Join(holders, ", "))
	case probeErr != nil:
		// Not cached: a transient failure must not decide later rows.
		return reconcileClient{}, "", false, probeErr
	case owner >= 0:
		rc.resolved[key] = resolvedHash{idx: owner, category: ownerCategory}
		return rc.entries[owner], ownerCategory, true, nil
	default:
		rc.resolved[key] = resolvedHash{idx: -1}
		return reconcileClient{}, "", false, nil
	}
}

// names returns the client display names.
func (rc *reconcileClients) names() []string {
	out := make([]string, 0, len(rc.entries))
	for _, e := range rc.entries {
		out = append(out, e.name)
	}
	return out
}
