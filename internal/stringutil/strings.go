// Package stringutil provides common string utility functions.
package stringutil

import "strings"

// ContainsIgnoreCase checks if s contains substr, ignoring case.
func ContainsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
