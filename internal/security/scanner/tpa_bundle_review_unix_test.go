//go:build unix

package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-011a: a TRANSIENT last-known-good read failure (EACCES here) must not
// forget the recoverable LKG. Embedded serves for that run, but the committed
// active record and its files stay, so the release comes back once the read works.
func TestRuntime_TransientLKGReadErrorKeepsTheRecord(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 does not deny root")
	}
	f := newRTFixture(t, rtOpts{})
	x := signedCandidate(t, keyPublisher, bundleOpts{Seq: 10, Tag: "X"})
	f.activate(x)
	files := f.lkgFiles()
	require.NotEmpty(t, files)

	f.restart()
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "older"})) // refused: downgrade
	lkg := filepath.Join(f.dir, "tpa-lkg", digestOf(x.Bundle)+".json")
	require.NoError(t, os.Chmod(lkg, 0o000))
	t.Cleanup(func() { _ = os.Chmod(lkg, 0o600) })

	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source, "embedded serves while the LKG is unreadable")
	st := f.store.persisted(t)
	require.NotNil(t, st.Active, "a transient read error does not erase the active record")
	assert.Equal(t, digestOf(x.Bundle), st.Active.FullDigest)
	assert.ElementsMatch(t, files, f.lkgFiles(), "the LKG files are not reconciled away")

	require.NoError(t, os.Chmod(lkg, 0o600))
	info = f.run()
	assert.Equal(t, digestOf(x.Bundle), info.FullDigest, "the release is restored from its LKG")
	assert.Len(t, f.store.historyRows(), 1, "restoring from the LKG is not a new activation")
}
