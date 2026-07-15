package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 085 T011 — full-mode byte-identity golden (FR-006/SC-003).
//
// The golden files under testdata/ were captured from the retrieve_tools
// handler BEFORE the entry-builder extraction (T012) and the shared-visibility
// rewiring (T010), so any post-refactor byte difference in the full-mode
// response is a regression. Regenerate ONLY when a deliberate, spec-approved
// response change lands: UPDATE_GOLDEN=1 go test ./internal/server -run
// TestRetrieveToolsFullMode_GoldenByteIdentity
//
// The comparison is trailing-newline-tolerant (strings.TrimRight on both
// sides) because the repo pre-commit hooks append trailing newlines to
// committed fixtures.

// seedEntryBuilderFixture indexes a deterministic corpus: two enabled servers,
// five tools with varied schemas (scalars+defaults, enums, nested objects,
// arrays, empty schema). No quarantine/pending/disabled records — this fixture
// exercises the plain full-mode entry path only.
func seedEntryBuilderFixture(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true,
	}))
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "weather", Enabled: true,
	}))

	tools := []*config.ToolMetadata{
		{
			Name: "github:create_issue", ServerName: "github",
			Description: "Create an issue to manage work. Supports labels and assignees.",
			ParamsJSON:  `{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"labels":{"type":"array","items":{"type":"string"}},"ttl":{"type":"integer","default":3600}},"required":["title"]}`,
			Hash:        "hash-create-issue",
		},
		{
			Name: "github:list_issues", ServerName: "github",
			Description: "List issues to manage a repository backlog.",
			ParamsJSON:  `{"type":"object","properties":{"state":{"enum":["open","closed","all"]},"repo":{"type":"string"}},"required":["repo"]}`,
			Hash:        "hash-list-issues",
		},
		{
			Name: "github:get_repo", ServerName: "github",
			Description: "Get repository metadata to manage projects.",
			ParamsJSON:  `{"type":"object","properties":{}}`,
			Hash:        "hash-get-repo",
		},
		{
			Name: "weather:get_forecast", ServerName: "weather",
			Description: "Get a weather forecast to manage travel plans.",
			ParamsJSON:  `{"type":"object","properties":{"location":{"type":"object","properties":{"lat":{"type":"number"},"lon":{"type":"number"}}},"days":{"type":"integer"}},"required":["location"]}`,
			Hash:        "hash-get-forecast",
		},
		{
			Name: "weather:search_city", ServerName: "weather",
			Description: "Search cities to manage location lookups across regions worldwide.",
			ParamsJSON:  `{"type":"object","properties":{"q":{"type":"string"},"id":{"type":["string","integer"]}},"required":["q"]}`,
			Hash:        "hash-search-city",
		},
	}
	for _, tool := range tools {
		require.NoError(t, proxy.index.IndexTool(tool))
	}
}

// callRetrieveRaw invokes the retrieve_tools handler and returns the raw
// serialized response text (the exact bytes an MCP client receives).
func callRetrieveRaw(t *testing.T, proxy *MCPProxyServer, args map[string]interface{}) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "retrieve_tools returned an error result")
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content")
	return text.Text
}

// compareGolden asserts got matches the committed golden file byte-for-byte,
// tolerating only a trailing newline (added by pre-commit hooks). Set
// UPDATE_GOLDEN=1 to (re)capture.
func compareGolden(t *testing.T, goldenPath, got string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(got+"\n"), 0o644))
		t.Logf("golden updated: %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file missing — run with UPDATE_GOLDEN=1 to capture")
	assert.Equal(t,
		strings.TrimRight(string(want), "\n"),
		strings.TrimRight(got, "\n"),
		"full-mode retrieve_tools response must be byte-identical to the pre-refactor capture (FR-006/SC-003)")
}

func TestRetrieveToolsFullMode_GoldenByteIdentity(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	t.Run("default", func(t *testing.T) {
		got := callRetrieveRaw(t, proxy, map[string]interface{}{
			"query": "manage", "limit": float64(10),
		})
		compareGolden(t, filepath.Join("testdata", "retrieve_full_default.golden.json"), got)
	})

	t.Run("include_stats", func(t *testing.T) {
		got := callRetrieveRaw(t, proxy, map[string]interface{}{
			"query": "manage", "limit": float64(10), "include_stats": true,
		})
		compareGolden(t, filepath.Join("testdata", "retrieve_full_stats.golden.json"), got)
	})
}

