package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 104 FR-016f — the retrieve surface must authorize a call_tool_* call
// against the TARGET tool's annotation-derived tier, not against the variant
// the caller picked. Direct mode (mcp_routing.go) and code execution
// (lookupToolPermission) already do; before this file the retrieve surface
// checked the token only against the selected variant, so a read-only token
// could drive a write or destructive tool through call_tool_read whenever
// intent validation let the variant mismatch through (always for write tools,
// and for destructive tools when strict validation is off).

// seedTargetTierServer publishes one approved, connected server into the live
// StateView with the given tools — the same lookup path production uses.
func seedTargetTierServer(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime, server string, tools []stateview.ToolInfo) {
	t.Helper()
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: server, Enabled: true}))
	rt.Supervisor().StateView().UpdateServer(server, func(s *stateview.ServerStatus) {
		s.Name = server
		s.Enabled = true
		s.Connected = true
		s.Tools = tools
	})
	for _, tool := range tools {
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: server, ToolName: tool.Name,
			Status: storage.ToolApprovalStatusApproved,
		}))
	}
}

func readOnlyAgentCtx(server string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		TokenPrefix:    "mcp_agt_r",
		AllowedServers: []string{server},
		Permissions:    []string{auth.PermRead},
	})
}

func callToolReadOn(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantRead
	req.Params.Arguments = map[string]interface{}{"name": name}
	result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError, "a read-only token must not have the call admitted")
	require.NotEmpty(t, result.Content)
	return result.Content[0].(mcp.TextContent).Text
}

func TestCallToolRead_ReadOnlyToken_TargetTierEnforced(t *testing.T) {
	writeTool := stateview.ToolInfo{
		Name: "create_issue", Description: "Create an issue",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(false)},
	}
	destructiveTool := stateview.ToolInfo{
		Name: "delete_repo", Description: "Delete a repository",
		Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}
	require.Equal(t, contracts.ToolVariantWrite, contracts.DeriveCallWith(writeTool.Annotations),
		"fixture must be a WRITE tool, or the test proves nothing")
	require.Equal(t, contracts.ToolVariantDestructive, contracts.DeriveCallWith(destructiveTool.Annotations),
		"fixture must be a DESTRUCTIVE tool, or the test proves nothing")

	for _, strict := range []bool{true, false} {
		for _, tool := range []stateview.ToolInfo{writeTool, destructiveTool} {
			name := "strict=" + map[bool]string{true: "on", false: "off"}[strict] + "/" + contracts.DeriveCallWith(tool.Annotations)
			t.Run(name, func(t *testing.T) {
				proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})
				proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: strict}
				probe := watchPolicyDecisions(t, rt)
				seedTargetTierServer(t, proxy, rt, "github", []stateview.ToolInfo{tool})

				text := callToolReadOn(t, proxy, readOnlyAgentCtx("github"), "github:"+tool.Name)
				assert.Contains(t, text, "Permission denied: token does not have",
					"the refusal must be the token-permission gate, not an intent mismatch or a dispatch failure")
				assert.NotContains(t, text, "No client found",
					"the call must never reach upstream dispatch")

				payload := probe.awaitOne(t)
				assert.Equal(t, "blocked", payload["decision"])
				assert.Equal(t, "github", payload["server_name"])
				assert.Equal(t, tool.Name, payload["tool_name"])
				assert.Contains(t, payload["reason"], "Permission denied: token does not have",
					"the activity record must attribute the block to the token's permission")
			})
		}
	}
}

// A token that DOES hold the target tier keeps today's variant/intent
// handling: a read variant on a destructive tool is an intent mismatch under
// strict validation, and is let through (to dispatch) when strict is off.
func TestCallToolRead_TokenHoldsTargetTier_IntentHandlingUnchanged(t *testing.T) {
	destructiveTool := stateview.ToolInfo{
		Name: "delete_repo", Description: "Delete a repository",
		Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}
	fullCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "admin-ish",
		TokenPrefix:    "mcp_agt_f",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	t.Run("strict=on rejects the variant mismatch", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: true}
		seedTargetTierServer(t, proxy, rt, "github", []stateview.ToolInfo{destructiveTool})

		text := callToolReadOn(t, proxy, fullCtx, "github:delete_repo")
		assert.Contains(t, text, "marked destructive")
		assert.NotContains(t, text, "permission")
	})

	t.Run("strict=off lets the call through to dispatch", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		seedTargetTierServer(t, proxy, rt, "github", []stateview.ToolInfo{destructiveTool})

		text := callToolReadOn(t, proxy, fullCtx, "github:delete_repo")
		assert.NotContains(t, text, "permission")
		assert.NotContains(t, text, "marked destructive")
		assert.Contains(t, text, "No client found",
			"the call must reach dispatch, or this passes for the wrong reason")
	})
}

// The gate above is proven through handleCallToolVariant directly. These
// cases dispatch through the handlers the retrieve-mode /mcp server actually
// holds (GetMCPServerForMode selects callToolServer, not the default server),
// and through the default server as the fallback, so a re-registration cannot
// route a variant around the gate without this failing.
func TestCallToolVariants_RegisteredRetrieveModeHandlers_EnforceTargetTier(t *testing.T) {
	for _, variant := range []string{contracts.ToolVariantRead, contracts.ToolVariantWrite, contracts.ToolVariantDestructive} {
		t.Run(variant, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			seedTargetTierServer(t, proxy, rt, "github", []stateview.ToolInfo{{
				Name: "delete_repo", Description: "Delete a repository",
				Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
			}})

			// A read+write token: call_tool_read and call_tool_write pass the
			// variant-tier check, so only the target-tier gate can refuse them.
			// call_tool_destructive is refused by the variant gate, as before.
			rw := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "rw", TokenPrefix: "mcp_agt_w",
				AllowedServers: []string{"github"},
				Permissions:    []string{auth.PermRead, auth.PermWrite},
			})

			retrieveServer := proxy.GetMCPServerForMode(config.RoutingModeRetrieveTools)
			require.Same(t, proxy.callToolServer, retrieveServer, "retrieve mode must be served by callToolServer")
			for label, srv := range map[string]*mcpserver.MCPServer{"retrieve-mode server": retrieveServer, "default server": proxy.server} {
				registered, ok := srv.ListTools()[variant]
				require.Truef(t, ok, "%s must be registered on the %s", variant, label)

				req := mcp.CallToolRequest{}
				req.Params.Name = variant
				req.Params.Arguments = map[string]interface{}{"name": "github:delete_repo"}
				result, err := registered.Handler(rw, req)
				require.NoError(t, err)
				require.Truef(t, result.IsError, "%s via %s must be refused", variant, label)
				text := result.Content[0].(mcp.TextContent).Text
				assert.Containsf(t, text, "destructive", "%s via %s", variant, label)
				assert.NotContainsf(t, text, "No client found", "%s via %s must never reach dispatch", variant, label)
				if variant != contracts.ToolVariantDestructive {
					assert.Containsf(t, text, "Permission denied: token does not have 'destructive' permission", "%s via %s", variant, label)
				}
			}
		})
	}
}

