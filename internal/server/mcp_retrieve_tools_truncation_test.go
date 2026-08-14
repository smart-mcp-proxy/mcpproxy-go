package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// retrieve_tools is subject to tool_response_limit like every other tool
// response: an oversize discovery payload is cut at the limit and the full
// payload is parked in the cache behind a read_cache key. Responses that fit
// under the limit stay byte-for-byte what they were before.
//
// The code-execution surface is exempt because it does not register read_cache
// (mcp_routing.go), so a banner there would point at an unavailable tool.

// setTruncateLimit swaps the proxy's live truncator for one with the given
// character limit. It sets truncatorFn (not a captured *Truncator) so the
// handler keeps resolving through currentTruncator(), exactly as production
// does for hot-reloaded limits.
func setTruncateLimit(proxy *MCPProxyServer, limit int) {
	tr := truncate.NewTruncator(limit)
	proxy.truncatorFn = func() *truncate.Truncator { return tr }
}

// cacheKeyRE extracts the cache key from the truncation banner.
var cacheKeyRE = regexp.MustCompile(`key="([^"]+)"`)

func TestRetrieveTools_OversizeResponseTruncatedWithReadCacheHandle(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	// Baseline: the full-mode response for this fixture, untruncated.
	full := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10),
	})

	limit := len(full) / 2
	require.Greater(t, limit, 400, "fixture must be large enough to exercise truncation")
	setTruncateLimit(proxy, limit)

	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10),
	})

	assert.Contains(t, raw, "[truncated by mcpproxy]")
	assert.Contains(t, raw, `Use read_cache tool: key="`)
	assert.LessOrEqual(t, len(raw), limit, "truncated response must fit within tool_response_limit")
}

func TestRetrieveTools_TruncatedPayloadReadableViaReadCache(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	full := callRetrieveRaw(t, proxy, args)

	var fullResp retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(full), &fullResp))
	require.NotEmpty(t, fullResp.Tools)

	setTruncateLimit(proxy, len(full)/2)
	raw := callRetrieveRaw(t, proxy, args)

	match := cacheKeyRE.FindStringSubmatch(raw)
	require.Len(t, match, 2, "truncated response must carry a read_cache key")

	// Raise the limit for the read-back so the assertion is about what landed
	// in the cache, not about read_cache's own (separately covered) recursive
	// truncation — the fixture's page is larger than this test's tiny limit.
	setTruncateLimit(proxy, 1_000_000)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"key": match[1], "offset": float64(0), "limit": float64(50),
	}
	result, err := proxy.handleReadCache(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "read_cache must resolve the key minted by retrieve_tools")

	var cached struct {
		Records []map[string]interface{} `json:"records"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &cached))

	// The cache holds the pre-truncation payload, paginated over the tools array.
	require.Len(t, cached.Records, len(fullResp.Tools))
	assert.Equal(t, fullResp.Tools[0]["name"], cached.Records[0]["name"])
	assert.Contains(t, cached.Records[0], "inputSchema")
}

// seedLockedHeavyFixture builds the payload shape that breaks a heuristic
// record-path pick: few callable tools with fat schemas, many locked entries
// with thin ones. The "disabled" array outnumbers "tools" by more than 2x, so
// the truncator's chooseBestArray prefers it — but the banner retrieve_tools
// emits promises tool entries.
func seedLockedHeavyFixture(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "s", Enabled: true}))

	props := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		props = append(props, fmt.Sprintf(`"field_%d":{"type":"string","description":"padding padding padding padding"}`, i))
	}
	fatSchema := `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`

	for _, name := range []string{"callable_one", "callable_two"} {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "s:" + name, ServerName: "s",
			Description: "widget helper thing",
			ParamsJSON:  fatSchema,
			Hash:        "hash-" + name,
		}))
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("locked_%02d", i)
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "s", ToolName: name,
			Status: storage.ToolApprovalStatusApproved, Disabled: true,
		}))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "s:" + name, ServerName: "s",
			Description: "widget helper thing",
			ParamsJSON:  `{"type":"object"}`,
			Hash:        "hash-" + name,
		}))
	}
}

// The truncation banner advertises exactly one pagination contract: read_cache
// pages the tool entries the response would have carried. It must never hand
// back the Spec 049 locked entries (or a schema's nested array) under that
// promise — an agent paging a truncated discovery result would silently read
// records that are not tools, and the tools past the cut would be unreachable.
func TestRetrieveTools_TruncationPagesToolsNotLockedEntries(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedLockedHeavyFixture(t, proxy)

	args := map[string]interface{}{
		"query": "widget helper thing", "limit": float64(20), "include_disabled": true,
	}
	full := callRetrieveRaw(t, proxy, args)

	var fullResp retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(full), &fullResp))
	require.Len(t, fullResp.Tools, 2, "fixture must keep the tools array small")
	require.Greater(t, len(fullResp.Disabled), 2*len(fullResp.Tools),
		"fixture must make the locked array outnumber the tools array by >2x")

	limit := len(full) / 2
	require.Greater(t, limit, 400, "fixture must be large enough to exercise truncation")
	setTruncateLimit(proxy, limit)

	raw := callRetrieveRaw(t, proxy, args)
	require.Contains(t, raw, "[truncated by mcpproxy]")
	assert.Contains(t, raw, fmt.Sprintf("records: %d", len(fullResp.Tools)),
		"the banner's record count must describe the tools array it promises")

	match := cacheKeyRE.FindStringSubmatch(raw)
	require.Len(t, match, 2, "truncated response must carry a read_cache key")

	setTruncateLimit(proxy, 1_000_000)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"key": match[1], "offset": float64(0), "limit": float64(50),
	}
	result, err := proxy.handleReadCache(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	var cached struct {
		Records []map[string]interface{} `json:"records"`
		Meta    struct {
			RecordPath   string `json:"record_path"`
			TotalRecords int    `json:"total_records"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &cached))

	assert.Equal(t, "tools", cached.Meta.RecordPath)
	require.Len(t, cached.Records, len(fullResp.Tools))
	for i, rec := range cached.Records {
		assert.Equal(t, fullResp.Tools[i]["name"], rec["name"])
		assert.Contains(t, rec, "inputSchema", "a paged record must be a tool entry")
		assert.NotContains(t, rec, "status", "a paged record must not be a locked entry")
	}
}

