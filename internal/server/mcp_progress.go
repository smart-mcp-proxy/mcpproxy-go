package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/progressrelay"
)

// withProgressRelay returns ctx carrying a sink for the upstream's
// notifications/progress of this call when the MCP client asked for progress
// (GH #1967). The upstream never sees the client's token: core mints one per
// call (progressrelay.Registry) and maps it back to this sink. A call without
// a token, or without a client session that can receive notifications, gets
// no sink and the upstream request carries no token.
//
// The message is upstream-written text an agent may read. It is capped by
// progressrelay and then passed through the output sanitisation policy the
// way skill words are, read live per message (redact and strip apply; under
// block, a message with a critical secret is left out and only the numbers
// are sent).
//
// Only the call_tool_* wrapper and the direct surface call this; a
// code_execution sub-call carries no progress token of its own.
func (p *MCPProxyServer) withProgressRelay(ctx context.Context, request mcp.CallToolRequest) context.Context {
	if request.Params.Meta == nil || request.Params.Meta.ProgressToken == nil {
		return ctx
	}
	sink := progressrelay.SessionSink(ctx, request.Params.Meta.ProgressToken, p.progressMessageCleaner())
	return progressrelay.WithSink(ctx, sink)
}

// progressMessageCleaner applies the output policy live when each message
// arrives (a policy switched on during a long call applies to its later
// messages); a message the policy withholds becomes "".
func (p *MCPProxyServer) progressMessageCleaner() func(string) string {
	return func(s string) string {
		pol := p.skillPolicy()
		if pol.mutates && pol.cfg.IsBlock() {
			if criticalAnywhere(pol.detector, s) {
				return ""
			}
			return s
		}
		return p.skillWordCleaner(pol)(s)
	}
}
