package scanner

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// twoReleases activates release A (sequence 5) then B (sequence 6), keeping A's
// bytes in a separate file the operator can name for a rollback.
func twoReleases(t *testing.T, f *rtFixture) (a, b Candidate, aPath string) {
	t.Helper()
	a = signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	b = signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "B"})
	aPath = f.drop + "/previous-release.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(b)
	return a, b, aPath
}

func TestRollback_ToFileByFullDigest(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, b, aPath := twoReleases(t, f)

	info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)

	assert.Equal(t, digestOf(a.Bundle), info.FullDigest)
	assert.Equal(t, "5", info.Sequence)
	assert.True(t, info.Rollback)
	assert.True(t, info.Pinned)
	assert.True(t, info.SignatureVerified)
	assert.Equal(t, BundleSourceFile, info.Source)
	assert.Contains(t, f.logMessages(zapcore.WarnLevel), "TPA scanner bundle ROLLBACK: operator override")

	st := f.store.persisted(t)
	pub := st.Authorities[AuthorityPublisher]
	assert.Equal(t, uint64(6), pub.Watermark, "the watermark is not lowered")
	assert.Equal(t, digestOf(b.Bundle), pub.WatermarkSetBy)
	require.NotNil(t, pub.Pin)
	assert.Equal(t, digestOf(a.Bundle), pub.Pin.FullDigest)
	require.Len(t, pub.DenyList, 1)
	assert.Equal(t, digestOf(b.Bundle), pub.DenyList[0].FullDigest)
	rows := f.store.historyRows()
	require.Len(t, rows, 3)
	assert.True(t, rows[2].Rollback)
	assert.Equal(t, "5", rows[2].Sequence)
	assert.Contains(t, f.lkgFiles(), digestOf(a.Bundle)+".json", "the rollback target is durable")
}

func TestRollback_DenyListedReleaseIsNotReactivatedByTheNextReload(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, b, aPath := twoReleases(t, f)
	_, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)

	// The configured path still holds release B, the one rolled away from.
	info := ReloadBundle(f.log)

	assert.Equal(t, digestOf(a.Bundle), info.FullDigest, "the pinned target keeps serving")
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDenyListed, info.LastRejection.Reason)
	assert.NotEqual(t, digestOf(b.Bundle), info.FullDigest)
	assert.True(t, info.Pinned)
	assert.Len(t, f.store.historyRows(), 3, "a refusal is not an activation")
}

func TestRollback_PinGovernsIntermediateReleasesUntilANewerOneArrives(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	aPath := f.drop + "/a.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 8, Tag: "B"}))
	_, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)

	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "intermediate"}))
	info := f.run()
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDowngrade, info.LastRejection.Reason, "an intermediate release is refused while the pin binds")
	assert.Equal(t, digestOf(a.Bundle), info.FullDigest)

	newer := signedCandidate(t, keyPublisher, bundleOpts{Seq: 9, Tag: "newer"})
	info = f.activate(newer)
	assert.Equal(t, "9", info.Sequence)
	assert.False(t, info.Pinned, "a genuinely newer release supersedes the pin")
	assert.False(t, info.Rollback)
}

func TestRollback_Unpin(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, _, aPath := twoReleases(t, f)
	_, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)

	info, err := UnpinBundle(f.log)
	require.NoError(t, err)

	assert.False(t, info.Pinned)
	assert.False(t, info.Rollback)
	assert.Equal(t, BundleSourceEmbedded, info.Source,
		"without the pin the rolled-back release sits below the watermark and the install falls back to the embedded default, as a restart would")
	assert.True(t, info.CoverageDegraded)
	assert.Equal(t, DegradedEmbeddedBelowWatermark, info.DegradedReason)
	pub := f.store.persisted(t).Authorities[AuthorityPublisher]
	assert.Nil(t, pub.Pin)
	assert.Len(t, pub.DenyList, 1, "unpinning does not forgive the rolled-away release")
	assert.Equal(t, uint64(6), pub.Watermark)
	assert.Contains(t, f.logMessages(zapcore.WarnLevel), "TPA scanner bundle pin removed")
	assert.Len(t, f.store.historyRows(), 3, "unpin is not an activation")

	// Unpinning when nothing is pinned is a quiet no-op.
	commits := f.store.commitCount()
	_, err = UnpinBundle(f.log)
	require.NoError(t, err)
	assert.Equal(t, commits, f.store.commitCount())
}

