package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

func TestSetProfileV3ReportsOnlyTheSessionSelection(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	ctx := sessionProfileCtx(t, proxy, "session-profile-source", "work-full")
	for _, tc := range []struct {
		name, selection, wantSource string
	}{
		{name: "selected profile", selection: "work-full", wantSource: "session"},
		{name: "cleared selection", selection: "", wantSource: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{"profile": tc.selection}
			result, err := proxy.handleSetProfile(ctx, request)
			require.NoError(t, err)
			require.False(t, result.IsError, resultText(t, result))
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
			require.Equal(t, tc.wantSource, payload["profile_source"])
			require.Equal(t, tc.selection, payload["active_profile"])
		})
	}
}

func TestSetProfileV3SwitchableClientCanSelectDeclaredTarget(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	t.Run("switchable client may select its declared one-hop target", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), "switchable-set-profile")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "work-full"}

		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
		var payload map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
		require.Equal(t, "work-full", payload["active_profile"])
		require.Equal(t, "session", payload["profile_source"])
		require.Equal(t, "work-full", proxy.sessionStore.GetActiveProfile("switchable-set-profile"))
		require.Equal(t, string(profile.SourceSession), proxy.ResolveProfileV3(ctx, idx).Source)
	})

	t.Run("locked client cannot select the same target", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("cursor", "work-readonly", "locked"), "locked-set-profile")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "work-full"}

		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "unknown profile 'work-full'", resultText(t, result))
		require.Empty(t, proxy.sessionStore.GetActiveProfile("locked-set-profile"))
	})

	t.Run("switchable All servers binding keeps legacy selectable profiles", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("all-servers", "", "switchable"), "all-servers-set-profile")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "legacy"}

		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
		require.Equal(t, "legacy", proxy.sessionStore.GetActiveProfile("all-servers-set-profile"))
	})
}

func TestSetProfileV3ConfinedAnonymousHonorsSwitchableTo(t *testing.T) {
	proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
		cfg.AnonymousProfile = "work-readonly"
	})
	ctx := sessionCtx(anonCtx(), "confined-anonymous-set-profile")
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"profile": "legacy"}

	result, err := proxy.handleSetProfile(ctx, request)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "unknown profile 'legacy'", resultText(t, result))
	require.Empty(t, proxy.sessionStore.GetActiveProfile("confined-anonymous-set-profile"))
}
