package dialgate

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T080: Admit/Retire ordering, Write after Retire, a blocked Write unblocked
// by Retire, Retire bounded and idempotent.

func TestAdmit_RefusedAfterRetire(t *testing.T) {
	g := New()
	release, err := g.Admit(context.Background())
	require.NoError(t, err)
	release()
	release() // extra release is a no-op

	g.Retire()
	g.Retire() // idempotent
	assert.True(t, g.Retired())
	_, err = g.Admit(context.Background())
	assert.ErrorIs(t, err, ErrRetired)
	assert.Error(t, g.Context().Err(), "Retire cancels the gate context")
}

func TestAdmit_HonoursContext(t *testing.T) {
	g := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.Admit(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

// An admitted section that began before the latch finishes before Retire
// returns; a section that begins after the latch is refused.
func TestRetire_WaitsForAdmittedSection(t *testing.T) {
	g := New()
	release, err := g.Admit(context.Background())
	require.NoError(t, err)

	retired := make(chan struct{})
	go func() { g.Retire(); close(retired) }()

	require.Eventually(t, g.RetireWaiting, 2*time.Second, time.Millisecond, "Retire waits on the barrier")
	select {
	case <-retired:
		t.Fatal("Retire returned while an admitted section was open")
	default:
	}
	assert.True(t, g.Retired(), "the latch is set before the barrier")

	// A new section is refused while Retire waits (no starvation of the writer).
	_, err = g.Admit(context.Background())
	assert.ErrorIs(t, err, ErrRetired)

	release()
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("Retire did not return after the section was released")
	}
}

func TestTrackProcess_KilledByRetireAndAfterRetire(t *testing.T) {
	g := New()
	var killed atomic.Int32
	p, err := g.TrackProcess(func() { killed.Add(1) })
	require.NoError(t, err)
	require.NotNil(t, p)

	g.Retire()
	assert.GreaterOrEqual(t, killed.Load(), int32(1), "a tracked process is killed by Retire")

	var late atomic.Int32
	_, err = g.TrackProcess(func() { late.Add(1) })
	assert.ErrorIs(t, err, ErrRetired)
	assert.Equal(t, int32(1), late.Load(), "a process registered after the latch is killed at once")
}

func TestUntrackedProcess_NotKilled(t *testing.T) {
	g := New()
	var killed atomic.Int32
	p, err := g.TrackProcess(func() { killed.Add(1) })
	require.NoError(t, err)
	p.Untrack()
	g.Retire()
	assert.Zero(t, killed.Load(), "an untracked process belongs to its own teardown")
}

// pipeListener serves conns that never read, so a large Write blocks.
func listenNeverRead(t *testing.T) (addr string, accepted <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ch := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			ch <- c
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		for {
			select {
			case c := <-ch:
				c.Close()
			default:
				return
			}
		}
	})
	return ln.Addr().String(), ch
}

func dialer() DialFunc { return (&net.Dialer{Timeout: 2 * time.Second}).DialContext }

func TestWriteAfterRetire_ErrRetired(t *testing.T) {
	addr, _ := listenNeverRead(t)
	g := New()
	conn, err := g.DialContext(dialer())(context.Background(), "tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("hello"))
	require.NoError(t, err)

	g.Retire()
	n, err := conn.Write([]byte("after"))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, ErrRetired)

	_, err = g.DialContext(dialer())(context.Background(), "tcp", addr)
	assert.ErrorIs(t, err, ErrRetired, "a dial after the latch is refused")
}

func TestBlockedWrite_UnblockedByRetire_Bounded(t *testing.T) {
	addr, _ := listenNeverRead(t)
	g := New()
	conn, err := g.DialContext(dialer())(context.Background(), "tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	// Keep writing until the peer's and our socket buffers are full and a
	// write blocks inside the socket. A single large write is not enough
	// everywhere: Windows loopback can absorb a 64 MiB write without
	// blocking. The writer counts completed writes; once the count stops
	// moving, the current write is stuck in c.Conn.Write.
	chunk := make([]byte, 1<<20)
	var completed atomic.Int64
	writeDone := make(chan error, 1)
	go func() {
		for {
			if _, err := conn.Write(chunk); err != nil {
				writeDone <- err
				return
			}
			completed.Add(1)
		}
	}()
	require.Eventually(t, func() bool {
		before := completed.Load()
		time.Sleep(150 * time.Millisecond)
		return completed.Load() == before
	}, 10*time.Second, 10*time.Millisecond, "a write never blocked")
	select {
	case err := <-writeDone:
		t.Fatalf("writer was expected to be blocked, returned %v", err)
	default:
	}

	start := time.Now()
	g.Retire()
	assert.Less(t, time.Since(start), time.Second, "Retire is bounded with a hung peer")
	select {
	case err := <-writeDone:
		require.Error(t, err, "the blocked write returns once its conn is closed")
		// The latch path returns the bare sentinel without touching the
		// socket; a write unblocked by Retire closing its connection returns
		// the socket error wrapped with ErrRetired. Requiring the wrapped
		// form proves Retire closed the tracked connection.
		assert.ErrorIs(t, err, ErrRetired)
		assert.NotSame(t, ErrRetired, err, "error must come from the closed socket, not the latch check")
		assert.NotEqual(t, ErrRetired.Error(), err.Error(), "error must wrap the socket error")
	case <-time.After(time.Second):
		t.Fatal("blocked write not unblocked by Retire")
	}
}

func TestRetire_CancelsInProgressDial(t *testing.T) {
	g := New()
	dialStarted := make(chan struct{})
	slowDial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	errc := make(chan error, 1)
	go func() {
		_, err := g.DialContext(slowDial)(context.Background(), "tcp", "127.0.0.1:1")
		errc <- err
	}()
	<-dialStarted
	start := time.Now()
	g.Retire()
	assert.Less(t, time.Since(start), time.Second)
	err := <-errc
	assert.ErrorIs(t, err, ErrRetired)
}

// Gated http.Transport: requests after Retire never reach the server.
func TestTransport_NoRequestAfterRetire(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	g := New()
	c := &http.Client{Transport: g.HTTPTransport(), Timeout: 2 * time.Second}
	resp, err := c.Get(srv.URL)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, int32(1), hits.Load())

	g.Retire()
	before := hits.Load()
	_, err = c.Get(srv.URL) // the idle keep-alive conn and a fresh dial are both refused
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRetired), "got %v", err)
	assert.Equal(t, before, hits.Load())
}

func TestRetire_ConcurrentStress(t *testing.T) {
	for i := 0; i < 50; i++ {
		g := New()
		var wg sync.WaitGroup
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := 0; k < 20; k++ {
					release, err := g.Admit(context.Background())
					if err != nil {
						if !errors.Is(err, ErrRetired) {
							t.Errorf("unexpected %v", err)
						}
						return
					}
					release()
				}
			}()
		}
		g.Retire()
		wg.Wait()
		_, err := g.Admit(context.Background())
		require.ErrorIs(t, err, ErrRetired)
	}
}
