package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// relayUpstream is an MCP server whose tools ask their client something
// mid-call: elicitation, sampling, roots, or which capabilities it declared.
func relayUpstream() *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer("relay-up", "1", mcpserver.WithElicitation(), mcpserver.WithRoots())
	text := func(s string) *mcp.CallToolResult { return mcp.NewToolResultText(s) }
	srv.AddTool(mcp.NewTool("confirm"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := srv.RequestElicitation(ctx, mcp.ElicitationRequest{Params: mcp.ElicitationParams{
			Message: "run it?", RequestedSchema: map[string]any{"type": "object"},
		}})
		if err != nil {
			return text("error: " + err.Error()), nil
		}
		return text(string(res.Action)), nil
	})
	srv.AddTool(mcp.NewTool("sample"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := srv.RequestSampling(ctx, mcp.CreateMessageRequest{CreateMessageParams: mcp.CreateMessageParams{MaxTokens: 5}})
		if err != nil {
			return text("error: " + err.Error()), nil
		}
		return text(res.Model), nil
	})
	srv.AddTool(mcp.NewTool("roots"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := srv.RequestRoots(ctx, mcp.ListRootsRequest{})
		if err != nil {
			return text("error: " + err.Error()), nil
		}
		uris := make([]string, 0, len(res.Roots))
		for _, r := range res.Roots {
			uris = append(uris, r.URI)
		}
		return text("roots:" + strings.Join(uris, ",")), nil
	})
	srv.AddTool(mcp.NewTool("login"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		session := mcpserver.ClientSessionFromContext(ctx)
		res, err := srv.RequestURLElicitation(ctx, session, "login-1", "https://example.test/login", "log in")
		if err != nil {
			return text("error: " + err.Error()), nil
		}
		if res.Action == mcp.ElicitationResponseActionAccept {
			_ = srv.SendElicitationComplete(ctx, session, "login-1")
		}
		return text(string(res.Action)), nil
	})
	srv.AddTool(mcp.NewTool("caps"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		info, _ := mcpserver.ClientSessionFromContext(ctx).(mcpserver.SessionWithClientInfo)
		if info == nil {
			return text("no session"), nil
		}
		b, _ := json.Marshal(info.GetClientCapabilities())
		return text(string(b)), nil
	})
	return srv
}

// downstreamSession stands in for the MCP client that made the tool call.
type downstreamSession struct {
	mu    sync.Mutex
	asked []string
	notes chan mcp.JSONRPCNotification
}

func (s *downstreamSession) SessionID() string                                   { return "downstream" }
func (s *downstreamSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.notes }
func (s *downstreamSession) Initialize()                                         {}
func (s *downstreamSession) Initialized() bool                                   { return true }
func (s *downstreamSession) GetClientInfo() mcp.Implementation                   { return mcp.Implementation{} }
func (s *downstreamSession) SetClientInfo(mcp.Implementation)                    {}
func (s *downstreamSession) SetClientCapabilities(mcp.ClientCapabilities)        {}
func (s *downstreamSession) GetClientCapabilities() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{
		Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{},
		Sampling:    &mcp.SamplingCapability{},
		Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}},
	}
}

func (s *downstreamSession) record(what string) {
	s.mu.Lock()
	s.asked = append(s.asked, what)
	s.mu.Unlock()
}

func (s *downstreamSession) RequestElicitation(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	s.record("elicit")
	return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}}, nil
}

func (s *downstreamSession) RequestSampling(context.Context, mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	s.record("sample")
	return &mcp.CreateMessageResult{Model: "client-model"}, nil
}

func (s *downstreamSession) ListRoots(context.Context, mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	s.record("roots")
	return &mcp.ListRootsResult{Roots: []mcp.Root{{URI: "file:///project"}}}, nil
}

func callText(t *testing.T, c *Client, ctx context.Context, tool string) string {
	t.Helper()
	res, err := c.CallTool(ctx, tool, nil)
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func assertRelays(t *testing.T, c *Client) {
	t.Helper()
	caps := callText(t, c, context.Background(), "caps")
	assert.Contains(t, caps, `"elicitation":{"form":{},"url":{}}`)
	assert.Contains(t, caps, `"sampling":{}`)
	assert.Contains(t, caps, `"roots":{}`)

	down := &downstreamSession{notes: make(chan mcp.JSONRPCNotification, 4)}
	ctx := mcpserver.NewMCPServer("down", "1").WithContext(context.Background(), down)
	assert.Equal(t, "accept", callText(t, c, ctx, "confirm"))
	assert.Equal(t, "client-model", callText(t, c, ctx, "sample"))
	assert.Equal(t, "roots:file:///project", callText(t, c, ctx, "roots"))
	assert.Equal(t, "accept", callText(t, c, ctx, "login"))
	assert.Equal(t, []string{"elicit", "sample", "roots", "elicit"}, down.asked)

	// The URL elicitation's completion reaches the client that was asked.
	select {
	case n := <-down.notes:
		assert.Equal(t, string(mcp.MethodNotificationElicitationComplete), n.Method)
		assert.Equal(t, "login-1", n.Params.AdditionalFields["elicitationId"])
	case <-time.After(5 * time.Second):
		t.Fatal("the URL elicitation's completion did not reach the client")
	}

	// A call no MCP client made (REST, CLI) is answered without asking.
	assert.Equal(t, "decline", callText(t, c, context.Background(), "confirm"))
	assert.Contains(t, callText(t, c, context.Background(), "sample"), "error:")
	assert.Equal(t, "roots:", callText(t, c, context.Background(), "roots"))
}

func TestRelay_StreamableHTTPUpstream(t *testing.T) {
	disableOAuthForTest(t)
	srv := httptest.NewServer(mcpserver.NewStreamableHTTPServer(relayUpstream()))
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{Name: "relay-http", Protocol: "streamable-http", URL: srv.URL, Enabled: true}
	c, err := NewClient("relay-http", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	assertRelays(t, c)
}

const relayStdioHelperEnv = "MCPPROXY_RELAY_STDIO_HELPER"

// TestRelayStdioHelper is the stdio upstream TestRelay_StdioUpstream spawns.
func TestRelayStdioHelper(t *testing.T) {
	if os.Getenv(relayStdioHelperEnv) != "1" {
		t.Skip("helper process")
	}
	_ = mcpserver.ServeStdio(relayUpstream())
	os.Exit(0)
}

// A stdio upstream's requests carry no call: they go to the client of the
// only call in flight.
func TestRelay_StdioUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the test binary")
	}
	cfg := &config.ServerConfig{
		Name: "relay-stdio", Protocol: "stdio", Enabled: true,
		Command: os.Args[0], Args: []string{"-test.run=^TestRelayStdioHelper$"},
		Env: map[string]string{relayStdioHelperEnv: "1"},
	}
	c, err := NewClient("relay-stdio", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	assertRelays(t, c)
}

func TestInflightCalls_Only(t *testing.T) {
	var f inflightCalls
	_, ok := f.only()
	assert.False(t, ok, "no call in flight")

	a := context.WithValue(context.Background(), struct{}{}, "a")
	doneA := f.add(a)
	got, ok := f.only()
	require.True(t, ok)
	assert.Equal(t, a, got)

	doneB := f.add(context.Background())
	_, ok = f.only()
	assert.False(t, ok, "two calls in flight: nobody to credit")

	doneA()
	_, ok = f.only()
	assert.True(t, ok)
	doneB()
	_, ok = f.only()
	assert.False(t, ok)
}
