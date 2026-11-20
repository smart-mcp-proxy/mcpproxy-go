package server

// #1964-#1966: an upstream's elicitation, sampling and roots requests made
// during a tool call reach the MCP client that made the call, through a real
// proxy, on every call surface.

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newRelayUpstream is a streamable-HTTP MCP server whose tools ask their
// client something mid-call.
func newRelayUpstream(t *testing.T) string {
	t.Helper()
	srv := mcpserver.NewMCPServer("relay-upstream", "1.0.0", mcpserver.WithToolCapabilities(true),
		mcpserver.WithElicitation(), mcpserver.WithRoots())
	noArgs := mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}}
	text := func(s string, err error) (*mcp.CallToolResult, error) {
		if err != nil {
			return mcp.NewToolResultText("error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(s), nil
	}
	srv.AddTool(mcp.Tool{Name: "confirm", Description: "Asks the user to confirm", InputSchema: noArgs},
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := srv.RequestElicitation(ctx, mcp.ElicitationRequest{Params: mcp.ElicitationParams{
				Message: "run it?", RequestedSchema: map[string]any{"type": "object"},
			}})
			if err != nil {
				return text("", err)
			}
			return text(string(res.Action), nil)
		})
	srv.AddTool(mcp.Tool{Name: "sample", Description: "Asks the client's model", InputSchema: noArgs},
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := srv.RequestSampling(ctx, mcp.CreateMessageRequest{CreateMessageParams: mcp.CreateMessageParams{
				Messages:  []mcp.SamplingMessage{{Role: mcp.RoleUser, Content: mcp.NewTextContent("hi")}},
				MaxTokens: 5,
			}})
			if err != nil {
				return text("", err)
			}
			return text(res.Model, nil)
		})
	srv.AddTool(mcp.Tool{Name: "roots", Description: "Lists the client's roots", InputSchema: noArgs},
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := srv.RequestRoots(ctx, mcp.ListRootsRequest{})
			if err != nil {
				return text("", err)
			}
			uris := make([]string, 0, len(res.Roots))
			for _, r := range res.Roots {
				uris = append(uris, r.URI)
			}
			return text("roots:"+strings.Join(uris, ","), nil)
		})
	hs := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
	t.Cleanup(func() { closeHTTPFixture(hs) })
	return hs.URL
}

// relayClient is the user's MCP client: it answers what the upstream asks.
type relayClient struct {
	mu    sync.Mutex
	asked []string
}

func (c *relayClient) record(s string) {
	c.mu.Lock()
	c.asked = append(c.asked, s)
	c.mu.Unlock()
}

func (c *relayClient) Elicit(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	c.record("elicit")
	return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}}, nil
}

func (c *relayClient) CreateMessage(context.Context, mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	c.record("sample")
	return &mcp.CreateMessageResult{Model: "client-model", SamplingMessage: mcp.SamplingMessage{Role: mcp.RoleAssistant, Content: mcp.NewTextContent("ok")}}, nil
}

func (c *relayClient) ListRoots(context.Context, mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	c.record("roots")
	return &mcp.ListRootsResult{Roots: []mcp.Root{{URI: "file:///project"}}}, nil
}

// relayMCPClient connects a client on the last legacy protocol revision (the
// one with server-initiated requests) with elicitation, sampling and roots.
func (env *TestEnvironment) relayMCPClient(path string, h *relayClient) *client.Client {
	t := env.t
	t.Helper()
	tr, err := transport.NewStreamableHTTP(env.baseURL()+path, transport.WithContinuousListening())
	require.NoError(t, err)
	c := client.NewClient(tr, client.WithElicitationHandler(h), client.WithSamplingHandler(h), client.WithRootsHandler(h))
	// mcp-go runs the GET stream, where sampling and roots requests arrive, on
	// the Start context: it must outlive this helper.
	require.NoError(t, c.Start(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_LEGACY_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "relay-test", Version: "1"}
	_, err = c.Initialize(ctx, req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (c *relayClient) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.asked
	c.asked = nil
	return out
}

// upstreamAsks drops the roots request mcpproxy itself sends on a client's
// first request (Spec 082 workspace), which is not the upstream's.
func upstreamAsks(asked []string) []string {
	if len(asked) > 0 && asked[0] == "roots" {
		return asked[1:]
	}
	return asked
}

func TestClientRelay_UpstreamRequestsReachTheCallingClient(t *testing.T) {
	env := startFwdEnv(t, []*config.ServerConfig{fwdServerCfg("relay", newRelayUpstream(t))}, nil)

	surfaces := []struct {
		name string
		path string
		call func(*testing.T, *client.Client, string) string
	}{
		{"call_tool_read", "/mcp", func(t *testing.T, c *client.Client, tool string) string { return fwdCallRead(t, c, "relay", tool) }},
		{"direct", "/mcp/all", func(t *testing.T, c *client.Client, tool string) string { return fwdCallDirect(t, c, "relay", tool) }},
		{"single_server", "/mcp/s/relay", func(t *testing.T, c *client.Client, tool string) string { return fwdCallSingleServer(t, c, tool) }},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			h := &relayClient{}
			c := env.relayMCPClient(s.path, h)
			assert.Equal(t, "accept", s.call(t, c, "confirm"))
			assert.Equal(t, "client-model", s.call(t, c, "sample"))
			assert.Equal(t, "roots:file:///project", s.call(t, c, "roots"))
			assert.Equal(t, []string{"elicit", "sample", "roots"}, upstreamAsks(h.take()))
		})
	}

	t.Run("code_execution", func(t *testing.T) {
		h := &relayClient{}
		c := env.relayMCPClient("/mcp/code", h)
		fwdCallCode(t, c, "relay", "confirm")
		assert.Equal(t, []string{"elicit"}, upstreamAsks(h.take()))
	})

	t.Run("client that cannot be asked", func(t *testing.T) {
		c := env.fwdClient("/mcp", nil) // declares nothing, on protocol 2026-07-28
		assert.Equal(t, "decline", fwdCallRead(t, c, "relay", "confirm"))
		assert.Contains(t, fwdCallRead(t, c, "relay", "sample"), "error:")
		assert.Equal(t, "roots:", fwdCallRead(t, c, "relay", "roots"))
	})
}
