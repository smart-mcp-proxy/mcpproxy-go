package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// Spec 105 FR-001 / FR-002 / FR-013 mandatory fixtures (task T027, gap
// FR001-G7). Each fixture is named in the spec text and was absent from the
// suite:
//
//	(a) upgrade: a pre-feature record is refused, returns no content, and is
//	    absent after the store is restarted (SC-004);
//	(b) fresh internal entry: refused for administrators, agents and the
//	    anonymous caller, never evicted (SC-005 named exception);
//	(c) recursive child on the REST direct call path, plus the spec's named
//	    fixture — a session-profiled administrator redeems a broad entry and
//	    a pinned wildcard agent is refused the child on MCP and REST;
//	(d) pinned token on REST: direct dispatch to the out-of-pin server is
//	    refused AND cache redemption of an entry containing it is refused
//	    with the nonexistent-key body (Scope Boundary exception);
//	(e) held call: the snapshot is the one resolved at dispatch, so a session
//	    narrowed while the upstream call is in flight neither re-stamps the
//	    entry nor redeems it (passes on the merge base — regression pin);
//	(f) empty server grant (codex round 1): an agent token with no allowed
//	    servers is deny-all on every dispatch gate and must be deny-all on
//	    redemption too — an identically-stamped record answers with the
//	    nonexistent-key body on MCP and REST.

// newRestartableProxy builds the minimal proxy on a caller-owned directory
// and returns it with an explicit close, so a test can stop it and open a
// second proxy on the SAME data — the only way to prove an invalidation
// reached disk rather than an in-memory view.
func newRestartableProxy(t *testing.T, dir string) (*MCPProxyServer, func()) {
	t.Helper()
	logger := zap.NewNop()

	sm, err := storage.NewManager(dir, logger.Sugar())
	require.NoError(t, err)
	idx, err := index.NewManager(dir, logger)
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.ToolsLimit = 20
	um := upstream.NewManager(logger, cfg, nil, secret.NewResolver(), nil)
	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	tr := truncate.NewTruncator(0)
	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr }, logger, nil, false, cfg, nil)

	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			cm.Close()
			_ = idx.Close()
			_ = sm.Close()
		})
	}
	t.Cleanup(closeFn)
	return proxy, closeFn
}

// putPreFeatureRecord writes a cache record in the exact wire shape a
// pre-feature binary persisted: no producer, no version.
func putPreFeatureRecord(t *testing.T, db *bbolt.DB, key, fullContent, recordPath string, total int) {
	t.Helper()
	now := time.Now()
	doc := map[string]interface{}{
		"key": key, "tool_name": "retrieve_tools", "args": map[string]interface{}{"query": "manage"},
		"timestamp": now, "full_content": fullContent, "record_path": recordPath,
		"total_records": total, "total_size": len(fullContent), "expires_at": now.Add(time.Hour),
		"access_count": 0, "last_accessed": now, "created_at": now,
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(cache.CacheBucket)).Put([]byte(key), data)
	}))
}

// callToolDirectText runs one CallToolDirect and returns the text it yields,
// with the error (nil on success) — the REST layer's view of the outcome.
func callToolDirectText(t *testing.T, proxy *MCPProxyServer, ctx context.Context, tool string, args map[string]interface{}) (string, error) {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Name = tool
	request.Params.Arguments = args
	out, err := proxy.CallToolDirect(ctx, request)
	if err != nil {
		return "", err
	}
	blocks, ok := out.([]mcp.Content)
	require.True(t, ok, "CallToolDirect returns raw content blocks, got %T", out)
	require.NotEmpty(t, blocks)
	text, ok := blocks[0].(mcp.TextContent)
	require.True(t, ok)
	return text.Text, nil
}

func readCacheDirect(t *testing.T, proxy *MCPProxyServer, ctx context.Context, key string) (string, error) {
	t.Helper()
	return callToolDirectText(t, proxy, ctx, "read_cache", map[string]interface{}{
		"key": key, "offset": float64(0), "limit": float64(50),
	})
}

