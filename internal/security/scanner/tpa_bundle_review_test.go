package scanner

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for the Wave 2 judge's findings (spec 101 FR-010, FR-008,
// FR-011a, FR-007/FR-009a).

// FR-010 / SC-002: an explicit rollback away from an UNSIGNED external bundle
// must not be undone by the next refresh. Unsigned releases are not deny-listed
// (they carry no trustworthy sequence), so the pin has to govern them.
func TestRollback_UnsignedReleaseRolledAwayIsNotReactivated(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	u := unsignedCandidate(t, bundleOpts{Tag: "U"})
	f.activate(u)

	info, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: embeddedDigest()}, f.log)
	require.NoError(t, err)
	require.Equal(t, BundleSourceEmbedded, info.Source)
	require.True(t, info.Pinned)
	require.True(t, info.Rollback)

	// The path still holds U; a reload (or any config hot-reload) must not
	// serve it again.
	info = ReloadBundle(f.log)
	assert.Equal(t, BundleSourceEmbedded, info.Source, "the operator's rollback is not silently reverted")
	assert.Equal(t, embeddedDigest(), info.FullDigest)
	assert.True(t, info.Pinned)
	assert.True(t, info.Rollback)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
	assert.Len(t, f.store.historyRows(), 2, "a refusal is not an activation")

	f.restart()
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "and a restart does not revert it either")
	assert.Len(t, f.store.historyRows(), 2)

	// Unpinning is the operator's explicit way back.
	info, err = UnpinBundle(f.log)
	require.NoError(t, err)
	assert.False(t, info.Pinned)
	assert.Equal(t, digestOf(u.Bundle), info.FullDigest)
}

// A rollback to a named unsigned target must still work while another pin binds.
func TestRollback_ToUnsignedTargetWhileAnotherPinBinds(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.activate(unsignedCandidate(t, bundleOpts{Tag: "U1"}))
	_, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: embeddedDigest()}, f.log)
	require.NoError(t, err)

	u2 := unsignedCandidate(t, bundleOpts{Tag: "U2"})
	u2Path := f.drop + "/u2.json"
	f.putAt(u2Path, u2)
	info, err := RollbackBundle(BundleRollbackRequest{Path: u2Path, Digest: digestOf(u2.Bundle)}, f.log)
	require.NoError(t, err)
	assert.Equal(t, digestOf(u2.Bundle), info.FullDigest)
	assert.True(t, info.Pinned)
}

// FR-010: while a pin to a different target binds, even the release that set the
// watermark is refused.
func TestRollback_PinRefusesTheWatermarkSetterToo(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	x := signedCandidate(t, keyPublisher, bundleOpts{Seq: 10, Tag: "X"})
	f.activate(x)

	// The operator clears the configured path (embedded serves) and rolls back to
	// the embedded default: nothing external was serving, so nothing is
	// deny-listed.
	f.sec.TPABundlePath = ""
	info := f.run()
	require.Equal(t, BundleSourceEmbedded, info.Source)
	_, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: embeddedDigest()}, f.log)
	require.NoError(t, err)

	f.sec.TPABundlePath = f.path
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "the pin binds; the setter is not special")
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
	assert.True(t, info.Pinned)

	// A genuinely newer release still supersedes the pin.
	info = f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 11, Tag: "newer"}))
	assert.False(t, info.Pinned)
}

// FR-011a: while the state cannot be loaded a NEW external activation is refused
// (activating without a durable watermark reopens the replay window).
func TestRuntime_StateLoadFailureRefusesNewActivation(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.store.failLoad = errors.New("bolt: timeout")
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))

	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectStateCommitFailed, info.LastRejection.Reason)
	assert.Zero(t, f.store.commitCount())
	assert.Empty(t, f.lkgFiles())

	// Once the store recovers the activation is recorded.
	f.store.failLoad = nil
	info = f.run()
	assert.Equal(t, "5", info.Sequence)
	assert.Equal(t, 1, f.store.commitCount())
	assert.Len(t, f.store.historyRows(), 1)
}

// FR-008 stage order: sidecar parse precedes the bundle byte ceiling.
func TestRuntime_MalformedSidecarOutranksOversizedBundle(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	prev := BundleStatus()
	chk := servingCheck()
	require.NoError(t, writeFileForTest(f.path, bytes.Repeat([]byte{'x'}, MaxBundleBytes+1)))
	require.NoError(t, writeFileForTest(f.path+".sig", bytes.Repeat([]byte{'g'}, 4096)))

	info := f.run()

	f.requireRejected(info, RejectSidecarInvalid, prev.FullDigest, chk)
}

// An oversized bundle with a WELL-FORMED sidecar is still a bundle ceiling.
func TestRuntime_OversizedBundleWithValidSidecarIsCeiling(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5})
	require.NoError(t, writeFileForTest(f.path, bytes.Repeat([]byte{'x'}, MaxBundleBytes+1)))
	require.NoError(t, writeFileForTest(f.path+".sig", c.Sidecar))

	info := f.run()

	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectCeilingExceeded, info.LastRejection.Reason)
}

// FR-007 / FR-009a: a non-zero publisher fallback baseline must not make the
// embedded default degraded on an ordinary fresh install.
func TestRuntime_EmbeddedWithoutSequenceIsTheFallbackBaseline(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	publisherFallbackBaseline = 5 // restored by withTrustAnchors' cleanup

	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.False(t, info.CoverageDegraded, "reason %q", info.DegradedReason)
}
