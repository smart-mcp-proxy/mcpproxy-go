package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 108 (Profiles v3) T018a (FR-019): the tool policy (max_tier cap, deny
// rules, classify) must never filter prompts — prompts/list and prompts/get
// under a v3 profile equal those of a legacy profile with the SAME servers,
// even when the v3 profile carries a deny rule that would exclude a
// similarly-named TOOL. filterAggregatedPromptsForAuth (mcp_direct_scope.go)
// consults only profileScope.Allows(serverName) — never a CompiledPolicy —
// so this is a permanent regression guard: 108-b must not, now or later,
// make prompt filtering consult the tool policy.
func TestPrompts_ProfileV3_ToolPolicyNeverFiltersPrompts(t *testing.T) {
	falseVal := false
	cfg := &config.Config{
		Servers: []*config.ServerConfig{{Name: "github"}, {Name: "notion"}},
		Profiles: []config.ProfileConfig{
			{
				Name: "work-readonly-v3", Servers: []string{"github", "notion"}, MaxTier: "read",
				Tools:         &config.ProfileToolRules{Deny: []string{"github:*secret*"}},
				CodeExecution: &falseVal,
			},
			// A legacy profile over the EXACT SAME servers — the FR-019
			// oracle: nothing in the v3 profile above may make prompt
			// visibility differ from this one.
			{Name: "legacy-samesrv", Servers: []string{"github", "notion"}},
		},
	}
	proxy := &MCPProxyServer{config: cfg}

	// A prompt whose name would match the v3 profile's deny GLOB if (wrongly)
	// evaluated as a tool identity.
	secretLikePrompt := aggregatedPromptForTest("github", "get_secret_info")
	otherPrompt := aggregatedPromptForTest("notion", "summarize_page")
	prompts := []mcp.Prompt{secretLikePrompt, otherPrompt}

	v3Ctx := profile.WithProfileScope(t.Context(), proxy.profileScopeForSlug("work-readonly-v3"))
	legacyCtx := profile.WithProfileScope(t.Context(), proxy.profileScopeForSlug("legacy-samesrv"))

	v3Result := promptNamesForTest(proxy.filterAggregatedPromptsForAuth(v3Ctx, prompts))
	legacyResult := promptNamesForTest(proxy.filterAggregatedPromptsForAuth(legacyCtx, prompts))

	assert.ElementsMatch(t, legacyResult, v3Result,
		"a tool-policy deny rule must never filter a prompt, even one whose name would match its glob")
	require.Len(t, v3Result, 2, "both prompts must be visible: neither server is out of scope")
}
