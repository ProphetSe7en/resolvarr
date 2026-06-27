package engine

import "testing"

func TestMatchReleaseTypeByContent(t *testing.T) {
	const mib = 1 << 20
	pack := QbitTorrentView{
		Hash: "aaa", Name: "Whiskey.Cavalier.S01.1080p.AMZN.WEB-DL-NTb",
		Files: []TorrentFileView{
			{Name: "Whiskey.Cavalier.S01E01.mkv", Size: 1500 * mib},
			{Name: "Whiskey.Cavalier.S01E02.mkv", Size: 1600 * mib},
			{Name: "Whiskey.Cavalier.S01E03.mkv", Size: 1550 * mib},
		},
	}
	single := QbitTorrentView{
		Hash: "bbb", Name: "Some.Show.S01E05.1080p.WEB-DL-GRP",
		Files: []TorrentFileView{
			{Name: "Some.Show.S01E05.mkv", Size: 900 * mib},
			{Name: "sample.mkv", Size: 20 * mib},
		},
	}

	tests := []struct {
		name        string
		size        int64
		torrents    []QbitTorrentView
		wantMatched bool
		wantType    string
		wantVideo   int
	}{
		{
			name:        "exact size in a pack -> season pack",
			size:        1600 * mib,
			torrents:    []QbitTorrentView{single, pack},
			wantMatched: true, wantType: ReleaseTypeSeasonPack, wantVideo: 3,
		},
		{
			name:        "exact size in a single -> single episode (sample excluded)",
			size:        900 * mib,
			torrents:    []QbitTorrentView{single, pack},
			wantMatched: true, wantType: ReleaseTypeSingleEpisode, wantVideo: 1,
		},
		{
			name:        "no torrent holds this size -> no match",
			size:        123 * mib,
			torrents:    []QbitTorrentView{single, pack},
			wantMatched: false,
		},
		{
			name:        "zero size never matches",
			size:        0,
			torrents:    []QbitTorrentView{pack},
			wantMatched: false,
		},
		{
			name:        "same size on a non-video file does not match",
			size:        500 * mib,
			torrents:    []QbitTorrentView{{Name: "x", Files: []TorrentFileView{{Name: "readme.nfo", Size: 500 * mib}}}},
			wantMatched: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchReleaseTypeByContent(tc.size, tc.torrents)
			if got.Matched != tc.wantMatched {
				t.Fatalf("Matched = %v, want %v", got.Matched, tc.wantMatched)
			}
			if !tc.wantMatched {
				return
			}
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
			if got.VideoFiles != tc.wantVideo {
				t.Errorf("VideoFiles = %d, want %d", got.VideoFiles, tc.wantVideo)
			}
		})
	}
}
