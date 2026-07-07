package main

import (
	"context"
	"fmt"
	"strings"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// mcpSession is an MCP client session against the core's /mcp endpoint
// (streamable HTTP — the default routing mode endpoint), used for the
// FR-007(b)/(c) retrieve_tools + call_tool_read assertions.
type mcpSession struct {
	client    *mcpclient.Client
	requestID string
}

// newMCPSession connects and initializes an MCP session. requestID, when
// non-empty, is sent as X-Request-Id on every HTTP request the session makes
// (used to empirically probe whether /mcp honors caller request ids).
func newMCPSession(ctx context.Context, baseURL, requestID string) (*mcpSession, error) {
	var opts []transport.StreamableHTTPCOption
	if requestID != "" {
		opts = append(opts, transport.WithHTTPHeaderFunc(func(context.Context) map[string]string {
			return map[string]string{"X-Request-Id": requestID}
		}))
	}
	cl, err := mcpclient.NewStreamableHttpClient(strings.TrimRight(baseURL, "/")+"/mcp", opts...)
	if err != nil {
		return nil, fmt.Errorf("create MCP client: %w", err)
	}
	if err := cl.Start(ctx); err != nil {
		return nil, fmt.Errorf("start MCP client: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "release-gate", Version: "0.1.0"}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		cl.Close()
		return nil, fmt.Errorf("initialize MCP session: %w", err)
	}
	return &mcpSession{client: cl, requestID: requestID}, nil
}

func (s *mcpSession) close() {
	if s != nil && s.client != nil {
		_ = s.client.Close()
	}
}

// callTool invokes a built-in proxy tool and returns concatenated text
// content. A tool-level error (IsError) is returned as a Go error.
func (s *mcpSession) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := s.client.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("MCP tools/call %s: %w", name, err)
	}
	text := textContent(res)
	if res.IsError {
		return text, fmt.Errorf("MCP tool %s returned error: %s", name, truncateStr(text, 400))
	}
	return text, nil
}

// retrieveTools runs the retrieve_tools discovery search.
func (s *mcpSession) retrieveTools(ctx context.Context, query string) (string, error) {
	return s.callTool(ctx, "retrieve_tools", map[string]any{"query": query})
}

// callUpstreamRead calls an upstream tool through call_tool_read (Spec 018).
func (s *mcpSession) callUpstreamRead(ctx context.Context, qualifiedTool string, args map[string]any) (string, error) {
	return s.callTool(ctx, "call_tool_read", map[string]any{
		"name": qualifiedTool,
		"args": args,
	})
}

func textContent(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
