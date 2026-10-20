package server

import (
	"context"
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

// Spec 058 US3 (research D5): `initialize` is METHOD_NOT_FOUND in the
// 2026-07-28 era, so AddAfterInitialize never fires for a modern client and the
// session store never learns its name — client-name attribution on activity
// records and the Spec 044 activation funnel go blind. The replacement data
// travels on every request: server.RequestProtocolInfoFromContext(ctx).
// ClientInfo, populated by mcp-go before any hook or handler runs.
//
// Test names avoid the substring "MCP" (release-qa-gate.yml skips on it).

// The attribution helper every activity funnel feeds reads the request-carried
// identity when no session knows the client.
func TestModernAttribution_ReadsClientInfoFromRequest(t *testing.T) {
	proxy := statelessListProxy(t)

	t.Run("modern request without a session", func(t *testing.T) {
		attr := proxy.activityAttribution(modernRequestCtx(context.Background()), "")
		assert.Equal(t, "era-probe", attr.ClientName)
		assert.Equal(t, "1.0.0", attr.ClientVersion)
	})

	t.Run("modern request on the stdio session, which initialize never named", func(t *testing.T) {
		attr := proxy.activityAttribution(modernRequestCtx(sessionCtx(adminCtx(), "stdio")), "stdio")
		assert.Equal(t, "era-probe", attr.ClientName)
		assert.Equal(t, "1.0.0", attr.ClientVersion)
	})

	t.Run("a session's recorded name still wins over the request's claim", func(t *testing.T) {
		proxy.sessionStore.SetSession("legacy-1", "ide-from-initialize", "2.0.0", false, false, nil)
		attr := proxy.activityAttribution(modernRequestCtx(sessionCtx(adminCtx(), "legacy-1")), "legacy-1")
		assert.Equal(t, "ide-from-initialize", attr.ClientName)
		assert.Empty(t, attr.ClientVersion, "the version is request-carried only when the name is")
	})

	t.Run("modern request that declared no clientInfo", func(t *testing.T) {
		ctx := mcpserver.WithRequestProtocolInfo(context.Background(), &mcpserver.RequestProtocolInfo{Modern: true, ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION})
		attr := proxy.activityAttribution(ctx, "")
		assert.Empty(t, attr.ClientName)
		assert.Empty(t, attr.ClientVersion)
	})
}

// End to end: a modern client's name and version reach the tool_call activity
// record, and the client-seen funnel observes it, with no initialize and no
// session.
func TestModernAttribution_ReachesActivityRecord(t *testing.T) {
	env := NewTestEnvironment(t)
	t.Cleanup(env.Cleanup)
	ctx := context.Background()

	mock := env.CreateMockUpstreamServer("attrib-srv", []mcp.Tool{{Name: "echo", Description: "Echo"}})
	old := env.proxyServer.runtime.Config()
	cfgCopy := *old
	cfg := &cfgCopy
	cfg.Servers = append(append([]*config.ServerConfig{}, old.Servers...), &config.ServerConfig{
		Name: "attrib-srv", URL: mock.addr, Protocol: "streamable-http", Enabled: true, Quarantined: false,
	})
	env.proxyServer.runtime.UpdateConfig(cfg, "")
	require.NoError(t, env.proxyServer.runtime.CommitConfig(cfg))
	time.Sleep(2 * time.Second)
	_ = env.proxyServer.runtime.DiscoverAndIndexTools(ctx)
	time.Sleep(2 * time.Second)

	rt := env.proxyServer.runtime
	before, err := rt.StorageManager().GetOnboardingState()
	require.NoError(t, err)
	seenBefore := 0
	if before != nil {
		seenBefore = len(before.ClientLastSeen)
	}

	url := unpinnedProxyEndpoint(t, env.proxyServer, env.proxyServer.mcpProxy.GetMCPServer())
	tr, err := transport.NewStreamableHTTP(url)
	require.NoError(t, err)
	c := client.NewClient(tr, modernEra().options...)
	require.NoError(t, c.Start(ctx))
	t.Cleanup(func() { _ = c.Close() })
	init := mcp.InitializeRequest{}
	init.Params.ClientInfo = mcp.Implementation{Name: "era-probe", Version: "1.0.0"}
	_, err = c.Initialize(ctx, init)
	require.NoError(t, err)
	requireEra(t, c, modernEra())
	require.Empty(t, tr.GetSessionId(), "the modern era binds no session; attribution cannot come from one")

	call := mcp.CallToolRequest{}
	call.Params.Name = "call_tool_read"
	call.Params.Arguments = map[string]interface{}{
		"name":   "attrib-srv:echo",
		"args":   map[string]interface{}{},
		"intent": map[string]interface{}{"operation_type": "read"},
	}
	result, err := c.CallTool(ctx, call)
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)

	record := awaitToolCallActivity(t, rt, "attrib-srv", "echo")
	assert.Equal(t, "era-probe", record.ClientName, "the modern client's self-reported name must reach the record")
	assert.Equal(t, "1.0.0", record.Metadata["client_version"], "and so must its version")
	assert.Empty(t, record.SessionID, "no session was involved")

	after, err := rt.StorageManager().GetOnboardingState()
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.Greater(t, len(after.ClientLastSeen), seenBefore,
		"the client-seen funnel must observe a modern client although initialize never ran")
}
