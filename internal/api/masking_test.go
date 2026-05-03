package api

import "testing"

func TestMaskKey_shortKeysCollapse(t *testing.T) {
	cases := []struct {
		name, in string
		want     string
	}{
		{"empty", "", maskSentinel},
		{"one_char", "a", maskSentinel},
		{"eight_exact", "12345678", maskSentinel},
		{"nine_chars", "123456789", "1234" + "*" + "6789"},
		{"standard_key", "abcdef1234567890", "abcd" + "********" + "7890"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := maskKey(tc.in); got != tc.want {
				t.Fatalf("maskKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsMasked(t *testing.T) {
	cases := []struct {
		name, in string
		want     bool
	}{
		{"empty", "", true},
		{"sentinel", maskSentinel, true},
		{"shorter_than_9", "1234****", false},
		{"exact_9_valid", "1234*6789", true},
		{"standard_masked", "abcd********7890", true},
		{"not_masked", "abcdef1234567890", false},
		{"mixed_middle", "abcd**x*7890", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isMasked(tc.in); got != tc.want {
				t.Fatalf("isMasked(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskKey_isMasked_roundTrip(t *testing.T) {
	// Anything maskKey produces must be identified as masked by isMasked.
	// Future: if maskKey's output format changes, this guards the pair.
	inputs := []string{
		"abcdef1234567890",
		"123456789",       // shortest that produces non-sentinel output
		"aaaaaaaaaaaaaaaa",
		"",
		"short",
	}
	for _, in := range inputs {
		if !isMasked(maskKey(in)) {
			t.Errorf("isMasked(maskKey(%q)) = false, want true", in)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	// Empty stays empty so UI can distinguish "not set" from "set but hidden".
	if got := maskSecret("", maskedDiscordWebhook); got != "" {
		t.Errorf("maskSecret(empty) = %q, want empty", got)
	}
	// Non-empty collapses to the placeholder regardless of content.
	if got := maskSecret("https://discord.com/api/webhooks/12345/abcdef", maskedDiscordWebhook); got != maskedDiscordWebhook {
		t.Errorf("maskSecret(url) = %q, want %q", got, maskedDiscordWebhook)
	}
}

func TestPreserveIfMasked(t *testing.T) {
	const stored = "https://discord.com/api/webhooks/real/value"
	// Round-trip: UI sent the mask back → preserve the real stored value.
	if got := preserveIfMasked(maskedDiscordWebhook, stored, maskedDiscordWebhook); got != stored {
		t.Errorf("preserveIfMasked(mask, stored, mask) = %q, want %q", got, stored)
	}
	// Fresh value: UI sent a new URL → use it as-is.
	const fresh = "https://discord.com/api/webhooks/new/value"
	if got := preserveIfMasked(fresh, stored, maskedDiscordWebhook); got != fresh {
		t.Errorf("preserveIfMasked(fresh, stored, mask) = %q, want %q", got, fresh)
	}
	// Explicit delete: UI sent empty → empty wins (it's not the mask).
	if got := preserveIfMasked("", stored, maskedDiscordWebhook); got != "" {
		t.Errorf("preserveIfMasked(empty, stored, mask) = %q, want empty (explicit delete)", got)
	}
}
