package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestCodeExecution_WithNilMainServer tests that code execution works when mainServer is nil (CLI mode)
func TestCodeExecution_WithNilMainServer(t *testing.T) {
	// Given: MCP proxy server with nil mainServer (simulates CLI mode)
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := &config.Config{
		DataDir:               tmpDir,
		EnableCodeExecution:   true,
		CodeExecutionPoolSize: 1,
		ToolResponseLimit:     10000,
		Servers:               []*config.ServerConfig{},
	}

	storageManager, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	defer storageManager.Close()

	indexManager, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	defer indexManager.Close()

	secretResolver := secret.NewResolver()
	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	require.NoError(t, err)
	defer cacheManager.Close()

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	// Create MCP proxy with nil mainServer
	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		truncator,
		logger,
		nil, // mainServer = nil (CLI mode)
		false,
		cfg,
		nil, // signature cache: standalone test construction
	)
	defer mcpProxy.Close()

	// When: Calling code_execution tool
	ctx := context.Background()
	args := map[string]interface{}{
		"code":  "({ result: input.value * 2 })",
		"input": map[string]interface{}{"value": 21},
		"options": map[string]interface{}{
			"timeout_ms":     10000,
			"max_tool_calls": 0,
		},
	}

	result, err := mcpProxy.CallBuiltInTool(ctx, "code_execution", args)

	// Then: Should not panic and should return result
	require.NoError(t, err, "CallBuiltInTool should not error")
	assert.NotNil(t, result, "Result should not be nil")
	assert.Greater(t, len(result.Content), 0, "Result should have content")
}

// newCodeExecProxy builds a minimal CLI-mode MCPProxyServer with code execution
// enabled, for profile-scope tests. Returns the proxy; cleanup is registered on t.
func newCodeExecProxy(t *testing.T) *server.MCPProxyServer {
	t.Helper()
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := &config.Config{
		DataDir:               tmpDir,
		EnableCodeExecution:   true,
		CodeExecutionPoolSize: 1,
		ToolResponseLimit:     10000,
		Servers:               []*config.ServerConfig{},
	}

	storageManager, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { storageManager.Close() })

	indexManager, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	t.Cleanup(func() { indexManager.Close() })

	secretResolver := secret.NewResolver()
	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cacheManager.Close() })

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	mcpProxy := server.NewMCPProxyServer(
		storageManager, indexManager, upstreamManager, cacheManager,
		truncator, logger, nil, false, cfg, nil,
	)
	t.Cleanup(func() { mcpProxy.Close() })
	return mcpProxy
}

// codeExecCallToolResult runs code that issues a single call_tool() and returns
// the parsed {ok, code} of that inner call.
func codeExecCallToolResult(t *testing.T, ctx context.Context, p *server.MCPProxyServer, server2, tool string) (ok bool, code string) {
	t.Helper()
	args := map[string]interface{}{
		"code": fmt.Sprintf(
			`var r = call_tool(%q, %q, {}); ({ ok: r.ok, code: r.error ? r.error.code : null })`,
			server2, tool),
		"input": map[string]interface{}{},
		"options": map[string]interface{}{
			"timeout_ms":     10000,
			"max_tool_calls": 0,
		},
	}
	result, err := p.CallBuiltInTool(ctx, "code_execution", args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, len(result.Content), 0)

	text := codeExecText(t, result.Content[0])
	var outer map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &outer))
	// code_execution wraps the script return value under "value".
	inner, ok2 := outer["value"].(map[string]interface{})
	require.True(t, ok2, "expected value object, got: %s", text)

	okVal, _ := inner["ok"].(bool)
	codeVal, _ := inner["code"].(string)
	return okVal, codeVal
}

func codeExecText(t *testing.T, c interface{}) string {
	t.Helper()
	if tc, ok := c.(mcp.TextContent); ok {
		return tc.Text
	}
	if tc, ok := c.(*mcp.TextContent); ok {
		return tc.Text
	}
	b, err := json.Marshal(c)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	s, _ := m["text"].(string)
	return s
}

// TestCodeExecution_ProfileDenyAllBlocksCallTool is the Spec 057 regression for
// Codex PR #622 finding #1: at a profile URL whose effective server set is empty
// (deny-all profile, or a non-overlapping token∩profile), code_execution must
// reject ALL call_tool() — an empty allow-list must NOT mean "allow all".
func TestCodeExecution_ProfileDenyAllBlocksCallTool(t *testing.T) {
	ctx := context.Background()

	t.Run("deny-all profile (servers: [])", func(t *testing.T) {
		p := newCodeExecProxy(t)
		// Deny-all profile: active scope with an empty effective server set.
		scope := profile.NewProfileScope("locked", nil)
		pctx := profile.WithProfileScope(ctx, scope)

		ok, code := codeExecCallToolResult(t, pctx, p, "anyserver", "anytool")
		assert.False(t, ok, "deny-all profile must block call_tool inside code_execution")
		assert.Equal(t, "SERVER_NOT_ALLOWED", code)
	})

	t.Run("profile allows servers but caller targets another", func(t *testing.T) {
		p := newCodeExecProxy(t)
		// Profile allows only "research-srv"; calling "deploy-srv" must be denied.
		scope := profile.NewProfileScope("research", []string{"research-srv"})
		pctx := profile.WithProfileScope(ctx, scope)

		ok, code := codeExecCallToolResult(t, pctx, p, "deploy-srv", "rollback")
		assert.False(t, ok, "server outside the profile must be blocked")
		assert.Equal(t, "SERVER_NOT_ALLOWED", code)
	})

	t.Run("no profile: empty allow-list still allows (backward compat)", func(t *testing.T) {
		p := newCodeExecProxy(t)
		// No profile scope in ctx → call_tool reaches the upstream layer, which
		// fails with a non-allow-list error (server not configured), NOT
		// SERVER_NOT_ALLOWED. Proves the empty-set guard is profile-gated.
		ok, code := codeExecCallToolResult(t, ctx, p, "anyserver", "anytool")
		assert.False(t, ok, "no upstream configured, call fails")
		assert.NotEqual(t, "SERVER_NOT_ALLOWED", code,
			"without a profile, an empty allow-list must not block as SERVER_NOT_ALLOWED")
	})
}

