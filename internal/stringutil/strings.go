// Package stringutil provides common string utility functions.
package stringutil

import (
	"strings"
	"unicode/utf8"
)

// ContainsIgnoreCase checks if s contains substr, ignoring case.
func ContainsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// CollapseRepeatedErrorWrappers removes consecutive duplicate wrapper segments
// from a wrapped Go error chain rendered as "outer: middle: inner".
//
// Third-party transports sometimes wrap an error with the same message twice
// (mcp-go's streamable_http wraps a send failure as "failed to send request"
// at two nesting levels), so a user-facing error reads
//
//	transport error: failed to send request: failed to send request: Post "…"
//
// ": " is not only a Go wrapping boundary — it also occurs inside quoted URLs,
// JSON, captured stderr and validation text. Deleting an adjacent repeat there
// would silently destroy real content. So a segment is dropped ONLY when all of
// the following hold:
//
//   - it is byte-identical to the segment immediately before it, and
//   - it is not the last segment (the root cause is never merged away), and
//   - it looks like a wrapper phrase rather than a payload — see isWrapperPhrase.
//
// A chain with no adjacent wrapper repeats is returned unchanged.
func CollapseRepeatedErrorWrappers(s string) string {
	const sep = ": "
	if !strings.Contains(s, sep) {
		return s
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		if i > 0 && i < len(parts)-1 && part == parts[i-1] && isWrapperPhrase(part) {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, sep)
}

// maxWrapperPhraseLen bounds what may be treated as a wrapper. Wrapper text
// written by a `fmt.Errorf("…: %w")` call is a short prose fragment; anything
// longer is far more likely to be captured output that merely happens to repeat.
const maxWrapperPhraseLen = 64

// isWrapperPhrase reports whether a chain segment has the shape of prose added
// by an error wrapper, as opposed to payload that must be preserved verbatim.
//
// Conservative by construction: a wrapper is short and made only of letters,
// digits, spaces and a few word-joining marks. Anything carrying a quote,
// brace, bracket, slash, equals sign or other punctuation is payload — a URL,
// a JSON fragment, a shell line — and is never dropped, even if it repeats.
func isWrapperPhrase(s string) bool {
	if s == "" || len(s) > maxWrapperPhraseLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == ' ', r == '-', r == '_', r == '.', r == ',':
		default:
			return false
		}
	}
	return true
}

// SafeTruncateBytes returns the largest cut length <= limit at which s can be
// sliced without splitting a multi-byte UTF-8 rune.
//
// Several budgets in this codebase are RAW BYTE budgets — a forwarded tool
// response under ToolResponseLimit, a stored activity response under
// activity_max_response_size, a recorded error message — and cutting at the raw
// offset can land in the middle of a multi-byte character. The result is
// invalid UTF-8, which JSON encoders do not reject: encoding/json substitutes
// U+FFFD, so the value silently GROWS past the very budget that produced it and
// its tail is corrupted. Backing up to the rune boundary is the only cut that
// keeps both properties.
//
// Callers must ensure limit < len(s) — i.e. that truncation is actually needed
// — before using the result as a cut point.
func SafeTruncateBytes(s string, limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit >= len(s) {
		return len(s)
	}
	// Back up to the start of the rune that straddles the cut point.
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}
