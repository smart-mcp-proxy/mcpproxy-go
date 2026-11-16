package types

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type transitionSink struct {
	mu  sync.Mutex
	got []Transition
}

func (s *transitionSink) observe(t Transition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, t)
}

func (s *transitionSink) snapshot() []Transition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Transition(nil), s.got...)
}

func (s *transitionSink) await(t *testing.T, n int) []Transition {
	t.Helper()
	require.Eventually(t, func() bool { return len(s.snapshot()) >= n }, 5*time.Second, time.Millisecond)
	return s.snapshot()
}

// Every mutation is recorded with its states, its time and the error of that
// moment, and delivered in the order it happened, including the paths whose
// state-change callback is dispatched in a goroutine.
func TestTransitionCapture_OrderedAcrossSyncAndAsyncPaths(t *testing.T) {
	sm := NewStateManager()
	sink := &transitionSink{}
	sm.SetTransitionObserver(sink.observe)

	boom := errors.New("connection reset")
	before := time.Now()
	sm.TransitionTo(StateConnecting)       // sync callback path
	sm.TransitionTo(StateReady)            // sync
	sm.SetError(boom)                      // async callback path
	sm.ResetForReconnect()                 // async
	sm.TransitionTo(StateConnecting)       // sync
	sm.SetTerminalError(boom, "MCPX_X")    // async
	sm.SetPendingAuth(errors.New("login")) // sync
	sm.SetOAuthError(boom)                 // sync
	sm.Reset()                             // async
	sm.TransitionTo(StateReady)
	after := time.Now()

	got := sink.await(t, 10)
	require.Len(t, got, 10)
	want := [][2]ConnectionState{
		{StateDisconnected, StateConnecting},
		{StateConnecting, StateReady},
		{StateReady, StateError},
		{StateError, StateDisconnected},
		{StateDisconnected, StateConnecting},
		{StateConnecting, StateError},
		{StateError, StatePendingAuth},
		{StatePendingAuth, StateError},
		{StateError, StateDisconnected},
		{StateDisconnected, StateReady},
	}
	for i, w := range want {
		assert.Equal(t, w[0], got[i].From, "transition %d from", i)
		assert.Equal(t, w[1], got[i].To, "transition %d to", i)
		assert.False(t, got[i].At.Before(before) || got[i].At.After(after), "transition %d time", i)
		if i > 0 {
			assert.False(t, got[i].At.Before(got[i-1].At), "times are in order")
		}
	}
	assert.Equal(t, boom, got[2].Err, "the error of the moment is captured")
	assert.Nil(t, got[1].Err)
	assert.Equal(t, "MCPX_X", got[5].TerminalCode)
	assert.Nil(t, got[9].Err, "Ready clears the error")
}

// Concurrent mutators: delivery is one at a time and matches the order the
// state lock admitted them (each From is the previous To). Run with -race.
func TestTransitionCapture_ConcurrentMutationsStayOrdered(t *testing.T) {
	sm := NewStateManager()
	sink := &transitionSink{}
	var inObserver int32
	var overlap bool
	var omu sync.Mutex
	sm.SetTransitionObserver(func(tr Transition) {
		omu.Lock()
		inObserver++
		if inObserver > 1 {
			overlap = true
		}
		omu.Unlock()
		sink.observe(tr)
		omu.Lock()
		inObserver--
		omu.Unlock()
	})

	const workers, each = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if (w+i)%2 == 0 {
					sm.SetError(errors.New("x"))
				} else {
					sm.TransitionTo(StateReady)
				}
			}
		}(w)
	}
	wg.Wait()

	got := sink.await(t, workers*each)
	require.Len(t, got, workers*each)
	assert.False(t, overlap, "the observer is never called concurrently for one connection")
	for i := 1; i < len(got); i++ {
		assert.Equal(t, got[i-1].To, got[i].From, "transition %d continues from the previous one", i)
	}
}

// A stalled observer cannot grow the queue without bound, and the newest
// state still arrives.
func TestTransitionCapture_StalledObserverKeepsTheNewest(t *testing.T) {
	sm := NewStateManager()
	sink := &transitionSink{}
	release := make(chan struct{})
	first := true
	sm.SetTransitionObserver(func(tr Transition) {
		if first {
			first = false
			<-release
		}
		sink.observe(tr)
	})

	sm.TransitionTo(StateConnecting) // taken by the drain, which then blocks
	require.Eventually(t, func() bool {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		return sm.draining && len(sm.transitions) == 0
	}, 5*time.Second, time.Millisecond)

	for i := 0; i < maxQueuedTransitions*2; i++ {
		sm.SetError(errors.New("x"))
	}
	sm.TransitionTo(StateReady)

	sm.mu.RLock()
	queued := len(sm.transitions)
	sm.mu.RUnlock()
	assert.LessOrEqual(t, queued, maxQueuedTransitions)

	close(release)
	require.Eventually(t, func() bool {
		got := sink.snapshot()
		return len(got) > 0 && got[len(got)-1].To == StateReady
	}, 5*time.Second, time.Millisecond, "the final Ready is delivered")

	// Nothing was dropped: the delivered records form one unbroken chain, and
	// the transitions that were folded are accounted for.
	require.Eventually(t, func() bool {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		return !sm.draining
	}, 5*time.Second, time.Millisecond)
	got := sink.snapshot()
	total := 0
	for i, tr := range got {
		total += 1 + tr.Folded
		if i > 0 {
			assert.Equal(t, got[i-1].To, tr.From, "record %d continues from the previous one", i)
		}
	}
	assert.Equal(t, 1+maxQueuedTransitions*2+1, total, "every transition is delivered or counted as folded")
}

