package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// FR-009a(b): an install whose operator root once served an external corpus
// must be degraded when that corpus is "no longer available or no longer
// valid". An operator epoch bump invalidates the old operator bundle
// (stale_epoch_replay); re-scoping the authority record must not forget that
// the operator corpus ever served.
func TestRuntime_OperatorEpochBumpWithoutANewBundleIsOperatorBundleMissing(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "old-epoch"}))

	// Key-compromise recovery: the operator bumps their epoch; the old bundle
	// is now a stale-epoch replay and the embedded default serves.
	f.sec.TPAOperatorEpoch = u64p(1)
	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectStaleEpochReplay, info.LastRejection.Reason)
	assert.True(t, info.CoverageDegraded, "the operator's corpus is no longer valid, so the embedded fallback is not full coverage")
	assert.Equal(t, DegradedOperatorBundleMissing, info.DegradedReason)
	assert.True(t, f.store.persisted(t).Authorities[AuthorityOperator].ExternalEverActivated,
		"the re-scope keeps the record that this authority once served a corpus")

	// The same after a restart: the latch is durable, not an in-memory leftover.
	f.restart()
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.True(t, info.CoverageDegraded)
	assert.Equal(t, DegradedOperatorBundleMissing, info.DegradedReason)

	// A newly activated operator bundle in the new epoch serves externally, so
	// the condition is moot.
	info = f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Epoch: 1, Tag: "new-epoch"}))
	assert.Equal(t, AuthorityOperator, info.Authority)
	assert.False(t, info.CoverageDegraded)
}

func TestRuntime_OperatorRootWithNothingEverActivatedIsNotDegraded(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})

	info := f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.False(t, info.CoverageDegraded, "a fresh install that declared the operator root has lost nothing")

	// Bumping the epoch on such an install has still lost nothing.
	f.sec.TPAOperatorEpoch = u64p(1)
	info = f.run()
	assert.False(t, info.CoverageDegraded)
}
