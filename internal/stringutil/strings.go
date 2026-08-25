// Package stringutil provides common string utility functions.
package stringutil

import "strings"

// ContainsIgnoreCase checks if s contains substr, ignoring case.
func ContainsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// CollapseRepeatedErrorWrappers removes consecutive duplicate segments from a
// wrapped Go error chain rendered as "outer: middle: inner".
//
// Third-party transports sometimes wrap an error with the same message twice
// (mcp-go's streamable_http wraps a send failure as "failed to send request"
// at two nesting levels), so a user-facing error reads
//
//	transport error: failed to send request: failed to send request: Post "…"
//
// Only *adjacent* identical segments are dropped, and only when the duplicate
// is a pure wrapper (it carries no distinguishing detail of its own), so no
// information is lost. The final segment — the actual cause — is never merged
// away, and a chain with no adjacent repeats is returned unchanged.
func CollapseRepeatedErrorWrappers(s string) string {
	const sep = ": "
	if !strings.Contains(s, sep) {
		return s
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		// Never drop the last segment: it is the root cause, and a chain like
		// "x: x" must keep one copy regardless.
		if i > 0 && i < len(parts)-1 && part == parts[i-1] {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, sep)
}
