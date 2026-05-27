package plex

// Library is one Plex library section ("Movies", "TV Shows", etc.) —
// the smallest unit a user picks when configuring a label-sync rule.
//
// Type is Plex's library-type discriminator: "movie" for film libraries,
// "show" for TV libraries. The UI filters the library picker by this
// field so a Radarr instance can only target movie libraries and a
// Sonarr instance can only target show libraries.
type Library struct {
	Key   string `json:"key"`   // Plex's internal section ID (e.g. "1") — used as the URL path component for item fetches
	Title string `json:"title"` // user-visible library name ("Movies", "Movies 4K", "TV Shows")
	Type  string `json:"type"`  // "movie" | "show" | "artist" | "photo" — we only care about movie + show
}

// Item is one library item (movie or series) with the minimal fields
// resolvarr needs to match it against Arr media + apply labels.
//
// We deliberately do NOT model posters, ratings, watch state, file
// paths, or any other metadata — those are out of scope. Plex API
// returns them; we discard them at JSON-decode time.
//
// GUIDs is the slice of external-ID URIs Plex collected from agents
// (TMDB / TVDB / IMDB are the three we care about). They look like
// "imdb://tt17526714" / "tmdb://933260" / "tvdb://12345". Parse with
// ParseGUID() to extract the source + ID.
//
// Labels is the current set of Plex labels on this item. We use it
// to compute the add/remove diff against the Arr-tag set + whitelist
// (per analysis-doc §1.2 "bidirectional within whitelist" invariant).
type Item struct {
	RatingKey string   // Plex's per-item internal ID (used in label-update URLs)
	Title     string   // for diagnostic logs + match-fallback (title+year tier)
	Year      int      // for match-fallback (year is required for the title+year tier)
	Type      string   // "movie" | "show" — season + episode types are out of scope for label sync
	GUIDs     []string // raw GUID URIs from Plex
	Labels    []string // current Plex labels (Label[].tag values; case preserved as Plex stores them)
	// Collections is the parallel set to Labels — Plex stores them
	// separately under Collection[]. Engine targets one or the other
	// per rule via PlexLabelRule.TargetType. Same case-preservation +
	// case-insensitive matching pattern as labels.
	Collections []string
}

// itemTypeCode maps Plex's library-type strings to the numeric `type`
// query-param required by the label-update endpoint. Plex's URL API
// uses the integer everywhere, but library section responses report
// the string form. We bridge here.
//
//	1 = movie
//	2 = show
//	3 = season  (out of scope for label sync)
//	4 = episode (out of scope for label sync)
func itemTypeCode(t string) int {
	switch t {
	case "movie":
		return 1
	case "show":
		return 2
	case "season":
		return 3
	case "episode":
		return 4
	}
	return 0 // unknown — caller treats as "skip"
}

// ---------- raw JSON-decode targets (internal — not exported) ----------
//
// Plex wraps every response in a `{"MediaContainer": {...}}` envelope.
// We surgically decode just the slots we need so future schema additions
// don't break our parsing.

type librariesResponse struct {
	MediaContainer struct {
		Directory []rawLibrary `json:"Directory"`
	} `json:"MediaContainer"`
}

type rawLibrary struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type itemsResponse struct {
	MediaContainer struct {
		Metadata []rawItem `json:"Metadata"`
	} `json:"MediaContainer"`
}

type rawItem struct {
	RatingKey string     `json:"ratingKey"`
	Title     string     `json:"title"`
	Year      int        `json:"year,omitempty"`
	Type      string     `json:"type"`
	// Plex returns BOTH a capital "Guid" (array of external GUIDs —
	// TMDB / TVDB / IMDB) AND a lowercase "guid" (Plex's own internal
	// GUID string, e.g. "plex://movie/abc123"). Go's JSON decoder
	// matches case-insensitively after exact-match fails, so without
	// an explicit absorber for the lowercase field, the string value
	// gets routed into Guid []rawGuid and the decode panics with
	// "cannot unmarshal string into Go struct field ... of type
	// []plex.rawGuid". The PlexInternalGUID field below catches the
	// lowercase one via its exact-match tag so the typed Guid array
	// stays clean.
	Guid             []rawGuid  `json:"Guid,omitempty"`
	PlexInternalGUID string     `json:"guid,omitempty"` // absorbed — Plex's own GUID, unused
	Label            []rawLabel `json:"Label,omitempty"`
	// Plex's Collection array has the same shape as Label
	// ({id, tag, filter}). Many users group movies by quality/release
	// taxonomy (FEL/MEL, Reference Audio, etc.) via collections
	// rather than labels — they show up as proper grouped views in
	// Plex Web. Engine reads both arrays; rule's TargetType decides
	// which one drives the diff.
	Collection []rawLabel `json:"Collection,omitempty"`
}

type rawGuid struct {
	ID string `json:"id"`
}

type rawLabel struct {
	Tag string `json:"tag"`
}

// identityResponse is the Plex /identity probe — used by Ping() to
// confirm "URL + token are valid + server is reachable" without
// fetching any library data.
type identityResponse struct {
	MediaContainer struct {
		MachineIdentifier string `json:"machineIdentifier"`
		Version           string `json:"version"`
		FriendlyName      string `json:"friendlyName"`
	} `json:"MediaContainer"`
}
