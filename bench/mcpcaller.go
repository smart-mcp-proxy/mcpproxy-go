package bench

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPRetrieveCaller is the live compact arm's transport (Spec 085 FR-017): it
// drives a running proxy's MCP endpoint and replays retrieve_tools calls with
// a per-call `detail` override, so full and compact responses are measured
// through the SAME pipeline against the same live index. The serialized
// response text it returns is exactly what an agent would receive — that is
// what gets tokenized.
type MCPRetrieveCaller struct {
	client *client.Client
}

// NewMCPRetrieveCaller connects and initializes an MCP client against mcpURL
// (e.g. "http://127.0.0.1:8092/mcp"). Callers must Close it.
func NewMCPRetrieveCaller(ctx context.Context, mcpURL string) (*MCPRetrieveCaller, error) {
	httpTransport, err := transport.NewStreamableHTTP(mcpURL)
	if err != nil {
		return nil, fmt.Errorf("mcp transport %q: %w", mcpURL, err)
	}
	c := client.NewClient(httpTransport)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("mcp start %q: %w", mcpURL, err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "mcpproxy-bench", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initRequest); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp initialize %q: %w", mcpURL, err)
	}
	return &MCPRetrieveCaller{client: c}, nil
}

// Close shuts the underlying MCP client down.
func (m *MCPRetrieveCaller) Close() error {
	return m.client.Close()
}

// RetrieveFunc adapts the caller to the RetrieveToolsFunc signature RunFlipGates
// consumes.
func (m *MCPRetrieveCaller) RetrieveFunc(ctx context.Context) RetrieveToolsFunc {
	return func(query string, limit int, detail string) ([]string, string, error) {
		return m.retrieveTools(ctx, query, limit, detail)
	}
}

// retrieveTools performs one retrieve_tools call and returns the ranked ids
// plus the raw response text.
func (m *MCPRetrieveCaller) retrieveTools(ctx context.Context, query string, limit int, detail string) ([]string, string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = "retrieve_tools"
	req.Params.Arguments = map[string]interface{}{
		"query":  query,
		"limit":  limit,
		"detail": detail,
	}
	result, err := m.client.CallTool(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("retrieve_tools %q (%s): %w", query, detail, err)
	}
	if result.IsError {
		return nil, "", fmt.Errorf("retrieve_tools %q (%s): tool error: %v", query, detail, result.Content)
	}
	if len(result.Content) == 0 {
		return nil, "", fmt.Errorf("retrieve_tools %q (%s): empty content", query, detail)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return nil, "", fmt.Errorf("retrieve_tools %q (%s): non-text content %T", query, detail, result.Content[0])
	}
	ids, err := parseRetrieveResponse(text.Text)
	if err != nil {
		return nil, "", fmt.Errorf("retrieve_tools %q (%s): %w", query, detail, err)
	}
	return ids, text.Text, nil
}

// parseRetrieveResponse extracts the ordered entry ids from a serialized
// retrieve_tools response body. Full-mode entries carry "name", compact-mode
// entries carry "id" (Spec 085 FR-002) — both are the "<server>:<tool>" id in
// ranked order.
func parseRetrieveResponse(body string) ([]string, error) {
	var resp struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("decode retrieve_tools response: %w", err)
	}
	ids := make([]string, 0, len(resp.Tools))
	for i, entry := range resp.Tools {
		raw, ok := entry["id"]
		if !ok {
			raw, ok = entry["name"]
		}
		if !ok {
			return nil, fmt.Errorf("entry %d carries neither id nor name", i)
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, fmt.Errorf("entry %d id: %w", i, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
