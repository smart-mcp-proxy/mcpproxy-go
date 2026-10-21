package scanner

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func operatorFixture(t *testing.T) *rtFixture {
	t.Helper()
	return newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
}

// T328: the activation observer fires once per history row and never
// otherwise (tasks B17).
func TestBundleObserver_FiresOncePerHistoryRow(t *testing.T) {
	f := operatorFixture(t)
	obs := recordActivations(t)

	a := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A", GeneratedAt: "2026-10-01T00:00:00Z"})
	b := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"})
	aPath := f.drop + "/previous.json"
	f.putAt(aPath, a)

	f.activate(a)
	rows := obs.all()
	require.Len(t, rows, 1)
	assert.Equal(t, digestOf(a.Bundle), rows[0].Digest)
	assert.Equal(t, "5", rows[0].Sequence)
	assert.Equal(t, AuthorityOperator, rows[0].Authority)
	assert.Equal(t, BundleSourceFile, rows[0].Source)
	assert.False(t, rows[0].Rollback)
	assert.Empty(t, rows[0].PreviousDigest, "the embedded default served before")
	assert.False(t, rows[0].ActivatedAt.IsZero())

	t.Run("an idempotent rerun does not fire", func(t *testing.T) {
		f.run()
		f.run()
		assert.Equal(t, 1, obs.count())
	})

	t.Run("a refusal does not fire", func(t *testing.T) {
		bad := b
		bad.Bundle = flipByte(t, b.Bundle, "bundle-marker")
		f.put(bad)
		info := f.run()
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, 1, obs.count())
	})

	t.Run("an LKG restore does not fire", func(t *testing.T) {
		f.put(a)
		f.restart()
		info := f.run()
		require.Equal(t, digestOf(a.Bundle), info.FullDigest)
		assert.Equal(t, 1, obs.count())
	})

	t.Run("a second activation fires with the previous digest", func(t *testing.T) {
		f.activate(b)
		rows := obs.all()
		require.Len(t, rows, 2)
		assert.Equal(t, digestOf(b.Bundle), rows[1].Digest)
		assert.Equal(t, digestOf(a.Bundle), rows[1].PreviousDigest)
	})

	t.Run("a rollback fires once with Rollback set", func(t *testing.T) {
		info, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
		require.NoError(t, err)
		require.Equal(t, digestOf(a.Bundle), info.FullDigest)
		rows := obs.all()
		require.Len(t, rows, 3)
		assert.True(t, rows[2].Rollback)
		assert.Equal(t, digestOf(a.Bundle), rows[2].Digest)
		assert.Equal(t, digestOf(b.Bundle), rows[2].PreviousDigest)
	})

	t.Run("an embedded fallback does not fire", func(t *testing.T) {
		f.sec.TPAActiveRoot = config.TPARootPublisher
		info := f.run()
		require.Equal(t, BundleSourceEmbedded, info.Source)
		assert.Equal(t, 3, obs.count())
	})
}

func TestBundleObserver_RunsOutsideTheRuntimeMutex(t *testing.T) {
	f := operatorFixture(t)
	called := make(chan BundleInfo, 1)
	unsub := AddBundleActivationObserver(func(BundleActivation) {
		// BundleStatus on a published runtime does not lock, but the observer
		// also asks for a reload, which does: it must not deadlock against the
		// activation that fired it.
		called <- ReloadBundle(nil)
	})
	t.Cleanup(unsub)

	c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"})
	f.activate(c)

	select {
	case info := <-called:
		assert.Equal(t, digestOf(c.Bundle), info.FullDigest)
	case <-time.After(10 * time.Second):
		t.Fatal("the observer did not return: it is dispatched while holding bundleRT.mu")
	}
}

func TestBundleObserver_UnsubscribeStopsDelivery(t *testing.T) {
	f := operatorFixture(t)
	var mu sync.Mutex
	n := 0
	unsub := AddBundleActivationObserver(func(BundleActivation) { mu.Lock(); n++; mu.Unlock() })

	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"}))
	drainBundleObserversForTest()
	mu.Lock()
	require.Equal(t, 1, n)
	mu.Unlock()

	unsub()
	unsub() // idempotent
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"}))
	drainBundleObserversForTest()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, n)
}

func TestBundleObserver_DrainWaitsForInFlightDispatches(t *testing.T) {
	f := operatorFixture(t)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var done sync.WaitGroup
	done.Add(1)
	unsub := AddBundleActivationObserver(func(BundleActivation) {
		started <- struct{}{}
		<-release
		done.Done()
	})
	t.Cleanup(unsub)

	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"}))
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("the observer was never called")
	}

	drained := make(chan struct{})
	go func() { drainBundleObserversForTest(); close(drained) }()
	select {
	case <-drained:
		t.Fatal("drain returned while a dispatch was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("drain never returned")
	}
	done.Wait()
}

func TestBundleObserver_RefreshObserverIsReplaceable(t *testing.T) {
	var got []RefreshOutcomeClass
	var mu sync.Mutex
	SetBundleRefreshObserver(func(c RefreshOutcomeClass) { mu.Lock(); got = append(got, c); mu.Unlock() })
	t.Cleanup(func() { SetBundleRefreshObserver(nil) })

	notifyRefreshObserver(RefreshClassActivated)
	SetBundleRefreshObserver(nil)
	notifyRefreshObserver(RefreshClassRejected)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []RefreshOutcomeClass{RefreshClassActivated}, got)
}

// US3 judge finding 7: dispatch stamps a strictly increasing Seq in commit
// order, so an observer can drop an event that arrives after a newer one.
func TestBundleObserver_SequenceFollowsCommitOrder(t *testing.T) {
	f := operatorFixture(t)
	var mu sync.Mutex
	var seqs []uint64
	unsub := AddBundleActivationObserver(func(a BundleActivation) {
		mu.Lock()
		seqs = append(seqs, a.Seq)
		mu.Unlock()
	})
	defer unsub()

	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"}))
	drainBundleObserversForTest() // arrival order == commit order for this assertion
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "B"}))
	drainBundleObserversForTest()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seqs, 2)
	assert.NotZero(t, seqs[0])
	assert.Less(t, seqs[0], seqs[1], "a later commit gets a higher sequence number")
}
