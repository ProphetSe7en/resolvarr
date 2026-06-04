package api

// webhook_qbit_category.go — qBit category-fix adapter for the
// M-Webhook dispatcher. Reconciles the pre→post-import category swap
// that Sonarr/Radarr normally drives at import-time but sometimes
// silently fails to apply (qBit API timeout, version-specific bug,
// import race). Fires on Connect Download events; never on Grab.
//
// Three-layer verification before mutating qBit (defence in depth — a
// false positive could land a stuck-on-pre-category torrent in the
// "imported" bucket without the file actually being imported):
//
//   Layer 1 — Payload sanity: movieFile / episodeFile populated +
//             non-empty downloadId (extractDownload returns OK +
//             ed.DownloadID != "").
//   Layer 2 — Arr's own history confirms an import landed:
//             GET /api/v3/history?downloadId=<id> + look for the
//             import-confirmation event (downloadFolderImported on
//             Radarr / episodeFileImported on Sonarr).
//   Layer 3 — qBit still has the torrent AND its current category
//             matches the pre-import name. If the torrent's gone (user
//             deleted) or the category is already correct (Arr did its
//             job), skip without touching qBit.
//
// Only when all three checks pass do we call qBit's SetTorrentCategory.
//
// Category names come from the Arr's live download-client config —
// fetched at fire-time via the 5-min cache. The rule's snapshot fields
// are the fallback if the live fetch fails (Arr unreachable, API key
// revoked, etc.).
//
// Architectural rule: the adapter NEVER decides categories itself.
// Pre/post category names always come from the Arr's download-client
// config (live or snapshot). No regex / inference / "guess from the
// torrent name" logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/qbit"
)

