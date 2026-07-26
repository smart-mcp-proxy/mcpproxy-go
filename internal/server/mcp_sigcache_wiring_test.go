package server

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// newRuntimeBackedProxy builds a real Runtime and an MCPProxyServer wired the
// way production wires them (NewServerWithConfigPath): the SAME
// Runtime-owned signature cache handed into NewMCPProxyServer.
func newRuntimeBackedProxy(t *testing.T) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()

	tmpDir := t.TempDir()
	logger := zap.NewNop()
	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir

	rt, err := runtime.New(cfg, filepath.Join(tmpDir, "mcp_config.json"), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	srv := &Server{logger: logger, runtime: rt}
	proxy := NewMCPProxyServer(
		rt.StorageManager(),
		rt.IndexManager(),
		rt.UpstreamManager(),
		rt.CacheManager(),
		rt.Truncator,
		logger,
		srv,
		false,
		cfg,
		rt.SignatureCache(),
	)
	return proxy, rt
}

// Spec 085 T008 (FR-008, research.md R9): exactly ONE *toolsig.Cache exists —
// created by the Runtime, warmed by the indexing path, read by the request
// path. A plausible-but-wrong implementation constructs a second cache inside
// internal/server, which passes functional tests while quietly recompiling
// per request; this test is the falsifier.
//
// T024 (US1) extends this test to drive a real compact retrieve_tools call
// once the compact serialization exists.
func TestSignatureCache_SingleOwnerWiring(t *testing.T) {
	proxy, rt := newRuntimeBackedProxy(t)

	// The request path must hold the exact instance the Runtime owns.
	require.Same(t, rt.SignatureCache(), proxy.sigCache,
		"indexing path (runtime) and request path (proxy) must share ONE cache instance")

	// Warm N tools through the runtime-owned handle (the indexing path)...
	const n = 7
	schema := `{"type":"object","properties":{"origin":{"type":"string"}},"required":["origin"]}`
	for i := 0; i < n; i++ {
		rt.SignatureCache().Warm(fmt.Sprintf("hash-%d", i), schema, "Tool description. More detail.")
	}
	compiledAfterWarm := rt.SignatureCache().CompileCount()
	require.Equal(t, int64(n), compiledAfterWarm, "warm-up compiles once per unique hash")

	// ...then read every one of them through the request-path handle: a
	// post-index read is a pure cache hit — the compile counter must not move.
	for i := 0; i < n; i++ {
		proxy.sigCache.Get(fmt.Sprintf("hash-%d", i), schema, "Tool description. More detail.")
	}
	require.Equal(t, compiledAfterWarm, proxy.sigCache.CompileCount(),
		"post-warm reads on the request path must not compile (FR-008: not per request)")
}

// T024 extension (US1, FR-008): after the indexing path has warmed the ONE
// shared cache, a real compact retrieve_tools call is a pure cache hit — the
// compile counter must not move. This is the end-to-end falsifier for
// "compiled at index time, not per request": a second cache, a wrong key, or
// a per-request Render would all move the counter.
func TestSignatureCache_CompactRetrieveIsPureCacheHit(t *testing.T) {
	proxy, rt := newRuntimeBackedProxy(t)

	// Index tools + warm the cache through the runtime-owned handle, exactly
	// like the indexing path does (lifecycle warms after BatchIndexTools —
	// covered by internal/runtime's TestApplyDifferentialToolUpdate_WarmsSignatureCache).
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true,
	}))
	tools := []*config.ToolMetadata{
		{
			Name: "github:create_issue", ServerName: "github",
			Description: "Create an issue to manage work.",
			ParamsJSON:  `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
			Hash:        "wire-hash-create",
		},
		{
			Name: "github:list_issues", ServerName: "github",
			Description: "List issues to manage a backlog.",
			ParamsJSON:  `{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"]}`,
			Hash:        "wire-hash-list",
		},
	}
	require.NoError(t, rt.IndexManager().BatchIndexTools(tools))
	for _, tool := range tools {
		rt.SignatureCache().Warm(tool.Hash, tool.ParamsJSON, tool.Description)
	}
	compiledAfterIndex := rt.SignatureCache().CompileCount()
	require.Equal(t, int64(len(tools)), compiledAfterIndex)

	// A real compact retrieve_tools call must not compile anything.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "manage", "limit": float64(10), "detail": "compact",
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := result.Content[0].(mcp.TextContent).Text
	require.Contains(t, text, `"sig"`, "compact retrieve must return compact entries")

	require.Equal(t, compiledAfterIndex, rt.SignatureCache().CompileCount(),
		"a post-index compact retrieve_tools must be a pure cache hit (FR-008)")
}

// A nil cache argument (standalone CLI/test constructions without a Runtime)
// still yields a working private cache — never a nil deref.
func TestSignatureCache_NilFallback(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NotNil(t, proxy.sigCache, "nil cache argument must self-construct a private cache")
	sig := proxy.sigCache.Get("h", `{"type":"object","properties":{}}`, "Desc.")
	require.Equal(t, "()", sig.Sig)
}
