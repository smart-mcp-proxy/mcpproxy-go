package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1918 CI run 44349461004: TestTPAFetchLifecycle_ActivatesReprotectsAndRefusesBadReleases
// saw 2 requests on its fake upstream although the sweep never dials it. The
// run logged 1,335 refused `DELETE http://127.0.0.1:<port>` session-close
// requests from clients of earlier tests closing after their servers had gone.
// This reproduces that mechanism deterministically; it does not prove what the
// two historical requests were (the old fixture recorded nothing about them).
//
// A real mcp-go client of an earlier test, holding a session, closes after its
// server's port was reused for the fake upstream: its DELETE reaches the
// upstream at "/" and is recorded as another path, not as a call. Deliberate
// requests to the configured target count whatever their method or headers,
// including a session-carrying DELETE, and so do the OAuth endpoints the proxy
// derives from the target's origin.
func TestTPAFetchFixture_ForeignCloseVersusCallsToTheConfiguredTarget(t *testing.T) {
	// An earlier test's MCP server and a client with a live (legacy) session.
	mcpSrv := mcpserver.NewMCPServer("earlier-test", "0.0.1", mcpserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool("noop"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	earlier := httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(earlier.Close) // idempotent; covers an early failure
	addr := earlier.Listener.Addr().String()

	c, err := client.NewStreamableHttpClient("http://" + addr)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Start(ctx))
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_LEGACY_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "earlier-test-client", Version: "0"}
	_, err = c.Initialize(ctx, init)
	require.NoError(t, err)

	// That test's server goes away; its port is given to this test's upstream.
	earlier.Close()
	up := newE2EUpstream(t)
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err, "rebinding the freed port")
	fake := httptest.NewUnstartedServer(up)
	fake.Listener = ln
	fake.Start()
	t.Cleanup(fake.Close)

	// Injected foreign close: the earlier client closes late.
	require.NoError(t, c.Close())
	assert.Equal(t, []string{"DELETE /"}, up.otherRequests(), "the late session close reaches the reused port")
	assert.Empty(t, up.callsToTarget(), "it is not a call to this fixture's configured target")
	// The old fixture counted every request: this one would have failed the assertion.

	// Deliberate calls to the configured target all count.
	do := func(method, path string, header map[string]string) {
		req, err := http.NewRequest(method, fake.URL+path, nil)
		require.NoError(t, err)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	do(http.MethodPost, up.target, nil)
	do(http.MethodDelete, up.target, map[string]string{"Mcp-Session-Id": "a-session"})
	do(http.MethodGet, "/.well-known/oauth-protected-resource"+up.target, nil)
	// Requests the proxy derives from the origin alone count too.
	do(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	do(http.MethodPost, "/register", nil)
	assert.Equal(t, []string{
		"POST " + up.target,
		"DELETE " + up.target,
		"GET /.well-known/oauth-protected-resource" + up.target,
		"GET /.well-known/oauth-authorization-server",
		"POST /register",
	}, up.callsToTarget(), "every method and header on the configured target, and origin-derived OAuth endpoints, count")
	assert.Equal(t, []string{"DELETE /"}, up.otherRequests())
}
