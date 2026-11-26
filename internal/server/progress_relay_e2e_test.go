package server

// GH #1967: an upstream's notifications/progress during a tool call reach the
// MCP client that made the call, under the client's own token, through a real
// proxy, on the call_tool_*, direct and /mcp/s/<server> surfaces.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/progressrelay"
)

// progressFixture is a streamable-HTTP upstream whose "work" tool reports
// progress three times, with the "who" argument in the message, under the
// token its request carried. tokens records what each call carried.
type progressFixture struct {
	url    string
	mu     sync.Mutex
	tokens []any
}

func newProgressUpstream(t *testing.T) *progressFixture {
	t.Helper()
	fx := &progressFixture{}
	srv := mcpserver.NewMCPServer("progress-upstream", "1.0.0", mcpserver.WithToolCapabilities(true))
	schema := mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{"who": map[string]any{"type": "string"}}}
	srv.AddTool(mcp.Tool{Name: "work", Description: "Does slow work and reports progress", InputSchema: schema},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var tok any
			if req.Params.Meta != nil {
				tok = req.Params.Meta.ProgressToken
			}
			fx.mu.Lock()
			fx.tokens = append(fx.tokens, tok)
			fx.mu.Unlock()
			who, _ := req.GetArguments()["who"].(string)
			if tok != nil {
				for i := 1; i <= 3; i++ {
					_ = srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
						"progressToken": tok, "progress": i, "total": 3, "message": fmt.Sprintf("%s step %d", who, i),
					})
					time.Sleep(progressrelay.MinInterval + 30*time.Millisecond)
				}
			}
			return mcp.NewToolResultText("done"), nil
		})
	hs := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
	t.Cleanup(func() { closeHTTPFixture(hs) })
	fx.url = hs.URL
	return fx
}

func (fx *progressFixture) takeTokens() []any {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	out := fx.tokens
	fx.tokens = nil
	return out
}

// progressNotes collects the progress notifications one client receives.
type progressNotes struct {
	mu    sync.Mutex
	notes []map[string]any
}

func watchProgress(c *client.Client) *progressNotes {
	pn := &progressNotes{}
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method != string(mcp.MethodNotificationProgress) {
			return
		}
		pn.mu.Lock()
		pn.notes = append(pn.notes, n.Params.AdditionalFields)
		pn.mu.Unlock()
	})
	return pn
}

func (pn *progressNotes) take() []map[string]any {
	pn.mu.Lock()
	defer pn.mu.Unlock()
	out := pn.notes
	pn.notes = nil
	return out
}

func progressCall(t *testing.T, c *client.Client, name string, args map[string]any, tok mcp.ProgressToken) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	if tok != nil {
		req.Params.Meta = &mcp.Meta{ProgressToken: tok}
	}
	res, err := c.CallTool(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "%s -> %s", name, fwdText(res))
}

func wrapperArgs(who string) map[string]any {
	return map[string]any{
		"name": "slow:work", "args": map[string]any{"who": who},
		"intent": map[string]any{"operation_type": "read"},
	}
}

func assertProgress(t *testing.T, pn *progressNotes, tok any, who string) {
	t.Helper()
	var got []map[string]any
	require.Eventually(t, func() bool {
		got = append(got, pn.take()...)
		return len(got) >= 3
	}, 5*time.Second, 20*time.Millisecond, "got %v", got)
	require.Len(t, got, 3)
	for i, f := range got {
		assert.Equal(t, tok, f["progressToken"], "the client's own token")
		assert.Equal(t, float64(i+1), f["progress"])
		assert.Equal(t, 3.0, f["total"])
		assert.Equal(t, fmt.Sprintf("%s step %d", who, i+1), f["message"])
	}
}

func TestProgressRelay_ReachesTheCallingClient(t *testing.T) {
	fx := newProgressUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{fwdServerCfg("slow", fx.url)}, nil)

	surfaces := []struct {
		name, path, tool string
		args             func(who string) map[string]any
	}{
		{"call_tool_read", "/mcp", "call_tool_read", wrapperArgs},
		{"direct", "/mcp/all", "slow__work", func(who string) map[string]any { return map[string]any{"who": who} }},
		{"single_server", "/mcp/s/slow", "work", func(who string) map[string]any { return map[string]any{"who": who} }},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			c := env.relayMCPClient(s.path, &relayClient{})
			pn := watchProgress(c)
			progressCall(t, c, s.tool, s.args("a"), "client-tok")
			assertProgress(t, pn, "client-tok", "a")
			toks := fx.takeTokens()
			require.Len(t, toks, 1)
			assert.NotNil(t, toks[0])
			assert.NotEqual(t, "client-tok", toks[0], "the client's token is never sent upstream")

			// No token from the client: none upstream, nothing forwarded.
			progressCall(t, c, s.tool, s.args("b"), nil)
			assert.Equal(t, []any{nil}, fx.takeTokens())
			time.Sleep(100 * time.Millisecond)
			assert.Empty(t, pn.take())
		})
	}

	t.Run("two clients with the same token see only their own call", func(t *testing.T) {
		ca := env.relayMCPClient("/mcp", &relayClient{})
		cb := env.relayMCPClient("/mcp", &relayClient{})
		pa, pb := watchProgress(ca), watchProgress(cb)
		var wg sync.WaitGroup
		for who, c := range map[string]*client.Client{"a": ca, "b": cb} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				progressCall(t, c, "call_tool_read", wrapperArgs(who), 1)
			}()
		}
		wg.Wait()
		assertProgress(t, pa, 1.0, "a")
		assertProgress(t, pb, 1.0, "b")
		toks := fx.takeTokens()
		require.Len(t, toks, 2)
		assert.NotEqual(t, toks[0], toks[1], "each upstream call has a token of its own")
	})

	t.Run("code_execution sub-calls forward nothing", func(t *testing.T) {
		c := env.relayMCPClient("/mcp/code", &relayClient{})
		pn := watchProgress(c)
		progressCall(t, c, "code_execution", map[string]any{
			"code":  `var r = call_tool("slow", "work", {who: "c"}); ({ ok: r.ok })`,
			"input": map[string]any{},
		}, "code-tok")
		assert.Equal(t, []any{nil}, fx.takeTokens())
		time.Sleep(100 * time.Millisecond)
		assert.Empty(t, pn.take())
	})
}
