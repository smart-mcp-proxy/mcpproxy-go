package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
)

// sessionGate serializes a session re-initialize with the requests sent on the
// same mcp-go client. mcp-go's Initialize writes the client's negotiated
// protocol state (protocol version, server and client capabilities, client
// info) without synchronization, and every request reads that state: before it
// is sent (sendRequest -> applyRequestMeta/applyStandardHeaders) and, for
// tools/call, again on the response. So a request holds the gate shared for
// the whole mcp-go call and a re-initialize holds it exclusively for the
// Initialize handshake only.
//
// It is a weighted semaphore rather than a sync.RWMutex so both sides honour
// their context: a re-initialize waiting behind a long in-flight call gives up
// at the flight's deadline instead of hanging, and a request queued behind a
// re-initialize returns when its caller goes away, with nothing sent. Like
// sync.RWMutex it is writer-preferring (FIFO): once a re-initialize is
// waiting, new requests queue behind it rather than starving it.
//
// The zero value is ready; the semaphore is created on first use because core
// clients are also built as struct literals.
type sessionGate struct {
	once sync.Once
	sem  *semaphore.Weighted
}

const sessionGateWeight = math.MaxInt64

func (g *sessionGate) weighted() *semaphore.Weighted {
	g.once.Do(func() { g.sem = semaphore.NewWeighted(sessionGateWeight) })
	return g.sem
}

// acquireShared admits one request; release it when the mcp-go call returns.
func (g *sessionGate) acquireShared(ctx context.Context) (release func(), err error) {
	sem := g.weighted()
	if err := sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { sem.Release(1) }, nil
}

// acquireExclusive waits for every admitted request to finish and holds new
// ones back until release is called.
func (g *sessionGate) acquireExclusive(ctx context.Context) (release func(), err error) {
	sem := g.weighted()
	if err := sem.Acquire(ctx, sessionGateWeight); err != nil {
		return nil, err
	}
	return func() { sem.Release(sessionGateWeight) }, nil
}

// ErrSessionChanged is returned, with nothing sent, by a request whose context
// was pinned to a session generation (WithSessionGeneration) when a session
// re-initialize ran after the pin was taken. The caller re-validates what it
// checked against the old session and sends again.
var ErrSessionChanged = errors.New("upstream session was re-initialized before the request was sent")

type sessionGenerationKey struct{}

// WithSessionGeneration pins the requests made with ctx to session generation
// gen (from SessionGeneration): such a request is refused with
// ErrSessionChanged, before anything is sent, if a session re-initialize has
// run since. The managed layer pins tools/call this way so a call checked
// against one session's toolset is never sent on a session whose toolset has
// not been verified yet.
func WithSessionGeneration(ctx context.Context, gen uint64) context.Context {
	return context.WithValue(ctx, sessionGenerationKey{}, gen)
}

// SessionGeneration counts the session re-initializes run on this client. It
// changes while ReinitializeSession holds the session gate exclusively, so a
// request admitted to the gate sees a stable value for its whole send.
func (c *Client) SessionGeneration() uint64 { return c.sessionGen.Load() }

// beginRequest admits a request to the mcp-go client, waiting out a session
// re-initialize in progress. A context error is wrapped so it still matches
// context.Canceled / context.DeadlineExceeded.
func (c *Client) beginRequest(ctx context.Context, method string) (release func(), err error) {
	release, err = c.sessionGate.acquireShared(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s not sent: waiting for session re-initialize: %w", method, err)
	}
	if gen, ok := ctx.Value(sessionGenerationKey{}).(uint64); ok && gen != c.sessionGen.Load() {
		release()
		return nil, fmt.Errorf("%s not sent: %w", method, ErrSessionChanged)
	}
	return release, nil
}

// Spec 113-e (G8): helpers that let the managed layer re-initialize a
// Streamable HTTP MCP session in place after the upstream answered HTTP 404
// (mcp-go: transport.ErrSessionTerminated) for a session id it no longer
// knows, without tearing down the connection or touching connectionEpoch.

// SessionSnapshot reports the Streamable HTTP transport's current session id
// (empty when the transport has none, e.g. mcp-go cleared it after a 404, or
// when the transport is stdio/SSE) and whether the negotiated protocol is the
// stateless 2026-07-28 era, where sessions do not exist and no re-init applies.
func (c *Client) SessionSnapshot() (id string, modern bool) {
	c.mu.RLock()
	cl := c.client
	info := c.serverInfo
	c.mu.RUnlock()

	if info != nil {
		modern = mcp.IsModernProtocol(info.ProtocolVersion)
	}
	if cl == nil {
		return "", modern
	}
	if sh, ok := innerTransport(cl).(*transport.StreamableHTTP); ok {
		return sh.GetSessionId(), modern
	}
	return "", modern
}

// ReinitializeSession performs a fresh initialize + notifications/initialized
// handshake on the existing mcp-go client and transport, so the upstream
// issues a new session id. It does not Close/Start the transport and does not
// change the connection generation. The legacy-era pin (Spec 058 FR-027)
// applies exactly as on the first handshake.
func (c *Client) ReinitializeSession(ctx context.Context) error {
	c.mu.RLock()
	cl := c.client
	c.mu.RUnlock()
	if cl == nil || !c.IsConnected() {
		return fmt.Errorf("client not connected")
	}

	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_LEGACY_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "mcpproxy-go", Version: "1.0.0"}
	req.Params.Capabilities = mcp.ClientCapabilities{}

	// Initialize rewrites the shared client's protocol state, so it runs only
	// once every request already on this client has returned, and holds new
	// ones until it is done.
	release, err := c.sessionGate.acquireExclusive(ctx)
	if err != nil {
		return fmt.Errorf("session re-initialize: waiting for in-flight requests: %w", err)
	}
	// Initialize mutates the client's session even when it fails, so the
	// generation moves either way.
	c.sessionGen.Add(1)
	res, err := cl.Initialize(ctx, req)
	release()
	if err != nil {
		return fmt.Errorf("session re-initialize failed: %w", err)
	}
	if mcp.IsModernProtocol(res.ProtocolVersion) {
		return fmt.Errorf("upstream answered MCP protocol version %s on session re-initialize (spec 058 FR-027)", res.ProtocolVersion)
	}

	c.mu.Lock()
	c.serverInfo = res
	c.mu.Unlock()
	c.logger.Debug("MCP session re-initialized", zap.String("server", c.config.Name))
	return nil
}
