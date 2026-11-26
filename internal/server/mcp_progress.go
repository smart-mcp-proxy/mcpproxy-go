package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/progressrelay"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// withProgressRelay returns ctx carrying a sink for the upstream's
// notifications/progress of this call when the MCP client asked for progress
// (GH #1967). The upstream never sees the client's token: core mints one per
// call (progressrelay.Registry) and maps it back to this sink. A call without
// a token, or without a client session that can receive notifications, gets
// no sink and the upstream request carries no token.
//
// Only the call_tool_* wrapper and the direct surface call this; a
// code_execution sub-call carries no progress token of its own.
func (p *MCPProxyServer) withProgressRelay(ctx context.Context, request mcp.CallToolRequest, serverName, toolName, contentTrust string) context.Context {
	if request.Params.Meta == nil || request.Params.Meta.ProgressToken == nil {
		return ctx
	}
	sink := progressrelay.SessionSink(ctx, request.Params.Meta.ProgressToken, p.progressMessageCleaner(serverName, toolName, contentTrust))
	return progressrelay.WithSink(ctx, sink)
}

// progressMessageCleaner treats a progress message like the text of the same
// tool's result: the same configuration, detector and content trust, in the
// same order. Block: a message with a critical secret is left out (""), the
// numbers are still sent. Otherwise redact and strip as decided for the tool,
// then the cut to progressrelay.MaxMessageBytes, then the spotlight frame for
// untrusted output, so the frame is never cut. Nothing is written to the
// activity log for a progress message.
func (p *MCPProxyServer) progressMessageCleaner(serverName, toolName, contentTrust string) func(string) string {
	return func(s string) string {
		cfg := p.config.OutputSanitisation
		if cfg == nil {
			cfg = config.DefaultOutputSanitisationConfig()
		}
		critical := false
		if cfg.IsBlock() && p.sanitisationDetector != nil {
			critical = hasCriticalDetection(p.sanitisationDetector, s)
		}
		d := evaluateOutputSanitisation(cfg, contentTrust, critical)
		if d.block {
			return ""
		}
		if d.redact || d.strip {
			s, _ = p.sanitiseTextValue(s, d, cfg.EnabledStripClasses(), map[string]struct{}{}, map[string]struct{}{})
		}
		s = progressrelay.CapMessage(s)
		if s != "" && d.spotlight {
			s = security.SpotlightUntrusted(s, serverName, toolName)
		}
		return s
	}
}