// --- FR-006/FR-007 result-set parity with the merge-base (main) ---
//
// The extracted visibility path must reproduce the merge-base FULL-mode
// filter semantics EXACTLY. Expected behavior below is derived by reading the
// merge-base code (commit 95cfcfed):
//
//   - git show main:internal/server/mcp.go, handleRetrieveToolsWithMode
//     filter loop (~:1345-1363): each index hit passes ONLY
//     serverDiscoverable (agent scope + profile scope) then isToolCallable —
//     no server-quarantine check, no pending/changed approval gate at this
//     point. Non-callable hits go to droppedCount/disabled[].
//   - git show main:internal/server/mcp.go, isToolCallable (~:5306-5357):
//     server exists + Enabled + not config-denied + approval.Disabled==false.
//     It never consults ServerConfig.Quarantined and never consults
//     approval.Status — so PENDING and CHANGED tools that are in the index
//     are callable results, and a quarantined server's tool that still
//     lingers in the index (transient toggle window) is returned too; the
//     quarantine second pass (collectQuarantinedToolMatches) dedupes it via
//     `seen` instead of re-adding it as a locked entry.
//
// Any drift here (e.g. gating pending/changed or quarantined-lingering tools
// out of SEARCH) violates FR-006 byte-identity. describe_tool is allowed to
// be stricter (contracts/describe_tool.md), search is not.
func TestRetrieveToolsFullMode_MergeBaseFilterSemantics(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true,
	}))
	// Enabled but quarantined: models the transient window where its tool is
	// still indexed after a runtime quarantine toggle.
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "quarry", Enabled: true, Quarantined: true,
	}))

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "pending_tool",
		Status: storage.ToolApprovalStatusPending,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "changed_tool",
		Status: storage.ToolApprovalStatusChanged,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "disabled_tool",
		Status: storage.ToolApprovalStatusApproved, Disabled: true,
	}))

	for _, tool := range []struct{ name, server, desc string }{
		{"github:plain_tool", "github", "mergebasequery alpha"},
		{"github:pending_tool", "github", "mergebasequery beta two"},
		{"github:changed_tool", "github", "mergebasequery gamma three words"},
		{"github:disabled_tool", "github", "mergebasequery delta four words here"},
		{"quarry:lingering_tool", "quarry", "mergebasequery epsilon five words here now"},
	} {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: tool.name, ServerName: tool.server,
			Description: tool.desc, ParamsJSON: `{"type":"object"}`,
		}))
	}

	cases := []struct {
		tool string
		want bool // returned by full-mode retrieve_tools per merge-base semantics
	}{
		{"github:plain_tool", true},
		// main:internal/server/mcp.go isToolCallable (~:5405-5410) checks only
		// approval.Disabled — a pending/changed Status passes.
		{"github:pending_tool", true},
		{"github:changed_tool", true},
		// approval.Disabled=true → !isToolCallable → dropped (~:1349).
		{"github:disabled_tool", false},
		// isToolCallable (~:5388-5395) checks Enabled only, never Quarantined.
		{"quarry:lingering_tool", true},
	}

	got := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": "mergebasequery", "limit": float64(20),
	})
	var resp retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(got), &resp))
	names := map[string]bool{}
	for _, entry := range resp.Tools {
		names[entry["name"].(string)] = true
	}

	for _, c := range cases {
		assert.Equal(t, c.want, names[c.tool],
			"FULL-mode result-set drift from merge-base for %s (FR-006): merge-base filter = scope -> isToolCallable only", c.tool)
	}
	assert.Equal(t, 4, resp.Total, "merge-base returns 4 of the 5 fixture tools")
}

// --- Spec 085 US1 T018 (FR-002): compact entry shape ---
//
// A compact entry carries EXACTLY {id, score, sig, desc, lossy}: no
// inputSchema, no full description, no annotations block — those move to
// describe_tool / self-healing errors. id is derived exactly as the
// full-mode "name" (result.Tool.Name), so ranked order and identity are
// untouched by construction.

func TestBuildToolEntry_CompactShape(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	result := &config.SearchResult{
		Tool: &config.ToolMetadata{
			Name: "github:create_issue", ServerName: "github",
			Description: "Create an issue to track work. Supports labels and assignees.",
			ParamsJSON:  `{"type":"object","properties":{"title":{"type":"string"},"ttl":{"type":"integer","default":3600}},"required":["title"]}`,
			Hash:        "hash-compact-shape",
		},
		Score: 0.42,
	}

	entry := proxy.buildToolEntry(result, config.ToolResponseModeCompact, toolEntryOpts{includeStats: true})

	assert.Equal(t, "github:create_issue", entry["id"], "id must equal the full-mode name (server:tool)")
	assert.Equal(t, 0.42, entry["score"], "relevance score must survive compaction")
	assert.Equal(t, "(title*:str, ttl:int=3600)", entry["sig"])
	assert.Equal(t, "Create an issue to track work.", entry["desc"], "desc must be the verbatim first sentence")
	assert.Equal(t, false, entry["lossy"])

	assert.Len(t, entry, 5, "compact entry must carry exactly {id, score, sig, desc, lossy}")
	for _, forbidden := range []string{"inputSchema", "description", "annotations", "name", "server", "call_with", "usage_count", "last_used"} {
		assert.NotContains(t, entry, forbidden, "compact entries must not leak full-mode field %q", forbidden)
	}
}

