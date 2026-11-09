package api

// scan_reconcile_targets.go — download-client fan-out for the
// reconcile-stuck-downloads apply path.
//
// A single Sonarr/Radarr instance can have several qBittorrent download
// clients attached (split by content type, or a VPN'd client alongside a
// direct one), and /api/v3/queue returns rows from ALL of them in one
// list. The original apply took one qBit instance + one category, so a
// stuck download living in the OTHER client either errored or — worse —
// silently matched nothing.
//
// reconcileClients resolves each stuck hash to the client that actually
// holds it and applies that client's own target category. Lookup is by
// hash against qBit itself rather than by matching the Arr's download-
// client NAME to a resolvarr instance name: names drift, hashes don't.
//
// Cost: one GetTorrent per (hash, client) until a hit, memoised per run
// so a season pack's N queue rows sharing one hash probe once. The probe
// also runs with a single configured client: it is what tells a torrent
// the user already removed (qBit accepts setCategory for unknown hashes
// silently) and one an earlier run already moved apart from a real move.

import (
	"context"
	"fmt"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/qbit"
)

// reconcileClient is one resolved download client for an apply run.
type reconcileClient struct {
	name     string
	category string
	client   *qbit.Client
}

// reconcileClients is the ordered set of download clients an apply run
// may act on, plus the per-run hash→client memo.
type reconcileClients struct {
	entries []reconcileClient
	// resolved memoises hash → owning client. idx -1 records "probed
	// every client, nobody has it" so a season pack doesn't re-probe
	// once per episode row.
	resolved map[string]resolvedHash
}

// resolvedHash is one memoised lookup: which entry holds the torrent
// (-1 = none) and the category it was in when asked.
type resolvedHash struct {
	idx      int
	category string
}

// reconcileTargetsFromRequest folds the request's two target shapes into
// one ordered list: the multi-client ReconcileTargets, plus the legacy
// single (ReconcileQbitInstanceID, ReconcilePostCategory) pair appended
// when it names a client the list doesn't already cover.
//
// Order matters only as probe order, which is arbitrary — a hash lives
// in exactly one client.
func reconcileTargetsFromRequest(req scanRunRequest) []scanReconcileTarget {
	out := make([]scanReconcileTarget, 0, len(req.ReconcileTargets)+1)
	seen := map[string]bool{}
	for _, t := range req.ReconcileTargets {
		id := strings.TrimSpace(t.QbitInstanceID)
		if id == "" {
			continue
		}
		// A client listed twice in ReconcileTargets is kept as-is so
		// newReconcileClients rejects it: two categories for one client
		// have no defined winner. Only the legacy pair below is folded
		// silently, because the UI sends it alongside the full list.
		seen[id] = true
		out = append(out, scanReconcileTarget{
			QbitInstanceID: id,
			PostCategory:   strings.TrimSpace(t.PostCategory),
		})
	}
	legacyID := strings.TrimSpace(req.ReconcileQbitInstanceID)
	if legacyID != "" && !seen[legacyID] {
		out = append(out, scanReconcileTarget{
			QbitInstanceID: legacyID,
			PostCategory:   strings.TrimSpace(req.ReconcilePostCategory),
		})
	}
	return out
}

// newReconcileClients resolves every target to a live qBit client.
// Returns an apiError when a target is unusable — a run that silently
// dropped one of the user's clients would look like it succeeded while
// leaving that client's stuck downloads untouched.
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
		c, err := qbit.New(qbit.Config{
			URL:          inst.URL,
			Username:     inst.Username,
			Password:     inst.Password,
			TrustedCerts: inst.TrustedCerts,
		})
		if err != nil {
			return nil, newAPIError(502, "qbit client init ("+inst.Name+"): "+err.Error())
		}
		rc.entries = append(rc.entries, reconcileClient{
			name:     inst.Name,
			category: t.PostCategory,
			client:   c,
		})
	}
	return rc, nil
}

// clientFor returns the download client holding hash, plus the
// torrent's current category there. Every configured client is asked in
// turn — including when there is only one: qBittorrent answers 200 to a
// setCategory for a hash it doesn't have, so skipping the lookup would
// report a torrent the user already removed as "moved", and the current
// category is what lets the caller skip a torrent an earlier run already
// moved. The answer is memoised per run, so a season pack's N rows
// sharing one hash cost one lookup.
//
// found=false means "no configured client has this hash". probeErr is
// non-nil when at least one client could not be asked, in which case a
// not-found answer is incomplete and the caller should say so rather
// than report the torrent as gone.
func (rc *reconcileClients) clientFor(ctx context.Context, hash string) (entry reconcileClient, currentCategory string, found bool, probeErr error) {
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
	// Every client is asked, not just until the first hit: the same
	// torrent can sit in two clients (cross-seeding), and then there is
	// no telling which copy the Arr's queue row is about. Moving the
	// first one found could recategorise a copy the user still needs.
	owner := -1
	ownerCategory := ""
	var holders []string
	for i := range rc.entries {
		t, ok, err := rc.entries[i].client.GetTorrent(ctx, hash)
		if err != nil {
			// Client down / auth expired. Treat as "not here" for this
			// pass but remember that the answer is incomplete.
			if probeErr == nil {
				probeErr = fmt.Errorf("could not check download client %s: %w", rc.entries[i].name, err)
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
	if len(holders) > 1 {
		return reconcileClient{}, "", false, fmt.Errorf("the same torrent is in several download clients (%s); move it by hand", strings.Join(holders, ", "))
	}
	if owner >= 0 && probeErr == nil {
		rc.resolved[key] = resolvedHash{idx: owner, category: ownerCategory}
		return rc.entries[owner], ownerCategory, true, nil
	}
	if owner >= 0 {
		// Found, but another client couldn't be asked — it might hold
		// a second copy. Not safe to pick one.
		return reconcileClient{}, "", false, probeErr
	}
	// Only memoise a negative answer when every client actually
	// answered. Caching "nobody has it" off the back of a transient
	// blip on one client would mis-route every later row sharing this
	// hash, and on a season pack that is every episode.
	if probeErr == nil {
		rc.resolved[key] = resolvedHash{idx: -1}
	}
	return reconcileClient{}, "", false, probeErr
}

// names returns the configured client display names, for summaries.
func (rc *reconcileClients) names() []string {
	out := make([]string, 0, len(rc.entries))
	for _, e := range rc.entries {
		out = append(out, e.name)
	}
	return out
}
