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
	// asks for page 1. Spec 105 FR-001 (task T031 inversion): for a scoped
	// caller the refusal is NON-DISCLOSING — byte-identical to the body a key
	// that never existed produces — so it cannot be asserted by its wording.
	// It is kept non-vacuous two ways: the administrator control below proves
	// the key is live (the refusal is the gate, not a miss), and the body is
	// compared against the nonexistent-key body rather than merely IsError.
	adminControl := readCacheAs(t, proxy, auth.WithAuthContext(session, auth.AdminContext()), key, 0)
	require.False(t, adminControl.IsError, "control: the key is live — an administrator pages it")
	absent := readCacheAs(t, proxy, narrow, "0000000000000000000000000000000000000000000000000000000000000000", 0)
	require.True(t, absent.IsError)
	require.Contains(t, resultText(t, absent), "cache key not found")
	for offset := range fullResp.Tools {
		result := readCacheAs(t, proxy, narrow, key, offset)
		assert.True(t, result.IsError, "offset %d: a narrower token must not read a broader token's cache entry", offset)
		assert.Equal(t, resultText(t, absent), resultText(t, result),
			"offset %d: the refusal must be the nonexistent-key body (non-disclosing)", offset)
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
	// Spec 105 FR-001 (task T031 inversion): the revocation is non-disclosing
	// — the stale pin sees the nonexistent-key body, not a wording that
	// confirms the entry exists. The administrator control proves it does.
	absent := readCacheAs(t, proxy, pinned, "0000000000000000000000000000000000000000000000000000000000000000", 0)
	require.True(t, absent.IsError)
	assert.Equal(t, resultText(t, absent), resultText(t, after), "a revoked read must be indistinguishable from a miss")
	assert.Contains(t, resultText(t, after), "cache key not found")
	assert.NotContains(t, resultText(t, after), "github:")
	control := readCacheAs(t, proxy, auth.WithAuthContext(context.Background(), auth.AdminContext()), match[1], 0)
	require.False(t, control.IsError, "control: the entry is still live for an administrator")
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

// Spec 105 PR D review round 17 MUST-FIX: cacheAuthorizationWith must stamp
// ProfileServers with the CALLER-INTERSECTED profile membership
// (profiles.EffectiveServersFor(profileName, ac.AllowedServers)), not the
// resolver's wildcard-derived ProfileScope.AllowedServerNames() — a server
// entirely outside the token's own grant must not affect this token's own
// cache behavior for a server it IS, and remains, authorized to reach.
//
// Scenario (traced by the round-16 reviewer): token AllowedServers={github}
// pinned to profile research={github, weather} — weather sits outside this
// token's own grant, and the token never had, and never will have, access to
// it. An operator later removes weather from the profile (an event this
// token has no authorization to observe or care about). The SAME token's
// cache read for its own authorized server (github) must not be revoked by
// that unrelated removal: before the fix, the cache stamp carried the
// resolver's wildcard-derived full profile membership {github, weather}, so
// the reader's now-narrower resolved set {github} failed the redemption
// set-covering comparison — an unrelated, never-authorized server's presence
// became an observable side-channel through the cache layer (SC-005-class).
func TestReadCache_CacheAuthzIntersectsCallerGrant_HiddenProfileMemberRemovalDoesNotRevoke(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "weather", Enabled: true}}
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github", "weather"}}}
	pIdx, err := proxy.index.ForProfile("research")
	require.NoError(t, err)
	for _, name := range []string{"github:create_issue", "github:list_issues", "github:get_repo", "weather:get_forecast", "weather:search_city"} {
		require.NoError(t, pIdx.IndexTool(&config.ToolMetadata{
			Name: name, ServerName: name[:strings.Index(name, ":")],
			Description: "manage things with " + name, ParamsJSON: `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
			Hash: "hash-" + name,
		}))
	}

	// Token is granted github ONLY — weather sits in the profile, but the
	// token never had, and never will have, access to it.
	pinned := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "pinned", TokenPrefix: "mcp_agt_pinne",
		AllowedServers: []string{"github"}, Permissions: []string{auth.PermRead},
		ProfilePin: "research",
	})

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	full, err := proxy.handleRetrieveTools(pinned, req)
	require.NoError(t, err)
	require.Contains(t, resultText(t, full), "github:", "premise: the token's own grant surfaces github")
	require.NotContains(t, resultText(t, full), "weather:", "premise: weather sits outside the token's own grant and must not be discoverable at all")
	setTruncateLimit(proxy, len(resultText(t, full))/2)
	truncated, err := proxy.handleRetrieveTools(pinned, req)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, truncated))
	require.Len(t, match, 2, "premise: the github-only response must still be large enough to truncate into a keyed page")
	setTruncateLimit(proxy, 1_000_000)

	before := readCacheAs(t, proxy, pinned, match[1], 0)
	require.False(t, before.IsError, "premise: the producing token reads its own entry")

	// Operator removes weather from the profile — a server this token never
	// had, and never will have, access to. This token's OWN cache behavior
	// for its own authorized server (github) must not change.
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github"}}}

	after := readCacheAs(t, proxy, pinned, match[1], 0)
	assert.False(t, after.IsError, "removing an out-of-grant profile member must not revoke this token's own cached access to its own authorized server")
	assert.Contains(t, resultText(t, after), `"records"`)
}

// Companion to the fix above: narrowing a profile to drop a server the token
// WAS itself authorized for (through the profile) must still revoke access to
// entries produced under the wider scope — the caller-intersection fix must
// not weaken this pre-existing guarantee (mirrors
// TestReadCache_DeletedPinnedProfileRevokesCachedAccess at a finer grain: a
// partial narrowing of the token's own reach, not a full profile deletion).
func TestReadCache_CacheAuthzIntersectsCallerGrant_OwnGrantMemberRemovalStillRevokes(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "weather", Enabled: true}}
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github", "weather"}}}
	pIdx, err := proxy.index.ForProfile("research")
	require.NoError(t, err)
	for _, name := range []string{"github:create_issue", "github:list_issues", "github:get_repo", "weather:get_forecast", "weather:search_city"} {
		require.NoError(t, pIdx.IndexTool(&config.ToolMetadata{
			Name: name, ServerName: name[:strings.Index(name, ":")],
			Description: "manage things with " + name, ParamsJSON: `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
			Hash: "hash-" + name,
		}))
	}

	// Token is granted BOTH servers the profile declares.
	pinned := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "pinned", TokenPrefix: "mcp_agt_pinne",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{auth.PermRead},
		ProfilePin: "research",
	})

	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	full, err := proxy.handleRetrieveTools(pinned, req)
	require.NoError(t, err)
	require.Contains(t, resultText(t, full), "github:")
	setTruncateLimit(proxy, len(resultText(t, full))/2)
	truncated, err := proxy.handleRetrieveTools(pinned, req)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, truncated))
	require.Len(t, match, 2)
	setTruncateLimit(proxy, 1_000_000)

	before := readCacheAs(t, proxy, pinned, match[1], 0)
	require.False(t, before.IsError, "premise: the producing token reads its own entry")

	// Operator narrows the profile to drop weather — a server the token WAS,
	// itself, authorized to reach through this profile. That must still
	// revoke this entry, even though the entry's own content came from
	// github (Spec 104 FR-016a compares server SETS, not entry content).
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github"}}}

	after := readCacheAs(t, proxy, pinned, match[1], 0)
	assert.True(t, after.IsError, "narrowing a server the token itself was authorized for must still revoke cached access")
	// The reader is a scoped agent, not an administrator: readCacheRefusal
	// remaps ErrUnauthorizedRead to the uniform ErrKeyNotFound body for any
	// non-administrator caller kind (FR-001 "refusal is non-disclosing") —
	// "not readable with this credential" is the administrator-only wording
	// (cache_authz.go readCacheRefusal, reached only when
	// reader.IsAdministrator()); this pinned agent must see the SAME body a
	// missing key produces, not the reason.
	assert.Contains(t, resultText(t, after), "cache key not found")
}