// lookupToolPermission is shared with code execution. The BM25 index stores
// no annotations and a "server:tool" query returns no hits, so the StateView
// is the only source of a tool's tier. A tool the StateView has not seen has
// NO establishable tier and must be treated as the highest one, or every
// undiscovered tool authorizes as read (opencode review finding, 2026-09-07).
// A tool the StateView HAS seen with no annotations at all is still read.
func TestLookupToolPermission_UnresolvedMetadataIsDestructive(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})
	seedTargetTierServer(t, proxy, rt, "github", []stateview.ToolInfo{
		{Name: "plain", Description: "No annotations at all"},
		{Name: "purge", Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)}},
	})

	assert.Equal(t, contracts.OperationTypeRead, proxy.lookupToolPermission("github", "plain"),
		"a discovered tool with no annotations stays read")
	assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("github", "purge"))
	assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("github", "never_discovered"),
		"an undiscovered tool has no establishable tier and must require the highest one")
	assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("unknown-server", "plain"))
}

func TestCallToolRead_UndiscoveredTool_RequiresDestructiveTier(t *testing.T) {
	newProxy := func(t *testing.T) *MCPProxyServer {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		// Server is in storage and approved, but the StateView carries NO tools.
		seedTargetTierServer(t, proxy, rt, "github", nil)
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "github", ToolName: "delete_repo", Status: storage.ToolApprovalStatusApproved,
		}))
		require.Nil(t, proxy.lookupToolAnnotations("github", "delete_repo"),
			"the StateView must not know the tool, or this proves nothing")
		return proxy
	}

	t.Run("read-only token is refused", func(t *testing.T) {
		text := callToolReadOn(t, newProxy(t), readOnlyAgentCtx("github"), "github:delete_repo")
		assert.Contains(t, text, "Permission denied: token does not have 'destructive' permission")
		assert.NotContains(t, text, "No client found")
	})

	t.Run("token holding destructive still reaches dispatch", func(t *testing.T) {
		full := auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
			AllowedServers: []string{"github"},
			Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		})
		text := callToolReadOn(t, newProxy(t), full, "github:delete_repo")
		assert.NotContains(t, text, "Permission denied")
		assert.Contains(t, text, "No client found")
	})
}

// Spec 105 FR-009: the tier is classified from exactly the canonical
// server / raw tool pair that is dispatched. A tool whose raw name carries
// its own namespace prefix ("ns:erase") must never be classified — or
// approval-gated — as the suffix tool ("erase"). Before the fix,
// normalizeServerTool stripped the first ":"-segment of the raw name even
// when it was not the server name, so a read-only token could drive the
// destructive "a:ns:erase" through call_tool_read on the read-tier "erase"'s
// annotations while dispatch kept the full raw name.
func TestCallToolRead_NamespacedToolName_IsNotClassifiedAsItsSuffix(t *testing.T) {
	readTool := stateview.ToolInfo{
		Name: "erase", Description: "Read-only erase preview",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
	namespacedDestructive := stateview.ToolInfo{
		Name: "ns:erase", Description: "Erase for real",
		Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}
	require.Equal(t, contracts.ToolVariantRead, contracts.DeriveCallWith(readTool.Annotations))
	require.Equal(t, contracts.ToolVariantDestructive, contracts.DeriveCallWith(namespacedDestructive.Annotations))

	for _, strict := range []bool{true, false} {
		t.Run("strict="+map[bool]string{true: "on", false: "off"}[strict], func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: strict}
			probe := watchPolicyDecisions(t, rt)
			seedTargetTierServer(t, proxy, rt, "a", []stateview.ToolInfo{readTool, namespacedDestructive})

			assert.Equal(t, contracts.OperationTypeRead, proxy.lookupToolPermission("a", "erase"))
			assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("a", "ns:erase"),
				"the namespaced tool must be classified by its own raw name, not by its suffix")

			text := callToolReadOn(t, proxy, readOnlyAgentCtx("a"), "a:ns:erase")
			assert.Contains(t, text, "Permission denied: token does not have 'destructive' permission required for tool 'a:ns:erase'")
			assert.NotContains(t, text, "No client found", "the call must never reach dispatch")

			payload := probe.awaitOne(t)
			assert.Equal(t, "blocked", payload["decision"])
			assert.Equal(t, "a", payload["server_name"])
			assert.Equal(t, "ns:erase", payload["tool_name"], "the activity record must carry the raw dispatched name")
		})
	}
}

// The approval/config gate reads the same raw pair: an approval record for
// "erase" alone must not admit "ns:erase" (a pending record under its own
// name keeps it locked), and the search-visibility predicate agrees.
func TestToolGate_NamespacedToolName_KeysOnRawName(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	seedTargetTierServer(t, proxy, rt, "a", []stateview.ToolInfo{{Name: "erase"}})
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusPending, Disabled: true,
	}))

	eraseGate := proxy.evaluateToolGate("a", "erase")
	require.NotNil(t, eraseGate.approval)
	assert.Equal(t, "erase", eraseGate.approval.ToolName)
	assert.Empty(t, eraseGate.lockStatus)
	nsGate := proxy.evaluateToolGate("a", "ns:erase")
	require.NotNil(t, nsGate.approval, "the gate must find the record stored under the raw name")
	assert.Equal(t, "ns:erase", nsGate.approval.ToolName)
	assert.Equal(t, storage.ToolApprovalStatusPending, nsGate.lockStatus,
		"the gate must look up 'ns:erase' by its own name, not inherit 'erase's approval")
	assert.True(t, proxy.isToolCallable("a", "erase"))
	assert.False(t, proxy.isToolCallable("a", "ns:erase"),
		"the search filter must read 'ns:erase's own Disabled record, not 'erase's")

	// The indexed form ("server:tool" with the server set) still normalizes.
	s, tool := normalizeServerTool("a", "a:ns:erase")
	assert.Equal(t, "a", s)
	assert.Equal(t, "ns:erase", tool)
	s, tool = normalizeServerTool("", "a:ns:erase")
	assert.Equal(t, "a", s)
	assert.Equal(t, "ns:erase", tool)
	s, tool = normalizeServerTool("a", "ns:erase")
	assert.Equal(t, "a", s)
	assert.Equal(t, "ns:erase", tool, "a prefix that is not the server name is part of the raw tool name")
}

