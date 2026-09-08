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
)

// A read_cache key is minted for whoever produced the truncated response. The
// key is a hash, not a credential: nothing about it says which authorization
// generated the payload behind it. Spec 104 FR-016a: a cache entry must carry
// the authorization it was produced under, and read_cache must refuse any
// request whose own authorization could not have produced that entry — on
// every page, not just the first.
//
// Scenario: a broad agent token (github + weather) truncates a retrieve_tools
// response that lists github tools. A narrower token (weather only) on the SAME
// MCP session then asks read_cache for the key. Before the fix it got the
// github tools it could never have discovered itself.

func readCacheAs(t *testing.T, proxy *MCPProxyServer, ctx context.Context, key string, offset int) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"key": key, "offset": float64(offset), "limit": float64(1),
	}
	result, err := proxy.handleReadCache(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func TestReadCache_NarrowerTokenOnSameSessionCannotReadBroaderEntry(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	helper := mcpserver.NewMCPServer("test", "1.0.0")
	session := helper.WithContext(context.Background(), &fakeClientSession{id: "shared-session"})

	broad := auth.WithAuthContext(session, &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "broad", TokenPrefix: "mcp_agt_broad",
		AllowedServers: []string{"github", "weather"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	})
	narrow := auth.WithAuthContext(session, &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "narrow", TokenPrefix: "mcp_agt_narro",
		AllowedServers: []string{"weather"},
		Permissions:    []string{auth.PermRead},
	})

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args

	full, err := proxy.handleRetrieveTools(broad, req)
	require.NoError(t, err)
	require.False(t, full.IsError)
	var fullResp retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, full)), &fullResp))
	require.GreaterOrEqual(t, len(fullResp.Tools), 2, "fixture must yield at least two pages of one record")
	require.Contains(t, resultText(t, full), "github:", "premise: the broad response lists a github tool the narrow token cannot see")

	setTruncateLimit(proxy, len(resultText(t, full))/2)
	truncated, err := proxy.handleRetrieveTools(broad, req)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, truncated))
	require.Len(t, match, 2, "truncated response must carry a read_cache key")
	key := match[1]
	setTruncateLimit(proxy, 1_000_000)

	// Every page, not only the first: an attacker who is refused page 0 just
	// asks for page 1. The refusal must be THE authorization refusal — a
	// key-not-found or any other error would pass a vacuous "IsError" check.
	for offset := range fullResp.Tools {
		result := readCacheAs(t, proxy, narrow, key, offset)
		assert.True(t, result.IsError, "offset %d: a narrower token must not read a broader token's cache entry", offset)
		assert.Contains(t, resultText(t, result), "not readable with this credential",
			"offset %d: refusal must be the authorization gate, not an unrelated failure", offset)
		assert.NotContains(t, resultText(t, result), "github:",
			"offset %d: refused read must not leak the out-of-scope payload", offset)
		assert.NotContains(t, resultText(t, result), `"records"`,
			"offset %d: refused read must not return a page at all", offset)
	}

	// Same authorization keeps reading exactly as before.
	for offset := range fullResp.Tools {
		result := readCacheAs(t, proxy, broad, key, offset)
		require.False(t, result.IsError, "offset %d: the producing authorization must still read its own entry", offset)
		var page struct {
			Records []map[string]interface{} `json:"records"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &page))
		require.Len(t, page.Records, 1)
		assert.Equal(t, fullResp.Tools[offset]["name"], page.Records[0]["name"])
	}

	// The narrow token is not blanket-refused: it reads its OWN entries.
	setTruncateLimit(proxy, 1_000_000)
	ownFull, err := proxy.handleRetrieveTools(narrow, req)
	require.NoError(t, err)
	setTruncateLimit(proxy, len(resultText(t, ownFull))/2)
	ownTrunc, err := proxy.handleRetrieveTools(narrow, req)
	require.NoError(t, err)
	ownMatch := cacheKeyRE.FindStringSubmatch(resultText(t, ownTrunc))
	require.Len(t, ownMatch, 2, "the narrow token's own oversize response must mint a key")
	setTruncateLimit(proxy, 1_000_000)
	own := readCacheAs(t, proxy, narrow, ownMatch[1], 0)
	require.False(t, own.IsError, "a token must be able to page the entry it produced itself: %s", resultText(t, own))
	assert.NotContains(t, resultText(t, own), "github:")
}

// Deleting (or narrowing) a pinned profile after the entry was produced must
// revoke cached access too: a stale pin resolves to a deny-all scope under the
// SAME profile name, so a name-only comparison would keep the payload readable
// for the cache TTL after the operator removed the profile.
func TestReadCache_DeletedPinnedProfileRevokesCachedAccess(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	// EffectiveServers only keeps servers the config knows about.
	proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "weather", Enabled: true}}
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github", "weather"}}}
	// Mimic the production profile-index sync (Profiles v2): the per-profile
	// index physically holds the profile's servers' tools.
	pIdx, err := proxy.index.ForProfile("research")
	require.NoError(t, err)
	for _, name := range []string{"github:create_issue", "github:list_issues", "github:get_repo", "weather:get_forecast", "weather:search_city"} {
		require.NoError(t, pIdx.IndexTool(&config.ToolMetadata{
			Name: name, ServerName: name[:strings.Index(name, ":")],
			Description: "manage things with " + name, ParamsJSON: `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
			Hash: "hash-" + name,
		}))
	}

	pinned := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "pinned", TokenPrefix: "mcp_agt_pinne",
		AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ProfilePin: "research",
	})

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	full, err := proxy.handleRetrieveTools(pinned, req)
	require.NoError(t, err)
	require.Contains(t, resultText(t, full), "github:", "premise: the pinned profile exposes github")
	require.GreaterOrEqual(t, len(resultText(t, full)), 800, "premise: the response must be large enough to truncate into a keyed page")
	setTruncateLimit(proxy, len(resultText(t, full))/2)
	truncated, err := proxy.handleRetrieveTools(pinned, req)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, truncated))
	require.Len(t, match, 2)
	setTruncateLimit(proxy, 1_000_000)

	before := readCacheAs(t, proxy, pinned, match[1], 0)
	require.False(t, before.IsError, "while the profile exists the producing token reads its entry")

	// Operator deletes the profile. The pin now resolves to deny-all.
	proxy.config.Profiles = nil

	after := readCacheAs(t, proxy, pinned, match[1], 0)
	assert.True(t, after.IsError, "a deleted pinned profile must revoke cached access")
	assert.Contains(t, resultText(t, after), "not readable with this credential")
	assert.NotContains(t, resultText(t, after), "github:")
}

// The reverse direction stays open: an admin (unrestricted) reader could have
// produced anything an agent token produced.
func TestReadCache_BroaderReaderMayReadNarrowerEntry(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	agent := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "narrow", TokenPrefix: "mcp_agt_narro",
		AllowedServers: []string{"github", "weather"},
		Permissions:    []string{auth.PermRead},
	})

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	full, err := proxy.handleRetrieveTools(agent, req)
	require.NoError(t, err)
	setTruncateLimit(proxy, len(resultText(t, full))/2)
	truncated, err := proxy.handleRetrieveTools(agent, req)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, truncated))
	require.Len(t, match, 2)
	setTruncateLimit(proxy, 1_000_000)

	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())
	result := readCacheAs(t, proxy, admin, match[1], 0)
	assert.False(t, result.IsError, "an unrestricted admin could have produced the entry, so it may read it")
}
