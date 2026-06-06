package engine

import "testing"

func TestClassifyStuckDownload(t *testing.T) {
	cases := []struct {
		name       string
		stuckScore int
		targets    []StuckTarget
		want       StuckVerdict
	}{
		{
			name:       "BLOOM 1675 vs imported Kitsune 1775 → redundant",
			stuckScore: 1675,
			targets:    []StuckTarget{{HasFile: true, ImportedScore: 1775}},
			want:       StuckRedundant,
		},
		{
			name:       "equal score (re-grab of what's imported) → redundant",
			stuckScore: 80,
			targets:    []StuckTarget{{HasFile: true, ImportedScore: 80}},
			want:       StuckRedundant,
		},
		{
			name:       "stuck is a genuine upgrade (imported lower) → needs-attention",
			stuckScore: 1775,
			targets:    []StuckTarget{{HasFile: true, ImportedScore: 1675}},
			want:       StuckNeedsAttention,
		},
		{
			name:       "target has no file (not imported) → needs-attention",
			stuckScore: 100,
			targets:    []StuckTarget{{HasFile: false, ImportedScore: 0}},
			want:       StuckNeedsAttention,
		},
		{
			name:       "season pack: all episodes covered >= → redundant",
			stuckScore: 90,
			targets: []StuckTarget{
				{HasFile: true, ImportedScore: 90},
				{HasFile: true, ImportedScore: 120},
				{HasFile: true, ImportedScore: 90},
			},
			want: StuckRedundant,
		},
		{
			name:       "season pack: one episode missing → needs-attention",
			stuckScore: 90,
			targets: []StuckTarget{
				{HasFile: true, ImportedScore: 90},
				{HasFile: false, ImportedScore: 0},
				{HasFile: true, ImportedScore: 90},
			},
			want: StuckNeedsAttention,
		},
		{
			name:       "season pack: one episode scores below stuck → needs-attention",
			stuckScore: 90,
			targets: []StuckTarget{
				{HasFile: true, ImportedScore: 90},
				{HasFile: true, ImportedScore: 50},
			},
			want: StuckNeedsAttention,
		},
		{
			name:       "no targets (unmapped download) → needs-attention",
			stuckScore: 100,
			targets:    nil,
			want:       StuckNeedsAttention,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyStuckDownload(tc.stuckScore, tc.targets); got != tc.want {
				t.Fatalf("ClassifyStuckDownload(%d, %+v) = %q, want %q", tc.stuckScore, tc.targets, got, tc.want)
			}
		})
	}
}
