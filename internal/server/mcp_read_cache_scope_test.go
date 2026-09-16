package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 FR-001/FR-002 at the read_cache handler (tasks T021, T022, T024,
// T025; gaps FR001-G1, G2, G4, G5). The cache-package tests pin the gate;
// these pin what the MCP surface does with the gate's answer: which callers
// are refused, what a refusal looks like, and whose snapshot a recursive
// child page inherits.

// allPerms is every permission tier — HasPermission is exact-match, so an
// "unrestricted" agent fixture must spell them all out.
var allPerms = []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}

// userCtx returns a context authenticated as a server-edition OAuth user —
// the `user` caller kind, bounded to its own identity (Spec 024).
func userCtx(id string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeUser, UserID: id, Email: id + "@example.com", Role: "user",
	})
}

// readCachePage is readCacheAs with a caller-chosen page size; the shared
// helper pages one record at a time, which can never re-truncate.
func readCachePage(t *testing.T, proxy *MCPProxyServer, ctx context.Context, key string, offset, limit int) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"key": key, "offset": float64(offset), "limit": float64(limit),
	}
	result, err := proxy.handleReadCache(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// produceTruncatedKey runs retrieve_tools as ctx under a limit that forces
// truncation and returns the minted read_cache key plus the untruncated
// response for comparison. The limit is restored afterwards.
func produceTruncatedKey(t *testing.T, proxy *MCPProxyServer, ctx context.Context) (key string, full retrieveToolsResponse) {
	t.Helper()
	args := map[string]interface{}{"query": "manage", "limit": float64(10)}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args

	setTruncateLimit(proxy, 1_000_000)
	fullResult, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	require.False(t, fullResult.IsError, resultText(t, fullResult))
	require.NoError(t, json.Unmarshal([]byte(resultText(t, fullResult)), &full))
	require.GreaterOrEqual(t, len(full.Tools), 2, "fixture must yield at least two records")

	setTruncateLimit(proxy, len(resultText(t, fullResult))/2)
	truncated, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, truncated))
	require.Len(t, match, 2, "truncated response must carry a read_cache key")
	setTruncateLimit(proxy, 1_000_000)
	return match[1], full
}

// expireCacheEntry rewrites the entry's expiry into the past in place, the
// way the cache package's own expiry test does.
func expireCacheEntry(t *testing.T, proxy *MCPProxyServer, key string) {
	t.Helper()
	require.NoError(t, proxy.storage.GetDB().Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(cache.CacheBucket))
		data := bucket.Get([]byte(key))
		require.NotNil(t, data, "premise: entry %q exists", key)
		var rec cache.Record
		require.NoError(t, rec.UnmarshalBinary(data))
		rec.ExpiresAt = time.Now().Add(-time.Hour)
		out, err := rec.MarshalBinary()
		require.NoError(t, err)
		return bucket.Put([]byte(key), out)
	}))
}

// FR001-G1 at the handler: a legacy (nil-producer) entry is refused for the
// administrator and for the anonymous /mcp caller — the two kinds that could
// redeem it before — with no `records` in the answer.
func TestReadCache_LegacyEntryRefusedForAdminAndAnonymous(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	const key = "legacy-entry"
	require.NoError(t, proxy.cacheManager.Store(key, "retrieve_tools", map[string]interface{}{"query": "manage"},
		`{"tools":[{"name":"github:SENTINEL_LEGACY"},{"name":"github:second"}]}`, "tools", 2))

	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"admin", adminCtx()},
		{"anonymous (no auth context)", context.Background()},
		{"anonymous (admin-shaped context)", auth.WithAuthContext(context.Background(), auth.AnonymousContext())},
		{"wildcard agent", agentCtx([]string{"*"}, allPerms, "")},
		{"user (server edition)", userCtx("01HUSER")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Re-seed: the first refused redemption invalidates the entry.
			require.NoError(t, proxy.cacheManager.Store(key, "retrieve_tools", map[string]interface{}{"query": "manage"},
				`{"tools":[{"name":"github:SENTINEL_LEGACY"},{"name":"github:second"}]}`, "tools", 2))
			result := readCachePage(t, proxy, tc.ctx, key, 0, 50)
			assert.True(t, result.IsError, "%s must be refused a legacy entry: %s", tc.name, resultText(t, result))
			assert.NotContains(t, resultText(t, result), `"records"`, "a refused legacy read must not return a page")
			assert.NotContains(t, resultText(t, result), "SENTINEL_LEGACY", "a refused legacy read must not leak content")
			_, present := proxy.cacheManager.Peek(key)
			assert.False(t, present, "%s: the legacy entry must be invalidated on first redemption", tc.name)
		})
	}
}

