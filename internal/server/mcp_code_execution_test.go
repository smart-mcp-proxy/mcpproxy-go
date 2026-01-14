package server_test

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"

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

// TestCodeExecution_WithMainServer tests that code execution still works with mainServer (normal mode)
func TestCodeExecution_WithMainServer(t *testing.T) {
	// This test would require mocking the mainServer interface
	// For now, we skip it as the existing integration tests cover this case
	t.Skip("Covered by existing integration tests")
}
