package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const recSentinel = "SENTINEL-carol-5b2e"

func TestScrubResultForRecord(t *testing.T) {
	r := httptestRequestWith("X-Tenant-Id", recSentinel)
	out := headerfwd.Outbound(headerfwd.Capture(r, map[string]struct{}{"X-Tenant-Id": {}}),
		headerfwd.Policy{Enabled: true, Allow: []string{"X-Tenant-Id"}, Transport: "http"})
	require.False(t, out.IsEmpty())

	orig := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("hello " + recSentinel),
			mcp.NewTextContent(`{"X-Tenant-Id":"` + recSentinel + `"}`),
			mcp.NewEmbeddedResource(mcp.TextResourceContents{URI: "x://r", Text: "X-Tenant-Id: " + recSentinel}),
		},
		StructuredContent: map[string]any{"who": recSentinel},
	}
	got := scrubResultForRecord(orig, out).(*mcp.CallToolResult)

	b, _ := json.Marshal(got)
	assert.NotContains(t, string(b), recSentinel, "the record copy must be scrubbed")
	// The original (what the client receives) is untouched.
	ob, _ := json.Marshal(orig)
	assert.Contains(t, string(ob), recSentinel)
	assert.Contains(t, orig.Content[0].(mcp.TextContent).Text, recSentinel)

	// No outbound set: same pointer, no copy.
	assert.Same(t, orig, scrubResultForRecord(orig, headerfwd.Snapshot{}))
	// Typed nil and nil survive.
	var nilRes *mcp.CallToolResult
	assert.Nil(t, scrubResultForRecord(nilRes, out).(*mcp.CallToolResult))
	assert.Nil(t, scrubResultForRecord(nil, out))
	// Non-CallToolResult values go through the generic path.
	g := scrubResultForRecord(map[string]any{"a": "v " + recSentinel}, out)
	gb, _ := json.Marshal(g)
	assert.NotContains(t, string(gb), recSentinel)
	assert.Equal(t, "x [forwarded:X-Tenant-Id] y", scrubForRecord("x "+recSentinel+" y", out))
}

func httptestRequestWith(name, value string) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	r.Header.Set(name, value)
	return r
}

