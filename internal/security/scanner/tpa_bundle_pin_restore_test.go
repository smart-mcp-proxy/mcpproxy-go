package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// FR-004b / FR-010: switching the active root away suspends the other root's
// pin and deny-list, and switching back restores them UNCHANGED. The pinned
// rollback target must therefore serve again after the round trip, even though
// the configured path still holds the deny-listed release.
func TestPin_SurvivesAnActiveRootRoundTrip(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"})
	b := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"})
	aPath := f.drop + "/previous.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(b)

	info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)
	require.Equal(t, digestOf(a.Bundle), info.FullDigest)
	rowsBefore := len(f.store.historyRows())

	// The operator switches to the publisher root: the pinned operator bundle
	// is deactivated and the embedded default serves.
	f.sec.TPAActiveRoot = config.TPARootPublisher
	info = f.run()
	require.Equal(t, BundleSourceEmbedded, info.Source)
	require.Nil(t, f.store.persisted(t).Active)
	op := f.store.persisted(t).Authorities[AuthorityOperator]
	require.NotNil(t, op.Pin, "switching roots suspends the pin, it does not delete it")
	require.Len(t, op.DenyList, 1)

	// ... and back. The configured path still holds B, the deny-listed release.
	f.sec.TPAActiveRoot = config.TPARootOperator
	info = f.run()

	assert.Equal(t, digestOf(a.Bundle), info.FullDigest, "the pinned target is restored unchanged")
	assert.Equal(t, BundleSourceFile, info.Source)
	assert.Equal(t, AuthorityOperator, info.Authority)
	assert.True(t, info.SignatureVerified)
	assert.True(t, info.Pinned)
	assert.True(t, info.Rollback)
	assert.False(t, info.CoverageDegraded, "a restored operator bundle is full coverage, not operator_bundle_missing")
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDenyListed, info.LastRejection.Reason, "the deny-listed release at the path is still refused")

	st := f.store.persisted(t)
	require.NotNil(t, st.Active)
	assert.Equal(t, digestOf(a.Bundle), st.Active.FullDigest)
	assert.Equal(t, uint64(6), st.Authorities[AuthorityOperator].Watermark, "the watermark is untouched")
	assert.Len(t, f.store.historyRows(), rowsBefore, "a restoration is not a new activation")
	assert.Contains(t, f.logMessages(zapcore.WarnLevel), "TPA scanner bundle restored from its rollback pin")

	// The restoration is durable: a restart lands on the same bundle.
	f.restart()
	info = f.run()
	assert.Equal(t, digestOf(a.Bundle), info.FullDigest)
	assert.True(t, info.Pinned)

	// And the next unchanged reload is a stable no-churn run.
	commits := f.store.commitCount()
	info = f.run()
	assert.Equal(t, digestOf(a.Bundle), info.FullDigest)
	assert.Equal(t, commits, f.store.commitCount())
}

// A pin whose LKG files are gone cannot be restored: the embedded default
// serves and the install says why (operator_bundle_missing), it does not crash
// or invent a bundle.
func TestPin_RestoreWithoutItsFilesFallsBackToEmbedded(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"})
	b := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"})
	aPath := f.drop + "/previous.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(b)
	_, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)

	f.sec.TPAActiveRoot = config.TPARootPublisher
	f.run()
	for _, name := range f.lkgFiles() {
		require.NoError(t, os.Remove(filepath.Join(f.dir, "tpa-lkg", name)))
	}
	f.sec.TPAActiveRoot = config.TPARootOperator
	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.True(t, info.CoverageDegraded)
	assert.Equal(t, DegradedOperatorBundleMissing, info.DegradedReason)
}

// US3 judge finding 10: removing tpa_bundle_path removes a FILE bundle, pinned
// or not, even with fetch on; the rollback pin must not resurrect it from its
// last-known-good files on the next run or after a restart.
func TestPin_RemovedFilePathDoesNotResurrectAPinnedFileBundleWithFetchOn(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
		s.TPAFetchEnabled = true
	}})
	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"})
	b := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"})
	aPath := f.drop + "/previous.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(b)
	info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)
	require.True(t, info.Pinned)

	f.sec.TPABundlePath = ""
	for i := 0; i < 2; i++ {
		info = f.run()
		assert.Equal(t, BundleSourceEmbedded, info.Source, "run %d: the removed file bundle must stay gone", i+1)
	}
	f.restart()
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "and across a restart")
}