func TestRollback_UsageErrors(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, _, aPath := twoReleases(t, f)
	prev := BundleStatus()
	good := digestOf(a.Bundle)

	cases := map[string]BundleRollbackRequest{
		"both embedded and path":    {Embedded: true, Path: aPath, Digest: good},
		"neither embedded nor path": {Digest: good},
		"12-hex fingerprint":        {Path: aPath, Digest: good[:12]},
		"uppercase digest":          {Path: aPath, Digest: strings.ToUpper(good)},
		"63 hex digits":             {Path: aPath, Digest: good[:63]},
		"non-hex digest":            {Path: aPath, Digest: strings.Repeat("z", 64)},
		"empty digest":              {Path: aPath},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := RollbackBundle(req, f.log)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBundleRollbackUsage)
			var refused *BundleRollbackRefused
			assert.False(t, errors.As(err, &refused), "a usage error is not a recorded rejection")
		})
	}
	assert.Equal(t, prev, BundleStatus(), "usage errors change nothing")
}

func TestRollback_EmbeddedDigestMismatchIsRecordedAndReturned(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	_, b, _ := twoReleases(t, f)
	chk := servingCheck()

	info, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: strings.Repeat("a", 64)}, f.log)

	var refused *BundleRollbackRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, RejectRollbackTargetMismatch, refused.Rejection.Reason)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectRollbackTargetMismatch, info.LastRejection.Reason)
	assert.Equal(t, digestOf(b.Bundle), info.FullDigest, "serving unchanged")
	assert.Same(t, chk, servingCheck())
}

// SC-010: a REAL 48-bit fingerprint collision. B shares A's 12-hex fingerprint
// and nothing else; neither the fingerprint nor the path may authorize the
// rollback, only the full digest.
func TestRollback_SC010_FingerprintCollisionIsRefused(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	bytesA, bytesB := sc010Bundle(sc010NonceA), sc010Bundle(sc010NonceB)
	candA := Candidate{Bundle: bytesA, Sidecar: signBundle(t, keyPublisher.priv, bytesA), SidecarPresent: true}
	candB := Candidate{Bundle: bytesB, Sidecar: signBundle(t, keyPublisher.priv, bytesB), SidecarPresent: true}
	require.Equal(t, sc010DigestA, digestOf(bytesA))
	require.Equal(t, sc010DigestB, digestOf(bytesB))

	// A is known-good (active, then superseded by a sequence-6 release).
	f.activate(candA)
	release6 := signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "six"})
	info := f.activate(release6)
	chk := servingCheck()

	// The operator names a path; an attacker has put B (same fingerprint) there.
	named := f.drop + "/rollback-target.json"
	f.putAt(named, candB)

	t.Run("a 12-hex fingerprint is a usage error", func(t *testing.T) {
		_, err := RollbackBundle(BundleRollbackRequest{Path: named, Digest: sc010Fingerprint}, f.log)
		assert.ErrorIs(t, err, ErrBundleRollbackUsage)
	})

	t.Run("A's full digest over B's bytes is a target mismatch", func(t *testing.T) {
		got, err := RollbackBundle(BundleRollbackRequest{Path: named, Digest: sc010DigestA}, f.log)
		var refused *BundleRollbackRefused
		require.ErrorAs(t, err, &refused)
		assert.Equal(t, RejectRollbackTargetMismatch, refused.Rejection.Reason)
		assert.Equal(t, info.FullDigest, got.FullDigest, "serving unchanged")
		assert.Same(t, chk, servingCheck())
		assert.Equal(t, uint64(6), f.store.persisted(t).Authorities[AuthorityPublisher].Watermark)
		assert.Nil(t, f.store.persisted(t).Authorities[AuthorityPublisher].Pin, "a refused rollback pins nothing")
		assert.Len(t, f.store.historyRows(), 2)
	})

	t.Run("A's full digest with A's bytes succeeds", func(t *testing.T) {
		f.putAt(named, candA)
		got, err := RollbackBundle(BundleRollbackRequest{Path: named, Digest: sc010DigestA}, f.log)
		require.NoError(t, err)
		assert.Equal(t, sc010DigestA, got.FullDigest)
		assert.True(t, got.Rollback)
		assert.True(t, got.Pinned)
		assert.Equal(t, uint64(6), f.store.persisted(t).Authorities[AuthorityPublisher].Watermark)
	})
}

