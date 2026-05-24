// webhook_notify_sections.go — per-function embed section builders.
//
// Each function family (Tag-Q-R, Auto-tags, Discover, Recover, Grab
// Rename, qBit S/E, qBit Category Fix, File Delete) produces a
// section of embed fields rendered as agents.PayloadField entries.
// Section builders read the typed Detail payload populated by the
// adapter (webhook_dispatch.go's canonical chain) and translate it
// into plain-language fields like "Sound: TrueHD Atmos 7.1".
//
// User-locked rules honoured by every section:
//
//  1. Only actual changes. A section is omitted entirely when the
//     adapter didn't populate Detail (legacy / unwired) OR the
//     populated Detail has no concrete values to render. No
//     "did not happen" lines, no "skipped because X" stubs.
//
//  2. Plain language, not jargon. Field names ("Sound", "Picture",
//     "Was", "Now", "Client") never use internal terms ("audio bucket",
//     "WebhookFnTagAudio", "Detail.PlainSummary"). The Adapter
//     pre-formats values; the builder just places them.
//
//  3. Bundling matches bash tagarr_import.sh. Tag-RG + Audio + Video
//     + DV all fire on one Download → ONE bundled section. Grab
//     Rename always gets its own section (different event class).
//     File-delete bundles the per-bucket strip + auto-strip Tag-RG
//     into one section.
//
//  4. Discord-friendly. Short values inline (3 per row at typical
//     client width); long values (torrent names, file paths) take
//     their own row.
//
// Per dev/analysis/M-webhook-notifications.md, the dispatcher (task #7)
// will call composeFields() once per fire to produce the
// agents.Payload.Fields slice.

package api

