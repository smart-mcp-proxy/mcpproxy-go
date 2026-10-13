package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 085 US1 T024 (FR-008): the indexing path WARMS the Runtime-owned
// signature cache — signatures are compiled at index time, keyed by the
// Spec-032 tool hash, so a later compact retrieve_tools is a pure cache hit
// (asserted server-side in internal/server/mcp_sigcache_wiring_test.go).
//
// The warm-proof trick: Cache.Get(hash, "", "") on a MISS would compile the
// empty schema ("()", empty desc). Getting the REAL signature back for a
// (hash, "", "") probe therefore proves the entry was already compiled by
// the indexing path, not by the probe.

func newSigWarmRuntime(t *testing.T) *Runtime {
	t.Helper()
	cfg := &config.Config{
		DataDir:           t.TempDir(),
		Listen:            "127.0.0.1:0",
		Servers:           []*config.ServerConfig{},
		QuarantineEnabled: boolP(false),
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func sigWarmTool(name, hash, schema, desc string) *config.ToolMetadata {
	return &config.ToolMetadata{
		ServerName:  "sig-server",
		Name:        name,
		Description: desc,
		ParamsJSON:  schema,
		Hash:        hash,
	}
}

// The cache must also RECONCILE (finding: stale hashes were never evicted —
// lifecycle only warmed new ones): after a differential update replaces or
// removes tools, entries keyed by the dead hashes are evicted and Len matches
// the live indexed tool count.
func TestApplyDifferentialToolUpdate_EvictsStaleSignatureCacheEntries(t *testing.T) {
	rt := newSigWarmRuntime(t)
	ctx := context.Background()

	schemaA := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	schemaB := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`
	schemaA2 := `{"type":"object","properties":{"path":{"type":"string"},"head":{"type":"integer"}},"required":["path"]}`

	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "sig-server", []*config.ToolMetadata{
		sigWarmTool("tool_a", "evict-hash-a", schemaA, "Read a file."),
		sigWarmTool("tool_b", "evict-hash-b", schemaB, "Search things."),
	}))
	assert.Equal(t, 2, rt.SignatureCache().Len())

	// tool_a's definition changes (new hash) and tool_b is REMOVED: after the
	// update the only live hash is evict-hash-a2.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "sig-server", []*config.ToolMetadata{
		sigWarmTool("tool_a", "evict-hash-a2", schemaA2, "Read a file."),
	}))

	assert.Equal(t, 1, rt.SignatureCache().Len(),
		"cache must reconcile to the live tool set after churn (stale hashes evicted)")

	// The live entry is a pure hit; the dead ones are genuine misses again.
	before := rt.SignatureCache().CompileCount()
	rt.SignatureCache().Warm("evict-hash-a2", schemaA2, "Read a file.")
	assert.Equal(t, before, rt.SignatureCache().CompileCount(), "live hash must remain cached")
	rt.SignatureCache().Get("evict-hash-a", schemaA, "Read a file.")
	assert.Equal(t, before, rt.SignatureCache().CompileCount(),
		"stale hash must compute-through without re-memoizing (post-reconcile Get gate)")
	assert.Equal(t, 1, rt.SignatureCache().Len(), "stale Get must not repopulate the cache")
}

func TestApplyDifferentialToolUpdate_WarmsSignatureCache(t *testing.T) {
	rt := newSigWarmRuntime(t)
	ctx := context.Background()

	schemaA := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	schemaB := `{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","default":10}},"required":["query"]}`

	// Added tools: warmed once per unique hash.
	tools := []*config.ToolMetadata{
		sigWarmTool("tool_a", "sig-hash-a", schemaA, "Read a file. Extra detail."),
		sigWarmTool("tool_b", "sig-hash-b", schemaB, "Search things. Extra detail."),
	}
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "sig-server", tools))

	assert.Equal(t, int64(2), rt.SignatureCache().CompileCount(),
		"indexing 2 new tools must compile exactly 2 signatures (FR-008: at index time)")

	// Warm-proof: a probe with EMPTY schema/description returns the real
	// signature — the entry pre-existed, compiled by the indexing path.
	sigA := rt.SignatureCache().Get("sig-hash-a", "", "")
	assert.Equal(t, "(path*:str)", sigA.Sig)
	assert.Equal(t, "Read a file.", sigA.Desc)
	sigB := rt.SignatureCache().Get("sig-hash-b", "", "")
	assert.Equal(t, "(query*:str, limit:int=10)", sigB.Sig)
	assert.Equal(t, int64(2), rt.SignatureCache().CompileCount(),
		"post-index probes must be pure cache hits")

	// Modified tool (new hash): the differential-update path warms the new key.
	schemaA2 := `{"type":"object","properties":{"path":{"type":"string"},"head":{"type":"integer"}},"required":["path"]}`
	modified := []*config.ToolMetadata{
		sigWarmTool("tool_a", "sig-hash-a2", schemaA2, "Read a file. Extra detail."),
		sigWarmTool("tool_b", "sig-hash-b", schemaB, "Search things. Extra detail."),
	}
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "sig-server", modified))

	assert.Equal(t, int64(3), rt.SignatureCache().CompileCount(),
		"a modified tool (new hash) must be re-warmed; the unchanged tool must not recompile")
	sigA2 := rt.SignatureCache().Get("sig-hash-a2", "", "")
	assert.Equal(t, "(path*:str, head:int)", sigA2.Sig)
	assert.Equal(t, int64(3), rt.SignatureCache().CompileCount())
}

// Spec 102 (found by live verification of Phase 3): the cache must be warm for
// an UNCHANGED index too, not only for tools an update added or modified.
//
// Warming used to hang off the add/modify branches alone. On a restart against
// an existing index those branches are both empty — the differential update
// finds nothing to do — so the cache stayed permanently empty for the life of
// the process. That was invisible to Spec 085, whose compact retrieve_tools
// reads through a COMPILING accessor and simply paid a first-call compile. It
// is not invisible to Spec 102: the deferred direct listing reads through
// Peek, which never compiles, so every entry lost its compact signature after
// any restart — the exact wire content FR-004 promises, gone, with the
// listing still looking well-formed.
func TestApplyDifferentialToolUpdate_WarmsUnchangedToolsOnRestart(t *testing.T) {
	ctx := context.Background()
	schema := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	tools := []*config.ToolMetadata{sigWarmTool("tool_a", "restart-hash-a", schema, "Read a file.")}

	rt := newSigWarmRuntime(t)
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "sig-server", tools))
	require.Equal(t, 1, rt.SignatureCache().Len())

	// Stand in for a process restart: the index still holds the tool, but the
	// process-local cache starts empty.
	require.Equal(t, 1, rt.SignatureCache().RetainHashes(nil), "cache must start cold")
	require.Zero(t, rt.SignatureCache().Len())

	// The very same tool set is rediscovered — no adds, no modifications.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "sig-server", tools))

	assert.Equal(t, 1, rt.SignatureCache().Len(),
		"an unchanged rediscovery must still leave the cache warm, or Peek-based consumers see nothing")

	sig, ok := rt.SignatureCache().Peek("restart-hash-a")
	require.True(t, ok, "Peek must hit after an unchanged rediscovery (Spec 102 FR-005)")
	assert.Equal(t, "(path*:str)", sig.Sig)
}