func TestRollback_OperatorTargetIsMeasuredAgainstTheOperatorBaseline(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(10)
	}})
	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 11, Tag: "A"})
	aPath := f.drop + "/a.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 12, Tag: "B"}))
	chk := servingCheck()

	// A genuinely operator-signed bundle BELOW the operator baseline cannot be
	// reached through the rollback bypass.
	low := signedCandidate(t, keyOperator, bundleOpts{Seq: 8, Tag: "low"})
	lowPath := f.drop + "/low.json"
	f.putAt(lowPath, low)
	_, err := RollbackBundle(BundleRollbackRequest{Path: lowPath, Digest: digestOf(low.Bundle)}, f.log)
	var refused *BundleRollbackRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, RejectDowngrade, refused.Rejection.Reason)
	assert.Contains(t, refused.Rejection.Detail, "below operator baseline 10")
	assert.Same(t, chk, servingCheck())

	// Sequence 11 clears the baseline.
	info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)
	assert.Equal(t, "11", info.Sequence)
	assert.Equal(t, AuthorityOperator, info.Authority)
	assert.False(t, info.CoverageDegraded)
}

func TestRollback_BaselinelessOperatorProceedsDegraded(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator }})
	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "A"})
	aPath := f.drop + "/a.json"
	f.putAt(aPath, a)
	f.activate(a)
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 4, Tag: "B"}))

	info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)

	require.NoError(t, err)
	assert.Equal(t, "3", info.Sequence)
	assert.True(t, info.CoverageDegraded)
	assert.Equal(t, DegradedOperatorAuthorityUnbaselined, info.DegradedReason)
}

func TestRollback_RolledBackToTheServingBundleDoesNotDenyListIt(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(a)

	info, err := RollbackBundle(BundleRollbackRequest{Path: f.path, Digest: digestOf(a.Bundle)}, f.log)

	require.NoError(t, err)
	assert.Equal(t, digestOf(a.Bundle), info.FullDigest)
	assert.Empty(t, f.store.persisted(t).Authorities[AuthorityPublisher].DenyList,
		"the release being rolled TO is never the one denied")
	assert.Equal(t, digestOf(a.Bundle), ReloadBundle(f.log).FullDigest)
}

// SC-010 replay: the history is durable, ordered, and survives a restart.
func TestRollback_HistoryReplaysAcrossRestart(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	const n = 12
	var digests []string
	for seq := uint64(1); seq <= n; seq++ {
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: seq, Tag: "h"})
		f.activate(c)
		digests = append(digests, digestOf(c.Bundle))
	}

	check := func() {
		rows, err := BundleHistory()
		require.NoError(t, err)
		require.Len(t, rows, n)
		for i, row := range rows {
			assert.Equal(t, digests[i], row.FullDigest, "row %d", i)
			assert.Equal(t, AuthorityPublisher, row.Authority)
			assert.True(t, row.SignatureVerified)
			assert.Equal(t, BundleSourceFile, row.Source)
		}
	}
	check()
	f.restart()
	check()
	// A restart plus a refresh of the unchanged file adds nothing.
	f.run()
	check()
}

// A rollback and an unpin exist to RECORD an override (pin, deny-list); against a
// state store that cannot be read they are refused rather than silently applied.
func TestRollback_RefusedWhileTheStateIsUnreadable(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, _, aPath := twoReleases(t, f)

	f.store.failLoad = errors.New("bolt: timeout")
	f.restart()
	f.run()
	prev := BundleStatus()

	_, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	var refused *BundleRollbackRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, RejectStateCommitFailed, refused.Rejection.Reason)

	_, err = UnpinBundle(f.log)
	assert.Error(t, err)
	assert.Equal(t, prev.FullDigest, BundleStatus().FullDigest, "refusals never change what serves")
}
