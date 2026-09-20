package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 PR C (FR-005, gaps FR005-G1..G5): retrieve_tools computes search,
// total, indexed counts, usage ranking and session risk over the AUTHORIZED
// POPULATION only for a scoped caller; administrator output stays
// byte-identical (SC-005), except the two named exceptions this file also
// covers — a profile-scoped administrator on the shared-index fallback (D3)
// and (in mcp_session_risk_test.go) session_risk, which has no admin
// exception at all.

// scopeRetrieveResponse decodes the fields this file's tests need, beyond the
// plain retrieveToolsResponse (mcp_disabled_discovery_test.go) other Spec 085
// tests already use.
type scopeRetrieveResponse struct {
	Tools []map[string]interface{} `json:"tools"`
	Total int                      `json:"total"`
	Debug struct {
		TotalIndexedTools int `json:"total_indexed_tools"`
	} `json:"debug"`
	UsageSummary struct {
		TopTools []map[string]interface{} `json:"top_tools"`
	} `json:"usage_summary"`
}

func callRetrieveScoped(t *testing.T, proxy *MCPProxyServer, ctx context.Context, args map[string]interface{}) scopeRetrieveResponse {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	require.False(t, result.IsError, "retrieve_tools returned an error result: %v", result.Content)
	text := result.Content[0].(mcp.TextContent).Text
	var resp scopeRetrieveResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	return resp
}

func toolNamesOf(resp scopeRetrieveResponse) []string {
	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		if name, ok := tl["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// seedScopeSearchFixture indexes "a:rotate_keys" (a single, low-frequency
// match for "rotate keys") plus two "b" tools whose descriptions repeat the
// query terms heavily — the same displacement shape
// internal/index/search_scoped_test.go uses — so the unscoped top-1 window is
// always a "b" tool while the entitled "a" tool is fully present and
// reachable by an exhaustive scan.
func seedScopeSearchFixture(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()
	for _, name := range []string{"a", "b"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: name, Enabled: true}))
	}
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:rotate_keys", ServerName: "a",
		Description: "rotate keys for a service account", ParamsJSON: "{}",
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "b:one", ServerName: "b",
		Description: "rotate keys rotate keys rotate keys rotate keys", ParamsJSON: "{}",
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "b:two", ServerName: "b",
		Description: "rotate keys rotate keys rotate keys", ParamsJSON: "{}",
	}))
}

