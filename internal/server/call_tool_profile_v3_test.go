package server

import (
	"context"
	"strings"
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
		name, tool, golden string
		tier               profile.Tier
		capText            string
	}{
		{name: "tier cap", tool: "github:create_issue", golden: "tier", tier: profile.TierWrite, capText: "read"},
		{name: "deny rule", tool: "github:get_secret_scanning_alert", golden: "rule", tier: profile.TierRead, capText: "read"},
		{name: "unannotated deny", tool: "github:search_code", golden: "unannotated", tier: profile.TierUnannotated, capText: "read"},
	}
	variants := []string{contracts.ToolVariantRead, contracts.ToolVariantWrite, contracts.ToolVariantDestructive}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, tool, _ := strings.Cut(tc.tool, ":")
			disclosed := v3Disclosed(t, tc.golden, server, tool, tc.tier, tc.capText, "work-readonly")
			undisclosed := undisclosedToolRefusal(t, tc.golden, server, tool, tc.tier, tc.capText)
			for _, variant := range variants {
				t.Run(variant, func(t *testing.T) {
					for source, contextFor := range sources {
						t.Run(source, func(t *testing.T) {
							result, err := proxy.handleCallToolVariant(contextFor(), auditCallToolRequest(tc.tool, nil), variant)
							require.NoError(t, err)
							require.NotNil(t, result)
							require.True(t, result.IsError)
							text := resultText(t, result)
							if source == "anonymous" {
								// Spec 108 D39: the operator's anonymous_profile is never
								// handed to an unauthenticated caller.
								require.Equal(t, undisclosed, text)
								require.NotContains(t, text, "work-readonly")
								require.NotContains(t, text, "Work · Read-only")
								return
							}
							// pin, binding, url, session: the caller's own profile is named.
							require.Equal(t, disclosed, text)
							require.Contains(t, text, `"Work · Read-only" (work-readonly)`)
						})
					}
				})
			}
		})
	}
	require.Empty(t, up.dispatched(), "profile denials must happen before upstream I/O")
}

// Spec 108 D39: an anonymous caller keeps the non-disclosing text.
func TestCallTool_AnonymousProfileRefusalStaysUndisclosed(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	proxy.currentConfig().AnonymousProfile = "work-readonly"

	result, err := proxy.handleCallToolVariant(anonCtx(), auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "blocked by profile: github:create_issue is a write tool; this profile allows read tools only", resultText(t, result))
}

// Spec 108 D39: a dangling pin has no policy and answers like an out-of-scope
// token (the Spec 105 non-disclosing shape), never naming a profile.
func TestCallTool_DanglingPinStaysNonDisclosing(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))

	result, err := proxy.handleCallToolVariant(pinnedProfileCtx("gone"), auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, result.IsError)
	text := resultText(t, result)
	require.NotContains(t, text, "gone")
	require.NotContains(t, text, "blocked by profile")
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
	require.Equal(t, v3TierRefusal(t), refusal.Error())
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
	require.Equal(t, v3TierRefusal(t), resultText(t, result))
}

func TestCallTool_ProfileDenialWritesBlockedActivityReason(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	go rt.ActivityService().Start(rt.AppContext(), rt)
	startDeadline := time.Now().Add(5 * time.Second)
	for !rt.ActivityService().Started() && time.Now().Before(startDeadline) {
		time.Sleep(time.Millisecond)
	}
	require.True(t, rt.ActivityService().Started(), "activity service must subscribe before the policy decision is emitted")
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
