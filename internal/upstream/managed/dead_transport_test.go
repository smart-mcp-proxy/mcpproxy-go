package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// RC4-STDIO-001: when a stdio upstream's child process dies, mcp-go's stdio
// transport closes its done channel and every later request fails with
// transport.ErrTransportClosed ("transport closed"). The managed layer used to
// classify that as "not a connection error", so the health loop logged it as
// "timeout (high activity), ignoring" and tool calls failed forever while the
// server kept reporting Ready/healthy. These tests pin the classification and
// the two state-machine entry points (health ping, failed tool call).

// coreShapedTransportClosed reproduces the exact error chain the core client
// returns for a tools/call on a dead stdio transport: mcp-go wraps the
// sentinel in transport.Error, and internal/upstream/core/client.go wraps
// that with %w.
func coreShapedTransportClosed() error {
	return fmt.Errorf("CallTool failed for 'echo': %w", transport.NewError(transport.ErrTransportClosed))
}

// httpShapedEOF reproduces the chain mcp-go's streamable-HTTP transport hands
// back when net/http's POST fails mid-response: transport.Error wrapping
// "failed to send request: %w" wrapping *url.Error wrapping the io error.
func httpShapedEOF(ioErr error) error {
	urlErr := &url.Error{Op: "Post", URL: "https://upstream.example/mcp", Err: ioErr}
	return fmt.Errorf("CallTool failed for 'echo': %w",
		transport.NewError(fmt.Errorf("failed to send request: %w", urlErr)))
}

func TestIsDeadTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sentinel transport closed", transport.ErrTransportClosed, true},
		{"core-wrapped transport closed", coreShapedTransportClosed(), true},
		{"flattened transport closed text", errors.New("transport error: transport closed"), true},
		{"wrapped io.ErrClosedPipe", fmt.Errorf("write: %w", io.ErrClosedPipe), true},
		{"wrapped os.ErrClosed", fmt.Errorf("failed to write request: %w", os.ErrClosed), true},
		{"flattened file already closed", errors.New("transport error: failed to write request: write |1: file already closed"), true},
		// One truncated/reset HTTP response on a LIVE streamable-HTTP or SSE
		// upstream (LB idle timeout, keep-alive race): net/http wraps io.EOF /
		// io.ErrUnexpectedEOF. That is not a dead transport and must keep the
		// normal flap tolerance instead of evicting on the first miss.
		{"http POST unexpected EOF", httpShapedEOF(io.ErrUnexpectedEOF), false},
		{"http POST EOF", httpShapedEOF(io.EOF), false},
		{"wrapped io.EOF", fmt.Errorf("failed to read: %w", io.EOF), false},
		// A live server's JSON-RPC error (mcp-go returns it unwrapped, not as
		// transport.Error) can echo errno text; that is a tool failure.
		{"jsonrpc error echoing file already closed", errors.New("CallTool failed for 'rm': close /x: file already closed"), false},
		{"jsonrpc error echoing transport closed", errors.New("CallTool failed for 'ws': upstream websocket transport closed"), false},
		{"jsonrpc error echoing closed pipe", errors.New("CallTool failed for 'exec': io: read/write on closed pipe"), false},
		// Even the full transport phrase, when it is server-supplied text
		// inside a JSON-RPC error rather than mcp-go's own wrapper, is a
		// tool failure (opencode review: match only the typed wrapper or a
		// message that starts with its prefix).
		{"jsonrpc error echoing the transport wrapper text", errors.New("CallTool failed for 'proxy': upstream said transport error: transport closed"), false},
		// A JSON-RPC error answered by a LIVE server whose message merely
		// mentions EOF is a tool failure, not a dead transport.
		{"tool error mentioning EOF", errors.New("tool error: unexpected EOF while parsing input"), false},
		{"caller canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"generic", errors.New("invalid params"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isDeadTransportError(tc.err))
		})
	}
}

func TestDeadTransportIsHardConnectionError(t *testing.T) {
	mc := newTestClientForHealth(t)
	err := coreShapedTransportClosed()
	assert.True(t, mc.isConnectionError(err), "a closed transport is a connection error")
	assert.False(t, isTransientHealthCheckError(err), "a closed transport is hard evidence, not a transient miss")
}

// TestPerformHealthCheck_TransportClosedFlipsToError is the health-loop half
// of RC4-STDIO-001: one ping on a dead stdio transport must flip the server to
// Error so the next tick's ShouldRetry→tryReconnect respawns the process.
func TestPerformHealthCheck_TransportClosedFlipsToError(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.healthProbe = &fakeProber{pingErr: transport.NewError(transport.ErrTransportClosed)}

	mc.performHealthCheck()

	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"a ping that fails with 'transport closed' must flip the server to Error on the first miss")
	info := mc.StateManager.GetConnectionInfo()
	assert.Equal(t, 1, info.RetryCount,
		"one death charges one retry, so the existing backoff starts at its shortest step")
	assert.False(t, info.Terminal, "a dead transport is not a parked/permanent failure")
}

// TestCallTool_TransportClosedMarksServerError is the call-path half: the
// first tool call that hits the dead transport stops the server reporting
// Ready, instead of every call failing while status stays healthy.
func TestCallTool_TransportClosedMarksServerError(t *testing.T) {
	mc, fake := newTestClientForCallTool(t, coreShapedTransportClosed())

	_, err := mc.CallTool(context.Background(), "echo", nil)
	require.Error(t, err)
	assert.Equal(t, 1, fake.callCount())
	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"a tool call failing with 'transport closed' must flip the server to Error")
}

