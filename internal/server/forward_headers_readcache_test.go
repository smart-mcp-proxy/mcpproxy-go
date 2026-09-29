package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
)

func edgeCtxWith(t *testing.T, value string) context.Context {
	t.Helper()
	r, _ := http.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	if value != "" {
		r.Header.Set("X-Tenant-Id", value)
	}
	return headerfwd.WithSnapshot(context.Background(), headerfwd.Capture(r, map[string]struct{}{"X-Tenant-Id": {}}))
}

func outboundFor(t *testing.T, proxy *MCPProxyServer, ctx context.Context, server string) headerfwd.Snapshot {
	t.Helper()
	snap, ok := headerfwd.SnapshotFrom(ctx)
	require.True(t, ok)
	policy, ok := proxy.forwardPolicyFor(server)
	require.True(t, ok)
	out := headerfwd.Outbound(snap, policy)
	require.False(t, out.IsEmpty())
	return out
}

// T023 / FR-017: an entry produced by a call that forwarded headers is
// redeemable only by a request forwarding the same set to the same upstream.
func TestReadCache_ForwardedDigestIsolation(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.Servers = []*config.ServerConfig{{
		Name: "srv", Enabled: true, Protocol: "http", URL: "http://127.0.0.1:1/mcp",
		ForwardHeaders: []string{"X-Tenant-Id"},
	}}
	content := `[{"n":1},{"n":2},{"n":3}]`
	producer := cache.Authorization{CallerKind: cache.CallerKindAnonymous}

	alice := edgeCtxWith(t, "alice-tenant")
	require.NoError(t, proxy.cacheStoreAsForwarded(producer, outboundFor(t, proxy, alice, "srv"), "srv").
		Store("k-fwd", "srv:tool", nil, content, "", 3))
	require.NoError(t, proxy.cacheStoreAsForwarded(producer, headerfwd.Snapshot{}, "srv").
		Store("k-plain", "srv:tool", nil, content, "", 3))

	read := func(ctx context.Context, key string) *mcp.CallToolResult { return readCacheAs(t, proxy, ctx, key, 0) }

	assert.False(t, read(alice, "k-fwd").IsError, "same forwarded set redeems")
	assert.False(t, read(edgeCtxWith(t, "alice-tenant"), "k-fwd").IsError, "a fresh request with the same value redeems")

	bob := read(edgeCtxWith(t, "bob-tenant"), "k-fwd")
	require.True(t, bob.IsError, "different value is refused")
	assert.False(t, strings.Contains(resultText(t, bob), "alice-tenant") || strings.Contains(resultText(t, bob), "bob-tenant"), "no value in the refusal")

	assert.True(t, read(context.Background(), "k-fwd").IsError, "no snapshot at all is refused")
	assert.True(t, read(edgeCtxWith(t, ""), "k-fwd").IsError, "missing header is refused")

	assert.False(t, read(bobCtx(t), "k-plain").IsError, "an entry that forwarded nothing is unaffected")
	assert.False(t, read(context.Background(), "k-plain").IsError)

	// Forwarding switched off: the reader's digest is empty, so the entry is refused.
	off := false
	proxy.config.ForwardClientHeaders = &off
	assert.True(t, read(alice, "k-fwd").IsError)
	proxy.config.ForwardClientHeaders = nil

	// The unscrubbed payload is what is cached, and the digest never appears on the wire.
	ok := read(alice, "k-fwd")
	assert.NotContains(t, resultText(t, ok), "forwarded_digest")
}

func bobCtx(t *testing.T) context.Context { return edgeCtxWith(t, "bob-tenant") }

// A recursively re-truncated read_cache page inherits its parent's fact.
func TestReadCache_ChildPageInheritsForwardedFact(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.Servers = []*config.ServerConfig{{
		Name: "srv", Enabled: true, Protocol: "http", URL: "http://127.0.0.1:1/mcp",
		ForwardHeaders: []string{"X-Tenant-Id"},
	}}
	producer := cache.Authorization{CallerKind: cache.CallerKindAnonymous}
	alice := edgeCtxWith(t, "alice-tenant")
	require.NoError(t, proxy.cacheStoreAsForwarded(producer, outboundFor(t, proxy, alice, "srv"), "srv").
		Store("parent", "srv:tool", nil, `[{"n":1},{"n":2}]`, "", 2))

	parent, err := proxy.cacheManager.GetRecords("parent", 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, parent.ForwardedDigest)

	require.NoError(t, proxy.cacheStoreAsChildPage(producer, parent).Store("child", "read_cache", nil, `[{"n":1}]`, "", 1))
	child, err := proxy.cacheManager.GetRecords("child", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, parent.ForwardedDigest, child.ForwardedDigest)
	assert.Equal(t, "srv", child.ForwardedServer)

	assert.True(t, readCacheAs(t, proxy, edgeCtxWith(t, "bob-tenant"), "child", 0).IsError)
	assert.False(t, readCacheAs(t, proxy, alice, "child", 0).IsError)
}

// FR-015b (review round 2): the cache holds the unscrubbed upstream payload,
// so the activity copy of a redeemed page must be scrubbed with the redeeming
// request's outbound set.
func TestReadCache_RedeemedPageActivityCopyIsScrubbed(t *testing.T) {
	const sentinel = "tenant-secret-42"
	proxy := createTestMCPProxyServer(t)
	proxy.config.Servers = []*config.ServerConfig{{
		Name: "srv", Enabled: true, Protocol: "http", URL: "http://127.0.0.1:1/mcp",
		ForwardHeaders: []string{"X-Tenant-Id"},
	}}
	producer := cache.Authorization{CallerKind: cache.CallerKindAnonymous}
	ctx := edgeCtxWith(t, sentinel)
	require.NoError(t, proxy.cacheStoreAsForwarded(producer, outboundFor(t, proxy, ctx, "srv"), "srv").
		Store("k-echo", "srv:tool", nil, `[{"echo":"`+sentinel+`"}]`, "", 1))

	page, err := proxy.cacheManager.GetRecordsAs("k-echo", 0, 1, proxy.cacheAuthorization(ctx))
	require.NoError(t, err)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.Contains(t, string(raw), sentinel, "the cached page itself is unscrubbed by design")

	ok, out := proxy.forwardedEntryRedeem(ctx, page)
	require.True(t, ok)
	rec, err := json.Marshal(scrubResultForRecord(page, out))
	require.NoError(t, err)
	assert.NotContains(t, string(rec), sentinel, "activity copy")
}