import (
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// filterResultsByFunctions reduces a results slice to only those
// entries whose Function is in `allowedFunctions`. Empty/nil filter
// = no filtering ("all functions" — the backward-compat default
// matching agents.Agent.Functions semantics).
//
// Shared by composeFields, composeTitle, and pickColor so a single
// agent's view of the fire is consistent across title, color, and
// embed body — the title combo can't include functions whose
// section will be filtered out, etc.
func filterResultsByFunctions(results []functionResult, allowedFunctions []string) []functionResult {
	if len(allowedFunctions) == 0 {
		return results
	}
	allowed := make(map[string]bool, len(allowedFunctions))
	for _, f := range allowedFunctions {
		allowed[f] = true
	}
	out := make([]functionResult, 0, len(results))
	for _, r := range results {
		if allowed[string(r.Function)] {
			out = append(out, r)
		}
	}
	return out
}

// displayBucketForEngine remaps the engine's per-sub-bucket names
// to the three display-buckets the FileDeleteDetail section renders
// ("audio" / "video" / "dv"). The engine emits finer-grained bucket
// names ("resolution" / "codec" / "hdr" for the video family,
// "dvdetail" for DV) which would otherwise leak through to the embed
// as five separate labels — the user thinks in three buckets
// regardless of the engine's internal sub-categorisation. Unknown
// engine buckets pass through unchanged so a future bucket addition
// surfaces visibly rather than silently disappearing.
func displayBucketForEngine(engineBucket string) string {
	switch engineBucket {
	case "audio":
		return "audio"
	case "resolution", "codec", "hdr":
		return "video"
	case "dvdetail":
		return "dv"
	}
	return engineBucket
}

// composeFields walks the per-rule fire results and produces the
// ordered list of embed fields for the agents.Payload. Order follows
// the user's visual scan path: what action was performed first (Tag
// + Auto-tags as the headline), auxiliary context next (Discover /
// Recover), qBit-side actions, then delete-cleanup. Mirrors the
// titleDisplayOrder grouping.
//
// The orchestrator collects typed details from every changed result
// then delegates to per-section append helpers. Each helper short-
// circuits on nil/empty inputs, so a fire with only Tag-RG produces
// the Tag section + nothing else — no empty headers, no padding.
//
// `allowedFunctions` is the per-agent Functions whitelist (from
// agents.Agent.Functions). Empty/nil = no filter ("all functions");
// non-empty = render only results whose Function is in the list.
// Each agent gets its own composeFields call with its own filter so
// the embed body is tailored to what THAT agent subscribed to.
func composeFields(event core.WebhookConnectEvent, results []functionResult, allowedFunctions []string, instanceName, ruleName, filename string) []agents.PayloadField {
	results = filterResultsByFunctions(results, allowedFunctions)
	// Local names use `rec` for RecoverDetail to avoid shadowing
	// the built-in `recover()` (vet -predeclared flags it; a future
	// `defer func() { recover() }()` added to this scope would
	// silently fail).
	var tag *TagDetail
	var audio *AudioDetail
	var video *VideoDetail
	var dv *DvDetail
	var discover *DiscoverDetail
	var rec *RecoverDetail
	var grab *GrabRenameDetail
	var qbitSe *QbitSeDetail
	var qbitCat *QbitCategoryFixDetail
	var fileDel *FileDeleteDetail

	for _, r := range results {
		if !r.Changed {
			continue
		}
		switch r.Function {
		case core.WebhookFnTagReleaseGroups:
			if d, ok := r.Detail.(TagDetail); ok {
				tag = &d
			}
		case core.WebhookFnTagAudio:
			if d, ok := r.Detail.(AudioDetail); ok {
				audio = &d
			}
		case core.WebhookFnTagVideo:
			if d, ok := r.Detail.(VideoDetail); ok {
				video = &d
			}
		case core.WebhookFnTagDvDetail:
			if d, ok := r.Detail.(DvDetail); ok {
				dv = &d
			}
		case core.WebhookFnDiscover:
			if d, ok := r.Detail.(DiscoverDetail); ok {
				discover = &d
			}
		case core.WebhookFnRecover:
			if d, ok := r.Detail.(RecoverDetail); ok {
				rec = &d
			}
		case core.WebhookFnGrabRename:
			if d, ok := r.Detail.(GrabRenameDetail); ok {
				grab = &d
			}
		case core.WebhookFnQbitSeTag:
			if d, ok := r.Detail.(QbitSeDetail); ok {
				qbitSe = &d
			}
		case core.WebhookFnQbitCategoryFix:
			if d, ok := r.Detail.(QbitCategoryFixDetail); ok {
				qbitCat = &d
			}
		}
		// File-delete details: TWO dispatchers fire on the same
		// delete event (per-bucket strip via dispatchFileDeleteCleanup
		// + Tag-RG strip via dispatchAutoStripTagRgOnDelete) and each
		// emits its OWN FileDeleteDetail covering its scope. Merge
		// here so the embed renders one consolidated section. First
		// match seeds the struct; subsequent matches union PerBucket +
		// take first-non-empty for scalars (only one dispatcher
		// populates each scalar in practice).
		if d, ok := r.Detail.(FileDeleteDetail); ok {
			if fileDel == nil {
				cp := d
				if cp.PerBucket != nil {
					// Defensive deep-copy so we don't mutate the
					// adapter's map when merging below.
					merged := make(map[string][]string, len(cp.PerBucket))
					for k, v := range cp.PerBucket {
						merged[k] = append([]string(nil), v...)
					}
					cp.PerBucket = merged
				}
				fileDel = &cp
			} else {
				for bucket, tags := range d.PerBucket {
					if fileDel.PerBucket == nil {
						fileDel.PerBucket = make(map[string][]string)
					}
					fileDel.PerBucket[bucket] = append(fileDel.PerBucket[bucket], tags...)
				}
				if fileDel.TagRgRemoved == "" {
					fileDel.TagRgRemoved = d.TagRgRemoved
				}
				if fileDel.Primary == "" {
					fileDel.Primary = d.Primary
				}
				if d.MirroredSecondary {
					fileDel.MirroredSecondary = true
				}
				if fileDel.SecondaryName == "" {
					fileDel.SecondaryName = d.SecondaryName
				}
			}
		}
	}

	// Cross-result coordination: fold Sync info into the Tag section.
	// Sync's Detail carries only the SecondaryName because the section
	// builder reads Mirrored + SecondaryName off TagDetail (single
	// "Tagged in: primary · secondary" line). When both Tag-RG and
	// Sync fired with Changed=true, mark Mirrored + populate Secondary
	// Name so the Tag section reads as a complete sentence.
	if tag != nil {
		for _, r := range results {
			if r.Function != core.WebhookFnSyncToSecondary || !r.Changed {
				continue
			}
			// Only flip Mirrored when the Sync result also carries a
			// SyncDetail. A defensive mismatch (wrong Detail type for
			// the function) would otherwise produce Mirrored=true
			// with empty SecondaryName, rendering "primary · secondary"
			// fallback — inconsistent with the actual fire state.
			if d, ok := r.Detail.(SyncDetail); ok {
				tag.Mirrored = true
				tag.SecondaryName = d.SecondaryName
			}
			break
		}
	}

	// Resolve the mirrored-secondary name once. Used by the
	// universal "Tagged in" line at the top of non-delete embeds so
	// auto-tag-only events (where no TagDetail exists) still surface
	// the mirror target. Falls back to TagDetail's own mirrored info
	// when TagDetail is present, which keeps the legacy Tag-RG +
	// Sync path unchanged.
	var mirroredSecondary string
	for _, r := range results {
		if r.Function != core.WebhookFnSyncToSecondary || !r.Changed {
			continue
		}
		if d, ok := r.Detail.(SyncDetail); ok {
			mirroredSecondary = strings.TrimSpace(d.SecondaryName)
		}
		break
	}
	if mirroredSecondary == "" && tag != nil && tag.Mirrored {
		mirroredSecondary = strings.TrimSpace(tag.SecondaryName)
	}

	// Defence-in-depth: if the caller didn't thread instanceName
	// (e.g. legacy tests or future call paths that forgot to pass
	// inst.Name) fall back to whatever the Detail layer populated.
	// Production callers should always pass inst.Name explicitly —
	// the fallbacks here just avoid a "primary Arr" placeholder
	// landing in real embeds when the data is sitting right there
	// on Detail.
	resolvedInstance := strings.TrimSpace(instanceName)
	if resolvedInstance == "" {
		if tag != nil {
			resolvedInstance = strings.TrimSpace(tag.Primary)
		}
		if resolvedInstance == "" && fileDel != nil {
			resolvedInstance = strings.TrimSpace(fileDel.Primary)
		}
	}

	// Build detail sections first into a separate slice so the
	// universal "Tagged in" lead + "Rule"/"Event"/"Filename" suffix
	// only emit when at least one detail field was added. This keeps
	// the legacy "empty fire → empty embed" contract: a results
	// slice with no Changed=true entries (or filtered out by the
	// agent's Functions whitelist) returns nil rather than emitting
	// a metadata-only embed.
	var detail []agents.PayloadField
	if isDeleteEvent(event) {
		// On delete events, the per-bucket strip + Tag-RG strip
		// collapse into one File-Delete section. The Tag /
		// Auto-tag / Discover / Recover sections are unreachable
		// per EventsForFunction — but defence-in-depth: skip them
		// here too if they somehow arrived.
		detail = appendFileDeleteSection(detail, fileDel)
	} else {
		// Non-delete events: build detail sections in user-scan
		// order. Tag section now emits only the Quality tag
		// (Tagged-in moved out to the universal lead below).
		detail = appendTagSection(detail, tag)
		detail = appendAutoTagsSection(detail, audio, video, dv)
		detail = appendDiscoverSection(detail, discover)
		detail = appendRecoverSection(detail, rec)
		detail = appendGrabRenameSection(detail, grab)
		detail = appendQbitSeSection(detail, qbitSe)
		detail = appendQbitCategoryFixSection(detail, qbitCat)
	}

	if len(detail) == 0 {
		return nil
	}

	var fields []agents.PayloadField
	// Universal "Tagged in" lead — non-Grab, non-delete events get
	// the instance name surfaced as the first row. Grab events
	// integrate the instance into the Grab Rename section's
	// "Renamed in"; delete events use the File-Delete section's
	// own "Cleaned in" line.
	if !isDeleteEvent(event) && event != core.WebhookEventGrab {
		fields = appendTaggedInSection(fields, resolvedInstance, mirroredSecondary)
	}
	fields = append(fields, detail...)
	fields = appendRuleSection(fields, ruleName)
	fields = appendEventFilenameSection(fields, event, filename)
	return fields
}

// appendTaggedInSection emits the universal "Tagged in: <instance>"
// line at the top of non-delete embeds. Always called for Import/
// Auto-tag events so the user knows WHERE the change landed
// regardless of which function families fired — fixes the legacy
// gap where auto-tag-only events (Audio + Video + DV with no
// Tag-RG) surfaced no instance context at all.
//
//	Tagged in    Radarr Movies
//	Tagged in    Radarr Movies · Radarr 4K   (mirrored)
//
// Always non-inline so the instance name has its own row — keeps
// the visual hierarchy "where → what" stable across single- and
// multi-function fires.
func appendTaggedInSection(fields []agents.PayloadField, primary, mirroredSecondary string) []agents.PayloadField {
	value := buildInstanceList(primary, mirroredSecondary != "", mirroredSecondary)
	if value == "" || value == "primary Arr" {
		// Empty primary name produces the generic fallback; suppress
		// the field entirely rather than emit "Tagged in: primary Arr"
		// — the user knows nothing concrete and the embed reads as
		// a debug stub.
		return fields
	}
	fields = append(fields, agents.PayloadField{
		Name:   "Tagged in",
		Value:  value,
		Inline: false,
	})
	return fields
}

// appendTagSection renders the release-group / filter-only tag
// outcome. Shown on Download events; on delete events the Tag-RG
// strip is folded into the File-Delete section (see
// appendFileDeleteSection).
//
//	Quality tag  FLUX
//
// "Tagged in" moved out to the universal appendTaggedInSection so
// every non-delete embed surfaces the instance regardless of which
// function families fired. This section now carries only what's
// Tag-RG-specific: the actual tag(s) added.
func appendTagSection(fields []agents.PayloadField, d *TagDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	// "Quality tag" line — the tag(s) added on this fire. The Added
	// slice is the post-change state from the user's perspective;
	// Removed lands in History but not in the embed (the user cares
	// what's tagged NOW, not what was unchecked).
	if tag := joinNonEmpty(d.Added, " · "); tag != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Quality tag",
			Value:  tag,
			Inline: true,
		})
	}
	return fields
}

