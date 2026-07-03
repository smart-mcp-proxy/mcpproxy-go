package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Quarantined tools are intentionally absent from the search index (their
// untrusted descriptions are withheld to avoid Tool Poisoning Attack exposure),
// so retrieve_tools' index loop can never surface them. These tests cover the
// discovery second pass that enumerates them from authoritative state, the
// description-withholding, the config-denied precedence, the dedup/scope/short-
// keyword handling, and the fact that surfaced quarantined tools stay
// non-callable at the call path.

func findDisabled(entries []contracts.LockedToolEntry, name string) (contracts.LockedToolEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return contracts.LockedToolEntry{}, false
}

// allowAll is the scope filter used in unit tests (no agent scope / profile).
func allowAll(string) bool { return true }

// A tool-level-quarantined (pending) tool on a trusted server is surfaced by
// include_disabled when its NAME matches the query — description withheld,
// pending_approval status + remediation.
func TestQuarantineDiscovery_PendingToolSurfacedByName(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "android", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "android", ToolName: "emulator_build_web",
		Status: storage.ToolApprovalStatusPending,
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "build emulator web", "limit": float64(10), "include_disabled": true,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)

	entry, ok := findDisabled(resp.Disabled, "android:emulator_build_web")
	require.True(t, ok, "quarantined tool must appear in disabled[] (got %+v)", resp.Disabled)
	assert.Equal(t, contracts.DisabledStatusPendingApproval, entry.Status)
	assert.Empty(t, entry.Description, "quarantined tool description must be withheld (TPA payload)")
	assert.Contains(t, resp.Remediation, contracts.DisabledStatusPendingApproval)
	assert.Empty(t, resp.Tools, "quarantined tool must not be a callable result")
}

// The second pass is query-scoped: a quarantined tool whose name does not match
// the query is NOT dumped into the results.
func TestQuarantineDiscovery_NameMustMatchQuery(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "android", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "android", ToolName: "emulator_build_web",
		Status: storage.ToolApprovalStatusChanged,
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "frobnicate widget", "limit": float64(10), "include_disabled": true,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)

	_, ok := findDisabled(resp.Disabled, "android:emulator_build_web")
	assert.False(t, ok, "non-matching quarantined tool must not be surfaced")
}

// Even without include_disabled, a query that matches only a quarantined tool
// gets the one-line nudge (droppedCount is bumped by the second pass) without
// inlining any locked entries.
func TestQuarantineDiscovery_ZeroResultNudge(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "android", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "android", ToolName: "emulator_build_web",
		Status: storage.ToolApprovalStatusPending,
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "build emulator web"}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "include_disabled:true")
	assert.Contains(t, text, "locked")
	assert.Nil(t, resp.Disabled, "nudge must not inline locked entries")
}

// Server-level quarantine: every tool on a Quarantined server is surfaced as a
// name-only locked entry with server_quarantined status. serverToolNames needs
// a wired runtime, so the tool-names source is injected here to exercise the
// branch (the production call passes p.serverToolNames).
func TestQuarantineDiscovery_ServerLevelBranch(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "collar-emu", Enabled: true, Quarantined: true,
	}))
	toolNames := func(string) []string { return []string{"emulator_build_web", "unrelated_tool"} }

	matches := proxy.collectQuarantinedToolMatches("build emulator", allowAll, map[string]bool{}, toolNames)

	require.Len(t, matches, 1, "only the name-matching tool surfaces (got %+v)", matches)
	assert.Equal(t, "collar-emu:emulator_build_web", matches[0].Name)
	assert.Equal(t, contracts.DisabledStatusServerQuarantined, matches[0].Status)
	assert.Empty(t, matches[0].Description, "description must be withheld")
}

// A pending tool that is ALSO denied by operator config must NOT be advertised
// as pending_approval — approving it in the UI cannot make it callable.
func TestQuarantineDiscovery_ConfigDeniedSkipped(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "android", Enabled: true, DisabledTools: []string{"emulator_build_web"},
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "android", ToolName: "emulator_build_web",
		Status: storage.ToolApprovalStatusPending,
	}))

	matches := proxy.collectQuarantinedToolMatches("build emulator web", allowAll, map[string]bool{}, proxy.serverToolNames)
	assert.Empty(t, matches, "config-denied tool must not be surfaced as pending_approval")
}

// Tools already handled by the index loop (passed in via `seen`) are not
// surfaced again by the second pass.
func TestQuarantineDiscovery_DedupAgainstSeen(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "android", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "android", ToolName: "emulator_build_web",
		Status: storage.ToolApprovalStatusPending,
	}))

	seen := map[string]bool{"android:emulator_build_web": true}
	matches := proxy.collectQuarantinedToolMatches("build emulator", allowAll, seen, proxy.serverToolNames)
	assert.Empty(t, matches, "already-seen tool must not be surfaced twice")
}

// A quarantined tool stays non-callable at the call path even though discovery
// surfaces it.
func TestQuarantinedTool_VisibleButNotCallable(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "collar-emu", Enabled: true, Quarantined: true,
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "collar-emu", "emulator_build_web", map[string]interface{}{})
	require.NotNil(t, result, "a quarantined server's tool call must be blocked")
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &response))
	assert.Equal(t, "QUARANTINED_SERVER_BLOCKED", response["status"])
}

// Short capability keywords ("ui", "qa", ...) are retained so they can match
// quarantined tool names; an all-single-char query falls back rather than
// returning nothing.
func TestQueryTokens_ShortKeywords(t *testing.T) {
	assert.Equal(t, []string{"ui", "tap"}, queryTokens("ui tap"))
	assert.Equal(t, []string{"qa", "build"}, queryTokens("qa build!"))
	assert.Equal(t, []string{"a"}, queryTokens("a"), "single-char fallback")
	assert.Empty(t, queryTokens("   "))
}

// Every status the discovery path can emit must have a non-empty, actionable
// remediation string.
func TestDisabledToolRemediation_ServerQuarantined(t *testing.T) {
	msg := disabledToolRemediation(contracts.DisabledStatusServerQuarantined)
	assert.NotEmpty(t, msg)
	assert.Contains(t, msg, "quarantined")
	assert.Contains(t, msg, "approve")
}