// (a) Upgrade fixture — SC-004.
func TestScopeCacheFixture_UpgradeRecordRefusedAndAbsentAfterRestart(t *testing.T) {
	dir := t.TempDir()
	proxy, closeProxy := newRestartableProxy(t, dir)

	const key = "pre-feature-key"
	putPreFeatureRecord(t, proxy.storage.GetDB(), key,
		`{"tools":[{"name":"github:SENTINEL_UPGRADE"},{"name":"github:second"}]}`, "tools", 2)
	_, present := proxy.cacheManager.Peek(key)
	require.True(t, present, "premise: the pre-feature record is readable by the decoder")

	// Each leg re-seeds the record (critique round 2, finding 3): the first
	// refused redemption invalidates it, so without re-seeding every later
	// leg would pass vacuously as a plain miss of an absent key.
	seed := func() {
		t.Helper()
		putPreFeatureRecord(t, proxy.storage.GetDB(), key,
			`{"tools":[{"name":"github:SENTINEL_UPGRADE"},{"name":"github:second"}]}`, "tools", 2)
		_, ok := proxy.cacheManager.Peek(key)
		require.True(t, ok, "premise: the pre-feature record is live before the leg")
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"administrator", adminCtx()},
		{"agent", agentCtx([]string{"*"}, allPerms, "")},
	} {
		seed()
		result := readCachePage(t, proxy, tc.ctx, key, 0, 50)
		assert.True(t, result.IsError, "%s must be refused the pre-feature record: %s", tc.name, resultText(t, result))
		assert.NotContains(t, resultText(t, result), "SENTINEL_UPGRADE", "%s: no content may be returned", tc.name)
		assert.NotContains(t, resultText(t, result), `"records"`, "%s: no page may be returned", tc.name)
		_, present = proxy.cacheManager.Peek(key)
		assert.False(t, present, "%s on MCP: the pre-feature record must be invalidated on first redemption", tc.name)

		// The REST door refuses too — against a LIVE record.
		seed()
		text, err := readCacheDirect(t, proxy, tc.ctx, key)
		assert.Error(t, err, "%s on REST must be refused, got %q", tc.name, text)
		if err != nil {
			assert.NotContains(t, err.Error(), "SENTINEL_UPGRADE")
		}
		_, present = proxy.cacheManager.Peek(key)
		assert.False(t, present, "%s on REST: the pre-feature record must be invalidated on first redemption", tc.name)
	}

	// Restart on the same data directory.
	closeProxy()
	reopened, _ := newRestartableProxy(t, dir)
	_, present = reopened.cacheManager.Peek(key)
	assert.False(t, present, "the pre-feature record must be absent after restart")
	after := readCachePage(t, reopened, adminCtx(), key, 0, 50)
	assert.True(t, after.IsError)
	assert.Contains(t, resultText(t, after), "cache key not found", "after restart the key is a plain miss")
}

// (b) Fresh internal entry — SC-005 named exception. Registry and guesser
// entries are written after the upgrade with the internal stamp (their writer
// tests assert the stamp); every read_cache caller is refused, the entry is
// kept for its internal readers, and an agent cannot distinguish it from an
// absent key on MCP or REST.
func TestScopeCacheFixture_FreshInternalEntryRefusedForEveryCaller(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	const key = "registry-servers:official:::10"
	require.NoError(t, proxy.cacheManager.StoreAs(key, "registry-servers", nil,
		`[{"id":"srv-1","name":"SENTINEL_INTERNAL"}]`, "", 1, cache.Authorization{CallerKind: "internal"}))

	agent := agentCtx([]string{"*"}, allPerms, "")
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"administrator", adminCtx()},
		{"anonymous", auth.WithAuthContext(context.Background(), auth.AnonymousContext())},
		{"agent", agent},
	} {
		result := readCachePage(t, proxy, tc.ctx, key, 0, 50)
		assert.True(t, result.IsError, "%s must be refused a fresh internal entry: %s", tc.name, resultText(t, result))
		assert.NotContains(t, resultText(t, result), "SENTINEL_INTERNAL")
		_, err := readCacheDirect(t, proxy, tc.ctx, key)
		assert.Error(t, err, "%s on REST must be refused", tc.name)
	}
	rec, present := proxy.cacheManager.Peek(key)
	require.True(t, present, "an internal entry is refused WITHOUT eviction")
	require.NotNil(t, rec.Producer)
	assert.Equal(t, "internal", rec.Producer.CallerKind)
	if got, err := proxy.cacheManager.Get(key); assert.NoError(t, err) {
		assert.Contains(t, got.FullContent, "SENTINEL_INTERNAL", "the ungated internal reader still serves it")
	}

	live := readCachePage(t, proxy, agent, key, 0, 50)
	absent := readCachePage(t, proxy, agent, key+"-absent", 0, 50)
	assert.Equal(t, resultText(t, absent), resultText(t, live), "MCP: internal key ≡ absent key for an agent")
	_, liveErr := readCacheDirect(t, proxy, agent, key)
	_, absentErr := readCacheDirect(t, proxy, agent, key+"-absent")
	require.Error(t, liveErr)
	require.Error(t, absentErr)
	assert.Equal(t, absentErr.Error(), liveErr.Error(), "REST: internal key ≡ absent key for an agent")
	assert.Contains(t, liveErr.Error(), "cache key not found", "REST: the shared body is the not-found one, not some earlier pre-check")
}