func TestBuildToolEntry_CompactLossyNestedRequired(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	result := &config.SearchResult{
		Tool: &config.ToolMetadata{
			Name: "billing:create_account", ServerName: "billing",
			Description: "Create a billing account.",
			ParamsJSON:  `{"type":"object","properties":{"name":{"type":"string"},"account":{"type":"object","properties":{"id":{"type":"string"}}}},"required":["name","account"]}`,
			Hash:        "hash-compact-lossy",
		},
		Score: 0.9,
	}

	entry := proxy.buildToolEntry(result, config.ToolResponseModeCompact, toolEntryOpts{})

	// Grammar E3: required nested object keeps its name + "*" and collapses
	// internally under "~" — the never-elide-required hard invariant (FR-003).
	assert.Equal(t, "(name*:str, account*~:obj)", entry["sig"])
	assert.Equal(t, true, entry["lossy"], "collapsed params must flag the entry lossy (FR-004)")
}

// The compact branch must read the SHARED signature cache — a pre-warmed
// hash is a pure cache hit (FR-008: compiled at index time, not per request).
func TestBuildToolEntry_CompactUsesSigCache(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	schema := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`
	proxy.sigCache.Warm("hash-warmed", schema, "Search things. Second sentence.")
	compiled := proxy.sigCache.CompileCount()

	result := &config.SearchResult{
		Tool: &config.ToolMetadata{
			Name: "search:query", ServerName: "search",
			Description: "Search things. Second sentence.",
			ParamsJSON:  schema,
			Hash:        "hash-warmed",
		},
		Score: 1.0,
	}
	entry := proxy.buildToolEntry(result, config.ToolResponseModeCompact, toolEntryOpts{})

	assert.Equal(t, "(q*:str)", entry["sig"])
	assert.Equal(t, compiled, proxy.sigCache.CompileCount(),
		"building a compact entry for a warmed hash must not compile (FR-008)")
}

// Defensive: a tool with no hash (never true for indexed tools) must not
// memoize under a shared empty key — two hashless tools with different
// schemas must render their own signatures, not collide.
func TestBuildToolEntry_CompactEmptyHashNoCollision(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	first := &config.SearchResult{Tool: &config.ToolMetadata{
		Name: "a:one", ServerName: "a", Description: "One.",
		ParamsJSON: `{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`,
	}, Score: 1}
	second := &config.SearchResult{Tool: &config.ToolMetadata{
		Name: "b:two", ServerName: "b", Description: "Two.",
		ParamsJSON: `{"type":"object","properties":{"y":{"type":"integer"}},"required":["y"]}`,
	}, Score: 1}

	e1 := proxy.buildToolEntry(first, config.ToolResponseModeCompact, toolEntryOpts{})
	e2 := proxy.buildToolEntry(second, config.ToolResponseModeCompact, toolEntryOpts{})
	assert.Equal(t, "(x*:str)", e1["sig"])
	assert.Equal(t, "(y*:int)", e2["sig"], "hashless tools must not share a cache slot")
}

// The stats fields carry wall-clock timestamps, so they cannot live in the
// byte-golden. This covers the include_stats per-entry branch functionally:
// seeded usage must surface as usage_count/last_used on the matching entry.
func TestRetrieveToolsFullMode_StatsFieldsFlowThrough(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	require.NoError(t, proxy.storage.IncrementToolUsage("github:create_issue"))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "manage", "limit": float64(10), "include_stats": true,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)

	var found bool
	for _, entry := range resp.Tools {
		if entry["name"] == "github:create_issue" {
			found = true
			assert.Equal(t, float64(1), entry["usage_count"], "usage_count must flow through")
			assert.Contains(t, entry, "last_used")
		} else {
			assert.NotContains(t, entry, "usage_count", "tools without usage must omit stats fields")
		}
	}
	require.True(t, found, "seeded tool must be in results")
}