// appendAutoTagsSection renders the bundled Audio + Video + DV
// detail outcome:
//
//	Sound         TrueHD Atmos 7.1
//	Picture       4K · HDR · Dolby Vision
//	Dolby Vision  Profile 7 · Layer 7.1
//
// Each sub-bucket is shown only when its PlainSummary is non-empty
// (Changed=true alone isn't sufficient — a future adapter could
// legitimately produce an empty PlainSummary if no user-facing tag
// landed). Inline so a typical "Sound + Picture" packs side-by-side.
//
// DV is its own row when present because the Picture line is often
// already long ("4K · HEVC · HDR10+"). Separating DV detail keeps
// each line readable.
func appendAutoTagsSection(fields []agents.PayloadField, a *AudioDetail, v *VideoDetail, dv *DvDetail) []agents.PayloadField {
	if a != nil {
		if s := strings.TrimSpace(a.PlainSummary); s != "" {
			fields = append(fields, agents.PayloadField{
				Name:   "Sound",
				Value:  s,
				Inline: true,
			})
		}
	}
	if v != nil {
		if s := strings.TrimSpace(v.PlainSummary); s != "" {
			fields = append(fields, agents.PayloadField{
				Name:   "Picture",
				Value:  s,
				Inline: true,
			})
		}
	}
	if dv != nil {
		if s := strings.TrimSpace(dv.PlainSummary); s != "" {
			fields = append(fields, agents.PayloadField{
				Name:   "Dolby Vision",
				Value:  s,
				Inline: true,
			})
		}
	}
	return fields
}

