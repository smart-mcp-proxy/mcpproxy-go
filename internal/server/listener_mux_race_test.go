package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTCPAndUnixMux builds a multiplexListener over a loopback TCP listener and
// a Unix socket, the same shape the core serves (TCP + tray socket).
func newTCPAndUnixMux(t *testing.T) (*multiplexListener, string, string) {
	t.Helper()

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Short path: macOS caps Unix socket paths at 104 bytes, and t.TempDir()
	// can exceed that.
	socketPath := filepath.Join("/tmp", fmt.Sprintf("mcpmuxrace-%d.sock", time.Now().UnixNano()))
	unixLn, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	mux := &multiplexListener{
		listeners: []*Listener{
			{Listener: tcpLn, Source: ConnectionSourceTCP, Address: tcpLn.Addr().String()},
			{Listener: unixLn, Source: ConnectionSourceTray, Address: socketPath},
		},
		logger: zap.NewNop(),
	}
	t.Cleanup(func() { _ = mux.Close() })
	return mux, tcpLn.Addr().String(), socketPath
}

// TestMultiplexListener_CloseWhileConnecting closes the mux while clients are
// hammering both listeners and an accept loop is draining it, as
// http.Server.Shutdown does. Close used to close connCh while a per-listener
// acceptLoop was blocked sending a freshly accepted connection on it, which
// panics with "send on closed channel" and takes down the core.
func TestMultiplexListener_CloseWhileConnecting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	mux, tcpAddr, socketPath := newTCPAndUnixMux(t)

	stop := make(chan struct{})
	var dialers sync.WaitGroup
	dial := func(network, addr string) {
		defer dialers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c, err := net.DialTimeout(network, addr, time.Second)
			if err == nil {
				_ = c.Close()
			}
		}
	}
	for i := 0; i < 4; i++ {
		dialers.Add(2)
		go dial("tcp", tcpAddr)
		go dial("unix", socketPath)
	}

	acceptDone := make(chan error, 1)
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			c, err := mux.Accept()
			if err != nil {
				acceptDone <- err
				return
			}
			_ = c.Close()
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}()

	// Close only once connections are flowing, so the accept loops are busy.
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("no connection accepted through the mux")
	}
	time.Sleep(time.Millisecond)
	require.NoError(t, mux.Close())

	select {
	case err := <-acceptDone:
		require.True(t, errors.Is(err, net.ErrClosed), "Accept after Close = %v, want net.ErrClosed", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close")
	}

	// Keep dialing a little after Close: an accept loop still holding a
	// connection must close it and exit, not send it anywhere.
	time.Sleep(5 * time.Millisecond)
	close(stop)
	dialers.Wait()
}

// TestMultiplexListener_CloseBeforeAccept: a mux closed before the first
// Accept must report net.ErrClosed instead of blocking forever.
func TestMultiplexListener_CloseBeforeAccept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	mux, _, _ := newTCPAndUnixMux(t)
	require.NoError(t, mux.Close())

	done := make(chan error, 1)
	go func() {
		_, err := mux.Accept()
		done <- err
	}()
	select {
	case err := <-done:
		require.True(t, errors.Is(err, net.ErrClosed), "Accept after Close = %v, want net.ErrClosed", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Accept blocked after Close")
	}
}
