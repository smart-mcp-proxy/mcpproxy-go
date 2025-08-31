package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
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
	upstreamManager := upstream.NewManager(zap.NewNop(), cfg)

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
		truncator,
		zap.NewNop(),
		nil, // mainServer not needed for this test
		false,
		cfg,
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

	if result == nil {
		t.Fatal("handleUpstreamServers returned nil result")
	}

	// The handler should respond quickly (within 1 second)
	if duration > time.Second {
		t.Fatalf("handleUpstreamServers took too long: %v (should be < 1s)", duration)
	}

	t.Logf("handleUpstreamServers completed in %v", duration)

	// Verify the result contains expected fields
	if len(result.Content) == 0 {
		t.Fatal("Result should contain content")
	}

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
	upstreamManager := upstream.NewManager(zap.NewNop(), cfg)

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
		truncator,
		zap.NewNop(),
		nil, // mainServer not needed for this test
		false,
		cfg,
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

	// Should be very fast for list operation
	if duration > 100*time.Millisecond {
		t.Fatalf("handleUpstreamServers list took too long: %v (should be < 100ms)", duration)
	}

	t.Logf("handleUpstreamServers list completed in %v", duration)
}