// TestRetrieveTools_FR005G1_ScopeAppliedBeforeLimit is T069's server-level
// scenario: an `a`-only token's `{query:"rotate keys", limit:1}` must surface
// "a:rotate_keys" — never the higher-scoring hidden "b" tool the unscoped
// window would return — while an unrestricted (`["*"]`) token and an
// unprofiled administrator both keep seeing the "b" tool unchanged.
func TestRetrieveTools_FR005G1_ScopeAppliedBeforeLimit(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedScopeSearchFixture(t, proxy)

	args := map[string]interface{}{"query": "rotate keys", "limit": float64(1)}

	// Control: the unscoped window really is a "b" hit — otherwise a green
	// scoped assertion below would be a fixture accident, not a fix.
	unscoped := callRetrieveScoped(t, proxy, adminCtx(), args)
	require.Len(t, unscoped.Tools, 1)
	require.Equal(t, "b", unscoped.Tools[0]["server"], "fixture: the hidden server must outrank the entitled one")
	require.Equal(t, 1, unscoped.Total)

	t.Run("a-only token surfaces the entitled hit, not the hidden higher scorer", func(t *testing.T) {
		resp := callRetrieveScoped(t, proxy, agentCtx([]string{"a"}, []string{auth.PermRead}, ""), args)
		assert.Equal(t, []string{"a:rotate_keys"}, toolNamesOf(resp))
		assert.Equal(t, 1, resp.Total)
	})

	t.Run(`wildcard ["*"] token control sees the hidden hit like admin`, func(t *testing.T) {
		resp := callRetrieveScoped(t, proxy, agentCtx([]string{"*"}, []string{auth.PermRead}, ""), args)
		require.Len(t, resp.Tools, 1)
		assert.Equal(t, "b", resp.Tools[0]["server"])
	})

	t.Run("unprofiled administrator is byte-for-byte unchanged (SC-005)", func(t *testing.T) {
		resp := callRetrieveScoped(t, proxy, adminCtx(), args)
		require.Len(t, resp.Tools, 1)
		assert.Equal(t, "b", resp.Tools[0]["server"])
	})

	t.Run("profile-scoped administrator on the shared-index fallback gets the exhaustive-filtered top-K (D3)", func(t *testing.T) {
		// Force ForProfile("broken") to fail: replace the profiles/ directory
		// with a plain file, so MkdirAll(filepath.Dir(profileIndexPath)) hits
		// ENOTDIR deterministically — "a profile with no per-profile index"
		// (research.md D3) without depending on bleve's own error internals.
		profilesDir := filepath.Join(proxy.config.DataDir, "index.bleve", "profiles")
		require.NoError(t, os.WriteFile(profilesDir, []byte("not a directory"), 0o644))

		ctx := profile.WithProfileScope(adminCtx(), profile.NewProfileScope("broken", []string{"a"}))
		resp := callRetrieveScoped(t, proxy, ctx, args)
		assert.Equal(t, []string{"a:rotate_keys"}, toolNamesOf(resp),
			"a profile-scoped administrator on the shared-index fallback must receive the exhaustive-filtered top-K, not today's post-limit filter (D3, SC-005 named exception)")
	})
}

// TestRetrieveTools_FR005G2_UsageStatsAuthorizedPopulation is T070: a scoped
// caller's usage_summary.top_tools must reflect its current authorized
// population's APPROVED tools only. Population membership alone would admit
// a pending tool (still indexed); approval alone never checks the tool still
// exists — a removed tool keeps a stale *approved* record — so both gates are
// required, and only their conjunction excludes every planted decoy.
func TestRetrieveTools_FR005G2_UsageStatsAuthorizedPopulation(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	for _, name := range []string{"a", "b"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: name, Enabled: true}))
	}

	// a:echo — indexed, no approval record (implicit default: approved) →
	// the ONE record an a-only caller must keep.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:echo", ServerName: "a", Description: "echo back the input", ParamsJSON: "{}",
	}))
	// a:pending_tool — indexed but pending approval → excluded by the
	// approval gate despite being in scope and indexed.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:pending_tool", ServerName: "a", Description: "awaiting review", ParamsJSON: "{}",
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "pending_tool", Status: storage.ToolApprovalStatusPending,
	}))
	// a:removed_tool — NOT indexed, but carries a stale *approved* record, so
	// it can only be excluded by population (index) membership.
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "removed_tool", Status: storage.ToolApprovalStatusApproved,
	}))
	// b:secret_sentinel_tool — indexed, approved, but out of the a-only
	// token's server scope entirely.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "b:secret_sentinel_tool", ServerName: "b", Description: "guard the vault", ParamsJSON: "{}",
	}))

	require.NoError(t, proxy.storage.IncrementToolUsage("b:secret_sentinel_tool"))
	require.NoError(t, proxy.storage.IncrementToolUsage("b:secret_sentinel_tool"))
	require.NoError(t, proxy.storage.IncrementToolUsage("b:secret_sentinel_tool"))
	require.NoError(t, proxy.storage.IncrementToolUsage("a:echo"))
	require.NoError(t, proxy.storage.IncrementToolUsage("a:removed_tool"))
	require.NoError(t, proxy.storage.IncrementToolUsage("a:pending_tool"))
	require.NoError(t, proxy.storage.IncrementToolUsage("a:pending_tool"))

	args := map[string]interface{}{"query": "echo", "include_stats": true}

	t.Run("a-only token keeps only its own authorized, approved usage record", func(t *testing.T) {
		resp := callRetrieveScoped(t, proxy, agentCtx([]string{"a"}, []string{auth.PermRead}, ""), args)
		require.Len(t, resp.UsageSummary.TopTools, 1)
		assert.Equal(t, "a:echo", resp.UsageSummary.TopTools[0]["tool_name"])
		assert.EqualValues(t, 1, resp.UsageSummary.TopTools[0]["count"])
	})

	t.Run("administrator is unchanged (SC-005)", func(t *testing.T) {
		want, err := proxy.storage.GetToolStats(10)
		require.NoError(t, err)
		resp := callRetrieveScoped(t, proxy, adminCtx(), args)
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(resp.UsageSummary.TopTools)
		assert.JSONEq(t, string(wantJSON), string(gotJSON))
	})
}

