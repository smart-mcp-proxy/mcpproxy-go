package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 108 (Profiles v3) T019: `/mcp/all` `tools/list` omits excluded tools
// for a caller with an effective profile — filterDirectModeToolsForAuth's
// STAMPED branch (the production path every real upstream tool takes; see
// mcp_direct_scope.go's own doc comment on the unstamped fallback being
// test-only) now also runs the FR-011 policy decision, LIST TIME ONLY.
func directStampedTool(server, rawName, tier string) mcp.Tool {
	entry := &directCatalogEntry{ServerName: server, ToolName: rawName, RequiredPermission: tier}
	return stampDirectTool(mcp.Tool{Name: server + "__" + rawName}, entry)
}

func TestDirectProtocol_ProfileV3CallRefusalOnWire(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)
	up := startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	proxy.RefreshDirectModeTools()
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "profile-locked", ProfilePin: "work-readonly",
		AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	// Mirror mcpAuthMiddleware: mcp-go's before-call hook needs this box to
	// distinguish tools/call re-evaluation from list-time filtering.
	ctx = withDirectRequestKindBox(ctx)

	listPayload, err := json.Marshal(proxy.directServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	require.NoError(t, err)
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(listPayload, &listed), string(listPayload))
	for _, tool := range listed.Result.Tools {
		assert.NotEqual(t, "github__create_issue", tool.Name, "profile-excluded direct tools are absent from /mcp/all tools/list")
	}

	callPayload, err := json.Marshal(proxy.directServer.HandleMessage(ctx,
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"github__create_issue","arguments":{}}}`)))
	require.NoError(t, err)
	var call struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(callPayload, &call), string(callPayload))
	require.True(t, call.Result.IsError, string(callPayload))
	require.NotEmpty(t, call.Result.Content)
	assert.Equal(t, v3TierRefusal(t), call.Result.Content[0].Text)
	assert.Empty(t, up.dispatched(), "a direct-mode refusal must precede upstream I/O")
}

func TestDirectCall_ProfileV3PolicyRefusesBeforeUpstream(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	up := startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	entry := &directCatalogEntry{
		DisplayName: FormatDirectToolName("github", "create_issue"),
		ServerName:  "github", ToolName: "create_issue",
		Annotations: writeSpec("create_issue").Annotations,
	}
	ctx := withDirectRequestKindBox(urlProfileCtx(proxy, "work-readonly"))
	setDirectRequestKind(ctx, directRequestKindCall)

	result, err := proxy.makeDirectModeHandler(entry)(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{}},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Equal(t, v3TierRefusal(t), resultText(t, result))
	require.Empty(t, up.dispatched(), "direct-mode profile denials must happen before upstream I/O")
}

func TestDirectCall_ProfileV3DeniesUnannotatedTool(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	updated := *rt.Config()
	updated.Profiles = append([]config.ProfileConfig(nil), updated.Profiles...)
	updated.Profiles[1].Unannotated = "deny"
	rt.UpdateConfig(&updated, "")
	up := startCountingUpstream(t, proxy, rt, "github", toolSpec{Name: "search_code", Description: "Search code"})

	entry := &directCatalogEntry{
		DisplayName: FormatDirectToolName("github", "search_code"),
		ServerName:  "github",
		ToolName:    "search_code",
		Description: "Search code",
	}
	ctx := withDirectRequestKindBox(urlProfileCtx(proxy, "work-full"))
	setDirectRequestKind(ctx, directRequestKindCall)
	result, err := proxy.makeDirectModeHandler(entry)(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{}},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Equal(t, v3Disclosed(t, "unannotated", "github", "search_code", profile.TierUnannotated, "read", "work-full"), resultText(t, result))
	require.Empty(t, up.dispatched(), "an unannotated tool denied by profile policy must never reach the upstream")
}

func TestFilterDirectModeToolsForAuth_ProfileV3(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)

	tools := []mcp.Tool{
		directStampedTool("github", "list_issues", "read"),
		directStampedTool("github", "create_issue", "write"),
		directStampedTool("notion", "update_page", "write"),
		directStampedTool("filesystem", "read_text_file", "read"),
	}

	t.Run("work-readonly: list-time filter excludes create_issue (tier cap) but keeps the allow-ruled notion:update_page", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "work-readonly")
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "notion__update_page"}, filtered)
	})

	t.Run("work-full: everything in scope is admitted", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "work-full")
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "github__create_issue", "notion__update_page", "filesystem__read_text_file"}, filtered)
	})

	t.Run("anonymous: effective anonymous profile filters the same tools without a URL profile", func(t *testing.T) {
		proxy.currentConfig().AnonymousProfile = "work-readonly"
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(anonCtx(), tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "notion__update_page"}, filtered)
	})

	t.Run("legacy: policy never excludes (only server scope does, which the fixture's legacy profile restricts to github)", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "legacy")
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "github__create_issue"}, filtered)
	})

	t.Run("call-time request: a policy-excluded tool must stay visible to the filter chain (108-d's own gate answers the call, not this filter)", func(t *testing.T) {
		ctx := withDirectRequestKindBox(urlProfileCtx(proxy, "work-readonly"))
		setDirectRequestKind(ctx, directRequestKindCall)
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, []mcp.Tool{directStampedTool("github", "create_issue", "write")}))
		assert.Equal(t, []string{"github__create_issue"}, filtered, "list-time exclusion must not leak into the call-time re-evaluation")
	})
}

func filterDirectToolNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	return names
}
