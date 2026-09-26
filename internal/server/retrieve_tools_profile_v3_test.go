package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 108 (Profiles v3) PR 108-b, T016: retrieve_tools filter-before-limit +
// hidden_by_profile + profile (url/session sources only), over the
// contracts/enforcement-matrix.md fixture. Sources exercised here are the
// three 108-b resolves without 108-c: URL (/mcp/p/<slug>), session
// (set_profile) and a legacy agent-token pin (Spec 057/105) — binding and
// anonymous rows are added by 108-d's T044a once 108-c lands the resolver
// tiers that produce them.

// profileV3RetrieveResponse decodes exactly the fields this file's tests
// need. HiddenByProfile and Profile are pointers so ABSENCE (nil) is
// distinguishable from a present zero value / empty string (FR-011).
type profileV3RetrieveResponse struct {
	Tools           []map[string]interface{} `json:"tools"`
	Total           int                      `json:"total"`
	HiddenByProfile *int                     `json:"hidden_by_profile"`
	Profile         *string                  `json:"profile"`
}

func callRetrieveToolsV3(t *testing.T, proxy *MCPProxyServer, ctx context.Context, query string, limit int) profileV3RetrieveResponse {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": query, "limit": float64(limit)}
	result, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	require.False(t, result.IsError, "retrieve_tools returned an error: %v", result.Content)
	var resp profileV3RetrieveResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &resp))
	return resp
}

// urlProfileCtx simulates a /mcp/p/<slug> request: an administrator-shaped
// context (profileMiddleware's real behaviour for an unauthenticated
// connection) carrying the URL-injected ProfileScope.
func urlProfileCtx(proxy *MCPProxyServer, slug string) context.Context {
	scope := proxy.profileScopeForSlug(slug)
	return profile.WithProfileScope(auth.WithAuthContext(context.Background(), auth.AdminContext()), scope)
}

// sessionProfileCtx simulates a set_profile session selection on the base
// /mcp endpoint: an administrator-shaped context bound to a stable mcp-go
// session id whose sessionStore entry names slug.
func sessionProfileCtx(t *testing.T, proxy *MCPProxyServer, sessionID, slug string) context.Context {
	t.Helper()
	proxy.sessionStore.SetActiveProfile(sessionID, slug)
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	base := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	return auth.WithAuthContext(base, auth.AdminContext())
}

// pinnedProfileCtx simulates a legacy agent token pinned to slug (Spec 057/
// 105): unrestricted server grant so only the pin's own reach governs.
func pinnedProfileCtx(slug string) context.Context {
	return agentCtx([]string{"*"}, []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}, slug)
}

func TestRetrieveTools_ProfileV3_DecisionMatrix(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	// contracts/enforcement-matrix.md "Expected profile decisions" (FR-010),
	// keyed by exact tool name (the deterministic query this file's tests
	// use — see profiles_v3_index_fixture_test.go's doc comment).
	cases := []struct {
		tool                           string
		admittedReadonly, admittedFull bool
		admittedLegacy                 bool
	}{
		{"list_issues", true, true, true},
		{"create_issue", false, true, true},
		{"delete_repo", false, true, true},
		{"search_code", false, true, true}, // legacy: server "github" is in legacy's servers, admitted (as_read default, no cap)
		{"get_secret_scanning_alert", false, true, true},
		{"update_page", true, true, false}, // legacy profile's servers = ["github"] only
		{"read_text_file", false, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			readonly := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), tc.tool, 5)
			assert.Equal(t, tc.admittedReadonly, len(readonly.Tools) > 0, "work-readonly: %s", tc.tool)

			full := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-full"), tc.tool, 5)
			assert.Equal(t, tc.admittedFull, len(full.Tools) > 0, "work-full: %s", tc.tool)

			legacy := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "legacy"), tc.tool, 5)
			assert.Equal(t, tc.admittedLegacy, len(legacy.Tools) > 0, "legacy: %s", tc.tool)
		})
	}

	t.Run("hidden_by_profile present (possibly 0) for non-legacy, absent for legacy", func(t *testing.T) {
		readonly := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), "create_issue", 5)
		require.NotNil(t, readonly.HiddenByProfile)
		assert.Equal(t, 1, *readonly.HiddenByProfile, "create_issue matches and is excluded by the tier cap")

		full := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-full"), "create_issue", 5)
		require.NotNil(t, full.HiddenByProfile)
		assert.Equal(t, 0, *full.HiddenByProfile, "work-full admits create_issue: nothing hidden")

		legacy := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "legacy"), "create_issue", 5)
		assert.Nil(t, legacy.HiddenByProfile, "legacy profile must never gain hidden_by_profile (SC-003)")
	})

	t.Run("deny-beats-allow overlap: get_secret_scanning_alert matched by both an allow and a deny rule", func(t *testing.T) {
		resp := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), "get_secret_scanning_alert", 5)
		assert.Empty(t, resp.Tools, "deny must beat allow on the overlapping pair (FR-010 step 2 before step 3)")
		require.NotNil(t, resp.HiddenByProfile)
		assert.Equal(t, 1, *resp.HiddenByProfile)
	})

	t.Run("allow rule admits despite the tier cap: notion:update_page", func(t *testing.T) {
		resp := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), "update_page", 5)
		require.Len(t, resp.Tools, 1)
		assert.Equal(t, "notion:update_page", resp.Tools[0]["name"])
	})
}

