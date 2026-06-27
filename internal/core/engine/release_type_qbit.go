package engine

// release_type_qbit.go — Tier 2 of the release-type cascade: confirm a
// file's release type against the qBittorrent torrent that actually holds
// it, matched by EXACT byte size. Two different encodes never produce
// byte-identical files, so a size match proves the on-disk file came from
// that torrent; the torrent's non-sample video-file count then settles the
// type (more than one = season pack, one = single episode). This is ground
// truth and overrides the grab-history cascade (Tier 1/3). See
// docs/resolvarr/release-type-recovery-design.md §5.2 and the Whiskey
// Cavalier case (§10), where the grab history alone pointed the wrong way.
//
// Pure logic — the live qBit I/O (fetch torrents + file lists) is in the
// API handler, same split as the rest of the cascade.

// QbitTorrentView is one qBit torrent reduced to its file list, for the
// content match. Mirrors the per-torrent /torrents/files payload.
type QbitTorrentView struct {
	Hash  string
	Name  string
	Files []TorrentFileView
}

// ContentMatch is the outcome of matching one on-disk file (by byte size)
// against the qBit torrents.
type ContentMatch struct {
	Matched     bool   // a torrent held a video file of the exact byte size
	TorrentName string // the matched torrent's display name
	VideoFiles  int    // non-sample video-file count in the matched torrent
	Type        string // ReleaseTypeSeasonPack (>1 video) | ReleaseTypeSingleEpisode (==1)
}

// MatchReleaseTypeByContent finds the torrent whose file list contains a
// video file of exactly fileSize bytes and derives the release type from
// that torrent's non-sample video-file count. Returns Matched=false when
// no torrent holds a file of that size (the torrent was removed, or qBit
// doesn't have this release). A non-positive fileSize never matches.
func MatchReleaseTypeByContent(fileSize int64, torrents []QbitTorrentView) ContentMatch {
	if fileSize <= 0 {
		return ContentMatch{}
	}
	for _, t := range torrents {
		hit := false
		for _, f := range t.Files {
			// Exact byte size on a video file is the proof. The size check
			// alone is near-unique, but we still require a video extension
			// so a same-size .nfo/.txt coincidence can't trigger a match.
			if f.Size == fileSize && isVideoFile(f.Name) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		vids, _ := videoFileStats(t.Files)
		typ := ReleaseTypeSingleEpisode
		if vids > 1 {
			typ = ReleaseTypeSeasonPack
		}
		return ContentMatch{Matched: true, TorrentName: t.Name, VideoFiles: vids, Type: typ}
	}
	return ContentMatch{}
}