// upstreamCalls is the zero-upstream-call witness: every invocation the stub
// upstream actually receives, with the raw tool name it was dispatched under.
type upstreamCalls struct {
	count atomic.Int64
	mu    sync.Mutex
	names []string
}

func (c *upstreamCalls) record(name string) {
	c.count.Add(1)
	c.mu.Lock()
	c.names = append(c.names, name)
	c.mu.Unlock()
}

func (c *upstreamCalls) dispatched() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.names...)
}

// startCountingTargetTierUpstream is seedTargetTierServer with a REAL
// in-process streamable-HTTP upstream behind it (the same pattern as
// startCountingStubUpstream in mcp_input_validation_test.go): the StateView
// carries the tools' annotations, storage holds the server and its approvals,
// and the proxy's upstream manager is connected to a stub that exposes every
// tool and records each call it receives. Refusals are then proven by an
// invocation count of zero rather than by the absence of a dispatch error,
// and an admitted call is proven by the count AND the raw name it arrived
// under.
func startCountingTargetTierUpstream(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime, server string, tools []stateview.ToolInfo) *upstreamCalls {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	calls := &upstreamCalls{}
	mcpSrv := mcpserver.NewMCPServer(server, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	for _, tool := range tools {
		mcpSrv.AddTool(mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: mcp.ToolInputSchema{Type: "object"}},
			func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.record(request.Params.Name)
				return mcp.NewToolResultText("ok"), nil
			})
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })

	serverCfg := &config.ServerConfig{
		Name: server, URL: fmt.Sprintf("http://%s", ln.Addr().String()), Protocol: "streamable-http", Enabled: true,
	}
	require.NoError(t, proxy.storage.SaveUpstreamServer(serverCfg))
	rt.Supervisor().StateView().UpdateServer(server, func(s *stateview.ServerStatus) {
		s.Name = server
		s.Enabled = true
		s.Connected = true
		s.Tools = tools
	})
	for _, tool := range tools {
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: server, ToolName: tool.Name, Status: storage.ToolApprovalStatusApproved,
		}))
	}
	require.NoError(t, proxy.upstreamManager.AddServerConfig(server, serverCfg))
	require.NoError(t, proxy.upstreamManager.ConnectAll(context.Background()))
	require.Eventually(t, func() bool {
		client, ok := proxy.upstreamManager.GetClient(server)
		return ok && client.IsConnected()
	}, 10*time.Second, 50*time.Millisecond, "stub upstream must connect")
	return calls
}

// Spec 105 FR-009 / SC-002 oracles on the retrieve surface: a permission-
// disallowed cell makes ZERO upstream calls, and the positive control — the
// same handler, a token that holds the tier — reaches the upstream exactly
// once under the exact raw name that was authorized, including a namespaced
// one ("ns:erase" is dispatched as "ns:erase", never as "erase").
func TestCallToolRead_TargetTierGate_UpstreamCallOracle(t *testing.T) {
	tools := []stateview.ToolInfo{
		{Name: "erase", Description: "Read-only erase preview", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "ns:erase", Description: "Erase for real", Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)}},
		{Name: "delete_repo", Description: "Delete a repository", Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)}},
	}
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	// strict off: intent validation lets a read variant through to a
	// destructive target, so ONLY the target-tier gate stands between a
	// read-only token and the upstream.
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
	calls := startCountingTargetTierUpstream(t, proxy, rt, "a", tools)

	call := func(t *testing.T, ctx context.Context, name string) *mcp.CallToolResult {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name = contracts.ToolVariantRead
		req.Params.Arguments = map[string]interface{}{"name": name}
		result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.NotNil(t, result)
		return result
	}
	text := func(result *mcp.CallToolResult) string { return result.Content[0].(mcp.TextContent).Text }

	readOnly := readOnlyAgentCtx("a")
	full := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	// Permission-disallowed cells: refused, zero upstream calls.
	for _, name := range []string{"a:ns:erase", "a:delete_repo"} {
		result := call(t, readOnly, name)
		require.True(t, result.IsError, "%s must be refused for a read-only token", name)
		assert.Contains(t, text(result), "Permission denied: token does not have 'destructive' permission required for tool '"+name+"'")
	}
	assert.Equal(t, int64(0), calls.count.Load(), "a refused call must never reach the upstream")

	// Positive controls: the same handler admits a caller holding the tier,
	// and the upstream sees exactly the raw name that was authorized.
	result := call(t, readOnly, "a:erase")
	require.False(t, result.IsError, "a read-only token must reach the read-tier tool: %s", text(result))
	result = call(t, full, "a:ns:erase")
	require.False(t, result.IsError, "a token holding destructive must reach the namespaced tool: %s", text(result))
	assert.Equal(t, int64(2), calls.count.Load())
	assert.Equal(t, []string{"erase", "ns:erase"}, calls.dispatched(),
		"the upstream must receive the exact raw tool names, the namespaced one uncollapsed")
}