// TestRetrieveTools_FR005G4_ScopedIndexedToolCount is T072:
// `debug.total_indexed_tools` must count the scoped caller's own authorized
// population, both when the search itself ran against the shared index and
// when it ran against an already physically-scoped per-profile index.
func TestRetrieveTools_FR005G4_ScopedIndexedToolCount(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	for _, name := range []string{"a", "b"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: name, Enabled: true}))
	}
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:t1", ServerName: "a", Description: "widget one", ParamsJSON: "{}",
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "b:t2", ServerName: "b", Description: "widget two", ParamsJSON: "{}",
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "b:t3", ServerName: "b", Description: "widget three", ParamsJSON: "{}",
	}))

	args := map[string]interface{}{"query": "widget", "debug": true}

	t.Run("a-only token via the shared index", func(t *testing.T) {
		resp := callRetrieveScoped(t, proxy, agentCtx([]string{"a"}, []string{auth.PermRead}, ""), args)
		assert.Equal(t, 1, resp.Debug.TotalIndexedTools)
	})

	t.Run("administrator counts the whole fleet", func(t *testing.T) {
		resp := callRetrieveScoped(t, proxy, adminCtx(), args)
		assert.Equal(t, 3, resp.Debug.TotalIndexedTools)
	})

	t.Run("profile-index path also counts only the authorized population", func(t *testing.T) {
		pIdx, err := proxy.index.ForProfile("dev")
		require.NoError(t, err)
		require.NoError(t, pIdx.IndexTool(&config.ToolMetadata{
			Name: "a:t1", ServerName: "a", Description: "widget one", ParamsJSON: "{}",
		}))

		ctx := profile.WithProfileScope(
			agentCtx([]string{"*"}, []string{auth.PermRead}, ""),
			profile.NewProfileScope("dev", []string{"a"}),
		)
		resp := callRetrieveScoped(t, proxy, ctx, args)
		assert.Equal(t, 1, resp.Debug.TotalIndexedTools)
	})
}

