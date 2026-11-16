package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Boundary tests for the /mcp/s/<server> surface against a real proxy,
// runtime and counting upstreams: the surface must never admit a caller, list
// a tool, forward instructions or dispatch a call that /mcp/all would refuse.

func pinnedCtx(pin string) context.Context {
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "pinned-bot", TokenPrefix: "mcp_pinned",
		ProfilePin: pin, AllowedServers: []string{"*"},
		Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	return withDirectRequestKindBox(ctx)
}

func scopedCtx(servers ...string) context.Context {
	return withDirectRequestKindBox(auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "scoped-bot", TokenPrefix: "mcp_scoped",
		AllowedServers: servers,
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	}))
}

func adminCtxWithKind() context.Context {
	return withDirectRequestKindBox(auth.WithAuthContext(context.Background(), auth.AdminContext()))
}

func singleServerCall(t *testing.T, ctx context.Context, proxy *MCPProxyServer, server, tool string) (isError bool, text string) {
	t.Helper()
	srv := proxy.SingleServerMCP(server)
	require.NotNil(t, srv, "surface for %q", server)
	setDirectRequestKind(ctx, directRequestKindCall)
	raw, err := json.Marshal(srv.HandleMessage(ctx, []byte(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+tool+`","arguments":{}}}`)))
	require.NoError(t, err)
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp), string(raw))
	if resp.Error != nil {
		return true, resp.Error.Message
	}
	if len(resp.Result.Content) > 0 {
		text = resp.Result.Content[0].Text
	}
	return resp.Result.IsError, text
}

func singleServerList(t *testing.T, ctx context.Context, proxy *MCPProxyServer, server string) []string {
	t.Helper()
	srv := proxy.SingleServerMCP(server)
	require.NotNil(t, srv, "surface for %q", server)
	setDirectRequestKind(ctx, directRequestKindList)
	raw, err := json.Marshal(srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	require.NoError(t, err)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp), string(raw))
	var names []string
	for _, tool := range resp.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestSingleServerBoundary_ServesBareToolsAndDispatches(t *testing.T) {
	proxy, _, ups := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()

	names := singleServerList(t, scopedCtx("github"), proxy, "github")
	assert.Contains(t, names, "list_issues")
	assert.Contains(t, names, "create_issue")
	for _, n := range names {
		assert.NotContains(t, n, "__", "no server__ prefix on the single-server surface")
	}

	isErr, text := singleServerCall(t, scopedCtx("github"), proxy, "github", "list_issues")
	assert.False(t, isErr, text)
	assert.Equal(t, []string{"list_issues"}, ups["github"].dispatched())
}

