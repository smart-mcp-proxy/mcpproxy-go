package core

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/progressrelay"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// progressUpstream is an MCP server whose "work" tool reports progress three
// times under whatever token the request carried, and remembers the token.
func progressUpstream(seen *[]any, mu *sync.Mutex) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer("progress-up", "1")
	srv.AddTool(mcp.NewTool("work"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var tok any
		if req.Params.Meta != nil {
			tok = req.Params.Meta.ProgressToken
		}
		mu.Lock()
		*seen = append(*seen, tok)
		mu.Unlock()
		if tok == nil {
			return mcp.NewToolResultText("no token"), nil
		}
		for i := 1; i <= 3; i++ {
			_ = srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
				"progressToken": tok, "progress": i, "total": 3, "message": fmt.Sprintf("step %d", i),
			})
			time.Sleep(progressrelay.MinInterval + 20*time.Millisecond)
		}
		return mcp.NewToolResultText("done"), nil
	})
	return srv
}

// GH #1967: an upstream's notifications/progress reach the client session
// that made the call, under the client's own token, while the upstream only
// ever sees a token mcpproxy minted for that call.
func TestProgressRelay_StreamableHTTPUpstream(t *testing.T) {
	disableOAuthForTest(t)
	var (
		mu   sync.Mutex
		seen []any
	)
	srv := httptest.NewServer(mcpserver.NewStreamableHTTPServer(progressUpstream(&seen, &mu)))
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{Name: "progress-http", Protocol: "streamable-http", URL: srv.URL, Enabled: true}
	c, err := NewClient("progress-http", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	// No sink: the upstream request carries no token, nothing is registered.
	assert.Equal(t, "no token", callText(t, c, context.Background(), "work"))

	down := &downstreamSession{notes: make(chan mcp.JSONRPCNotification, 16)}
	dctx := mcpserver.NewMCPServer("down", "1").WithContext(context.Background(), down)
	sink := progressrelay.SessionSink(dctx, "client-tok", nil)
	require.NotNil(t, sink)
	assert.Equal(t, "done", callText(t, c, progressrelay.WithSink(dctx, sink), "work"))
	assert.Equal(t, 0, c.progress.Len(), "the call's token is forgotten when it ends")

	mu.Lock()
	require.Len(t, seen, 2)
	assert.Nil(t, seen[0])
	upTok, ok := seen[1].(string)
	mu.Unlock()
	require.True(t, ok)
	assert.NotEqual(t, "client-tok", upTok, "the client's token is never sent upstream")

	var got []mcp.JSONRPCNotification
	for len(got) < 3 {
		select {
		case n := <-down.notes:
			got = append(got, n)
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d progress notifications, want 3", len(got))
		}
	}
	for i, n := range got {
		assert.Equal(t, string(mcp.MethodNotificationProgress), n.Method)
		f := n.Params.AdditionalFields
		assert.Equal(t, "client-tok", f["progressToken"])
		assert.Equal(t, float64(i+1), f["progress"])
		assert.Equal(t, 3.0, f["total"])
		assert.Equal(t, fmt.Sprintf("step %d", i+1), f["message"])
	}

	// A late notification for the ended call reaches nobody.
	assert.False(t, c.progress.Dispatch(mcp.JSONRPCNotification{Notification: mcp.Notification{
		Method: string(mcp.MethodNotificationProgress),
		Params: mcp.NotificationParams{AdditionalFields: map[string]any{"progressToken": upTok, "progress": 9.0}},
	}}))
}

// A disconnect forgets every call in flight.
func TestProgressRelay_DisconnectClears(t *testing.T) {
	var c Client
	tok, end := c.progress.Begin(progressrelay.WithSink(context.Background(), func(progressrelay.Update) bool { t.Error("forwarded after disconnect"); return true }))
	defer end()
	require.NotNil(t, tok)
	c.clearProgressCalls()
	assert.Equal(t, 0, c.progress.Len())
}