// A full queue folds a pair whose middle state is not Ready first, so a loss
// and a recovery stay separate records while any other pair can be folded.
func TestTransitionCapture_FoldingKeepsLossAndRecoveryApart(t *testing.T) {
	sm := NewStateManager()
	sm.mu.Lock()
	sm.transitions = []Transition{
		{From: StateError, To: StateReady},      // a recovery
		{From: StateReady, To: StateError},      // a loss
		{From: StateError, To: StateConnecting}, // foldable with the next
		{From: StateConnecting, To: StateError},
	}
	sm.foldOldestLocked()
	q := append([]Transition(nil), sm.transitions...)
	sm.mu.Unlock()

	require.Len(t, q, 3)
	assert.Equal(t, Transition{From: StateError, To: StateReady}, q[0], "the recovery is still its own record")
	// The loss absorbed the step after it: it still leaves Ready, so it is
	// still a loss, and it says one transition was folded into it.
	assert.Equal(t, Transition{From: StateReady, To: StateConnecting, Folded: 1}, q[1])
	assert.Equal(t, Transition{From: StateConnecting, To: StateError}, q[2])
	for i := 1; i < len(q); i++ {
		assert.Equal(t, q[i-1].To, q[i].From, "the chain is unbroken")
	}
}

// Without an observer nothing is queued and no goroutine is started.
func TestTransitionCapture_NoObserverNoQueue(t *testing.T) {
	sm := NewStateManager()
	sm.TransitionTo(StateConnecting)
	sm.SetError(errors.New("x"))
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	assert.Empty(t, sm.transitions)
	assert.False(t, sm.draining)
}

// Review round 2: folding must not erase an outage. An outage and its
// recovery fold to Ready -> Ready only when no other pair is left, and then
// the record says what it hid.
func TestTransitionCapture_FoldingNeverErasesAnOutage(t *testing.T) {
	sm := NewStateManager()
	sm.mu.Lock()
	sm.transitions = []Transition{
		{From: StateReady, To: StateError},
		{From: StateError, To: StateConnecting},
		{From: StateConnecting, To: StateReady},
	}
	sm.foldOldestLocked()
	require.Len(t, sm.transitions, 2)
	assert.Equal(t, Transition{From: StateReady, To: StateConnecting, Folded: 1}, sm.transitions[0],
		"the loss is still a loss")
	sm.foldOldestLocked()
	q := append([]Transition(nil), sm.transitions...)
	sm.mu.Unlock()

	require.Len(t, q, 1)
	assert.Equal(t, StateReady, q[0].From)
	assert.Equal(t, StateReady, q[0].To)
	assert.Equal(t, 1, q[0].FoldedLost, "the hidden loss is counted")
	assert.Equal(t, 1, q[0].FoldedRestored, "the hidden recovery is counted")
}

// A login requirement hidden by a fold is counted too.
func TestTransitionCapture_FoldingCountsAHiddenLoginRequirement(t *testing.T) {
	f, hides := foldPair(
		Transition{From: StateConnecting, To: StatePendingAuth},
		Transition{From: StatePendingAuth, To: StateConnecting},
	)
	assert.True(t, hides)
	assert.Equal(t, 1, f.FoldedLogin)
}

// Review round 5: folding remembers whether a login requirement came after
// the last recovery it hid.
func TestTransitionCapture_FoldingRemembersTheLastLogin(t *testing.T) {
	seq := []ConnectionState{StatePendingAuth, StateConnecting, StateReady, StateError, StatePendingAuth, StateConnecting, StateError}
	f := Transition{From: seq[0], To: seq[1]}
	for i := 2; i < len(seq); i++ {
		f, _ = foldPair(f, Transition{From: seq[i-1], To: seq[i]})
	}
	assert.Equal(t, StatePendingAuth, f.From)
	assert.Equal(t, StateError, f.To)
	assert.Equal(t, 1, f.FoldedLost)
	assert.Equal(t, 1, f.FoldedRestored)
	assert.Equal(t, 1, f.FoldedLogin)
	assert.True(t, f.FoldedLoginLast, "the login requirement came after the recovery")

	// The other way round: a login requirement, then a recovery.
	seq = []ConnectionState{StateError, StatePendingAuth, StateConnecting, StateReady, StateError}
	f = Transition{From: seq[0], To: seq[1]}
	for i := 2; i < len(seq); i++ {
		f, _ = foldPair(f, Transition{From: seq[i-1], To: seq[i]})
	}
	assert.Equal(t, 1, f.FoldedLogin)
	assert.False(t, f.FoldedLoginLast, "a recovery followed it")
}
