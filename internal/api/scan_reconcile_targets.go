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
// so a season pack's N queue rows sharing one hash probe once. With a
// single configured client the probe is skipped entirely — there is
// nothing to disambiguate, and skipping keeps the single-client path
// byte-identical to its pre-fan-out behaviour.

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
	// resolved memoises hash → index into entries. -1 records "probed
	// every client, nobody has it" so a season pack doesn't re-probe
	// once per episode row.
	resolved map[string]int
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
		if id == "" || seen[id] {
			continue
		}
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
	rc := &reconcileClients{resolved: map[string]int{}}
	for _, t := range targets {
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

// clientFor returns the download client holding hash. With exactly one
// configured client it returns that client without probing (nothing to
// disambiguate, and the SetCategory call below surfaces a genuinely
// missing hash as its own error). With several, it asks each in turn
// and memoises the answer.
//
// found=false means "no configured client has this hash" — the caller
// reports it on the row rather than guessing a client.
func (rc *reconcileClients) clientFor(ctx context.Context, hash string) (entry reconcileClient, found bool) {
	if len(rc.entries) == 1 {
		return rc.entries[0], true
	}
	key := strings.ToLower(strings.TrimSpace(hash))
	if key == "" {
		return reconcileClient{}, false
	}
	if idx, ok := rc.resolved[key]; ok {
		if idx < 0 {
			return reconcileClient{}, false
		}
		return rc.entries[idx], true
	}
	probeFailed := false
	for i := range rc.entries {
		_, ok, err := rc.entries[i].client.GetTorrent(ctx, hash)
		if err != nil {
			// Client down / auth expired. Treat as "not here" for this
			// pass but remember that the answer is incomplete.
			probeFailed = true
			continue
		}
		if ok {
			rc.resolved[key] = i
			return rc.entries[i], true
		}
	}
	// Only memoise a negative answer when every client actually
	// answered. Caching "nobody has it" off the back of a transient
	// blip on one client would mis-route every later row sharing this
	// hash, and on a season pack that is every episode.
	if !probeFailed {
		rc.resolved[key] = -1
	}
	return reconcileClient{}, false
}

// names returns the configured client display names, for summaries.
func (rc *reconcileClients) names() []string {
	out := make([]string, 0, len(rc.entries))
	for _, e := range rc.entries {
		out = append(out, e.name)
	}
	return out
}