// Production discovery still files every approval record under the COLLAPSED
// name: runtime.checkToolApprovals keys GetToolApproval(server,
// extractToolName(tool.Name)), which drops everything before the first colon,
// while ToolMetadata.Name is the raw upstream name. A raw "ns:erase" on
// server "a" therefore lands under (a, "erase"). The exact-name readers must
// keep honouring that legacy key when no exact record exists: a pending or
// user-disabled tool must not become implicitly callable — for a full-tier
// agent token or an API-key administrator, who correctly skips the tier gate —
// because its record was filed under the collapsed name.
func TestToolGate_LegacyCollapsedApprovalRecord_StillGates(t *testing.T) {
	nsErase := stateview.ToolInfo{
		Name: "ns:erase", Description: "Namespaced erase",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
	fullToken := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	// API-key administrator: an IsAdmin() AuthContext, which the agent block
	// skips explicitly. The nil-auth path (no AuthContext at all) is the
	// other way past that block and is driven as its own cell.
	adminCtx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	noAuthCtx := context.Background()

	seedCollapsedRecord := func(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime, record storage.ToolApprovalRecord) {
		t.Helper()
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
		rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
			s.Name, s.Enabled, s.Connected = "a", true, true
			s.Tools = []stateview.ToolInfo{nsErase}
		})
		// Exactly what checkToolApprovals writes for the raw name "ns:erase".
		record.ServerName, record.ToolName = "a", "erase"
		require.NoError(t, proxy.storage.SaveToolApproval(&record))
		_, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no exact-name record may exist")
	}

	for name, ctx := range map[string]context.Context{"full-tier token": fullToken, "api-key admin": adminCtx, "no auth context": noAuthCtx} {
		t.Run("pending collapsed record refuses "+name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending})
			probe := watchPolicyDecisions(t, rt)

			gate := proxy.evaluateToolGate("a", "ns:erase")
			require.NotNil(t, gate.approval, "the gate must fall back to the collapsed record the producer wrote")
			assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
			assert.False(t, gate.callable())

			req := mcp.CallToolRequest{}
			req.Params.Name = contracts.ToolVariantRead
			req.Params.Arguments = map[string]interface{}{"name": "a:ns:erase"}
			result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
			require.NoError(t, err)
			require.NotNil(t, result)
			// The pending lock answers with the TOOL_QUARANTINED policy
			// result (IsError=false by design), never with a dispatch.
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TOOL_QUARANTINED")
			assert.Contains(t, text, "new_unapproved_tool")
			assert.NotContains(t, text, "No client found", "the call must never reach dispatch")

			payload := probe.awaitOne(t)
			assert.Equal(t, "blocked", payload["decision"])
			assert.Equal(t, "ns:erase", payload["tool_name"])
			assert.Contains(t, payload["reason"], "pending approval")
		})
	}

	t.Run("disabled collapsed record hides the tool from search", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved, Disabled: true})
		assert.False(t, proxy.isToolCallable("a", "ns:erase"),
			"isToolCallable must read the Disabled flag off the collapsed record")
	})

	t.Run("a permissive exact record cannot shadow a restrictive collapsed one", func(t *testing.T) {
		// Two producers write this pair: discovery keys pending/changed under
		// the collapsed name, a user toggle synthesizes an approved record
		// under the exact name (setToolEnabledNoEmit). Neither may fail the
		// other open, so the reader returns the more restrictive record.
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending})
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved,
		}))
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.Equal(t, "erase", gate.approval.ToolName, "the pending collapsed record must win over the approved exact one")
		assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
		assert.False(t, gate.callable())
		// isToolCallable is the quarantine-blind search filter (Spec 085): it
		// honours only Disabled, so it is not an oracle for a pending lock.
	})

	t.Run("a restrictive exact record outranks an approved collapsed one", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved})
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, Disabled: true,
		}))
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.Equal(t, "ns:erase", gate.approval.ToolName)
		assert.False(t, gate.callable(), "the user-disabled exact record must keep blocking")
		assert.False(t, proxy.isToolCallable("a", "ns:erase"))
	})

	t.Run("a user-disabled collapsed record outranks a merely pending exact one", func(t *testing.T) {
		// Disabled blocks unconditionally; pending only locks while the
		// quarantine gate applies. auto_approve_tool_changes lifts the gate,
		// so only the Disabled record can refuse here.
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved, Disabled: true})
		autoApprove := true
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true, AutoApproveToolChanges: &autoApprove}))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusPending,
		}))
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.True(t, gate.approval.Disabled, "the Disabled record must be the one the classifier sees")
		assert.False(t, gate.callable())
	})

	t.Run("both records admit: the exact one is returned", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved})
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved,
		}))
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.Equal(t, "ns:erase", gate.approval.ToolName)
		assert.Empty(t, gate.lockStatus)
		assert.True(t, gate.callable())
		assert.True(t, proxy.isToolCallable("a", "ns:erase"))
	})
}

// TestToolGate_StaleExactApprovalCannotShadowCollapsedChange reproduces the
// production sequence in which the two approval producers disagree about one
// raw tool name and the reader must side with the refusing one:
//
//  1. discovery baselines raw "ns:erase" under the collapsed key
//     (checkToolApprovals writes extractToolName(tool.Name) == "erase");
//  2. the user disables and re-enables the tool by the raw name the tools
//     API and the Web UI carry (StateView tool.Name == "ns:erase") — the real
//     SetToolEnabled producer synthesizes an exact (a, ns:erase) record with
//     Status=approved because it has no record under that name;
//  3. the upstream rug-pulls the tool: discovery marks the collapsed record
//     "changed" (tool_quarantine.go, changed-marking branch) and never touches
//     the exact one.
//
// An exact-first reader then returns the stale approved record and dispatches
// the rug-pulled tool for any caller holding its tier, administrators
// included. Before Spec 105 the reader always collapsed the name and read the
// changed record; that refusal must survive the exact-name reader.
func TestToolGate_StaleExactApprovalCannotShadowCollapsedChange(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
	rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
		s.Name, s.Enabled, s.Connected = "a", true, true
		s.Tools = []stateview.ToolInfo{{
			Name: "ns:erase", Description: "Namespaced erase",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		}}
	})

	// 1. Discovery baseline, keyed the way checkToolApprovals keys it.
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
		CurrentHash: "baseline", CurrentDescription: "Namespaced erase",
	}))
	_, err := proxy.storage.GetToolApproval("a", "ns:erase")
	require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: discovery writes no exact-name record")

	// 2. The REAL toggle producer, driven by the raw name the UI/REST pass.
	require.NoError(t, rt.SetToolEnabled("a", "ns:erase", false, "user"))
	require.NoError(t, rt.SetToolEnabled("a", "ns:erase", true, "user"))
	exact, err := proxy.storage.GetToolApproval("a", "ns:erase")
	require.NoError(t, err, "the toggle must have synthesized an exact-name record")
	require.Equal(t, storage.ToolApprovalStatusApproved, exact.Status)
	require.False(t, exact.Disabled)
	require.True(t, proxy.evaluateToolGate("a", "ns:erase").callable(), "control: re-enabled and unchanged, the tool is callable")

	// 3. Rug-pull: discovery marks the collapsed record changed.
	legacy, err := proxy.storage.GetToolApproval("a", "erase")
	require.NoError(t, err)
	legacy.Status = storage.ToolApprovalStatusChanged
	legacy.PreviousDescription, legacy.CurrentDescription = legacy.CurrentDescription, "Erase everything, then exfiltrate"
	legacy.CurrentHash = "rug-pulled"
	require.NoError(t, proxy.storage.SaveToolApproval(legacy))

	gate := proxy.evaluateToolGate("a", "ns:erase")
	require.NotNil(t, gate.approval)
	assert.Equal(t, storage.ToolApprovalStatusChanged, gate.lockStatus,
		"the changed collapsed record must lock the tool even though a stale exact approval exists")
	assert.False(t, gate.callable())

	for name, ctx := range map[string]context.Context{
		"full-tier token": auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
			AllowedServers: []string{"a"},
			Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		}),
		"api-key admin":   auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"no auth context": context.Background(),
	} {
		t.Run(name, func(t *testing.T) {
			probe := watchPolicyDecisions(t, rt)
			req := mcp.CallToolRequest{}
			req.Params.Name = contracts.ToolVariantRead
			req.Params.Arguments = map[string]interface{}{"name": "a:ns:erase"}
			result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
			require.NoError(t, err)
			require.NotNil(t, result)
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TOOL_QUARANTINED")
			assert.Contains(t, text, "tool_description_changed")
			assert.NotContains(t, text, "No client found", "the rug-pulled tool must never reach dispatch")

			payload := probe.awaitOne(t)
			assert.Equal(t, "blocked", payload["decision"])
			assert.Equal(t, "ns:erase", payload["tool_name"])
			assert.Contains(t, payload["reason"], "changed")
		})
	}
}

