package server

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSearchToolsForProfileFiltersBeforeLimit(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}}
		cfg.Profiles = []config.ProfileConfig{{Name: "cap-read", Servers: []string{"a"}, MaxTier: config.ProfileTierRead}}
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

	// Control: the raw unprofiled rank must put the write tool in the only slot.
	raw, err := proxy.index.SearchTools("widget", 1)
	require.NoError(t, err)
	require.Len(t, raw, 1)
	require.Equal(t, "a:many", raw[0].Tool.Name, "fixture: write tool must outrank the read tool")

	mainServer := &Server{runtime: rt, mcpProxy: proxy}

	rows, handled, err := mainServer.SearchToolsForProfile(urlProfileCtx(proxy, "cap-read"), "widget", 1, nil)

	require.NoError(t, err)
	require.True(t, handled)
	require.Len(t, rows, 1, "an excluded high-ranked hit must not consume the only result slot")
	tool, ok := rows[0]["tool"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "a:keep", tool["name"])
	require.False(t, mainServer.ToolAllowedByProfile(urlProfileCtx(proxy, "cap-read"), "a", "many"))
	require.True(t, mainServer.ToolAllowedByProfile(urlProfileCtx(proxy, "cap-read"), "a", "keep"))
}

func TestProfileRESTDiscoveryPreservesLegacyProfileBehavior(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)
	mainServer := &Server{runtime: rt, mcpProxy: proxy}
	ctx := urlProfileCtx(proxy, "legacy")

	rows, handled, err := mainServer.SearchToolsForProfile(ctx, "list_issues", 10, nil)
	require.NoError(t, err)
	require.True(t, handled)
	require.NotEmpty(t, rows, "legacy profiles keep their existing in-scope discovery behavior")
	require.True(t, mainServer.ToolAllowedByProfile(ctx, "github", "create_issue"), "legacy profile does not acquire a tier policy")
	require.False(t, mainServer.ToolAllowedByProfile(ctx, "filesystem", "read_text_file"), "legacy profile still enforces its server scope")
}
