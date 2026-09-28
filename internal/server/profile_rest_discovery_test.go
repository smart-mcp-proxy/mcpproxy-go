package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchToolsForProfileFiltersBeforeLimit(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)
	mainServer := &Server{runtime: rt, mcpProxy: proxy}

	rows, handled, err := mainServer.SearchToolsForProfile(urlProfileCtx(proxy, "work-readonly"), "list_issues", 1, nil)

	require.NoError(t, err)
	require.True(t, handled)
	require.Len(t, rows, 1, "an excluded high-ranked hit must not consume the only result slot")
	tool, ok := rows[0]["tool"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "github:list_issues", tool["name"])
	require.False(t, mainServer.ToolAllowedByProfile(urlProfileCtx(proxy, "work-readonly"), "github", "create_issue"))
	require.True(t, mainServer.ToolAllowedByProfile(urlProfileCtx(proxy, "work-readonly"), "github", "list_issues"))
	require.False(t, mainServer.ToolAllowedByProfile(urlProfileCtx(proxy, "work-readonly"), "filesystem", "read_text_file"))
	require.True(t, mainServer.ToolAllowedByProfile(adminCtx(), "filesystem", "read_text_file"), "an unprofiled admin keeps the existing view")
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
