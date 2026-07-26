package server

import (
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setStaticTruncator wires a fixed truncator for tests that exercise the
// serving path with a specific limit. The value is captured eagerly so later
// reassignment of the source expression does not affect it.
func (p *MCPProxyServer) setStaticTruncator(tr *truncate.Truncator) {
	p.truncatorFn = func() *truncate.Truncator { return tr }
}

// TestTruncatorHotReloadOnServingPath (#861 defect 2): the MCP request handler
// must resolve the truncator through the injected getter at use time, not
// capture a pointer once at construction. When a config reload swaps the
// runtime truncator, the handler's next tool call must observe the new limit.
//
// With the old captured-pointer wiring, currentTruncator() would keep returning
// the truncator handed to the constructor even after the runtime swapped it, so
// the pre-swap assertion below would still hold after the swap and this test
// would fail.
func TestTruncatorHotReloadOnServingPath(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.Servers = []*config.ServerConfig{}

	sm, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { sm.Close() })

	idx, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	t.Cleanup(func() { idx.Close() })

	um := upstream.NewManager(logger, cfg, sm.GetBoltDB(), secret.NewResolver(), sm)

	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	// Live truncator holder, swapped to simulate a config reload. The handler
	// must read through the getter so it observes the swap.
	var live atomic.Pointer[truncate.Truncator]
	live.Store(truncate.NewTruncator(100)) // limit 100

	proxy := NewMCPProxyServer(sm, idx, um, cm, live.Load, logger, nil, false, cfg, nil)
	t.Cleanup(func() { proxy.Close() })

	// An 11-char payload: under limit 100 it should NOT truncate.
	payload := "12345678901"
	require.Len(t, payload, 11)

	tr := proxy.currentTruncator()
	require.NotNil(t, tr)
	assert.False(t, tr.ShouldTruncate(payload), "limit 100 must not truncate an 11-char payload")

	// Simulate a hot reload lowering the limit to 10.
	live.Store(truncate.NewTruncator(10))

	tr = proxy.currentTruncator()
	require.NotNil(t, tr)
	assert.True(t, tr.ShouldTruncate(payload),
		"after reload to limit 10 the serving path must observe the new limit")
}
