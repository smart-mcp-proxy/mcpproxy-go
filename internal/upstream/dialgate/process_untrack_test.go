package dialgate

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Review round 2 (Spec 116 PR4): Retire kills from a snapshot of the tracked
// processes. A kill taken in that snapshot must not run once the owner has
// untracked the process (it is about to reap the child, after which the OS
// may hand the process-group ID to someone else).
func TestProcess_KillAfterUntrackIsNoop(t *testing.T) {
	g := New()
	var kills atomic.Int32
	p, err := g.TrackProcess(func() { kills.Add(1) })
	require.NoError(t, err)

	p.Untrack()
	p.Kill() // what a Retire holding a stale snapshot would do
	require.Zero(t, kills.Load(), "a kill after Untrack must not signal the released process group")
}

// Untrack waits for a kill that is already running, so the owner never reaps
// the child while the gate is still signalling its group.
func TestProcess_UntrackWaitsForRunningKill(t *testing.T) {
	g := New()
	inKill := make(chan struct{})
	releaseKill := make(chan struct{})
	p, err := g.TrackProcess(func() {
		select {
		case <-inKill:
		default:
			close(inKill)
		}
		<-releaseKill
	})
	require.NoError(t, err)

	retired := make(chan struct{})
	go func() { g.Retire(); close(retired) }()
	<-inKill

	untracked := make(chan struct{})
	go func() { p.Untrack(); close(untracked) }()
	select {
	case <-untracked:
		t.Fatal("Untrack returned while Retire's kill of the process was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseKill)
	<-untracked
	<-retired
}
