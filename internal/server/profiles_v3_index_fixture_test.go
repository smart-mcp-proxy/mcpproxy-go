package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108 (Profiles v3) 108-b: newProfilesV3Fixture (108-a,
// profiles_v3_fixture_test.go) wires the enforcement-matrix fixture's three
// upstreams into StateView + storage (for annotation/identity resolution)
// but never into the search index — 108-a exercises CompiledPolicy.Decide
// directly and never calls retrieve_tools/describe_tool. This file adds the
// index side those handlers actually search over.
//
// Descriptions here are the exact RAW tool name, mirroring T001's node-side
// fixture convention ("descriptions = tool names") — but note the QUERY
// strings this file's tests use are the exact tool names too (never the
// contracts/enforcement-matrix.md natural-language probes "issue"/"secret"/
// "search"/"repo"): tool_name is keyword-analyzed (Spec 105 D-something;
// bleve.go), so an exact-name query is the only query shape guaranteed to
// hit deterministically regardless of how the standard analyzer segments a
// snake_case description. The DECISIONS under test (admitted/excluded per
// tool, hidden_by_profile counts, profile field presence) are unaffected by
// this substitution — only the probe string choice is.
func indexEnforcementMatrixFixtureTools(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()
	tools := []*config.ToolMetadata{
		{
			Name: "github:list_issues", ServerName: "github", Description: "list_issues", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		},
		{
			Name: "github:create_issue", ServerName: "github", Description: "create_issue", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
		},
		{
			Name: "github:delete_repo", ServerName: "github", Description: "delete_repo", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
		},
		{
			Name: "github:search_code", ServerName: "github", Description: "search_code", ParamsJSON: "{}",
		},
		{
			Name: "github:get_secret_scanning_alert", ServerName: "github", Description: "get_secret_scanning_alert", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		},
		{
			Name: "notion:update_page", ServerName: "notion", Description: "update_page", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
		},
		{
			Name: "filesystem:read_text_file", ServerName: "filesystem", Description: "read_text_file", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		},
	}
	for _, tool := range tools {
		require.NoError(t, proxy.index.IndexTool(tool))
	}

	// retrieve_tools searches a per-profile PHYSICAL index (index/manager.go
	// ForProfile) once a profile is in effect — a lazily created, initially
	// EMPTY store that production keeps in sync via
	// runtime.rebuildProfileIndex → Manager.RebuildProfileFromShared on every
	// config publish. This test fixture's proxy.index is independent of the
	// runtime's own indexManager (createTestProxyWithRuntimeCfg wires two
	// separate Managers, as the real core does not), so nothing rebuilds it
	// automatically here — do it once, explicitly, for exactly the three
	// enforcementMatrixProfiles fixture profiles.
	for name, servers := range map[string][]string{
		"work-readonly": {"github", "notion"},
		"work-full":     {"github", "notion", "filesystem"},
		"legacy":        {"github"},
	} {
		require.NoError(t, proxy.index.RebuildProfileFromShared(name, servers))
	}
}