// appendDiscoverSection renders the discovery outcome — a new
// release group landed in the user's Active list (or commented for
// manual review).
//
//	New group   FLUX
//
// "Auto-active" was deliberately dropped: if the group landed as
// active, the next tag-fire will surface that via Quality tag; if
// it landed disabled-for-review (default), it's not embed-worthy
// noise. The user's "only actual changes" rule favours brevity.
// AutoEnabled is still on DiscoverDetail for History debugging.
func appendDiscoverSection(fields []agents.PayloadField, d *DiscoverDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	group := strings.TrimSpace(d.NewGroup)
	if group == "" {
		return fields
	}
	fields = append(fields, agents.PayloadField{
		Name:   "New group",
		Value:  group,
		Inline: true,
	})
	return fields
}

// appendRecoverSection renders the release-group-recovery outcome:
// the engine backfilled a missing release-group from the Arr's grab
// history.
//
//	Recovered    FLUX
//	Source       grab history
func appendRecoverSection(fields []agents.PayloadField, d *RecoverDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	group := strings.TrimSpace(d.RecoveredGroup)
	if group == "" {
		return fields
	}
	fields = append(fields, agents.PayloadField{
		Name:   "Recovered",
		Value:  group,
		Inline: true,
	})
	if src := strings.TrimSpace(d.Source); src != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Source",
			Value:  src,
			Inline: true,
		})
	}
	return fields
}

