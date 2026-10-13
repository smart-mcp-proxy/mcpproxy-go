package dialgate

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Sol review round 3: a Write admitted before the latch and blocked on the
// socket fails when Retire's sweep closes the connection under it. That
// error must carry ErrRetired, as a Read's does, so the refresh coordinator
// does not charge it to the grant as a transient failure and set a cooldown
// that would also hold back the replacement instance.
func TestGatedConn_WriteCutByRetire_CarriesErrRetired(t *testing.T) {
	g := New()
	var peer net.Conn
	dial := g.DialContext(func(context.Context, string, string) (net.Conn, error) {
		c, p := net.Pipe()
		peer = p
		return c, nil
	})
	conn, err := dial(context.Background(), "tcp", "pipe")
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })

	writeErr := make(chan error, 1)
	go func() {
		// net.Pipe writes block until the peer reads; the peer never does.
		_, err := conn.Write([]byte("POST /token HTTP/1.1\r\n\r\nrefresh_token=rt"))
		writeErr <- err
	}()
	require.Eventually(t, func() bool {
		// The write holds the read lock: a write-lock attempt fails.
		if g.mu.TryLock() {
			g.mu.Unlock()
			return false
		}
		return true
	}, 5*time.Second, time.Millisecond, "the write is in progress")

	g.Retire()
	select {
	case err := <-writeErr:
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrRetired), "got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked write was not cut by Retire")
	}
}
