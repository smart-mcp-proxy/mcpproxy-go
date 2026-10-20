package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

// crash makes a fault-injection seam abort the run the way a process death
// would: by panicking out of the pipeline. The fixture then simulates a restart
// over the same store and data directory.
func crash() { panic("simulated process death") }

// --- FR-011a: crash points ------------------------------------------------------

// Fault after the LKG files are durable but before the state commit. The state
// is the old, consistent one; the half-installed LKG files are orphans.
func TestCrash_AfterLKGWriteBeforeCommit(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(a)
	b := signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "B"})
	f.put(b)

	lkgAfterWriteHook = crash
	require.Panics(t, func() { f.run() })
	lkgAfterWriteHook = nil

	st := f.store.persisted(t)
	assert.Equal(t, digestOf(a.Bundle), st.Active.FullDigest, "no commit happened, the pair is still (A, watermark 5)")
	assert.Equal(t, uint64(5), st.Authorities[AuthorityPublisher].Watermark)
	assert.Len(t, f.store.historyRows(), 1)
	assert.Contains(t, f.lkgFiles(), digestOf(b.Bundle)+".json", "the candidate's LKG is durable before the commit")

	t.Run("restart with the candidate still at the path activates it cleanly", func(t *testing.T) {
		f.restart()
		info := f.run()
		assert.Equal(t, "6", info.Sequence)
		assert.Equal(t, uint64(6), f.store.persisted(t).Authorities[AuthorityPublisher].Watermark)
		assert.Len(t, f.store.historyRows(), 2)
		assert.ElementsMatch(t, []string{digestOf(b.Bundle) + ".json", digestOf(b.Bundle) + ".json.sig"}, f.lkgFiles())
	})
}

func TestCrash_AfterLKGWriteBeforeCommit_RestartWithoutTheFileServesLKG(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(a)
	b := signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "B"})
	f.put(b)
	lkgAfterWriteHook = crash
	require.Panics(t, func() { f.run() })
	lkgAfterWriteHook = nil

	f.restart()
	f.remove()
	info := f.run()

	assert.Equal(t, digestOf(a.Bundle), info.FullDigest, "the recorded active bundle serves from its LKG files")
	assert.Equal(t, "5", info.Sequence)
	assert.Equal(t, BundleSourceFile, info.Source)
	assert.False(t, info.CoverageDegraded)
	assert.NotContains(t, f.lkgFiles(), digestOf(b.Bundle)+".json", "the orphan from the interrupted activation is reconciled away")
	assert.Contains(t, f.lkgFiles(), digestOf(a.Bundle)+".json")
}

// Fault after the commit but before publication: the persisted pair is the new
// one, and a restart lands on it.
func TestCrash_AfterCommitBeforePublish(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"}))
	b := signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "B"})
	f.put(b)

	stateAfterCommitHook = crash
	require.Panics(t, func() { f.run() })
	stateAfterCommitHook = nil

	st := f.store.persisted(t)
	assert.Equal(t, digestOf(b.Bundle), st.Active.FullDigest)
	assert.Equal(t, uint64(6), st.Authorities[AuthorityPublisher].Watermark)
	assert.Len(t, f.store.historyRows(), 2, "state and history row were one transaction")

	f.restart()
	f.remove()
	info := f.run()
	assert.Equal(t, digestOf(b.Bundle), info.FullDigest, "(bundle, watermark) is the consistent pair (B, 6)")
	assert.Equal(t, "6", info.Sequence)
	assert.True(t, info.SignatureVerified)
}

// A rollback that survives a crash keeps its deny-list: the next refresh must
// not re-activate the release the operator just rejected.
func TestCrash_RollbackKeepsItsDenyList(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, b, aPath := twoReleases(t, f)

	stateAfterCommitHook = crash
	require.Panics(t, func() {
		_, _ = RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	})
	stateAfterCommitHook = nil

	f.restart()
	// The configured path still holds release B, the one rolled away from.
	info := f.run()

	assert.Equal(t, digestOf(a.Bundle), info.FullDigest, "the rollback target serves from its LKG files")
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDenyListed, info.LastRejection.Reason, "B is refused: the deny-list was part of the same transaction")
	assert.NotEqual(t, digestOf(b.Bundle), info.FullDigest)
	assert.True(t, info.Pinned)
	assert.True(t, info.Rollback)
}