// echoUpstream serves a tool that echoes the X-Tenant-Id header it received
// into a SUCCESSFUL result, in three forms (plain, JSON-escaped, name-anchored).
func (env *TestEnvironment) echoUpstream(name string) string {
	mcpServer := mcpserver.NewMCPServer(name, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	mcpServer.AddTool(mcp.Tool{
		Name:        "whoami",
		Description: "Echoes the tenant header",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		v := req.Header.Get("X-Tenant-Id")
		return mcp.NewToolResultText(fmt.Sprintf("plain=%s json=%q name-anchored=X-Tenant-Id: %s", v, v, v)), nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(env.t, err)
	hs := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpServer), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	env.t.Cleanup(func() { _ = hs.Close() })
	return "http://" + ln.Addr().String()
}

// T018a / SC-003 (success-result echo): the calling client receives the result
// unmodified; the activity Response and the BBolt ToolCallRecord carry no
// forwarded value.
func TestForwardedHeaderSuccessEchoScrubbedFromRecords(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()
	addr := env.echoUpstream("echoer")

	// A client that sends the tenant header on every POST.
	tr, err := transport.NewStreamableHTTP(env.proxyAddr, transport.WithHTTPHeaders(map[string]string{"X-Tenant-Id": recSentinel}))
	require.NoError(t, err)
	mcpClient := client.NewClient(tr)
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()
	add := mcp.CallToolRequest{}
	add.Params.Name = "upstream_servers"
	add.Params.Arguments = map[string]interface{}{
		"operation": "add", "name": "echoer", "url": addr, "protocol": "streamable-http",
		"enabled": true, "forward_headers_json": `["X-Tenant-Id"]`,
	}
	_, err = mcpClient.CallTool(ctx, add)
	require.NoError(t, err)

	rt := env.proxyServer.runtime
	sc, err := rt.StorageManager().GetUpstreamServer("echoer")
	require.NoError(t, err)
	require.Equal(t, []string{"X-Tenant-Id"}, sc.ForwardHeaders)
	sc.Quarantined = false
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
	servers, err := rt.StorageManager().ListUpstreamServers()
	require.NoError(t, err)
	cfg := rt.Config()
	cfg.Servers = servers
	require.NoError(t, rt.LoadConfiguredServers(cfg))
	time.Sleep(3 * time.Second)
	_ = rt.DiscoverAndIndexTools(ctx)
	time.Sleep(3 * time.Second)

	call := mcp.CallToolRequest{}
	call.Params.Name = "call_tool_read"
	call.Params.Arguments = map[string]interface{}{
		"name": "echoer:whoami", "args": map[string]interface{}{},
		"intent": map[string]interface{}{"operation_type": "read"},
	}
	res, err := mcpClient.CallTool(ctx, call)
	require.NoError(t, err)
	require.False(t, res.IsError, "%+v", res.Content)

	var clientText string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			clientText += tc.Text
		}
	}
	assert.Contains(t, clientText, recSentinel, "the client's own value is returned unmodified")

	rec := awaitToolCallActivity(t, rt, "echoer", "whoami")
	assert.Equal(t, "success", rec.Status)
	assert.NotEmpty(t, rec.Response)
	assert.NotContains(t, rec.Response, recSentinel, "activity Response must be scrubbed")
	assert.Contains(t, rec.Response, "[forwarded:X-Tenant-Id]")

	calls, err := rt.StorageManager().GetServerToolCalls(storage.GenerateServerID(sc), 20)
	require.NoError(t, err)
	require.NotEmpty(t, calls)
	for _, c := range calls {
		b, _ := json.Marshal(c)
		assert.False(t, strings.Contains(string(b), recSentinel), "ToolCallRecord must not carry the sentinel")
	}
}

// Round 4: Meta.AdditionalFields, RequestState and InputRequests are record
// copies too.
func TestScrubResultForRecord_MetaAndMultiRoundTrip(t *testing.T) {
	r := httptestRequestWith("X-Tenant-Id", recSentinel)
	out := headerfwd.Outbound(headerfwd.Capture(r, map[string]struct{}{"X-Tenant-Id": {}}),
		headerfwd.Policy{Enabled: true, Allow: []string{"X-Tenant-Id"}, Transport: "http"})
	orig := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent("ok")},
	}
	orig.Meta = &mcp.Meta{AdditionalFields: map[string]any{"echo": "who " + recSentinel}}
	orig.RequestState = "state-" + recSentinel
	got := scrubResultForRecord(orig, out).(*mcp.CallToolResult)
	b, _ := json.Marshal(got)
	assert.NotContains(t, string(b), recSentinel)
	assert.Equal(t, "state-[forwarded:X-Tenant-Id]", got.RequestState)
	// Original untouched.
	assert.Contains(t, orig.RequestState, recSentinel)
	assert.Contains(t, orig.Meta.AdditionalFields["echo"], recSentinel)
}

// Round 4: a value cut by response truncation must not survive as a prefix.
func TestScrubForRecord_TruncatedPrefix(t *testing.T) {
	r := httptestRequestWith("X-Tenant-Id", recSentinel)
	out := headerfwd.Outbound(headerfwd.Capture(r, map[string]struct{}{"X-Tenant-Id": {}}),
		headerfwd.Policy{Enabled: true, Allow: []string{"X-Tenant-Id"}, Transport: "http"})
	cut := recSentinel[:len(recSentinel)-5]
	for _, in := range []string{"data " + cut, "data " + cut + "\n\n... [truncated by mcpproxy]"} {
		got := scrubForRecord(in, out)
		assert.NotContains(t, got, cut[:10], in)
	}
}