// (c) Recursive child on the REST direct call path: the child page an
// administrator mints while paging an agent's entry through
// /api/v1/tools/call carries the agent's snapshot, so the agent reads it.
func TestScopeCacheFixture_RecursiveChildOnREST(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	producer := agentCtx([]string{"*"}, allPerms, "")
	k1, full := produceTruncatedKey(t, proxy, producer)

	adminPage, err := readCacheDirect(t, proxy, adminCtx(), k1)
	require.NoError(t, err, "premise: the administrator pages the agent's entry on REST")
	setTruncateLimit(proxy, len(adminPage)/2)
	adminTrunc, err := readCacheDirect(t, proxy, adminCtx(), k1)
	setTruncateLimit(proxy, 1_000_000)
	require.NoError(t, err)
	match := cacheKeyRE.FindStringSubmatch(adminTrunc)
	require.Len(t, match, 2, "premise: the oversize REST page mints a child key")
	k2 := match[1]

	rec, ok := proxy.cacheManager.Peek(k2)
	require.True(t, ok)
	require.NotNil(t, rec.Producer)
	assert.Equal(t, cache.CallerKindAgent, rec.Producer.CallerKind, "the REST child carries the parent's (agent) snapshot")

	child, err := readCacheDirect(t, proxy, producer, k2)
	require.NoError(t, err, "the parent's producer must read the recursive child on REST")
	var page struct {
		Records []map[string]interface{} `json:"records"`
	}
	require.NoError(t, json.Unmarshal([]byte(child), &page))
	require.Len(t, page.Records, len(full.Tools))
}

