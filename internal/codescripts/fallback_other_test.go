//go:build !unix && !windows

package codescripts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Round 14 SHOULD (codex r11 #2): the shared tests in codescripts_test.go
// call warmStoredNames and quiesceIndexRebuilds unconditionally
// (TestResolveScoped_NeverReadsTheDirectory and friends), but those helpers
// previously existed only in the unix-tagged (storednames_other_test.go) and
// windows-tagged (storedspellings_probe_test.go) test files. A GOOS with
// neither tag — plan9, js/wasm, wasip1 — failed to even COMPILE its test
// binary, so fallback_other.go's fail-closed behavior (Warm as a no-op,
// storedSpellingsOf/openScriptFile always "not found") was never exercised
// there. These mirror the unix/windows helpers' names and signatures with
// the trivial bodies this platform's no-index design actually needs.

// quiesceIndexRebuilds is a no-op here: there is no index, and therefore no
// rebuild goroutine, to wait on.
func quiesceIndexRebuilds() {}

// warmStoredNames calls the package's real Warm, which fallback_other.go
// defines as a no-op returning nil — there is nothing to build an index
// from on a platform with no directory-descriptor primitive of its own.
func warmStoredNames(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, Warm(dir))
}