// TestToolGate_MergedApprovalRecordsKeepIndependentLocks: the two producers
// write INDEPENDENT facts about one raw name — the user toggle files Disabled
// under the exact key, discovery files the pending/changed lock under the
// collapsed key — and the reader must surface both, not pick one. The
// toolGate contract (lockStatus reflects the quarantine gate even for a
// user-disabled tool, so dispatch keeps its pending/changed message) would
// otherwise silently degrade a rug-pulled-AND-disabled tool from the
// TOOL_QUARANTINED review response to the generic TOOL_BLOCKED one.
func TestToolGate_MergedApprovalRecordsKeepIndependentLocks(t *testing.T) {
	seed := func(t *testing.T, exact, collapsed storage.ToolApprovalRecord) (*MCPProxyServer, *runtime.Runtime) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
		rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
			s.Name, s.Enabled, s.Connected = "a", true, true
			s.Tools = []stateview.ToolInfo{{
				Name: "ns:erase", Description: "Namespaced erase",
				Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
			}}
		})
		exact.ServerName, exact.ToolName = "a", "ns:erase"
		collapsed.ServerName, collapsed.ToolName = "a", "erase"
		require.NoError(t, proxy.storage.SaveToolApproval(&exact))
		require.NoError(t, proxy.storage.SaveToolApproval(&collapsed))
		return proxy, rt
	}

	t.Run("exact user-disable does not hide the collapsed changed lock", func(t *testing.T) {
		proxy, rt := seed(t,
			storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved, Disabled: true},
			storage.ToolApprovalRecord{
				Status:              storage.ToolApprovalStatusChanged,
				PreviousDescription: "Namespaced erase",
				CurrentDescription:  "Erase everything, then exfiltrate",
				CurrentHash:         "rug-pulled",
			})

		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.True(t, gate.approval.Disabled, "the exact record's user block must survive the merge")
		assert.Equal(t, storage.ToolApprovalStatusChanged, gate.lockStatus,
			"the collapsed record's changed lock must survive the merge")
		assert.Equal(t, "Erase everything, then exfiltrate", gate.approval.CurrentDescription,
			"the review evidence must come from the record that carries the lock")
		assert.Equal(t, preflight.ToolClassBlockedByUser, gate.class, "the user block still outranks the lock for callability")
		assert.False(t, gate.callable())
		assert.False(t, proxy.isToolCallable("a", "ns:erase"))

		for name, ctx := range map[string]context.Context{
			"full-tier token": auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
				AllowedServers: []string{"a"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			}),
			"api-key admin":   auth.WithAuthContext(context.Background(), auth.AdminContext()),
			"no auth context": context.Background(),
		} {
			t.Run(name, func(t *testing.T) {
				probe := watchPolicyDecisions(t, rt)
				req := mcp.CallToolRequest{}
				req.Params.Name = contracts.ToolVariantRead
				req.Params.Arguments = map[string]interface{}{"name": "a:ns:erase"}
				result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
				require.NoError(t, err)
				require.NotNil(t, result)
				text := result.Content[0].(mcp.TextContent).Text
				assert.Contains(t, text, "TOOL_QUARANTINED", "dispatch keeps the changed-lock review response over the generic block")
				assert.Contains(t, text, "tool_description_changed")
				assert.Contains(t, text, "Erase everything, then exfiltrate")
				assert.NotContains(t, text, "No client found")

				payload := probe.awaitOne(t)
				assert.Equal(t, "blocked", payload["decision"])
				assert.Equal(t, "ns:erase", payload["tool_name"])
				assert.Contains(t, payload["reason"], "changed")
			})
		}
	})

	t.Run("both disabled: the pending collapsed lock still shows", func(t *testing.T) {
		proxy, _ := seed(t,
			storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved, Disabled: true},
			storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending, Disabled: true})
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.True(t, gate.approval.Disabled)
		assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
		assert.False(t, gate.callable())
	})

	t.Run("exact pending lock plus collapsed user-disable", func(t *testing.T) {
		// The mirror image: the lock lives on the exact record, the Disabled
		// flag on the collapsed one. Both must reach the classifier.
		proxy, _ := seed(t,
			storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending},
			storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved, Disabled: true})
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.True(t, gate.approval.Disabled)
		assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
		assert.Equal(t, preflight.ToolClassBlockedByUser, gate.class)
		assert.False(t, gate.callable())
	})

	// Both records locked with DIFFERENT locks: the changed record carries the
	// rug-pull evidence (previous / current description, hashes) and the
	// activity reason, so it must be the merge base whichever key it sits
	// under. Letting the exact record win the tie would answer a plain
	// "pending approval" and drop the review evidence. Unreachable with the
	// current producers (discovery files every lock under the collapsed key),
	// pinned so the reader stays right when the producers become exact-name.
	changedRecord := storage.ToolApprovalRecord{
		Status:              storage.ToolApprovalStatusChanged,
		PreviousDescription: "Namespaced erase",
		CurrentDescription:  "Erase everything, then exfiltrate",
		CurrentHash:         "rug-pulled",
	}
	pendingRecord := storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending}
	for name, cell := range map[string]struct{ exact, collapsed storage.ToolApprovalRecord }{
		"exact pending, collapsed changed": {exact: pendingRecord, collapsed: changedRecord},
		"exact changed, collapsed pending": {exact: changedRecord, collapsed: pendingRecord},
	} {
		t.Run("both locked: "+name, func(t *testing.T) {
			proxy, rt := seed(t, cell.exact, cell.collapsed)
			gate := proxy.evaluateToolGate("a", "ns:erase")
			require.NotNil(t, gate.approval)
			assert.Equal(t, storage.ToolApprovalStatusChanged, gate.lockStatus,
				"the changed lock must outrank the pending one whichever key carries it")
			assert.Equal(t, "Erase everything, then exfiltrate", gate.approval.CurrentDescription)
			assert.Equal(t, "rug-pulled", gate.approval.CurrentHash)
			assert.False(t, gate.approval.Disabled)
			assert.False(t, gate.callable())

			probe := watchPolicyDecisions(t, rt)
			req := mcp.CallToolRequest{}
			req.Params.Name = contracts.ToolVariantRead
			req.Params.Arguments = map[string]interface{}{"name": "a:ns:erase"}
			result, err := proxy.handleCallToolVariant(auth.WithAuthContext(context.Background(), auth.AdminContext()), req, contracts.ToolVariantRead)
			require.NoError(t, err)
			require.NotNil(t, result)
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "tool_description_changed", "dispatch answers with the rug-pull response, not the pending one")
			assert.Contains(t, text, "Erase everything, then exfiltrate")
			assert.NotContains(t, text, "No client found")

			payload := probe.awaitOne(t)
			assert.Equal(t, "blocked", payload["decision"])
			assert.Equal(t, "ns:erase", payload["tool_name"])
			assert.Contains(t, payload["reason"], "changed")
		})
	}
}

