package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

func TestUpstreamServersHandlerPerformance(t *testing.T) {
	// Create temporary directory for test
	tempDir := t.TempDir()

	// Create test config
	cfg := config.DefaultConfig()
	cfg.DataDir = tempDir
	cfg.ReadOnlyMode = false
	cfg.DisableManagement = false
	cfg.AllowServerAdd = true

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create storage manager
	storageManager, err := storage.NewManager(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create storage manager: %v", err)
	}
	defer storageManager.Close()

	// Create index manager
	indexManager, err := index.NewManager(tempDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create index manager: %v", err)
	}
	defer indexManager.Close()

	// Create upstream manager
	upstreamManager := upstream.NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)

	// Create cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer cacheManager.Close()

	// Create truncator
	truncator := truncate.NewTruncator(20000)

	// Create MCP proxy server
	mcpProxy := NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		func() *truncate.Truncator { return truncator },
		zap.NewNop(),
		nil, // mainServer not needed for this test
		false,
		cfg,
		nil, // signature cache: standalone test construction
	)

	// Test adding a problematic upstream server (the one that was hanging)
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "upstream_servers",
			Arguments: map[string]interface{}{
				"operation": "add",
				"name":      "searx-kevinwatt",
				"command":   "npx",
				"args":      []interface{}{"-y", "@kevinwatt/mcp-server-searxng"},
				"env":       map[string]interface{}{"SEARXNG_INSTANCES": "https://searx.mxchange.org/"},
				"enabled":   false, // Disabled for performance test to avoid connection monitoring delays
			},
		},
	}

	// Measure execution time
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := mcpProxy.handleUpstreamServers(ctx, request)
	duration := time.Since(start)

	// Assertions
	if err != nil {
		t.Fatalf("handleUpstreamServers returned error: %v", err)
	}

	require.NotNil(t, result, "handleUpstreamServers returned nil result")

	// The handler should respond quickly (within 1 second)
	if duration > time.Second {
		t.Fatalf("handleUpstreamServers took too long: %v (should be < 1s)", duration)
	}

	t.Logf("handleUpstreamServers completed in %v", duration)

	// Verify the result contains expected fields
	require.NotEmpty(t, result.Content, "Result should contain content")

	// The result should indicate success without hanging
	t.Logf("Result: %+v", result.Content[0])
}

func TestUpstreamServersListOperation(t *testing.T) {
	// Create temporary directory for test
	tempDir := t.TempDir()

	// Create test config
	cfg := config.DefaultConfig()
	cfg.DataDir = tempDir

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create storage manager
	storageManager, err := storage.NewManager(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create storage manager: %v", err)
	}
	defer storageManager.Close()

	// Create index manager
	indexManager, err := index.NewManager(tempDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create index manager: %v", err)
	}
	defer indexManager.Close()

	// Create upstream manager
	upstreamManager := upstream.NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)

	// Create cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer cacheManager.Close()

	// Create truncator
	truncator := truncate.NewTruncator(20000)

	// Create MCP proxy server
	mcpProxy := NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		func() *truncate.Truncator { return truncator },
		zap.NewNop(),
		nil, // mainServer not needed for this test
		false,
		cfg,
		nil, // signature cache: standalone test construction
	)

	// Test listing upstream servers
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "upstream_servers",
			Arguments: map[string]interface{}{
				"operation": "list",
			},
		},
	}

	// Measure execution time
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := mcpProxy.handleUpstreamServers(ctx, request)
	duration := time.Since(start)

	// Assertions
	if err != nil {
		t.Fatalf("handleUpstreamServers returned error: %v", err)
	}

	if result == nil {
		t.Fatal("handleUpstreamServers returned nil result")
	}

	// Should be fast for a list operation — see upstreamServersListCeiling's
	// doc comment for why this is a generous ceiling rather than a tight
	// latency assertion.
	if duration > upstreamServersListCeiling {
		t.Fatalf("handleUpstreamServers list took too long: %v (should be < %v)", duration, upstreamServersListCeiling)
	}

	t.Logf("handleUpstreamServers list completed in %v", duration)
}

// upstreamServersListCeiling is a ceiling with real headroom, not the
// observed budget for the `list` operation itself. It exists to catch an
// architectural regression (e.g. `list` starting to make an upstream call or
// a full index scan) rather than to pin the handler to a specific latency —
// the previous 100ms bound had no slack on shared/loaded CI runners and was
// observed failing at 157ms on macOS/windows-latest while ubuntu-latest
// passed on the identical commit (PR #1296), matching this repo's documented
// history of timing-sensitive unit tests flaking on noisier macOS/Windows
// GitHub Actions runners (see the ceiling comment on preflightBenchPerOpCeiling
// in internal/httpapi/preflight_bench_test.go for the same pattern).
const upstreamServersListCeiling = 500 * time.Millisecond
