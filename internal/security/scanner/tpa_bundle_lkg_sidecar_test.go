package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reformatted returns the same signed bundle with a sidecar that differs only
// in JSON whitespace: the signature is identical, the sidecar bytes (and so
// their digest) are not.
func reformatted(c Candidate) Candidate {
	return Candidate{Bundle: c.Bundle, Sidecar: append(append([]byte(nil), c.Sidecar...), '\n'), SidecarPresent: true}
}

// FR-011a: the recorded (bundle, sidecar) pair must stay recoverable until the
// new state is committed. A re-formatted sidecar for the SAME bundle bytes is
// a different activation (the sidecar digest differs); writing its LKG file
// must not clobber the file the committed Active record still names.
func TestLKG_ReformattedSidecarNeverClobbersTheRecordedPair(t *testing.T) {
	setup := func(t *testing.T) (*rtFixture, Candidate, Candidate) {
		f := newRTFixture(t, rtOpts{})
		a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
		f.activate(a)
		re := reformatted(a)
		require.NotEqual(t, digestOf(a.Sidecar), digestOf(re.Sidecar))
		return f, a, re
	}
	requireRecordedPairLoads := func(t *testing.T, f *rtFixture, a Candidate) {
		t.Helper()
		f.restart()
		f.remove()
		info := f.run()
		assert.Equal(t, digestOf(a.Bundle), info.FullDigest, "the recorded active bundle still serves from its LKG files")
		assert.Equal(t, BundleSourceFile, info.Source)
		assert.True(t, info.SignatureVerified)
		assert.False(t, info.CoverageDegraded)
		if info.LastRejection != nil {
			assert.NotEqual(t, RejectTamper, info.LastRejection.Reason, "the recorded pair is intact, not a tamper")
		}
		rec := f.store.persisted(t).Active
		require.NotNil(t, rec)
		assert.Equal(t, digestOf(a.Sidecar), rec.SidecarDigest)
	}

	t.Run("commit fails after the reformatted sidecar was written", func(t *testing.T) {
		f, a, re := setup(t)
		f.store.failCommit = errors.New("disk full")
		f.put(re)
		info := f.run()
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectStateCommitFailed, info.LastRejection.Reason)
		f.store.failCommit = nil

		requireRecordedPairLoads(t, f, a)
	})

	t.Run("process dies between the LKG write and the commit", func(t *testing.T) {
		f, a, re := setup(t)
		f.put(re)
		lkgAfterWriteHook = crash
		require.Panics(t, func() { f.run() })
		lkgAfterWriteHook = nil

		requireRecordedPairLoads(t, f, a)
	})
}

// The sidecar LKG file is content-addressed by BOTH digests, and only the pair
// the state records survives reconciliation.
func TestLKG_SidecarFileIsNamedByBundleAndSidecarDigest(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(a)

	want := []string{digestOf(a.Bundle) + ".json", digestOf(a.Bundle) + "." + digestOf(a.Sidecar) + ".sig"}
	assert.ElementsMatch(t, want, f.lkgFiles())

	// A legacy-named sidecar and a stray pair for the same bundle are debris.
	dir := filepath.Join(f.dir, "tpa-lkg")
	require.NoError(t, os.WriteFile(filepath.Join(dir, digestOf(a.Bundle)+".json.sig"), []byte("legacy"), 0o600))
	other := reformatted(a)
	require.NoError(t, os.WriteFile(filepath.Join(dir, digestOf(a.Bundle)+"."+digestOf(other.Sidecar)+".sig"), other.Sidecar, 0o600))
	f.restart()
	f.run()
	assert.ElementsMatch(t, want, f.lkgFiles(), "reconcile removes every sidecar file the state does not name")
}
