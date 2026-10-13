package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// setProfileRefusalGolden reads the shared golden of the set_profile refusal
// texts (internal/profile/testdata/contract/set_profile_refusals.json).
func setProfileRefusalGolden(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("../profile/testdata/contract/set_profile_refusals.json")
	require.NoError(t, err)
	var golden map[string]string
	require.NoError(t, json.Unmarshal(raw, &golden))
	return golden
}

// expectedSetProfileRefusal is the golden refusal text a caller gets for a
// refused slug: client credentials by mode, everyone else the Spec 105 text.
func expectedSetProfileRefusal(t *testing.T, ctx context.Context, slug string) string {
	t.Helper()
	golden := setProfileRefusalGolden(t)
	key := "scoped_unknown"
	if ac := auth.AuthContextFromContext(ctx); ac.IsClientCredential() {
		if ac.ProfileMode == auth.ProfileModeLocked {
			key = "locked"
		} else {
			key = "switchable"
		}
	}
	return strings.ReplaceAll(golden[key], "<p>", slug)
}

// F-11 (user test 2026-10-02): a locked client asking for a profile it cannot
// have was told "unknown profile 'work-full'" although the profile exists. The
// text now depends only on the caller's own credential, never on the slug.
func TestSetProfileV3_ClientRefusalTextMatchesGolden(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	golden := setProfileRefusalGolden(t)

	call := func(ctx context.Context, slug string) *mcp.CallToolResult {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": slug}
		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		return result
	}

	t.Run("locked client: existing and missing slugs get the same locked text", func(t *testing.T) {
		for i, slug := range []string{"work-full", "legacy", "nope"} {
			sid := "locked-golden-" + string(rune('a'+i))
			ctx := sessionCtx(clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), sid)
			result := call(ctx, slug)
			require.True(t, result.IsError)
			text := resultText(t, result)
			require.Equal(t, strings.ReplaceAll(golden["locked"], "<p>", slug), text)
			require.NotContains(t, text, "work-readonly", "a refusal never names the bound profile")
			require.NotContains(t, text, "available")
			require.Empty(t, proxy.sessionStore.GetActiveProfile(sid))
		}
	})

	t.Run("switchable client: non-declared and missing slugs get the switchable text", func(t *testing.T) {
		for i, slug := range []string{"legacy", "nope"} {
			sid := "switchable-golden-" + string(rune('a'+i))
			ctx := sessionCtx(clientCtx("laptop", "work-readonly", auth.ProfileModeSwitchable), sid)
			result := call(ctx, slug)
			require.True(t, result.IsError)
			text := resultText(t, result)
			require.Equal(t, strings.ReplaceAll(golden["switchable"], "<p>", slug), text)
			require.NotContains(t, text, "work-readonly")
			require.Empty(t, proxy.sessionStore.GetActiveProfile(sid))
		}
	})

	t.Run("switchable client may still switch to its declared target", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", auth.ProfileModeSwitchable), "switchable-ok")
		require.False(t, call(ctx, "work-full").IsError)
		require.Equal(t, "work-full", proxy.sessionStore.GetActiveProfile("switchable-ok"))
	})

	t.Run("locked client may still select its own base and clear", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), "locked-own")
		require.False(t, call(ctx, "work-readonly").IsError)
		require.False(t, call(ctx, "").IsError)
	})

	t.Run("unpinned agent token keeps the Spec 105 text", func(t *testing.T) {
		ctx := setProfileScopedCtx("agent-golden", "github")
		result := call(ctx, "nope")
		require.True(t, result.IsError)
		require.Equal(t, strings.ReplaceAll(golden["scoped_unknown"], "<p>", "nope"), resultText(t, result))
	})

	t.Run("administrator keeps the available list", func(t *testing.T) {
		result := call(setProfileAdminCtx("admin-golden"), "nope")
		require.True(t, result.IsError)
		text := resultText(t, result)
		prefix := strings.ReplaceAll(strings.SplitN(golden["admin_unknown"], "<list>", 2)[0], "<p>", "nope")
		require.True(t, strings.HasPrefix(text, prefix), text)
		require.Contains(t, text, "work-full")
		require.Contains(t, text, "work-readonly")
	})
}
