// scan_history.go — adhoc-scan dump browser endpoints. Adhoc scans
// (Tag library / Discover / Recover / Cleanup / Audio / Video / DV
// detail) write their full scanResponse to /config/logs/scan-{action}-
// YYYYMMDD-HHMMSS.json via dumpScanJSON. These endpoints expose the
// dumps to the UI History viewer so users can re-open any past run
// without losing the data when the live result panel is dismissed.
//
// Schedule + rule runs are NOT covered here — they have their own
// per-run history under the scheduler's storage (scheduleId-{ts}.log
// + .json) which the existing schedule-history modal reads.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// historyPreviewBytes caps how much of each scan dump we read just to
// extract mode/instance/itemCount for the listing. Top-level fields
// land in the first object header; the items array lives at the end
// and we only need its length, which we approximate from the bytes we
// did read (preview's items will be truncated, but we don't surface
// that count — see handleScanHistory).
const historyPreviewBytes = 64 * 1024

// scanHistoryEntry is one row in the history listing. Parsed lazily
// from the file name (action, timestamp) + a tiny re-read for the
// totals/instance fields the UI shows in the row preview. Heavier
// per-row metadata stays inside the JSON file itself; clicking the
// row triggers a full read via /api/scan/history/{filename}.
type scanHistoryEntry struct {
	File         string    `json:"file"`                   // bare filename, used by the per-row endpoint
	Action       string    `json:"action"`                 // tag / discover / recover / cleanup / audiotags / videotags / dvdetail
	Timestamp    time.Time `json:"timestamp"`              // parsed from filename
	Mode         string    `json:"mode,omitempty"`         // preview / apply (read from file)
	Instance     string    `json:"instance,omitempty"`     // instance name (read from file)
	InstanceID   string    `json:"instanceId,omitempty"`   // instance ID — lets the UI filter by current scanInstanceId / app type
	InstanceType string    `json:"instanceType,omitempty"` // 'radarr' | 'sonarr' — drives per-app-type filtering on Activity
	SizeBytes    int64     `json:"sizeBytes"`
	ItemCount    int       `json:"itemCount,omitempty"` // items in the response (rough activity signal)
}

