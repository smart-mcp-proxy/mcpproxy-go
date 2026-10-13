package dialgate

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T4.16 (review R5.1): an admitted dial parked after the TCP connect but
// before track; Retire runs its first sweep; the dial is then released.
func TestLateRegister_ClosedByTrack(t *testing.T) {
	addr, accepted := listenNeverRead(t)
	g := New()

	parked := make(chan struct{})
	resume := make(chan struct{})
	swept := make(chan struct{})
	g.SetHooks(&Hooks{
		BeforeTrack:             func() { close(parked); <-resume },
		AfterSweepBeforeBarrier: func() { close(swept) },
	})

	dialErr := make(chan error, 1)
	var dialed net.Conn
	go func() {
		c, err := g.DialContext(dialer())(context.Background(), "tcp", addr)
		dialed = c
		dialErr <- err
	}()
	<-parked
	peer := <-accepted

	retired := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		g.Retire()
		retired <- time.Since(start)
	}()
	<-swept
	require.Eventually(t, g.RetireWaiting, time.Second, time.Millisecond)
	close(resume)

	err := <-dialErr
	assert.ErrorIs(t, err, ErrRetired, "track closes a conn registered after the first sweep")
	assert.Nil(t, dialed)
	assert.Less(t, <-retired, time.Second)

	// No byte reached the peer, and the peer sees the close.
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	n, rerr := peer.Read(make([]byte, 16))
	assert.Zero(t, n)
	assert.Error(t, rerr)
}

// The variant that bypasses track's latch check proves the second sweep:
// the conn registered during the drain is closed after the barrier, and a
// Write on it returns without sending a byte.
func TestLateRegister_SecondSweepCloses(t *testing.T) {
	addr, accepted := listenNeverRead(t)
	g := New()

	parked := make(chan struct{})
	resume := make(chan struct{})
	swept := make(chan struct{})
	g.SetHooks(&Hooks{
		BeforeTrack:             func() { close(parked); <-resume },
		AfterSweepBeforeBarrier: func() { close(swept) },
		SkipTrackLatch:          true,
	})

	type res struct {
		c   net.Conn
		err error
	}
	dialRes := make(chan res, 1)
	go func() {
		c, err := g.DialContext(dialer())(context.Background(), "tcp", addr)
		dialRes <- res{c, err}
	}()
	<-parked
	peer := <-accepted

	retired := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		g.Retire()
		retired <- time.Since(start)
	}()
	<-swept
	close(resume)
	r := <-dialRes
	require.NoError(t, r.err, "the bypass registers the conn")
	assert.Less(t, <-retired, time.Second)

	n, err := r.c.Write(make([]byte, 1<<20))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, ErrRetired)

	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	got, rerr := peer.Read(make([]byte, 16))
	assert.Zero(t, got, "no byte reaches the peer after the latch")
	assert.Error(t, rerr, "the second sweep closed the conn")
}

// T4.10 (review R2.3): an admitted HTTP request about to enter its nested
// dial and write while Retire runs. The wrapper and the request path take
// no lock of their own, so there is no nesting: Retire returns within its
// bound and the dial gets ErrRetired.
func TestNoNesting_RequestDuringRetire(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	g := New()
	parked := make(chan struct{})
	resume := make(chan struct{})
	var once atomic.Bool
	g.SetHooks(&Hooks{BeforeAdmit: func() {
		if once.CompareAndSwap(false, true) {
			close(parked)
			<-resume
		}
	}})
	c := &http.Client{Transport: g.Transport(nil), Timeout: 3 * time.Second}

	errc := make(chan error, 1)
	go func() {
		resp, err := c.Get(srv.URL)
		if resp != nil {
			resp.Body.Close()
		}
		errc <- err
	}()
	<-parked

	start := time.Now()
	g.Retire()
	assert.Less(t, time.Since(start), time.Second)
	close(resume)

	err := <-errc
	assert.ErrorIs(t, err, ErrRetired)
	assert.Zero(t, hits.Load())
}