// (c') The spec's named fixture (FR-001): an administrator with SESSION
// profile {github} redeems an entry containing weather; the recursive child
// is then requested by a wildcard full-permission agent pinned to {github} —
// refused on MCP and on REST, with the nonexistent-key body. The child is an
// administrator snapshot (parent's, or the narrower session-profiled one) and
// an agent never qualifies for an administrator snapshot; the administrator's
// own redemption of the broad entry is the caller-kind-first rule (D5).
func TestScopeCacheFixture_ProfiledAdminChildNotRedeemableByPinnedAgent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "weather", Enabled: true}}
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github"}}}

	// Unscoped administrator produces the broad entry (it lists weather).
	k1, full := produceTruncatedKey(t, proxy, adminCtx())
	require.True(t, func() bool {
		for _, tool := range full.Tools {
			if strings.HasPrefix(fmt.Sprint(tool["name"]), "weather:") {
				return true
			}
		}
		return false
	}(), "premise: the broad entry contains a weather tool")

	// Administrator bound to session profile {github}.
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	session := helper.WithContext(context.Background(), &fakeClientSession{id: "profiled-admin-session"})
	profiledAdmin := auth.WithAuthContext(session, auth.AdminContext())
	proxy.sessionStore.SetActiveProfile("profiled-admin-session", "research")
	name, scope := proxy.resolveActiveProfile(profiledAdmin)
	require.Equal(t, "research", name)
	require.NotNil(t, scope)
	require.False(t, scope.Allows("weather"), "premise: the session profile excludes weather")

	adminPage := readCachePage(t, proxy, profiledAdmin, k1, 0, 50)
	require.False(t, adminPage.IsError, "caller-kind first: a profile-bound administrator redeems any snapshot (D5): %s", resultText(t, adminPage))
	setTruncateLimit(proxy, len(resultText(t, adminPage))/2)
	adminTrunc := readCachePage(t, proxy, profiledAdmin, k1, 0, 50)
	setTruncateLimit(proxy, 1_000_000)
	require.False(t, adminTrunc.IsError)
	match := cacheKeyRE.FindStringSubmatch(resultText(t, adminTrunc))
	require.Len(t, match, 2, "premise: the profiled administrator's oversize page mints a child key")
	k2 := match[1]
	rec, ok := proxy.cacheManager.Peek(k2)
	require.True(t, ok)
	require.NotNil(t, rec.Producer)
	assert.Equal(t, cache.CallerKindAdmin, rec.Producer.CallerKind, "the child is an administrator snapshot")
	// Under D5 the pinned agent below is refused by KIND whichever
	// administrator snapshot the child carries, so the kind alone does not
	// pin parent-stamping (critique round 2, finding 1). The parent was
	// produced UNSCOPED; a child stamped with the redeemer would carry
	// Profile "research", ProfileScoped true, ProfileServers {github}.
	// The security assertion for monotone provenance across kinds is
	// carried by TestReadCache_RecursiveChildInheritsParentProducer.
	assert.False(t, rec.Producer.ProfileScoped, "the child carries the PARENT's unscoped snapshot, not the session-profiled redeemer's")
	assert.Empty(t, rec.Producer.Profile)
	assert.Empty(t, rec.Producer.ProfileServers)

	pinned := agentCtx([]string{"*"}, allPerms, "research")
	absentKey := "0000000000000000000000000000000000000000000000000000000000000000"
	child := readCachePage(t, proxy, pinned, k2, 0, 50)
	absent := readCachePage(t, proxy, pinned, absentKey, 0, 50)
	require.True(t, child.IsError, "a pinned wildcard agent must not redeem the administrator's child: %s", resultText(t, child))
	assert.NotContains(t, resultText(t, child), "weather:")
	assert.Equal(t, resultText(t, absent), resultText(t, child), "MCP: the refusal is the nonexistent-key body")

	_, childErr := readCacheDirect(t, proxy, pinned, k2)
	_, absentErr := readCacheDirect(t, proxy, pinned, absentKey)
	require.Error(t, childErr, "REST: a pinned wildcard agent must not redeem the administrator's child")
	require.Error(t, absentErr)
	assert.Equal(t, absentErr.Error(), childErr.Error(), "REST: the refusal is the nonexistent-key body")
	assert.Contains(t, childErr.Error(), "cache key not found", "REST: equality alone would also hold for a shared pre-check error")
}

// (d) Pinned token on REST (Scope Boundary exception): a token allowing
// {github, weather} but pinned to research={github} is refused a direct call
// to weather through /api/v1/tools/call, and is refused — with the
// nonexistent-key body — the redemption of an unpinned entry containing
// weather through the same endpoint's read_cache branch.
func TestScopeCacheFixture_PinnedTokenRESTDispatchAndRedemptionParity(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "weather", Enabled: true}}
	proxy.config.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"github"}}}

	unpinned := agentCtx([]string{"github", "weather"}, []string{auth.PermRead}, "")
	pinned := agentCtx([]string{"github", "weather"}, []string{auth.PermRead}, "research")

	key, full := produceTruncatedKey(t, proxy, unpinned)
	require.True(t, func() bool {
		for _, tool := range full.Tools {
			if strings.HasPrefix(fmt.Sprint(tool["name"]), "weather:") {
				return true
			}
		}
		return false
	}(), "premise: the unpinned entry contains a weather tool")

	// Direct dispatch to the out-of-pin server is refused on the REST path.
	_, dispatchErr := callToolDirectText(t, proxy, pinned, contracts.ToolVariantRead,
		map[string]interface{}{"name": "weather:get_forecast", "args": map[string]interface{}{}})
	require.Error(t, dispatchErr, "the pin must refuse direct dispatch to weather")
	assert.Contains(t, dispatchErr.Error(), "not in profile 'research'")

	// The producing (unpinned) token reads its entry on the same endpoint.
	_, err := readCacheDirect(t, proxy, unpinned, key)
	require.NoError(t, err, "control: the unpinned producer redeems its own entry on REST")

	// Cache redemption by the pinned token is refused with the
	// nonexistent-key body.
	_, redeemErr := readCacheDirect(t, proxy, pinned, key)
	_, absentErr := readCacheDirect(t, proxy, pinned, "no-such-key")
	require.Error(t, redeemErr, "the pinned token must not redeem an unpinned entry containing weather")
	require.Error(t, absentErr)
	assert.NotContains(t, redeemErr.Error(), "weather:")
	assert.Equal(t, absentErr.Error(), redeemErr.Error(), "the redemption refusal must be the nonexistent-key body")
	assert.Contains(t, redeemErr.Error(), "cache key not found")
}