// TestCallTool_DeadTransportBurstCountsOneRetry: a burst of concurrent calls
// that all hit the same dead transport must record ONE failure, not one per
// call — otherwise each extra SetError bumps retryCount and stretches the
// reconnect backoff (or exhausts MaxConnectionRetries) for a single death.
func TestCallTool_DeadTransportBurstCountsOneRetry(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, coreShapedTransportClosed())

	_, err := mc.CallTool(context.Background(), "echo", nil)
	require.Error(t, err)
	require.Equal(t, types.StateError, mc.StateManager.GetState())
	first := mc.StateManager.GetConnectionInfo().RetryCount

	// A call that raced the first (already past its connectivity checks when
	// the state flipped) reports the same dead transport afterwards.
	mc.recordDeadConnection("echo", coreShapedTransportClosed(), mc.ConnectionEpoch())
	assert.Equal(t, first, mc.StateManager.GetConnectionInfo().RetryCount,
		"a second report of the same dead connection must not bump retryCount")
}

// TestCallTool_DeadTransportAfterDisconnectKeepsDisconnected: a call that was
// on the wire when the user disconnected the server sees "transport closed"
// too; that verdict belongs to the closed generation and must not flip the
// deliberately Disconnected client to Error.
func TestCallTool_DeadTransportAfterDisconnectKeepsDisconnected(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, nil)
	staleEpoch := mc.ConnectionEpoch()
	mc.connectionEpoch.Store(nextConnectionEpoch())
	mc.StateManager.Reset()

	mc.recordDeadConnection("echo", coreShapedTransportClosed(), staleEpoch)
	assert.Equal(t, types.StateDisconnected, mc.StateManager.GetState())
}

// TestPerformHealthCheck_HTTPEOFIsTolerated: an HTTP-shaped EOF from a live
// upstream stays below the eviction threshold on the first miss.
func TestPerformHealthCheck_HTTPEOFIsTolerated(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.healthProbe = &fakeProber{pingErr: httpShapedEOF(io.ErrUnexpectedEOF)}

	mc.performHealthCheck()

	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"one truncated HTTP response must not evict a live upstream")
}

// TestCallTool_JSONRPCErrorEchoingClosedFileKeepsReady: a healthy server's own
// tool error that mentions "file already closed" must not flip it to Error.
func TestCallTool_JSONRPCErrorEchoingClosedFileKeepsReady(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, errors.New("CallTool failed for 'echo': close /x: file already closed"))

	_, err := mc.CallTool(context.Background(), "echo", nil)
	require.Error(t, err)
	assert.Equal(t, types.StateReady, mc.StateManager.GetState())
}

// disconnectDuringPingProber simulates Disconnect running while the health
// ping is on the wire: the transport closes (the ping fails with "transport
// closed"), Disconnect bumps the epoch and Resets the state, and only then
// does the health goroutine reach its verdict.
type disconnectDuringPingProber struct {
	mc    *Client
	calls atomic.Int32
}

func (p *disconnectDuringPingProber) Ping(_ context.Context) error {
	p.calls.Add(1)
	p.mc.epochMu.Lock()
	p.mc.connectionEpoch.Store(nextConnectionEpoch())
	p.mc.epochMu.Unlock()
	p.mc.StateManager.Reset()
	return transport.NewError(transport.ErrTransportClosed)
}

// TestPerformHealthCheck_DeadTransportAfterDisconnectKeepsDisconnected is the
// health-loop twin of the call-path guard: a ping that failed because
// Disconnect closed the transport must not flip the Reset client to Error.
func TestPerformHealthCheck_DeadTransportAfterDisconnectKeepsDisconnected(t *testing.T) {
	mc := newTestClientForHealth(t)
	p := &disconnectDuringPingProber{mc: mc}
	mc.healthProbe = p

	mc.performHealthCheck()

	require.Equal(t, int32(1), p.calls.Load())
	assert.Equal(t, types.StateDisconnected, mc.StateManager.GetState(),
		"a ping failure from the closed generation must not re-mark the Disconnected client")
	assert.Equal(t, 0, mc.StateManager.GetConnectionInfo().RetryCount)
}

// TestSetErrorIfCurrentConnection_EpochArm pins the generation half of the
// guard on its own: the client is still Ready, but on a NEWER connection than
// the one that produced the failure (a reconnect landed while the request was
// in flight). The stale verdict must be dropped, not charged to the new
// session. The !IsConnected() half is covered by the after-Disconnect tests.
func TestSetErrorIfCurrentConnection_EpochArm(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, nil)
	require.Equal(t, types.StateReady, mc.StateManager.GetState())
	staleEpoch := mc.ConnectionEpoch()
	mc.connectionEpoch.Store(nextConnectionEpoch()) // reconnected, still Ready

	assert.False(t, mc.setErrorIfCurrentConnection(coreShapedTransportClosed(), staleEpoch))
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a failure from a replaced connection must not mark the current one")

	assert.True(t, mc.setErrorIfCurrentConnection(coreShapedTransportClosed(), mc.ConnectionEpoch()))
	assert.Equal(t, types.StateError, mc.StateManager.GetState())
}
