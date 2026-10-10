package upstream

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-02 cross-review: the sweep lists servers one after another, so each
// server's inventory ticket must be taken immediately before ITS tools/list.
// A ticket taken by another discovery pass while the sweep is stalled on the
// first server must sort before the second server's capture and after the
// first's — otherwise a genuinely newer capture of the second server would be
// dropped as stale.
func TestDiscoverToolsReportTicketed_TicketsFollowPerServerCaptureOrder(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	m, _ := newObservedManager(t)

	var armed atomic.Bool
	var firstOnce sync.Once
	firstEntered := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	newUpstream := func(name string) *httptest.Server {
		srv := mcpserver.NewMCPServer(name, "0.0.1", mcpserver.WithToolCapabilities(true))
		srv.AddTool(mcp.NewTool(name+"_tool", mcp.WithDescription(name)), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
		handler := mcpserver.NewStreamableHTTPServer(srv)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if armed.Load() && bytes.Contains(body, []byte(`"tools/list"`)) {
				first := false
				firstOnce.Do(func() { first = true })
				if first {
					firstEntered <- name
					<-release
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(ts.Close)
		return ts
	}

	for _, name := range []string{"srv-a", "srv-b"} {
		ts := newUpstream(name)
		require.NoError(t, m.AddServerConfig(name, &config.ServerConfig{Name: name, Protocol: "streamable-http", URL: ts.URL, Enabled: true}))
		client, ok := m.GetClient(name)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		require.NoError(t, client.Connect(ctx))
		cancel()
	}
	armed.Store(true)

	var counter atomic.Uint64
	type result struct {
		listed  []string
		tickets map[string]uint64
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, listed, tickets, err := m.DiscoverToolsReportTicketed(context.Background(), false, func() uint64 { return counter.Add(1) })
		done <- result{listed, tickets, err}
	}()

	var stalled string
	select {
	case stalled = <-firstEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the sweep never reached its first tools/list")
	}
	// Another discovery pass captures while the sweep is stalled.
	other := counter.Add(1)
	releaseOnce.Do(func() { close(release) })

	var res result
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the sweep did not finish")
	}
	require.NoError(t, res.err)
	require.ElementsMatch(t, []string{"srv-a", "srv-b"}, res.listed)
	later := "srv-a"
	if stalled == "srv-a" {
		later = "srv-b"
	}
	require.Less(t, res.tickets[stalled], other, "the stalled server's capture started before the other pass")
	require.Greater(t, res.tickets[later], other, "a server listed after the other pass must carry a later ticket")
}