// dispatchQbitCategoryFix runs the import-confirmation + qBit-category-
// reconcile flow on Connect Download events. Returns OK=true with a
// descriptive summary on every clean path (skip-due-to-* / changed
// category). OK=false only on actual failures (qBit unreachable,
// malformed payload, missing instance, history-fetch error).
//
// Idempotent: layer 3's "category already matches post-import" branch
// makes Connect retries free — re-applying the fix is a no-op.
func (s *Server) dispatchQbitCategoryFix(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnQbitCategoryFix, OK: true, Summary: "skipped (not a Download event)"}
	}
	if rule.QbitCategoryFix == nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "rule has QbitCategoryFix function but no criteria struct",
		}
	}
	cfgRule := rule.QbitCategoryFix

	// Layer 1 — payload sanity.
	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "decode payload failed", Err: err,
		}
	}
	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: true,
			Summary: "skipped (no movieFile/episodeFile on event — not an import)",
		}
	}
	if ed.DownloadID == "" {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: true,
			Summary: "skipped (no downloadId on event — manual import?)",
		}
	}

	// Resolve Arr client. Instance must still exist (deleted between
	// receive and dispatch produces a clean error result).
	arrInst := findInstanceByID(cfg, rule.InstanceID)
	if arrInst == nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "instance vanished between event receive and dispatch",
		}
	}
	arrClient := s.arrClientFor(arrInst)

	// Layer 2 — verify via Arr's history that an import actually
	// landed. Some Connect setups emit Download events on rejected
	// imports too (older Radarr versions); the history walk is the
	// canonical "did this import succeed?" question.
	//
	// Race window: Sonarr/Radarr's NotificationService fires Connect
	// events from a separate goroutine than the history-row writer.
	// Connect event can arrive BEFORE the history INSERT commits on
	// busy systems. Single-shot lookup would skip these races and the
	// stuck category would never get fixed.
	//
	// Retry with exponential backoff covers the realistic race window
	// (~100ms-2s typically; we go to ~10s for safety). Mirrors the
	// waitForTorrent pattern Grab Rename + qBit S/E use on the qBit
	// side. If the import truly failed (rejected, not just delayed),
	// the history event never appears and we skip cleanly after the
	// retry budget.
	historyBackoffMs := []int{0, 500, 1500, 3000, 5000} // total ~10s
	var importedEvent *arr.HistoryRecord
	for attempt, delay := range historyBackoffMs {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return functionResult{
					Function: core.WebhookFnQbitCategoryFix, OK: false,
					Summary: "context cancelled during history retry", Err: ctx.Err(),
				}
			case <-time.After(time.Duration(delay) * time.Millisecond):
			}
		}
		history, err := arrClient.ListHistoryByDownloadID(ctx, ed.DownloadID)
		if err != nil {
			// Hard error (timeout, 5xx). Don't retry — fail loudly.
			return functionResult{
				Function: core.WebhookFnQbitCategoryFix, OK: false,
				Summary: fmt.Sprintf("fetch Arr history (attempt %d/%d)", attempt+1, len(historyBackoffMs)),
				Err:     err,
			}
		}
		importedEvent = arr.FindImportedEvent(history, rule.AppType)
		if importedEvent != nil {
			break
		}
	}
	// historyConfirmed = "Arr's history showed downloadFolderImported /
	// episodeFileImported within the retry budget". Even when this is
	// false we proceed to Layer 3 — qBit's own state is the real
	// answer to "did the import happen and was the category swapped?"
	// The history walk is supplementary, not gating.
	historyConfirmed := importedEvent != nil

	// Resolve pre/post categories — live fetch (cached 5min) with
	// snapshot fallback. Empty / equal values short-circuit before
	// touching qBit (validator catches the bad-config case at save-
	// time, but defence-in-depth for older saved rules).
	preCat, postCat := s.resolveQbitCategories(ctx, arrInst, arrClient, rule)
	if preCat == "" || postCat == "" {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "invalid category config (pre or post category empty after live + snapshot fallback)",
		}
	}
	if strings.EqualFold(preCat, postCat) {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: fmt.Sprintf("invalid category config (pre %q equals post %q)", preCat, postCat),
		}
	}

	// Resolve qBit instance + client.
	qbitInst := findQbitInstanceByID(cfg, cfgRule.QbitInstanceID)
	if qbitInst == nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: fmt.Sprintf("qbit instance %q not found in config", cfgRule.QbitInstanceID),
		}
	}
	qbitClient, err := qbit.New(qbit.Config{
		URL:          qbitInst.URL,
		Username:     qbitInst.Username,
		Password:     qbitInst.Password,
		TrustedCerts: qbitInst.TrustedCerts,
	})
	if err != nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "qbit client init", Err: err,
		}
	}

	// Layer 3a — qBit still has the torrent.
	torrent, found, err := waitForTorrent(ctx, qbitClient, ed.DownloadID)
	if err != nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "qbit GetTorrent", Err: err,
		}
	}
	if !found {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: true,
			Summary: fmt.Sprintf("skipped (torrent hash %s removed from qBit before fix)", ed.DownloadID),
		}
	}
	// Layer 3b — category already correct? Three subcases (state-based,
	// independent of whether history-walk confirmed import — qBit's
	// current state is the most reliable signal of what actually
	// happened):
	//   - matches post-import: Arr did its job, no-op.
	//   - matches neither pre- nor post-import: user customised the
	//     category manually; don't override.
	//   - still on pre-import: candidate for swap, but only if history
	//     confirmed the import (defensive — without confirmation we
	//     can't know whether to swap or wait).
	currentCat := torrent.Category
	if strings.EqualFold(currentCat, postCat) {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: true,
			Summary: fmt.Sprintf("skipped (category already %q — Arr completed the swap)", currentCat),
		}
	}
	if !strings.EqualFold(currentCat, preCat) {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: true,
			Summary: fmt.Sprintf("skipped (category %q matches neither pre %q nor post %q — leaving user-set value alone)", currentCat, preCat, postCat),
		}
	}
	// Pre-import category but no history confirmation. Skip with a
	// state-aware message instead of the old alarming "import may have
	// failed" text — the Connect Download event itself is import-
	// confirmation, so "import failed" was misleading. Most common
	// reasons: Arr's history INSERT lagged past our 10s budget, OR
	// Arr's history endpoint is slow / lagged on this instance.
	if !historyConfirmed {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: true,
			Summary: fmt.Sprintf("skipped (qBit still on pre-import category %q but Arr history-walk didn't confirm the downloadFolderImported event within 10s — likely Arr-history-lag, re-check qBit manually if this persists)", currentCat),
		}
	}

	// Layer 3c — grace window. Arr's history confirmed the import is
	// committed, but Arr's qBit-category-swap runs on a separate post-
	// commit goroutine and can take a few seconds. Without this grace
	// poll, resolvarr would race Arr to the swap and steal the work
	// even when Arr would have completed it normally in 2-5 seconds.
	//
	// Poll qBit's category with backoff up to ~10s. Break early when:
	//   - Category flips to post → Arr did its job, skip.
	//   - Category flips to anything else → user manually changed, skip.
	//
	// Only swap when the category STAYS on pre-import through the
	// entire budget — that's the genuinely-stuck case the function
	// exists to fix.
	graceBackoffMs := []int{2000, 3000, 5000} // total ~10s
	for _, delay := range graceBackoffMs {
		select {
		case <-ctx.Done():
			return functionResult{
				Function: core.WebhookFnQbitCategoryFix, OK: false,
				Summary: "context cancelled during grace poll", Err: ctx.Err(),
			}
		case <-time.After(time.Duration(delay) * time.Millisecond):
		}
		recheck, recheckFound, recheckErr := qbitClient.GetTorrent(ctx, ed.DownloadID)
		if recheckErr != nil {
			// Transient qBit error mid-poll — treat as "still pre", let
			// the next iteration retry. If the final SetTorrentCategory
			// also fails, that surfaces the real qBit problem.
			continue
		}
		if !recheckFound {
			return functionResult{
				Function: core.WebhookFnQbitCategoryFix, OK: true,
				Summary: fmt.Sprintf("skipped (torrent hash %s removed from qBit during grace poll)", ed.DownloadID),
			}
		}
		recheckCat := recheck.Category
		if strings.EqualFold(recheckCat, postCat) {
			return functionResult{
				Function: core.WebhookFnQbitCategoryFix, OK: true,
				Summary: fmt.Sprintf("skipped (category became %q during grace window — Arr completed the swap)", recheckCat),
			}
		}
		if !strings.EqualFold(recheckCat, preCat) {
			return functionResult{
				Function: core.WebhookFnQbitCategoryFix, OK: true,
				Summary: fmt.Sprintf("skipped (category changed to %q during grace window — user customised manually)", recheckCat),
			}
		}
		// Still on pre — continue polling.
	}

	// All three layers + grace window pass. Apply the post-import
	// category — Arr genuinely dropped the ball.
	if err := qbitClient.SetTorrentCategory(ctx, ed.DownloadID, postCat); err != nil {
		return functionResult{
			Function: core.WebhookFnQbitCategoryFix, OK: false,
			Summary: "qbit set category", Err: err,
		}
	}
	return functionResult{
		Function: core.WebhookFnQbitCategoryFix, OK: true,
		Summary: fmt.Sprintf("changed category %q → %q (Arr did not complete the swap within ~10s grace window)", preCat, postCat),
	}
}

