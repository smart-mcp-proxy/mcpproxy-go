package scanner

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T323: the activation self-check runs for a freshly offered candidate and for
// an explicit rollback, and NOT when a bundle that already passed is restored
// (startup LKG restore, rollback-pin restore). Tasks B13, Δ27.
func TestSelfCheckMode_RunsForNormalAndRollbackOnly(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
	var calls atomic.Int32
	install := func() { withSelfCheck(t, func(*BundleCheck) error { calls.Add(1); return nil }) }
	install()

	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"})
	b := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"})
	aPath := f.drop + "/previous.json"
	f.putAt(aPath, a)

	f.activate(a)
	assert.EqualValues(t, 1, calls.Load(), "evalNormal runs the self-check once")
	f.activate(b)
	require.EqualValues(t, 2, calls.Load())

	calls.Store(0)
	info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)
	require.Equal(t, digestOf(a.Bundle), info.FullDigest)
	assert.EqualValues(t, 1, calls.Load(), "evalRollback runs the self-check once")

	calls.Store(0)
	f.restart()
	install() // a restart clears the hook, like a fresh process
	info = f.run()
	require.Equal(t, digestOf(a.Bundle), info.FullDigest, "the rolled-back bundle is restored at startup")
	assert.Zero(t, calls.Load(), "a startup LKG restore does not run the self-check")

	// The pin restore (FR-004b): away from the operator root and back.
	f.sec.TPAActiveRoot = config.TPARootPublisher
	f.run()
	calls.Store(0)
	f.sec.TPAActiveRoot = config.TPARootOperator
	info = f.run()
	require.Equal(t, digestOf(a.Bundle), info.FullDigest, "the pinned target is restored")
	assert.Zero(t, calls.Load(), "a pin restore does not run the self-check")
}
