// mcpcall.go — real MCP retrieve_tools invocation (Spec 083 US1, FR-001,
// research D1).
//
// The REST /api/v1/index/search endpoint used by live.go is NOT the payload an
// agent pays for: it misses usage_instructions, call_with, annotations, and
// session_risk. This file speaks the actual MCP protocol (streamable-http,
// mark3labs/mcp-go client) against the proxy's /mcp endpoint and returns the
// raw text content of each retrieve_tools call — the exact bytes an agent's
// context ingests — plus the client-measured latency (FR-023; the server-side
// timing fields are stubs, same rationale as LiveClient.Search).
package bench

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// mcpEndpointURL builds the streamable-http endpoint URL from the proxy base
// URL, appending /mcp when absent and authenticating via the ?apikey= query
// parameter — the same surface LAP uses (quickstart.md), accepted by the proxy
// whether or not require_mcp_auth is enabled. An empty key omits the param
// (/mcp is unauthenticated by default for client compatibility).
func mcpEndpointURL(baseURL, apiKey string) string {
	u := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(u, "/mcp") {
		u += "/mcp"
	}
	if apiKey != "" {
		u += "?apikey=" + url.QueryEscape(apiKey)
	}
	return u
}

// MCPCaller is one initialized MCP session against a running proxy. The live
// run reuses a single session for every golden query (one initialize, N
// retrieve_tools calls) so per-query latency measures the call, not session
// setup.
type MCPCaller struct {
	client *mcpclient.Client
}

// NewMCPCaller connects to the proxy's /mcp streamable-http endpoint and
// performs the MCP initialize handshake. Callers must Close() the session.
func NewMCPCaller(ctx context.Context, baseURL, apiKey string) (*MCPCaller, error) {
	endpoint := mcpEndpointURL(baseURL, apiKey)
	trans, err := transport.NewStreamableHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("build streamable-http transport for %q: %w", endpoint, err)
	}
	c := mcpclient.NewClient(trans)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("start MCP client for %q: %w", endpoint, err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-bench",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize MCP session with %q: %w", endpoint, err)
	}
	return &MCPCaller{client: c}, nil
}

// Close terminates the MCP session.
func (c *MCPCaller) Close() error {
	return c.client.Close()
}

// RetrieveTools calls the proxy's retrieve_tools tool with query and returns
// the raw text content of the result — the exact JSON string marshaled by
// internal/server/mcp.go handleRetrieveToolsWithMode — plus the client-side
// round-trip latency of the call itself (FR-023). limit <= 0 omits the limit
// argument so the proxy's configured tools_limit default applies, matching
// what a real agent pays per call.
func (c *MCPCaller) RetrieveTools(ctx context.Context, query string, limit int) (string, time.Duration, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = "retrieve_tools"
	args := map[string]interface{}{"query": query}
	if limit > 0 {
		args["limit"] = limit
	}
	req.Params.Arguments = args

	start := time.Now()
	result, err := c.client.CallTool(ctx, req)
	latency := time.Since(start)
	if err != nil {
		return "", latency, fmt.Errorf("call retrieve_tools(query=%q): %w", query, err)
	}

	text := textContent(result)
	if result.IsError {
		return "", latency, fmt.Errorf("retrieve_tools(query=%q) returned an error result: %s", query, text)
	}
	if text == "" {
		return "", latency, fmt.Errorf("retrieve_tools(query=%q) returned no text content", query)
	}
	return text, latency, nil
}

// textContent concatenates every text content block of a tool result. The
// proxy emits exactly one block; concatenation keeps the extraction total
// rather than silently dropping bytes if that ever changes.
func textContent(result *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range result.Content {
		if tc, ok := mcp.AsTextContent(content); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// InvokeRetrieveTools is the one-shot form: open a session, perform a single
// retrieve_tools call with the proxy's default limit, and close. The live
// harness prefers one MCPCaller across the golden set; this entry point serves
// smoke tests and single-query probes.
func InvokeRetrieveTools(ctx context.Context, baseURL, apiKey, query string) (string, time.Duration, error) {
	caller, err := NewMCPCaller(ctx, baseURL, apiKey)
	if err != nil {
		return "", 0, err
	}
	defer caller.Close()
	return caller.RetrieveTools(ctx, query, 0)
}
