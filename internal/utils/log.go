package utils

// SanitizeLogField strips control bytes (0x00-0x1F and 0x7F) from a
// string so that user-supplied input can't inject newlines, carriage
// returns, or escape sequences into log output (baseline T68).
// Output is also capped at maxLen bytes so a malicious client can't
// blow up log storage with one huge request.
//
// Use on any string that originated with the user (URL params, form
// fields, JSON body values) before it reaches log.Printf / a debug-log
// writer. Leave alone for strings that come from trusted sources
// (config values we put there, internal enums).
func SanitizeLogField(s string) string {
	const maxLen = 1024
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			b = append(b, ' ')
			continue
		}
		b = append(b, c)
	}
	return string(b)
}
