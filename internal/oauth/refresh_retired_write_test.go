package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// writeSignalConn signals the first Write on the connection.
type writeSignalConn struct {
	net.Conn
	onWrite func()
}

func (c *writeSignalConn) Write(b []byte) (int, error) {
	c.onWrite()
	return c.Conn.Write(b)
}

// Sol review round 3: a refresh POST admitted before retirement and blocked
// in its write is cut off when the instance is retired. The refresh fails
// with ErrRetired, the coordinator records no cooldown for the grant, and
// the next (healthy) refresh runs at once instead of waiting out a cooldown
// the retired instance set.
func TestRefreshCoordinator_RetiredMidWrite_NoCooldown(t *testing.T) {
	f := newCoordFixture(t, "dcr-client")
	c := NewRefreshCoordinator()
	gate := dialgate.New()

	writing := make(chan struct{})
	var once sync.Once
	var peers []net.Conn
	var peersMu sync.Mutex
	t.Cleanup(func() {
		peersMu.Lock()
		defer peersMu.Unlock()
		for _, p := range peers {
			_ = p.Close()
		}
	})
	base := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		cli, srv := net.Pipe() // the peer never reads: the request write blocks
		peersMu.Lock()
		peers = append(peers, srv)
		peersMu.Unlock()
		return &writeSignalConn{Conn: cli, onWrite: func() { once.Do(func() { close(writing) }) }}, nil
	}}
	hc := &http.Client{Transport: gate.Transport(base)}

	retiredReq := f.req(RefreshTriggerReactive, "rt-0")
	retiredReq.Refresh = func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://as.example/token",
			strings.NewReader("grant_type=refresh_token&refresh_token="+rec.RefreshToken))
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("token request: %w", err)
		}
		return nil, errors.New("unexpected token response")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := c.Do(ctx, retiredReq)
		done <- err
	}()
	select {
	case <-writing:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh POST never started writing")
	}
	gate.Retire()

	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the cut-off refresh did not return")
	}
	require.Error(t, err)
	require.True(t, errors.Is(err, dialgate.ErrRetired), "got %v", err)

	c.mu.Lock()
	st := c.keys[flightKey{serverKey: f.key}]
	if st != nil {
		assert.True(t, st.cooldownUntil.IsZero(), "a retirement-cut refresh records no cooldown")
		assert.Zero(t, st.transientFailures)
		assert.Nil(t, st.latchErr)
	}
	c.mu.Unlock()

	tok, _, err := c.Do(ctx, f.req(RefreshTriggerReactive, "rt-0"))
	require.NoError(t, err, "the healthy refresh is not held back by a cooldown")
	require.NotNil(t, tok)
	assert.Equal(t, int32(1), f.calls.Load(), "the healthy refresh ran its own token request")
}
