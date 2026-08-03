package inject

import "strings"

// sid returns a syntactically valid session ID that still reads as its label
// in a failure message. Real KSUIDs are opaque; test fixtures should not be.
//
// The body is exactly 27 ASCII base62 characters, so it satisfies
// agentcat.IsValidSessionID by construction and the slice can never split a
// rune. Use it for any ses_ literal that travels as a session_id tool-call
// argument, or that must equal one — a short literal like "ses_abc" is
// rejected as a value this server never issued. Literals that seed an Event,
// an exporter, or the redactor directly are never resolved and need no helper.
func sid(label string) string {
	var b strings.Builder
	for _, r := range label {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return "ses_" + (b.String() + strings.Repeat("0", 27))[:27]
}