// (e) Held call — regression pin (passes on the merge base): the producer
// snapshot is captured when the call is authorized, not when the response
// comes back to be truncated. A session selection narrowed from {a,b} to {a}
// while the upstream call to b is in flight leaves the entry stamped {a,b},
// and that entry is not redeemable under {a} on MCP or REST; the {a,b}
// session still reads it.
func TestScopeCacheFixture_HeldCallKeepsDispatchTimeSnapshot(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t,
		[]*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}},
		func(cfg *config.Config) {
			cfg.Profiles = []config.ProfileConfig{
				{Name: "wide", Servers: []string{"a", "b"}},
				{Name: "narrow", Servers: []string{"a"}},
			}
		})
	up := startCountingUpstream(t, proxy, rt, "b", readSpec("held"))

	// Replace the stub handler with one that blocks until released and then
	// answers with a payload large enough to be truncated into a cache key.
	records := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		records = append(records, fmt.Sprintf(`{"id":%d,"note":"SENTINEL_HELD padding padding padding padding padding"}`, i))
	}
	payload := "[" + strings.Join(records, ",") + "]"
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	up.mcpSrv.AddTool(mcp.Tool{Name: "held", Description: "Read held", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			up.record(request.Params.Name)
			startOnce.Do(func() { close(started) })
			<-release
			return mcp.NewToolResultText(payload), nil
		})

	const sid = "held-session"
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	session := helper.WithContext(context.Background(), &fakeClientSession{id: sid})
	agent := auth.WithAuthContext(session, &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "held-agent", TokenPrefix: "mcp_agt_held",
		AllowedServers: []string{"a", "b"}, Permissions: []string{auth.PermRead},
	})
	proxy.sessionStore.SetActiveProfile(sid, "wide")
	setTruncateLimit(proxy, len(payload)/4)

	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantRead
	req.Params.Arguments = map[string]interface{}{"name": "b:held", "args": map[string]interface{}{}}
	var (
		result *mcp.CallToolResult
		err    error
		done   = make(chan struct{})
	)
	go func() {
		defer close(done)
		result, err = proxy.handleCallToolVariant(agent, req, contracts.ToolVariantRead)
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("the upstream call never started")
	}
	// Narrow the session selection while the call is held, then let it finish.
	proxy.sessionStore.SetActiveProfile(sid, "narrow")
	close(release)
	<-done
	setTruncateLimit(proxy, 1_000_000)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "the call authorized under {a,b} completes: %s", resultText(t, result))
	require.Equal(t, int64(1), up.count.Load())
	match := cacheKeyRE.FindStringSubmatch(resultText(t, result))
	require.Len(t, match, 2, "premise: the held call's oversize response mints a key")
	key := match[1]

	rec, ok := proxy.cacheManager.Peek(key)
	require.True(t, ok)
	require.NotNil(t, rec.Producer)
	assert.Equal(t, "wide", rec.Producer.Profile, "the entry is stamped with the profile in effect at dispatch")
	assert.ElementsMatch(t, []string{"a", "b"}, rec.Producer.ProfileServers)

	// Under the narrowed session ({a}) the entry is not redeemable, on MCP or
	// REST; the {a,b} session still reads it.
	narrowed := readCachePage(t, proxy, agent, key, 0, 50)
	assert.True(t, narrowed.IsError, "an entry stamped {a,b} must not be redeemable under {a}: %s", resultText(t, narrowed))
	assert.NotContains(t, resultText(t, narrowed), "SENTINEL_HELD")
	_, restErr := readCacheDirect(t, proxy, agent, key)
	assert.Error(t, restErr, "REST: an entry stamped {a,b} must not be redeemable under {a}")

	proxy.sessionStore.SetActiveProfile(sid, "wide")
	wide := readCachePage(t, proxy, agent, key, 0, 50)
	require.False(t, wide.IsError, "the {a,b} session reads the entry it produced: %s", resultText(t, wide))
	assert.Contains(t, resultText(t, wide), "SENTINEL_HELD")
}

