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
	adminCtx := context.Background() // API-key administrator: no agent AuthContext

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

	for name, ctx := range map[string]context.Context{"full-tier token": fullToken, "api-key admin": adminCtx} {
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

	t.Run("an exact record outranks the collapsed one", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		seedCollapsedRecord(t, proxy, rt, storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending})
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved,
		}))
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.Equal(t, "ns:erase", gate.approval.ToolName, "the exact-name record is authoritative when present")
		assert.Empty(t, gate.lockStatus)
		assert.True(t, gate.callable())
		assert.True(t, proxy.isToolCallable("a", "ns:erase"))
	})
}
