package scanner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// SC-012: an attacker who can replace config.db hands the loader a state with a
// lowered watermark, a cleared ratchet and a forged pin. The guarantees that
// must survive are exactly the ones anchored OUTSIDE the data directory: the
// publisher baseline (from the binary), the operator baseline, require_signed
// and the active-root declaration. This test asserts only those; it deliberately
// asserts nothing about the ratchet, the watermark or the pin surviving.
func TestSC012_StateSubstitution_OutOfDataDirAnchorsStillHold(t *testing.T) {
	forged := func(pinDigest string, pinSeq uint64) *TPABundleState {
		return &TPABundleState{
			SchemaVersion:    TPABundleStateSchemaVersion,
			SignatureRatchet: false, // cleared
			Authorities: map[string]*TPAAuthorityState{
				AuthorityPublisher: {Epoch: 0, Watermark: 0, Pin: &TPABundlePin{Sequence: pinSeq, FullDigest: pinDigest, PinnedAt: time.Now()}},
				AuthorityOperator:  {Epoch: 0, Watermark: 0, Pin: &TPABundlePin{Sequence: pinSeq, FullDigest: pinDigest, PinnedAt: time.Now()}},
			},
		}
	}

	t.Run("publisher baseline refuses an older publisher bundle, even one a forged pin names", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{EmbeddedSeq: 100})
		old := signedCandidate(t, keyPublisher, bundleOpts{Seq: 50, Tag: "old"})
		f.store.loadOverride = forged(digestOf(old.Bundle), 50)
		f.put(old)

		info := f.run()

		assert.Equal(t, embeddedDigest(), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
		assert.Contains(t, info.LastRejection.Detail, "baseline 100")
	})

	t.Run("require_signed_bundle still refuses an unsigned drop", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.RequireSignedBundle = true }})
		f.store.loadOverride = forged("", 0)
		f.put(unsignedCandidate(t, bundleOpts{Seq: 5}))

		info := f.run()

		assert.Equal(t, embeddedDigest(), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectSignatureRequired, info.LastRejection.Reason)
	})

	t.Run("operator baseline still refuses an older operator bundle", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootOperator
			s.TPAOperatorBaseline = u64p(10)
		}})
		old := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "old"})
		f.store.loadOverride = forged(digestOf(old.Bundle), 5)
		f.put(old)

		info := f.run()

		assert.Equal(t, embeddedDigest(), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
	})

	t.Run("the root declaration still refuses the other authority", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{})
		f.store.loadOverride = forged("", 0)
		f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 50}))

		info := f.run()

		assert.Equal(t, embeddedDigest(), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectAuthorityNotActive, info.LastRejection.Reason)
	})
}