// TestRetrieveTools_ScopeOracle is T073 (US1.5): a single three-server
// fixture exercised with include_stats + debug + session_risk together,
// table-driven over an a-only token and an administrator, so the population
// every field is computed over is checked for internal self-consistency in
// one place rather than one field at a time.
func TestRetrieveTools_ScopeOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — needs runtime for session_risk")
	}
	proxy, rt, _ := buildMCPProxyWithActivation(t)

	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: name, Enabled: true}))
	}
	// "a": fully read-only tool (no risk contribution). "b"/"c": nil
	// annotations, the MCP-spec most-permissive default that trips every risk
	// flag — an admin's fleet-wide view must therefore read "high", while an
	// a-only view must never see it.
	toolFixtures := map[string]stateview.ToolInfo{
		"a": {Name: "widget_a", Description: "widget a", Annotations: &config.ToolAnnotations{
			ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		}},
		"b": {Name: "widget_b", Description: "widget b"},
		"c": {Name: "widget_c", Description: "widget c"},
	}
	for server, tool := range toolFixtures {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: server + ":" + tool.Name, ServerName: server, Description: tool.Description, ParamsJSON: "{}",
		}))
		// A real StateView snapshot makes the tool "discovered" (identity.Found),
		// which under the default-on quarantine gate requires an explicit
		// approval record or the tool is implicitly pending — unlike the
		// no-runtime fixtures elsewhere in this file, this test needs every
		// tool approved so the usage/session-risk populations aren't emptied
		// by an unrelated gate.
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: server, ToolName: tool.Name, Status: storage.ToolApprovalStatusApproved,
		}))
		rt.Supervisor().StateView().UpdateServer(server, func(s *stateview.ServerStatus) {
			s.Name = server
			s.Connected = true
			s.ToolsDiscovered = true
			s.Tools = []stateview.ToolInfo{toolFixtures[s.Name]}
		})
	}
	require.NoError(t, proxy.storage.IncrementToolUsage("a:widget_a"))
	require.NoError(t, proxy.storage.IncrementToolUsage("a:widget_a"))
	require.NoError(t, proxy.storage.IncrementToolUsage("b:widget_b"))
	for i := 0; i < 5; i++ {
		require.NoError(t, proxy.storage.IncrementToolUsage("c:widget_c"))
	}

	args := map[string]interface{}{"query": "widget", "limit": float64(10), "include_stats": true, "debug": true}

	cases := []struct {
		name              string
		ctx               context.Context
		wantServers       []string
		wantDebugTotal    int
		wantUsageNames    []string
		wantSessionRisk   string
		wantLethalTrifect bool
	}{
		{
			name:              "a-only token",
			ctx:               agentCtx([]string{"a"}, []string{auth.PermRead}, ""),
			wantServers:       []string{"a"},
			wantDebugTotal:    1,
			wantUsageNames:    []string{"a:widget_a"},
			wantSessionRisk:   "low",
			wantLethalTrifect: false,
		},
		{
			name:              "administrator",
			ctx:               adminCtx(),
			wantServers:       []string{"a", "b", "c"},
			wantDebugTotal:    3,
			wantUsageNames:    []string{"c:widget_c", "a:widget_a", "b:widget_b"},
			wantSessionRisk:   "high",
			wantLethalTrifect: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = args
			result, err := proxy.handleRetrieveToolsWithMode(tc.ctx, req, config.RoutingModeRetrieveTools)
			require.NoError(t, err)
			require.False(t, result.IsError)
			text := result.Content[0].(mcp.TextContent).Text

			var full struct {
				scopeRetrieveResponse
				SessionRisk struct {
					Level          string `json:"level"`
					LethalTrifecta bool   `json:"lethal_trifecta"`
				} `json:"session_risk"`
			}
			require.NoError(t, json.Unmarshal([]byte(text), &full))

			gotServers := map[string]bool{}
			for _, tl := range full.Tools {
				gotServers[tl["server"].(string)] = true
			}
			wantServers := map[string]bool{}
			for _, s := range tc.wantServers {
				wantServers[s] = true
			}
			assert.Equal(t, wantServers, gotServers, "search population")

			assert.Equal(t, tc.wantDebugTotal, full.Debug.TotalIndexedTools, "debug.total_indexed_tools")

			gotUsageNames := make([]string, 0, len(full.UsageSummary.TopTools))
			for _, tt := range full.UsageSummary.TopTools {
				gotUsageNames = append(gotUsageNames, tt["tool_name"].(string))
			}
			assert.Equal(t, tc.wantUsageNames, gotUsageNames, "usage_summary.top_tools population/order")

			assert.Equal(t, tc.wantSessionRisk, full.SessionRisk.Level, "session_risk.level")
			assert.Equal(t, tc.wantLethalTrifect, full.SessionRisk.LethalTrifecta, "session_risk.lethal_trifecta")
		})
	}
}