// handleScanHistory returns the list of scan dumps under /config/logs/.
// Sorted newest-first across all actions; the UI groups by action
// client-side. No pagination — the retention prune (default 14 days)
// caps the list at a few hundred entries on a busy install.
func (s *Server) handleScanHistory(w http.ResponseWriter, r *http.Request) {
	dir := "/config/logs"
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, []scanHistoryEntry{})
		return
	}

	out := make([]scanHistoryEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "scan-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		// scan-<action>-YYYYMMDD-HHMMSS.json
		base := strings.TrimSuffix(strings.TrimPrefix(name, "scan-"), ".json")
		parts := strings.Split(base, "-")
		if len(parts) < 3 {
			continue
		}
		// Reconstruct: action may itself contain hyphens (none today,
		// but keep the parser tolerant). Last two parts are date+time.
		ts, terr := time.ParseInLocation("20060102-150405",
			parts[len(parts)-2]+"-"+parts[len(parts)-1], time.Local)
		if terr != nil {
			continue
		}
		action := strings.Join(parts[:len(parts)-2], "-")

		entry := scanHistoryEntry{
			File:      name,
			Action:    action,
			Timestamp: ts,
		}
		// Lstat (NOT Stat) to avoid following a symlink — entries
		// pointing outside /config/logs/ should not surface in this
		// list. e.IsDir() above already filters dir-symlinks; this
		// also rejects file-symlinks.
		full := filepath.Join(dir, name)
		info, lerr := os.Lstat(full)
		if lerr != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		entry.SizeBytes = info.Size()
		// Best-effort peek at the JSON for mode + instance + item
		// count. Cap the read at 64 KB — top-level fields land in
		// the first object and totals.items carries the number we
		// want (no need to count the full items array). Allows the
		// listing endpoint to handle libraries with thousands of
		// scan dumps without doing hundreds of MB of JSON parses.
		if entry.SizeBytes > 0 {
			if preview, ok := readScanPreview(full); ok {
				entry.Mode = preview.Mode
				entry.Instance = preview.InstanceName
				entry.InstanceID = preview.InstanceID
				entry.InstanceType = preview.InstanceType
				entry.ItemCount = preview.ItemCount
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	writeJSON(w, out)
}

// scanPreviewFields are the row-preview fields readScanPreview pulls
// from a dump. Bundled in a struct so the call-site stays readable as
// the field count grows (instance.id and instance.type were added for
// per-app-type filtering on Activity).
type scanPreviewFields struct {
	Mode         string
	InstanceName string
	InstanceID   string
	InstanceType string
	ItemCount    int
}

// readScanPreview returns the row-preview fields plus an ok flag.
// Reads up to historyPreviewBytes from the start of the file. The
// scanResponse JSON is laid out so totals.items lives near the top —
// well within 64 KB even for large libraries. If the JSON is too
// minified or the totals block is unusually deep, parsing falls
// through with ok=false and the caller renders an empty-preview row.
func readScanPreview(path string) (scanPreviewFields, bool) {
	var zero scanPreviewFields
	f, err := os.Open(path)
	if err != nil {
		return zero, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, historyPreviewBytes))
	if err != nil {
		return zero, false
	}
	// Try the fast path: the buffer is a complete JSON object. Most
	// dumps will be larger than 64 KB total but smaller than 64 KB up
	// to (and including) the totals block, so the parse will fail
	// because the items array is truncated. Fall through to a
	// regex-free best-effort grab in that case.
	var preview struct {
		Mode     string `json:"mode"`
		Instance struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"instance"`
		Totals struct {
			Items int `json:"items"`
		} `json:"totals"`
	}
	if json.Unmarshal(data, &preview) == nil {
		return scanPreviewFields{
			Mode:         preview.Mode,
			InstanceName: preview.Instance.Name,
			InstanceID:   preview.Instance.ID,
			InstanceType: preview.Instance.Type,
			ItemCount:    preview.Totals.Items,
		}, true
	}
	// Truncated JSON — pick the fields out by string search. The dump
	// is pretty-printed (json.MarshalIndent in dumpScanJSON), so the
	// fields appear on their own indented lines and a substring search
	// is unambiguous.
	mode := extractStringField(data, "mode")
	inst := extractStringField(data, "name")
	instID := extractStringField(data, "id")
	instType := extractStringField(data, "type")
	itm := extractIntField(data, "items")
	if mode == "" && inst == "" && itm == 0 {
		return zero, false
	}
	return scanPreviewFields{
		Mode:         mode,
		InstanceName: inst,
		InstanceID:   instID,
		InstanceType: instType,
		ItemCount:    itm,
	}, true
}

// extractStringField looks for `"<key>": "value"` (allowing whitespace)
// and returns the first match. Quotes inside the value are not handled
// — fine for our schema (mode is an enum, instance.name is a label
// the user typed but rejected if it contained quotes upstream).
func extractStringField(buf []byte, key string) string {
	needle := []byte("\"" + key + "\":")
	i := bytesIndex(buf, needle)
	if i < 0 {
		return ""
	}
	rest := buf[i+len(needle):]
	// Skip whitespace until the opening quote.
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n') {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := bytesIndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return string(rest[:end])
}

// extractIntField looks for `"<key>": <number>` and returns the int.
// Returns 0 on miss.
func extractIntField(buf []byte, key string) int {
	needle := []byte("\"" + key + "\":")
	i := bytesIndex(buf, needle)
	if i < 0 {
		return 0
	}
	rest := buf[i+len(needle):]
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n') {
		rest = rest[1:]
	}
	n := 0
	for j := 0; j < len(rest); j++ {
		c := rest[j]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func bytesIndex(haystack, needle []byte) int {
	return strings.Index(string(haystack), string(needle))
}

func bytesIndexByte(haystack []byte, b byte) int {
	for i := range haystack {
		if haystack[i] == b {
			return i
		}
	}
	return -1
}

// handleScanHistoryFile serves one scan dump by filename. The dump
// is the raw scanResponse JSON; the UI hydrates the same scanResults
// slot the live scan populates, so the existing per-action result
// rendering replays untouched.
//
// Filename validation: only the basename, must match scan-*.json,
// no slashes (path-traversal defence). Files outside /config/logs/
// are not reachable.
func (s *Server) handleScanHistoryFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name == "" || strings.ContainsAny(name, "/\\") {
		writeError(w, 400, "invalid filename")
		return
	}
	if !strings.HasPrefix(name, "scan-") || !strings.HasSuffix(name, ".json") {
		writeError(w, 400, "filename must match scan-*.json")
		return
	}
	path := filepath.Join("/config/logs", name)
	// Lstat + reject symlink: /config is appdata (user-writable at
	// host uid:gid). A symlink scan-foo.json → /etc/passwd or
	// → /config/clonarr.json would otherwise be served verbatim.
	// Defence-in-depth on top of the basename validation above.
	info, lerr := os.Lstat(path)
	if lerr != nil {
		writeError(w, 404, "file not found")
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		writeError(w, 400, "scan history files must be regular files")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, 404, "file not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