// appendGrabRenameSection renders the qBittorrent-side torrent
// rename outcome — bash tagarr_import.sh:452-489 field shape with
// the user-friendly "Torrent Name / Restored to Release Name"
// wording rather than the earlier compact "Was / Now".
//
//	Renamed in                  qBit Movies
//	Release Group Recovered     FLUX                (when GroupRecovered set)
//	Tokens Recovered            Director's Cut · IMAX (when TokensRecovered)
//	⚠ Scene CF                  No longer matches after rename
//	Torrent Name                Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR
//	Restored to Release Name    Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR-FLUX
//
// Torrent Name + Restored to Release Name are both inline:false so
// they stack vertically with the same left-edge — makes the
// before/after diff scannable without horizontal offset.
//
// ⚠ Scene CF warning surfaces only when the rename changed CF
// matching (worth user review — affects Radarr scoring).
func appendGrabRenameSection(fields []agents.PayloadField, d *GrabRenameDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	from := strings.TrimSpace(d.From)
	to := strings.TrimSpace(d.To)
	if from == "" && to == "" {
		return fields
	}
	if qbit := strings.TrimSpace(d.QbitInstance); qbit != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Renamed in",
			Value:  qbit,
			Inline: false,
		})
	}
	if group := strings.TrimSpace(d.GroupRecovered); group != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Release Group Recovered",
			Value:  group,
			Inline: true,
		})
	}
	if tokens := joinNonEmpty(d.TokensRecovered, " · "); tokens != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Tokens Recovered",
			Value:  tokens,
			Inline: true,
		})
	}
	if d.SceneCFChanged {
		fields = append(fields, agents.PayloadField{
			Name:   "⚠ Scene CF",
			Value:  "No longer matches after rename",
			Inline: false,
		})
	}
	if from != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Torrent Name",
			Value:  from,
			Inline: false,
		})
	}
	if to != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Restored to Release Name",
			Value:  to,
			Inline: false,
		})
	}
	return fields
}

// appendRuleSection emits the "Rule" field surfacing which webhook
// rule produced this notification. Replaces the previous
// FooterSuffix that crammed " · rule: X" onto the footer line
// alongside the version string — moving rule into the body keeps
// the footer clean for the embed timestamp and gives rule its own
// visual weight when multi-rule users need to attribute the fire.
//
//	Rule    4K imports
//
// Inline:false to keep the rule name on its own row even when
// short — visually consistent regardless of rule-name length.
// Empty ruleName suppresses the field entirely.
func appendRuleSection(fields []agents.PayloadField, ruleName string) []agents.PayloadField {
	name := strings.TrimSpace(ruleName)
	if name == "" {
		return fields
	}
	fields = append(fields, agents.PayloadField{
		Name:   "Rule",
		Value:  name,
		Inline: false,
	})
	return fields
}