func TestCrash_FaultBeforeRollbackCommitChangesNothing(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, b, aPath := twoReleases(t, f)

	lkgAfterWriteHook = crash
	require.Panics(t, func() {
		_, _ = RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	})
	lkgAfterWriteHook = nil

	f.restart()
	info := f.run()
	assert.Equal(t, digestOf(b.Bundle), info.FullDigest, "an interrupted rollback never half-happened")
	assert.False(t, info.Pinned)
	assert.Empty(t, f.store.persisted(t).Authorities[AuthorityPublisher].DenyList)
}

// --- LKG store --------------------------------------------------------------------

func TestLKG_StartupReconcilesOrphans(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	lkg := filepath.Join(f.dir, "tpa-lkg")
	require.NoError(t, os.MkdirAll(lkg, 0o700))
	orphan := strings.Repeat("ab", 32)
	outside := filepath.Join(t.TempDir(), "outside.json")
	require.NoError(t, os.WriteFile(outside, []byte("precious"), 0o600))
	for name, content := range map[string]string{
		orphan + ".json":     "orphan bundle",
		orphan + ".json.sig": "orphan sidecar",
		".tmp-12345":         "interrupted write",
		"README.txt":         "not ours, left alone",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(lkg, name), []byte(content), 0o600))
	}
	linkName := strings.Repeat("cd", 32) + ".json"
	if err := os.Symlink(outside, filepath.Join(lkg, linkName)); err != nil {
		t.Logf("symlinks unavailable: %v", err)
		linkName = ""
	}

	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
	f.run()

	files := f.lkgFiles()
	assert.NotContains(t, files, orphan+".json")
	assert.NotContains(t, files, orphan+".json.sig")
	assert.NotContains(t, files, ".tmp-12345")
	assert.Contains(t, files, "README.txt", "files that are not LKG entries are never touched")
	if linkName != "" {
		assert.Contains(t, files, linkName, "a symlink is never followed or removed")
	}
	got, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "precious", string(got), "nothing outside tpa-lkg is ever touched")
}

func TestLKG_SubstitutedFileFallsBackToEmbedded(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(a)
	lkgFile := filepath.Join(f.dir, "tpa-lkg", digestOf(a.Bundle)+".json")
	evil := signedCandidate(t, keyPublisher, bundleOpts{Seq: 99, Tag: "evil"})
	require.NoError(t, os.WriteFile(lkgFile, evil.Bundle, 0o600))

	f.restart()
	f.remove()
	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source, "bytes that do not hash to the recorded digest are treated as missing")
	assert.Equal(t, embeddedDigest(), info.FullDigest)
	assert.NotEmpty(t, f.logMessages(zapcore.WarnLevel))
	assert.Nil(t, f.store.persisted(t).Active, "the dead record is cleared")
}

func TestLKG_NoDataDirMeansNoFiles(t *testing.T) {
	f := newRTFixture(t, rtOpts{NoDataDir: true})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
	_, err := os.Stat(filepath.Join(f.dir, "tpa-lkg"))
	assert.True(t, os.IsNotExist(err))

	// The in-memory pointer is the only LKG: it survives the file going away.
	f.remove()
	info := f.run()
	assert.Equal(t, "5", info.Sequence)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectReadError, info.LastRejection.Reason)
}

func TestLKG_RemovedFileKeepsServingWhilePathStaysConfigured(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5})
	f.activate(a)
	f.remove()

	info := f.run()

	assert.Equal(t, digestOf(a.Bundle), info.FullDigest, "last-known-good keeps serving")
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectReadError, info.LastRejection.Reason)
	assert.Contains(t, info.LoadError, RejectReadError)
	assert.False(t, info.CoverageDegraded)
}

func TestLKG_ClearingThePathFallsBackToEmbeddedAndClearsActive(t *testing.T) {
	f := newRTFixture(t, rtOpts{EmbeddedSeq: 3})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
	f.sec.TPABundlePath = ""

	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.Empty(t, info.LoadError)
	assert.Nil(t, f.store.persisted(t).Active)
	assert.Len(t, f.store.historyRows(), 1, "falling back is not an activation")
	assert.True(t, info.CoverageDegraded, "embedded sequence 3 is below the retained watermark 5")
	assert.Equal(t, DegradedEmbeddedBelowWatermark, info.DegradedReason)
	assert.Empty(t, f.lkgFiles(), "no live record references an LKG file any more")
}