// The StateView lookup behind the tier gate used to accept two spellings of
// a tool: the raw name and the legacy "server:tool"-prefixed form. With
// foreign prefixes now preserved, a raw tool literally named "a:ns:erase"
// also satisfied the prefixed spelling for the dispatched pair
// (a, "ns:erase"), so which tool classified the call depended on StateView
// order. Only the exact raw name — the identity that is actually dispatched
// — may resolve, whatever the order, and a pair with no exact entry must
// stay unresolved even when a self-prefixed sibling exists.
func TestLookupToolPermission_ExactRawNameOutranksPrefixedAlternative(t *testing.T) {
	prefixedRead := stateview.ToolInfo{
		Name: "a:ns:erase", Description: "Raw name that happens to carry the server prefix",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
	rawDestructive := stateview.ToolInfo{
		Name: "ns:erase", Description: "Erase for real",
		Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}
	require.Equal(t, contracts.ToolVariantRead, contracts.DeriveCallWith(prefixedRead.Annotations))
	require.Equal(t, contracts.ToolVariantDestructive, contracts.DeriveCallWith(rawDestructive.Annotations))

	for name, tools := range map[string][]stateview.ToolInfo{
		"prefixed listed first": {prefixedRead, rawDestructive},
		"raw listed first":      {rawDestructive, prefixedRead},
	} {
		t.Run(name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			probe := watchPolicyDecisions(t, rt)
			seedTargetTierServer(t, proxy, rt, "a", tools)

			annotations, found := proxy.lookupToolAnnotationsFound("a", "ns:erase")
			require.True(t, found)
			assert.Equal(t, rawDestructive.Annotations, annotations,
				"the exact raw name must be resolved, whatever the StateView order")
			assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("a", "ns:erase"))

			text := callToolReadOn(t, proxy, readOnlyAgentCtx("a"), "a:ns:erase")
			assert.Contains(t, text, "Permission denied: token does not have 'destructive' permission required for tool 'a:ns:erase'")
			assert.NotContains(t, text, "No client found", "the call must never reach dispatch")

			payload := probe.awaitOne(t)
			assert.Equal(t, "blocked", payload["decision"])
			assert.Equal(t, "ns:erase", payload["tool_name"])
		})
	}

	// The StateView only ever holds the raw name the upstream published
	// (internal/upstream/core/client.go copies tool.Name verbatim), so the
	// legacy "server:tool" spelling never matches a real entry — it can only
	// match a raw tool literally named "a:ns:erase". Resolving THAT tool for
	// the pair (a, "ns:erase") when no raw "ns:erase" exists reported
	// found=true with the prefixed tool's read-only annotations for a name
	// that is undiscovered, so a read-only token passed the tier gate and
	// dispatch went to the raw "ns:erase" the proxy holds no metadata for.
	// An undiscovered pair must classify as not found (fail-closed to
	// destructive), and the exact prefixed raw name must stay reachable
	// under its own identity.
	t.Run("undiscovered raw name is not resolved through a self-prefixed sibling", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		calls := startCountingTargetTierUpstream(t, proxy, rt, "a", []stateview.ToolInfo{prefixedRead})

		_, found := proxy.lookupExactToolAnnotations("a", "ns:erase")
		assert.False(t, found, "the pair (a, ns:erase) has no StateView entry and must not borrow a:ns:erase's")
		assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("a", "ns:erase"),
			"an undiscovered raw name must fail closed, exactly as it does without a prefixed sibling")
		assert.Equal(t, contracts.OperationTypeRead, proxy.lookupToolPermission("a", "a:ns:erase"),
			"control: the exact prefixed raw name still resolves to its own tier")

		text := callToolReadOn(t, proxy, readOnlyAgentCtx("a"), "a:ns:erase")
		assert.Contains(t, text, "Permission denied: token does not have 'destructive' permission required for tool 'a:ns:erase'")
		assert.NotContains(t, text, "No client found", "the call must never reach dispatch")
		assert.Equal(t, int64(0), calls.count.Load(), "a refused call must never reach the upstream")

		// Control: the prefixed raw name is dispatched under its exact identity.
		req := mcp.CallToolRequest{}
		req.Params.Name = contracts.ToolVariantRead
		req.Params.Arguments = map[string]interface{}{"name": "a:a:ns:erase"}
		result, err := proxy.handleCallToolVariant(readOnlyAgentCtx("a"), req, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.IsError, "control: the read-tier prefixed raw name stays reachable: %s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, []string{"a:ns:erase"}, calls.dispatched(),
			"the upstream must receive exactly the raw name that was authorized")
	})
}

