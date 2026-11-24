package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// Issue #2027: a profile's tool_response_limit replaces the global limit for
// calls served under it. 0 means no limit, so a script that parses the JSON
// itself gets the whole answer, structuredContent included, while a caller on
// no profile keeps the global bound.
func TestCallTool_ProfileToolResponseLimit(t *testing.T) {
	const globalLimit = 400
	proxy, _, ups := newProfilesV3FixtureUpstreams(t, func(cfg *config.Config) {
		zero, small := 0, 120
		cfg.Profiles = append(cfg.Profiles,
			config.ProfileConfig{Name: "script", Servers: []string{"github"}, ToolResponseLimit: &zero},
			config.ProfileConfig{Name: "small", Servers: []string{"github"}, ToolResponseLimit: &small},
		)
	})
	proxy.setStaticTruncator(truncate.NewTruncator(globalLimit))

	body := strings.Repeat("x", 5000)
	structured := map[string]any{
		"value":           []any{strings.Repeat("y", 5000)},
		"@odata.nextLink": "https://graph.example/next",
	}
	up := ups["github"]
	up.okBody.Store(&body)
	up.okStructured.Store(&structured)

	call := func(ctx context.Context) *mcp.CallToolResult {
		t.Helper()
		result, err := proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.IsError, "the call must dispatch: %v", result.Content)
		return result
	}
	adminCtx := func() context.Context {
		return auth.WithAuthContext(context.Background(), auth.AdminContext())
	}

	t.Run("no profile keeps the global limit", func(t *testing.T) {
		result := call(adminCtx())
		require.Nil(t, result.StructuredContent, "oversize structuredContent is withheld under the global limit")
		require.Less(t, len(resultText(t, result)), len(body))
	})

	t.Run("profile with 0 returns the whole answer", func(t *testing.T) {
		for name, ctx := range map[string]context.Context{
			"url":     urlProfileCtx(proxy, "script"),
			"session": sessionProfileCtx(t, proxy, "limit-session", "script"),
			"pin":     pinnedProfileCtx("script"),
		} {
			t.Run(name, func(t *testing.T) {
				result := call(ctx)
				require.Len(t, result.Content, 1)
				require.Contains(t, resultText(t, result), body, "the text block arrives whole")
				got, ok := result.StructuredContent.(map[string]any)
				require.True(t, ok, "structuredContent is forwarded as an object, got %T", result.StructuredContent)
				require.Equal(t, "https://graph.example/next", got["@odata.nextLink"], "sibling fields survive")
			})
		}
	})

	t.Run("profile with a smaller limit bounds below the global one", func(t *testing.T) {
		short := strings.Repeat("z", 300) // under the global limit, over the profile's
		up.okBody.Store(&short)
		up.okStructured.Store(nil)
		t.Cleanup(func() { up.okBody.Store(&body); up.okStructured.Store(&structured) })

		require.Contains(t, resultText(t, call(adminCtx())), short, "control: whole under the global limit")
		require.NotContains(t, resultText(t, call(urlProfileCtx(proxy, "small"))), short)
	})
}

// A profile that sets no limit, and a name the snapshot no longer has, both
// resolve to the live global truncator (so a hot-reloaded global limit still
// applies to them).
func TestTruncatorForProfile_Inherits(t *testing.T) {
	proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
		zero := 0
		cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: "script", Servers: []string{"github"}, ToolResponseLimit: &zero})
	})
	global := truncate.NewTruncator(777)
	proxy.setStaticTruncator(global)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	require.Same(t, global, proxy.truncatorForProfile(idx, ""))
	require.Same(t, global, proxy.truncatorForProfile(idx, "legacy"))
	require.Same(t, global, proxy.truncatorForProfile(idx, "deleted-profile"))
	require.Same(t, global, proxy.truncatorForProfile(nil, "script"))
	require.Equal(t, 0, proxy.truncatorForProfile(idx, "script").Limit())
}

// Direct mode bounds text by a bare character count; the same rule applies.
func TestResponseLimitForProfile(t *testing.T) {
	zero, small := 0, 120
	idx := newProfileIndex(&config.Config{
		Servers: []*config.ServerConfig{{Name: "github"}},
		Profiles: []config.ProfileConfig{
			{Name: "script", Servers: []string{"github"}, ToolResponseLimit: &zero},
			{Name: "small", Servers: []string{"github"}, ToolResponseLimit: &small},
			{Name: "plain", Servers: []string{"github"}},
		},
	})
	require.Equal(t, 0, responseLimitForProfile(idx, "script", 20000))
	require.Equal(t, 120, responseLimitForProfile(idx, "small", 20000))
	require.Equal(t, 20000, responseLimitForProfile(idx, "plain", 20000))
	require.Equal(t, 20000, responseLimitForProfile(idx, "gone", 20000))
	require.Equal(t, 20000, responseLimitForProfile(idx, "", 20000))
	require.Equal(t, 20000, responseLimitForProfile(nil, "script", 20000))
}

// A profile's wider limit must not carry a structured result past output
// sanitisation: that reads text blocks only, and the global limit is what
// turned an oversize structured result into text. While sanitisation can block
// or rewrite output, the result is still handed over as text (so it is
// sanitised) and the profile's limit decides only how much of it is delivered.
func TestCallTool_ProfileToolResponseLimit_KeepsStructuredSanitised(t *testing.T) {
	const globalLimit = 400
	proxy, _, ups := newProfilesV3FixtureUpstreams(t, func(cfg *config.Config) {
		zero := 0
		cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: "script", Servers: []string{"github"}, ToolResponseLimit: &zero})
	})
	proxy.setStaticTruncator(truncate.NewTruncator(globalLimit))
	proxy.config.OutputSanitisation = &config.OutputSanitisationConfig{ResponseAction: "redact", MaxRedactions: 100}

	body := "summary"
	structured := map[string]any{"value": []any{strings.Repeat("y", 5000)}, "@odata.nextLink": "https://graph.example/next"}
	ups["github"].okBody.Store(&body)
	ups["github"].okStructured.Store(&structured)

	result, err := proxy.handleCallToolVariant(urlProfileCtx(proxy, "script"), auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	require.Nil(t, result.StructuredContent, "the structured result goes through the sanitised text path")

	var whole string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "@odata.nextLink") {
			whole = tc.Text
		}
	}
	require.NotEmpty(t, whole, "the structured result is delivered as a text block")
	require.Greater(t, len(whole), 5000, "and the profile's limit delivers it whole")
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(whole), &parsed), "still one parseable JSON document")
}

func TestStructuredWithholdLimit(t *testing.T) {
	for _, tc := range []struct {
		name            string
		profile, global int
		sanitising      bool
		want            int
	}{
		{"same limit", 400, 400, true, 400},
		{"narrower profile", 100, 400, true, 100},
		{"wider profile, no sanitisation", 9000, 400, false, 9000},
		{"unlimited profile, no sanitisation", 0, 400, false, 0},
		{"wider profile, sanitising", 9000, 400, true, 400},
		{"unlimited profile, sanitising", 0, 400, true, 400},
		{"unlimited global, sanitising", 100, 0, true, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, structuredWithholdLimit(tc.profile, tc.global, tc.sanitising))
		})
	}
}
