package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// toonModes is every toon_output value under which the out-of-scope surfaces
// must behave byte-identically (spec 084 T031, FR-013/FR-014).
var toonModes = []string{"off", "adaptive", "always"}

// installToonEncodeRecorder swaps the toonEncodeBlock seam for a delegating
// recorder so a test can assert the encoder is NEVER invoked on a surface.
// Restored on cleanup.
func installToonEncodeRecorder(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := toonEncodeBlock
	toonEncodeBlock = func(text string, mode toonenc.Mode, pct, budget int) (string, toonenc.Decision) {
		calls++
		return orig(text, mode, pct, budget)
	}
	t.Cleanup(func() { toonEncodeBlock = orig })
	return &calls
}

// renderResult flattens a CallToolResult's content into a comparable string.
func renderResult(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, r)
	out := ""
	for _, c := range r.Content {
		tc, ok := c.(mcp.TextContent)
		require.True(t, ok, "expected text content, got %T", c)
		out += tc.Text + "\x00"
	}
	return out
}

// TestSurfaceIsolation_RetrieveTools (FR-013): retrieve_tools responses are
// byte-identical under every toon_output value and never touch the encoder.
func TestSurfaceIsolation_RetrieveTools(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedDiscoveryFixture(t, proxy)
	calls := installToonEncodeRecorder(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "repo issue helper", "limit": float64(10)}

	outputs := map[string]string{}
	for _, mode := range toonModes {
		proxy.config.ToonOutput = mode
		result, err := proxy.handleRetrieveTools(context.Background(), req)
		require.NoError(t, err, "mode=%s", mode)
		outputs[mode] = renderResult(t, result)
	}

	assert.Equal(t, outputs["off"], outputs["adaptive"], "retrieve_tools must be byte-identical in adaptive mode")
	assert.Equal(t, outputs["off"], outputs["always"], "retrieve_tools must be byte-identical in always mode")
	assert.Zero(t, *calls, "retrieve_tools must never invoke the TOON encoder")
}

// TestSurfaceIsolation_CodeExecution (FR-014): the code_execution result
// envelope is byte-identical under every toon_output value — its JSON-object
// output would otherwise be a prime always-mode target — and the encoder is
// never invoked.
func TestSurfaceIsolation_CodeExecution(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.EnableCodeExecution = true
	cfg.CodeExecutionPoolSize = 1
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

	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return truncate.NewTruncator(0) }, logger, nil, false, cfg, nil)
	t.Cleanup(func() { proxy.Close() })

	calls := installToonEncodeRecorder(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"code":  "({ answer: 42, rows: [1, 2, 3] })",
		"input": map[string]interface{}{},
	}

	outputs := map[string]string{}
	for _, mode := range toonModes {
		proxy.config.ToonOutput = mode
		result, err := proxy.handleCodeExecution(context.Background(), req)
		require.NoError(t, err, "mode=%s", mode)
		outputs[mode] = renderResult(t, result)
	}

	assert.Equal(t, outputs["off"], outputs["adaptive"], "code_execution must be byte-identical in adaptive mode")
	assert.Equal(t, outputs["off"], outputs["always"], "code_execution must be byte-identical in always mode")
	assert.Zero(t, *calls, "code_execution must never invoke the TOON encoder")
}

// TestSurfaceIsolation_DirectMode (FR-014): the direct-mode handler never
// invokes the encoder and renders identically under every toon_output value.
// Exercised through the handler's reachable-without-upstream path (a
// deterministic error result); live-upstream byte-identity is E2E scope.
func TestSurfaceIsolation_DirectMode(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.Servers = []*config.ServerConfig{}

	sm, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { sm.Close() })

	um := upstream.NewManager(logger, cfg, sm.GetBoltDB(), secret.NewResolver(), sm)

	proxy := &MCPProxyServer{
		config:          cfg,
		logger:          logger,
		storage:         sm,
		upstreamManager: um,
		sessionStore:    NewSessionStore(logger),
	}

	calls := installToonEncodeRecorder(t)
	handler := proxy.makeDirectModeHandler("srv", "list_things", nil)

	req := mcp.CallToolRequest{}
	req.Params.Name = "srv__list_things"

	outputs := map[string]string{}
	for _, mode := range toonModes {
		proxy.config.ToonOutput = mode
		result, err := handler(context.Background(), req)
		require.NoError(t, err, "mode=%s", mode)
		outputs[mode] = renderResult(t, result)
	}

	assert.Equal(t, outputs["off"], outputs["adaptive"], "direct mode must be byte-identical in adaptive mode")
	assert.Equal(t, outputs["off"], outputs["always"], "direct mode must be byte-identical in always mode")
	assert.Zero(t, *calls, "direct mode must never invoke the TOON encoder")
}
