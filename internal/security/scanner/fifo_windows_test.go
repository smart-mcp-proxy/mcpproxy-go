//go:build windows

package scanner

import "testing"

// makeTestFIFO is a no-op on Windows, which has no FIFOs in the filesystem.
func makeTestFIFO(t *testing.T, _ string) { t.Helper() }
