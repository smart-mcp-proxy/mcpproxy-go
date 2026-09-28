package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestCallTool_ProfileV3_RefusalsAreStableAcrossResolutionSources(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	up := startCountingUpstream(t, proxy, rt, "github",
		writeSpec("create_issue"),
		readSpec("get_secret_scanning_alert"),
		toolSpec{Name: "search_code", Description: "Search code"},
	)

	proxy.currentConfig().AnonymousProfile = "work-readonly"

	sources := map[string]func() context.Context{
		"url": func() context.Context { return urlProfileCtx(proxy, "work-readonly") },
		"session": func() context.Context {
			return sessionProfileCtx(t, proxy, "session-profile-v3", "work-readonly")
		},
		"pin":       func() context.Context { return pinnedProfileCtx("work-readonly") },
		"binding":   func() context.Context { return clientCtx("desktop", "work-readonly", "switchable") },
		"anonymous": anonCtx,
	}
	cases := []struct {
		name, tool, refusal string
	}{
		{
			name: "tier cap", tool: "github:create_issue",
			refusal: "blocked by profile: github:create_issue is a write tool; this profile allows read tools only",
		},
		{
			name: "deny rule", tool: "github:get_secret_scanning_alert",
			refusal: "blocked by profile: github:get_secret_scanning_alert is denied by a profile rule",
		},
		{
			name: "unannotated deny", tool: "github:search_code",
			refusal: "blocked by profile: github:search_code has no tier annotation; an operator can classify it in the profile to allow it",
		},
	}
	variants := []string{contracts.ToolVariantRead, contracts.ToolVariantWrite, contracts.ToolVariantDestructive}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, variant := range variants {
				t.Run(variant, func(t *testing.T) {
					var first string
					for source, contextFor := range sources {
						t.Run(source, func(t *testing.T) {
							result, err := proxy.handleCallToolVariant(contextFor(), auditCallToolRequest(tc.tool, nil), variant)
							require.NoError(t, err)
							require.NotNil(t, result)
							require.True(t, result.IsError)
							text := resultText(t, result)
							require.Equal(t, tc.refusal, text)
							require.NotContains(t, text, "work-readonly")
							require.NotContains(t, text, "Work · Read-only")
							if first == "" {
								first = text
							} else {
								require.Equal(t, first, text, "refusal bytes must not reveal the resolution source")
							}
						})
					}
				})
			}
		})
	}
	require.Empty(t, up.dispatched(), "profile denials must happen before upstream I/O")
}

func TestCallTool_ProfileV3_RestDispatchPreservesTypedRefusal(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	up := startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))

	_, err := proxy.CallToolDirect(urlProfileCtx(proxy, "work-readonly"), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      contracts.ToolVariantWrite,
			Arguments: map[string]interface{}{"name": "github:create_issue", "args_json": "{}"},
		},
	})

	var refusal *profile.ToolBlockedError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, profile.BlockReasonTier, refusal.Reason)
	require.Equal(t, "blocked by profile: github:create_issue is a write tool; this profile allows read tools only", refusal.Error())
	require.Empty(t, up.dispatched(), "REST dispatch must refuse before upstream I/O")
}

func TestCallTool_ProfileRefusalPrecedesTokenPermissionRefusal(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "read-only-agent", AllowedServers: []string{"*"},
		Permissions: []string{auth.PermRead}, ProfilePin: "work-readonly",
	})
	result, err := proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "blocked by profile: github:create_issue is a write tool; this profile allows read tools only", resultText(t, result))
}

func TestCallTool_ProfileDenialWritesBlockedActivityReason(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	go rt.ActivityService().Start(rt.AppContext(), rt)
	result, err := proxy.handleCallToolVariant(urlProfileCtx(proxy, "work-readonly"), auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, result.IsError)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records, _, listErr := rt.StorageManager().ListActivities(storage.ActivityFilter{Limit: 50})
		require.NoError(t, listErr)
		for _, record := range records {
			if record.Type == storage.ActivityTypePolicyDecision && record.ServerName == "github" && record.ToolName == "create_issue" {
				require.Equal(t, "blocked", record.Status)
				require.Equal(t, string(profile.BlockReasonTier), record.Metadata[storage.MetadataKeyBlockReason])
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("profile denial did not persist a blocked activity record with block_reason")
}
