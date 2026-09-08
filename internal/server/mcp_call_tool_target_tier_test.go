package server

import (
	"context"
	"testing"

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