// appendEventFilenameSection emits the universal Event + Filename
// suffix at the bottom of every embed. Mirrors bash
// tagarr_import.sh:1414-1421's "Event + Filename" tail block.
//
//	Event       Import
//	Filename    The.Substance.2024.2160p.WEB-DL.DV.HDR-FLUX.mkv
//
// Event is inline:true so on Grab events (where Filename is
// empty — file not imported yet) the row collapses gracefully.
// Filename is inline:false because import paths can be long and
// would wrap awkwardly in an inline slot.
//
// Empty event-label OR unknown event suppresses the Event field.
// Empty filename suppresses the Filename field. A Grab event with
// no relativePath simply omits Filename and only shows Event.
func appendEventFilenameSection(fields []agents.PayloadField, event core.WebhookConnectEvent, filename string) []agents.PayloadField {
	if label := eventLabel(event); label != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Event",
			Value:  label,
			Inline: true,
		})
	}
	if name := strings.TrimSpace(filename); name != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Filename",
			Value:  name,
			Inline: false,
		})
	}
	return fields
}

// appendQbitSeSection renders the qBittorrent Season/Episode
// classification outcome (Sonarr-only).
//
//	Tag           Episode
//	Type          Episode
//	Client        qBit TV
//
// "Type" repeats Tag when they match (the common case). The Tag is
// what's WRITTEN to qBit; the Type is the engine's classification.
// If a future adapter diverges them (e.g. classify as "Season pack"
// but write tag "Season"), both surface — the user wants to see
// both decisions.
func appendQbitSeSection(fields []agents.PayloadField, d *QbitSeDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	tag := strings.TrimSpace(d.Tag)
	if tag == "" {
		return fields
	}
	fields = append(fields, agents.PayloadField{
		Name:   "Tag",
		Value:  tag,
		Inline: true,
	})
	if cls := strings.TrimSpace(d.Classification); cls != "" && cls != tag {
		fields = append(fields, agents.PayloadField{
			Name:   "Type",
			Value:  cls,
			Inline: true,
		})
	}
	if qbit := strings.TrimSpace(d.QbitInstance); qbit != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Client",
			Value:  qbit,
			Inline: true,
		})
	}
	return fields
}

// appendQbitCategoryFixSection renders the qBittorrent category-swap
// outcome. Mirrors the user's mental model "the torrent was in X,
// now it's in Y because Sonarr/Radarr's own swap silently failed".
//
//	Was in        movies
//	Moved to      movies-imported
//	Client        qBit Movies
//
// SkipReason from the Detail is NOT rendered — by definition the
// orchestrator only invokes this builder when Changed=true (an
// actual swap happened). SkipReason is for History debugging only.
func appendQbitCategoryFixSection(fields []agents.PayloadField, d *QbitCategoryFixDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	pre := strings.TrimSpace(d.PreCat)
	post := strings.TrimSpace(d.PostCat)
	if pre == "" && post == "" {
		return fields
	}
	if pre != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Was in",
			Value:  pre,
			Inline: true,
		})
	}
	if post != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Moved to",
			Value:  post,
			Inline: true,
		})
	}
	if qbit := strings.TrimSpace(d.QbitInstance); qbit != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Client",
			Value:  qbit,
			Inline: true,
		})
	}
	return fields
}

// appendFileDeleteSection renders the bundled per-bucket strip +
// auto-strip Tag-RG outcome for file-delete events. Plain-language
// surface — the user wants to know WHAT was cleaned up, not which
// internal function ran.
//
//	Cleaned in   primary · Radarr 4K
//	Removed      Audio · Video · Quality tag
//
// "Cleaned in" lists the instances where tags were stripped (primary
// + secondary when MirroredSecondary). Symmetric with Tag section's
// "Tagged in" — same mental model "{verb} in: {instances}" across
// the two lifecycle directions.
//
// "Removed" lists the categories of tags that were actually stripped
// (any non-empty bucket in PerBucket → its display name appears).
// "Quality tag" appended when TagRgRemoved is set (release-group
// tag OR filter-only tag — both are "the quality tag" to the user).
//
// Per-bucket TAG VALUES (e.g. "TrueHD 7.1") aren't shown by default
// — they'd push the embed past comfort on a multi-bucket strip. A
// future drill-in could surface them via Payload.Detail follow-up.
func appendFileDeleteSection(fields []agents.PayloadField, d *FileDeleteDetail) []agents.PayloadField {
	if d == nil {
		return fields
	}
	var categories []string
	// Stable order so multi-bucket strips always read "Audio · Video · DV".
	for _, bucket := range []struct {
		key   string
		label string
	}{
		{"audio", "Audio"},
		{"video", "Video"},
		{"dv", "DV"},
	} {
		if tags := d.PerBucket[bucket.key]; len(tags) > 0 {
			categories = append(categories, bucket.label)
		}
	}
	if strings.TrimSpace(d.TagRgRemoved) != "" {
		categories = append(categories, "Quality tag")
	}
	if len(categories) == 0 {
		return fields
	}
	// "Cleaned in" instance list — folds the mirrored-to fact into
	// the primary instance name (symmetric with Tag section's
	// "Tagged in"). Always emit when something was cleaned, so the
	// user always knows WHICH Arr the cleanup hit.
	cleanedIn := buildInstanceList(d.Primary, d.MirroredSecondary, d.SecondaryName)
	if cleanedIn != "" {
		fields = append(fields, agents.PayloadField{
			Name:   "Cleaned in",
			Value:  cleanedIn,
			Inline: false,
		})
	}
	fields = append(fields, agents.PayloadField{
		Name:   "Removed",
		Value:  strings.Join(categories, " · "),
		Inline: false,
	})
	return fields
}

