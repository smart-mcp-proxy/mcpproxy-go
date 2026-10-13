package server

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
)

// captureProbe serves a bare tool that reports what the edge snapshot holds
// (names only: the snapshot has no value accessor) over the exact options every
// client-facing mount is built with.
func captureProbe(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	srv := mcpserver.NewMCPServer("capture-probe", "1.0.0")
	srv.AddTool(mcp.NewTool("snap"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		snap, ok := headerfwd.SnapshotFrom(ctx)
		if !ok {
			return mcp.NewToolResultText("none"), nil
		}
		return mcp.NewToolResultText(strings.Join(snap.Names(), ",")), nil
	})
	streamable := mcpserver.NewStreamableHTTPServer(srv, clientFacingStreamableOptions(func() *config.Config { return cfg })...)
	hs := httptest.NewServer(streamable)
	t.Cleanup(hs.Close)
	return hs
}

func probeCall(t *testing.T, url string, hdr map[string]string) string {
	t.Helper()
	tr, err := transport.NewStreamableHTTP(url, transport.WithHTTPHeaders(hdr))
	require.NoError(t, err)
	c := client.NewClient(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Start(ctx))
	defer c.Close()
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)
	req := mcp.CallToolRequest{}
	req.Params.Name = "snap"
	res, err := c.CallTool(ctx, req)
	require.NoError(t, err)
	return res.Content[0].(mcp.TextContent).Text
}

func fwdCfg(servers ...*config.ServerConfig) *config.Config { return &config.Config{Servers: servers} }

func TestClientFacingCapture_UnionOnlyAndDenyList(t *testing.T) {
	cfg := fwdCfg(
		&config.ServerConfig{Name: "a", Enabled: true, Protocol: "streamable-http", URL: "http://x", ForwardHeaders: []string{"X-Tenant-Id"}},
		&config.ServerConfig{Name: "b", Enabled: true, Protocol: "http", URL: "http://y", ForwardHeaders: []string{"x-region"}},
		&config.ServerConfig{Name: "sse", Enabled: true, Protocol: "sse", URL: "http://z", ForwardHeaders: []string{"X-Sse-Only"}},
		&config.ServerConfig{Name: "off", Enabled: false, Protocol: "http", URL: "http://w", ForwardHeaders: []string{"X-Disabled"}},
	)
	hs := captureProbe(t, cfg)
	got := probeCall(t, hs.URL, map[string]string{
		"X-Tenant-Id": "t1", "X-Region": "eu", "X-Sse-Only": "s", "X-Disabled": "d",
		"X-Not-Listed": "n", "X-Api-Key": "k",
	})
	assert.Equal(t, "X-Region,X-Tenant-Id", got, "only the union of enabled http/streamable allowlists")
}

func TestClientFacingCapture_DeniedNameNeverCaptured(t *testing.T) {
	cfg := fwdCfg(&config.ServerConfig{Name: "a", Enabled: true, Protocol: "http", URL: "http://x",
		ForwardHeaders: []string{"X-API-Key", "Cookie", "Origin"}})
	hs := captureProbe(t, cfg)
	assert.Equal(t, "none", probeCall(t, hs.URL, map[string]string{"X-Api-Key": "k", "Cookie": "c", "Origin": "o"}))
}

func TestClientFacingCapture_SwitchOffAndNilProvider(t *testing.T) {
	off := false
	cfg := fwdCfg(&config.ServerConfig{Name: "a", Enabled: true, Protocol: "http", URL: "http://x", ForwardHeaders: []string{"X-Tenant-Id"}})
	cfg.ForwardClientHeaders = &off
	assert.Equal(t, "none", probeCall(t, captureProbe(t, cfg).URL, map[string]string{"X-Tenant-Id": "t"}))

	t.Setenv(config.EnvForwardClientHeaders, "false")
	cfg.ForwardClientHeaders = nil
	assert.Equal(t, "none", probeCall(t, captureProbe(t, cfg).URL, map[string]string{"X-Tenant-Id": "t"}))

	// nil provider: no capture, no panic.
	srv := mcpserver.NewMCPServer("p", "1")
	hs := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, clientFacingStreamableOptions(nil)...))
	defer hs.Close()
	resp, err := http.Post(hs.URL, "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
}

// Every Streamable HTTP mount is built through clientFacingStreamableOptions
// (so all five /mcp* mounts capture), and the /v1/tool_code aliases reuse the
// /mcp handler. A mount added without the shared options fails here.
func TestAllStreamableMountsUseSharedCaptureOptions(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "server.go", nil, 0)
	require.NoError(t, err)
	var mounts int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "NewStreamableHTTPServer" {
			return true
		}
		mounts++
		found := false
		for _, a := range call.Args {
			ast.Inspect(a, func(m ast.Node) bool {
				if c, ok := m.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "clientFacingStreamableOptions" {
						found = true
					}
				}
				return true
			})
		}
		assert.True(t, found, "NewStreamableHTTPServer at %s must use clientFacingStreamableOptions", fset.Position(call.Pos()))
		return true
	})
	assert.Equal(t, 5, mounts, "/mcp, /mcp/all, /mcp/code, /mcp/call and /mcp/p/<slug>")

	src, err := os.ReadFile("server.go")
	require.NoError(t, err)
	for _, alias := range []string{`mux.Handle("/v1/tool_code", mcpHandler)`, `mux.Handle("/v1/tool-code", mcpHandler)`} {
		assert.Contains(t, string(src), alias)
	}
}