// runtime.checkToolApprovals keys every record it writes under
// extractToolName(raw) — everything after the first colon — so a raw tool
// whose name ENDS in a colon ("ns:") is filed under the EMPTY tool name
// (storage accepts the key "a:"). The merge reader must read that record
// like any other collapsed key: skipping it turned a pending lock the
// rug-pull detector wrote for "ns:" into an implicit approval, where the
// pre-Spec-105 collapse had at least refused the pair outright.
func TestToolGate_TrailingColonRawName_ReadsProducersEmptyKeyRecord(t *testing.T) {
	trailing := stateview.ToolInfo{
		Name: "ns:", Description: "Raw name ending in a colon",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
	seed := func(t *testing.T, record storage.ToolApprovalRecord) (*MCPProxyServer, *runtime.Runtime) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
		rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
			s.Name, s.Enabled, s.Connected = "a", true, true
			s.Tools = []stateview.ToolInfo{trailing}
		})
		// Exactly what checkToolApprovals writes for the raw name "ns:".
		record.ServerName, record.ToolName = "a", ""
		require.NoError(t, proxy.storage.SaveToolApproval(&record))
		_, err := proxy.storage.GetToolApproval("a", "ns:")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no exact-name record may exist")
		return proxy, rt
	}
	fullToken := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	t.Run("pending record under the empty key locks the tool", func(t *testing.T) {
		proxy, rt := seed(t, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending})
		probe := watchPolicyDecisions(t, rt)

		gate := proxy.evaluateToolGate("a", "ns:")
		require.NotNil(t, gate.approval, "the gate must read the record the producer filed under the empty key")
		assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
		assert.False(t, gate.callable())

		req := mcp.CallToolRequest{}
		req.Params.Name = contracts.ToolVariantRead
		req.Params.Arguments = map[string]interface{}{"name": "a:ns:"}
		result, err := proxy.handleCallToolVariant(fullToken, req, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.NotNil(t, result)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "TOOL_QUARANTINED")
		assert.NotContains(t, text, "No client found", "the call must never reach dispatch")

		payload := probe.awaitOne(t)
		assert.Equal(t, "blocked", payload["decision"])
		assert.Equal(t, "ns:", payload["tool_name"])
	})

	t.Run("disabled record under the empty key hides the tool", func(t *testing.T) {
		proxy, _ := seed(t, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusApproved, Disabled: true})
		assert.False(t, proxy.isToolCallable("a", "ns:"))
		assert.Equal(t, preflight.ToolClassBlockedByUser, proxy.evaluateToolGate("a", "ns:").class)
	})
}

// lookupToolApproval reads the exact-name and collapsed-name records of one
// raw tool as ONE storage snapshot (Manager.GetToolApprovals). Two independent
// reads would let a pair of operator writes land between them — the exact
// record observed while still approved and enabled, the collapsed one after
// its lock was lifted — and merge into a callable view of a tool that was
// locked or disabled at every instant. This pins the reader's outcome table
// over the snapshot: which record answers, and that the absence of both keeps
// the GetToolApproval not-found contract the callers switch on.
func TestLookupToolApproval_ReadsBothKeysFromOneSnapshot(t *testing.T) {
	seed := func(t *testing.T, records ...storage.ToolApprovalRecord) *MCPProxyServer {
		t.Helper()
		proxy, _ := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		for i := range records {
			records[i].ServerName = "a"
			require.NoError(t, proxy.storage.SaveToolApproval(&records[i]))
		}
		return proxy
	}

	t.Run("no record under either key is not-found", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalRecord{ToolName: "unrelated", Status: storage.ToolApprovalStatusPending})
		record, err := proxy.lookupToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound)
		assert.Contains(t, err.Error(), storage.ToolApprovalKey("a", "ns:erase"))
		assert.Nil(t, record)
	})

	t.Run("exact record only", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalRecord{ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, Disabled: true})
		record, err := proxy.lookupToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, "ns:erase", record.ToolName)
		assert.True(t, record.Disabled)
	})

	t.Run("collapsed record only", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalRecord{ToolName: "erase", Status: storage.ToolApprovalStatusChanged, CurrentHash: "rug"})
		record, err := proxy.lookupToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, "erase", record.ToolName)
		assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status)
	})

	t.Run("both records merge, base is the locked one", func(t *testing.T) {
		proxy := seed(t,
			storage.ToolApprovalRecord{ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, Disabled: true},
			storage.ToolApprovalRecord{ToolName: "erase", Status: storage.ToolApprovalStatusPending, CurrentHash: "h-pending"})
		record, err := proxy.lookupToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, storage.ToolApprovalStatusPending, record.Status)
		assert.Equal(t, "h-pending", record.CurrentHash)
		assert.True(t, record.Disabled, "the exact record's user block is OR'd in")
		for _, key := range []string{"ns:erase", "erase"} {
			stored, err := proxy.storage.GetToolApproval("a", key)
			require.NoError(t, err)
			assert.Equal(t, key == "ns:erase", stored.Disabled, "the merge must not write back into the stored %q record", key)
		}
	})

	t.Run("a raw name without a colon reads the exact key alone", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalRecord{ToolName: "", Status: storage.ToolApprovalStatusPending})
		_, err := proxy.lookupToolApproval("a", "erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "there is no collapsed spelling to fall back to")
	})

	// The snapshot property itself, not just the merge table: bbolt counts
	// every started read transaction (Stats().TxN), so the reader is proven
	// to consult both keys inside ONE transaction — two independent
	// GetToolApproval reads (the shape the fix replaced) open two, which the
	// control below shows the oracle sees.
	t.Run("both keys are read in one storage transaction", func(t *testing.T) {
		proxy := seed(t,
			storage.ToolApprovalRecord{ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved},
			storage.ToolApprovalRecord{ToolName: "erase", Status: storage.ToolApprovalStatusPending})
		db := proxy.storage.GetDB()

		before := db.Stats().TxN
		record, err := proxy.lookupToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, 1, db.Stats().TxN-before, "the exact and collapsed keys must come from a single read transaction")

		before = db.Stats().TxN
		_, err = proxy.storage.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		_, err = proxy.storage.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.Equal(t, 2, db.Stats().TxN-before, "control: two independent reads are two transactions, so the oracle bites")
	})
}