// buildInstanceList renders the instance-list shared by Tag section's
// "Tagged in" + File-Delete section's "Cleaned in". Single instance →
// primary name only. Mirrored → "primary · secondary" with both
// names. Adapter populates real names from
// Config.Instances[rule.InstanceID].Name + the secondary instance's
// name; missing names fall back to generic placeholders ("primary
// Arr" / "secondary") so the section reads as a complete sentence
// even on legacy / partial Detail.
func buildInstanceList(primary string, mirrored bool, secondary string) string {
	primaryName := strings.TrimSpace(primary)
	if primaryName == "" {
		primaryName = "primary Arr"
	}
	if !mirrored {
		return primaryName
	}
	secondaryName := strings.TrimSpace(secondary)
	if secondaryName == "" {
		secondaryName = "secondary"
	}
	return primaryName + " · " + secondaryName
}

// formatAutoTagPlainSummary builds the user-facing one-liner for an
// Audio/Video/DV section from the engine's emitted tag labels.
// Strips the supplied bucket-prefix when present so labels like
// "audio-truehd-71" render as "truehd-71" rather than the raw
// internal form. Multiple labels join with " · ".
//
// Best-effort plain-language: the engine emits canonical lowercase-
// dash labels (`^[a-z0-9-]+$`), which is constraint-driven, not
// human-readable. This helper makes the embed slightly less robotic
// without requiring a full mediaInfo → display-string converter.
// User-confirmed in conversation: showing actual tag labels is OK
// for the first cut; a future polish can build a proper formatter
// reading mediaInfo directly when the visual gap matters.
//
// TODO (deferred from task 7.2 review): the bucketPrefix argument is
// today hardcoded by each adapter ("audio-" / "video-" / "dv-"). The
// engine actually respects user-configured BucketConfig.Prefix
// (defaults to those values) and Video uses THREE distinct prefixes
// (Resolution.Prefix, Codec.Prefix, HDR.Prefix). A user with a
// non-default prefix sees their tags rendered verbatim (graceful
// fallback) but the strip is a no-op. Proper fix: thread engineCfg
// through to the adapter wrap site, pass the actual configured
// prefix(es). Defer until real-world soak shows the visual gap
// matters to non-default-prefix users.
func formatAutoTagPlainSummary(labels []string, bucketPrefix string) string {
	if len(labels) == 0 {
		return ""
	}
	stripped := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		stripped = append(stripped, strings.TrimPrefix(l, bucketPrefix))
	}
	return strings.Join(stripped, " · ")
}

// joinNonEmpty filters empty strings from the slice then joins with
// the separator. Used for tag-list and trigger-list fields where the
// adapter might have populated `["", "x", ""]` and we want "x" not
// " · x · ".
func joinNonEmpty(items []string, sep string) string {
	cleaned := make([]string, 0, len(items))
	for _, s := range items {
		if v := strings.TrimSpace(s); v != "" {
			cleaned = append(cleaned, v)
		}
	}
	return strings.Join(cleaned, sep)
}
