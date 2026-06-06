package engine

// reconcile.go — pure decision helper for the reconcile-stuck-downloads
// feature. Given a stuck queue item's release score and the current
// state of the target(s) it maps to, decide whether the stuck download
// is redundant (the target already has an equal/better file) or needs
// attention. No I/O — the queue read, target lookup, and qBit category
// change live in the API layer.

// StuckVerdict is the outcome of classifying a stuck download.
type StuckVerdict string

const (
	// StuckRedundant: every target already has a file scoring >= the
	// stuck item's score, so the stuck download is superseded (it
	// imported, or a better release did). Eligible to have its qBit
	// category changed and handed to the user's cleanup.
	StuckRedundant StuckVerdict = "redundant"
	// StuckNeedsAttention: a target has no file, or the imported file
	// scores below the stuck item (the stuck one is a genuine pending
	// upgrade). Surface it; never touch it automatically.
	StuckNeedsAttention StuckVerdict = "needs-attention"
)

// StuckTarget is the current state of one Arr item a stuck download maps
// to — a movie, or one episode of a pack.
type StuckTarget struct {
	HasFile       bool
	ImportedScore int
}

// ClassifyStuckDownload decides whether a stuck download is redundant.
// Redundant ONLY when there is at least one target AND every target
// already has a file scoring >= the stuck item's score. Any target
// missing a file, or scoring below the stuck item, yields
// needs-attention (never auto-acted). No targets (couldn't map the
// download to an Arr item) is also needs-attention — we can't prove the
// download is superseded, so we don't touch it.
func ClassifyStuckDownload(stuckScore int, targets []StuckTarget) StuckVerdict {
	if len(targets) == 0 {
		return StuckNeedsAttention
	}
	for _, t := range targets {
		if !t.HasFile || t.ImportedScore < stuckScore {
			return StuckNeedsAttention
		}
	}
	return StuckRedundant
}
