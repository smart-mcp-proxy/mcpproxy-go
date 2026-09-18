package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
)

// Every truncation banner mcpproxy emits points the caller at read_cache. That
// banner reaches REST/CLI/Web-UI/tray callers too (POST /api/v1/tools/call →
// CallToolDirect), so read_cache has to be routable there — otherwise the
// advertised follow-up answers "unknown tool: read_cache" with an HTTP 500 and
// the full payload is unreachable outside the MCP surface.

func TestCallToolDirect_ReadCacheIsRoutable(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Name = "read_cache"
	request.Params.Arguments = map[string]interface{}{
		"key": "no-such-key", "offset": float64(0), "limit": float64(50),
	}

	_, err := proxy.CallToolDirect(context.Background(), request)
	require.Error(t, err, "an unresolvable key is still a read_cache answer, not a routing miss")
	assert.NotContains(t, err.Error(), "unknown tool",
		"read_cache must be routed by CallToolDirect, not fall through to the default branch")
	assert.Contains(t, err.Error(), "cache key not found")
}

func TestCallToolDirect_ReadCachePagesStoredRecords(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Spec 105 FR-002 (task T031 inversion): an unstamped Store is legacy
	// provenance and is refused for every caller, so the record is seeded
	// stamped as the anonymous /mcp caller — the authorization the
	// unauthenticated CallToolDirect below acts under.
	const key = "cache-key-direct"
	content := `{"tools":[{"name":"github:get_repo"},{"name":"github:list_issues"}]}`
	require.NoError(t, proxy.cacheManager.StoreAs(key, "retrieve_tools", map[string]interface{}{"query": "manage"}, content, "tools", 2,
		cache.Authorization{CallerKind: cache.CallerKindAnonymous}))

	request := mcp.CallToolRequest{}
	request.Params.Name = "read_cache"
	request.Params.Arguments = map[string]interface{}{
		"key": key, "offset": float64(0), "limit": float64(50),
	}

	result, err := proxy.CallToolDirect(context.Background(), request)
	require.NoError(t, err)

	blocks, ok := result.([]mcp.Content)
	require.True(t, ok, "CallToolDirect returns the raw content blocks")
	require.NotEmpty(t, blocks)
	text, ok := blocks[0].(mcp.TextContent)
	require.True(t, ok)

	var page struct {
		Records []map[string]interface{} `json:"records"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &page))
	require.Len(t, page.Records, 2)
	assert.Equal(t, "github:get_repo", page.Records[0]["name"])
}

// Spec 105 FR-001/FR-010 (gap FR001-G5, task T025): the REST direct call path
// (`POST /api/v1/tools/call` → CallToolDirect) is a read_cache redemption
// door too, and an agent token's refusal there must be as non-disclosing as
// on the MCP surface: a key produced under a broader authorization answers
// with the SAME error text as a key that never existed. Status parity
// already held (both are isError → HTTP 500); the body did not.
func TestCallToolDirect_AgentReadCacheRefusalMatchesNotFound(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	broad := agentCtx([]string{"github", "weather"}, []string{auth.PermRead, auth.PermWrite}, "")
	narrow := agentCtx([]string{"weather"}, []string{auth.PermRead}, "")
	liveKey, _ := produceTruncatedKey(t, proxy, broad)

	readVia := func(ctx context.Context, key string) error {
		request := mcp.CallToolRequest{}
		request.Params.Name = "read_cache"
		request.Params.Arguments = map[string]interface{}{
			"key": key, "offset": float64(0), "limit": float64(50),
		}
		_, err := proxy.CallToolDirect(ctx, request)
		return err
	}

	// Controls: the producer reads its own key here; the administrator's
	// nonexistent-key body is unchanged.
	require.NoError(t, readVia(broad, liveKey), "the producing token reads its entry on the REST path")
	adminAbsent := readVia(adminCtx(), "no-such-key")
	require.Error(t, adminAbsent)
	require.Contains(t, adminAbsent.Error(), "cache key not found")

	live := readVia(narrow, liveKey)
	absent := readVia(narrow, "no-such-key")
	require.Error(t, live, "a narrower token must be refused the broader entry")
	require.Error(t, absent)
	assert.NotContains(t, live.Error(), "github:", "the refusal must not leak the out-of-scope payload")
	assert.Equal(t, absent.Error(), live.Error(),
		"on the REST path a broader-scope key must be indistinguishable from a nonexistent key")
	assert.Contains(t, live.Error(), "cache key not found")
}
