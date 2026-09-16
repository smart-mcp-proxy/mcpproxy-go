//go:build darwin || windows

package codescripts

import "testing"

// warmStoredNames is a no-op where storedSpellingsOf is a single-entry platform
// call (darwin F_GETPATH, Windows FindFirstFile) and there is no index to
// warm; the Linux/BSD counterpart lists the directory once.
func warmStoredNames(t *testing.T, _ string) {
	t.Helper()
}