func TestRetrieveTools_UnderLimitByteIdentical(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}

	// Truncation disabled (the shipped default when tool_response_limit is 0).
	disabled := callRetrieveRaw(t, proxy, args)

	// A limit far above the response size must leave it untouched.
	setTruncateLimit(proxy, 1_000_000)
	underLimit := callRetrieveRaw(t, proxy, args)

	assert.Equal(t, disabled, underLimit, "responses under the limit must be byte-identical")
	assert.NotContains(t, underLimit, "[truncated by mcpproxy]")
}

func TestRetrieveTools_CodeExecModeNeverTruncated(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	full := callRetrieveRaw(t, proxy, args)
	setTruncateLimit(proxy, len(full)/2)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := proxy.handleRetrieveToolsWithMode(context.Background(), req, config.RoutingModeCodeExecution)
	require.NoError(t, err)
	require.False(t, result.IsError)

	raw := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, raw, "[truncated by mcpproxy]",
		"the code-execution surface has no read_cache tool, so its responses stay whole")
	assert.Greater(t, len(raw), len(full)/2)
}

func TestRetrieveTools_SingleOversizeEntryPassesThrough(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "bulky", Enabled: true,
	}))
	props := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		props = append(props, fmt.Sprintf(`"field_%d":{"type":"string","description":"padding padding padding"}`, i))
	}
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "bulky:megaschema", ServerName: "bulky",
		Description: "A singular sprawling tool used to manage everything.",
		ParamsJSON:  `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`,
		Hash:        "hash-megaschema",
	}))

	args := map[string]interface{}{"query": "megaschema", "limit": float64(10)}
	full := callRetrieveRaw(t, proxy, args)
	require.Greater(t, len(full), 2000)

	setTruncateLimit(proxy, 2000)
	raw := callRetrieveRaw(t, proxy, args)

	// Nothing to subdivide with a single record — the oversize text flows
	// through intact rather than minting a key that resolves to itself.
	assert.Equal(t, full, raw)
	assert.NotContains(t, raw, "[truncated by mcpproxy]")
}