// resolveQbitCategories returns the pre/post category names for the
// rule. Tries the live fetch from the Arr's download-client config
// first (cached 5min); falls back to the rule's snapshot fields if the
// live fetch fails OR if the matched download-client entry doesn't
// have both pre + post populated.
//
// Empty-return ("", "") means "neither live nor snapshot produced a
// usable pair" — the adapter treats that as a hard error rather than a
// silent skip (the rule is mis-configured and the user should know).
func (s *Server) resolveQbitCategories(
	ctx context.Context,
	arrInst *core.Instance,
	arrClient *arr.Client,
	rule *core.WebhookRule,
) (preCat, postCat string) {
	cfgRule := rule.QbitCategoryFix
	if cfgRule == nil {
		return "", ""
	}
	// Live fetch via the per-instance cache.
	clients, err := s.ArrDLCache().Get(ctx, arrInst, arrClient)
	if err == nil {
		for i := range clients {
			if clients[i].ID != cfgRule.ArrDownloadClientID {
				continue
			}
			pre := clients[i].QbitPreImportCategory(rule.AppType)
			post := clients[i].QbitPostImportCategory(rule.AppType)
			if pre != "" && post != "" {
				return pre, post
			}
			// Matched ID but one or both categories empty — fall
			// through to snapshot. The user may have un-set a category
			// in Sonarr/Radarr after creating the rule; snapshot
			// preserves the original intent for the duration of the
			// rule's life.
			break
		}
	}
	// Snapshot fallback. Empty strings still possible if the snapshot
	// was never populated (rule saved before snapshot fields landed —
	// shouldn't happen via the validator, but defensive).
	return cfgRule.PreImportCategorySnapshot, cfgRule.PostImportCategorySnapshot
}

// categoryFixDeferDelay resolves how long after the import event the
// deferred category-fix runs. 0 / unset → 30s default; clamped to 600s.
func (s *Server) categoryFixDeferDelay(rule *core.WebhookRule) time.Duration {
	secs := 30
	if rule.QbitCategoryFix != nil && rule.QbitCategoryFix.DeferSeconds > 0 {
		secs = rule.QbitCategoryFix.DeferSeconds
	}
	if secs > 600 {
		secs = 600
	}
	return time.Duration(secs) * time.Second
}

// scheduleDeferredCategoryFix runs the qBit category-fix in the
// background `delay` after the import event, instead of inline in the
// Connect webhook dispatch. The webhook response returns immediately so
// Sonarr/Radarr's import notification never blocks on the fix's
// history-retry + grace-poll (which serialised per-file imports on a
// season pack). The deferred run records its own single-function history
// entry + notification when it completes.
//
// Inputs are snapshotted (the request body + structs must survive past
// the HTTP response; config is re-read fresh at fire-time). Pending
// timers are lost on container restart — acceptable for a best-effort
// workaround for an Arr-side category-swap bug.
func (s *Server) scheduleDeferredCategoryFix(rule *core.WebhookRule, inst *core.Instance, env *connectEventEnvelope, body []byte) {
	if rule == nil || inst == nil || env == nil {
		return
	}
	delay := s.categoryFixDeferDelay(rule)
	ruleCopy := *rule
	instCopy := *inst
	envCopy := *env
	bodyCopy := append([]byte(nil), body...)
	started := time.Now().UTC()
	time.AfterFunc(delay, func() {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Fprintf(os.Stderr, "resolvarr: deferred qBit category-fix panicked for rule %s: %v\n", ruleCopy.ID, rec)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cfg := s.App.Config.Get()
		res := s.dispatchQbitCategoryFix(ctx, &ruleCopy, cfg, &envCopy, bodyCopy)
		run := buildWebhookRuleRun(&envCopy, bodyCopy, started, []functionResult{res})
		s.fireWebhookNotifications(&ruleCopy, &instCopy, &envCopy, bodyCopy, []functionResult{res}, run)
		s.appendWebhookRuleRunsBatch([]pendingRuleRun{{ruleID: ruleCopy.ID, run: run}})
	})
}