// FR001-G2 at the handler: the proxy's own registry and guesser entries are
// non-redeemable through read_cache for administrators, indistinguishable
// from a missing key for agents, and never evicted by the refusal (their keys
// are guessable; the registry and guesser readers depend on them).
func TestReadCache_InternalEntriesRefusedWithoutEviction(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"servers": []map[string]interface{}{{
				"server": map[string]interface{}{
					"name":        "io.example/SENTINEL_REGISTRY_SERVER",
					"description": "a registry listing",
					"remotes": []interface{}{
						map[string]interface{}{"type": "streamable-http", "url": "https://example.com/mcp"},
					},
				},
			}},
			"metadata": map[string]interface{}{},
		})
	}))
	defer registry.Close()

	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Registries = []config.RegistryEntry{{
			ID: "scope-reg", Name: "Scope Reg", URL: registry.URL, ServersURL: registry.URL,
			Protocol: "modelcontextprotocol/registry",
		}}
		cfg.AllowPrivateRegistryFetch = true
	})

	// The registry entry is written by the real runtime writer.
	servers, _, err := rt.SearchRegistryServers("scope-reg", "", "", 5)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	registryKey := fmt.Sprintf("registry-servers:%s:%s:%s:%d", "scope-reg", "", "", 5)
	_, ok := proxy.cacheManager.Peek(registryKey)
	require.True(t, ok, "premise: the registry writer persisted %q", registryKey)

	// The guesser entry carries the same internal stamp its writer applies
	// (asserted in internal/experiments); it is seeded here with that stamp.
	const npmKey = "npm:@acme/SENTINEL_NPM_PACKAGE"
	require.NoError(t, proxy.cacheManager.StoreAs(npmKey, "repo_guess",
		map[string]interface{}{"package_name": "@acme/SENTINEL_NPM_PACKAGE", "type": "npm"},
		`{"type":"npm","package_name":"@acme/SENTINEL_NPM_PACKAGE","exists":true}`, "", 1,
		cache.Authorization{CallerKind: "internal"}))

	agent := agentCtx([]string{"*"}, allPerms, "")
	for _, key := range []string{registryKey, npmKey} {
		t.Run(key, func(t *testing.T) {
			admin := readCachePage(t, proxy, adminCtx(), key, 0, 50)
			assert.True(t, admin.IsError, "an administrator must not redeem an internal entry: %s", resultText(t, admin))
			assert.NotContains(t, resultText(t, admin), "SENTINEL_", "a refused internal read must not leak the payload")
			assert.NotContains(t, resultText(t, admin), `"records"`)

			// Scoped callers (agent tokens, server-edition users) cannot tell a
			// live internal key from an absent one.
			for _, scoped := range []struct {
				name string
				ctx  context.Context
			}{{"agent", agent}, {"user", userCtx("01HUSER")}} {
				live := readCachePage(t, proxy, scoped.ctx, key, 0, 50)
				absent := readCachePage(t, proxy, scoped.ctx, key+"-does-not-exist", 0, 50)
				require.True(t, live.IsError)
				require.True(t, absent.IsError)
				assert.Equal(t, resultText(t, absent), resultText(t, live),
					"%s: a scoped caller must not be able to tell a live internal key from an absent one", scoped.name)
				assert.Contains(t, resultText(t, live), "cache key not found")
			}

			_, stillThere := proxy.cacheManager.Peek(key)
			assert.True(t, stillThere, "a refused redemption must not evict the internal entry %q", key)
		})
	}

	// The registry's own reader still serves the (unevicted) entry.
	servers, info, err := rt.SearchRegistryServers("scope-reg", "", "", 5)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.NotNil(t, info)
}

