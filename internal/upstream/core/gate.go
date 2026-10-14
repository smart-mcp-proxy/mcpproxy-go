package core

import (
	"context"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Spec 116 PR4 (#1564): the core client's side of the retirement barrier.
// See internal/upstream/dialgate for the barrier itself.

// Gate returns the instance's retirement barrier.
func (c *Client) Gate() *dialgate.Gate {
	if g := c.gate.Load(); g != nil {
		return g
	}
	// Hand-constructed test clients: an open gate, installed once.
	g := dialgate.New()
	if c.gate.CompareAndSwap(nil, g) {
		return g
	}
	return c.gate.Load()
}

// SetGate makes this client share g. The upstream manager uses it for the
// transient clients of a manual OAuth flow, so retiring the registered
// instance also stops the login it started (FR-024). Call it before the
// client is used.
func (c *Client) SetGate(g *dialgate.Gate) {
	if g != nil {
		c.gate.Store(g)
	}
}

// Retire closes the instance's gate (FR-022): from its return, the client
// launches nothing, dials nothing and writes nothing. The upstream manager
// calls it, through the managed client, when it unregisters the instance.
// Teardown (Disconnect: the MCP close, the process-group kill, `docker stop`)
// is not gated and still runs afterwards.
func (c *Client) Retire() { c.Gate().Retire() }

// Retired reports whether the instance's gate is retired.
func (c *Client) Retired() bool { return c.Gate().Retired() }

// innerTransport is the ONE place core asserts capabilities on an mcp-go
// client's transport (FR-023b, review R8.1): the transport core installs is
// a dialgate wrapper, and the OAuth handler lookup and the Streamable HTTP
// session snapshot need the concrete transport underneath it.
func innerTransport(cl *client.Client) transport.Interface {
	if cl == nil {
		return nil
	}
	return dialgate.Unwrap(cl.GetTransport())
}

// oauthEnv is the OAuth environment of this instance (FR-024a): every OAuth
// preflight, resource auto-detection, metadata discovery and cache fetch, and
// the OAuth HTTP client mcp-go uses for registration, code exchange and
// refresh, run on the instance's gated transport.
func (c *Client) oauthEnv(ctx context.Context) oauth.Env {
	return oauth.Env{Ctx: ctx, Transport: c.Gate().HTTPTransport(), Binding: c.CredentialBinding()}
}

// SetCredentialBinding binds the instance's OAuth state to b, the committed
// credential binding of the view it was built for (Spec 116 PR2b, #1607).
// The upstream manager sets it before the instance becomes reachable, and on
// the transient clients of a manual OAuth flow (the registered instance's
// binding), so every token, client registration and completion record the
// instance writes, reads or clears is bound to it. Call it before the client
// is used.
func (c *Client) SetCredentialBinding(b commitview.CredentialBinding) {
	c.binding.Store(&b)
}

// CredentialBinding returns the instance's credential binding (zero: unbound,
// the CLI and hand-built test clients).
func (c *Client) CredentialBinding() commitview.CredentialBinding {
	if b := c.binding.Load(); b != nil {
		return *b
	}
	return commitview.CredentialBinding{}
}
