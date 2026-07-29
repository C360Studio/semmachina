package persona

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// clip is the only reason the cap-exhaustion explanation fits a budget the
// content store enforces before it writes, so its bound has to hold for every
// limit rather than for the two the package happens to pass it. A helper whose
// guarantee is conditional on the caller's number being comfortably larger than
// a constant it cannot see is a bound that stops holding the day somebody
// tightens it.
//
// Valid UTF-8 out is part of the same guarantee: a fragment cut mid-sequence
// round-trips through JSON as a replacement character, which reads as corrupted
// storage rather than as a trimmed diagnostic.
func TestClip_NeverExceedsItsLimitAndNeverSplitsARune(t *testing.T) {
	inputs := []string{
		"",
		"short",
		strings.Repeat("a", 4096),
		// Multibyte throughout, so a naive byte cut lands mid-sequence.
		strings.Repeat("risk: \"catastrophique\" — não aceito. 世界", 64),
		// A single rune wider than several of the limits below.
		"世" + strings.Repeat("界", 200),
	}

	for _, text := range inputs {
		for _, limit := range []int{0, 1, 2, 3, len(elision) - 1, len(elision), len(elision) + 1, 17, 128, 4096} {
			got := clip(text, limit)
			if len(got) > limit {
				t.Fatalf("clip(%d bytes, limit %d) returned %d bytes; the caller's budget is spent before the "+
					"content store ever sees the message", len(text), limit, len(got))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("clip(%d bytes, limit %d) cut a rune in half", len(text), limit)
			}
			if len(text) <= limit && got != text {
				t.Fatalf("clip trimmed a fragment that already fit: %d bytes under a limit of %d", len(text), limit)
			}
			if !strings.HasPrefix(text, strings.TrimSuffix(got, elision)) {
				t.Fatalf("clip(%d bytes, limit %d) did not keep the front of the fragment; the front is where a "+
					"refusal names the field it could not accept", len(text), limit)
			}
		}
	}
}