// FR001-G4: recursive provenance is monotone. A child page minted while an
// administrator pages an agent's entry carries the PARENT's snapshot (the
// agent's — the redeemer is broader, not narrower), so the producing agent
// can read the child it could have produced. Before this feature the child
// was stamped with the redeemer, and the producer was locked out of its own
// payload's continuation.
func TestReadCache_RecursiveChildInheritsParentProducer(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	producer := agentCtx([]string{"*"}, allPerms, "")
	k1, full := produceTruncatedKey(t, proxy, producer)

	// The administrator pages K1 whole under a limit that forces read_cache's
	// own output to be truncated and re-cached as K2.
	adminPage := readCachePage(t, proxy, adminCtx(), k1, 0, 50)
	require.False(t, adminPage.IsError, "premise: an administrator reads an agent's entry: %s", resultText(t, adminPage))
	setTruncateLimit(proxy, len(resultText(t, adminPage))/2)
	adminTrunc := readCachePage(t, proxy, adminCtx(), k1, 0, 50)
	setTruncateLimit(proxy, 1_000_000)
	require.False(t, adminTrunc.IsError)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, adminTrunc))
	require.Len(t, match, 2, "premise: the administrator's oversize page mints a child key")
	k2 := match[1]
	require.NotEqual(t, k1, k2)

	rec, ok := proxy.cacheManager.Peek(k2)
	require.True(t, ok)
	require.NotNil(t, rec.Producer, "the child page must carry a producer snapshot")
	parentName := auth.AuthContextFromContext(producer).AgentName
	assert.Equal(t, cache.CallerKindAgent, rec.Producer.CallerKind, "the child carries the PARENT's (agent) snapshot, not the administrator redeemer's")
	assert.Equal(t, parentName, rec.Producer.Principal)

	// The producing agent reads the child it could have produced.
	child := readCachePage(t, proxy, producer, k2, 0, 50)
	require.False(t, child.IsError, "the parent's producer must read the recursive child: %s", resultText(t, child))
	var page struct {
		Records []map[string]interface{} `json:"records"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, child)), &page))
	require.Len(t, page.Records, len(full.Tools))
	assert.Equal(t, full.Tools[0]["name"], page.Records[0]["name"])
}

// FR001-G5: an agent's read_cache refusal is non-disclosing. A key that
// exists but was produced under a broader authorization, a key that never
// existed and a key whose entry expired all answer with the SAME text — the
// not-found body — so the refusal cannot be used as an existence oracle.
// Administrators are unaffected (they read the live key; nonexistent keeps
// its body).
func TestReadCache_AgentRefusalIsNonDisclosing(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	broad := agentCtx([]string{"github", "weather"}, []string{auth.PermRead, auth.PermWrite}, "")
	narrow := agentCtx([]string{"weather"}, []string{auth.PermRead}, "")

	liveKey, _ := produceTruncatedKey(t, proxy, broad)
	expiredKey, _ := produceTruncatedKey(t, proxy, broad)
	expireCacheEntry(t, proxy, expiredKey)
	absentKey := "0000000000000000000000000000000000000000000000000000000000000000"

	adminLive := readCachePage(t, proxy, adminCtx(), liveKey, 0, 1)
	require.False(t, adminLive.IsError, "control: the administrator reads the live key")
	adminAbsent := readCachePage(t, proxy, adminCtx(), absentKey, 0, 1)
	require.True(t, adminAbsent.IsError)
	require.Contains(t, resultText(t, adminAbsent), "cache key not found")

	live := readCachePage(t, proxy, narrow, liveKey, 0, 1)
	absent := readCachePage(t, proxy, narrow, absentKey, 0, 1)
	expired := readCachePage(t, proxy, narrow, expiredKey, 0, 1)
	require.True(t, live.IsError)
	require.True(t, absent.IsError)
	require.True(t, expired.IsError)
	assert.NotContains(t, resultText(t, live), "github:")

	assert.Equal(t, resultText(t, absent), resultText(t, live),
		"a broader-scope key must be indistinguishable from a nonexistent key for the narrow token")
	assert.Equal(t, resultText(t, absent), resultText(t, expired),
		"an expired key must be indistinguishable from a nonexistent key for the narrow token")
	assert.Contains(t, resultText(t, live), "cache key not found",
		"the unified body keeps the not-found substring agents already handle")
	// Page 1 as well as page 0: the body must not vary with the offset either.
	assert.Equal(t, resultText(t, absent), resultText(t, readCachePage(t, proxy, narrow, liveKey, 1, 1)))
}

// Critique round 2, finding 2: the administrator-facing refusal bodies were
// asserted nowhere (a mutation hiding the reason from administrators too
// passed the whole suite), and the pre-feature parity body — the anonymous
// /mcp caller refused an authenticated administrator's entry — lost its only
// assertion when T031 inverted the agent tests. Administrators get the
// REASON: legacy provenance (invalidated), an internal entry, or, for the
// anonymous caller, an authenticated administrator's entry (SC-005 parity
// control). The same keys answer a scoped caller with the not-found body.
func TestReadCache_AdministratorRefusalBodiesNameTheReason(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	anonymous := auth.WithAuthContext(context.Background(), auth.AnonymousContext())
	agent := agentCtx([]string{"*"}, allPerms, "")

	// Legacy provenance: the administrator is told it predates stamping.
	const legacyKey = "legacy-entry"
	require.NoError(t, proxy.cacheManager.Store(legacyKey, "retrieve_tools", map[string]interface{}{"query": "manage"},
		`{"tools":[{"name":"github:SENTINEL_LEGACY"}]}`, "tools", 1))
	legacy := readCachePage(t, proxy, adminCtx(), legacyKey, 0, 50)
	require.True(t, legacy.IsError)
	assert.Contains(t, resultText(t, legacy), "predates provenance stamping", "the administrator gets the legacy reason")
	assert.NotContains(t, resultText(t, legacy), "SENTINEL_LEGACY")

	// Internal entry: the administrator is told it is mcpproxy's own.
	const internalKey = "registry-servers:official:::10"
	require.NoError(t, proxy.cacheManager.StoreAs(internalKey, "registry-servers", nil,
		`[{"id":"srv-1","name":"SENTINEL_INTERNAL"}]`, "", 1, cache.Authorization{CallerKind: cache.CallerKindInternal}))
	internal := readCachePage(t, proxy, adminCtx(), internalKey, 0, 50)
	require.True(t, internal.IsError)
	assert.Contains(t, resultText(t, internal), "internal to mcpproxy", "the administrator gets the internal-entry reason")
	assert.NotContains(t, resultText(t, internal), "SENTINEL_INTERNAL")

	// Anonymous below authenticated administrator: the pre-feature body.
	adminKey, _ := produceTruncatedKey(t, proxy, adminCtx())
	control := readCachePage(t, proxy, adminCtx(), adminKey, 0, 1)
	require.False(t, control.IsError, "control: the producing administrator reads its entry")
	anon := readCachePage(t, proxy, anonymous, adminKey, 0, 1)
	require.True(t, anon.IsError, "the anonymous caller ranks below an authenticated administrator")
	assert.Contains(t, resultText(t, anon), "not readable with this credential", "SC-005 parity: the anonymous caller's body is unchanged")
	assert.NotContains(t, resultText(t, anon), "github:")

	// The same three keys are plain misses for a scoped caller.
	absent := readCachePage(t, proxy, agent, "0000000000000000000000000000000000000000000000000000000000000000", 0, 1)
	require.True(t, absent.IsError)
	for _, key := range []string{internalKey, adminKey} {
		got := readCachePage(t, proxy, agent, key, 0, 1)
		require.True(t, got.IsError)
		assert.Equal(t, resultText(t, absent), resultText(t, got), "%s: a scoped caller gets the not-found body, never the reason", key)
	}
	require.NoError(t, proxy.cacheManager.Store(legacyKey, "retrieve_tools", map[string]interface{}{"query": "manage"},
		`{"tools":[{"name":"github:SENTINEL_LEGACY"}]}`, "tools", 1))
	got := readCachePage(t, proxy, agent, legacyKey, 0, 1)
	require.True(t, got.IsError)
	assert.Equal(t, resultText(t, absent), resultText(t, got), "a scoped caller gets the not-found body for a legacy entry too")
}