// (f) Empty server grant (codex round 1, MUST-FIX). Token creation
// normalises an empty allowed_servers to ["*"], but a server-edition
// rotation that narrows the grant persists nil, and auth.CanAccessServer /
// serverInScope treat that as deny-all: the token can call no upstream tool.
// Before the fix cache.CouldHaveProduced let coversServers([], []) succeed,
// so such a token could redeem an agent record whose snapshot also carried
// an empty grant — content the deny-all gates would never have let it
// produce. Now the gated predicate refuses an empty-grant agent reader
// outright, and the refusal is the nonexistent-key body on both doors.
func TestScopeCacheFixture_EmptyGrantAgentIsDenyAllOnRedemption(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "weather", Enabled: true}}

	for _, grant := range []struct {
		name    string
		allowed []string
	}{
		{"nil grant", nil},
		{"empty list grant", []string{}},
	} {
		t.Run(grant.name, func(t *testing.T) {
			denyAll := agentCtx(grant.allowed, []string{auth.PermRead}, "")
			require.False(t, auth.AuthContextFromContext(denyAll).CanAccessServer("github"),
				"premise: an empty grant is deny-all on the dispatch gate")

			// Direct dispatch is refused on the REST path — the same door
			// the redemption below goes through.
			_, dispatchErr := callToolDirectText(t, proxy, denyAll, contracts.ToolVariantRead,
				map[string]interface{}{"name": "github:list_repos", "args": map[string]interface{}{}})
			require.Error(t, dispatchErr, "premise: the empty grant refuses direct dispatch")

			// An identically-stamped deny-all record: the reader's own
			// snapshot, byte for byte, as a producer.
			stamp := proxy.cacheAuthorization(denyAll)
			require.Equal(t, cache.CallerKindAgent, stamp.CallerKind)
			require.Empty(t, stamp.AllowedServers)
			key := strings.Repeat("e", 63) + map[bool]string{true: "0", false: "1"}[grant.allowed == nil]
			require.NoError(t, proxy.cacheManager.StoreAs(key, "retrieve_tools",
				map[string]interface{}{"query": "manage"}, `[{"name":"SENTINEL_EMPTY_GRANT"}]`, "", 1, stamp))
			absentKey := strings.Repeat("f", 64)

			// Control: an administrator redeems it (kind first), so the
			// record is live and readable — the refusal below is the gate.
			adminPage := readCachePage(t, proxy, adminCtx(), key, 0, 50)
			require.False(t, adminPage.IsError, "control: administrator reads the empty-grant record: %s", resultText(t, adminPage))
			require.Contains(t, resultText(t, adminPage), "SENTINEL_EMPTY_GRANT")

			// MCP: live key ≡ absent key for the empty-grant reader.
			live := readCachePage(t, proxy, denyAll, key, 0, 50)
			absent := readCachePage(t, proxy, denyAll, absentKey, 0, 50)
			require.True(t, live.IsError, "the empty-grant agent must not redeem an identically-stamped record: %s", resultText(t, live))
			require.True(t, absent.IsError)
			assert.NotContains(t, resultText(t, live), "SENTINEL_EMPTY_GRANT")
			assert.Equal(t, resultText(t, absent), resultText(t, live), "MCP: live key ≡ absent key for the empty-grant agent")
			assert.Contains(t, resultText(t, live), "cache key not found")

			// REST (/api/v1/tools/call read_cache branch): same parity.
			liveText, liveErr := readCacheDirect(t, proxy, denyAll, key)
			_, absentErr := readCacheDirect(t, proxy, denyAll, absentKey)
			require.Error(t, liveErr, "REST: the empty-grant agent must not redeem the record: %s", liveText)
			require.Error(t, absentErr)
			assert.Equal(t, absentErr.Error(), liveErr.Error(), "REST: live key ≡ absent key for the empty-grant agent")
			assert.Contains(t, liveErr.Error(), "cache key not found")

			// The refusal does not evict: the administrator still reads it.
			after := readCachePage(t, proxy, adminCtx(), key, 0, 50)
			require.False(t, after.IsError, "a refused redemption of a stamped record must not evict it")
		})
	}
}