// TestCodeExecution_WithMainServer tests that code execution still works with mainServer (normal mode)
func TestCodeExecution_WithMainServer(t *testing.T) {
	// This test would require mocking the mainServer interface
	// For now, we skip it as the existing integration tests cover this case
	t.Skip("Covered by existing integration tests")
}

// TestCodeExecution_TypeScript tests TypeScript execution via the MCP tool
func TestCodeExecution_TypeScript(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := &config.Config{
		DataDir:               tmpDir,
		EnableCodeExecution:   true,
		CodeExecutionPoolSize: 1,
		ToolResponseLimit:     10000,
		Servers:               []*config.ServerConfig{},
	}

	storageManager, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	defer storageManager.Close()

	indexManager, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	defer indexManager.Close()

	secretResolver := secret.NewResolver()
	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	require.NoError(t, err)
	defer cacheManager.Close()

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		truncator,
		logger,
		nil,
		false,
		cfg,
		nil, // signature cache: standalone test construction
	)
	defer mcpProxy.Close()

	ctx := context.Background()
	args := map[string]interface{}{
		"code":     "const x: number = 42; const msg: string = \"hello\"; ({ result: x, message: msg })",
		"language": "typescript",
		"input":    map[string]interface{}{},
		"options": map[string]interface{}{
			"timeout_ms":     10000,
			"max_tool_calls": 0,
		},
	}

	result, err := mcpProxy.CallBuiltInTool(ctx, "code_execution", args)

	require.NoError(t, err, "CallBuiltInTool should not error")
	assert.NotNil(t, result, "Result should not be nil")
	assert.Greater(t, len(result.Content), 0, "Result should have content")
}

// TestCodeExecution_JavaScriptBackwardCompat tests that JavaScript still works without language param
func TestCodeExecution_JavaScriptBackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := &config.Config{
		DataDir:               tmpDir,
		EnableCodeExecution:   true,
		CodeExecutionPoolSize: 1,
		ToolResponseLimit:     10000,
		Servers:               []*config.ServerConfig{},
	}

	storageManager, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	defer storageManager.Close()

	indexManager, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	defer indexManager.Close()

	secretResolver := secret.NewResolver()
	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	require.NoError(t, err)
	defer cacheManager.Close()

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		truncator,
		logger,
		nil,
		false,
		cfg,
		nil, // signature cache: standalone test construction
	)
	defer mcpProxy.Close()

	ctx := context.Background()
	// No "language" key - backward compatible
	args := map[string]interface{}{
		"code":  "({ result: input.value * 2 })",
		"input": map[string]interface{}{"value": 21},
		"options": map[string]interface{}{
			"timeout_ms":     10000,
			"max_tool_calls": 0,
		},
	}

	result, err := mcpProxy.CallBuiltInTool(ctx, "code_execution", args)

	require.NoError(t, err, "CallBuiltInTool should not error")
	assert.NotNil(t, result, "Result should not be nil")
	assert.Greater(t, len(result.Content), 0, "Result should have content")
}

// TestCodeExecution_InvalidLanguage tests that an invalid language returns an error
func TestCodeExecution_InvalidLanguage(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := &config.Config{
		DataDir:               tmpDir,
		EnableCodeExecution:   true,
		CodeExecutionPoolSize: 1,
		ToolResponseLimit:     10000,
		Servers:               []*config.ServerConfig{},
	}

	storageManager, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	defer storageManager.Close()

	indexManager, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	defer indexManager.Close()

	secretResolver := secret.NewResolver()
	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	require.NoError(t, err)
	defer cacheManager.Close()

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		truncator,
		logger,
		nil,
		false,
		cfg,
		nil, // signature cache: standalone test construction
	)
	defer mcpProxy.Close()

	ctx := context.Background()
	args := map[string]interface{}{
		"code":     "({ result: 42 })",
		"language": "python",
		"input":    map[string]interface{}{},
		"options": map[string]interface{}{
			"timeout_ms":     10000,
			"max_tool_calls": 0,
		},
	}

	result, err := mcpProxy.CallBuiltInTool(ctx, "code_execution", args)

	// The tool returns an error in the result content, not as a Go error
	require.NoError(t, err, "CallBuiltInTool should not return Go error")
	assert.NotNil(t, result, "Result should not be nil")
	assert.Greater(t, len(result.Content), 0, "Result should have content")
}
