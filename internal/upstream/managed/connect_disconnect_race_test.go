package managed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// TestConnect_DisconnectDuringDialDoesNotReportReady is the regression guard
// for the stuck re-enable seen after Spec 116 PR1 (#1590): a Disconnect that
// lands while Connect is dialing (mc.mu released) closes the transport and
// resets the state to Disconnected. Connect then used to finish with
// TransitionTo(Ready) regardless, so the client reported Ready over a closed
// transport: every ListTools failed with "client not connected", and because
// the client looked connected nothing ever reconnected it.
//
// Connect must instead report that it was aborted and leave the client
// Disconnected, so the next connect attempt works.
func TestConnect_DisconnectDuringDialDoesNotReportReady(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := mcpserver.NewMCPServer("race-upstream", "0.0.1", mcpserver.WithToolCapabilities(true))
	ts := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(ts.Close)

	cfg := &config.ServerConfig{Name: "race-server", Protocol: "streamable-http", URL: ts.URL, Enabled: true}
	mc, err := NewClient("race-server", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Disconnect() })

	// The seam runs after the transport connected and before Connect takes
	// mc.mu back: exactly where a concurrent Disconnect (the supervisor's
	// reconnect leg, a commit worker's RemoveServer) can land.
	fired := false
	hook := func() {
		if fired {
			return
		}
		fired = true
		require.NoError(t, mc.Disconnect())
	}
	mc.connectAfterDial.Store(&hook)
	t.Cleanup(func() { mc.connectAfterDial.Store(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = mc.Connect(ctx)
	require.True(t, fired, "seam did not run")
	require.ErrorIs(t, err, ErrConnectAborted)
	require.False(t, mc.IsConnected(), "a client whose transport was closed mid-connect must not report Ready")
	require.Equal(t, types.StateDisconnected, mc.GetState())

	// The aborted attempt leaves a clean client: the next connect succeeds and
	// the transport really works.
	require.NoError(t, mc.Connect(ctx))
	require.True(t, mc.IsConnected())
	_, err = mc.ListTools(ctx)
	require.NoError(t, err)
}

// TestConnect_DisconnectDuringFailingDialKeepsDisconnected covers a Disconnect
// that cancels (or simply overlaps) a dial that then fails: the failure must
// not overwrite the Disconnected state the Disconnect left with Error, which
// would put an intentionally disconnected client on the retry ladder.
func TestConnect_DisconnectDuringFailingDialKeepsDisconnected(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	// A port nothing listens on: the dial fails fast.
	ts := servertest.NewTestStreamableHTTPServer(mcpserver.NewMCPServer("gone", "0.0.1"))
	deadURL := ts.URL
	ts.Close()

	cfg := &config.ServerConfig{Name: "race-fail", Protocol: "streamable-http", URL: deadURL, Enabled: true}
	mc, err := NewClient("race-fail", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Disconnect() })

	fired := false
	hook := func() {
		if fired {
			return
		}
		fired = true
		require.NoError(t, mc.Disconnect())
	}
	mc.connectAfterDial.Store(&hook)
	t.Cleanup(func() { mc.connectAfterDial.Store(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = mc.Connect(ctx)
	require.True(t, fired, "seam did not run")
	require.ErrorIs(t, err, ErrConnectAborted)
	require.Equal(t, types.StateDisconnected, mc.GetState(),
		"a superseded failing dial must not overwrite the Disconnected state")
}

// TestConnect_NewerAttemptSupersedesTheOlderOne covers a Disconnect followed by
// a newer Connect that both complete while the first attempt is still dialing.
// The first attempt must discard its result and leave the newer attempt's
// connection alone: no second Ready publication, no closing of the shared
// transport the newer attempt is using.
func TestConnect_NewerAttemptSupersedesTheOlderOne(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := mcpserver.NewMCPServer("race-upstream", "0.0.1", mcpserver.WithToolCapabilities(true))
	ts := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(ts.Close)

	cfg := &config.ServerConfig{Name: "race-newer", Protocol: "streamable-http", URL: ts.URL, Enabled: true}
	mc, err := NewClient("race-newer", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Disconnect() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fired := false
	var newerErr error
	var epochAfterNewer int64
	hook := func() {
		if fired {
			return
		}
		fired = true
		require.NoError(t, mc.Disconnect())
		newerErr = mc.Connect(ctx)
		epochAfterNewer = mc.ConnectionEpoch()
	}
	mc.connectAfterDial.Store(&hook)
	t.Cleanup(func() { mc.connectAfterDial.Store(nil) })

	err = mc.Connect(ctx)
	require.True(t, fired, "seam did not run")
	require.NoError(t, newerErr, "the newer attempt must connect")
	require.ErrorIs(t, err, ErrConnectAborted, "the older attempt must report that it was superseded")
	require.True(t, mc.IsConnected(), "the newer attempt's connection must stay Ready")
	require.Equal(t, epochAfterNewer, mc.ConnectionEpoch(),
		"the superseded attempt must not open another connection generation over the newer one")
	_, err = mc.ListTools(ctx)
	require.NoError(t, err, "the superseded attempt must not close the transport the newer attempt uses")
}

// TestConnect_ReconnectTeardownSupersedesADialingAttempt covers the reconnect
// helpers (tryReconnect, TryReconnectSync), which tear the connection down
// without mc.mu and then start their own Connect. An older attempt whose dial
// finishes in between must not publish Ready over the transport the helper
// just closed; otherwise the helper's Connect hits ErrConnectAlreadyActive and
// the client is stuck Ready over a closed transport.
func TestConnect_ReconnectTeardownSupersedesADialingAttempt(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := mcpserver.NewMCPServer("race-upstream", "0.0.1", mcpserver.WithToolCapabilities(true))
	ts := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(ts.Close)

	cfg := &config.ServerConfig{Name: "race-reconnect", Protocol: "streamable-http", URL: ts.URL, Enabled: true}
	mc, err := NewClient("race-reconnect", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Disconnect() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Park the first attempt after its dial succeeded.
	dialed, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	afterDial := func() {
		once.Do(func() {
			close(dialed)
			<-release
		})
	}
	mc.connectAfterDial.Store(&afterDial)
	t.Cleanup(func() { mc.connectAfterDial.Store(nil) })

	oldDone := make(chan error, 1)
	go func() { oldDone <- mc.Connect(ctx) }()
	select {
	case <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("first attempt never finished dialing")
	}

	// A concurrent failure moves the client to Error, which is what lets the
	// reconnect helpers run while the first attempt is still out.
	mc.StateManager.SetError(errors.New("simulated health-check failure"))

	// Let the first attempt finish after the helper's teardown and before its
	// replacement Connect starts.
	var oldErr error
	afterTeardown := func() {
		close(release)
		oldErr = <-oldDone
	}
	mc.reconnectAfterTeardown.Store(&afterTeardown)
	t.Cleanup(func() { mc.reconnectAfterTeardown.Store(nil) })

	require.NoError(t, mc.TryReconnectSync(ctx), "the helper's replacement connect must succeed")
	require.ErrorIs(t, oldErr, ErrConnectAborted, "the older attempt must be superseded by the reconnect teardown")
	require.True(t, mc.IsConnected())
	_, err = mc.ListTools(ctx)
	require.NoError(t, err, "the client must be usable, not Ready over a closed transport")
}
