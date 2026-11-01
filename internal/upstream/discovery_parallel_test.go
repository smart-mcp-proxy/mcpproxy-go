package upstream

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// inFlightGauge counts concurrent tools/list requests across upstreams. Each
// request waits until another one is in flight (or a timeout passes), so a
// sweep that lists servers one at a time never reaches a peak above 1.
type inFlightGauge struct {
	mu       sync.Mutex
	cur, max int
	overlap  chan struct{}
	once     sync.Once
	armed    atomic.Bool
}

func (g *inFlightGauge) enter() {
	g.mu.Lock()
	g.cur++
	if g.cur > g.max {
		g.max = g.cur
	}
	if g.cur >= 2 {
		g.once.Do(func() { close(g.overlap) })
	}
	g.mu.Unlock()
	select {
	case <-g.overlap:
	case <-time.After(2 * time.Second):
	}
	g.mu.Lock()
	g.cur--
	g.mu.Unlock()
}

func newGaugedUpstream(t *testing.T, g *inFlightGauge, tool string) *mcpserver.MCPServer {
	t.Helper()
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeListTools(func(context.Context, any, *mcp.ListToolsRequest) {
		// Connect lists tools too; only the sweep's lists are measured.
		if g.armed.Load() {
			g.enter()
		}
	})
	srv := mcpserver.NewMCPServer("gauged", "0.0.1", mcpserver.WithToolCapabilities(true), mcpserver.WithHooks(hooks))
	srv.AddTool(mcp.NewTool(tool, mcp.WithDescription("test tool")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	return srv
}

// A slow upstream must not hold up the tools/list of every other server: the
// sweep lists servers concurrently.
func TestDiscoverToolsListsServersConcurrently(t *testing.T) {
	m, _ := newObservedManager(t)
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	g := &inFlightGauge{overlap: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		ts := servertest.NewTestStreamableHTTPServer(newGaugedUpstream(t, g, fmt.Sprintf("tool_%d", i)))
		t.Cleanup(ts.Close)
		id := fmt.Sprintf("srv-%d", i)
		require.NoError(t, m.AddServerConfig(id, &config.ServerConfig{
			Name: fmt.Sprintf("server-%d", i), Protocol: "streamable-http", URL: ts.URL, Enabled: true,
		}))
		client, ok := m.GetClient(id)
		require.True(t, ok)
		require.NoError(t, client.Connect(ctx))
	}
	g.armed.Store(true)

	tools, listed, err := m.DiscoverToolsReport(ctx, false)
	require.NoError(t, err)
	assert.Len(t, tools, 3)
	assert.Len(t, listed, 3)
	g.mu.Lock()
	peak := g.max
	g.mu.Unlock()
	assert.GreaterOrEqual(t, peak, 2, "tools/list requests never overlapped: the sweep lists servers one at a time")
}