func TestSingleServerBoundary_TokenNotAllowedServerGets404(t *testing.T) {
	proxy, _, _ := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()
	srv := &Server{logger: zap.NewNop(), mcpProxy: proxy}

	req := httptest.NewRequest(http.MethodPost, "/mcp/s/notion", nil)
	req = req.WithContext(scopedCtx("github"))
	rec := httptest.NewRecorder()
	srv.serveSingleServer(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// An unknown server answers identically.
	req = httptest.NewRequest(http.MethodPost, "/mcp/s/nope", nil)
	req = req.WithContext(scopedCtx("github"))
	rec2 := httptest.NewRecorder()
	srv.serveSingleServer(rec2, req)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
	assert.Equal(t, rec.Body.String(), rec2.Body.String())
}

// Spec 118: a token pinned to a profile that does not reach the server is
// refused with the same 404, even though its server allow-list is "*".
func TestSingleServerBoundary_ProfilePinOutsideReachIs404(t *testing.T) {
	proxy, _, _ := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()
	srv := &Server{logger: zap.NewNop(), mcpProxy: proxy}

	// "legacy" reaches github only.
	for _, name := range []string{"notion", "filesystem"} {
		req := httptest.NewRequest(http.MethodPost, "/mcp/s/"+name, nil)
		req = req.WithContext(pinnedCtx("legacy"))
		rec := httptest.NewRecorder()
		srv.serveSingleServer(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code, name)
	}
	assert.True(t, proxy.singleServerAdmits(pinnedCtx("legacy"), "github"), "pin reaches github")
	assert.False(t, proxy.singleServerAdmits(pinnedCtx("legacy"), "notion"))
	assert.False(t, proxy.singleServerAdmits(pinnedCtx("does-not-exist"), "github"), "dangling pin fails closed")
}

func TestSingleServerBoundary_PinnedReadOnlyProfileHidesAndBlocksWriteTools(t *testing.T) {
	proxy, _, ups := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()

	names := singleServerList(t, pinnedCtx("work-readonly"), proxy, "github")
	assert.NotContains(t, names, "create_issue", "write tool above the profile's max_tier")
	assert.NotContains(t, names, "delete_repo")

	isErr, _ := singleServerCall(t, pinnedCtx("work-readonly"), proxy, "github", "create_issue")
	assert.True(t, isErr)
	assert.Empty(t, ups["github"].dispatched(), "a refused call never reaches the upstream")
}

func TestSingleServerBoundary_InstructionsOnlyForAdmittedCallers(t *testing.T) {
	proxy, _, _ := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()
	srv := proxy.SingleServerMCP("notion")
	require.NotNil(t, srv)

	assert.Equal(t, "upstream instructions of notion",
		proxy.instructionsForCaller(scopedCtx("notion"), srv, ""))
	assert.Empty(t, proxy.instructionsForCaller(pinnedCtx("legacy"), srv, ""), "pin outside reach")
	assert.Empty(t, proxy.instructionsForCaller(scopedCtx("github"), srv, ""), "token not allowed the server")
}

func TestSingleServerBoundary_QuarantinedAndDisabledServers(t *testing.T) {
	proxy, _, ups := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()
	srv := proxy.SingleServerMCP("notion")
	require.NotNil(t, srv)

	cfg, err := proxy.storage.GetUpstreamServer("notion")
	require.NoError(t, err)

	quarantined := *cfg
	quarantined.Quarantined = true
	require.NoError(t, proxy.storage.SaveUpstreamServer(&quarantined))
	assert.Empty(t, proxy.instructionsForCaller(adminCtxWithKind(), srv, ""), "a quarantined server's instructions are never forwarded")
	isErr, _ := singleServerCall(t, scopedCtx("notion"), proxy, "notion", "update_page")
	assert.True(t, isErr)
	assert.Empty(t, ups["notion"].dispatched())

	disabled := *cfg
	disabled.Enabled = false
	require.NoError(t, proxy.storage.SaveUpstreamServer(&disabled))
	assert.Empty(t, proxy.instructionsForCaller(adminCtxWithKind(), srv, ""), "a disabled server's instructions are not forwarded")
	isErr, _ = singleServerCall(t, scopedCtx("notion"), proxy, "notion", "update_page")
	assert.True(t, isErr)
	assert.Empty(t, ups["notion"].dispatched())
}

func TestSingleServerBoundary_PendingAndChangedToolsNotListedOrCallable(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}}
	})
	ups := startCountingUpstream(t, proxy, rt, "github",
		readSpec("list_issues"),
		toolSpec{Name: "pending_tool", Description: "p", Approval: storage.ToolApprovalStatusPending, Annotations: readSpec("x").Annotations},
		toolSpec{Name: "changed_tool", Description: "c", Approval: storage.ToolApprovalStatusChanged, Annotations: readSpec("x").Annotations},
	)
	proxy.RefreshDirectModeTools()

	names := singleServerList(t, scopedCtx("github"), proxy, "github")
	assert.Contains(t, names, "list_issues")
	assert.NotContains(t, names, "pending_tool")
	assert.NotContains(t, names, "changed_tool")

	// An administrator keeps pending/changed tools in the operator listing
	// (as on /mcp/all) but still cannot dispatch them.
	for _, tool := range []string{"pending_tool", "changed_tool"} {
		for _, ctx := range []context.Context{scopedCtx("github"), adminCtxWithKind()} {
			isErr, _ := singleServerCall(t, ctx, proxy, "github", tool)
			assert.True(t, isErr, tool)
		}
	}
	assert.Empty(t, ups.dispatched())
}

func TestSingleServerBoundary_RemovedServerIsNoLongerServed(t *testing.T) {
	proxy, _, _ := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()
	require.NotNil(t, proxy.SingleServerMCP("notion"))

	proxy.upstreamManager.RemoveServer("notion")
	assert.Nil(t, proxy.SingleServerMCP("notion"), "a removed server is no longer served")
	assert.False(t, proxy.singleServerAdmits(adminCtxWithKind(), "notion"))
}

func TestSingleServerBoundary_ReplacedServerDropsOldTools(t *testing.T) {
	proxy, rt, ups := newProfilesV3FixtureUpstreams(t, nil)
	proxy.RefreshDirectModeTools()
	require.Contains(t, singleServerList(t, scopedCtx("notion"), proxy, "notion"), "update_page")

	// Replace the server in place: a new upstream with a different tool.
	proxy.upstreamManager.RemoveServer("notion")
	ups2 := startCountingUpstream(t, proxy, rt, "notion", readSpec("search_pages"))
	proxy.RefreshDirectModeTools()

	names := singleServerList(t, scopedCtx("notion"), proxy, "notion")
	assert.Contains(t, names, "search_pages")
	assert.NotContains(t, names, "update_page", "the replaced server's old tools are gone")

	isErr, _ := singleServerCall(t, scopedCtx("notion"), proxy, "notion", "update_page")
	assert.True(t, isErr)
	assert.Empty(t, ups["notion"].dispatched(), "the old upstream is never dispatched to")
	isErr, text := singleServerCall(t, scopedCtx("notion"), proxy, "notion", "search_pages")
	assert.False(t, isErr, text)
	assert.Equal(t, []string{"search_pages"}, ups2.dispatched())
}

// A call on the single-server surface is attributed to the calling token and
// session exactly as a /mcp/all call is, and is logged against the upstream's
// own tool name.
func TestSingleServerBoundary_ActivityRecordCarriesTheCaller(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.proxy.RefreshDirectModeTools()
	registerAttributionSession(f, "s-single", "Claude Code", "scoped-bot", "")

	ctx := sessionCtx(scopedCtx("github"), "s-single")
	isErr, text := singleServerCall(t, ctx, f.proxy, "github", "list_issues")
	require.False(t, isErr, text)

	rec := waitToolCallFor(t, f, "s-single", "github", "list_issues")
	assert.Equal(t, "scoped-bot", rec.TokenName)
	assert.Equal(t, "Claude Code", rec.ClientName)
}