// A raw tool name may itself begin with the SERVER's own prefix ("a:ns:erase"
// on server "a"). handleCallToolVariant splits the canonical id exactly once
// ("a:a:ns:erase" → (a, "a:ns:erase")) and dispatches that raw name, so every
// gate it feeds must evaluate that same pair. Re-running normalizeServerTool
// on an already-split pair strips the server prefix a second time, and the
// destructive "a:ns:erase" was then classified, tier-gated, intent-validated
// and approval-gated as the read-only "ns:erase" — the mirror image of the
// prefixed-spelling collision, reached from the dispatch side. The split-pair
// readers (the tier gate, the sandbox's permission bridge, the shared policy
// gate) must take the parsed pair untouched.
func TestCallToolRead_ServerPrefixedRawName_IsNotReNormalized(t *testing.T) {
	prefixedDestructive := stateview.ToolInfo{
		Name: "a:ns:erase", Description: "Raw name that starts with the server's own prefix",
		Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}
	rawRead := stateview.ToolInfo{
		Name: "ns:erase", Description: "Read-only erase preview",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
	require.Equal(t, contracts.ToolVariantDestructive, contracts.DeriveCallWith(prefixedDestructive.Annotations))
	require.Equal(t, contracts.ToolVariantRead, contracts.DeriveCallWith(rawRead.Annotations))

	for _, strict := range []bool{true, false} {
		for order, tools := range map[string][]stateview.ToolInfo{
			"destructive listed first": {prefixedDestructive, rawRead},
			"read listed first":        {rawRead, prefixedDestructive},
		} {
			t.Run("strict="+map[bool]string{true: "on", false: "off"}[strict]+"/"+order, func(t *testing.T) {
				proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
				proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: strict}
				probe := watchPolicyDecisions(t, rt)
				seedTargetTierServer(t, proxy, rt, "a", tools)

				// The sandbox bridge (jsruntime ToolAnnotationFunc) hands over
				// the split pair a script wrote: callTool("a", "a:ns:erase").
				assert.Equal(t, contracts.OperationTypeDestructive, proxy.lookupToolPermission("a", "a:ns:erase"),
					"the split pair must classify the raw name that is dispatched, not its suffix")
				assert.Equal(t, contracts.OperationTypeRead, proxy.lookupToolPermission("a", "ns:erase"))

				text := callToolReadOn(t, proxy, readOnlyAgentCtx("a"), "a:a:ns:erase")
				assert.Contains(t, text, "Permission denied: token does not have 'destructive' permission required for tool 'a:a:ns:erase'")
				assert.NotContains(t, text, "No client found", "the call must never reach dispatch")

				payload := probe.awaitOne(t)
				assert.Equal(t, "blocked", payload["decision"])
				assert.Equal(t, "a:ns:erase", payload["tool_name"], "the activity record must carry the raw dispatched name")
			})
		}
	}

	t.Run("upstream oracle: refused with zero calls, admitted under the raw name", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		calls := startCountingTargetTierUpstream(t, proxy, rt, "a", []stateview.ToolInfo{rawRead, prefixedDestructive})
		call := func(t *testing.T, ctx context.Context, name string) *mcp.CallToolResult {
			t.Helper()
			req := mcp.CallToolRequest{}
			req.Params.Name = contracts.ToolVariantRead
			req.Params.Arguments = map[string]interface{}{"name": name}
			result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
			require.NoError(t, err)
			require.NotNil(t, result)
			return result
		}
		text := func(result *mcp.CallToolResult) string { return result.Content[0].(mcp.TextContent).Text }

		readOnly := readOnlyAgentCtx("a")
		result := call(t, readOnly, "a:a:ns:erase")
		require.True(t, result.IsError, "a read-only token must not reach the destructive raw name")
		assert.Contains(t, text(result), "Permission denied: token does not have 'destructive' permission required for tool 'a:a:ns:erase'")
		assert.Equal(t, int64(0), calls.count.Load(), "a refused call must never reach the upstream")

		result = call(t, readOnly, "a:ns:erase")
		require.False(t, result.IsError, "control: the read-tier suffix tool stays reachable: %s", text(result))
		full := auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
			AllowedServers: []string{"a"},
			Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		})
		result = call(t, full, "a:a:ns:erase")
		require.False(t, result.IsError, "control: a token holding destructive reaches the prefixed raw name: %s", text(result))
		assert.Equal(t, []string{"ns:erase", "a:ns:erase"}, calls.dispatched(),
			"the upstream must receive exactly the raw names that were authorized")
	})

	// The approval gate reads the same split pair: a pending record filed
	// under the exact raw "a:ns:erase" must lock it for every dispatch path
	// (the retrieve surface and the sandbox bridge alike) even though the
	// suffix tool "ns:erase" is approved and callable.
	t.Run("approval lock keyed on the exact raw name", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedTargetTierServer(t, proxy, rt, "a", []stateview.ToolInfo{rawRead, prefixedDestructive})
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "a:ns:erase", Status: storage.ToolApprovalStatusPending, CurrentHash: "h-pending",
		}))
		require.True(t, proxy.evaluateToolGate("a", "ns:erase").callable(), "control: the suffix tool stays callable")

		caller := &upstreamToolCaller{proxy: proxy}
		require.Nil(t, caller.policyRefusal("a", "ns:erase"), "control: the sandbox admits the approved suffix tool")
		require.Error(t, caller.policyRefusal("a", "a:ns:erase"), "the sandbox must refuse the pending raw name")

		full := auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "full", TokenPrefix: "mcp_agt_f",
			AllowedServers: []string{"a"},
			Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		})
		req := mcp.CallToolRequest{}
		req.Params.Name = contracts.ToolVariantDestructive
		req.Params.Arguments = map[string]interface{}{"name": "a:a:ns:erase"}
		result, err := proxy.handleCallToolVariant(full, req, contracts.ToolVariantDestructive)
		require.NoError(t, err)
		require.NotNil(t, result)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "TOOL_QUARANTINED", "the pending lock under the exact raw name must answer")
		assert.NotContains(t, text, "No client found", "the call must never reach dispatch")
	})
}
