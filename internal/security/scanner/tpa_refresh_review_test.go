package scanner

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Review findings on Spec 101 US3 (refresher): the live security block can be
// removed, and a manual cycle must not undo an early re-arm.

// removeSecurityBlock mimics deleting the whole `security` block and hot
// reloading: the runtime gets the default policy and the refresher is nudged
// with a nil config, while the provider now answers nil.
func (h *refresherHarness) removeSecurityBlock() {
	h.t.Helper()
	h.mu.Lock()
	h.cfg = nil
	h.mu.Unlock()
	ConfigureBundleFromConfig(nil, h.f.log)
	h.ref.Nudge(nil)
}

func servingPolicy() *bundlePolicy {
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	return bundleRT.policy
}

func TestRefreshScheduler_RemovingTheSecurityBlockRevertsToTheDefaultPolicy(t *testing.T) {
	ch := newTestChannel(t)
	pair := ch.publishRelease(2, "fetched")
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, nil)
	h.start()

	// A nudge while the block exists stores the fetch-enabled config.
	h.reload(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(1) })
	require.Equal(t, RefreshClassActivated, h.tick(), "the fetch leg activates the operator-signed release")
	require.Equal(t, digestOf(pair.Bundle), BundleStatus().FullDigest)
	requestsBefore := ch.count()
	require.True(t, servingPolicy().FetchEnabled)

	h.removeSecurityBlock()
	h.tick()

	assert.Equal(t, requestsBefore, ch.count(), "the removed fetch settings must not issue another request")
	p := servingPolicy()
	assert.False(t, p.FetchEnabled, "the cycle must not restore the stale fetch setting")
	assert.Equal(t, config.TPARootPublisher, p.ActiveRoot, "the cycle must not restore the stale trust set")
	assert.Equal(t, BundleSourceEmbedded, BundleStatus().Source, "the operator key is no longer trusted: the embedded corpus serves")
	assert.Equal(t, fingerprintOf(nil), p.Fingerprint)

	// A manual refresh behaves the same.
	h.ref.Refresh(t.Context(), f.log)
	assert.Equal(t, requestsBefore, ch.count())
	assert.False(t, servingPolicy().FetchEnabled)
}

func TestRefreshScheduler_NilNudgeIsTheDefaultPolicy(t *testing.T) {
	ch := newTestChannel(t)
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, nil)
	h.start()
	gen := h.ref.currentGeneration()

	h.removeSecurityBlock()

	assert.Greater(t, h.ref.currentGeneration(), gen, "dropping the fetch settings supersedes in-flight downloads")
	h.ref.mu.Lock()
	fp := h.ref.fp
	h.ref.mu.Unlock()
	assert.Equal(t, fingerprintOf(nil), fp)
}

func TestRefreshScheduler_ManualCycleKeepsAnEarlyRearm(t *testing.T) {
	ch := newTestChannel(t)
	pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "held"})
	g := holdReleaseJSON(ch, pair, 2)
	f := newFileAndFetchFixture(t, ch, true)
	h := newRefresherHarness(t, f, nil)
	h.start()
	timer := h.clock.loop(t)
	armsBefore := timer.arms()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ref.Refresh(t.Context(), f.log)
	}()
	g.waitReached(t)

	// The fetch settings change while the manual cycle is in flight. The fake
	// clock does not move: a different rand keeps the early look off the
	// instant the first-cycle timer is already armed for (an identical instant
	// needs no re-arm).
	h.setRand(0.2)
	h.reload(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(2) })
	require.Eventually(t, func() bool { return timer.arms() > armsBefore }, 5*time.Second, time.Millisecond)
	early := timer.last()
	assert.Less(t, early, 6*time.Minute, "the settings change asked for a look within the first-cycle window")
	armsAfterNudge := timer.arms()

	close(g.release)
	<-done
	time.Sleep(150 * time.Millisecond)

	assert.Equal(t, armsAfterNudge, timer.arms(), "the finished manual cycle must not push the early look out to a full interval")
	assert.Equal(t, early, timer.last())
}

// --- offered-run state_commit_failed (fetch leg) ---------------------------------------

func TestRefreshRuntime_OfferedCommitFailureKeepsFetchedBundleServing(t *testing.T) {
	f := newFetchFixture(t, nil)
	prev := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "prev"})
	require.Equal(t, digestOf(prev.Bundle), runOffered(prev, f.log).FullDigest)
	chk := servingCheck()
	lkgBefore := f.lkgFiles()
	rowsBefore := len(f.store.historyRows())

	f.store.failCommit = errors.New("disk full")
	next := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "next"})
	info, res := offerCandidate(next, f.log)

	assert.Equal(t, RefreshOutcomeRejected, res.Outcome)
	require.NotNil(t, res.Rejection)
	assert.Equal(t, RejectStateCommitFailed, res.Rejection.Reason)
	assert.Equal(t, digestOf(prev.Bundle), info.FullDigest, "the previous fetched bundle keeps serving")
	assert.Same(t, chk, servingCheck())
	assert.Empty(t, info.LoadError, "a refused offer is the fetch leg's business, not the file leg's load_error")
	assert.ElementsMatch(t, lkgBefore, f.lkgFiles(), "the LKG of the failed digest is reconciled away")
	assert.Len(t, f.store.historyRows(), rowsBefore)
	assert.Equal(t, uint64(2), f.store.persisted(t).Authorities[AuthorityOperator].Watermark)

	// The disk recovers: the very same pair is not remembered as refused.
	f.store.failCommit = nil
	_, res = offerCandidate(next, f.log)
	assert.Equal(t, RefreshOutcomeActivated, res.Outcome)
	assert.Equal(t, digestOf(next.Bundle), BundleStatus().FullDigest)
}

func TestRefreshRuntime_OfferedCommitFailureOnFreshInstallKeepsEmbeddedServing(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.store.failCommit = errors.New("read-only database")
	c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "fresh"})

	info, res := offerCandidate(c, f.log)

	assert.Equal(t, RefreshOutcomeRejected, res.Outcome)
	require.NotNil(t, res.Rejection)
	assert.Equal(t, RejectStateCommitFailed, res.Rejection.Reason)
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.False(t, info.CoverageDegraded)
	assert.Empty(t, info.LoadError)
	assert.Empty(t, f.lkgFiles())

	f.store.failCommit = nil
	_, res = offerCandidate(c, f.log)
	assert.Equal(t, RefreshOutcomeActivated, res.Outcome, "the same pair activates once the disk recovers")
}
