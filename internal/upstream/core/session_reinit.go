package core

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

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
	if sh, ok := cl.GetTransport().(*transport.StreamableHTTP); ok {
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

	res, err := cl.Initialize(ctx, req)
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
