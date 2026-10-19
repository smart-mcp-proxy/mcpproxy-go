package dialgate

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 PR4, T4.3 (US4 scenario 3): a reused connection whose request
// fails makes net/http retry the (idempotent) request on a new connection.
// The retry's dial is parked; the instance is retired; released, the retry
// never reaches the server, and Retire does not hang.
func TestRetire_NetHTTPRetryRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	var requests atomic.Int64
	go func() {
		first := true
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn, first bool) {
				defer conn.Close()
				br := bufio.NewReader(conn)
				for n := 0; ; n++ {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					_, _ = io.Copy(io.Discard, req.Body)
					requests.Add(1)
					if first && n == 1 {
						// The reused connection fails before any response
						// byte: net/http retries the GET on a new connection.
						return
					}
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
				}
			}(conn, first)
			first = false
		}
	}()

	g := New()
	var dials atomic.Int64
	parked := make(chan struct{})
	resume := make(chan struct{})
	g.SetHooks(&Hooks{BeforeAdmit: func() {
		if dials.Add(1) == 2 { // the retry's dial
			close(parked)
			<-resume
		}
	}})
	c := &http.Client{Transport: g.Transport(nil), Timeout: 5 * time.Second}
	url := "http://" + ln.Addr().String() + "/"

	resp, err := c.Get(url)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, int64(1), requests.Load())

	errc := make(chan error, 1)
	go func() {
		resp, err := c.Get(url) // reuses the conn; the server drops it; net/http retries
		if resp != nil {
			resp.Body.Close()
		}
		errc <- err
	}()
	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("net/http did not retry on a new connection")
	}
	before := requests.Load()

	start := time.Now()
	g.Retire()
	assert.Less(t, time.Since(start), time.Second, "Retire does not hang")
	close(resume)

	err = <-errc
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRetired)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, before, requests.Load(), "the retry never reaches the server")
}

// Control for the fixture: without retirement the retry succeeds.
func TestRetire_NetHTTPRetryControl(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	var conns atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			first := conns.Add(1) == 1
			go func(conn net.Conn) {
				defer conn.Close()
				br := bufio.NewReader(conn)
				for n := 0; ; n++ {
					if _, err := http.ReadRequest(br); err != nil {
						return
					}
					if first && n == 1 {
						return
					}
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
				}
			}(conn)
		}
	}()
	c := &http.Client{Transport: New().Transport(nil), Timeout: 5 * time.Second}
	url := "http://" + ln.Addr().String() + "/"
	for i := 0; i < 2; i++ {
		resp, err := c.Get(url)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	assert.Equal(t, int64(2), conns.Load(), "the second GET was retried on a new connection")
}