// TestRetrieveTools_ProfileV3_ProfileFieldSourceGating pins FR-011's `profile`
// field rule: present only for url/session sources, absent for a pin — for
// the SAME non-legacy profile and tool.
func TestRetrieveTools_ProfileV3_ProfileFieldSourceGating(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	t.Run("url source: profile field present", func(t *testing.T) {
		resp := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), "list_issues", 5)
		require.NotNil(t, resp.Profile)
		assert.Equal(t, "work-readonly", *resp.Profile)
	})

	t.Run("session source: profile field present", func(t *testing.T) {
		ctx := sessionProfileCtx(t, proxy, "sess-v3-1", "work-readonly")
		resp := callRetrieveToolsV3(t, proxy, ctx, "list_issues", 5)
		require.NotNil(t, resp.Profile)
		assert.Equal(t, "work-readonly", *resp.Profile)
	})

	t.Run("pin source: profile field absent", func(t *testing.T) {
		resp := callRetrieveToolsV3(t, proxy, pinnedProfileCtx("work-readonly"), "list_issues", 5)
		assert.Nil(t, resp.Profile, "a pinned caller must never learn it is pinned or to what (research D27)")
		require.NotNil(t, resp.HiddenByProfile, "hidden_by_profile is independent of source and still applies")
	})
}

// TestRetrieveTools_ProfileV3_DanglingPinDenyAll pins FR-020: a legacy agent
// token pinned to a profile hand-deleted from config resolves deny-all, with
// hidden_by_profile: 0 and NO profile field (base source, never disclosed).
func TestRetrieveTools_ProfileV3_DanglingPinDenyAll(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	resp := callRetrieveToolsV3(t, proxy, pinnedProfileCtx("ghost-profile"), "list_issues", 5)
	assert.Empty(t, resp.Tools, "a dangling pin must deny all")
	require.NotNil(t, resp.HiddenByProfile, "a dangling base always counts as non-legacy")
	assert.Equal(t, 0, *resp.HiddenByProfile, "the effective server scope is empty, so nothing is counted as hidden")
	assert.Nil(t, resp.Profile)
}

// TestRetrieveTools_ProfileV3_FilterBeforeLimit is the FR-011 "filter before
// limit" assertion: a policy-excluded tool that ranks first in the raw
// corpus must never displace an admitted one from a small window, and
// hidden_by_profile must still count it. Uses a dedicated small fixture
// (rather than the enforcement-matrix one) so the rank order can be forced
// deterministically via BM25 term-frequency repetition — the same technique
// internal/index/search_scoped_test.go's buildScopedSeamCorpus and
// search_admitted_test.go's buildAdmittedSeamCorpus use at the index layer;
// this is the same property proven end-to-end through retrieve_tools.
func TestRetrieveTools_ProfileV3_FilterBeforeLimit(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		config.EnablePolicyForTest(t)
		cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}}
		cfg.Profiles = []config.ProfileConfig{
			{Name: "cap-read", Servers: []string{"a"}, MaxTier: "read"},
		}
	})

	startCountingUpstream(t, proxy, rt, "a",
		toolSpec{Name: "keep", Description: "widget status check", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		toolSpec{Name: "many", Description: strings.Repeat("widget ", 10), Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}},
	)
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:keep", ServerName: "a", Description: "widget status check", ParamsJSON: "{}",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:many", ServerName: "a", Description: strings.Repeat("widget ", 10), ParamsJSON: "{}",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
	}))
	require.NoError(t, proxy.index.RebuildProfileFromShared("cap-read", []string{"a"}))

	// Control: the unscoped/unprofiled window really is the "many" hit.
	unprofiled := callRetrieveToolsV3(t, proxy, adminCtx(), "widget", 1)
	require.Len(t, unprofiled.Tools, 1)
	assert.Equal(t, "a:many", unprofiled.Tools[0]["name"], "fixture: the write tool must outrank the read one")

	resp := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "cap-read"), "widget", 1)
	require.Len(t, resp.Tools, 1, "the admitted hit must fill the page despite the higher-ranked excluded one")
	assert.Equal(t, "a:keep", resp.Tools[0]["name"])
	require.NotNil(t, resp.HiddenByProfile)
	assert.Equal(t, 1, *resp.HiddenByProfile, "a:many must still be counted even though the page was already full")
}

// TestRetrieveTools_ProfileV3_LegacyTitleStaysLegacy pins the data-model.md
// §1 rule that Title/Description alone never flip IsLegacy() — a legacy
// profile that sets ONLY a display title must stay byte-identical (SC-003).
func TestRetrieveTools_ProfileV3_LegacyTitleStaysLegacy(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	resp := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "legacy"), "list_issues", 5)
	assert.Nil(t, resp.HiddenByProfile)
	assert.Nil(t, resp.Profile)
}
