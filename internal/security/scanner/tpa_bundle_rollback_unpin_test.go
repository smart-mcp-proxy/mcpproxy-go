package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UnpinBundle must re-validate the serving bundle even when nothing about the
// policy or the file bytes changed since the last run: the rolled-back target
// sits AT the configured path, so only the forced re-run (not the idempotence
// shortcut) can notice that, unpinned, it is below the watermark.
func TestRollback_UnpinRevalidatesATargetAtTheConfiguredPath(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(a)
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "B"}))

	// The operator restores A's bytes to the configured path and rolls back to
	// them there; the next reload leaves them serving (pinned).
	f.put(a)
	_, err := RollbackBundle(BundleRollbackRequest{Path: f.path, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)
	info := ReloadBundle(f.log)
	require.Equal(t, digestOf(a.Bundle), info.FullDigest)
	require.True(t, info.Pinned)

	info, err = UnpinBundle(f.log)
	require.NoError(t, err)

	assert.False(t, info.Pinned)
	assert.Equal(t, BundleSourceEmbedded, info.Source, "unpinned, sequence 5 sits below the watermark 6 and stops serving")
	assert.Equal(t, DegradedEmbeddedBelowWatermark, info.DegradedReason)
	assert.True(t, info.CoverageDegraded)
}

// FR-010 / FR-009: an explicit rollback to a signed target that never went
// through normal activation is itself a signed activation, so it arms the
// signature ratchet and the "an external corpus once served" latch.
func TestRollback_ToAnUnactivatedSignedTargetArmsTheRatchet(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	target := signedCandidate(t, keyPublisher, bundleOpts{Seq: 7, Tag: "never-activated"})
	targetPath := f.drop + "/target.json"
	f.putAt(targetPath, target)

	info, err := RollbackBundle(BundleRollbackRequest{Path: targetPath, Digest: digestOf(target.Bundle)}, f.log)
	require.NoError(t, err)
	require.Equal(t, digestOf(target.Bundle), info.FullDigest)

	st := f.store.persisted(t)
	assert.True(t, st.SignatureRatchet, "a signed rollback target arms the ratchet")
	assert.True(t, st.Authorities[AuthorityPublisher].ExternalEverActivated)

	// The ratchet is live: an unsigned drop at the configured path is refused.
	f.put(unsignedCandidate(t, bundleOpts{Seq: 8}))
	info = f.run()
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectUnsignedRatchet, info.LastRejection.Reason)
	assert.Equal(t, digestOf(target.Bundle), info.FullDigest)
}
